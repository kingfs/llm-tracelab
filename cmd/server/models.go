package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

const (
	codexConfigCommand           = "models.codex_config"
	codexConfigModelProvider     = "llm-tracelab"
	codexConfigProviderName      = "llm-tracelab"
	codexConfigAPIKeyEnv         = "LLM_TRACELAB_API_KEY"
	codexConfigWireAPI           = "responses"
	codexConfigRequestMaxRetries = 2
	codexConfigStreamMaxRetries  = 2
	codexConfigStreamIdleTimeout = 120000
)

type modelsCodexConfigOptions struct {
	configPath      string
	codexConfigPath string
	format          string
	stdout          io.Writer
	model           string
}

type modelsCodexConfigResult struct {
	Model       string                    `json:"model"`
	WireAPI     string                    `json:"wire_api"`
	Profile     modelsCodexProfileConfig  `json:"profile"`
	Provider    modelsCodexProviderConfig `json:"provider"`
	Diagnostics modelsCodexDiagnostics    `json:"diagnostics"`
	TOML        string                    `json:"toml"`
	Warnings    []string                  `json:"warnings"`
}

type modelsCodexProfileConfig struct {
	ModelProvider              string `json:"model_provider"`
	Model                      string `json:"model"`
	ModelContextWindow         int    `json:"model_context_window"`
	ModelAutoCompactTokenLimit int    `json:"model_auto_compact_token_limit"`
}

type modelsCodexProviderConfig struct {
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	ResponsesPath       string `json:"responses_path"`
	ServerOrigin        string `json:"server_origin"`
	EnvKey              string `json:"env_key"`
	WireAPI             string `json:"wire_api"`
	RequestMaxRetries   int    `json:"request_max_retries"`
	StreamMaxRetries    int    `json:"stream_max_retries"`
	StreamIdleTimeoutMS int    `json:"stream_idle_timeout_ms"`
	BaseURLSource       string `json:"base_url_source"`
}

type modelsCodexDiagnostics struct {
	MatchedProfile                    modelsCodexMatchedProfile `json:"matched_profile"`
	RuntimeProfileSource              string                    `json:"runtime_profile_source"`
	ProfilePrecedence                 []string                  `json:"profile_precedence"`
	CatalogProfileRole                string                    `json:"catalog_profile_role"`
	CapabilitySource                  string                    `json:"capability_source"`
	CompactLimitSource                string                    `json:"compact_limit_source"`
	CompactLimitMarginTokens          int                       `json:"compact_limit_margin_tokens"`
	CompactHistoryItemThreshold       int                       `json:"compact_history_item_threshold"`
	CompactHistoryItemThresholdSource string                    `json:"compact_history_item_threshold_source"`
	ResponsesServerEnabled            bool                      `json:"responses_server_enabled"`
	DatabaseAvailable                 bool                      `json:"database_available"`
	CatalogModelPresent               bool                      `json:"catalog_model_present"`
	ChannelModelPresent               bool                      `json:"channel_model_present"`
	ChannelModelCount                 int                       `json:"channel_model_count"`
	CatalogSource                     string                    `json:"catalog_source"`
	ChannelSource                     string                    `json:"channel_source"`
	DriftWarnings                     []string                  `json:"drift_warnings,omitempty"`
	CodexConfig                       modelsCodexLocalConfig    `json:"codex_config"`
}

