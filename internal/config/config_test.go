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
	t.Setenv("LLM_TRACELAB_UPSTREAM_API_TYPE", "responses")
	t.Setenv("LLM_TRACELAB_UPSTREAM_MODE", "server")

	cfg := Config{
		Upstreams: []UpstreamTargetConfig{
			{
				ID: "primary",
				Upstream: UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ApiKey:         "config-placeholder-key",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Mode:           "proxy",
				},
			},
			{
				ID: "secondary",
				Upstream: UpstreamConfig{
					BaseURL:        "https://secondary.example.com/v1",
					ApiKey:         "secondary-placeholder-key",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Mode:           "proxy",
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
	if cfg.Upstreams[0].Upstream.APIType != "responses" {
		t.Fatalf("first upstream api_type = %q", cfg.Upstreams[0].Upstream.APIType)
	}
	if cfg.Upstreams[0].Upstream.Mode != "server" {
		t.Fatalf("first upstream mode = %q", cfg.Upstreams[0].Upstream.Mode)
	}
	if cfg.Upstreams[1].Upstream.ApiKey != "secondary-placeholder-key" {
		t.Fatalf("second upstream api_key = %q", cfg.Upstreams[1].Upstream.ApiKey)
	}
	if cfg.Upstreams[1].Upstream.APIType != "chat_completions" {
		t.Fatalf("second upstream api_type = %q", cfg.Upstreams[1].Upstream.APIType)
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

func TestLoadParsesUpstreamAPISurface(t *testing.T) {
	path := writeTempConfig(t, `
upstreams:
  - id: "primary"
    upstream:
      base_url: "https://api.openai.com/v1"
      api_type: "responses"
      mode: "server"
      capabilities:
        responses: true
        chat_completions: false
        embeddings: true
        models: true
        tokenize: false
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	upstream := cfg.Upstreams[0].Upstream
	if upstream.APIType != "responses" || upstream.Mode != "server" {
		t.Fatalf("api surface = api_type:%q mode:%q, want responses/server", upstream.APIType, upstream.Mode)
	}
	if upstream.Capabilities.Responses == nil || !*upstream.Capabilities.Responses {
		t.Fatalf("capabilities.responses = %v, want true", upstream.Capabilities.Responses)
	}
	if upstream.Capabilities.ChatCompletions == nil || *upstream.Capabilities.ChatCompletions {
		t.Fatalf("capabilities.chat_completions = %v, want false", upstream.Capabilities.ChatCompletions)
	}
	if upstream.Capabilities.Embeddings == nil || !*upstream.Capabilities.Embeddings {
		t.Fatalf("capabilities.embeddings = %v, want true", upstream.Capabilities.Embeddings)
	}
	if upstream.Capabilities.Models == nil || !*upstream.Capabilities.Models {
		t.Fatalf("capabilities.models = %v, want true", upstream.Capabilities.Models)
	}
	if upstream.Capabilities.Tokenize == nil || *upstream.Capabilities.Tokenize {
		t.Fatalf("capabilities.tokenize = %v, want false", upstream.Capabilities.Tokenize)
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
  scope: "header"
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
	if cfg.Limits.MaxConcurrent != 1 || cfg.Limits.MaxQueued != 2 || cfg.Limits.ChannelKeyHeader != "X-TraceLab-Channel" || cfg.Limits.ScopeOrDefault() != "header" {
		t.Fatalf("Limits = %+v", cfg.Limits)
	}
}

func TestResponsesServerConfigDisabledByDefault(t *testing.T) {
	cfg := Config{}
	if cfg.ResponsesServerEnabled() {
		t.Fatalf("ResponsesServerEnabled() = true, want false")
	}
	if cfg.ResponsesDefaultModel() != "" {
		t.Fatalf("ResponsesDefaultModel() = %q, want empty", cfg.ResponsesDefaultModel())
	}
	if cfg.ResponsesForceStore() {
		t.Fatalf("ResponsesForceStore() = true, want false")
	}
	if got := cfg.ResponsesMaxRequestBodyBytes(); got != 16<<20 {
		t.Fatalf("ResponsesMaxRequestBodyBytes() = %d, want %d", got, 16<<20)
	}
	if got := cfg.ResponsesServerPath(); got != "/v1/responses" {
		t.Fatalf("ResponsesServerPath() = %q, want /v1/responses", got)
	}
}

func TestLoadParsesResponsesServerConfigFromYAML(t *testing.T) {
	path := writeTempConfig(t, `
responses_server:
  enabled: true
  default_model: "qwen3"
  force_store: true
  max_request_body_bytes: 1048576
  path: "/v1/responses"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.ResponsesServerEnabled() {
		t.Fatalf("ResponsesServerEnabled() = false, want true")
	}
	if got := cfg.ResponsesDefaultModel(); got != "qwen3" {
		t.Fatalf("ResponsesDefaultModel() = %q, want qwen3", got)
	}
	if !cfg.ResponsesForceStore() {
		t.Fatalf("ResponsesForceStore() = false, want true")
	}
	if got := cfg.ResponsesMaxRequestBodyBytes(); got != 1048576 {
		t.Fatalf("ResponsesMaxRequestBodyBytes() = %d, want 1048576", got)
	}
	if got := cfg.ResponsesServerPath(); got != "/v1/responses" {
		t.Fatalf("ResponsesServerPath() = %q, want /v1/responses", got)
	}
}

func TestResponsesServerEnvOverrides(t *testing.T) {
	t.Setenv("LLM_TRACELAB_RESPONSES_ENABLED", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_DEFAULT_MODEL", "env-model")
	t.Setenv("LLM_TRACELAB_RESPONSES_FORCE_STORE", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_MAX_REQUEST_BODY_BYTES", "2097152")
	t.Setenv("LLM_TRACELAB_RESPONSES_PATH", "/custom/responses")

	cfg := Config{}
	cfg.ResponsesServer.DefaultModel = "yaml-model"
	cfg.ResponsesServer.MaxRequestBodyBytes = 1024
	applyEnvOverrides(&cfg)

	if !cfg.ResponsesServerEnabled() {
		t.Fatalf("ResponsesServerEnabled() = false, want true")
	}
	if got := cfg.ResponsesDefaultModel(); got != "env-model" {
		t.Fatalf("ResponsesDefaultModel() = %q, want env-model", got)
	}
	if !cfg.ResponsesForceStore() {
		t.Fatalf("ResponsesForceStore() = false, want true")
	}
	if got := cfg.ResponsesMaxRequestBodyBytes(); got != 2097152 {
		t.Fatalf("ResponsesMaxRequestBodyBytes() = %d, want 2097152", got)
	}
	if got := cfg.ResponsesServerPath(); got != "/custom/responses" {
		t.Fatalf("ResponsesServerPath() = %q, want /custom/responses", got)
	}
}

func TestWebSearchConfigDisabledByDefault(t *testing.T) {
	cfg := Config{}
	webSearch := cfg.WebSearchConfig()

	if cfg.WebSearchEnabled() {
		t.Fatalf("WebSearchEnabled() = true, want false")
	}
	if webSearch.Enabled {
		t.Fatalf("WebSearchConfig().Enabled = true, want false")
	}
	if webSearch.Provider != "disabled" {
		t.Fatalf("WebSearchConfig().Provider = %q, want disabled", webSearch.Provider)
	}
	if webSearch.MaxResults != 5 {
		t.Fatalf("WebSearchConfig().MaxResults = %d, want 5", webSearch.MaxResults)
	}
	if webSearch.BaseURL != "" {
		t.Fatalf("WebSearchConfig().BaseURL = %q, want empty", webSearch.BaseURL)
	}
	if webSearch.TimeoutMS != 5000 {
		t.Fatalf("WebSearchConfig().TimeoutMS = %d, want 5000", webSearch.TimeoutMS)
	}
	if webSearch.UserAgent != "llm-tracelab web_search" {
		t.Fatalf("WebSearchConfig().UserAgent = %q, want default", webSearch.UserAgent)
	}
}

func TestWebSearchEnvOverrides(t *testing.T) {
	t.Setenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED", "true")
	t.Setenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_PROVIDER", "searxng")
	t.Setenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_MAX_RESULTS", "3")
	t.Setenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_BASE_URL", "http://127.0.0.1:8888")
	t.Setenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_TIMEOUT_MS", "2500")
	t.Setenv("LLM_TRACELAB_TOOLS_WEB_SEARCH_USER_AGENT", "llm-tracelab-test")

	cfg := Config{}
	cfg.Tools.WebSearch.Provider = "mock"
	cfg.Tools.WebSearch.MaxResults = 2
	cfg.Tools.WebSearch.TimeoutMS = 1000
	applyEnvOverrides(&cfg)

	webSearch := cfg.WebSearchConfig()
	if !cfg.WebSearchEnabled() {
		t.Fatalf("WebSearchEnabled() = false, want true")
	}
	if webSearch.Provider != "searxng" {
		t.Fatalf("WebSearchConfig().Provider = %q, want searxng", webSearch.Provider)
	}
	if webSearch.MaxResults != 3 {
		t.Fatalf("WebSearchConfig().MaxResults = %d, want 3", webSearch.MaxResults)
	}
	if webSearch.BaseURL != "http://127.0.0.1:8888" {
		t.Fatalf("WebSearchConfig().BaseURL = %q", webSearch.BaseURL)
	}
	if webSearch.TimeoutMS != 2500 {
		t.Fatalf("WebSearchConfig().TimeoutMS = %d, want 2500", webSearch.TimeoutMS)
	}
	if webSearch.UserAgent != "llm-tracelab-test" {
		t.Fatalf("WebSearchConfig().UserAgent = %q", webSearch.UserAgent)
	}
}

func TestLoadParsesWebSearchConfigFromYAML(t *testing.T) {
	clearWebSearchEnv(t)
	path := writeTempConfig(t, `
tools:
  web_search:
    enabled: true
    provider: "mock"
    max_results: 2
    base_url: "http://127.0.0.1:8888"
    timeout_ms: 1500
    user_agent: "yaml-agent"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	webSearch := cfg.WebSearchConfig()
	if !cfg.WebSearchEnabled() {
		t.Fatalf("WebSearchEnabled() = false, want true")
	}
	if webSearch.Provider != "mock" {
		t.Fatalf("WebSearchConfig().Provider = %q, want mock", webSearch.Provider)
	}
	if webSearch.MaxResults != 2 {
		t.Fatalf("WebSearchConfig().MaxResults = %d, want 2", webSearch.MaxResults)
	}
	if webSearch.BaseURL != "http://127.0.0.1:8888" {
		t.Fatalf("WebSearchConfig().BaseURL = %q", webSearch.BaseURL)
	}
	if webSearch.TimeoutMS != 1500 {
		t.Fatalf("WebSearchConfig().TimeoutMS = %d, want 1500", webSearch.TimeoutMS)
	}
	if webSearch.UserAgent != "yaml-agent" {
		t.Fatalf("WebSearchConfig().UserAgent = %q", webSearch.UserAgent)
	}
}

func TestLimitScopeDefaults(t *testing.T) {
	if got := (LimitConfig{}).ScopeOrDefault(); got != "global" {
		t.Fatalf("ScopeOrDefault() = %q, want global", got)
	}
	if got := (LimitConfig{ChannelKeyHeader: "X-TraceLab-Channel"}).ScopeOrDefault(); got != "header" {
		t.Fatalf("ScopeOrDefault() = %q, want header", got)
	}
	if got := (LimitConfig{Scope: " Credential ", ChannelKeyHeader: "X-TraceLab-Channel"}).ScopeOrDefault(); got != "credential" {
		t.Fatalf("ScopeOrDefault() = %q, want credential", got)
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

func clearWebSearchEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_PROVIDER",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_MAX_RESULTS",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_BASE_URL",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_TIMEOUT_MS",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_USER_AGENT",
	} {
		t.Setenv(name, "")
	}
}
