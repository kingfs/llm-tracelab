package runtime

import (
	"context"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

func TestMemoryStoreContinuationItemsWalksPreviousResponses(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	first := protocol.Response{
		ID:        "resp_1",
		Object:    "response",
		Status:    "completed",
		Model:     "model",
		CreatedAt: 1,
		Output: []protocol.OutputItem{{
			ID:      "out_1",
			Type:    "message",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "one"}},
		}},
	}
	second := protocol.Response{
		ID:                 "resp_2",
		Object:             "response",
		Status:             "completed",
		Model:              "model",
		CreatedAt:          2,
		PreviousResponseID: "resp_1",
		Output: []protocol.OutputItem{{
			ID:      "out_2",
			Type:    "message",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "two"}},
		}},
	}

	if err := store.Put(ctx, first, protocol.CreateResponseRequest{Input: "first"}, []protocol.InputItem{messageInput("in_1", "first")}, first.Output); err != nil {
		t.Fatalf("put first: %v", err)
	}
	if err := store.Put(ctx, second, protocol.CreateResponseRequest{Input: "second", PreviousResponseID: "resp_1"}, []protocol.InputItem{messageInput("in_2", "second")}, second.Output); err != nil {
		t.Fatalf("put second: %v", err)
	}

	items, ok, err := store.ContinuationItems(ctx, "resp_2")
	if err != nil || !ok {
		t.Fatalf("ContinuationItems ok=%v err=%v", ok, err)
	}
	if len(items) != 4 {
		t.Fatalf("len(items) = %d, want 4: %#v", len(items), items)
	}
	if got := items[0].Input.Content[0].Text; got != "first" {
		t.Fatalf("items[0] = %q, want first", got)
	}
	if got := items[1].Output.Content[0].Text; got != "one" {
		t.Fatalf("items[1] = %q, want one", got)
	}
	if got := items[2].Input.Content[0].Text; got != "second" {
		t.Fatalf("items[2] = %q, want second", got)
	}
	if got := items[3].Output.Content[0].Text; got != "two" {
		t.Fatalf("items[3] = %q, want two", got)
	}
}

func TestMemoryStoreContinuationItemsStopsAtCompactBoundary(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	first := protocol.Response{ID: "resp_1", Object: "response", Status: "completed", Model: "model"}
	second := protocol.Response{ID: "resp_2", Object: "response", Status: "completed", Model: "model", PreviousResponseID: "resp_1"}
	third := protocol.Response{ID: "resp_3", Object: "response", Status: "completed", Model: "model", PreviousResponseID: "resp_2"}

	if err := store.Put(ctx, first, protocol.CreateResponseRequest{Input: "first"}, []protocol.InputItem{messageInput("in_1", "first")}, nil); err != nil {
		t.Fatalf("put first: %v", err)
	}
	if err := store.Put(ctx, second, protocol.CreateResponseRequest{Input: "compact"}, []protocol.InputItem{{ID: "in_2", Type: "compact_request"}}, nil); err != nil {
		t.Fatalf("put second: %v", err)
	}
	if err := store.Put(ctx, third, protocol.CreateResponseRequest{Input: "third"}, []protocol.InputItem{messageInput("in_3", "third")}, nil); err != nil {
		t.Fatalf("put third: %v", err)
	}

	items, ok, err := store.ContinuationItems(ctx, "resp_3")
	if err != nil || !ok {
		t.Fatalf("ContinuationItems ok=%v err=%v", ok, err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want compact boundary plus current: %#v", len(items), items)
	}
	if items[0].Input.Type != "compact_request" {
		t.Fatalf("first item type = %q, want compact_request", items[0].Input.Type)
	}
	if got := items[1].Input.Content[0].Text; got != "third" {
		t.Fatalf("second item = %q, want third", got)
	}
}

func messageInput(id string, text string) protocol.InputItem {
	return protocol.InputItem{
		ID:      id,
		Type:    "message",
		Role:    "user",
		Content: []protocol.ContentPart{{Type: "input_text", Text: text}},
	}
}