type modelsCodexMatchedProfile struct {
	Matched       bool   `json:"matched"`
	Index         int    `json:"index"`
	Kind          string `json:"kind,omitempty"`
	Source        string `json:"source"`
	Name          string `json:"name,omitempty"`
	Pattern       string `json:"pattern,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
}

type modelsCodexLocalConfig struct {
	Path            string                        `json:"path,omitempty"`
	Status          string                        `json:"status"`
	Present         bool                          `json:"present"`
	Readable        bool                          `json:"readable"`
	Parsed          bool                          `json:"parsed"`
	ProfileName     string                        `json:"profile_name,omitempty"`
	ProviderName    string                        `json:"provider_name,omitempty"`
	ProfilePresent  bool                          `json:"profile_present"`
	ProviderPresent bool                          `json:"provider_present"`
	Fields          []modelsCodexLocalFieldStatus `json:"fields,omitempty"`
	DriftWarnings   []string                      `json:"drift_warnings"`
}

type modelsCodexLocalFieldStatus struct {
	Field    string `json:"field"`
	Present  bool   `json:"present"`
	Matched  bool   `json:"matched"`
	Expected string `json:"expected"`
	Actual   string `json:"actual,omitempty"`
}

func newModelsCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "models",
		Short:         "Inspect model-related local configuration",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newModelsCodexConfigCommand(runtime))
	return cmd
}

func newModelsCodexConfigCommand(runtime *cliRuntime) *cobra.Command {
	var codexConfigPath string
	cmd := &cobra.Command{
		Use:   "codex-config <model>",
		Short: "Generate a Codex profile TOML suggestion from local Responses model profiles",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runModelsCodexConfigWithOptions(modelsCodexConfigOptions{
					configPath:      runtime.configPath(),
					codexConfigPath: codexConfigPath,
					format:          runtime.outputFormat(),
					stdout:          cmd.OutOrStdout(),
					model:           args[0],
				})
			})
		},
	}
	cmd.Flags().StringVar(&codexConfigPath, "codex-config", "", "Path to a local Codex TOML config file to diagnose for drift")
	return cmd
}

func runModelsCodexConfigWithOptions(opts modelsCodexConfigOptions) int {
	cfg, err := appconfig.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := buildModelsCodexConfigResult(cfg, opts.model, opts.codexConfigPath)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, codexConfigCommand, result, func(w io.Writer) error {
		writeModelsCodexConfigText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write models codex-config result failed", "error", err)
		return 1
	}
	return 0
}

func buildModelsCodexConfigResult(cfg *appconfig.Config, model string, codexConfigPath string) modelsCodexConfigResult {
	if cfg == nil {
		cfg = &appconfig.Config{}
	}
	model = strings.TrimSpace(model)
	match := cfg.MatchResponsesModelProfile(model)
	profile := match.Profile
	warnings := make([]string, 0, 4)
	if !cfg.ResponsesServerEnabled() {
		warnings = append(warnings, "responses_server.enabled is false; this is a configuration suggestion only until server-mode is enabled")
	}
	if !match.Matched {
		warnings = append(warnings, fmt.Sprintf("no responses_server.model_profiles entry matched model %q; using zero model limits", model))
	}
	contextWindow := profile.ContextWindowTokens
	autoCompactLimit := 0
	compactLimitSource := "none"
	if contextWindow > 0 {
		autoCompactLimit = contextWindow * 8 / 10
		if match.Matched {
			compactLimitSource = match.Source + ".context_window_tokens_80_percent"
		} else {
			compactLimitSource = "context_window_80_percent"
		}
	} else if match.Matched {
		warnings = append(warnings, fmt.Sprintf("matched profile for model %q has no context_window_tokens; Codex token limits are emitted as 0", model))
	}
	provider, providerWarnings := buildModelsCodexProviderConfig(cfg)
	warnings = append(warnings, providerWarnings...)
	historyThreshold, historyThresholdSource := codexCompactHistoryItemThreshold(cfg, match)
	catalogDiagnostics := buildModelsCatalogDriftDiagnostics(cfg, model, match.Matched)
	warnings = append(warnings, catalogDiagnostics.DriftWarnings...)
	codexConfigDiagnostics := modelsCodexLocalConfig{Status: "not_configured", DriftWarnings: []string{}}
	diagnostics := modelsCodexDiagnostics{
		MatchedProfile: modelsCodexMatchedProfile{
			Matched:       match.Matched,
			Index:         match.Index,
			Kind:          match.Kind,
			Source:        match.Source,
			Name:          profile.Name,
			Pattern:       profile.Pattern,
			UpstreamModel: profile.UpstreamModel,
		},
		RuntimeProfileSource:              "responses_server.model_profiles",
		ProfilePrecedence:                 []string{"responses_server.model_profiles", "zero_limits_when_unmatched"},
		CatalogProfileRole:                "diagnostic_only",
		CapabilitySource:                  "provider_upstream_capabilities",
		CompactLimitSource:                compactLimitSource,
		CompactLimitMarginTokens:          contextWindow - autoCompactLimit,
		CompactHistoryItemThreshold:       historyThreshold,
		CompactHistoryItemThresholdSource: historyThresholdSource,
		ResponsesServerEnabled:            cfg.ResponsesServerEnabled(),
		DatabaseAvailable:                 catalogDiagnostics.DatabaseAvailable,
		CatalogModelPresent:               catalogDiagnostics.CatalogModelPresent,
		ChannelModelPresent:               catalogDiagnostics.ChannelModelPresent,
		ChannelModelCount:                 catalogDiagnostics.ChannelModelCount,
		CatalogSource:                     catalogDiagnostics.CatalogSource,
		ChannelSource:                     catalogDiagnostics.ChannelSource,
		DriftWarnings:                     catalogDiagnostics.DriftWarnings,
		CodexConfig:                       codexConfigDiagnostics,
	}
	result := modelsCodexConfigResult{
		Model:   model,
		WireAPI: codexConfigWireAPI,
		Profile: modelsCodexProfileConfig{
			ModelProvider:              codexConfigModelProvider,
			Model:                      model,
			ModelContextWindow:         contextWindow,
			ModelAutoCompactTokenLimit: autoCompactLimit,
		},
		Provider:    provider,
		Diagnostics: diagnostics,
		Warnings:    warnings,
	}
	result.TOML = modelsCodexConfigTOML(result)
	result.Diagnostics.CodexConfig = diagnoseModelsCodexLocalConfig(codexConfigPath, result)
	warnings = append(warnings, result.Diagnostics.CodexConfig.DriftWarnings...)
	result.Warnings = warnings
	return result
}

type modelsCatalogDriftDiagnostics struct {
	DatabaseAvailable   bool
	CatalogModelPresent bool
	ChannelModelPresent bool
	ChannelModelCount   int
	CatalogSource       string
	ChannelSource       string
	DriftWarnings       []string
}

func buildModelsCatalogDriftDiagnostics(cfg *appconfig.Config, model string, profileMatched bool) modelsCatalogDriftDiagnostics {
	diagnostics := modelsCatalogDriftDiagnostics{
		CatalogSource: "unavailable",
		ChannelSource: "unavailable",
	}
	if cfg == nil || cfg.DatabaseDriver() != "sqlite" {
		return diagnostics
	}
	dbPath := cfg.DatabasePath()
	if strings.TrimSpace(dbPath) == "" || dbPath == ":memory:" {
		return diagnostics
	}
	if _, err := os.Stat(dbPath); err != nil {
		return diagnostics
	}
	outputDir := cfg.TraceOutputDir()
	if strings.TrimSpace(outputDir) == "" {
		outputDir = "."
	}

	st, err := store.NewWithDatabaseOptions(
		outputDir,
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
		store.DatabaseOptions{AutoMigrate: false},
	)
	if err != nil {
		return diagnostics
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Debug("Close application store after model diagnostics failed", "error", err)
		}
	}()

	model = strings.ToLower(strings.TrimSpace(model))
	channelModels, err := st.ListChannelModels("", false)
	if err != nil {
		return diagnostics
	}
	diagnostics.DatabaseAvailable = true
	diagnostics.ChannelSource = "missing"
	for _, channelModel := range channelModels {
		if strings.ToLower(strings.TrimSpace(channelModel.Model)) != model {
			continue
		}
		diagnostics.ChannelModelCount++
		diagnostics.ChannelModelPresent = true
		diagnostics.ChannelSource = "channel_models"
	}

	if _, err := st.GetModelCatalog(model); err == nil {
		diagnostics.CatalogModelPresent = true
		diagnostics.CatalogSource = "model_catalog"
	} else if errors.Is(err, sql.ErrNoRows) {
		diagnostics.CatalogSource = "missing"
	} else {
		return modelsCatalogDriftDiagnostics{
			CatalogSource: "unavailable",
			ChannelSource: "unavailable",
		}
	}

	diagnostics.DriftWarnings = modelsCatalogDriftWarnings(model, profileMatched, diagnostics)
	return diagnostics
}

func modelsCatalogDriftWarnings(model string, profileMatched bool, diagnostics modelsCatalogDriftDiagnostics) []string {
	if !diagnostics.DatabaseAvailable {
		return nil
	}
	warnings := make([]string, 0, 2)
	if profileMatched && !diagnostics.CatalogModelPresent {
		warnings = append(warnings, fmt.Sprintf("matched responses_server.model_profiles for model %q, but model_catalog has no entry for it", model))
	}
	if profileMatched && !diagnostics.ChannelModelPresent {
		warnings = append(warnings, fmt.Sprintf("matched responses_server.model_profiles for model %q, but channel_models has no entry for it", model))
	}
	if diagnostics.ChannelModelPresent && !diagnostics.CatalogModelPresent {
		warnings = append(warnings, fmt.Sprintf("channel_models contains model %q, but model_catalog has no entry for it", model))
	}
	if diagnostics.CatalogModelPresent && !diagnostics.ChannelModelPresent {
		warnings = append(warnings, fmt.Sprintf("model_catalog contains model %q, but channel_models has no entry for it", model))
	}
	return warnings
}

func diagnoseModelsCodexLocalConfig(path string, suggestion modelsCodexConfigResult) modelsCodexLocalConfig {
	path = strings.TrimSpace(path)
	diagnostics := modelsCodexLocalConfig{
		Path:          path,
		Status:        "not_configured",
		ProfileName:   suggestion.Model,
		ProviderName:  suggestion.Profile.ModelProvider,
		DriftWarnings: []string{},
	}
	if path == "" {
		return diagnostics
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			diagnostics.Status = "missing"
			diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file does not exist")
			return diagnostics
		}
		diagnostics.Status = "unreadable"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file is not readable")
		return diagnostics
	}
	diagnostics.Present = true
	if info.IsDir() {
		diagnostics.Status = "unreadable"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config path is a directory")
		return diagnostics
	}

	body, err := os.ReadFile(path)
	if err != nil {
		diagnostics.Status = "unreadable"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file is not readable")
		return diagnostics
	}
	diagnostics.Readable = true

	var document map[string]any
	if err := toml.Unmarshal(body, &document); err != nil {
		diagnostics.Status = "parse_error"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file is invalid TOML")
		return diagnostics
	}
	diagnostics.Parsed = true

	profiles := tomlChildMap(document, "profiles")
	profile := tomlChildMap(profiles, suggestion.Model)
	diagnostics.ProfilePresent = profile != nil
	if !diagnostics.ProfilePresent {
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config profile %q is missing", suggestion.Model))
	}

	providers := tomlChildMap(document, "model_providers")
	provider := tomlChildMap(providers, suggestion.Profile.ModelProvider)
	diagnostics.ProviderPresent = provider != nil
	if !diagnostics.ProviderPresent {
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config provider %q is missing", suggestion.Profile.ModelProvider))
	}

	diagnostics.Fields = append(diagnostics.Fields,
		compareModelsCodexStringField(profile, "profile.model_provider", "model_provider", suggestion.Profile.ModelProvider, false),
		compareModelsCodexStringField(profile, "profile.model", "model", suggestion.Profile.Model, false),
		compareModelsCodexIntField(profile, "profile.model_context_window", "model_context_window", suggestion.Profile.ModelContextWindow),
		compareModelsCodexIntField(profile, "profile.model_auto_compact_token_limit", "model_auto_compact_token_limit", suggestion.Profile.ModelAutoCompactTokenLimit),
		compareModelsCodexStringField(provider, "provider.base_url", "base_url", suggestion.Provider.BaseURL, true),
		compareModelsCodexStringField(provider, "provider.wire_api", "wire_api", suggestion.Provider.WireAPI, false),
		compareModelsCodexStringField(provider, "provider.env_key", "env_key", suggestion.Provider.EnvKey, false),
	)
	for _, field := range diagnostics.Fields {
		if field.Matched {
			continue
		}
		if !field.Present {
			diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config field %s is missing; expected %s", field.Field, field.Expected))
			continue
		}
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config field %s is %s; expected %s", field.Field, field.Actual, field.Expected))
	}

	if len(diagnostics.DriftWarnings) > 0 {
		diagnostics.Status = "drift"
		return diagnostics
	}
	diagnostics.Status = "ok"
	return diagnostics
}

func tomlChildMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	child, ok := parent[key]
	if !ok {
		return nil
	}
	childMap, ok := child.(map[string]any)
	if !ok {
		return nil
	}
	return childMap
}

func compareModelsCodexStringField(parent map[string]any, field string, key string, expected string, redactURL bool) modelsCodexLocalFieldStatus {
	status := modelsCodexLocalFieldStatus{
		Field:    field,
		Expected: expected,
	}
	if redactURL {
		status.Expected = redactURLLike(expected)
	}
	if parent == nil {
		return status
	}
	raw, ok := parent[key]
	if !ok {
		return status
	}
	status.Present = true
	actual, ok := raw.(string)
	if !ok {
		status.Actual = fmt.Sprintf("<%T>", raw)
		return status
	}
	status.Matched = actual == expected
	if redactURL {
		status.Actual = redactURLLike(actual)
	} else {
		status.Actual = actual
	}
	return status
}

func compareModelsCodexIntField(parent map[string]any, field string, key string, expected int) modelsCodexLocalFieldStatus {
	status := modelsCodexLocalFieldStatus{
		Field:    field,
		Expected: strconv.Itoa(expected),
	}
	if parent == nil {
		return status
	}
	raw, ok := parent[key]
	if !ok {
		return status
	}
	status.Present = true
	actual, ok := tomlInt(raw)
	if !ok {
		status.Actual = fmt.Sprintf("<%T>", raw)
		return status
	}
	status.Actual = strconv.Itoa(actual)
	status.Matched = actual == expected
	return status
}

func tomlInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case int32:
		return int(value), true
	case uint:
		if value > uint(^uint(0)>>1) {
			return 0, false
		}
		return int(value), true
	case uint64:
		if value > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(value), true
	case uint32:
		return int(value), true
	default:
		return 0, false
	}
}

func buildModelsCodexProviderConfig(cfg *appconfig.Config) (modelsCodexProviderConfig, []string) {
	warnings := []string{}
	origin, originWarnings := codexServerOrigin(cfg.Server.Port)
	warnings = append(warnings, originWarnings...)
	responsesPath := normalizeCodexResponsesPath(cfg.ResponsesServerPath())
	baseURLPath := strings.TrimSuffix(responsesPath, "/responses")
	baseURLSource := "responses_server.path without trailing /responses"
	if responsesPath == "/responses" {
		baseURLPath = ""
	}
	if !strings.HasSuffix(responsesPath, "/responses") {
		baseURLPath = strings.TrimRight(responsesPath, "/")
		baseURLSource = "responses_server.path"
		warnings = append(warnings, fmt.Sprintf("responses_server.path %q does not end with /responses; Codex wire_api=responses normally appends /responses to provider base_url", responsesPath))
	}
	baseURL := origin
	if baseURLPath != "" && baseURLPath != "/" {
		baseURL += baseURLPath
	}
	return modelsCodexProviderConfig{
		Name:                codexConfigProviderName,
		BaseURL:             baseURL,
		ResponsesPath:       responsesPath,
		ServerOrigin:        origin,
		EnvKey:              codexConfigAPIKeyEnv,
		WireAPI:             codexConfigWireAPI,
		RequestMaxRetries:   codexConfigRequestMaxRetries,
		StreamMaxRetries:    codexConfigStreamMaxRetries,
		StreamIdleTimeoutMS: codexConfigStreamIdleTimeout,
		BaseURLSource:       baseURLSource,
	}, warnings
}

func codexServerOrigin(port string) (string, []string) {
	port = strings.TrimSpace(strings.TrimPrefix(port, ":"))
	if port == "" {
		return "http://127.0.0.1", []string{"server.port is empty; provider base_url omits a port"}
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "http://127.0.0.1:" + port, []string{fmt.Sprintf("server.port %q is not a plain TCP port; provider base_url uses it verbatim", port)}
	}
	return "http://127.0.0.1:" + port, nil
}

func normalizeCodexResponsesPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/v1/responses"
	}
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func codexCompactHistoryItemThreshold(cfg *appconfig.Config, match appconfig.ResponsesModelProfileMatch) (int, string) {
	if match.Matched && match.Profile.CompactHistoryItemThreshold > 0 {
		return match.Profile.CompactHistoryItemThreshold, match.Source + ".compact_history_item_threshold"
	}
	if cfg.ResponsesCompactHistoryItemThreshold() > 0 {
		return cfg.ResponsesCompactHistoryItemThreshold(), "responses_server.compact_history_item_threshold"
	}
	return 0, "none"
}

func writeModelsCodexConfigText(w io.Writer, result modelsCodexConfigResult) {
	fmt.Fprintf(w, "# llm-tracelab Codex config suggestion\n")
	fmt.Fprintf(w, "# model: %s\n", result.Model)
	if len(result.Warnings) > 0 {
		fmt.Fprintln(w, "# warnings:")
		for _, warning := range result.Warnings {
			fmt.Fprintf(w, "# - %s\n", warning)
		}
	}
	fmt.Fprintf(w, "# diagnostics: database_available=%t catalog_model_present=%t channel_model_present=%t channel_model_count=%d catalog_source=%s channel_source=%s\n",
		result.Diagnostics.DatabaseAvailable,
		result.Diagnostics.CatalogModelPresent,
		result.Diagnostics.ChannelModelPresent,
		result.Diagnostics.ChannelModelCount,
		result.Diagnostics.CatalogSource,
		result.Diagnostics.ChannelSource,
	)
	fmt.Fprintf(w, "# profile_sources: runtime_profile_source=%s catalog_profile_role=%s capability_source=%s precedence=%s\n",
		result.Diagnostics.RuntimeProfileSource,
		result.Diagnostics.CatalogProfileRole,
		result.Diagnostics.CapabilitySource,
		strings.Join(result.Diagnostics.ProfilePrecedence, ","),
	)
	fmt.Fprintf(w, "# codex_config: status=%s present=%t readable=%t parsed=%t profile_present=%t provider_present=%t",
		result.Diagnostics.CodexConfig.Status,
		result.Diagnostics.CodexConfig.Present,
		result.Diagnostics.CodexConfig.Readable,
		result.Diagnostics.CodexConfig.Parsed,
		result.Diagnostics.CodexConfig.ProfilePresent,
		result.Diagnostics.CodexConfig.ProviderPresent,
	)
	if result.Diagnostics.CodexConfig.Path != "" {
		fmt.Fprintf(w, " path=%s", result.Diagnostics.CodexConfig.Path)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
	fmt.Fprint(w, result.TOML)
}

func modelsCodexConfigTOML(result modelsCodexConfigResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "model_provider = %s\n", tomlString(result.Profile.ModelProvider))
	fmt.Fprintf(&b, "model = %s\n", tomlString(result.Profile.Model))
	fmt.Fprintf(&b, "model_context_window = %d\n", result.Profile.ModelContextWindow)
	fmt.Fprintf(&b, "model_auto_compact_token_limit = %d\n\n", result.Profile.ModelAutoCompactTokenLimit)
	fmt.Fprintf(&b, "[model_providers.%s]\n", tomlBareKey(result.Profile.ModelProvider))
	fmt.Fprintf(&b, "name = %s\n", tomlString(result.Provider.Name))
	fmt.Fprintf(&b, "base_url = %s\n", tomlString(result.Provider.BaseURL))
	fmt.Fprintf(&b, "env_key = %s\n", tomlString(result.Provider.EnvKey))
	fmt.Fprintf(&b, "wire_api = %s\n", tomlString(result.Provider.WireAPI))
	fmt.Fprintf(&b, "request_max_retries = %d\n", result.Provider.RequestMaxRetries)
	fmt.Fprintf(&b, "stream_max_retries = %d\n", result.Provider.StreamMaxRetries)
	fmt.Fprintf(&b, "stream_idle_timeout_ms = %d\n", result.Provider.StreamIdleTimeoutMS)
	return b.String()
}

func tomlString(value string) string {
	return strconv.Quote(value)
}

func tomlBareKey(value string) string {
	if value == "" {
		return `""`
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return strconv.Quote(value)
	}
	return value
}
