package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"strconv"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/appdbmigrate"
	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/providerprobe"
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
	configPath      string
	codexConfigPath string
	format          string
	stdout          io.Writer
	checkDB         bool
	probeProviders  bool
	failOnWarn      bool
	failOnFail      bool
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

type doctorProviderProbeSummary struct {
	Total    int `json:"total"`
	Detected int `json:"detected"`
	Error    int `json:"error"`
	Unknown  int `json:"unknown"`
}

type doctorCheck struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

func newDoctorCommand(runtime *cliRuntime) *cobra.Command {
	var checkDB bool
	var codexConfigPath string
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
					configPath:      runtime.configPath(),
					codexConfigPath: codexConfigPath,
					format:          runtime.outputFormat(),
					stdout:          cmd.OutOrStdout(),
					checkDB:         checkDB,
					probeProviders:  probeProviders,
					failOnWarn:      failOnWarn,
					failOnFail:      failOnFail,
				})
			})
		},
	}
	cmd.Flags().BoolVar(&checkDB, "check-db", false, "Read migration status from the configured database")
	cmd.Flags().StringVar(&codexConfigPath, "codex-config", "", "Path to a local Codex TOML config file to diagnose for drift")
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
	result.Checks = append(result.Checks, checkDoctorResponsesHTTPGuard(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesDefaultModel(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesStoreReadiness(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesStoreHealth(cfg, opts.checkDB))
	result.Checks = append(result.Checks, checkDoctorResponsesModelProfiles(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesModelCatalogDrift(cfg))
	result.Checks = append(result.Checks, checkDoctorResponsesCodexConfigDrift(cfg, opts.codexConfigPath))
	result.Checks = append(result.Checks, checkDoctorResponsesCodexCompat(cfg))
	result.Checks = append(result.Checks, checkDoctorWebSearch(cfg))
	result.Checks = append(result.Checks, checkDoctorMCPTools(cfg))
	result.Checks = append(result.Checks, checkDoctorProviderConfig(cfg))
	result.Checks = append(result.Checks, checkDoctorAuthMigrationScope(cfg))
	result.Checks = append(result.Checks, checkDoctorProviderProbe(cfg, opts.probeProviders))
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

// checkDoctorResponsesServerBackend reports whether the local Responses
// execution mode has an OpenAI-compatible chat-completions backend to
// orchestrate into. The mode is always available and only used as a fallback
// for models without a native Responses upstream, so a missing backend is a
// warning: /v1/chat/completions and /v1/messages still work, and so does
// /v1/responses for natively served models.
func checkDoctorResponsesServerBackend(cfg *appconfig.Config) doctorCheck {
	if err := router.ValidateLocalResponsesServerBackendConfig(cfg); err != nil {
		return doctorCheck{
			Name:    "responses_server.backend",
			Status:  doctorStatusWarn,
			Message: "local Responses execution mode has no enabled OpenAI-compatible chat-completions backend; only natively served Responses models will work",
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

func checkDoctorResponsesHTTPGuard(cfg *appconfig.Config) doctorCheck {
	effectivePath := cfg.ResponsesServerPath()
	normalizedPath, pathErr := normalizeDoctorResponsesPath(effectivePath)
	detail := map[string]any{
		"responses_path":             effectivePath,
		"responses_path_configured":  strings.TrimSpace(cfg.ResponsesServer.Path) != "",
		"normalized_path":            normalizedPath,
		"max_request_body_bytes":     cfg.ResponsesMaxRequestBodyBytes(),
		"max_body_configured":        cfg.ResponsesServer.MaxRequestBodyBytes > 0,
		"force_store":                cfg.ResponsesForceStore(),
		"auth":                       doctorResponsesHTTPGuardAuthDetail(cfg),
		"management":                 doctorResponsesHTTPGuardManagementDetail(cfg, normalizedPath),
		"server_port":                strings.TrimSpace(cfg.Server.Port),
		"monitor_port":               strings.TrimSpace(cfg.Monitor.Port),
		"proxy_server":               "server.port",
		"management_server":          "monitor.port",
		"request_body_guard_source":  "internal/responses/httpapi.WithMaxBodyBytes",
		"entrypoint_normalizer":      "internal/proxy.normalizeClientEntrypoint",
		"auth_database_status_check": "not_opened",
	}
	if pathErr != nil {
		detail["error"] = pathErr.Error()
		return doctorCheck{Name: "responses_server.http_guard", Status: doctorStatusFail, Message: "responses server path is invalid", Detail: detail}
	}
	if normalizedPath == "/" {
		detail["error"] = "responses_server.path must not be / because it captures every proxy request"
		return doctorCheck{Name: "responses_server.http_guard", Status: doctorStatusFail, Message: "responses server path captures every request", Detail: detail}
	}

	var warnings []string
	var failures []string
	if cfg.ResponsesMaxRequestBodyBytes() < 1024 {
		warnings = append(warnings, "responses_server.max_request_body_bytes is below 1024 bytes and may reject normal JSON requests")
	}
	if canonical := canonicalDoctorClientEntrypoint(normalizedPath); canonical != normalizedPath {
		detail["client_entrypoint_canonical_path"] = canonical
		warnings = append(warnings, "responses_server.path is rewritten by the proxy entrypoint normalizer before local responses routing")
	}
	management := doctorResponsesHTTPGuardManagement(cfg, normalizedPath)
	detail["management"] = management.detail
	failures = append(failures, management.failures...)
	warnings = append(warnings, management.warnings...)

	authDetail := doctorResponsesHTTPGuardAuthDetail(cfg)
	detail["auth"] = authDetail
	if supported, _ := authDetail["auth_store_driver_supported"].(bool); !supported {
		warnings = append(warnings, "auth verifier will not become effective because serve cannot open an auth store for the configured database driver")
	}

	if len(failures) > 0 {
		detail["failures"] = failures
		if len(warnings) > 0 {
			detail["warnings"] = warnings
		}
		return doctorCheck{Name: "responses_server.http_guard", Status: doctorStatusFail, Message: "responses server HTTP guard has startup conflicts", Detail: detail}
	}
	if len(warnings) > 0 {
		detail["warnings"] = warnings
		return doctorCheck{Name: "responses_server.http_guard", Status: doctorStatusWarn, Message: "responses server HTTP guard has offline warnings", Detail: detail}
	}
	return doctorCheck{Name: "responses_server.http_guard", Status: doctorStatusPass, Message: "responses server HTTP guard configuration is ready", Detail: detail}
}

func normalizeDoctorResponsesPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("responses_server.path must not be empty")
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("responses_server.path must be an absolute HTTP path starting with /")
	}
	normalized := pathpkg.Clean(raw)
	if normalized == "." {
		normalized = "/"
	}
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	if normalized != "/" {
		normalized = strings.TrimRight(normalized, "/")
	}
	return normalized, nil
}

func canonicalDoctorClientEntrypoint(path string) string {
	switch path {
	case "/responses":
		return "/v1/responses"
	case "/v1/tokenize":
		return "/tokenize"
	case "/v1/detokenize":
		return "/detokenize"
	case "/anthropic/messages", "/anthropic/v1/messages":
		return "/v1/messages"
	case "/anthropic/messages/count_tokens", "/anthropic/v1/messages/count_tokens":
		return "/v1/messages/count_tokens"
	default:
		return path
	}
}

func doctorResponsesHTTPGuardAuthDetail(cfg *appconfig.Config) map[string]any {
	driver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	supported := driver == "sqlite" || driver == "postgres"
	databasePathSource := "default"
	if strings.TrimSpace(cfg.Auth.DatabasePath) != "" {
		databasePathSource = "auth.database_path"
	} else if strings.TrimSpace(cfg.Database.DSN) != "" {
		databasePathSource = "database.dsn"
	}
	return map[string]any{
		"token_auth_config_flag":       "not_configurable",
		"api_auth_config_flag":         "not_configurable",
		"proxy_auth_required":          true,
		"management_auth_required":     strings.TrimSpace(cfg.Monitor.Port) != "",
		"mcp_auth_required":            cfg.MCP.Enabled && strings.TrimSpace(cfg.Monitor.Port) != "",
		"verifier_source":              "auth store opened by serve",
		"verifier_status_check":        "configuration-only",
		"auth_store_driver":            driver,
		"auth_store_driver_supported":  supported,
		"auth_database_path_source":    databasePathSource,
		"auth_database_opened":         false,
		"auth_database_opened_reason":  "doctor does not open auth database",
		"auth_migration_check_command": "auth migrate status",
	}
}

type doctorResponsesHTTPGuardManagementReport struct {
	detail   map[string]any
	warnings []string
	failures []string
}

func doctorResponsesHTTPGuardManagementDetail(cfg *appconfig.Config, normalizedPath string) map[string]any {
	return doctorResponsesHTTPGuardManagement(cfg, normalizedPath).detail
}

func doctorResponsesHTTPGuardManagement(cfg *appconfig.Config, normalizedPath string) doctorResponsesHTTPGuardManagementReport {
	serverPort := strings.TrimSpace(cfg.Server.Port)
	monitorPort := strings.TrimSpace(cfg.Monitor.Port)
	monitorEnabled := monitorPort != ""
	samePort := monitorEnabled && serverPort != "" && serverPort == monitorPort
	mcpPath := ""
	mcpPathOverlap := false
	if cfg.MCP.Enabled {
		if normalized, err := normalizeMCPPath(cfg.MCP.Path); err == nil {
			mcpPath = normalized
			mcpPathOverlap = httpPathsOverlap(normalizedPath, mcpPath)
		} else {
			mcpPath = strings.TrimSpace(cfg.MCP.Path)
		}
	}
	monitorPathOverlap := monitorEnabled && httpPathsOverlap(normalizedPath, "/api")
	detail := map[string]any{
		"server_port":                  serverPort,
		"monitor_port":                 monitorPort,
		"monitor_enabled":              monitorEnabled,
		"same_port":                    samePort,
		"mcp_enabled":                  cfg.MCP.Enabled,
		"mcp_path":                     mcpPath,
		"mcp_path_overlap":             mcpPathOverlap,
		"monitor_api_prefix":           "/api",
		"monitor_api_overlap":          monitorPathOverlap,
		"serve_boundary":               "proxy and management use separate http.Server instances",
		"runtime_conflict_possible":    samePort && (mcpPathOverlap || monitorPathOverlap),
		"management_path_check_scope":  "mcp path and monitor /api prefix",
		"management_root_app_handler":  monitorEnabled,
		"management_root_overlap_note": "ignored unless responses_server.path is /, which is checked separately",
	}
	var report doctorResponsesHTTPGuardManagementReport
	report.detail = detail
	if samePort {
		report.failures = append(report.failures, "server.port and monitor.port are the same; serve starts proxy and management as separate HTTP servers")
	}
	if samePort && mcpPathOverlap {
		report.failures = append(report.failures, "responses_server.path overlaps mcp.path on the same server/monitor port")
	}
	if samePort && monitorPathOverlap {
		report.failures = append(report.failures, "responses_server.path overlaps monitor /api management routes on the same server/monitor port")
	}
	return report
}

func httpPathsOverlap(a string, b string) bool {
	a = strings.TrimRight(a, "/")
	b = strings.TrimRight(b, "/")
	if a == "" {
		a = "/"
	}
	if b == "" {
		b = "/"
	}
	if a == "/" || b == "/" {
		return true
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func checkDoctorResponsesDefaultModel(cfg *appconfig.Config) doctorCheck {
	detail := map[string]any{
		"default_model": cfg.ResponsesDefaultModel(),
	}
	if cfg.ResponsesDefaultModel() == "" {
		return doctorCheck{
			Name:    "responses_server.default_model",
			Status:  doctorStatusWarn,
			Message: "responses_server.default_model is empty; requests that omit a model fall back to upstream selection instead of a Responses default",
			Detail:  detail,
		}
	}
	return doctorCheck{Name: "responses_server.default_model", Status: doctorStatusPass, Message: "responses default model is configured", Detail: detail}
}

func checkDoctorResponsesStoreReadiness(cfg *appconfig.Config) doctorCheck {
	effectiveDriver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	detail := map[string]any{
		"force_store":               cfg.ResponsesForceStore(),
		"database_driver":           effectiveDriver,
		"database_driver_raw":       strings.TrimSpace(cfg.Database.Driver),
		"database_dsn":              redactConfigInspectDSN(cfg.DatabaseDSN()),
		"database_auto_migrate":     cfg.DatabaseAutoMigrate(),
		"migration_mode":            appDBMigrationMode(effectiveDriver),
		"production_storage_driver": appdbmigrate.ProductionStorageDriver,
		"production_ready":          effectiveDriver == appdbmigrate.ProductionStorageDriver,
		"storage_role":              configInspectDatabaseStorageRole(effectiveDriver),
		"storage_contract":          configInspectDatabaseStorageContract(effectiveDriver),
		"status_check":              "configuration-only",
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

var doctorResponsesSemanticTables = []string{"responses", "response_items"}
var doctorResponsesAuditTables = []string{"request_audits", "execution_events", "upstream_exchanges", "tool_call_audits"}
var doctorResponsesSettingsTables = []string{"app_settings"}

func checkDoctorResponsesStoreHealth(cfg *appconfig.Config, checkDB bool) doctorCheck {
	driver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	requiredTables := doctorResponsesRequiredStoreHealthTables()
	detail := map[string]any{
		"force_store":               cfg.ResponsesForceStore(),
		"database_driver":           driver,
		"database_driver_raw":       strings.TrimSpace(cfg.Database.Driver),
		"database_dsn":              redactConfigInspectDSN(cfg.DatabaseDSN()),
		"database_auto_migrate":     cfg.DatabaseAutoMigrate(),
		"migration_mode":            appDBMigrationMode(driver),
		"production_storage_driver": appdbmigrate.ProductionStorageDriver,
		"production_ready":          driver == appdbmigrate.ProductionStorageDriver,
		"storage_role":              configInspectDatabaseStorageRole(driver),
		"storage_contract":          configInspectDatabaseStorageContract(driver),
		"status_check":              appDBStatusCheckMode(checkDB),
		"check_db_required":         !checkDB && cfg.ResponsesForceStore() && !cfg.DatabaseAutoMigrate(),
		"required_semantic_tables":  append([]string(nil), doctorResponsesSemanticTables...),
		"required_audit_tables":     append([]string(nil), doctorResponsesAuditTables...),
		"required_settings_tables":  append([]string(nil), doctorResponsesSettingsTables...),
		"required_tables":           requiredTables,
	}
	if !checkDB {
		if cfg.ResponsesForceStore() && !cfg.DatabaseAutoMigrate() {
			detail["warnings"] = []string{"responses_server.force_store is true and database.auto_migrate is false; run doctor --check-db or db migrate status --check-db before serving"}
			return doctorCheck{Name: "responses_server.store_health", Status: doctorStatusWarn, Message: "responses store health needs an explicit database check", Detail: detail}
		}
		return doctorCheck{Name: "responses_server.store_health", Status: doctorStatusPass, Message: "responses store health reported from configuration", Detail: detail}
	}

	if err := applyAppDBStatusCheck(cfg, detail); err != nil {
		detail["error"] = redactDoctorMessage(err.Error())
		return doctorCheck{Name: "responses_server.store_health", Status: doctorStatusFail, Message: "responses store database status check failed", Detail: detail}
	}
	missing, err := checkDoctorResponsesStoreTables(cfg, requiredTables)
	if err != nil {
		detail["error"] = redactDoctorMessage(err.Error())
		return doctorCheck{Name: "responses_server.store_health", Status: doctorStatusFail, Message: "responses store table check failed", Detail: detail}
	}
	detail["database_tables_checked"] = true
	detail["database_required_tables_present"] = len(missing) == 0
	if len(missing) > 0 {
		detail["database_missing_tables"] = missing
		return doctorCheck{Name: "responses_server.store_health", Status: doctorStatusFail, Message: "responses store database is missing required tables", Detail: detail}
	}
	return doctorCheck{Name: "responses_server.store_health", Status: doctorStatusPass, Message: "responses store database contains required tables", Detail: detail}
}

func doctorResponsesRequiredStoreHealthTables() []string {
	tables := make([]string, 0, len(doctorResponsesSemanticTables)+len(doctorResponsesAuditTables)+len(doctorResponsesSettingsTables))
	tables = append(tables, doctorResponsesSemanticTables...)
	tables = append(tables, doctorResponsesAuditTables...)
	tables = append(tables, doctorResponsesSettingsTables...)
	return tables
}

func checkDoctorResponsesStoreTables(cfg *appconfig.Config, requiredTables []string) ([]string, error) {
	driver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	switch driver {
	case "sqlite":
		return checkDoctorResponsesSQLiteTables(cfg.DatabaseDSN(), requiredTables)
	case "postgres":
		return checkDoctorResponsesPostgresTables(cfg.DatabaseDSN(), requiredTables)
	default:
		return nil, fmt.Errorf("responses store table check does not support database driver %q", driver)
	}
}

func checkDoctorResponsesSQLiteTables(dsn string, requiredTables []string) ([]string, error) {
	dbPath := appconfig.SQLitePathFromDSN(dsn)
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("sqlite application database path is empty")
	}
	if dbPath == ":memory:" {
		return nil, fmt.Errorf("sqlite in-memory database is not inspectable")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("sqlite application database file does not exist")
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", doctorSQLiteReadOnlyDSN(dbPath))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var missing []string
	for _, table := range requiredTables {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (
			SELECT 1
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		)`, table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, table)
		}
	}
	return missing, nil
}

func doctorSQLiteReadOnlyDSN(dbPath string) string {
	values := url.Values{}
	values.Set("mode", "ro")
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}).String()
}

func checkDoctorResponsesPostgresTables(dsn string, requiredTables []string) ([]string, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var missing []string
	for _, table := range requiredTables {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1
		)`, table).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, table)
		}
	}
	return missing, nil
}

func checkDoctorResponsesModelProfiles(cfg *appconfig.Config) doctorCheck {
	profiles := cfg.ResponsesModelProfiles()
	detail := map[string]any{
		"auto_compact":                          cfg.ResponsesAutoCompactEnabled(),
		"compact_history_item_threshold":        cfg.ResponsesCompactHistoryItemThreshold(),
		"compact_history_item_threshold_config": cfg.ResponsesServer.CompactHistoryItemThreshold,
		"model_profiles":                        len(profiles),
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

func checkDoctorResponsesModelCatalogDrift(cfg *appconfig.Config) doctorCheck {
	model := cfg.ResponsesDefaultModel()
	detail := map[string]any{
		"model": model,
	}
	if strings.TrimSpace(model) == "" {
		detail["skipped_reason"] = "responses_server.default_model is empty"
		return doctorCheck{Name: "responses_server.model_catalog_drift", Status: doctorStatusWarn, Message: "responses default model is empty; model catalog drift check skipped", Detail: detail}
	}

	match := cfg.MatchResponsesModelProfile(model)
	diagnostics := buildModelsCatalogDriftDiagnostics(cfg, model, match.Matched, false)
	detail["matched_profile"] = modelsCodexMatchedProfile{
		Matched:       match.Matched,
		Index:         match.Index,
		Kind:          match.Kind,
		Source:        match.Source,
		Name:          match.Profile.Name,
		Pattern:       match.Profile.Pattern,
		UpstreamModel: match.Profile.UpstreamModel,
	}
	detail["database_available"] = diagnostics.DatabaseAvailable
	detail["catalog_model_present"] = diagnostics.CatalogModelPresent
	detail["channel_model_present"] = diagnostics.ChannelModelPresent
	detail["channel_model_count"] = diagnostics.ChannelModelCount
	detail["catalog_source"] = diagnostics.CatalogSource
	detail["channel_source"] = diagnostics.ChannelSource
	driftWarnings := diagnostics.DriftWarnings
	if driftWarnings == nil {
		driftWarnings = []string{}
	}
	detail["drift_warnings"] = driftWarnings

	if len(driftWarnings) > 0 {
		return doctorCheck{Name: "responses_server.model_catalog_drift", Status: doctorStatusWarn, Message: "responses model catalog/channel drift detected", Detail: detail}
	}
	if !diagnostics.DatabaseAvailable {
		return doctorCheck{Name: "responses_server.model_catalog_drift", Status: doctorStatusPass, Message: "responses model catalog drift check skipped; application SQLite database unavailable", Detail: detail}
	}
	return doctorCheck{Name: "responses_server.model_catalog_drift", Status: doctorStatusPass, Message: "responses model catalog/channel drift not detected", Detail: detail}
}

func checkDoctorResponsesCodexConfigDrift(cfg *appconfig.Config, codexConfigPath string) doctorCheck {
	model := cfg.ResponsesDefaultModel()
	result := buildModelsCodexConfigResult(cfg, model, codexConfigPath, false)
	diagnostics := result.Diagnostics.CodexConfig
	detail := doctorCodexConfigDriftDetail(cfg, result, diagnostics, strings.TrimSpace(codexConfigPath) != "")

	switch diagnostics.Status {
	case "not_configured":
		detail["skipped_reason"] = "doctor --codex-config is empty"
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusPass, Message: "Codex config drift check skipped; not configured", Detail: detail}
	case "ok":
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusPass, Message: "Codex config drift not detected", Detail: detail}
	case "missing":
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusWarn, Message: "Codex config file is missing", Detail: detail}
	case "unreadable":
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusWarn, Message: "Codex config file is unreadable", Detail: detail}
	case "parse_error":
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusWarn, Message: "Codex config file is invalid TOML", Detail: detail}
	case "drift":
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusWarn, Message: "Codex config drift detected", Detail: detail}
	default:
		return doctorCheck{Name: "responses_server.codex_config_drift", Status: doctorStatusWarn, Message: "Codex config drift check returned an unknown status", Detail: detail}
	}
}

func doctorCodexConfigDriftDetail(cfg *appconfig.Config, result modelsCodexConfigResult, diagnostics modelsCodexLocalConfig, configured bool) map[string]any {
	driftWarnings := diagnostics.DriftWarnings
	if driftWarnings == nil {
		driftWarnings = []string{}
	}
	fields := diagnostics.Fields
	if fields == nil {
		fields = []modelsCodexLocalFieldStatus{}
	}
	return map[string]any{
		"configured":        configured,
		"model":             result.Model,
		"status":            diagnostics.Status,
		"profile_name":      diagnostics.ProfileName,
		"provider_name":     diagnostics.ProviderName,
		"present":           diagnostics.Present,
		"readable":          diagnostics.Readable,
		"parsed":            diagnostics.Parsed,
		"profile_present":   diagnostics.ProfilePresent,
		"provider_present":  diagnostics.ProviderPresent,
		"fields":            fields,
		"drift_warnings":    driftWarnings,
		"matched_profile":   result.Diagnostics.MatchedProfile,
		"provider_base_url": redactURLLike(result.Provider.BaseURL),
	}
}

func checkDoctorResponsesCodexCompat(cfg *appconfig.Config) doctorCheck {
	compat := cfg.ResponsesCodexCompatConfig()
	detail := map[string]any{
		"enabled":                    compat.Enabled,
		"auto_inject_hosted_tools":   append([]string(nil), compat.AutoInjectHostedTools...),
		"inject_when_tools_absent":   compat.InjectWhenToolsAbsent != nil && *compat.InjectWhenToolsAbsent,
		"preserve_client_tools":      compat.PreserveClientTools != nil && *compat.PreserveClientTools,
		"default_tool_choice":        compat.DefaultToolChoice,
		"tools_web_search_enabled":   cfg.WebSearchEnabled(),
		"tools_mcp_enabled":          cfg.MCPToolsEnabled(),
		"injectable_hosted_tool_cnt": 0,
	}
	if !compat.Enabled {
		detail["skipped_reason"] = "responses_server.codex_compat.enabled is false"
		return doctorCheck{Name: "responses_server.codex_compat", Status: doctorStatusPass, Message: "Codex compatibility injection is disabled", Detail: detail}
	}

	var warnings []string
	injectable := 0
	seen := map[string]struct{}{}
	for _, tool := range compat.AutoInjectHostedTools {
		normalized := strings.ToLower(strings.TrimSpace(tool))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		switch normalized {
		case "web_search", "web_search_preview":
			if cfg.WebSearchEnabled() {
				injectable++
			} else {
				warnings = append(warnings, "web_search injection requested but tools.web_search.enabled is false")
			}
		case "mcp":
			if cfg.MCPToolsEnabled() {
				injectable++
			} else {
				warnings = append(warnings, "mcp injection requested but tools.mcp.enabled is false")
			}
		default:
			warnings = append(warnings, fmt.Sprintf("unsupported hosted tool %q is configured for Codex compatibility injection", tool))
		}
	}
	detail["injectable_hosted_tool_cnt"] = injectable
	if len(warnings) > 0 {
		detail["warnings"] = warnings
	}
	if len(compat.AutoInjectHostedTools) == 0 {
		detail["warnings"] = append(warnings, "Codex compatibility is enabled but auto_inject_hosted_tools is empty")
		return doctorCheck{Name: "responses_server.codex_compat", Status: doctorStatusWarn, Message: "Codex compatibility has no hosted tools to inject", Detail: detail}
	}
	if injectable == 0 {
		if len(warnings) == 0 {
			warnings = append(warnings, "Codex compatibility is enabled but no configured hosted tools are injectable")
			detail["warnings"] = warnings
		}
		return doctorCheck{Name: "responses_server.codex_compat", Status: doctorStatusWarn, Message: "Codex compatibility has no injectable hosted tools", Detail: detail}
	}
	if len(warnings) > 0 {
		return doctorCheck{Name: "responses_server.codex_compat", Status: doctorStatusWarn, Message: "Codex compatibility injection has warnings", Detail: detail}
	}
	return doctorCheck{Name: "responses_server.codex_compat", Status: doctorStatusPass, Message: "Codex compatibility injection configuration is valid", Detail: detail}
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

func checkDoctorMCPTools(cfg *appconfig.Config) doctorCheck {
	mcpTools := cfg.MCPToolsConfig()
	detail := map[string]any{
		"enabled":            mcpTools.Enabled,
		"default_timeout_ms": mcpTools.DefaultTimeoutMS,
		"max_result_bytes":   mcpTools.MaxResultBytes,
		"server_count":       len(mcpTools.Servers),
		"enabled_servers":    0,
		"servers":            doctorMCPToolServerDetails(mcpTools.Servers),
	}
	if !mcpTools.Enabled {
		detail["skipped_reason"] = "tools.mcp.enabled is false"
		return doctorCheck{Name: "tools.mcp.config", Status: doctorStatusPass, Message: "MCP hosted tools are disabled", Detail: detail}
	}
	if len(mcpTools.Servers) == 0 {
		return doctorCheck{Name: "tools.mcp.config", Status: doctorStatusFail, Message: "MCP hosted tools are enabled but no servers are configured", Detail: detail}
	}

	var failures []string
	enabledServers := 0
	for idx, server := range mcpTools.Servers {
		if !server.EnabledOrDefault() {
			continue
		}
		enabledServers++
		label := doctorMCPToolServerLabel(idx, server)
		if strings.TrimSpace(server.URL) == "" {
			failures = append(failures, label+" url is required")
		} else if parsed, err := url.Parse(strings.TrimSpace(server.URL)); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			failures = append(failures, label+" url must be an absolute URL")
		}
		if envName := strings.TrimSpace(server.BearerTokenEnv); envName != "" && os.Getenv(envName) == "" {
			failures = append(failures, label+" bearer_token_env is set but the environment variable is empty or unset")
		}
	}
	detail["enabled_servers"] = enabledServers
	if enabledServers == 0 {
		failures = append(failures, "tools.mcp has no enabled servers")
	}
	if len(failures) > 0 {
		detail["failures"] = failures
		return doctorCheck{Name: "tools.mcp.config", Status: doctorStatusFail, Message: "MCP hosted tool configuration is invalid", Detail: detail}
	}
	return doctorCheck{Name: "tools.mcp.config", Status: doctorStatusPass, Message: "MCP hosted tool configuration is valid", Detail: detail}
}

func doctorMCPToolServerDetails(servers []appconfig.MCPToolServerConfig) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, server := range servers {
		envName := strings.TrimSpace(server.BearerTokenEnv)
		out = append(out, map[string]any{
			"id":                      strings.TrimSpace(server.ID),
			"label":                   strings.TrimSpace(server.Label),
			"url":                     redactURLLike(server.URL),
			"bearer_token_env":        envName,
			"bearer_token_configured": envName != "" && os.Getenv(envName) != "",
			"enabled_tools":           append([]string(nil), server.EnabledTools...),
			"disabled_tools":          append([]string(nil), server.DisabledTools...),
			"enabled":                 server.EnabledOrDefault(),
		})
	}
	return out
}

func doctorMCPToolServerLabel(idx int, server appconfig.MCPToolServerConfig) string {
	if server.ID != "" {
		return fmt.Sprintf("servers[%d] %q", idx, server.ID)
	}
	if server.Label != "" {
		return fmt.Sprintf("servers[%d] %q", idx, server.Label)
	}
	return fmt.Sprintf("servers[%d]", idx)
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

func checkDoctorAuthMigrationScope(cfg *appconfig.Config) doctorCheck {
	driver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	shared := driver == appdbmigrate.ProductionStorageDriver
	effectiveNamespace := "auth"
	schemaAuthority := "sqlite_auth_embedded_migrations"
	rollbackScope := "auth_database_migration_set"
	rollbackSupported := true
	storageContract := "sqlite_auth_migrations_for_legacy_dev_test"
	storageRole := appdbmigrate.SQLiteStorageRole
	if shared {
		effectiveNamespace = "application"
		schemaAuthority = "application_postgres_migration_set"
		rollbackScope = "unsupported_from_auth_cli_shared_application_migration_set"
		rollbackSupported = false
		storageContract = "postgres_application_schema_owns_auth_tables"
		storageRole = "production"
	}
	return doctorCheck{
		Name:    "auth.migration_scope",
		Status:  doctorStatusPass,
		Message: "auth migration scope contract reported",
		Detail: map[string]any{
			"database_driver":                  driver,
			"production_storage_driver":        appdbmigrate.ProductionStorageDriver,
			"production_ready":                 shared,
			"database_namespace":               "auth",
			"effective_database_namespace":     effectiveNamespace,
			"schema_authority":                 schemaAuthority,
			"storage_role":                     storageRole,
			"storage_contract":                 storageContract,
			"application_namespace_shared":     shared,
			"independent_auth_namespace":       !shared,
			"postgres_auth_namespace_strategy": map[bool]string{true: "shared_application_schema_migrations", false: "not_applicable"}[shared],
			"rollback_supported":               rollbackSupported,
			"auth_namespace_rollback_scope":    rollbackScope,
			"command":                          "auth migrate",
			"canonical_postgres_setup_command": "db migrate up",
		},
	}
}

func checkDoctorProviderProbe(cfg *appconfig.Config, probeProviders bool) doctorCheck {
	if !probeProviders {
		return doctorCheck{Name: "provider_probe.network", Status: doctorStatusPass, Message: "provider network probe skipped", Detail: map[string]any{"enabled": false}}
	}
	targets, err := providerProbeTargets(*cfg, "")
	if err != nil {
		return doctorCheck{
			Name:    "provider_probe.network",
			Status:  doctorStatusWarn,
			Message: "provider network probe requested but no enabled provider target with base_url is configured",
			Detail: map[string]any{
				"enabled":          true,
				"network_requests": false,
				"summary":          doctorProviderProbeSummary{},
				"error":            redactDoctorMessage(err.Error()),
			},
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	report := providerprobe.ProbeBatch(context.Background(), targets, client)
	report = redactDoctorProviderProbeReport(report)
	summary := summarizeDoctorProviderProbe(report)
	status := doctorStatusPass
	if summary.Error > 0 {
		status = doctorStatusFail
	} else if summary.Unknown > 0 {
		status = doctorStatusWarn
	}
	return doctorCheck{
		Name:    "provider_probe.network",
		Status:  status,
		Message: fmt.Sprintf("provider network probe completed: total=%d detected=%d error=%d unknown=%d", summary.Total, summary.Detected, summary.Error, summary.Unknown),
		Detail: map[string]any{
			"enabled":          true,
			"network_requests": true,
			"summary":          summary,
			"reports":          report.Reports,
		},
	}
}

func summarizeDoctorProviderProbe(report providerprobe.BatchReport) doctorProviderProbeSummary {
	summary := doctorProviderProbeSummary{Total: len(report.Reports)}
	for _, item := range report.Reports {
		switch item.Status {
		case providerprobe.StatusDetected:
			summary.Detected++
		case providerprobe.StatusError:
			summary.Error++
		default:
			summary.Unknown++
		}
	}
	return summary
}

func redactDoctorProviderProbeReport(report providerprobe.BatchReport) providerprobe.BatchReport {
	for reportIdx := range report.Reports {
		item := &report.Reports[reportIdx]
		item.BaseURL = redactURLLike(item.BaseURL)
		item.Error = redactDoctorMessage(item.Error)
		for warningIdx := range item.Warnings {
			item.Warnings[warningIdx] = redactDoctorMessage(item.Warnings[warningIdx])
		}
		for endpointIdx := range item.CheckedEndpoints {
			endpoint := &item.CheckedEndpoints[endpointIdx]
			endpoint.URL = redactURLLike(endpoint.URL)
			endpoint.Error = redactDoctorMessage(endpoint.Error)
			endpoint.StatusText = redactDoctorMessage(endpoint.StatusText)
		}
	}
	return report
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
