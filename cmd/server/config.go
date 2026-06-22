package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/spf13/cobra"
)

type configInspectOptions struct {
	configPath string
	format     string
	stdout     io.Writer
}

type configInspectResult struct {
	ConfigPath      string                     `json:"config_path"`
	Server          configInspectServer        `json:"server"`
	Monitor         configInspectMonitor       `json:"monitor"`
	MCP             configInspectMCP           `json:"mcp"`
	Database        configInspectDatabase      `json:"database"`
	Trace           configInspectTrace         `json:"trace"`
	ResponsesServer configInspectResponses     `json:"responses_server"`
	Tools           configInspectTools         `json:"tools"`
	ProviderProbe   configInspectProviderProbe `json:"provider_probe"`
	Upstreams       configInspectUpstreams     `json:"upstreams"`
}

type configInspectServer struct {
	Port string `json:"port"`
}

type configInspectMonitor struct {
	Port string `json:"port"`
}

type configInspectMCP struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type configInspectDatabase struct {
	Driver      string `json:"driver"`
	DSN         string `json:"dsn"`
	AutoMigrate bool   `json:"auto_migrate"`
}

type configInspectTrace struct {
	OutputDir string `json:"output_dir"`
}

type configInspectResponses struct {
	Enabled            bool                           `json:"enabled"`
	Path               string                         `json:"path"`
	DefaultModel       string                         `json:"default_model"`
	ForceStore         bool                           `json:"force_store"`
	MaxBody            int64                          `json:"max_body"`
	AutoCompact        bool                           `json:"auto_compact"`
	ModelProfilesCount int                            `json:"model_profiles_count"`
	FunctionExecutors  configInspectFunctionExecutors `json:"function_executors"`
}

type configInspectFunctionExecutors struct {
	Enabled bool `json:"enabled"`
}

type configInspectTools struct {
	WebSearch configInspectWebSearch `json:"web_search"`
}

type configInspectWebSearch struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
}

type configInspectProviderProbe struct {
	StartupFill bool   `json:"startup_fill"`
	Timeout     string `json:"timeout"`
}

type configInspectUpstreams struct {
	Targets []configInspectUpstreamTarget `json:"targets"`
}

type configInspectUpstreamTarget struct {
	ID               string                               `json:"id"`
	Enabled          bool                                 `json:"enabled"`
	BaseURL          string                               `json:"base_url"`
	APIType          string                               `json:"api_type"`
	ProtocolFamily   string                               `json:"protocol_family"`
	ProviderPreset   string                               `json:"provider_preset"`
	Mode             string                               `json:"mode"`
	Capabilities     appconfig.UpstreamCapabilitiesConfig `json:"capabilities"`
	ModelDiscovery   string                               `json:"model_discovery"`
	StaticModelCount int                                  `json:"static_model_count"`
	CredentialCount  int                                  `json:"credential_count"`
}

func newConfigCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "config",
		Short:         "Inspect effective configuration",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newConfigInspectCommand(runtime))
	return cmd
}

func newConfigInspectCommand(runtime *cliRuntime) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect",
		Short: "Print a redacted effective configuration summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runConfigInspectWithOptions(configInspectOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
}

func runConfigInspectWithOptions(opts configInspectOptions) int {
	cfg, err := appconfig.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := buildConfigInspectResult(opts.configPath, cfg)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "config.inspect", result, func(w io.Writer) error {
		writeConfigInspectText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write config inspect result failed", "error", err)
		return 1
	}
	return 0
}

func buildConfigInspectResult(configPath string, cfg *appconfig.Config) configInspectResult {
	if cfg == nil {
		cfg = &appconfig.Config{}
	}
	functionExecutors := cfg.ResponsesFunctionExecutorsConfig()
	result := configInspectResult{
		ConfigPath: configPath,
		Server: configInspectServer{
			Port: cfg.Server.Port,
		},
		Monitor: configInspectMonitor{
			Port: cfg.Monitor.Port,
		},
		MCP: configInspectMCP{
			Enabled: cfg.MCP.Enabled,
			Path:    configInspectMCPPath(cfg),
		},
		Database: configInspectDatabase{
			Driver:      cfg.DatabaseDriver(),
			DSN:         redactConfigInspectDSN(cfg.DatabaseDSN()),
			AutoMigrate: cfg.DatabaseAutoMigrate(),
		},
		Trace: configInspectTrace{
			OutputDir: cfg.TraceOutputDir(),
		},
		ResponsesServer: configInspectResponses{
			Enabled:            cfg.ResponsesServerEnabled(),
			Path:               cfg.ResponsesServerPath(),
			DefaultModel:       cfg.ResponsesDefaultModel(),
			ForceStore:         cfg.ResponsesForceStore(),
			MaxBody:            cfg.ResponsesMaxRequestBodyBytes(),
			AutoCompact:        cfg.ResponsesAutoCompactEnabled(),
			ModelProfilesCount: len(cfg.ResponsesModelProfiles()),
			FunctionExecutors: configInspectFunctionExecutors{
				Enabled: functionExecutors.Enabled,
			},
		},
		Tools: configInspectTools{
			WebSearch: configInspectWebSearch{
				Enabled:  cfg.Tools.WebSearch.Enabled,
				Provider: strings.TrimSpace(cfg.Tools.WebSearch.Provider),
				BaseURL:  redactURLLike(cfg.Tools.WebSearch.BaseURL),
			},
		},
		ProviderProbe: configInspectProviderProbe{
			StartupFill: cfg.ProviderProbeStartupFillEnabled(),
			Timeout:     cfg.ProviderProbeTimeout().String(),
		},
	}
	for _, target := range cfg.EffectiveUpstreams() {
		result.Upstreams.Targets = append(result.Upstreams.Targets, inspectUpstreamTarget(target))
	}
	return result
}

