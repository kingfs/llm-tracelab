package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	configSourceConfigFile    = "config_file"
	configSourceDefault       = "default"
	configSourceEffective     = "effective"
	configSourceEmpty         = "empty"
	configSourceDerived       = "derived"
	configSourceNotConfigured = "not_configured"
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
	Sources         configInspectSources       `json:"sources"`
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

type configInspectSources struct {
	ConfigPath      string                              `json:"config_path"`
	Server          configInspectServerSources          `json:"server"`
	Monitor         configInspectMonitorSources         `json:"monitor"`
	MCP             configInspectMCPSources             `json:"mcp"`
	Database        configInspectDatabaseSources        `json:"database"`
	Trace           configInspectTraceSources           `json:"trace"`
	ResponsesServer configInspectResponsesSources       `json:"responses_server"`
	Tools           configInspectToolsSources           `json:"tools"`
	Upstreams       configInspectUpstreamsSourceSummary `json:"upstreams"`
}

type configInspectServerSources struct {
	Port string `json:"port"`
}

type configInspectMonitorSources struct {
	Port string `json:"port"`
}

type configInspectMCPSources struct {
	Enabled string `json:"enabled"`
	Path    string `json:"path"`
}

type configInspectDatabaseSources struct {
	Driver      string `json:"driver"`
	DSN         string `json:"dsn"`
	AutoMigrate string `json:"auto_migrate"`
}

type configInspectTraceSources struct {
	OutputDir string `json:"output_dir"`
}

type configInspectResponsesSources struct {
	Enabled      string `json:"enabled"`
	Path         string `json:"path"`
	DefaultModel string `json:"default_model"`
}

type configInspectToolsSources struct {
	WebSearch configInspectWebSearchSources `json:"web_search"`
}

