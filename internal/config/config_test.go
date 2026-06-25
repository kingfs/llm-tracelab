package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestProviderProbeConfigLoadsStartupFillAndTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
provider_probe:
  startup_fill: true
  timeout: 2s
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.ProviderProbeStartupFillEnabled() {
		t.Fatalf("ProviderProbeStartupFillEnabled() = false, want true")
	}
	if got := cfg.ProviderProbeTimeout(); got != 2*time.Second {
		t.Fatalf("ProviderProbeTimeout() = %v, want 2s", got)
	}
}

func TestProviderProbeConfigEnvOverrides(t *testing.T) {
	t.Setenv("LLM_TRACELAB_PROVIDER_PROBE_STARTUP_FILL", "true")
	t.Setenv("LLM_TRACELAB_PROVIDER_PROBE_TIMEOUT", "3s")

	cfg := Config{}
	applyEnvOverrides(&cfg)

	if !cfg.ProviderProbeStartupFillEnabled() {
		t.Fatalf("ProviderProbeStartupFillEnabled() = false, want true")
	}
	if got := cfg.ProviderProbeTimeout(); got != 3*time.Second {
		t.Fatalf("ProviderProbeTimeout() = %v, want 3s", got)
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

func TestBootstrapUpstreamEnvOverridesOnlyFirstConfiguredUpstream(t *testing.T) {
	t.Setenv("LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL", "http://host.docker.internal:8000/v1")
	t.Setenv("LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY", "bootstrap-placeholder-key")

	cfg := Config{
		Upstream: UpstreamConfig{
			BaseURL: "https://legacy.example.com/v1",
			ApiKey:  "legacy-placeholder-key",
		},
		Upstreams: []UpstreamTargetConfig{
			{
				ID: "primary",
				Upstream: UpstreamConfig{
					BaseURL: "https://api.openai.com/v1",
					ApiKey:  "config-placeholder-key",
				},
			},
			{
				ID: "secondary",
				Upstream: UpstreamConfig{
					BaseURL: "https://secondary.example.com/v1",
					ApiKey:  "secondary-placeholder-key",
				},
			},
		},
	}
	applyEnvOverrides(&cfg)

	if cfg.Upstream.BaseURL != "https://legacy.example.com/v1" || cfg.Upstream.ApiKey != "legacy-placeholder-key" {
		t.Fatalf("legacy upstream = %+v, want unchanged", cfg.Upstream)
	}
	if cfg.Upstreams[0].Upstream.BaseURL != "http://host.docker.internal:8000/v1" {
		t.Fatalf("first upstream base_url = %q", cfg.Upstreams[0].Upstream.BaseURL)
	}
	if cfg.Upstreams[0].Upstream.ApiKey != "bootstrap-placeholder-key" {
		t.Fatalf("first upstream api_key = %q", cfg.Upstreams[0].Upstream.ApiKey)
	}
	if cfg.Upstreams[1].Upstream.BaseURL != "https://secondary.example.com/v1" || cfg.Upstreams[1].Upstream.ApiKey != "secondary-placeholder-key" {
		t.Fatalf("second upstream = %+v, want unchanged", cfg.Upstreams[1].Upstream)
	}
}

func TestBootstrapUpstreamEnvCreatesSingleDefaultUpstream(t *testing.T) {
	t.Setenv("LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL", "http://host.docker.internal:8000/v1")
	t.Setenv("LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY", "bootstrap-placeholder-key")

	cfg := Config{}
	applyEnvOverrides(&cfg)

	targets := cfg.EffectiveUpstreams()
	if len(targets) != 1 {
		t.Fatalf("len(EffectiveUpstreams()) = %d, want 1", len(targets))
	}
	target := targets[0]
	if target.Upstream.BaseURL != "http://host.docker.internal:8000/v1" {
		t.Fatalf("bootstrap base_url = %q", target.Upstream.BaseURL)
	}
	if target.Upstream.ApiKey != "bootstrap-placeholder-key" {
		t.Fatalf("bootstrap api_key = %q", target.Upstream.ApiKey)
	}
	if target.Upstream.ProviderPreset != "openai" || target.Upstream.ProtocolFamily != "openai_compatible" {
		t.Fatalf("bootstrap upstream preset/family = %q/%q", target.Upstream.ProviderPreset, target.Upstream.ProtocolFamily)
	}
	if target.Upstream.Capabilities.ChatCompletions == nil || !*target.Upstream.Capabilities.ChatCompletions {
		t.Fatalf("bootstrap chat_completions capability = %#v", target.Upstream.Capabilities.ChatCompletions)
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

func TestLoadAllowsMissingLegacyLLMAPIKeyReference(t *testing.T) {
	oldValue, wasSet := os.LookupEnv("LLM_API_KEY")
	if err := os.Unsetenv("LLM_API_KEY"); err != nil {
		t.Fatalf("Unsetenv() error = %v", err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv("LLM_API_KEY", oldValue)
			return
		}
		_ = os.Unsetenv("LLM_API_KEY")
	})
	path := writeTempConfig(t, `
server:
  port: "8080"
upstreams:
  - id: "primary"
    upstream:
      base_url: "https://api.openai.com/v1"
      api_key: "$env:LLM_API_KEY"
      provider_preset: "openai"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Upstreams[0].Upstream.ApiKey != "" {
		t.Fatalf("api_key = %q, want empty", cfg.Upstreams[0].Upstream.ApiKey)
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
        tool_calling: false
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
	if upstream.Capabilities.ToolCalling == nil || *upstream.Capabilities.ToolCalling {
		t.Fatalf("capabilities.tool_calling = %v, want false", upstream.Capabilities.ToolCalling)
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
	if cfg.ResponsesAutoCompactEnabled() {
		t.Fatalf("ResponsesAutoCompactEnabled() = true, want false")
	}
	if got := cfg.ResponsesCompactHistoryItemThreshold(); got != 0 {
		t.Fatalf("ResponsesCompactHistoryItemThreshold() = %d, want 0", got)
	}
	if got := cfg.ResponsesModelProfiles(); len(got) != 0 {
		t.Fatalf("ResponsesModelProfiles() len = %d, want 0", len(got))
	}
	executors := cfg.ResponsesFunctionExecutorsConfig()
	if executors.Enabled {
		t.Fatalf("ResponsesFunctionExecutorsConfig().Enabled = true, want false")
	}
	if executors.Timeout != 5*time.Second {
		t.Fatalf("ResponsesFunctionExecutorsConfig().Timeout = %v, want 5s", executors.Timeout)
	}
	if executors.MaxResultBytes != 64<<10 {
		t.Fatalf("ResponsesFunctionExecutorsConfig().MaxResultBytes = %d, want %d", executors.MaxResultBytes, 64<<10)
	}
	codexCompat := cfg.ResponsesCodexCompatConfig()
	if codexCompat.Enabled {
		t.Fatalf("ResponsesCodexCompatConfig().Enabled = true, want false")
	}
	if len(codexCompat.AutoInjectHostedTools) != 0 {
		t.Fatalf("ResponsesCodexCompatConfig().AutoInjectHostedTools = %v, want empty", codexCompat.AutoInjectHostedTools)
	}
	if codexCompat.InjectWhenToolsAbsent == nil || !*codexCompat.InjectWhenToolsAbsent {
		t.Fatalf("ResponsesCodexCompatConfig().InjectWhenToolsAbsent = %v, want true", codexCompat.InjectWhenToolsAbsent)
	}
	if codexCompat.PreserveClientTools == nil || !*codexCompat.PreserveClientTools {
		t.Fatalf("ResponsesCodexCompatConfig().PreserveClientTools = %v, want true", codexCompat.PreserveClientTools)
	}
	if codexCompat.DefaultToolChoice != "auto" {
		t.Fatalf("ResponsesCodexCompatConfig().DefaultToolChoice = %#v, want auto", codexCompat.DefaultToolChoice)
	}
}

func TestLoadParsesResponsesServerConfigFromYAML(t *testing.T) {
	workingDir := t.TempDir()
	commandPath := filepath.Join(workingDir, "lookup")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write command fixture: %v", err)
	}
	path := writeTempConfig(t, fmt.Sprintf(`
responses_server:
  enabled: true
  default_model: "qwen3"
  force_store: true
  max_request_body_bytes: 1048576
  path: "/v1/responses"
  auto_compact: true
  compact_history_item_threshold: 12
  codex_compat:
    enabled: true
    auto_inject_hosted_tools: [" web_search ", "mcp"]
    inject_when_tools_absent: false
    preserve_client_tools: false
    default_tool_choice: required
  function_executors:
    enabled: true
    timeout: 2s
    max_result_bytes: 128
    redaction:
      arguments: true
      output: true
    executors:
      - name: "lookup"
        type: "static_response"
        output:
          ok: true
      - name: "disabled_lookup"
        type: "static_response"
        enabled: false
        output: "off"
      - name: "run_lookup"
        type: "external_command"
        command: " %s "
        args: ["ok"]
        timeout: 1s
        env:
          STATIC_VALUE: "static"
        env_allowlist: [" PATH "]
        process:
          working_dir: %q
          require_absolute_command: true
          allowed_command_dirs: [" %s "]
          reject_root: true
  model_profiles:
    - name: "qwen3"
      context_window_tokens: 32768
      max_output_tokens: 4096
      tool_output_token_limit: 6000
      model_reasoning_effort: " high "
      compact_history_item_threshold: 8
      upstream_model: "qwen/qwen3"
      tokenize_counter:
        enabled: true
        upstream_id: " primary "
        timeout: 750ms
    - pattern: "gpt-4o*"
      compact_history_item_threshold: 6
`, commandPath, workingDir, workingDir))

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
	if !cfg.ResponsesAutoCompactEnabled() {
		t.Fatalf("ResponsesAutoCompactEnabled() = false, want true")
	}
	if got := cfg.ResponsesCompactHistoryItemThreshold(); got != 12 {
		t.Fatalf("ResponsesCompactHistoryItemThreshold() = %d, want 12", got)
	}
	codexCompat := cfg.ResponsesCodexCompatConfig()
	if !codexCompat.Enabled || len(codexCompat.AutoInjectHostedTools) != 2 || codexCompat.AutoInjectHostedTools[0] != "web_search" || codexCompat.AutoInjectHostedTools[1] != "mcp" || codexCompat.InjectWhenToolsAbsent == nil || *codexCompat.InjectWhenToolsAbsent || codexCompat.PreserveClientTools == nil || *codexCompat.PreserveClientTools || codexCompat.DefaultToolChoice != "required" {
		t.Fatalf("ResponsesCodexCompatConfig() = %+v, want YAML values", codexCompat)
	}
	profiles := cfg.ResponsesModelProfiles()
	if len(profiles) != 2 {
		t.Fatalf("ResponsesModelProfiles() len = %d, want 2", len(profiles))
	}
	if got := profiles[0]; got.Name != "qwen3" || got.ContextWindowTokens != 32768 || got.MaxOutputTokens != 4096 || got.ToolOutputTokenLimit != 6000 || got.ModelReasoningEffort != "high" || got.CompactHistoryItemThreshold != 8 || got.UpstreamModel != "qwen/qwen3" {
		t.Fatalf("first model profile = %+v", got)
	}
	if got := profiles[0].TokenizeCounter; got.Enabled == nil || !*got.Enabled || got.UpstreamID != "primary" || got.Timeout != 750*time.Millisecond {
		t.Fatalf("first model profile tokenize_counter = %+v, want enabled primary 750ms", got)
	}
	if got := profiles[1]; got.Pattern != "gpt-4o*" || got.CompactHistoryItemThreshold != 6 {
		t.Fatalf("second model profile = %+v", got)
	}
	executors := cfg.ResponsesFunctionExecutorsConfig()
	if !executors.Enabled || executors.Timeout != 2*time.Second || executors.MaxResultBytes != 128 || !executors.Redaction.Arguments || !executors.Redaction.Output {
		t.Fatalf("function executors policy = %+v, want enabled 2s 128 redacted", executors)
	}
	if len(executors.Executors) != 3 {
		t.Fatalf("function executors len = %d, want 3", len(executors.Executors))
	}
	if got := executors.Executors[0]; got.Name != "lookup" || got.Type != "static_response" {
		t.Fatalf("first function executor = %+v", got)
	}
	if got := executors.Executors[1]; got.Name != "disabled_lookup" || got.Enabled == nil || *got.Enabled {
		t.Fatalf("second function executor = %+v", got)
	}
	if got := executors.Executors[2]; got.Name != "run_lookup" || got.Type != "external_command" || got.Command != commandPath || got.Timeout != time.Second || len(got.Args) != 1 || got.Args[0] != "ok" || got.Env["STATIC_VALUE"] != "static" || len(got.EnvAllowlist) != 1 || got.EnvAllowlist[0] != "PATH" || got.Process.WorkingDir != workingDir || !got.Process.RequireAbsoluteCommand || len(got.Process.AllowedCommandDirs) != 1 || got.Process.AllowedCommandDirs[0] != workingDir || !got.Process.RejectRoot {
		t.Fatalf("third function executor = %+v", got)
	}
}

func TestResponsesFunctionExecutorsConfigValidation(t *testing.T) {
	disabled := false
	cfg := Config{}
	cfg.ResponsesServer.FunctionExecutors.Enabled = true
	cfg.ResponsesServer.FunctionExecutors.Executors = []ResponsesFunctionExecutorBinding{
		{Name: " lookup ", Type: " STATIC_RESPONSE ", Output: "ok"},
		{Name: "lookup", Type: "static_response", Output: "duplicate"},
		{Name: " ", Type: "static_response", Output: "missing name"},
		{Name: "future", Type: "external_command", Command: "echo ok"},
		{Name: "mystery", Type: "unknown_type"},
		{Name: "off", Type: "static_response", Enabled: &disabled},
	}

	executors := cfg.ResponsesFunctionExecutorsConfig()
	if len(executors.Executors) != 6 {
		t.Fatalf("len(executors) = %d, want 6", len(executors.Executors))
	}
	if got := executors.Executors[0]; got.Name != "lookup" || got.Type != "static_response" || !got.Available || len(got.Warnings) != 0 {
		t.Fatalf("first executor = %+v, want normalized available static_response", got)
	}
	if got := executors.Executors[1]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "duplicate executor name") {
		t.Fatalf("duplicate executor = %+v, want unavailable duplicate warning", got)
	}
	if got := executors.Executors[2]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "name is required") {
		t.Fatalf("empty-name executor = %+v, want unavailable name warning", got)
	}
	if got := executors.Executors[3]; !got.Available || got.Command != "echo ok" || len(got.Warnings) != 0 {
		t.Fatalf("external command executor = %+v, want available external command", got)
	}
	if got := executors.Executors[4]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "unsupported executor type") {
		t.Fatalf("unknown executor = %+v, want unavailable unsupported warning", got)
	}
	if got := executors.Executors[5]; got.Available || len(got.Warnings) != 0 {
		t.Fatalf("disabled executor = %+v, want unavailable without validation warning", got)
	}
	if got := strings.Join(executors.Warnings, " "); strings.Contains(got, "no available executors") {
		t.Fatalf("warnings = %q, did not expect no-available warning when one static executor is available", got)
	}
}

func TestMatchResponsesModelProfilePrefersExactBeforePattern(t *testing.T) {
	cfg := Config{}
	cfg.ResponsesServer.ModelProfiles = []ResponsesModelProfileConfig{
		{
			Pattern:             "gpt-*",
			ContextWindowTokens: 100,
		},
		{
			Name:                "gpt-5",
			ContextWindowTokens: 200,
		},
	}

	match := cfg.MatchResponsesModelProfile("gpt-5")
	if !match.Matched || match.Kind != "exact" || match.Index != 1 || match.Source != "responses_server.model_profiles[1].name" {
		t.Fatalf("exact match = %+v", match)
	}
	if match.Profile.ContextWindowTokens != 200 {
		t.Fatalf("exact profile context window = %d, want 200", match.Profile.ContextWindowTokens)
	}
}

func TestMatchResponsesModelProfileFallsBackToPattern(t *testing.T) {
	cfg := Config{}
	cfg.ResponsesServer.ModelProfiles = []ResponsesModelProfileConfig{
		{
			Name: "qwen3",
		},
		{
			Pattern:                     "gpt-4o*",
			CompactHistoryItemThreshold: 8,
		},
	}

	match := cfg.MatchResponsesModelProfile("gpt-4o-mini")
	if !match.Matched || match.Kind != "pattern" || match.Index != 1 || match.Source != "responses_server.model_profiles[1].pattern" {
		t.Fatalf("pattern match = %+v", match)
	}
	if match.Profile.CompactHistoryItemThreshold != 8 {
		t.Fatalf("pattern profile threshold = %d, want 8", match.Profile.CompactHistoryItemThreshold)
	}
}

func TestMatchResponsesModelProfileNoProfile(t *testing.T) {
	cfg := Config{}
	cfg.ResponsesServer.ModelProfiles = []ResponsesModelProfileConfig{
		{Name: "qwen3"},
		{Pattern: "gpt-4o*"},
	}

	match := cfg.MatchResponsesModelProfile("claude")
	if match.Matched || match.Index != -1 || match.Source != "none" {
		t.Fatalf("no match = %+v", match)
	}
}

func TestResponsesFunctionExecutorsConfigWarnsWhenEnabledWithoutAvailableExecutor(t *testing.T) {
	disabled := false
	cfg := Config{}
	cfg.ResponsesServer.FunctionExecutors.Enabled = true
	cfg.ResponsesServer.FunctionExecutors.Executors = []ResponsesFunctionExecutorBinding{
		{Name: "future", Type: "external_command"},
		{Name: "off", Type: "static_response", Enabled: &disabled},
	}

	executors := cfg.ResponsesFunctionExecutorsConfig()
	if got := strings.Join(executors.Warnings, " "); !strings.Contains(got, "no available executors") {
		t.Fatalf("warnings = %q, want no available executors warning", got)
	}
}

func TestResponsesFunctionExecutorsConfigValidatesExternalCommandProcessIsolation(t *testing.T) {
	cfg := Config{}
	cfg.ResponsesServer.FunctionExecutors.Enabled = true
	cfg.ResponsesServer.FunctionExecutors.Executors = []ResponsesFunctionExecutorBinding{
		{
			Name:    "relative_working_dir",
			Type:    "external_command",
			Command: "/bin/echo",
			Process: ResponsesFunctionExecutorProcessConfig{
				WorkingDir: "relative-dir",
			},
		},
		{
			Name:    "relative_command",
			Type:    "external_command",
			Command: "echo",
			Process: ResponsesFunctionExecutorProcessConfig{
				RequireAbsoluteCommand: true,
			},
		},
	}

	executors := cfg.ResponsesFunctionExecutorsConfig()
	if len(executors.Executors) != 2 {
		t.Fatalf("len(executors) = %d, want 2", len(executors.Executors))
	}
	if got := executors.Executors[0]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "working_dir must be absolute") {
		t.Fatalf("relative working dir executor = %+v, want unavailable working_dir warning", got)
	}
	if got := executors.Executors[1]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "command must be absolute") {
		t.Fatalf("relative command executor = %+v, want unavailable absolute command warning", got)
	}
	if got := strings.Join(executors.Warnings, " "); !strings.Contains(got, "no available executors") {
		t.Fatalf("warnings = %q, want no available executors warning", got)
	}
}

func TestResponsesFunctionExecutorsConfigValidatesAllowedCommandDirs(t *testing.T) {
	allowedDir := t.TempDir()
	commandPath := filepath.Join(allowedDir, "lookup")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write command fixture: %v", err)
	}
	filePath := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file fixture: %v", err)
	}
	outsideCommand := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outsideCommand, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write outside command fixture: %v", err)
	}

	cfg := Config{}
	cfg.ResponsesServer.FunctionExecutors.Enabled = true
	cfg.ResponsesServer.FunctionExecutors.Executors = []ResponsesFunctionExecutorBinding{
		{
			Name:    "allowed",
			Type:    "external_command",
			Command: commandPath,
			Process: ResponsesFunctionExecutorProcessConfig{
				AllowedCommandDirs: []string{" " + allowedDir + " ", ""},
				RejectRoot:         true,
			},
		},
		{
			Name:    "relative_allowed_dir",
			Type:    "external_command",
			Command: commandPath,
			Process: ResponsesFunctionExecutorProcessConfig{
				AllowedCommandDirs: []string{"relative-dir"},
			},
		},
		{
			Name:    "missing_allowed_dir",
			Type:    "external_command",
			Command: commandPath,
			Process: ResponsesFunctionExecutorProcessConfig{
				AllowedCommandDirs: []string{filepath.Join(t.TempDir(), "missing")},
			},
		},
		{
			Name:    "file_allowed_dir",
			Type:    "external_command",
			Command: commandPath,
			Process: ResponsesFunctionExecutorProcessConfig{
				AllowedCommandDirs: []string{filePath},
			},
		},
		{
			Name:    "outside_allowed_dir",
			Type:    "external_command",
			Command: outsideCommand,
			Process: ResponsesFunctionExecutorProcessConfig{
				AllowedCommandDirs: []string{allowedDir},
			},
		},
		{
			Name:    "root_rejected",
			Type:    "external_command",
			Command: commandPath,
			Process: ResponsesFunctionExecutorProcessConfig{
				AllowedCommandDirs: []string{string(filepath.Separator)},
				RejectRoot:         true,
			},
		},
	}

	executors := cfg.ResponsesFunctionExecutorsConfig()
	if len(executors.Executors) != 6 {
		t.Fatalf("len(executors) = %d, want 6", len(executors.Executors))
	}
	if got := executors.Executors[0]; !got.Available || len(got.Warnings) != 0 || len(got.Process.AllowedCommandDirs) != 1 || got.Process.AllowedCommandDirs[0] != allowedDir || !got.Process.RejectRoot {
		t.Fatalf("allowed executor = %+v, want available with trimmed allowed dirs", got)
	}
	if got := executors.Executors[1]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "allowed_command_dirs entries must be absolute") {
		t.Fatalf("relative allowed dir executor = %+v, want absolute warning", got)
	}
	if got := executors.Executors[2]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "allowed_command_dirs entry is not accessible") {
		t.Fatalf("missing allowed dir executor = %+v, want accessible warning", got)
	}
	if got := executors.Executors[3]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "allowed_command_dirs entries must be directories") {
		t.Fatalf("file allowed dir executor = %+v, want directory warning", got)
	}
	if got := executors.Executors[4]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "command must resolve inside process.allowed_command_dirs") {
		t.Fatalf("outside command executor = %+v, want allowed dir warning", got)
	}
	if got := executors.Executors[5]; got.Available || !strings.Contains(strings.Join(got.Warnings, " "), "must not include filesystem root") {
		t.Fatalf("root rejected executor = %+v, want reject_root warning", got)
	}
}

func TestResponsesServerEnvOverrides(t *testing.T) {
	t.Setenv("LLM_TRACELAB_RESPONSES_ENABLED", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_DEFAULT_MODEL", "env-model")
	t.Setenv("LLM_TRACELAB_RESPONSES_FORCE_STORE", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_MAX_REQUEST_BODY_BYTES", "2097152")
	t.Setenv("LLM_TRACELAB_RESPONSES_PATH", "/custom/responses")
	t.Setenv("LLM_TRACELAB_RESPONSES_AUTO_COMPACT", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_COMPACT_HISTORY_ITEM_THRESHOLD", "7")
	t.Setenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_ENABLED", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_TIMEOUT", "3s")
	t.Setenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_MAX_RESULT_BYTES", "256")
	t.Setenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_REDACT_ARGUMENTS", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_FUNCTION_EXECUTORS_REDACT_OUTPUT", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_CODEX_COMPAT_ENABLED", "true")
	t.Setenv("LLM_TRACELAB_RESPONSES_CODEX_COMPAT_AUTO_INJECT_HOSTED_TOOLS", " web_search, mcp ,,")
	t.Setenv("LLM_TRACELAB_RESPONSES_CODEX_COMPAT_INJECT_WHEN_TOOLS_ABSENT", "false")
	t.Setenv("LLM_TRACELAB_RESPONSES_CODEX_COMPAT_PRESERVE_CLIENT_TOOLS", "false")
	t.Setenv("LLM_TRACELAB_RESPONSES_CODEX_COMPAT_DEFAULT_TOOL_CHOICE", "required")

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
	if !cfg.ResponsesAutoCompactEnabled() {
		t.Fatalf("ResponsesAutoCompactEnabled() = false, want true")
	}
	if got := cfg.ResponsesCompactHistoryItemThreshold(); got != 7 {
		t.Fatalf("ResponsesCompactHistoryItemThreshold() = %d, want 7", got)
	}
	executors := cfg.ResponsesFunctionExecutorsConfig()
	if !executors.Enabled || executors.Timeout != 3*time.Second || executors.MaxResultBytes != 256 || !executors.Redaction.Arguments || !executors.Redaction.Output {
		t.Fatalf("ResponsesFunctionExecutorsConfig() = %+v, want env overrides", executors)
	}
	codexCompat := cfg.ResponsesCodexCompatConfig()
	if !codexCompat.Enabled || len(codexCompat.AutoInjectHostedTools) != 2 || codexCompat.AutoInjectHostedTools[0] != "web_search" || codexCompat.AutoInjectHostedTools[1] != "mcp" || codexCompat.InjectWhenToolsAbsent == nil || *codexCompat.InjectWhenToolsAbsent || codexCompat.PreserveClientTools == nil || *codexCompat.PreserveClientTools || codexCompat.DefaultToolChoice != "required" {
		t.Fatalf("ResponsesCodexCompatConfig() = %+v, want env overrides", codexCompat)
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

func TestMCPToolsConfigDisabledByDefault(t *testing.T) {
	clearMCPToolsEnv(t)
	cfg := Config{}
	mcpTools := cfg.MCPToolsConfig()

	if cfg.MCPToolsEnabled() {
		t.Fatalf("MCPToolsEnabled() = true, want false")
	}
	if mcpTools.Enabled {
		t.Fatalf("MCPToolsConfig().Enabled = true, want false")
	}
	if mcpTools.DefaultTimeoutMS != 60000 {
		t.Fatalf("MCPToolsConfig().DefaultTimeoutMS = %d, want 60000", mcpTools.DefaultTimeoutMS)
	}
	if mcpTools.MaxResultBytes != 65536 {
		t.Fatalf("MCPToolsConfig().MaxResultBytes = %d, want 65536", mcpTools.MaxResultBytes)
	}
	if len(mcpTools.Servers) != 0 {
		t.Fatalf("MCPToolsConfig().Servers = %d, want 0", len(mcpTools.Servers))
	}
}

func TestMCPToolsEnvOverrides(t *testing.T) {
	t.Setenv("LLM_TRACELAB_TOOLS_MCP_ENABLED", "true")
	t.Setenv("LLM_TRACELAB_TOOLS_MCP_DEFAULT_TIMEOUT_MS", "2500")
	t.Setenv("LLM_TRACELAB_TOOLS_MCP_MAX_RESULT_BYTES", "4096")

	cfg := Config{}
	cfg.Tools.MCP.DefaultTimeoutMS = 1000
	cfg.Tools.MCP.MaxResultBytes = 2048
	applyEnvOverrides(&cfg)

	mcpTools := cfg.MCPToolsConfig()
	if !cfg.MCPToolsEnabled() {
		t.Fatalf("MCPToolsEnabled() = false, want true")
	}
	if mcpTools.DefaultTimeoutMS != 2500 {
		t.Fatalf("MCPToolsConfig().DefaultTimeoutMS = %d, want 2500", mcpTools.DefaultTimeoutMS)
	}
	if mcpTools.MaxResultBytes != 4096 {
		t.Fatalf("MCPToolsConfig().MaxResultBytes = %d, want 4096", mcpTools.MaxResultBytes)
	}
}

func TestLoadParsesMCPToolsConfigFromYAML(t *testing.T) {
	clearMCPToolsEnv(t)
	path := writeTempConfig(t, `
tools:
  mcp:
    enabled: true
    default_timeout_ms: 1500
    max_result_bytes: 8192
    servers:
      - id: linear
        label: Linear
        url: "https://mcp.example.com/sse?api_key=query-secret"
        bearer_token_env: LINEAR_MCP_TOKEN
        enabled_tools: [create_issue, list_issues]
        disabled_tools: [delete_issue]
      - id: disabled
        url: "https://disabled.example.com/mcp"
        enabled: false
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	mcpTools := cfg.MCPToolsConfig()
	if !cfg.MCPToolsEnabled() {
		t.Fatalf("MCPToolsEnabled() = false, want true")
	}
	if mcpTools.DefaultTimeoutMS != 1500 || mcpTools.MaxResultBytes != 8192 {
		t.Fatalf("MCPToolsConfig() = %+v, want YAML timeout/result limits", mcpTools)
	}
	if len(mcpTools.Servers) != 2 {
		t.Fatalf("len(MCPToolsConfig().Servers) = %d, want 2", len(mcpTools.Servers))
	}
	first := mcpTools.Servers[0]
	if first.ID != "linear" || first.Label != "Linear" || first.URL != "https://mcp.example.com/sse?api_key=query-secret" || first.BearerTokenEnv != "LINEAR_MCP_TOKEN" || !first.EnabledOrDefault() {
		t.Fatalf("first server = %+v, want parsed enabled server", first)
	}
	if strings.Join(first.EnabledTools, ",") != "create_issue,list_issues" || strings.Join(first.DisabledTools, ",") != "delete_issue" {
		t.Fatalf("first server tools = %+v/%+v", first.EnabledTools, first.DisabledTools)
	}
	if mcpTools.Servers[1].EnabledOrDefault() {
		t.Fatalf("second server EnabledOrDefault() = true, want false")
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

func clearMCPToolsEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"LLM_TRACELAB_TOOLS_MCP_ENABLED",
		"LLM_TRACELAB_TOOLS_MCP_DEFAULT_TIMEOUT_MS",
		"LLM_TRACELAB_TOOLS_MCP_MAX_RESULT_BYTES",
	} {
		t.Setenv(name, "")
	}
}
