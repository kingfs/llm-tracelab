package main

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
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
	configPath string
	format     string
	stdout     io.Writer
	model      string
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
	CompactLimitSource                string                    `json:"compact_limit_source"`
	CompactLimitMarginTokens          int                       `json:"compact_limit_margin_tokens"`
	CompactHistoryItemThreshold       int                       `json:"compact_history_item_threshold"`
	CompactHistoryItemThresholdSource string                    `json:"compact_history_item_threshold_source"`
	ResponsesServerEnabled            bool                      `json:"responses_server_enabled"`
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
	return &cobra.Command{
		Use:   "codex-config <model>",
		Short: "Generate a Codex profile TOML suggestion from local Responses model profiles",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runModelsCodexConfigWithOptions(modelsCodexConfigOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
					model:      args[0],
				})
			})
		},
	}
}

func runModelsCodexConfigWithOptions(opts modelsCodexConfigOptions) int {
	cfg, err := appconfig.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := buildModelsCodexConfigResult(cfg, opts.model)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, codexConfigCommand, result, func(w io.Writer) error {
		writeModelsCodexConfigText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write models codex-config result failed", "error", err)
		return 1
	}
	return 0
}

func buildModelsCodexConfigResult(cfg *appconfig.Config, model string) modelsCodexConfigResult {
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
		CompactLimitSource:                compactLimitSource,
		CompactLimitMarginTokens:          contextWindow - autoCompactLimit,
		CompactHistoryItemThreshold:       historyThreshold,
		CompactHistoryItemThresholdSource: historyThresholdSource,
		ResponsesServerEnabled:            cfg.ResponsesServerEnabled(),
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
	return result
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