type configInspectWebSearchSources struct {
	Enabled  string `json:"enabled"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
}

type configInspectUpstreamsSourceSummary struct {
	Targets     string `json:"targets"`
	Credentials string `json:"credentials"`
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
	webSearch := cfg.WebSearchConfig()
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
				Enabled:  webSearch.Enabled,
				Provider: webSearch.Provider,
				BaseURL:  redactURLLike(webSearch.BaseURL),
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
	result.Sources = buildConfigInspectSources(configPath, cfg)
	return result
}

func buildConfigInspectSources(configPath string, cfg *appconfig.Config) configInspectSources {
	probe := loadConfigSourceProbe(configPath)
	return configInspectSources{
		ConfigPath: configPathSource(configPath),
		Server: configInspectServerSources{
			Port: probe.stringFieldSource("server.port", cfg.Server.Port, "LLM_TRACELAB_SERVER_PORT"),
		},
		Monitor: configInspectMonitorSources{
			Port: probe.stringFieldSource("monitor.port", cfg.Monitor.Port, "LLM_TRACELAB_MONITOR_PORT"),
		},
		MCP: configInspectMCPSources{
			Enabled: probe.boolFieldSource("mcp.enabled", "LLM_TRACELAB_MCP_ENABLED"),
			Path:    probe.stringFieldSource("mcp.path", configInspectMCPPath(cfg), "LLM_TRACELAB_MCP_PATH"),
		},
		Database: configInspectDatabaseSources{
			Driver:      probe.defaultableStringFieldSource("database.driver", cfg.Database.Driver, "LLM_TRACELAB_DATABASE_DRIVER"),
			DSN:         probe.databaseDSNSource(cfg),
			AutoMigrate: probe.pointerBoolFieldSource("database.auto_migrate", cfg.Database.AutoMigrate, "LLM_TRACELAB_DATABASE_AUTO_MIGRATE"),
		},
		Trace: configInspectTraceSources{
			OutputDir: probe.traceOutputDirSource(cfg),
		},
		ResponsesServer: configInspectResponsesSources{
			Enabled:      probe.boolFieldSource("responses_server.enabled", "LLM_TRACELAB_RESPONSES_ENABLED"),
			Path:         probe.defaultableStringFieldSource("responses_server.path", cfg.ResponsesServer.Path, "LLM_TRACELAB_RESPONSES_PATH"),
			DefaultModel: probe.stringFieldSource("responses_server.default_model", cfg.ResponsesDefaultModel(), "LLM_TRACELAB_RESPONSES_DEFAULT_MODEL"),
		},
		Tools: configInspectToolsSources{
			WebSearch: configInspectWebSearchSources{
				Enabled:  probe.boolFieldSource("tools.web_search.enabled", "LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED"),
				Provider: probe.defaultableStringFieldSource("tools.web_search.provider", cfg.Tools.WebSearch.Provider, "LLM_TRACELAB_TOOLS_WEB_SEARCH_PROVIDER"),
				BaseURL:  probe.stringFieldSource("tools.web_search.base_url", cfg.Tools.WebSearch.BaseURL, "LLM_TRACELAB_TOOLS_WEB_SEARCH_BASE_URL"),
			},
		},
		Upstreams: probe.upstreamsSourceSummary(cfg),
	}
}

func configPathSource(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return configSourceNotConfigured
	}
	return configSourceEffective
}

type configSourceProbe struct {
	fields map[string]struct{}
}

func loadConfigSourceProbe(configPath string) configSourceProbe {
	probe := configSourceProbe{fields: make(map[string]struct{})}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return probe
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return probe
	}
	node := &root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		node = root.Content[0]
	}
	collectYAMLFields(node, "", probe.fields)
	return probe
}

func collectYAMLFields(node *yaml.Node, prefix string, fields map[string]struct{}) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := strings.TrimSpace(node.Content[i].Value)
		if key == "" {
			continue
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		fields[path] = struct{}{}
		collectYAMLFields(node.Content[i+1], path, fields)
	}
}

func (p configSourceProbe) has(path string) bool {
	_, ok := p.fields[path]
	return ok
}

func (p configSourceProbe) stringFieldSource(path string, effectiveValue string, envNames ...string) string {
	if anyStringEnvSet(envNames...) {
		return configSourceEffective
	}
	if p.has(path) {
		return configSourceConfigFile
	}
	if strings.TrimSpace(effectiveValue) == "" {
		return configSourceEmpty
	}
	return configSourceEffective
}

func (p configSourceProbe) defaultableStringFieldSource(path string, rawValue string, envNames ...string) string {
	if anyStringEnvSet(envNames...) {
		return configSourceEffective
	}
	if p.has(path) {
		return configSourceConfigFile
	}
	if strings.TrimSpace(rawValue) == "" {
		return configSourceDefault
	}
	return configSourceEffective
}

func (p configSourceProbe) boolFieldSource(path string, envNames ...string) string {
	if anyBoolEnvSet(envNames...) {
		return configSourceEffective
	}
	if p.has(path) {
		return configSourceConfigFile
	}
	return configSourceDefault
}

func (p configSourceProbe) pointerBoolFieldSource(path string, rawValue *bool, envNames ...string) string {
	if anyBoolEnvSet(envNames...) {
		return configSourceEffective
	}
	if p.has(path) {
		return configSourceConfigFile
	}
	if rawValue == nil {
		return configSourceDefault
	}
	return configSourceEffective
}

func (p configSourceProbe) traceOutputDirSource(cfg *appconfig.Config) string {
	if anyStringEnvSet("LLM_TRACELAB_TRACE_OUTPUT_DIR") {
		return configSourceEffective
	}
	if anyStringEnvSet("LLM_TRACELAB_OUTPUT_DIR") {
		return configSourceEffective
	}
	if p.has("trace.output_dir") {
		return configSourceConfigFile
	}
	if p.has("debug.output_dir") && strings.TrimSpace(cfg.Trace.OutputDir) == "" {
		return configSourceDerived
	}
	if strings.TrimSpace(cfg.TraceOutputDir()) == "" {
		return configSourceEmpty
	}
	return configSourceEffective
}

func (p configSourceProbe) databaseDSNSource(cfg *appconfig.Config) string {
	if anyStringEnvSet("LLM_TRACELAB_DATABASE_DSN") {
		return configSourceEffective
	}
	if p.has("database.dsn") {
		return configSourceConfigFile
	}
	if strings.TrimSpace(cfg.DatabaseDSN()) == "" {
		return configSourceEmpty
	}
	return configSourceDerived
}

func (p configSourceProbe) upstreamsSourceSummary(cfg *appconfig.Config) configInspectUpstreamsSourceSummary {
	targets := configSourceDefault
	credentials := configSourceNotConfigured
	if p.has("upstreams") {
		targets = configSourceConfigFile
		credentials = configSourceConfigFile
	}
	if p.has("upstream") && !p.has("upstreams") {
		targets = configSourceConfigFile
	}
	if anyStringEnvSet(
		"LLM_TRACELAB_UPSTREAM_BASE_URL",
		"LLM_TRACELAB_UPSTREAM_API_KEY",
		"LLM_TRACELAB_UPSTREAM_PROVIDER_PRESET",
		"LLM_TRACELAB_UPSTREAM_API_TYPE",
		"LLM_TRACELAB_UPSTREAM_MODE",
		"LLM_TRACELAB_UPSTREAM_PROTOCOL_FAMILY",
		"LLM_TRACELAB_UPSTREAM_ROUTING_PROFILE",
		"LLM_TRACELAB_UPSTREAM_API_VERSION",
		"LLM_TRACELAB_UPSTREAM_DEPLOYMENT",
		"LLM_TRACELAB_UPSTREAM_PROJECT",
		"LLM_TRACELAB_UPSTREAM_LOCATION",
		"LLM_TRACELAB_UPSTREAM_MODEL_RESOURCE",
	) {
		targets = configSourceEffective
	}
	if anyStringEnvSet("LLM_TRACELAB_UPSTREAM_API_KEY") {
		credentials = configSourceEffective
	}
	if strings.TrimSpace(cfg.Upstream.ApiKey) != "" && len(cfg.Upstreams) == 0 && credentials == configSourceNotConfigured {
		credentials = configSourceDerived
	}
	return configInspectUpstreamsSourceSummary{
		Targets:     targets,
		Credentials: credentials,
	}
}

func anyStringEnvSet(names ...string) bool {
	for _, name := range names {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

func anyBoolEnvSet(names ...string) bool {
	for _, name := range names {
		if raw := os.Getenv(name); raw != "" {
			if _, err := strconv.ParseBool(raw); err == nil {
				return true
			}
		}
	}
	return false
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
	fmt.Fprintf(w, "sources: config_path=%s server.port=%s monitor.port=%s database.driver=%s database.dsn=%s trace.output_dir=%s responses_server.default_model=%s tools.web_search.provider=%s upstreams.targets=%s upstreams.credentials=%s\n",
		result.Sources.ConfigPath,
		result.Sources.Server.Port,
		result.Sources.Monitor.Port,
		result.Sources.Database.Driver,
		result.Sources.Database.DSN,
		result.Sources.Trace.OutputDir,
		result.Sources.ResponsesServer.DefaultModel,
		result.Sources.Tools.WebSearch.Provider,
		result.Sources.Upstreams.Targets,
		result.Sources.Upstreams.Credentials,
	)
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
