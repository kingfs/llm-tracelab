package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`

	Monitor struct {
		Port string `yaml:"port"`
	} `yaml:"monitor"`

	MCP struct {
		Enabled bool   `yaml:"enabled"`
		Path    string `yaml:"path"`
	} `yaml:"mcp"`

	Auth struct {
		DatabasePath string        `yaml:"database_path"`
		SessionTTL   time.Duration `yaml:"session_ttl"`
	} `yaml:"auth"`

	Database struct {
		Driver       string `yaml:"driver"`
		DSN          string `yaml:"dsn"`
		MaxOpenConns int    `yaml:"max_open_conns"`
		MaxIdleConns int    `yaml:"max_idle_conns"`
		AutoMigrate  *bool  `yaml:"auto_migrate"`
	} `yaml:"database"`

	Trace struct {
		OutputDir string `yaml:"output_dir"`
	} `yaml:"trace"`

	Upstream  UpstreamConfig         `yaml:"upstream"`
	Upstreams []UpstreamTargetConfig `yaml:"upstreams"`

	ProviderProbe ProviderProbeConfig `yaml:"provider_probe"`

	Router RouterConfig `yaml:"router"`

	Limits LimitConfig `yaml:"limits"`

	ResponsesServer ResponsesServerConfig `yaml:"responses_server"`

	Tools ToolsConfig `yaml:"tools"`

	Debug struct {
		OutputDir string `yaml:"output_dir"`
		MaskKey   bool   `yaml:"mask_key"`
	} `yaml:"debug"`

	// 新增 Chaos 配置
	Chaos struct {
		Enabled bool        `yaml:"enabled"`
		Rules   []ChaosRule `yaml:"rules"`
	} `yaml:"chaos"`
}

type UpstreamConfig struct {
	BaseURL        string                     `yaml:"base_url"`
	ApiKey         string                     `yaml:"api_key"`
	ProviderPreset string                     `yaml:"provider_preset"`
	APIType        string                     `yaml:"api_type"`
	Mode           string                     `yaml:"mode"`
	Capabilities   UpstreamCapabilitiesConfig `yaml:"capabilities"`
	ProtocolFamily string                     `yaml:"protocol_family"`
	RoutingProfile string                     `yaml:"routing_profile"`
	APIVersion     string                     `yaml:"api_version"`
	Deployment     string                     `yaml:"deployment"`
	Project        string                     `yaml:"project"`
	Location       string                     `yaml:"location"`
	ModelResource  string                     `yaml:"model_resource"`
	Headers        map[string]string          `yaml:"headers"`
}

type UpstreamCapabilitiesConfig struct {
	Responses       *bool `yaml:"responses" json:"responses,omitempty"`
	ChatCompletions *bool `yaml:"chat_completions" json:"chat_completions,omitempty"`
	ToolCalling     *bool `yaml:"tool_calling" json:"tool_calling,omitempty"`
	Embeddings      *bool `yaml:"embeddings" json:"embeddings,omitempty"`
	Models          *bool `yaml:"models" json:"models,omitempty"`
	Tokenize        *bool `yaml:"tokenize" json:"tokenize,omitempty"`
}

type UpstreamTargetConfig struct {
	ID                 string             `yaml:"id"`
	Enabled            *bool              `yaml:"enabled"`
	Priority           int                `yaml:"priority"`
	Weight             float64            `yaml:"weight"`
	CapacityHint       float64            `yaml:"capacity_hint"`
	ModelDiscovery     string             `yaml:"model_discovery"`
	StaticModels       []string           `yaml:"static_models"`
	AllowUnknownModels *bool              `yaml:"allow_unknown_models"`
	Upstream           UpstreamConfig     `yaml:"upstream"`
	Credentials        []CredentialConfig `yaml:"credentials"`
}

type ProviderProbeConfig struct {
	StartupFill bool          `yaml:"startup_fill"`
	Timeout     time.Duration `yaml:"timeout"`
}

type CredentialConfig struct {
	ID               string            `yaml:"id"`
	Name             string            `yaml:"name"`
	Enabled          *bool             `yaml:"enabled"`
	ApiKey           string            `yaml:"api_key"`
	Headers          map[string]string `yaml:"headers"`
	ConcurrencyLimit int               `yaml:"concurrency_limit"`
}

type RouterConfig struct {
	ModelDiscovery struct {
		Enabled         *bool         `yaml:"enabled"`
		RefreshInterval time.Duration `yaml:"refresh_interval"`
		StartupPolicy   string        `yaml:"startup_policy"`
	} `yaml:"model_discovery"`
	Selection struct {
		Policy           string        `yaml:"policy"`
		Epsilon          float64       `yaml:"epsilon"`
		OpenWindow       time.Duration `yaml:"open_window"`
		FailureThreshold int64         `yaml:"failure_threshold"`
	} `yaml:"selection"`
	Fallback struct {
		OnMissingModel string `yaml:"on_missing_model"`
	} `yaml:"fallback"`
}

type LimitConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Scope            string `yaml:"scope"`
	MaxConcurrent    int    `yaml:"max_concurrent"`
	MaxQueued        int    `yaml:"max_queued"`
	ChannelKeyHeader string `yaml:"channel_key_header"`
}

type ResponsesServerConfig struct {
	Enabled                     bool                            `yaml:"enabled"`
	DefaultModel                string                          `yaml:"default_model"`
	ForceStore                  bool                            `yaml:"force_store"`
	MaxRequestBodyBytes         int64                           `yaml:"max_request_body_bytes"`
	Path                        string                          `yaml:"path"`
	AutoCompact                 bool                            `yaml:"auto_compact"`
	CompactHistoryItemThreshold int                             `yaml:"compact_history_item_threshold"`
	ModelProfiles               []ResponsesModelProfileConfig   `yaml:"model_profiles"`
	FunctionExecutors           ResponsesFunctionExecutorConfig `yaml:"function_executors"`
}

type ResponsesModelProfileConfig struct {
	Name                        string                         `yaml:"name"`
	Pattern                     string                         `yaml:"pattern"`
	ContextWindowTokens         int                            `yaml:"context_window_tokens"`
	MaxOutputTokens             int                            `yaml:"max_output_tokens"`
	CompactHistoryItemThreshold int                            `yaml:"compact_history_item_threshold"`
	UpstreamModel               string                         `yaml:"upstream_model"`
	TokenizeCounter             ResponsesTokenizeCounterConfig `yaml:"tokenize_counter"`
}

type ResponsesTokenizeCounterConfig struct {
	Enabled    *bool         `yaml:"enabled"`
	UpstreamID string        `yaml:"upstream_id"`
	Timeout    time.Duration `yaml:"timeout"`
}

type ResponsesFunctionExecutorConfig struct {
	Enabled        bool                               `yaml:"enabled"`
	Timeout        time.Duration                      `yaml:"timeout"`
	MaxResultBytes int                                `yaml:"max_result_bytes"`
	Redaction      ResponsesFunctionRedactionConfig   `yaml:"redaction"`
	Executors      []ResponsesFunctionExecutorBinding `yaml:"executors"`
	Warnings       []string                           `yaml:"-" json:"-"`
}

type ResponsesFunctionRedactionConfig struct {
	Arguments bool `yaml:"arguments"`
	Output    bool `yaml:"output"`
}

type ResponsesFunctionExecutorBinding struct {
	Name         string                                 `yaml:"name"`
	Type         string                                 `yaml:"type"`
	Enabled      *bool                                  `yaml:"enabled"`
	Output       any                                    `yaml:"output"`
	Command      string                                 `yaml:"command"`
	Args         []string                               `yaml:"args"`
	Timeout      time.Duration                          `yaml:"timeout"`
	Env          map[string]string                      `yaml:"env"`
	EnvAllowlist []string                               `yaml:"env_allowlist"`
	Process      ResponsesFunctionExecutorProcessConfig `yaml:"process"`
	Available    bool                                   `yaml:"-" json:"-"`
	Warnings     []string                               `yaml:"-" json:"-"`
}

type ResponsesFunctionExecutorProcessConfig struct {
	WorkingDir             string   `yaml:"working_dir"`
	RequireAbsoluteCommand bool     `yaml:"require_absolute_command"`
	AllowedCommandDirs     []string `yaml:"allowed_command_dirs"`
	RejectRoot             bool     `yaml:"reject_root"`
}

const (
	ResponsesFunctionExecutorTypeStaticResponse  = "static_response"
	ResponsesFunctionExecutorTypeExternalCommand = "external_command"
)

func SupportedResponsesFunctionExecutorTypes() []string {
	return []string{
		ResponsesFunctionExecutorTypeStaticResponse,
		ResponsesFunctionExecutorTypeExternalCommand,
	}
}

type ToolsConfig struct {
	WebSearch WebSearchToolConfig `yaml:"web_search"`
}

type WebSearchToolConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"`
	MaxResults int    `yaml:"max_results"`
	BaseURL    string `yaml:"base_url"`
	TimeoutMS  int    `yaml:"timeout_ms"`
	UserAgent  string `yaml:"user_agent"`
}