func configInspectMCPPath(cfg *appconfig.Config) string {
	if cfg == nil || !cfg.MCP.Enabled {
		return ""
	}
	normalized, err := normalizeMCPPath(cfg.MCP.Path)
	if err != nil {
		return strings.TrimSpace(cfg.MCP.Path)
	}
	return normalized
}

func inspectUpstreamTarget(target appconfig.UpstreamTargetConfig) configInspectUpstreamTarget {
	enabled := true
	if target.Enabled != nil {
		enabled = *target.Enabled
	}
	upstream := target.Upstream
	return configInspectUpstreamTarget{
		ID:               strings.TrimSpace(target.ID),
		Enabled:          enabled,
		BaseURL:          redactURLLike(upstream.BaseURL),
		APIType:          strings.TrimSpace(upstream.APIType),
		ProtocolFamily:   strings.TrimSpace(upstream.ProtocolFamily),
		ProviderPreset:   strings.TrimSpace(upstream.ProviderPreset),
		Mode:             strings.TrimSpace(upstream.Mode),
		Capabilities:     upstream.Capabilities,
		ModelDiscovery:   strings.TrimSpace(target.ModelDiscovery),
		StaticModelCount: len(target.StaticModels),
		CredentialCount:  len(target.EffectiveCredentials()),
	}
}

func redactURLLike(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(username, "<redacted>")
		} else if shouldRedactKey(username) {
			parsed.User = url.User("<redacted>")
		}
	}
	query := parsed.Query()
	for key, values := range query {
		if shouldRedactKey(key) {
			redacted := make([]string, len(values))
			for i := range redacted {
				redacted[i] = "<redacted>"
			}
			query[key] = redacted
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func redactConfigInspectDSN(raw string) string {
	redacted := redactURLLike(raw)
	redacted = appconfig.RedactDSN(redacted)
	return strings.ReplaceAll(redacted, "%3Credacted%3E", "<redacted>")
}

func shouldRedactKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"key", "token", "secret", "password", "passwd", "authorization", "auth"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func writeConfigInspectText(w io.Writer, result configInspectResult) {
	fmt.Fprintf(w, "config: %s\n", result.ConfigPath)
	fmt.Fprintf(w, "server: port=%s monitor_port=%s\n", result.Server.Port, result.Monitor.Port)
	fmt.Fprintf(w, "mcp: enabled=%t path=%s\n", result.MCP.Enabled, result.MCP.Path)
	fmt.Fprintf(w, "database: driver=%s dsn=%s auto_migrate=%t\n", result.Database.Driver, result.Database.DSN, result.Database.AutoMigrate)
	fmt.Fprintf(w, "trace: output_dir=%s\n", result.Trace.OutputDir)
	fmt.Fprintf(w, "responses_server: enabled=%t path=%s default_model=%s max_body=%d auto_compact=%t profiles=%d executors_enabled=%t\n",
		result.ResponsesServer.Enabled,
		result.ResponsesServer.Path,
		result.ResponsesServer.DefaultModel,
		result.ResponsesServer.MaxBody,
		result.ResponsesServer.AutoCompact,
		result.ResponsesServer.ModelProfilesCount,
		result.ResponsesServer.FunctionExecutors.Enabled,
	)
	fmt.Fprintf(w, "tools.web_search: enabled=%t provider=%s base_url=%s\n",
		result.Tools.WebSearch.Enabled,
		result.Tools.WebSearch.Provider,
		result.Tools.WebSearch.BaseURL,
	)
	fmt.Fprintf(w, "provider_probe: startup_fill=%t timeout=%s\n", result.ProviderProbe.StartupFill, result.ProviderProbe.Timeout)
	for _, target := range result.Upstreams.Targets {
		fmt.Fprintf(w, "upstream: id=%s enabled=%t base_url=%s api_type=%s protocol_family=%s provider_preset=%s mode=%s model_discovery=%s static_models=%d credentials=%d\n",
			target.ID,
			target.Enabled,
			target.BaseURL,
			target.APIType,
			target.ProtocolFamily,
			target.ProviderPreset,
			target.Mode,
			target.ModelDiscovery,
			target.StaticModelCount,
			target.CredentialCount,
		)
	}
}
