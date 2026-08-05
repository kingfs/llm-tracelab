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

func TestAnalyzeLegacyObservationUsesModelScope(t *testing.T) {
	toolCall := observe.SemanticNode{
		ID:             "node-shell",
		ProviderType:   "local_shell_call",
		NormalizedType: observe.NodeToolCall,
		Path:           "$.output[0]",
		Metadata:       map[string]any{"arguments": `{"cmd":"rm -rf /"}`},
	}
	obs := observe.TraceObservation{
		TraceID: "trace-legacy-model",
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{toolCall},
		},
	}

	findings, err := NewRunner().Analyze(context.Background(), obs)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	finding := requireFindingCategory(t, findings, "filesystem_destructive_operation")
	if got := metadataStringValue(finding.Metadata, "exchange_kind"); got != "model" {
		t.Fatalf("exchange_kind metadata = %q, want model", got)
	}
	if got := metadataStringValue(finding.Metadata, "exchange_role"); got != "legacy_model" {
		t.Fatalf("exchange_role metadata = %q, want legacy_model", got)
	}
}

func TestAnalyzeEntryObservationScopesFindingsAndSkipsModelToolDetectors(t *testing.T) {
	toolCall := observe.SemanticNode{
		ID:             "node-entry-shell",
		ProviderType:   "local_shell_call",
		NormalizedType: observe.NodeToolCall,
		Path:           "$.response.output[0]",
		Metadata:       map[string]any{"arguments": `{"cmd":"rm -rf /"}`},
	}
	secret := observe.SemanticNode{
		ID:             "node-entry-secret",
		ProviderType:   "input_text",
		NormalizedType: observe.NodeText,
		Path:           "$.request.input",
		Text:           "bearer entry_secret_token_abcdefghijklmnopqrstuvwxyz",
	}
	obs := observe.TraceObservation{
		TraceID:      "trace-entry",
		ExchangeKind: "entry",
		ExchangeRole: "client_request",
		Request: observe.ObservationRequest{
			Nodes: []observe.SemanticNode{secret},
		},
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{toolCall},
		},
	}

	findings, err := NewRunner().Analyze(context.Background(), obs)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if hasFindingCategory(findings, "filesystem_destructive_operation") {
		t.Fatalf("entry observation produced model tool finding: %+v", findings)
	}
	finding := requireFindingCategory(t, findings, "credential_leak")
	if got := metadataStringValue(finding.Metadata, "exchange_kind"); got != "entry" {
		t.Fatalf("exchange_kind metadata = %q, want entry", got)
	}
	if got := metadataStringValue(finding.Metadata, "exchange_role"); got != "client_request" {
		t.Fatalf("exchange_role metadata = %q, want client_request", got)
	}
	scope, ok := finding.Metadata["exchange_scope"].(map[string]any)
	if !ok {
		t.Fatalf("exchange_scope metadata missing or wrong type: %+v", finding.Metadata)
	}
	if got := metadataStringValue(scope, "kind"); got != "entry" {
		t.Fatalf("exchange_scope.kind = %q, want entry", got)
	}
}

func requireFindingCategory(t *testing.T, findings []observe.Finding, category string) observe.Finding {
	t.Helper()
	for _, finding := range findings {
		if finding.Category == category {
			return finding
		}
	}
	t.Fatalf("missing finding category %q in %+v", category, findings)
	return observe.Finding{}
}

func hasFindingCategory(findings []observe.Finding, category string) bool {
	for _, finding := range findings {
		if finding.Category == category {
			return true
		}
	}
	return false
}

func metadataStringValue(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}
