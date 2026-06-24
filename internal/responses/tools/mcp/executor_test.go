package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/hosted"
)

func TestExecutorHappyPath(t *testing.T) {
	executor := testExecutor(true)

	result, err := executor.ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type: hosted.ToolTypeMCP,
		Tool: protocol.Tool{
			Type:         hosted.ToolTypeMCP,
			ServerLabel:  "workspace",
			AllowedTools: []any{"lookup"},
		},
		Arguments: json.RawMessage(`{"tool":"lookup","arguments":{"query":"trace"}}`),
	})
	if err != nil {
		t.Fatalf("ExecuteHostedTool returned error: %v", err)
	}
	if result.Status != hosted.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	output, ok := result.Output.(Output)
	if !ok {
		t.Fatalf("output = %T, want Output", result.Output)
	}
	if output.ServerLabel != "workspace" || output.ToolName != "lookup" {
		t.Fatalf("output server/tool = %q/%q", output.ServerLabel, output.ToolName)
	}
	if output.Text != "mock lookup complete" {
		t.Fatalf("text result = %q", output.Text)
	}
	if got := output.Arguments["query"]; got != "trace" {
		t.Fatalf("argument query = %#v, want trace", got)
	}
}

func TestExecutorDisabled(t *testing.T) {
	_, err := testExecutor(false).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"trace"}`),
	})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("ExecuteHostedTool error = %v, want ErrDisabled", err)
	}
}

func TestExecutorDenylistedTool(t *testing.T) {
	_, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:       "workspace",
			Enabled:     true,
			DeniedTools: []string{"delete_trace"},
			Tools: []ToolDescriptor{{
				Name:    "delete_trace",
				Enabled: true,
			}},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Name:      "delete_trace",
		Arguments: json.RawMessage(`{"server_label":"workspace"}`),
	})
	if !errors.Is(err, ErrToolDenied) {
		t.Fatalf("ExecuteHostedTool error = %v, want ErrToolDenied", err)
	}
}

func TestExecutorUnknownTool(t *testing.T) {
	_, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:   "workspace",
			Enabled: true,
			Tools: []ToolDescriptor{{
				Name:    "lookup",
				Enabled: true,
			}},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Name:      "missing",
		Arguments: json.RawMessage(`{"server_label":"workspace"}`),
	})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("ExecuteHostedTool error = %v, want ErrUnknownTool", err)
	}
}

func TestExecutorRemoteStreamableHTTPHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var req struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.JSONRPC != "2.0" || req.Method != "tools/call" || req.Params.Name != "lookup" {
			t.Fatalf("json-rpc request = %#v", req)
		}
		if got := req.Params.Arguments["query"]; got != "trace" {
			t.Fatalf("argument query = %#v, want trace", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"remote lookup complete"}],"structuredContent":{"ok":true,"count":2}}}`))
	}))
	defer server.Close()

	result, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:        "workspace",
			Enabled:      true,
			URL:          server.URL,
			Timeout:      time.Second,
			AllowedTools: []string{"lookup"},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"lookup","arguments":{"query":"trace"}}`),
	})
	if err != nil {
		t.Fatalf("ExecuteHostedTool returned error: %v", err)
	}
	output, ok := result.Output.(Output)
	if !ok {
		t.Fatalf("output = %T, want Output", result.Output)
	}
	if output.Text != "remote lookup complete" {
		t.Fatalf("text = %q", output.Text)
	}
	structured, ok := output.StructuredResult.(map[string]any)
	if !ok || structured["ok"] != true {
		t.Fatalf("structured result = %#v", output.StructuredResult)
	}
	if result.Summary.Output.Value.(map[string]any)["response_bytes"] == 0 {
		t.Fatalf("summary response_bytes = %#v", result.Summary.Output.Value)
	}
}

func TestExecutorRemoteStreamableHTTPBearerHeader(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "env-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer explicit-secret" {
			t.Fatalf("authorization = %q, want explicit token", got)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer server.Close()

	_, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:          "workspace",
			Enabled:        true,
			URL:            server.URL,
			BearerToken:    "explicit-secret",
			BearerTokenEnv: "MCP_TEST_TOKEN",
			AllowedTools:   []string{"lookup"},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"lookup"}`),
	})
	if err != nil {
		t.Fatalf("ExecuteHostedTool returned error: %v", err)
	}
}

