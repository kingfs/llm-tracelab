package config

import (
	"os"
	"testing"
)

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "postgres url password",
			dsn:  "postgres://user:secret@example.com:5432/traces?sslmode=require",
			want: "postgres://user:<redacted>@example.com:5432/traces?sslmode=require",
		},
		{
			name: "postgres url without password",
			dsn:  "postgres://user@example.com:5432/traces",
			want: "postgres://user@example.com:5432/traces",
		},
		{
			name: "keyword password",
			dsn:  "host=localhost user=trace password=secret dbname=traces",
			want: "host=localhost user=trace password=<redacted> dbname=traces",
		},
		{
			name: "keyword password case insensitive",
			dsn:  "host=localhost user=trace Password=secret dbname=traces",
			want: "host=localhost user=trace Password=<redacted> dbname=traces",
		},
		{
			name: "sqlite path",
			dsn:  "./logs/llm_tracelab.sqlite3",
			want: "./logs/llm_tracelab.sqlite3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactDSN(tt.dsn); got != tt.want {
				t.Fatalf("RedactDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLitePathFromDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{name: "plain path", dsn: "/tmp/traces.sqlite3", want: "/tmp/traces.sqlite3"},
		{name: "sqlite url absolute", dsn: "sqlite:///tmp/traces.sqlite3", want: "/tmp/traces.sqlite3"},
		{name: "file uri with query", dsn: "file:/tmp/traces.sqlite3?mode=rwc&_pragma=busy_timeout(5000)", want: "/tmp/traces.sqlite3"},
		{name: "memory uri", dsn: "file::memory:?cache=shared", want: ":memory:"},
		{name: "empty", dsn: " ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SQLitePathFromDSN(tt.dsn); got != tt.want {
				t.Fatalf("SQLitePathFromDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLegacyOutputDirEnvOverridesTraceAndDebugOutput(t *testing.T) {
	t.Setenv("LLM_TRACELAB_OUTPUT_DIR", "/app/data/traces")
	t.Setenv("LLM_TRACELAB_TRACE_OUTPUT_DIR", "")

	cfg := Config{}
	cfg.Trace.OutputDir = "./logs"
	cfg.Debug.OutputDir = "./debug"
	applyEnvOverrides(&cfg)

	if cfg.TraceOutputDir() != "/app/data/traces" {
		t.Fatalf("TraceOutputDir() = %q, want /app/data/traces", cfg.TraceOutputDir())
	}
	if cfg.Debug.OutputDir != "/app/data/traces" {
		t.Fatalf("Debug.OutputDir = %q, want /app/data/traces", cfg.Debug.OutputDir)
	}
}

func TestTraceOutputDirEnvOverridesLegacyOutputDirEnv(t *testing.T) {
	t.Setenv("LLM_TRACELAB_OUTPUT_DIR", "/app/data/legacy")
	t.Setenv("LLM_TRACELAB_TRACE_OUTPUT_DIR", "/app/data/traces")

	cfg := Config{}
	cfg.Trace.OutputDir = "./logs"
	cfg.Debug.OutputDir = "./debug"
	applyEnvOverrides(&cfg)

	if cfg.TraceOutputDir() != "/app/data/traces" {
		t.Fatalf("TraceOutputDir() = %q, want /app/data/traces", cfg.TraceOutputDir())
	}
	if cfg.Debug.OutputDir != "/app/data/legacy" {
		t.Fatalf("Debug.OutputDir = %q, want /app/data/legacy", cfg.Debug.OutputDir)
	}
}

func TestLegacyUpstreamEnvOverridesFirstConfiguredUpstream(t *testing.T) {
	t.Setenv("LLM_TRACELAB_UPSTREAM_BASE_URL", "https://proxy.example.com/v1")
	t.Setenv("LLM_TRACELAB_UPSTREAM_API_KEY", "env-placeholder-key")
	t.Setenv("LLM_TRACELAB_UPSTREAM_PROVIDER_PRESET", "openrouter")

	cfg := Config{
		Upstreams: []UpstreamTargetConfig{
			{
				ID: "primary",
				Upstream: UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ApiKey:         "config-placeholder-key",
					ProviderPreset: "openai",
				},
			},
			{
				ID: "secondary",
				Upstream: UpstreamConfig{
					BaseURL:        "https://secondary.example.com/v1",
					ApiKey:         "secondary-placeholder-key",
					ProviderPreset: "openai",
				},
			},
		},
	}
	applyEnvOverrides(&cfg)

	if cfg.Upstreams[0].Upstream.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("first upstream base_url = %q", cfg.Upstreams[0].Upstream.BaseURL)
	}
	if cfg.Upstreams[0].Upstream.ApiKey != "env-placeholder-key" {
		t.Fatalf("first upstream api_key = %q", cfg.Upstreams[0].Upstream.ApiKey)
	}
	if cfg.Upstreams[0].Upstream.ProviderPreset != "openrouter" {
		t.Fatalf("first upstream provider_preset = %q", cfg.Upstreams[0].Upstream.ProviderPreset)
	}
	if cfg.Upstreams[1].Upstream.ApiKey != "secondary-placeholder-key" {
		t.Fatalf("second upstream api_key = %q", cfg.Upstreams[1].Upstream.ApiKey)
	}
}

func TestLoadExpandsEnvReferences(t *testing.T) {
	t.Setenv("OPENAI_TEST_KEY", "test-placeholder-key")
	path := writeTempConfig(t, `
server:
  port: "8080"
upstreams:
  - id: "primary"
    upstream:
      base_url: "https://api.openai.com/v1"
      api_key: "$env:OPENAI_TEST_KEY"
      provider_preset: "openai"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Upstreams[0].Upstream.ApiKey != "test-placeholder-key" {
		t.Fatalf("api_key = %q, want test-placeholder-key", cfg.Upstreams[0].Upstream.ApiKey)
	}
}

func TestLoadParsesExplicitCredentials(t *testing.T) {
	t.Setenv("OPENAI_TEST_KEY", "test-key-from-env")
	t.Setenv("OPENAI_BACKUP_KEY", "backup-key-from-env")
	path := writeTempConfig(t, `
upstreams:
  - id: "primary"
    upstream:
      base_url: "https://api.openai.com/v1"
      api_key: "$env:OPENAI_TEST_KEY"
      provider_preset: "openai"
    credentials:
      - id: "primary"
        name: "Primary account"
        api_key: "$env:OPENAI_TEST_KEY"
        concurrency_limit: 2
        headers:
          X-Test-Credential: "primary"
      - id: "backup"
        name: "Backup account"
        api_key: "$env:OPENAI_BACKUP_KEY"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	target := cfg.Upstreams[0]
	if !target.HasExplicitCredentials() {
		t.Fatalf("HasExplicitCredentials() = false, want true")
	}
	credentials := target.EffectiveCredentials()
	if len(credentials) != 2 {
		t.Fatalf("len(EffectiveCredentials()) = %d, want 2", len(credentials))
	}
	if credentials[0].ID != "primary" || credentials[0].ApiKey != "test-key-from-env" || credentials[0].ConcurrencyLimit != 2 {
		t.Fatalf("primary credential = %+v", credentials[0])
	}
	if credentials[0].Headers["X-Test-Credential"] != "primary" {
		t.Fatalf("primary credential headers = %+v", credentials[0].Headers)
	}
	if credentials[1].ID != "backup" || credentials[1].ApiKey != "backup-key-from-env" {
		t.Fatalf("backup credential = %+v", credentials[1])
	}
}

func TestEffectiveCredentialsUsesExplicitCredentialsBeforeInlineKey(t *testing.T) {
	target := UpstreamTargetConfig{
		Upstream: UpstreamConfig{
			ApiKey: "inline-placeholder",
		},
		Credentials: []CredentialConfig{
			{
				ID:     "explicit",
				ApiKey: "explicit-placeholder",
			},
		},
	}

	credentials := target.EffectiveCredentials()
	if len(credentials) != 1 {
		t.Fatalf("len(EffectiveCredentials()) = %d, want 1", len(credentials))
	}
	if credentials[0].ID != "explicit" || credentials[0].ApiKey != "explicit-placeholder" {
		t.Fatalf("EffectiveCredentials()[0] = %+v, want explicit credential", credentials[0])
	}
}

func TestEffectiveCredentialsCompilesImplicitDefaultFromInlineKey(t *testing.T) {
	target := UpstreamTargetConfig{
		Upstream: UpstreamConfig{
			ApiKey: "inline-placeholder",
		},
	}

	credentials := target.EffectiveCredentials()
	if len(credentials) != 1 {
		t.Fatalf("len(EffectiveCredentials()) = %d, want 1", len(credentials))
	}
	if credentials[0].ID != "default" || credentials[0].ApiKey != "inline-placeholder" {
		t.Fatalf("EffectiveCredentials()[0] = %+v, want implicit default credential", credentials[0])
	}
	if credentials[0].Enabled == nil || !*credentials[0].Enabled {
		t.Fatalf("implicit default Enabled = %v, want true", credentials[0].Enabled)
	}
}

func TestLoadLimitConfigDisabledByDefault(t *testing.T) {
	cfg := Config{}
	if cfg.Limits.Enabled {
		t.Fatalf("Limits.Enabled = true, want false")
	}
	if cfg.Limits.LocalConcurrencyEnabled() {
		t.Fatalf("LocalConcurrencyEnabled() = true, want false")
	}
}

func TestLoadLimitConfigFromYAML(t *testing.T) {
	path := writeTempConfig(t, `
limits:
  enabled: true
  max_concurrent: 1
  max_queued: 2
  channel_key_header: "X-TraceLab-Channel"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Limits.LocalConcurrencyEnabled() {
		t.Fatalf("LocalConcurrencyEnabled() = false, want true")
	}
	if cfg.Limits.MaxConcurrent != 1 || cfg.Limits.MaxQueued != 2 || cfg.Limits.ChannelKeyHeader != "X-TraceLab-Channel" {
		t.Fatalf("Limits = %+v", cfg.Limits)
	}
}

func TestLoadFailsWhenEnvReferenceMissing(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: "8080"
upstream:
  base_url: "https://api.openai.com/v1"
  api_key: "$env:DOES_NOT_EXIST_FOR_TEST"
  provider_preset: "openai"
`)

	if _, err := Load(path); err == nil {
		t.Fatalf("Load() error = nil, want missing env error")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
