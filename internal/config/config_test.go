package config

import "testing"

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
	t.Setenv("LLM_TRACELAB_UPSTREAM_API_KEY", "sk-env")
	t.Setenv("LLM_TRACELAB_UPSTREAM_PROVIDER_PRESET", "openrouter")

	cfg := Config{
		Upstreams: []UpstreamTargetConfig{
			{
				ID: "primary",
				Upstream: UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ApiKey:         "sk-config",
					ProviderPreset: "openai",
				},
			},
			{
				ID: "secondary",
				Upstream: UpstreamConfig{
					BaseURL:        "https://secondary.example.com/v1",
					ApiKey:         "sk-secondary",
					ProviderPreset: "openai",
				},
			},
		},
	}
	applyEnvOverrides(&cfg)

	if cfg.Upstreams[0].Upstream.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("first upstream base_url = %q", cfg.Upstreams[0].Upstream.BaseURL)
	}
	if cfg.Upstreams[0].Upstream.ApiKey != "sk-env" {
		t.Fatalf("first upstream api_key = %q", cfg.Upstreams[0].Upstream.ApiKey)
	}
	if cfg.Upstreams[0].Upstream.ProviderPreset != "openrouter" {
		t.Fatalf("first upstream provider_preset = %q", cfg.Upstreams[0].Upstream.ProviderPreset)
	}
	if cfg.Upstreams[1].Upstream.ApiKey != "sk-secondary" {
		t.Fatalf("second upstream api_key = %q", cfg.Upstreams[1].Upstream.ApiKey)
	}
}