func (c LimitConfig) LocalConcurrencyEnabled() bool {
	return c.Enabled && c.MaxConcurrent > 0
}

func (c LimitConfig) ScopeOrDefault() string {
	scope := strings.ToLower(strings.TrimSpace(c.Scope))
	if scope != "" {
		return scope
	}
	if strings.TrimSpace(c.ChannelKeyHeader) != "" {
		return "header"
	}
	return "global"
}

type ChaosRule struct {
	Model      string        `yaml:"model"`       // 针对的模型，"*" 代表所有
	Rate       float64       `yaml:"rate"`        // 概率 0.0 ~ 1.0
	Action     string        `yaml:"action"`      // "delay" 或 "error"
	Delay      time.Duration `yaml:"delay"`       // 延迟时间
	StatusCode int           `yaml:"status_code"` // 错误码
	Message    string        `yaml:"message"`     // 错误内容
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	applyEnvOverrides(&cfg)
	if err := expandEnvRefs(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("LLM_TRACELAB_SERVER_PORT"); v != "" {
		cfg.Server.Port = v
	}
	if v := os.Getenv("LLM_TRACELAB_MONITOR_PORT"); v != "" {
		cfg.Monitor.Port = v
	}
	if v := os.Getenv("LLM_TRACELAB_MCP_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.MCP.Enabled = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_MCP_PATH"); v != "" {
		cfg.MCP.Path = v
	}
	if v := os.Getenv("LLM_TRACELAB_AUTH_DATABASE_PATH"); v != "" {
		cfg.Auth.DatabasePath = v
	}
	if v := os.Getenv("LLM_TRACELAB_AUTH_SESSION_TTL"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			cfg.Auth.SessionTTL = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_DATABASE_DRIVER"); v != "" {
		cfg.Database.Driver = v
	}
	if v := os.Getenv("LLM_TRACELAB_DATABASE_DSN"); v != "" {
		cfg.Database.DSN = v
	}
	if v := os.Getenv("LLM_TRACELAB_DATABASE_MAX_OPEN_CONNS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Database.MaxOpenConns = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_DATABASE_MAX_IDLE_CONNS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Database.MaxIdleConns = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_DATABASE_AUTO_MIGRATE"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Database.AutoMigrate = &parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_BASE_URL"); v != "" {
		cfg.Upstream.BaseURL = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.BaseURL = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_API_KEY"); v != "" {
		cfg.Upstream.ApiKey = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.ApiKey = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_PROVIDER_PRESET"); v != "" {
		cfg.Upstream.ProviderPreset = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.ProviderPreset = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_API_TYPE"); v != "" {
		cfg.Upstream.APIType = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.APIType = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_MODE"); v != "" {
		cfg.Upstream.Mode = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.Mode = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_PROTOCOL_FAMILY"); v != "" {
		cfg.Upstream.ProtocolFamily = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.ProtocolFamily = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_ROUTING_PROFILE"); v != "" {
		cfg.Upstream.RoutingProfile = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.RoutingProfile = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_API_VERSION"); v != "" {
		cfg.Upstream.APIVersion = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.APIVersion = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_DEPLOYMENT"); v != "" {
		cfg.Upstream.Deployment = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.Deployment = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_PROJECT"); v != "" {
		cfg.Upstream.Project = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.Project = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_LOCATION"); v != "" {
		cfg.Upstream.Location = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.Location = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_MODEL_RESOURCE"); v != "" {
		cfg.Upstream.ModelResource = v
		applyFirstUpstreamOverride(cfg, func(upstream *UpstreamConfig) {
			upstream.ModelResource = v
		})
	}
	if v := os.Getenv("LLM_TRACELAB_PROVIDER_PROBE_STARTUP_FILL"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ProviderProbe.StartupFill = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_PROVIDER_PROBE_TIMEOUT"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			cfg.ProviderProbe.Timeout = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_OUTPUT_DIR"); v != "" {
		cfg.Debug.OutputDir = v
		cfg.Trace.OutputDir = v
	}
	if v := os.Getenv("LLM_TRACELAB_TRACE_OUTPUT_DIR"); v != "" {
		cfg.Trace.OutputDir = v
	}
	if v := os.Getenv("LLM_TRACELAB_MASK_KEY"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Debug.MaskKey = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ResponsesServer.Enabled = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_DEFAULT_MODEL"); v != "" {
		cfg.ResponsesServer.DefaultModel = v
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_FORCE_STORE"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ResponsesServer.ForceStore = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_MAX_REQUEST_BODY_BYTES"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.ResponsesServer.MaxRequestBodyBytes = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_PATH"); v != "" {
		cfg.ResponsesServer.Path = v
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_AUTO_COMPACT"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ResponsesServer.AutoCompact = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_COMPACT_HISTORY_ITEM_THRESHOLD"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.ResponsesServer.CompactHistoryItemThreshold = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ResponsesServer.FunctionExecutors.Enabled = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_TIMEOUT"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			cfg.ResponsesServer.FunctionExecutors.Timeout = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_MAX_RESULT_BYTES"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.ResponsesServer.FunctionExecutors.MaxResultBytes = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_REDACT_ARGUMENTS"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ResponsesServer.FunctionExecutors.Redaction.Arguments = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_REDACT_OUTPUT"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.ResponsesServer.FunctionExecutors.Redaction.Output = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Tools.WebSearch.Enabled = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_PROVIDER"); v != "" {
		cfg.Tools.WebSearch.Provider = v
	}
	if v := os.Getenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_MAX_RESULTS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Tools.WebSearch.MaxResults = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_BASE_URL"); v != "" {
		cfg.Tools.WebSearch.BaseURL = v
	}
	if v := os.Getenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_TIMEOUT_MS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.Tools.WebSearch.TimeoutMS = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_USER_AGENT"); v != "" {
		cfg.Tools.WebSearch.UserAgent = v
	}
}

