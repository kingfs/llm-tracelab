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

	Router RouterConfig `yaml:"router"`

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
	BaseURL        string            `yaml:"base_url"`
	ApiKey         string            `yaml:"api_key"`
	ProviderPreset string            `yaml:"provider_preset"`
	ProtocolFamily string            `yaml:"protocol_family"`
	RoutingProfile string            `yaml:"routing_profile"`
	APIVersion     string            `yaml:"api_version"`
	Deployment     string            `yaml:"deployment"`
	Project        string            `yaml:"project"`
	Location       string            `yaml:"location"`
	ModelResource  string            `yaml:"model_resource"`
	Headers        map[string]string `yaml:"headers"`
}

type UpstreamTargetConfig struct {
	ID             string         `yaml:"id"`
	Enabled        *bool          `yaml:"enabled"`
	Priority       int            `yaml:"priority"`
	Weight         float64        `yaml:"weight"`
	CapacityHint   float64        `yaml:"capacity_hint"`
	ModelDiscovery string         `yaml:"model_discovery"`
	StaticModels   []string       `yaml:"static_models"`
	Upstream       UpstreamConfig `yaml:"upstream"`
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
