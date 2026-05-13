package analyzer

import (
	"context"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/observe"
)

func TestDefaultDetectorsFindAuditSignals(t *testing.T) {
	toolCall := observe.SemanticNode{
		ID:             "node-shell",
		ProviderType:   "local_shell_call",
		NormalizedType: observe.NodeToolCall,
		Path:           "$.output[0]",
		Text:           `{"cmd":"rm -rf /"}`,
		Metadata:       map[string]any{"arguments": `{"cmd":"rm -rf /"}`},
	}
	secret := observe.SemanticNode{
		ID:             "node-secret",
		ProviderType:   "output_text",
		NormalizedType: observe.NodeText,
		Path:           "$.output[1].content[0]",
		Text:           "token sk-test_abcdefghijklmnopqrstuvwxyz",
	}
	refusal := observe.SemanticNode{
		ID:             "node-refusal",
		ProviderType:   "refusal",
		NormalizedType: observe.NodeRefusal,
		Path:           "$.output[2].content[0]",
		Text:           "I can't help with that.",
	}
	errorResult := observe.SemanticNode{
		ID:             "node-error",
		ProviderType:   "function_call_output",
		NormalizedType: observe.NodeToolResult,
		Path:           "$.output[3]",
		Text:           `{"error":"failed"}`,
		Metadata:       map[string]any{"status": "error"},
	}
	obs := observe.TraceObservation{
		TraceID: "trace-audit",
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{toolCall, secret, refusal, errorResult},
		},
		Tools: observe.ObservationTools{
			Calls: []observe.ToolCallObservation{{
				ID:       "call-shell",
				Kind:     "local_shell_call",
				ArgsText: `{"cmd":"rm -rf /"}`,
				NodeID:   "node-shell",
			}},
			Results: []observe.ToolResultObservation{{
				ID:      "call-error",
				Kind:    "function_call_output",
				Text:    `{"error":"failed"}`,
				NodeID:  "node-error",
				IsError: true,
			}},
		},
	}

	findings, err := NewRunner().Analyze(context.Background(), obs)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	want := map[string]bool{
		"filesystem_destructive_operation": false,
		"credential_leak":                  false,
		"model_refusal":                    false,
		"tool_result_error":                false,
	}
	for _, finding := range findings {
		if _, ok := want[finding.Category]; ok {
			want[finding.Category] = true
			if finding.ID == "" || finding.EvidencePath == "" || finding.Detector == "" || finding.DetectorVersion == "" {
				t.Fatalf("incomplete finding = %+v", finding)
			}
			if finding.NodeID == "" {
				t.Fatalf("finding missing node id = %+v", finding)
			}
		}
	}
	for category, found := range want {
		if !found {
			t.Fatalf("missing finding category %q in %+v", category, findings)
		}
	}
}