func applyFirstUpstreamOverride(cfg *Config, apply func(*UpstreamConfig)) {
	if len(cfg.Upstreams) == 0 {
		return
	}
	apply(&cfg.Upstreams[0].Upstream)
}

func expandEnvRefs(target any) error {
	return expandEnvValue(reflect.ValueOf(target), "")
}

func expandEnvValue(value reflect.Value, path string) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		return expandEnvValue(value.Elem(), path)
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			fieldType := value.Type().Field(i)
			if fieldType.PkgPath != "" {
				continue
			}
			nextPath := fieldType.Name
			if path != "" {
				nextPath = path + "." + nextPath
			}
			if err := expandEnvValue(field, nextPath); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if err := expandEnvValue(value.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, key := range value.MapKeys() {
			mapValue := value.MapIndex(key)
			if mapValue.Kind() == reflect.String {
				expanded, err := expandEnvString(mapValue.String(), fmt.Sprintf("%s[%s]", path, key.String()))
				if err != nil {
					return err
				}
				value.SetMapIndex(key, reflect.ValueOf(expanded))
				continue
			}
			copyValue := reflect.New(mapValue.Type()).Elem()
			copyValue.Set(mapValue)
			if err := expandEnvValue(copyValue, fmt.Sprintf("%s[%s]", path, key.String())); err != nil {
				return err
			}
			value.SetMapIndex(key, copyValue)
		}
	case reflect.String:
		if !value.CanSet() {
			return nil
		}
		expanded, err := expandEnvString(value.String(), path)
		if err != nil {
			return err
		}
		value.SetString(expanded)
	}
	return nil
}

