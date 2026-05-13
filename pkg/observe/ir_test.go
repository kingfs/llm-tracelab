package observe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestTraceObservationJSONRoundTrip(t *testing.T) {
	nodeID := StableNodeID("response", "$.output[0]", "message", 0)
	obs := TraceObservation{
		TraceID:       "trace-1",
		Provider:      "openai_compatible",
		Operation:     "responses",
		Endpoint:      "/v1/responses",
		Model:         "gpt-5.1",
		Parser:        "openai-responses",
		ParserVersion: "0.1.0",
		Status:        ParseStatusParsed,
		Warnings: []ParseWarning{{
			Code:    "unknown_output_item",
			Message: "preserved unknown output item",
			Path:    "$.output[1]",
		}},
		Response: ObservationResponse{
			Outputs: []SemanticNode{{
				ID:             nodeID,
				ProviderType:   "message",
				NormalizedType: NodeMessage,
				Role:           "assistant",
				Path:           "$.output[0]",
				Raw:            json.RawMessage(`{"type":"message","role":"assistant"}`),
				Children: []SemanticNode{{
					ID:             StableNodeID("response", "$.output[0].content[0]", "output_text", 0),
					ProviderType:   "output_text",
					NormalizedType: NodeText,
					Path:           "$.output[0].content[0]",
					Text:           "hello",
				}},
			}},
		},
		Tools: ObservationTools{
			Calls: []ToolCallObservation{{
				ID:       "call-1",
				Name:     "shell",
				Kind:     "local_shell_call",
				Owner:    ToolOwnerModelRequested,
				ArgsText: `{"cmd":"ls"}`,
				NodeID:   nodeID,
				Path:     "$.output[0]",
			}},
		},
		Findings: []Finding{{
			ID:              "finding-1",
			Category:        "dangerous_command",
			Severity:        SeverityHigh,
			Confidence:      0.9,
			Title:           "Dangerous command",
			EvidencePath:    EvidencePath("trace-1", SemanticNode{ID: nodeID, Path: "$.output[0]"}),
			NodeID:          nodeID,
			Detector:        "dangerous-shell",
			DetectorVersion: "0.1.0",
		}},
		RawRefs: RawReferences{
			CassettePath:  "trace.http",
			RequestStart:  10,
			RequestEnd:    20,
			ResponseStart: 21,
			ResponseEnd:   40,
		},
	}

	payload, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got TraceObservation
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.TraceID != obs.TraceID {
		t.Fatalf("TraceID = %q, want %q", got.TraceID, obs.TraceID)
	}
	if got.Response.Outputs[0].Children[0].Text != "hello" {
		t.Fatalf("child text = %q", got.Response.Outputs[0].Children[0].Text)
	}
	if got.Tools.Calls[0].Owner != ToolOwnerModelRequested {
		t.Fatalf("tool owner = %q", got.Tools.Calls[0].Owner)
	}
	if got.Findings[0].Severity != SeverityHigh {
		t.Fatalf("finding severity = %q", got.Findings[0].Severity)
	}
}

func TestFlattenAndRebuildNodeTree(t *testing.T) {
	root := SemanticNode{
		ID:             "root",
		ProviderType:   "message",
		NormalizedType: NodeMessage,
		Path:           "$.messages[0]",
		Index:          0,
		Children: []SemanticNode{{
			ID:             "child",
			ProviderType:   "text",
			NormalizedType: NodeText,
			Path:           "$.messages[0].content",
			Index:          0,
			Text:           "hello",
		}},
	}

	flat := FlattenNodes([]SemanticNode{root})
	if len(flat) != 2 {
		t.Fatalf("len(flat) = %d, want 2", len(flat))
	}
	if flat[1].ParentID != "root" {
		t.Fatalf("child parent = %q, want root", flat[1].ParentID)
	}

	tree := RebuildNodeTree(flat)
	if len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("rebuilt tree = %+v", tree)
	}
	if tree[0].Children[0].Text != "hello" {
		t.Fatalf("rebuilt child text = %q", tree[0].Children[0].Text)
	}
}

func TestRegistrySelectsParser(t *testing.T) {
	parser := fakeParser{name: "fake", version: "0.1.0"}
	registry := NewRegistry(parser)
	input := ParseInput{
		TraceID: "trace-1",
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  "openai_compatible",
				Operation: "responses",
			},
		},
	}

	obs, err := registry.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Parser != parser.name {
		t.Fatalf("Parser = %q, want %q", obs.Parser, parser.name)
	}
}

type fakeParser struct {
	name    string
	version string
}

func (p fakeParser) Name() string    { return p.name }
func (p fakeParser) Version() string { return p.version }
func (p fakeParser) CanParse(input ParseInput) bool {
	return input.Header.Meta.Provider == "openai_compatible"
}
func (p fakeParser) Parse(ctx context.Context, input ParseInput) (TraceObservation, error) {
	return TraceObservation{
		TraceID:       input.TraceID,
		Provider:      input.Header.Meta.Provider,
		Operation:     input.Header.Meta.Operation,
		Parser:        p.name,
		ParserVersion: p.version,
		Status:        ParseStatusParsed,
	}, nil
}
