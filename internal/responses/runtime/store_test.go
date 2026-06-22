package runtime

import (
	"context"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	tracestore "github.com/kingfs/llm-tracelab/internal/store"
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

func TestEntStorePutGetInputItemsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newEntStoreForTest(t)
	resp := protocol.Response{
		ID:        "resp_ent_1",
		Object:    "response",
		CreatedAt: 11,
		Status:    "completed",
		Model:     "model",
		Output: []protocol.OutputItem{{
			ID:      "out_ent_1",
			Type:    "message",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "pong"}},
		}},
		Usage: protocol.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
		Metadata: map[string]any{
			"codex": map[string]any{"thread_id": "thread_1"},
		},
	}
	inputs := []protocol.InputItem{messageInput("in_ent_1", "ping")}

	if err := store.Put(ctx, resp, protocol.CreateResponseRequest{Input: "ping"}, inputs, resp.Output); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, ok, err := store.Get(ctx, resp.ID)
	if err != nil || !ok {
		t.Fatalf("Get() ok=%v err=%v", ok, err)
	}
	if got.ID != resp.ID || got.Model != resp.Model || got.Status != resp.Status {
		t.Fatalf("Get() = %#v, want id/model/status from original", got)
	}
	if got.Usage != resp.Usage {
		t.Fatalf("Usage = %#v, want %#v", got.Usage, resp.Usage)
	}
	if len(got.Output) != 1 || got.Output[0].Content[0].Text != "pong" {
		t.Fatalf("Output = %#v, want pong", got.Output)
	}

	gotInputs, ok, err := store.InputItems(ctx, resp.ID)
	if err != nil || !ok {
		t.Fatalf("InputItems() ok=%v err=%v", ok, err)
	}
	if len(gotInputs) != 1 || gotInputs[0].Content[0].Text != "ping" {
		t.Fatalf("InputItems() = %#v, want ping", gotInputs)
	}
}

func TestEntStoreFunctionToolItemsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newEntStoreForTest(t)
	resp := protocol.Response{
		ID:        "resp_ent_tool_1",
		Object:    "response",
		CreatedAt: 12,
		Status:    "completed",
		Model:     "model",
		Output: []protocol.OutputItem{{
			ID:        "fc_call_lookup",
			Type:      "function_call",
			Status:    "completed",
			CallID:    "call_lookup",
			Name:      "lookup",
			Arguments: `{"q":"codex"}`,
			Extra:     map[string]any{"provider_item_id": "item_1"},
		}},
	}
	inputs := []protocol.InputItem{{
		ID:     "in_tool_result_1",
		Type:   "function_call_output",
		CallID: "call_lookup",
		Name:   "lookup",
		Output: map[string]any{"ok": true, "value": "42"},
		Extra:  map[string]any{"status": "completed"},
	}}

	if err := store.Put(ctx, resp, protocol.CreateResponseRequest{Input: "tool"}, inputs, resp.Output); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, ok, err := store.Get(ctx, resp.ID)
	if err != nil || !ok {
		t.Fatalf("Get() ok=%v err=%v", ok, err)
	}
	if len(got.Output) != 1 || got.Output[0].Extra["provider_item_id"] != "item_1" {
		t.Fatalf("output item extra round-trip mismatch: %#v", got.Output)
	}
	items, ok, err := store.ContinuationItems(ctx, resp.ID)
	if err != nil || !ok {
		t.Fatalf("ContinuationItems() ok=%v err=%v", ok, err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want input and output: %#v", len(items), items)
	}
	input := items[0].Input
	if input == nil || input.Type != "function_call_output" || input.Extra["status"] != "completed" {
		t.Fatalf("function output input round-trip mismatch: %#v", items[0])
	}
	outputMap, ok := input.Output.(map[string]any)
	if !ok || outputMap["ok"] != true || outputMap["value"] != "42" {
		t.Fatalf("function output payload = %#v, want object payload", input.Output)
	}
}