func expandEnvString(raw string, path string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "$env:") {
		return raw, nil
	}
	name := strings.TrimSpace(strings.TrimPrefix(trimmed, "$env:"))
	if name == "" {
		return "", fmt.Errorf("empty env reference at %s", path)
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("environment variable %s referenced at %s is not set", name, path)
	}
	return value, nil
}

func (c Config) EffectiveUpstreams() []UpstreamTargetConfig {
	if len(c.Upstreams) > 0 {
		return append([]UpstreamTargetConfig(nil), c.Upstreams...)
	}

	enabled := true
	return []UpstreamTargetConfig{
		{
			ID:       "default",
			Enabled:  &enabled,
			Priority: 100,
			Weight:   1,
			Upstream: c.Upstream,
		},
	}
}

func (t UpstreamTargetConfig) HasExplicitCredentials() bool {
	return len(t.Credentials) > 0
}

func (t UpstreamTargetConfig) EffectiveCredentials() []CredentialConfig {
	if len(t.Credentials) > 0 {
		return cloneCredentialConfigs(t.Credentials)
	}
	if strings.TrimSpace(t.Upstream.ApiKey) == "" {
		return nil
	}
	enabled := true
	return []CredentialConfig{
		{
			ID:      "default",
			Name:    "default",
			Enabled: &enabled,
			ApiKey:  t.Upstream.ApiKey,
		},
	}
}

