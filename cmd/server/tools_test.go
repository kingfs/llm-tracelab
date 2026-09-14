package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type toolsStatusEnvelopeForTest struct {
	OK      bool              `json:"ok"`
	Command string            `json:"command"`
	Result  toolsStatusResult `json:"result"`
}

func TestRootCommandRegistersToolsStatus(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	found, _, err := cmd.Find([]string{"tools", "status"})
	if err != nil || found.CommandPath() != "llm-tracelab tools status" {
		t.Fatalf("found command path = %q err=%v", found.CommandPath(), err)
	}
}

func TestToolsStatusJSONReadyForCodexWebSearch(t *testing.T) {
	t.Parallel()

	configPath := writeToolsStatusTestConfig(t, `
responses_server:
  codex_compat:
    enabled: true
    auto_inject_hosted_tools: ["web_search", "mcp"]
tools:
  web_search:
    enabled: true
    provider: mock
    max_results: 3
    timeout_ms: 2500
  mcp:
    enabled: false
`)
	out, err := executeToolsStatusForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v output=%s", err, out)
	}
	envelope := decodeToolsStatusEnvelopeForTest(t, out)
	if !envelope.OK || envelope.Command != "tools.status" {
		t.Fatalf("envelope = %+v, want tools.status ok", envelope)
	}
	result := envelope.Result
	if !result.ResponsesServer.CodexCompat.Enabled {
		t.Fatalf("responses_server status = %+v, want enabled", result.ResponsesServer)
	}
	if !result.ReadyForCodexWebSearch || result.ReadyForCodexWebSearchWhy == "" {
		t.Fatalf("ready_for_codex_web_search = %t reason=%q", result.ReadyForCodexWebSearch, result.ReadyForCodexWebSearchWhy)
	}
	if got := strings.Join(result.ResponsesServer.CodexCompat.InjectableTools, ","); got != "web_search" {
		t.Fatalf("injectable_tools = %q, want web_search", got)
	}
	if result.Tools.WebSearch.Readiness != toolsReadinessReady || result.Tools.WebSearch.Provider != "mock" || result.Tools.WebSearch.MaxResults != 3 || result.Tools.WebSearch.TimeoutMS != 2500 {
		t.Fatalf("web_search status = %+v, want ready mock", result.Tools.WebSearch)
	}
}

func TestToolsStatusWebSearchSearXNGWithoutBaseURLNotReady(t *testing.T) {
	t.Parallel()

	configPath := writeToolsStatusTestConfig(t, `
responses_server:
  codex_compat:
    enabled: true
    auto_inject_hosted_tools: ["web_search"]
tools:
  web_search:
    enabled: true
    provider: searxng
  mcp:
    enabled: false
`)
	out, err := executeToolsStatusForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v output=%s", err, out)
	}
	result := decodeToolsStatusEnvelopeForTest(t, out).Result
	if result.ReadyForCodexWebSearch {
		t.Fatalf("ready_for_codex_web_search = true, want false")
	}
	if result.Tools.WebSearch.BaseURLPresent || result.Tools.WebSearch.Readiness != toolsReadinessNotReady {
		t.Fatalf("web_search status = %+v, want missing base URL not_ready", result.Tools.WebSearch)
	}
	if len(result.Tools.WebSearch.Warnings) == 0 {
		t.Fatalf("web_search warnings empty, want provider configuration warning")
	}
}

func TestToolsStatusMCPEnabledWithoutServersNotReady(t *testing.T) {
	t.Parallel()

	configPath := writeToolsStatusTestConfig(t, `
tools:
  mcp:
    enabled: true
`)
	out, err := executeToolsStatusForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v output=%s", err, out)
	}
	mcp := decodeToolsStatusEnvelopeForTest(t, out).Result.Tools.MCP
	if mcp.Readiness != toolsReadinessNotReady || len(mcp.Warnings) == 0 {
		t.Fatalf("mcp status = %+v, want not_ready warning", mcp)
	}
}

func TestToolsStatusMCPRedactsServerSecrets(t *testing.T) {
	t.Setenv("TOOLS_STATUS_TOKEN_ENV", "bearer-secret-value")
	configPath := writeToolsStatusTestConfig(t, `
tools:
  mcp:
    enabled: true
    servers:
      - id: workspace
        label: Workspace
        url: "https://example.com/mcp?api_key=query-secret&token=other-secret"
        bearer_token_env: TOOLS_STATUS_TOKEN_ENV
        enabled_tools: ["read_file"]
`)
	out, err := executeToolsStatusForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("Execute() error = %v output=%s", err, out)
	}
	if strings.Contains(out, "query-secret") || strings.Contains(out, "other-secret") || strings.Contains(out, "bearer-secret-value") {
		t.Fatalf("tools status leaked secret: %s", out)
	}
	result := decodeToolsStatusEnvelopeForTest(t, out).Result
	if result.Tools.MCP.Readiness != toolsReadinessReady || result.Tools.MCP.EnabledServerCount != 1 {
		t.Fatalf("mcp status = %+v, want ready enabled server", result.Tools.MCP)
	}
	if len(result.Tools.MCP.Servers) != 1 || !strings.Contains(result.Tools.MCP.Servers[0].URL, "%3Credacted%3E") || !result.Tools.MCP.Servers[0].BearerTokenConfigured {
		t.Fatalf("mcp server = %+v, want redacted URL and configured bearer env", result.Tools.MCP.Servers)
	}
}

func TestToolsStatusTextIsShort(t *testing.T) {
	t.Parallel()

	configPath := writeToolsStatusTestConfig(t, `
responses_server:
tools:
  web_search:
    enabled: false
  mcp:
    enabled: false
`)
	out, err := executeToolsStatusForTest(configPath)
	if err != nil {
		t.Fatalf("Execute() error = %v output=%s", err, out)
	}
	for _, want := range []string{"tools: ready_for_codex_web_search=false", "tools.web_search:", "tools.mcp:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output missing %q: %s", want, out)
		}
	}
}

func executeToolsStatusForTest(configPath string, args ...string) (string, error) {
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	fullArgs := append([]string{"-c", configPath, "tools", "status"}, args...)
	cmd.SetArgs(fullArgs)
	err := cmd.Execute()
	return out.String(), err
}

func writeToolsStatusTestConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return configPath
}

func decodeToolsStatusEnvelopeForTest(t *testing.T, output string) toolsStatusEnvelopeForTest {
	t.Helper()
	var envelope toolsStatusEnvelopeForTest
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, output)
	}
	return envelope
}