func TestEntStoreContinuationItemsWalksPreviousResponses(t *testing.T) {
	ctx := context.Background()
	store := newEntStoreForTest(t)
	first := protocol.Response{
		ID:        "resp_ent_1",
		Object:    "response",
		Status:    "completed",
		Model:     "model",
		CreatedAt: 1,
		Output: []protocol.OutputItem{{
			ID:      "out_ent_1",
			Type:    "message",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "one"}},
		}},
	}
	second := protocol.Response{
		ID:                 "resp_ent_2",
		Object:             "response",
		Status:             "completed",
		Model:              "model",
		CreatedAt:          2,
		PreviousResponseID: "resp_ent_1",
		Output: []protocol.OutputItem{{
			ID:      "out_ent_2",
			Type:    "message",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "two"}},
		}},
	}

	if err := store.Put(ctx, first, protocol.CreateResponseRequest{Input: "first"}, []protocol.InputItem{messageInput("in_ent_1", "first")}, first.Output); err != nil {
		t.Fatalf("put first: %v", err)
	}
	if err := store.Put(ctx, second, protocol.CreateResponseRequest{Input: "second", PreviousResponseID: "resp_ent_1"}, []protocol.InputItem{messageInput("in_ent_2", "second")}, second.Output); err != nil {
		t.Fatalf("put second: %v", err)
	}

	items, ok, err := store.ContinuationItems(ctx, "resp_ent_2")
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

func TestEntStoreContinuationItemsStopsAtCompactBoundary(t *testing.T) {
	ctx := context.Background()
	store := newEntStoreForTest(t)
	first := protocol.Response{ID: "resp_ent_1", Object: "response", Status: "completed", Model: "model", CreatedAt: 1}
	second := protocol.Response{ID: "resp_ent_2", Object: "response", Status: "completed", Model: "model", CreatedAt: 2, PreviousResponseID: "resp_ent_1"}
	third := protocol.Response{ID: "resp_ent_3", Object: "response", Status: "completed", Model: "model", CreatedAt: 3, PreviousResponseID: "resp_ent_2"}

	if err := store.Put(ctx, first, protocol.CreateResponseRequest{Input: "first"}, []protocol.InputItem{messageInput("in_ent_1", "first")}, nil); err != nil {
		t.Fatalf("put first: %v", err)
	}
	if err := store.Put(ctx, second, protocol.CreateResponseRequest{Input: "compact"}, []protocol.InputItem{{ID: "in_ent_2", Type: "compact_request"}}, nil); err != nil {
		t.Fatalf("put second: %v", err)
	}
	if err := store.Put(ctx, third, protocol.CreateResponseRequest{Input: "third"}, []protocol.InputItem{messageInput("in_ent_3", "third")}, nil); err != nil {
		t.Fatalf("put third: %v", err)
	}

	items, ok, err := store.ContinuationItems(ctx, "resp_ent_3")
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

func TestEntStoreLatestResponseIDByConversation(t *testing.T) {
	ctx := context.Background()
	store := newEntStoreForTest(t)
	first := protocol.Response{
		ID:        "resp_thread_1",
		Object:    "response",
		Status:    "completed",
		Model:     "model",
		CreatedAt: 1,
		Metadata:  map[string]any{"codex": map[string]any{"thread_id": "thread_1"}},
	}
	second := protocol.Response{
		ID:        "resp_thread_2",
		Object:    "response",
		Status:    "completed",
		Model:     "model",
		CreatedAt: 2,
		Metadata:  map[string]any{"codex": map[string]any{"thread_id": "thread_1"}},
	}
	other := protocol.Response{
		ID:        "resp_thread_other",
		Object:    "response",
		Status:    "completed",
		Model:     "model",
		CreatedAt: 3,
		Metadata:  map[string]any{"codex": map[string]any{"thread_id": "thread_2"}},
	}

	for _, resp := range []protocol.Response{first, second, other} {
		if err := store.Put(ctx, resp, protocol.CreateResponseRequest{Input: "x"}, []protocol.InputItem{messageInput("in_"+resp.ID, resp.ID)}, nil); err != nil {
			t.Fatalf("put %s: %v", resp.ID, err)
		}
	}

	got, ok, err := store.LatestResponseIDByConversation(ctx, "thread_1")
	if err != nil || !ok {
		t.Fatalf("LatestResponseIDByConversation ok=%v err=%v", ok, err)
	}
	if got != "resp_thread_2" {
		t.Fatalf("latest response = %q, want resp_thread_2", got)
	}
}

func newEntStoreForTest(t *testing.T) *EntStore {
	t.Helper()
	st, err := tracestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("store.Close() error = %v", err)
		}
	})
	return NewEntStore(st.EntClient())
}

func messageInput(id string, text string) protocol.InputItem {
	return protocol.InputItem{
		ID:      id,
		Type:    "message",
		Role:    "user",
		Content: []protocol.ContentPart{{Type: "input_text", Text: text}},
	}
}