func cloneCredentialConfigs(in []CredentialConfig) []CredentialConfig {
	out := make([]CredentialConfig, len(in))
	for i, credential := range in {
		out[i] = credential
		if credential.Headers != nil {
			out[i].Headers = make(map[string]string, len(credential.Headers))
			for key, value := range credential.Headers {
				out[i].Headers[key] = value
			}
		}
	}
	return out
}

func (c Config) AuthDatabasePath() string {
	if strings.TrimSpace(c.Auth.DatabasePath) != "" {
		return c.Auth.DatabasePath
	}
	return c.DatabasePath()
}

func (c Config) AuthSessionTTL() time.Duration {
	if c.Auth.SessionTTL > 0 {
		return c.Auth.SessionTTL
	}
	return 24 * time.Hour
}

func (c Config) TraceOutputDir() string {
	if strings.TrimSpace(c.Trace.OutputDir) != "" {
		return c.Trace.OutputDir
	}
	return c.Debug.OutputDir
}

func (c Config) DatabaseDriver() string {
	if strings.TrimSpace(c.Database.Driver) != "" {
		return strings.ToLower(strings.TrimSpace(c.Database.Driver))
	}
	return "sqlite"
}

func (c Config) DatabasePath() string {
	if strings.TrimSpace(c.Database.DSN) != "" && c.DatabaseDriver() == "sqlite" {
		if path := SQLitePathFromDSN(c.Database.DSN); path != "" {
			return path
		}
	}
	if strings.TrimSpace(c.Auth.DatabasePath) != "" {
		return c.Auth.DatabasePath
	}
	return filepath.Join(c.TraceOutputDir(), "llm_tracelab.sqlite3")
}

func SQLitePathFromDSN(dsn string) string {
	path := strings.TrimSpace(dsn)
	if path == "" {
		return ""
	}
	path = strings.TrimPrefix(path, "sqlite://")
	path = strings.TrimPrefix(path, "file:")
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimSpace(path)
}

func (c Config) DatabaseDSN() string {
	if strings.TrimSpace(c.Database.DSN) != "" {
		return c.Database.DSN
	}
	if c.DatabaseDriver() == "sqlite" {
		return c.DatabasePath()
	}
	return ""
}

func RedactDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return redactURLPassword(dsn)
	}
	if strings.HasPrefix(dsn, "mysql://") {
		return redactURLPassword(dsn)
	}
	if lowerDSN := strings.ToLower(dsn); strings.Contains(lowerDSN, "password=") || strings.Contains(lowerDSN, "passwd=") {
		parts := strings.Fields(dsn)
		for i, part := range parts {
			lower := strings.ToLower(part)
			if strings.HasPrefix(lower, "password=") || strings.HasPrefix(lower, "passwd=") {
				key, _, _ := strings.Cut(part, "=")
				parts[i] = key + "=<redacted>"
			}
		}
		return strings.Join(parts, " ")
	}
	return dsn
}

func redactURLPassword(dsn string) string {
	parts := strings.SplitN(dsn, "://", 2)
	if len(parts) != 2 {
		return dsn
	}
	scheme, rest := parts[0], parts[1]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return dsn
	}
	userInfo := rest[:at]
	if !strings.Contains(userInfo, ":") {
		return dsn
	}
	user, _, _ := strings.Cut(userInfo, ":")
	return scheme + "://" + user + ":<redacted>@" + rest[at+1:]
}