func TestExecutorRemoteStreamableHTTPAllowDenyPolicy(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`))
	}))
	defer server.Close()

	executor := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:        "workspace",
			Enabled:      true,
			URL:          server.URL,
			AllowedTools: []string{"lookup"},
			DeniedTools:  []string{"delete_trace"},
		}},
	})
	_, err := executor.ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"delete_trace"}`),
	})
	if !errors.Is(err, ErrToolDenied) {
		t.Fatalf("delete_trace error = %v, want ErrToolDenied", err)
	}
	_, err = executor.ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"read_trace"}`),
	})
	if !errors.Is(err, ErrToolNotAllowed) {
		t.Fatalf("read_trace error = %v, want ErrToolNotAllowed", err)
	}
	if called {
		t.Fatalf("remote server was called for blocked tools")
	}
}

func TestExecutorRemoteStreamableHTTPMissingBearerEnv(t *testing.T) {
	_, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:          "workspace",
			Enabled:        true,
			URL:            "http://127.0.0.1:1/mcp",
			BearerTokenEnv: "MCP_TEST_MISSING_TOKEN",
			AllowedTools:   []string{"lookup"},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"lookup"}`),
	})
	if !errors.Is(err, ErrMissingBearerToken) {
		t.Fatalf("ExecuteHostedTool error = %v, want ErrMissingBearerToken", err)
	}
}

func TestExecutorRemoteStreamableHTTPResultTooLarge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + strings.Repeat("x", 256) + `"}]}}`))
	}))
	defer server.Close()

	_, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:          "workspace",
			Enabled:        true,
			URL:            server.URL,
			MaxResultBytes: 64,
			AllowedTools:   []string{"lookup"},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"lookup"}`),
	})
	var tooLarge ResultTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("ExecuteHostedTool error = %T %v, want ResultTooLargeError", err, err)
	}
}

func TestExecutorRemoteStreamableHTTPRedactsBearerFromErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"token explicit-secret rejected"}}`))
	}))
	defer server.Close()

	_, err := NewExecutor(Options{
		Enabled: true,
		Servers: []ServerDescriptor{{
			Label:        "workspace",
			Enabled:      true,
			URL:          server.URL,
			BearerToken:  "explicit-secret",
			AllowedTools: []string{"lookup"},
		}},
	}).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","tool":"lookup"}`),
	})
	if err == nil {
		t.Fatalf("ExecuteHostedTool returned nil error, want json-rpc error")
	}
	if strings.Contains(err.Error(), "explicit-secret") {
		t.Fatalf("error leaked bearer token: %v", err)
	}
}

func TestExecutorArgumentSummaryRedactsLongArguments(t *testing.T) {
	secret := strings.Repeat("sensitive-token-", 40)
	raw := json.RawMessage(`{"server_label":"workspace","tool":"lookup","query":"` + secret + `"}`)

	result, err := testExecutor(true).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: raw,
	})
	if err != nil {
		t.Fatalf("ExecuteHostedTool returned error: %v", err)
	}

	encoded, err := json.Marshal(result.Summary.Arguments.Value)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("argument summary contains raw long argument: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"redacted":true`) {
		t.Fatalf("argument summary = %s, want redacted true", encoded)
	}
}

func TestExecutorInvalidMissingTool(t *testing.T) {
	_, err := testExecutor(true).ExecuteHostedTool(context.Background(), hosted.ToolContext{}, hosted.ToolCall{
		Type:      hosted.ToolTypeMCP,
		Arguments: json.RawMessage(`{"server_label":"workspace","query":"trace"}`),
	})
	if !errors.Is(err, ErrMissingTool) {
		t.Fatalf("ExecuteHostedTool error = %v, want ErrMissingTool", err)
	}
}

func TestDescriptorFromProtocolTool(t *testing.T) {
	descriptor := DescriptorFromProtocolTool(protocol.Tool{
		Type:         "mcp",
		ServerLabel:  "workspace",
		AllowedTools: map[string]any{"tools": []any{"lookup", "read_trace"}},
		Extra:        map[string]any{"denied_tools": []any{"delete_trace"}},
	})

	if descriptor.Type != "mcp" || descriptor.ServerLabel != "workspace" {
		t.Fatalf("descriptor type/server = %q/%q", descriptor.Type, descriptor.ServerLabel)
	}
	if strings.Join(descriptor.AllowedTools, ",") != "lookup,read_trace" {
		t.Fatalf("allowed tools = %#v", descriptor.AllowedTools)
	}
	if strings.Join(descriptor.DeniedTools, ",") != "delete_trace" {
		t.Fatalf("denied tools = %#v", descriptor.DeniedTools)
	}
}

func testExecutor(enabled bool) *Executor {
	return NewExecutor(Options{
		Enabled: enabled,
		Servers: []ServerDescriptor{{
			ID:           "srv-workspace",
			Label:        "workspace",
			Enabled:      true,
			AllowedTools: []string{"lookup", "read_trace"},
			Tools: []ToolDescriptor{{
				Name:             "lookup",
				Enabled:          true,
				TextResult:       "mock lookup complete",
				StructuredResult: map[string]any{"ok": true},
			}, {
				Name:    "read_trace",
				Enabled: true,
			}},
		}},
	})
}
