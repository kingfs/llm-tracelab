package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigInspectSourcesDefaultsJSON(t *testing.T) {
	clearConfigInspectSourceEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "config", "inspect"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Database struct {
				Driver                  string `json:"driver"`
				DSN                     string `json:"dsn"`
				AutoMigrate             bool   `json:"auto_migrate"`
				MigrationMode           string `json:"migration_mode"`
				ProductionStorageDriver string `json:"production_storage_driver"`
				ProductionReady         bool   `json:"production_ready"`
				StorageRole             string `json:"storage_role"`
				StorageContract         string `json:"storage_contract"`
			} `json:"database"`
			ResponsesServer struct {
				Path        string `json:"path"`
				CodexCompat struct {
					Enabled               bool     `json:"enabled"`
					AutoInjectHostedTools []string `json:"auto_inject_hosted_tools"`
					InjectWhenToolsAbsent bool     `json:"inject_when_tools_absent"`
					PreserveClientTools   bool     `json:"preserve_client_tools"`
					DefaultToolChoice     any      `json:"default_tool_choice"`
				} `json:"codex_compat"`
			} `json:"responses_server"`
			Tools struct {
				WebSearch struct {
					Provider string `json:"provider"`
				} `json:"web_search"`
			} `json:"tools"`
			Sources struct {
				ConfigPath string `json:"config_path"`
				Server     struct {
					Port string `json:"port"`
				} `json:"server"`
				Database struct {
					Driver      string `json:"driver"`
					DSN         string `json:"dsn"`
					AutoMigrate string `json:"auto_migrate"`
				} `json:"database"`
				ResponsesServer struct {
					Path        string `json:"path"`
					CodexCompat struct {
						Enabled               string `json:"enabled"`
						AutoInjectHostedTools string `json:"auto_inject_hosted_tools"`
						InjectWhenToolsAbsent string `json:"inject_when_tools_absent"`
						PreserveClientTools   string `json:"preserve_client_tools"`
						DefaultToolChoice     string `json:"default_tool_choice"`
					} `json:"codex_compat"`
				} `json:"responses_server"`
				Tools struct {
					WebSearch struct {
						Provider string `json:"provider"`
					} `json:"web_search"`
				} `json:"tools"`
				Upstreams struct {
					Targets     string `json:"targets"`
					Credentials string `json:"credentials"`
				} `json:"upstreams"`
			} `json:"sources"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK {
		t.Fatalf("envelope.OK = false, output=%q", out.String())
	}
	if envelope.Result.Database.Driver != "sqlite" || envelope.Result.Database.DSN != "llm_tracelab.sqlite3" || !envelope.Result.Database.AutoMigrate {
		t.Fatalf("database result = %+v", envelope.Result.Database)
	}
	if envelope.Result.Database.MigrationMode != "schema-init" || envelope.Result.Database.ProductionStorageDriver != "postgres" || envelope.Result.Database.ProductionReady || envelope.Result.Database.StorageRole != "legacy_dev_test_compatibility" || envelope.Result.Database.StorageContract != "sqlite_startup_schema_fallback_for_legacy_dev_test_only" {
		t.Fatalf("database storage contract = %+v", envelope.Result.Database)
	}
	if envelope.Result.ResponsesServer.Path != "/v1/responses" {
		t.Fatalf("responses path = %q", envelope.Result.ResponsesServer.Path)
	}
	if envelope.Result.ResponsesServer.CodexCompat.Enabled || len(envelope.Result.ResponsesServer.CodexCompat.AutoInjectHostedTools) != 0 || !envelope.Result.ResponsesServer.CodexCompat.InjectWhenToolsAbsent || !envelope.Result.ResponsesServer.CodexCompat.PreserveClientTools || envelope.Result.ResponsesServer.CodexCompat.DefaultToolChoice != "auto" {
		t.Fatalf("responses codex_compat = %+v", envelope.Result.ResponsesServer.CodexCompat)
	}
	if envelope.Result.Tools.WebSearch.Provider != "disabled" {
		t.Fatalf("web_search provider = %q", envelope.Result.Tools.WebSearch.Provider)
	}
	sources := envelope.Result.Sources
	if sources.ConfigPath != configSourceEffective {
		t.Fatalf("sources.config_path = %q", sources.ConfigPath)
	}
	if sources.Server.Port != configSourceEmpty {
		t.Fatalf("sources.server.port = %q", sources.Server.Port)
	}
	if sources.Database.Driver != configSourceDefault || sources.Database.DSN != configSourceDerived || sources.Database.AutoMigrate != configSourceDefault {
		t.Fatalf("sources.database = %+v", sources.Database)
	}
	if sources.ResponsesServer.Path != configSourceDefault {
		t.Fatalf("sources.responses_server.path = %q", sources.ResponsesServer.Path)
	}
	if sources.ResponsesServer.CodexCompat.Enabled != configSourceDefault || sources.ResponsesServer.CodexCompat.AutoInjectHostedTools != configSourceDefault || sources.ResponsesServer.CodexCompat.InjectWhenToolsAbsent != configSourceDefault || sources.ResponsesServer.CodexCompat.PreserveClientTools != configSourceDefault || sources.ResponsesServer.CodexCompat.DefaultToolChoice != configSourceDefault {
		t.Fatalf("sources.responses_server.codex_compat = %+v", sources.ResponsesServer.CodexCompat)
	}
	if sources.Tools.WebSearch.Provider != configSourceDefault {
		t.Fatalf("sources.tools.web_search.provider = %q", sources.Tools.WebSearch.Provider)
	}
	if sources.Upstreams.Targets != configSourceNotConfigured || sources.Upstreams.Credentials != configSourceNotConfigured {
		t.Fatalf("sources.upstreams = %+v", sources.Upstreams)
	}
}

func TestConfigInspectSourcesConfigFileAndTextSummary(t *testing.T) {
	clearConfigInspectSourceEnv(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
server:
  port: "8088"
monitor:
  port: "9099"
mcp:
  enabled: true
  path: mcp
database:
  driver: postgres
  dsn: postgres://app:secret-db@example.com:5432/traces
  auto_migrate: false
trace:
  output_dir: /tmp/traces
responses_server:
  enabled: true
  default_model: gpt-5
  path: /v1/responses
  codex_compat:
    enabled: true
    auto_inject_hosted_tools: [web_search]
    inject_when_tools_absent: false
    preserve_client_tools: false
    default_tool_choice: required
tools:
  web_search:
    enabled: true
    provider: searxng
    base_url: https://search.example.com/search?api_key=secret-web
upstreams:
  - id: primary
    upstream:
      base_url: https://api.example.com/v1?token=secret-token
      api_key: secret-key
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "config", "inspect"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(json) error = %v", err)
	}
	for _, secret := range []string{"secret-db", "secret-web", "secret-token", "secret-key"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("config inspect JSON leaked secret marker %q", secret)
		}
	}

	var envelope struct {
		Result struct {
			Sources struct {
				Server struct {
					Port string `json:"port"`
				} `json:"server"`
				Monitor struct {
					Port string `json:"port"`
				} `json:"monitor"`
				MCP struct {
					Enabled string `json:"enabled"`
					Path    string `json:"path"`
				} `json:"mcp"`
				Database struct {
					Driver      string `json:"driver"`
					DSN         string `json:"dsn"`
					AutoMigrate string `json:"auto_migrate"`
				} `json:"database"`
				Trace struct {
					OutputDir string `json:"output_dir"`
				} `json:"trace"`
				ResponsesServer struct {
					Enabled      string `json:"enabled"`
					Path         string `json:"path"`
					DefaultModel string `json:"default_model"`
					CodexCompat  struct {
						Enabled               string `json:"enabled"`
						AutoInjectHostedTools string `json:"auto_inject_hosted_tools"`
						InjectWhenToolsAbsent string `json:"inject_when_tools_absent"`
						PreserveClientTools   string `json:"preserve_client_tools"`
						DefaultToolChoice     string `json:"default_tool_choice"`
					} `json:"codex_compat"`
				} `json:"responses_server"`
				Tools struct {
					WebSearch struct {
						Enabled  string `json:"enabled"`
						Provider string `json:"provider"`
						BaseURL  string `json:"base_url"`
					} `json:"web_search"`
				} `json:"tools"`
				Upstreams struct {
					Targets     string `json:"targets"`
					Credentials string `json:"credentials"`
				} `json:"upstreams"`
			} `json:"sources"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	sources := envelope.Result.Sources
	for name, got := range map[string]string{
		"server.port":                           sources.Server.Port,
		"monitor.port":                          sources.Monitor.Port,
		"mcp.enabled":                           sources.MCP.Enabled,
		"mcp.path":                              sources.MCP.Path,
		"database.driver":                       sources.Database.Driver,
		"database.dsn":                          sources.Database.DSN,
		"database.auto_migrate":                 sources.Database.AutoMigrate,
		"trace.output_dir":                      sources.Trace.OutputDir,
		"responses_server.enabled":              sources.ResponsesServer.Enabled,
		"responses_server.path":                 sources.ResponsesServer.Path,
		"responses_server.default_model":        sources.ResponsesServer.DefaultModel,
		"responses_server.codex_compat.enabled": sources.ResponsesServer.CodexCompat.Enabled,
		"responses_server.codex_compat.auto_inject_hosted_tools": sources.ResponsesServer.CodexCompat.AutoInjectHostedTools,
		"responses_server.codex_compat.inject_when_tools_absent": sources.ResponsesServer.CodexCompat.InjectWhenToolsAbsent,
		"responses_server.codex_compat.preserve_client_tools":    sources.ResponsesServer.CodexCompat.PreserveClientTools,
		"responses_server.codex_compat.default_tool_choice":      sources.ResponsesServer.CodexCompat.DefaultToolChoice,
		"tools.web_search.enabled":                               sources.Tools.WebSearch.Enabled,
		"tools.web_search.provider":                              sources.Tools.WebSearch.Provider,
		"tools.web_search.base_url":                              sources.Tools.WebSearch.BaseURL,
		"upstreams.targets":                                      sources.Upstreams.Targets,
		"upstreams.credentials":                                  sources.Upstreams.Credentials,
	} {
		if got != configSourceConfigFile {
			t.Fatalf("sources[%s] = %q, want %q", name, got, configSourceConfigFile)
		}
	}

	cmd = newRootCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "config", "inspect"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(text) error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "sources:") || !strings.Contains(text, "database.dsn=config_file") || !strings.Contains(text, "responses_server.codex_compat.enabled=config_file") || !strings.Contains(text, "upstreams.targets=config_file") {
		t.Fatalf("text output missing source summary: %q", text)
	}
	for _, secret := range []string{"secret-db", "secret-web", "secret-token", "secret-key"} {
		if strings.Contains(text, secret) {
			t.Fatalf("config inspect text leaked secret marker %q", secret)
		}
	}
}

func clearConfigInspectSourceEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"LLM_TRACELAB_SERVER_PORT",
		"LLM_TRACELAB_MONITOR_PORT",
		"LLM_TRACELAB_MCP_ENABLED",
		"LLM_TRACELAB_MCP_PATH",
		"LLM_TRACELAB_DATABASE_DRIVER",
		"LLM_TRACELAB_DATABASE_DSN",
		"LLM_TRACELAB_DATABASE_AUTO_MIGRATE",
		"LLM_TRACELAB_OUTPUT_DIR",
		"LLM_TRACELAB_TRACE_OUTPUT_DIR",
		"LLM_TRACELAB_RESPONSES_ENABLED",
		"LLM_TRACELAB_RESPONSES_DEFAULT_MODEL",
		"LLM_TRACELAB_RESPONSES_PATH",
		"LLM_TRACELAB_RESPONSES_CODEX_COMPAT_ENABLED",
		"LLM_TRACELAB_RESPONSES_CODEX_COMPAT_AUTO_INJECT_HOSTED_TOOLS",
		"LLM_TRACELAB_RESPONSES_CODEX_COMPAT_INJECT_WHEN_TOOLS_ABSENT",
		"LLM_TRACELAB_RESPONSES_CODEX_COMPAT_PRESERVE_CLIENT_TOOLS",
		"LLM_TRACELAB_RESPONSES_CODEX_COMPAT_DEFAULT_TOOL_CHOICE",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_PROVIDER",
		"LLM_TRACELAB_TOOLS_WEB_SEARCH_BASE_URL",
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
	} {
		t.Setenv(name, "")
	}
}
