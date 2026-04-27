package config

import (
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port      string `yaml:"port"`
		AuthToken string `yaml:"auth_token"`
	} `yaml:"server"`

	Monitor struct {
		Port      string `yaml:"port"`
		AuthToken string `yaml:"auth_token"`
	} `yaml:"monitor"`

	MCP struct {
		Enabled   bool   `yaml:"enabled"`
		Path      string `yaml:"path"`
		AuthToken string `yaml:"auth_token"`
	} `yaml:"mcp"`

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
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("LLM_TRACELAB_SERVER_PORT"); v != "" {
		cfg.Server.Port = v
	}
	if v := os.Getenv("LLM_TRACELAB_SERVER_AUTH_TOKEN"); v != "" {
		cfg.Server.AuthToken = v
	}
	if v := os.Getenv("LLM_TRACELAB_MONITOR_PORT"); v != "" {
		cfg.Monitor.Port = v
	}
	if v := os.Getenv("LLM_TRACELAB_MONITOR_AUTH_TOKEN"); v != "" {
		cfg.Monitor.AuthToken = v
	}
	if v := os.Getenv("LLM_TRACELAB_MCP_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.MCP.Enabled = parsed
		}
	}
	if v := os.Getenv("LLM_TRACELAB_MCP_PATH"); v != "" {
		cfg.MCP.Path = v
	}
	if v := os.Getenv("LLM_TRACELAB_MCP_AUTH_TOKEN"); v != "" {
		cfg.MCP.AuthToken = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_BASE_URL"); v != "" {
		cfg.Upstream.BaseURL = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_API_KEY"); v != "" {
		cfg.Upstream.ApiKey = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_PROVIDER_PRESET"); v != "" {
		cfg.Upstream.ProviderPreset = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_PROTOCOL_FAMILY"); v != "" {
		cfg.Upstream.ProtocolFamily = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_ROUTING_PROFILE"); v != "" {
		cfg.Upstream.RoutingProfile = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_API_VERSION"); v != "" {
		cfg.Upstream.APIVersion = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_DEPLOYMENT"); v != "" {
		cfg.Upstream.Deployment = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_PROJECT"); v != "" {
		cfg.Upstream.Project = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_LOCATION"); v != "" {
		cfg.Upstream.Location = v
	}
	if v := os.Getenv("LLM_TRACELAB_UPSTREAM_MODEL_RESOURCE"); v != "" {
		cfg.Upstream.ModelResource = v
	}
	if v := os.Getenv("LLM_TRACELAB_OUTPUT_DIR"); v != "" {
		cfg.Debug.OutputDir = v
	}
	if v := os.Getenv("LLM_TRACELAB_MASK_KEY"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Debug.MaskKey = parsed
		}
	}
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
