package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