func (c Config) DatabaseAutoMigrate() bool {
	if c.Database.AutoMigrate != nil {
		return *c.Database.AutoMigrate
	}
	return true
}

func (c Config) DatabaseMaxOpenConns() int {
	if c.Database.MaxOpenConns > 0 {
		return c.Database.MaxOpenConns
	}
	return 4
}

func (c Config) DatabaseMaxIdleConns() int {
	if c.Database.MaxIdleConns > 0 {
		return c.Database.MaxIdleConns
	}
	return 4
}

func (c Config) ProviderProbeStartupFillEnabled() bool {
	return c.ProviderProbe.StartupFill
}

func (c Config) ProviderProbeTimeout() time.Duration {
	if c.ProviderProbe.Timeout > 0 {
		return c.ProviderProbe.Timeout
	}
	return 10 * time.Second
}

func (c Config) ResponsesServerEnabled() bool {
	return c.ResponsesServer.Enabled
}

func (c Config) ResponsesDefaultModel() string {
	return strings.TrimSpace(c.ResponsesServer.DefaultModel)
}

func (c Config) ResponsesForceStore() bool {
	return c.ResponsesServer.ForceStore
}

func (c Config) ResponsesMaxRequestBodyBytes() int64 {
	if c.ResponsesServer.MaxRequestBodyBytes > 0 {
		return c.ResponsesServer.MaxRequestBodyBytes
	}
	return 16 << 20
}

func (c Config) ResponsesServerPath() string {
	if path := strings.TrimSpace(c.ResponsesServer.Path); path != "" {
		return path
	}
	return "/v1/responses"
}

func (c Config) ResponsesAutoCompactEnabled() bool {
	return c.ResponsesServer.AutoCompact
}

func (c Config) ResponsesCompactHistoryItemThreshold() int {
	if c.ResponsesServer.CompactHistoryItemThreshold > 0 {
		return c.ResponsesServer.CompactHistoryItemThreshold
	}
	return 0
}

func (c Config) ResponsesModelProfiles() []ResponsesModelProfileConfig {
	profiles := make([]ResponsesModelProfileConfig, 0, len(c.ResponsesServer.ModelProfiles))
	for _, profile := range c.ResponsesServer.ModelProfiles {
		profile.Name = strings.TrimSpace(profile.Name)
		profile.Pattern = strings.TrimSpace(profile.Pattern)
		profile.UpstreamModel = strings.TrimSpace(profile.UpstreamModel)
		profile.TokenizeCounter.UpstreamID = strings.TrimSpace(profile.TokenizeCounter.UpstreamID)
		profiles = append(profiles, profile)
	}
	return profiles
}

