package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

func codexFixturePath(name string) string {
	return filepath.Join("..", "..", "..", "tests", "fixtures", "codex", name)
}

func decodeCodexFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile(codexFixturePath(name))
	if err != nil {
		t.Fatalf("read codex fixture %s: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode codex fixture %s: %v", name, err)
	}
	return out
}

func TestCodexTextCreateFixtureAlignsWithRuntimeChatRequest(t *testing.T) {
	req := decodeCodexFixture[protocol.CreateResponseRequest](t, "text_create_request.json")
	client := &fakeChatClient{resp: finalChatResponse("Hello from the offline fixture.")}
	store := NewMemoryStore()
	rt := New(Config{DefaultModel: "fallback-model"}, client, store)

	resp, err := rt.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	wantMessages := []ChatMessage{
		{Role: "system", Content: "You are a concise coding assistant."},
		{Role: "user", Content: "Say hello in one short sentence."},
	}
	if !reflect.DeepEqual(client.req.Messages, wantMessages) {
		t.Fatalf("chat messages mismatch\nwant: %#v\n got: %#v", wantMessages, client.req.Messages)
	}
	if client.req.Model != "local-test-model" {
		t.Fatalf("chat model = %q, want local-test-model", client.req.Model)
	}
	if resp.Model != "local-test-model" || resp.Status != "completed" {
		t.Fatalf("response mismatch: %#v", resp)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "message" || resp.Output[0].Content[0].Text != "Hello from the offline fixture." {
		t.Fatalf("response output mismatch: %#v", resp.Output)
	}
	if _, ok, err := store.Get(context.Background(), resp.ID); err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
}

func TestCodexFunctionCallContinuationFixtureAlignsWithRuntimeChatRequest(t *testing.T) {
	initialReq := decodeCodexFixture[protocol.CreateResponseRequest](t, "function_call_request.json")
	initialResp := decodeCodexFixture[protocol.Response](t, "function_call_expected_response.json")
	continuationReq := decodeCodexFixture[protocol.CreateResponseRequest](t, "function_call_output_continuation_request.json")
	store := NewMemoryStore()
	if err := store.Put(context.Background(), initialResp, initialReq, requestInputItems(initialReq), initialResp.Output); err != nil {
		t.Fatalf("seed response store: %v", err)
	}

	client := &fakeChatClient{resp: finalChatResponse("Continuation complete.")}
	rt := New(Config{DefaultModel: "fallback-model"}, client, store)

	resp, err := rt.Create(context.Background(), continuationReq)
	if err != nil {
		t.Fatalf("Create() continuation error = %v", err)
	}

	if len(client.req.Messages) != 3 {
		t.Fatalf("chat messages len = %d, want 3: %#v", len(client.req.Messages), client.req.Messages)
	}
	if got := client.req.Messages[0]; got.Role != "user" || got.Content != "Read package metadata." {
		t.Fatalf("history user message mismatch: %#v", got)
	}
	assistant := client.req.Messages[1]
	if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("history assistant tool call mismatch: %#v", assistant)
	}
	call := assistant.ToolCalls[0]
	if call.ID != "call_read_file_1" || call.Function.Name != "read_file" || call.Function.Arguments != `{"path":"go.mod"}` {
		t.Fatalf("history tool call mismatch: %#v", call)
	}
	tool := client.req.Messages[2]
	if tool.Role != "tool" || tool.ToolCallID != "call_read_file_1" || tool.Content != `{"content":"module github.com/kingfs/llm-tracelab"}` {
		t.Fatalf("continuation tool message mismatch: %#v", tool)
	}
	if resp.PreviousResponseID != "resp_fixture_tool_call" {
		t.Fatalf("response previous_response_id = %q, want resp_fixture_tool_call", resp.PreviousResponseID)
	}
}

func TestCodexOrdinaryWebSearchDescriptorFixtureDoesNotReachChatToolsWithoutProvider(t *testing.T) {
	req := decodeCodexFixture[protocol.CreateResponseRequest](t, "ordinary_web_search_descriptor_request.json")
	client := &fakeChatClient{resp: finalChatResponse("Local answer.")}
	rt := New(Config{DefaultModel: "fallback-model", WebSearchEnabled: false}, client, NewMemoryStore())

	_, err := rt.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(client.reqs))
	}
	if !client.req.Stream {
		t.Fatalf("chat stream = false, want true from fixture request")
	}
	if len(client.req.Tools) != 0 {
		t.Fatalf("chat tools = %#v, want ordinary web_search descriptor withheld without provider", client.req.Tools)
	}
	if client.req.ToolChoice != nil {
		t.Fatalf("chat tool_choice = %#v, want nil when web_search is withheld", client.req.ToolChoice)
	}
}
