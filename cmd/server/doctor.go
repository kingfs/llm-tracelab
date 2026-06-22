package main

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/websearch"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/spf13/cobra"
)

const (
	doctorStatusPass = "pass"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
)

type doctorOptions struct {
	configPath     string
	format         string
	stdout         io.Writer
	checkDB        bool
	probeProviders bool
	failOnWarn     bool
	failOnFail     bool
}

type doctorResult struct {
	Status  string              `json:"status"`
	Summary doctorSummary       `json:"summary"`
	Config  doctorConfigSummary `json:"config"`
	Checks  []doctorCheck       `json:"checks"`
}

type doctorSummary struct {
	Pass  int `json:"pass"`
	Warn  int `json:"warn"`
	Fail  int `json:"fail"`
	Total int `json:"total"`
}

type doctorConfigSummary struct {
	ConfigPath      string                     `json:"config_path"`
	Server          configInspectServer        `json:"server"`
	Monitor         configInspectMonitor       `json:"monitor"`
	MCP             configInspectMCP           `json:"mcp"`
	Database        configInspectDatabase      `json:"database"`
	Trace           configInspectTrace         `json:"trace"`
	ResponsesServer configInspectResponses     `json:"responses_server"`
	Tools           configInspectTools         `json:"tools"`
	ProviderProbe   configInspectProviderProbe `json:"provider_probe"`
	Upstreams       doctorUpstreamsSummary     `json:"upstreams"`
}

type doctorUpstreamsSummary struct {
	Targets        int `json:"targets"`
	EnabledTargets int `json:"enabled_targets"`
	Credentials    int `json:"credentials"`
	StaticModels   int `json:"static_models"`
}

type doctorCheck struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

func newDoctorCommand(runtime *cliRuntime) *cobra.Command {
	var checkDB bool
	var probeProviders bool
	var failOnWarn bool
	var failOnFail bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run offline startup diagnostics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runDoctorWithOptions(doctorOptions{
					configPath:     runtime.configPath(),
					format:         runtime.outputFormat(),
					stdout:         cmd.OutOrStdout(),
					checkDB:        checkDB,
					probeProviders: probeProviders,
					failOnWarn:     failOnWarn,
					failOnFail:     failOnFail,
				})
			})
		},
	}
	cmd.Flags().BoolVar(&checkDB, "check-db", false, "Read migration status from the configured database")
	cmd.Flags().BoolVar(&probeProviders, "probe-providers", false, "Probe configured provider endpoints")
	cmd.Flags().BoolVar(&failOnWarn, "fail-on-warn", false, "Exit non-zero when diagnostics contain warnings")
	cmd.Flags().BoolVar(&failOnFail, "fail-on-fail", true, "Exit non-zero when diagnostics contain failures")
	return cmd
}

func runDoctorWithOptions(opts doctorOptions) int {
	result := buildDoctorResult(opts)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "doctor", result, func(w io.Writer) error {
		writeDoctorText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write doctor result failed", "error", err)
		return 1
	}
	if result.Status == doctorStatusFail && opts.failOnFail {
		return 1
	}
	if result.Status == doctorStatusWarn && opts.failOnWarn {
		return 1
	}
	return 0
}