func (c Config) ResponsesFunctionExecutorsConfig() ResponsesFunctionExecutorConfig {
	cfg := c.ResponsesServer.FunctionExecutors
	cfg.Warnings = nil
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxResultBytes <= 0 {
		cfg.MaxResultBytes = 64 << 10
	}
	bindings := make([]ResponsesFunctionExecutorBinding, 0, len(cfg.Executors))
	seen := map[string]struct{}{}
	enabledAvailable := 0
	for _, binding := range cfg.Executors {
		binding.Name = strings.TrimSpace(binding.Name)
		binding.Type = strings.ToLower(strings.TrimSpace(binding.Type))
		binding.Command = strings.TrimSpace(binding.Command)
		binding.Process.WorkingDir = strings.TrimSpace(binding.Process.WorkingDir)
		binding.Process.AllowedCommandDirs = trimStringSlice(binding.Process.AllowedCommandDirs)
		binding.Available = false
		binding.Warnings = nil
		for i := range binding.EnvAllowlist {
			binding.EnvAllowlist[i] = strings.TrimSpace(binding.EnvAllowlist[i])
		}
		enabled := true
		if binding.Enabled != nil {
			enabled = *binding.Enabled
		}
		if binding.Name == "" {
			binding.Warnings = append(binding.Warnings, "executor name is required")
		} else if _, ok := seen[binding.Name]; ok {
			binding.Warnings = append(binding.Warnings, fmt.Sprintf("duplicate executor name %q is ignored", binding.Name))
		} else {
			seen[binding.Name] = struct{}{}
		}
		switch binding.Type {
		case ResponsesFunctionExecutorTypeStaticResponse:
			if len(binding.Warnings) == 0 && enabled {
				binding.Available = true
				enabledAvailable++
			}
		case ResponsesFunctionExecutorTypeExternalCommand:
			if binding.Command == "" {
				binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q command is required", binding.Name))
			}
			if binding.Process.WorkingDir != "" {
				if !filepath.IsAbs(binding.Process.WorkingDir) {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.working_dir must be absolute", binding.Name))
				} else if info, err := os.Stat(binding.Process.WorkingDir); err != nil {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.working_dir is not accessible: %v", binding.Name, err))
				} else if !info.IsDir() {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.working_dir must be a directory", binding.Name))
				}
			}
			if binding.Process.RequireAbsoluteCommand && binding.Command != "" && !filepath.IsAbs(binding.Command) {
				binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q command must be absolute when process.require_absolute_command is true", binding.Name))
			}
			allowedCommandDirs := make([]string, 0, len(binding.Process.AllowedCommandDirs))
			for _, dir := range binding.Process.AllowedCommandDirs {
				if dir == "" {
					continue
				}
				if !filepath.IsAbs(dir) {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.allowed_command_dirs entries must be absolute", binding.Name))
					continue
				}
				info, err := os.Stat(dir)
				if err != nil {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.allowed_command_dirs entry is not accessible: %v", binding.Name, err))
					continue
				}
				if !info.IsDir() {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.allowed_command_dirs entries must be directories", binding.Name))
					continue
				}
				resolved, err := filepath.EvalSymlinks(dir)
				if err != nil {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.allowed_command_dirs entry cannot resolve symlinks: %v", binding.Name, err))
					continue
				}
				if binding.Process.RejectRoot && filepath.Clean(resolved) == string(filepath.Separator) {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q process.allowed_command_dirs must not include filesystem root when process.reject_root is true", binding.Name))
					continue
				}
				allowedCommandDirs = append(allowedCommandDirs, resolved)
			}
			if binding.Command != "" && len(allowedCommandDirs) > 0 {
				if !filepath.IsAbs(binding.Command) {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q command must be absolute when process.allowed_command_dirs is configured", binding.Name))
				} else if resolvedCommand, err := filepath.EvalSymlinks(binding.Command); err != nil {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q command cannot resolve symlinks: %v", binding.Name, err))
				} else if !pathWithinAnyDir(resolvedCommand, allowedCommandDirs) {
					binding.Warnings = append(binding.Warnings, fmt.Sprintf("external_command executor %q command must resolve inside process.allowed_command_dirs", binding.Name))
				}
			}
			if len(binding.Warnings) == 0 && enabled {
				binding.Available = true
				enabledAvailable++
			}
		default:
			if binding.Type == "" {
				binding.Warnings = append(binding.Warnings, fmt.Sprintf("executor %q type is required", binding.Name))
			} else {
				binding.Warnings = append(binding.Warnings, fmt.Sprintf("unsupported executor type %q for %q", binding.Type, binding.Name))
			}
		}
		cfg.Warnings = append(cfg.Warnings, binding.Warnings...)
		bindings = append(bindings, binding)
	}
	if cfg.Enabled && len(bindings) == 0 {
		cfg.Warnings = append(cfg.Warnings, "responses function executors are enabled but no executors are configured")
	} else if cfg.Enabled && enabledAvailable == 0 {
		cfg.Warnings = append(cfg.Warnings, "responses function executors are enabled but no available executors are configured")
	}
	if cfg.Warnings == nil {
		cfg.Warnings = []string{}
	}
	cfg.Executors = bindings
	return cfg
}

func trimStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func pathWithinAnyDir(path string, dirs []string) bool {
	for _, dir := range dirs {
		if pathWithinDir(path, dir) {
			return true
		}
	}
	return false
}

func pathWithinDir(path string, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (c Config) WebSearchEnabled() bool {
	return c.WebSearchConfig().Enabled
}

func (c Config) WebSearchConfig() WebSearchToolConfig {
	cfg := c.Tools.WebSearch
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	if cfg.Provider == "" {
		cfg.Provider = "disabled"
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 5
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 5000
	}
	cfg.UserAgent = strings.TrimSpace(cfg.UserAgent)
	if cfg.UserAgent == "" {
		cfg.UserAgent = "llm-tracelab web_search"
	}
	return cfg
}
