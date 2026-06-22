package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

type fakeChatClient struct {
	req  ChatCompletionRequest
	resp ChatCompletionResponse
	err  error
}

func (f *fakeChatClient) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	f.req = req
	return f.resp, f.err
}

func TestRuntimeCreateStringInputCallsChatClientAndStoresResponse(t *testing.T) {
	temp := 0.25
	topP := 0.9
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{
					Content: "done",
					ToolCalls: []ChatToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: ChatToolCallFunction{
							Name:      "lookup",
							Arguments: `{"q":"codex"}`,
						},
					}},
				},
			}},
			Usage: ChatUsage{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		},
	}
	store := NewMemoryStore()
	rt := New(Config{DefaultModel: "fallback-model"}, client, store)

	req := protocol.CreateResponseRequest{
		Input:        "hello",
		Instructions: "be terse",
		Tools: []protocol.Tool{{
			Type:        "function",
			Name:        "lookup",
			Description: "lookup docs",
			Parameters:  map[string]any{"type": "object"},
		}},
		ToolChoice:      map[string]any{"type": "auto"},
		MaxOutputTokens: 123,
		Temperature:     &temp,
		TopP:            &topP,
		Metadata:        map[string]any{"trace_id": "trace-1"},
	}

	resp, err := rt.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	wantMessages := []ChatMessage{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hello"},
	}
	if !reflect.DeepEqual(client.req.Messages, wantMessages) {
		t.Fatalf("messages mismatch\nwant: %#v\n got: %#v", wantMessages, client.req.Messages)
	}
	if client.req.Model != "fallback-model" {
		t.Fatalf("model = %q, want fallback-model", client.req.Model)
	}
	if client.req.MaxTokens != 123 || client.req.Temperature != &temp || client.req.TopP != &topP {
		t.Fatalf("sampling fields not forwarded: %#v", client.req)
	}
	if len(client.req.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(client.req.Tools))
	}
	if got := client.req.Tools[0]; got.Type != "function" || got.Function.Name != "lookup" || got.Function.Description != "lookup docs" {
		t.Fatalf("unexpected tool mapping: %#v", got)
	}
	if !reflect.DeepEqual(client.req.ToolChoice, req.ToolChoice) {
		t.Fatalf("tool_choice mismatch\nwant: %#v\n got: %#v", req.ToolChoice, client.req.ToolChoice)
	}

	if resp.Model != "fallback-model" {
		t.Fatalf("response model = %q, want fallback-model", resp.Model)
	}
	if resp.PreviousResponseID != "" {
		t.Fatalf("previous_response_id = %q, want empty", resp.PreviousResponseID)
	}
	if resp.Usage != (protocol.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18}) {
		t.Fatalf("usage mismatch: %#v", resp.Usage)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("output len = %d, want 2: %#v", len(resp.Output), resp.Output)
	}
	if got := resp.Output[0]; got.Type != "function_call" || got.CallID != "call_1" || got.Name != "lookup" || got.Arguments != `{"q":"codex"}` {
		t.Fatalf("unexpected function_call output: %#v", got)
	}
	if got := resp.Output[1]; got.Type != "message" || got.Role != "assistant" || len(got.Content) != 1 || got.Content[0].Text != "done" {
		t.Fatalf("unexpected message output: %#v", got)
	}

	stored, ok, err := store.Get(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(stored.Output, resp.Output) {
		t.Fatalf("stored output mismatch\nwant: %#v\n got: %#v", resp.Output, stored.Output)
	}
	inputs, ok, err := store.InputItems(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored inputs lookup ok=%v err=%v", ok, err)
	}
	if len(inputs) != 1 || inputs[0].Type != "message" || inputs[0].Role != "user" || inputs[0].Content[0].Text != "hello" {
		t.Fatalf("stored input items mismatch: %#v", inputs)
	}
}