func buildDoctorResult(opts doctorOptions) doctorResult {
	result := doctorResult{
		Status: doctorStatusPass,
		Config: doctorConfigSummary{
			ConfigPath: opts.configPath,
		},
	}
	cfg, err := appconfig.Load(opts.configPath)
	if err != nil {
		result.Checks = append(result.Checks, doctorCheck{
			Name:    "config.load",
			Status:  doctorStatusFail,
			Message: "configuration could not be loaded",
			Detail:  map[string]any{"error": err.Error()},
		})
		finalizeDoctorResult(&result)
		return result
	}

	result.Config = buildDoctorConfigSummary(opts.configPath, cfg)
	result.Checks = append(result.Checks, doctorCheck{
		Name:    "config.load",
		Status:  doctorStatusPass,
		Message: "configuration loaded",
	})
	result.Checks = append(result.Checks, checkDoctorServerPort(cfg))
	result.Checks = append(result.Checks, checkDoctorManagementConsistency(cfg))
	result.Checks = append(result.Checks, checkDoctorDatabaseMigration(cfg, opts.checkDB))
	result.Checks = append(result.Checks, checkDoctorResponsesServerBackend(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesDefaultModel(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesStoreReadiness(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesModelProfiles(cfg))
	result.Checks = append(result.Checks, checkDoctorWebSearch(cfg))
	result.Checks = append(result.Checks, checkDoctorProviderConfig(cfg))
	result.Checks = append(result.Checks, checkDoctorAuthMigrationScope())
	result.Checks = append(result.Checks, checkDoctorProviderProbe(opts.probeProviders))
	finalizeDoctorResult(&result)
	return result
}

func buildDoctorConfigSummary(configPath string, cfg *appconfig.Config) doctorConfigSummary {
	inspect := buildConfigInspectResult(configPath, cfg)
	summary := doctorConfigSummary{
		ConfigPath:      inspect.ConfigPath,
		Server:          inspect.Server,
		Monitor:         inspect.Monitor,
		MCP:             inspect.MCP,
		Database:        inspect.Database,
		Trace:           inspect.Trace,
		ResponsesServer: inspect.ResponsesServer,
		Tools:           inspect.Tools,
		ProviderProbe:   inspect.ProviderProbe,
	}
	for _, target := range cfg.EffectiveUpstreams() {
		summary.Upstreams.Targets++
		enabled := true
		if target.Enabled != nil {
			enabled = *target.Enabled
		}
		if enabled {
			summary.Upstreams.EnabledTargets++
		}
		summary.Upstreams.Credentials += len(target.EffectiveCredentials())
		summary.Upstreams.StaticModels += len(target.StaticModels)
	}
	return summary
}

func checkDoctorServerPort(cfg *appconfig.Config) doctorCheck {
	port := strings.TrimSpace(cfg.Server.Port)
	if port == "" {
		return doctorCheck{Name: "server.port", Status: doctorStatusWarn, Message: "server.port is empty; serve will listen on ':'"}
	}
	value, err := strconv.Atoi(port)
	if err != nil || value <= 0 || value > 65535 {
		return doctorCheck{
			Name:    "server.port",
			Status:  doctorStatusFail,
			Message: "server.port must be a TCP port number between 1 and 65535",
			Detail:  map[string]any{"port": port},
		}
	}
	return doctorCheck{Name: "server.port", Status: doctorStatusPass, Message: "server port is valid", Detail: map[string]any{"port": port}}
}

func checkDoctorManagementConsistency(cfg *appconfig.Config) doctorCheck {
	if err := validateServeConfig(cfg); err != nil {
		return doctorCheck{
			Name:    "monitor_mcp.consistency",
			Status:  doctorStatusFail,
			Message: "monitor and MCP settings are inconsistent",
			Detail:  map[string]any{"error": err.Error()},
		}
	}
	return doctorCheck{
		Name:    "monitor_mcp.consistency",
		Status:  doctorStatusPass,
		Message: "monitor and MCP settings are consistent",
		Detail: map[string]any{
			"monitor_port": strings.TrimSpace(cfg.Monitor.Port),
			"mcp_enabled":  cfg.MCP.Enabled,
			"mcp_path":     configInspectMCPPath(cfg),
		},
	}
}

func checkDoctorDatabaseMigration(cfg *appconfig.Config, checkDB bool) doctorCheck {
	report := appDBMigrationReport(cfg, "status", 0, false, false, false, checkDB)
	report["dsn"] = redactConfigInspectDSN(cfg.DatabaseDSN())
	if checkDB {
		if err := applyAppDBStatusCheck(cfg, report); err != nil {
			report["error"] = redactDoctorMessage(err.Error())
			return doctorCheck{
				Name:    "database.migration",
				Status:  doctorStatusFail,
				Message: "database migration status check failed",
				Detail:  report,
			}
		}
	}
	return doctorCheck{
		Name:    "database.migration",
		Status:  doctorStatusPass,
		Message: "database migration mode reported",
		Detail:  report,
	}
}

func checkDoctorResponsesServerBackend(cfg *appconfig.Config) doctorCheck {
	if !cfg.ResponsesServerEnabled() {
		return doctorCheck{Name: "responses_server.backend", Status: doctorStatusPass, Message: "responses server is disabled"}
	}
	if err := router.ValidateLocalResponsesServerBackendConfig(cfg); err != nil {
		return doctorCheck{
			Name:    "responses_server.backend",
			Status:  doctorStatusFail,
			Message: "responses server requires an enabled OpenAI-compatible chat-completions backend",
			Detail: map[string]any{
				"error":                err.Error(),
				"router_config_source": "config-file-only",
			},
		}
	}
	return doctorCheck{
		Name:    "responses_server.backend",
		Status:  doctorStatusPass,
		Message: "responses server backend is valid",
		Detail:  map[string]any{"router_config_source": "config-file-only"},
	}
}

func checkDoctorResponsesDefaultModel(cfg *appconfig.Config) doctorCheck {
	detail := map[string]any{
		"enabled":       cfg.ResponsesServerEnabled(),
		"default_model": cfg.ResponsesDefaultModel(),
	}
	if !cfg.ResponsesServerEnabled() {
		return doctorCheck{Name: "responses_server.default_model", Status: doctorStatusPass, Message: "responses server is disabled", Detail: detail}
	}
	if cfg.ResponsesDefaultModel() == "" {
		return doctorCheck{
			Name:    "responses_server.default_model",
			Status:  doctorStatusWarn,
			Message: "responses server is enabled but responses_server.default_model is empty",
			Detail:  detail,
		}
	}
	return doctorCheck{Name: "responses_server.default_model", Status: doctorStatusPass, Message: "responses default model is configured", Detail: detail}
}

func checkDoctorResponsesStoreReadiness(cfg *appconfig.Config) doctorCheck {
	effectiveDriver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	detail := map[string]any{
		"enabled":               cfg.ResponsesServerEnabled(),
		"force_store":           cfg.ResponsesForceStore(),
		"database_driver":       effectiveDriver,
		"database_driver_raw":   strings.TrimSpace(cfg.Database.Driver),
		"database_dsn":          redactConfigInspectDSN(cfg.DatabaseDSN()),
		"database_auto_migrate": cfg.DatabaseAutoMigrate(),
		"migration_mode":        appDBMigrationMode(effectiveDriver),
		"status_check":          "configuration-only",
	}
	if !cfg.ResponsesServerEnabled() {
		return doctorCheck{Name: "responses_server.store", Status: doctorStatusPass, Message: "responses server is disabled", Detail: detail}
	}
	switch effectiveDriver {
	case "sqlite":
		return checkDoctorResponsesStoreAutoMigrate(cfg, detail)
	case "postgres":
		if strings.TrimSpace(cfg.DatabaseDSN()) == "" {
			return doctorCheck{
				Name:    "responses_server.store",
				Status:  doctorStatusFail,
				Message: "responses server postgres store requires database.dsn",
				Detail:  detail,
			}
		}
		return checkDoctorResponsesStoreAutoMigrate(cfg, detail)
	default:
		return doctorCheck{
			Name:    "responses_server.store",
			Status:  doctorStatusFail,
			Message: "responses server store database driver is unsupported",
			Detail:  detail,
		}
	}
}

func checkDoctorResponsesStoreAutoMigrate(cfg *appconfig.Config, detail map[string]any) doctorCheck {
	if cfg.ResponsesForceStore() && !cfg.DatabaseAutoMigrate() {
		return doctorCheck{
			Name:    "responses_server.store",
			Status:  doctorStatusWarn,
			Message: "responses server force_store is enabled and database auto_migrate is disabled; ensure application migrations are applied before serving",
			Detail:  detail,
		}
	}
	return doctorCheck{Name: "responses_server.store", Status: doctorStatusPass, Message: "responses server store configuration is ready for offline startup", Detail: detail}
}

func checkDoctorResponsesModelProfiles(cfg *appconfig.Config) doctorCheck {
	profiles := cfg.ResponsesModelProfiles()
	detail := map[string]any{
		"enabled":                               cfg.ResponsesServerEnabled(),
		"auto_compact":                          cfg.ResponsesAutoCompactEnabled(),
		"compact_history_item_threshold":        cfg.ResponsesCompactHistoryItemThreshold(),
		"compact_history_item_threshold_config": cfg.ResponsesServer.CompactHistoryItemThreshold,
		"model_profiles":                        len(profiles),
	}
	if !cfg.ResponsesServerEnabled() {
		return doctorCheck{Name: "responses_server.model_profiles", Status: doctorStatusPass, Message: "responses server is disabled", Detail: detail}
	}
	issues := checkDoctorResponsesModelProfileIssues(cfg, profiles)
	if len(issues.failures) > 0 {
		detail["failures"] = issues.failures
		if len(issues.warnings) > 0 {
			detail["warnings"] = issues.warnings
		}
		return doctorCheck{Name: "responses_server.model_profiles", Status: doctorStatusFail, Message: "responses server model profile numeric relationships are invalid", Detail: detail}
	}
	if len(issues.warnings) > 0 {
		detail["warnings"] = issues.warnings
		return doctorCheck{Name: "responses_server.model_profiles", Status: doctorStatusWarn, Message: "responses server model profiles have offline diagnostic warnings", Detail: detail}
	}
	return doctorCheck{Name: "responses_server.model_profiles", Status: doctorStatusPass, Message: "responses server model profile thresholds are valid", Detail: detail}
}

type doctorResponsesModelProfileIssues struct {
	failures []string
	warnings []string
}

func checkDoctorResponsesModelProfileIssues(cfg *appconfig.Config, profiles []appconfig.ResponsesModelProfileConfig) doctorResponsesModelProfileIssues {
	var issues doctorResponsesModelProfileIssues
	if cfg.ResponsesServer.CompactHistoryItemThreshold < 0 {
		issues.failures = append(issues.failures, "responses_server.compact_history_item_threshold must be zero or positive")
	}
	hasCompactionBudget := cfg.ResponsesServer.CompactHistoryItemThreshold > 0
	for idx, profile := range profiles {
		label := doctorResponsesModelProfileLabel(idx, profile)
		if profile.Name == "" && profile.Pattern == "" {
			issues.warnings = append(issues.warnings, label+" has neither name nor pattern and will not match models")
		}
		if profile.ContextWindowTokens < 0 {
			issues.failures = append(issues.failures, label+" context_window_tokens must be zero or positive")
		}
		if profile.MaxOutputTokens < 0 {
			issues.failures = append(issues.failures, label+" max_output_tokens must be zero or positive")
		}
		if profile.CompactHistoryItemThreshold < 0 {
			issues.failures = append(issues.failures, label+" compact_history_item_threshold must be zero or positive")
		}
		if profile.ContextWindowTokens > 0 {
			hasCompactionBudget = true
		}
		if profile.CompactHistoryItemThreshold > 0 {
			hasCompactionBudget = true
		}
		if profile.ContextWindowTokens > 0 && profile.MaxOutputTokens > 0 && profile.MaxOutputTokens >= profile.ContextWindowTokens {
			issues.failures = append(issues.failures, label+" max_output_tokens must be less than context_window_tokens")
		}
		if profile.ContextWindowTokens <= 0 && profile.MaxOutputTokens > 0 {
			issues.warnings = append(issues.warnings, label+" max_output_tokens is set without context_window_tokens")
		}
	}
	if cfg.ResponsesAutoCompactEnabled() && !hasCompactionBudget {
		issues.warnings = append(issues.warnings, "responses_server.auto_compact is enabled without a global threshold, profile threshold, or profile context window")
	}
	return issues
}

func doctorResponsesModelProfileLabel(idx int, profile appconfig.ResponsesModelProfileConfig) string {
	if profile.Name != "" {
		return fmt.Sprintf("model_profiles[%d] %q", idx, profile.Name)
	}
	if profile.Pattern != "" {
		return fmt.Sprintf("model_profiles[%d] pattern %q", idx, profile.Pattern)
	}
	return fmt.Sprintf("model_profiles[%d]", idx)
}

func checkDoctorWebSearch(cfg *appconfig.Config) doctorCheck {
	webSearchConfig := cfg.WebSearchConfig()
	detail := map[string]any{
		"enabled":     cfg.WebSearchEnabled(),
		"provider":    webSearchConfig.Provider,
		"base_url":    redactURLLike(webSearchConfig.BaseURL),
		"max_results": webSearchConfig.MaxResults,
	}
	if !cfg.WebSearchEnabled() {
		return doctorCheck{Name: "web_search.provider", Status: doctorStatusPass, Message: "web search is disabled", Detail: detail}
	}
	if strings.EqualFold(webSearchConfig.Provider, websearch.ProviderDisabled) {
		return doctorCheck{Name: "web_search.provider", Status: doctorStatusFail, Message: "web search is enabled but provider is disabled", Detail: detail}
	}
	if _, err := websearch.NewProvider(websearch.Options{
		Provider:   webSearchConfig.Provider,
		BaseURL:    webSearchConfig.BaseURL,
		TimeoutMS:  webSearchConfig.TimeoutMS,
		UserAgent:  webSearchConfig.UserAgent,
		MaxResults: webSearchConfig.MaxResults,
	}); err != nil {
		detail["error"] = redactDoctorMessage(err.Error())
		return doctorCheck{Name: "web_search.provider", Status: doctorStatusFail, Message: "web search provider configuration is invalid", Detail: detail}
	}
	return doctorCheck{Name: "web_search.provider", Status: doctorStatusPass, Message: "web search provider configuration is valid", Detail: detail}
}

func checkDoctorProviderConfig(cfg *appconfig.Config) doctorCheck {
	summary := buildDoctorConfigSummary("", cfg).Upstreams
	status := doctorStatusPass
	message := "provider configuration is present"
	if summary.EnabledTargets == 0 {
		status = doctorStatusWarn
		message = "no enabled upstream targets are configured"
	}
	return doctorCheck{
		Name:    "provider_config.basic_count",
		Status:  status,
		Message: message,
		Detail: map[string]any{
			"targets":         summary.Targets,
			"enabled_targets": summary.EnabledTargets,
			"credentials":     summary.Credentials,
			"static_models":   summary.StaticModels,
		},
	}
}

func checkDoctorAuthMigrationScope() doctorCheck {
	return doctorCheck{
		Name:    "auth.migration_scope",
		Status:  doctorStatusPass,
		Message: "auth migrations are managed by the auth migrate command, outside application db migrate",
		Detail: map[string]any{
			"application_db_scope": "excluded",
			"command":              "auth migrate",
		},
	}
}

func checkDoctorProviderProbe(probeProviders bool) doctorCheck {
	if !probeProviders {
		return doctorCheck{Name: "provider_probe.network", Status: doctorStatusPass, Message: "provider network probe skipped", Detail: map[string]any{"enabled": false}}
	}
	return doctorCheck{
		Name:    "provider_probe.network",
		Status:  doctorStatusWarn,
		Message: "provider probe is requested but doctor keeps startup diagnostics offline and does not make provider network requests",
		Detail:  map[string]any{"enabled": true, "network_requests": false},
	}
}

func finalizeDoctorResult(result *doctorResult) {
	if result == nil {
		return
	}
	for _, check := range result.Checks {
		switch check.Status {
		case doctorStatusFail:
			result.Summary.Fail++
		case doctorStatusWarn:
			result.Summary.Warn++
		default:
			result.Summary.Pass++
		}
	}
	result.Summary.Total = len(result.Checks)
	switch {
	case result.Summary.Fail > 0:
		result.Status = doctorStatusFail
	case result.Summary.Warn > 0:
		result.Status = doctorStatusWarn
	default:
		result.Status = doctorStatusPass
	}
}

func writeDoctorText(w io.Writer, result doctorResult) {
	fmt.Fprintf(w, "doctor: %s\n", result.Status)
	fmt.Fprintf(w, "checks: pass=%d warn=%d fail=%d total=%d\n", result.Summary.Pass, result.Summary.Warn, result.Summary.Fail, result.Summary.Total)
	for _, check := range result.Checks {
		fmt.Fprintf(w, "- %s: %s - %s\n", check.Status, check.Name, check.Message)
	}
}

func redactDoctorMessage(message string) string {
	message = appconfig.RedactDSN(message)
	message = redactURLLike(message)
	return message
}
