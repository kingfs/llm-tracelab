package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/websearch"
)

type fakeChatClient struct {
	reqs  []ChatCompletionRequest
	req   ChatCompletionRequest
	resps []ChatCompletionResponse
	resp  ChatCompletionResponse
	errs  []error
	err   error
}

func (f *fakeChatClient) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	f.req = req
	f.reqs = append(f.reqs, req)
	index := len(f.reqs) - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return ChatCompletionResponse{}, f.errs[index]
	}
	if f.err != nil {
		return ChatCompletionResponse{}, f.err
	}
	if index < len(f.resps) {
		return f.resps[index], nil
	}
	return f.resp, nil
}

type fakeWebSearchProvider struct {
	queries []websearch.Query
	result  websearch.Result
	err     error
}

func (f *fakeWebSearchProvider) Search(ctx context.Context, query websearch.Query) (websearch.Result, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return websearch.Result{}, f.err
	}
	return f.result, nil
}

type fakeExecutionEventRecorder struct {
	events []audit.ExecutionEvent
}

func (f *fakeExecutionEventRecorder) RecordExecutionEvent(ctx context.Context, event audit.ExecutionEvent) error {
	f.events = append(f.events, event)
	return nil
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

func TestRuntimeCreateContinuesAfterClientSubmittedFunctionOutput(t *testing.T) {
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message: ChatMessage{
						ToolCalls: []ChatToolCall{{
							ID:   "call_lookup",
							Type: "function",
							Function: ChatToolCallFunction{
								Name:      "lookup",
								Arguments: `{"q":"codex"}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: ChatUsage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Content: "The lookup result is ready."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 15, CompletionTokens: 6, TotalTokens: 21},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	store := NewMemoryStore()
	rt := New(Config{DefaultModel: "gpt-test"}, client, store, WithExecutionEventRecorder(events))

	first, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "lookup codex",
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	if len(first.Output) != 1 || first.Output[0].Type != "function_call" || first.Output[0].CallID != "call_lookup" {
		t.Fatalf("first output = %#v, want function_call call_lookup", first.Output)
	}

	second, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		PreviousResponseID: first.ID,
		Input: []any{map[string]any{
			"type":    "function_call_output",
			"call_id": "call_lookup",
			"name":    "lookup",
			"output":  map[string]any{"ok": true, "value": "42"},
			"status":  "completed",
		}},
	})
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if len(second.Output) != 1 || second.Output[0].Content[0].Text != "The lookup result is ready." {
		t.Fatalf("second output = %#v, want final message", second.Output)
	}

	if len(client.reqs) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(client.reqs))
	}
	secondMessages := client.reqs[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second messages len = %d, want 3: %#v", len(secondMessages), secondMessages)
	}
	if secondMessages[0].Role != "user" || secondMessages[0].Content != "lookup codex" {
		t.Fatalf("history user message mismatch: %#v", secondMessages[0])
	}
	if secondMessages[1].Role != "assistant" || len(secondMessages[1].ToolCalls) != 1 || secondMessages[1].ToolCalls[0].ID != "call_lookup" {
		t.Fatalf("history assistant function call mismatch: %#v", secondMessages[1])
	}
	if secondMessages[2].Role != "tool" || secondMessages[2].ToolCallID != "call_lookup" || secondMessages[2].Content != `{"ok":true,"value":"42"}` {
		t.Fatalf("submitted tool output message mismatch: %#v", secondMessages[2])
	}

	inputs, ok, err := store.InputItems(context.Background(), second.ID)
	if err != nil || !ok {
		t.Fatalf("InputItems(second) ok=%v err=%v", ok, err)
	}
	if len(inputs) != 1 || inputs[0].Extra["status"] != "completed" {
		t.Fatalf("stored function output input lost extra fields: %#v", inputs)
	}
	if len(events.events) != 2 {
		t.Fatalf("execution events len = %d, want requested and submitted: %#v", len(events.events), events.events)
	}
	if events.events[0].Status != "requested" || events.events[0].DetailsJSON["tool_name"] != "lookup" || events.events[0].DetailsJSON["call_id"] != "call_lookup" {
		t.Fatalf("requested event mismatch: %#v", events.events[0])
	}
	if events.events[1].Status != "submitted" || events.events[1].DetailsJSON["tool_name"] != "lookup" || events.events[1].DetailsJSON["call_id"] != "call_lookup" {
		t.Fatalf("submitted event mismatch: %#v", events.events[1])
	}
}

func TestResponseToolsToChatToolsMapsHostedWebSearchWhenReady(t *testing.T) {
	tools := responseToolsToChatTools([]protocol.Tool{
		{Type: "web_search_preview"},
		{
			Type:        "function",
			Name:        "lookup",
			Description: "lookup docs",
			Parameters:  map[string]any{"type": "object"},
		},
	}, true)

	if len(tools) != 2 {
		t.Fatalf("tools len = %d, want 2: %#v", len(tools), tools)
	}
	if got := tools[0]; got.Type != "function" || got.Function.Name != "web_search" {
		t.Fatalf("unexpected web_search mapping: %#v", got)
	}
	props, ok := tools[0].Function.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("web_search parameters missing properties: %#v", tools[0].Function.Parameters)
	}
	if _, ok := props["query"].(map[string]any); !ok {
		t.Fatalf("web_search query schema missing: %#v", props)
	}
	if got := tools[1]; got.Type != "function" || got.Function.Name != "lookup" || got.Function.Description != "lookup docs" {
		t.Fatalf("function tool mapping changed: %#v", got)
	}

	disabled := responseToolsToChatTools([]protocol.Tool{{Type: "web_search"}}, false)
	if len(disabled) != 0 {
		t.Fatalf("disabled web_search mapped to chat tools: %#v", disabled)
	}
}

func TestRuntimeCreateExecutesHostedWebSearchToolLoop(t *testing.T) {
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message: ChatMessage{
						ToolCalls: []ChatToolCall{{
							ID:   "call_search",
							Type: "function",
							Function: ChatToolCallFunction{
								Name:      "web_search",
								Arguments: `{"query":"llm trace replay"}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Content: "Use cassettes for deterministic replay."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 20, CompletionTokens: 7, TotalTokens: 27},
			},
		},
	}
	provider := &fakeWebSearchProvider{
		result: websearch.Result{Results: []websearch.SearchResult{{
			Title:   "TraceLab docs",
			URL:     "https://example.test/docs",
			Snippet: "Record and replay LLM API traffic.",
		}}},
	}
	events := &fakeExecutionEventRecorder{}
	store := NewMemoryStore()
	rt := New(Config{
		DefaultModel:        "gpt-test",
		WebSearchEnabled:    true,
		WebSearchMaxResults: 2,
	}, client, store, WithWebSearchProvider(provider), WithExecutionEventRecorder(events))

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input:      "search it",
		ToolChoice: "web_search",
		Tools:      []protocol.Tool{{Type: "web_search_preview"}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if len(client.reqs) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(client.reqs))
	}
	if len(provider.queries) != 1 {
		t.Fatalf("provider queries = %d, want 1", len(provider.queries))
	}
	if provider.queries[0].Text != "llm trace replay" || provider.queries[0].MaxResults != 2 {
		t.Fatalf("provider query mismatch: %#v", provider.queries[0])
	}
	firstTools := client.reqs[0].Tools
	if len(firstTools) != 1 || firstTools[0].Function.Name != "web_search" {
		t.Fatalf("first chat tools = %#v, want web_search", firstTools)
	}
	firstChoice, ok := client.reqs[0].ToolChoice.(map[string]any)
	if !ok || firstChoice["type"] != "function" {
		t.Fatalf("first chat tool_choice = %#v, want forced function web_search", client.reqs[0].ToolChoice)
	}
	firstChoiceFunction, ok := firstChoice["function"].(map[string]any)
	if !ok || firstChoiceFunction["name"] != "web_search" {
		t.Fatalf("first chat tool_choice function = %#v, want web_search", firstChoice["function"])
	}
	secondMessages := client.reqs[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second messages len = %d, want 3: %#v", len(secondMessages), secondMessages)
	}
	if secondMessages[1].Role != "assistant" || len(secondMessages[1].ToolCalls) != 1 || secondMessages[1].ToolCalls[0].Function.Name != "web_search" {
		t.Fatalf("second assistant tool call message mismatch: %#v", secondMessages[1])
	}
	if secondMessages[2].Role != "tool" || secondMessages[2].ToolCallID != "call_search" {
		t.Fatalf("second tool message metadata mismatch: %#v", secondMessages[2])
	}
	toolContent, _ := secondMessages[2].Content.(string)
	if !strings.Contains(toolContent, "TraceLab docs") || !strings.Contains(toolContent, "llm trace replay") {
		t.Fatalf("second tool message content missing result: %q", toolContent)
	}

	if resp.Usage != (protocol.Usage{InputTokens: 30, OutputTokens: 10, TotalTokens: 40}) {
		t.Fatalf("usage mismatch: %#v", resp.Usage)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("output len = %d, want 2: %#v", len(resp.Output), resp.Output)
	}
	if got := resp.Output[0]; got.Type != "web_search_call" || got.Status != "completed" || got.CallID != "call_search" {
		t.Fatalf("unexpected web_search output: %#v", got)
	}
	if got := resp.Output[0].Action["query"]; got != "llm trace replay" {
		t.Fatalf("web_search action query = %#v", got)
	}
	if len(events.events) != 2 {
		t.Fatalf("execution events len = %d, want 2: %#v", len(events.events), events.events)
	}
	if events.events[0].EventType != "response.tool_call" || events.events[0].Phase != "tool_call" || events.events[0].Status != "started" {
		t.Fatalf("started event mismatch: %#v", events.events[0])
	}
	if events.events[1].EventType != "response.tool_call" || events.events[1].Phase != "tool_call" || events.events[1].Status != "completed" {
		t.Fatalf("completed event mismatch: %#v", events.events[1])
	}
	if events.events[1].DetailsJSON["call_id"] != "call_search" || events.events[1].DetailsJSON["query"] != "llm trace replay" || events.events[1].DetailsJSON["result_count"] != 1 {
		t.Fatalf("completed event details mismatch: %#v", events.events[1].DetailsJSON)
	}
	if got := resp.Output[1]; got.Type != "message" || got.Content[0].Text != "Use cassettes for deterministic replay." {
		t.Fatalf("unexpected final message: %#v", got)
	}

	stored, ok, err := store.Get(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(stored.Output, resp.Output) {
		t.Fatalf("stored output mismatch\nwant: %#v\n got: %#v", resp.Output, stored.Output)
	}
}

func TestRuntimeCreateWebSearchMaxToolIterationsGuard(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{
					ToolCalls: []ChatToolCall{{
						ID:   "call_search",
						Type: "function",
						Function: ChatToolCallFunction{
							Name:      "web_search",
							Arguments: `{"query":"again"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
		},
	}
	provider := &fakeWebSearchProvider{}
	rt := New(Config{
		DefaultModel:      "gpt-test",
		MaxToolIterations: 1,
		WebSearchEnabled:  true,
	}, client, NewMemoryStore(), WithWebSearchProvider(provider))

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "loop",
		Tools: []protocol.Tool{{Type: "web_search"}},
	})
	if err == nil {
		t.Fatal("Create returned nil error, want max iteration error")
	}
	var maxErr MaxToolIterationsError
	if !errors.As(err, &maxErr) || maxErr.Max != 1 {
		t.Fatalf("error = %v, want MaxToolIterationsError{Max:1}", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(client.reqs))
	}
	if len(provider.queries) != 1 {
		t.Fatalf("provider queries = %d, want 1", len(provider.queries))
	}
}

func TestRuntimeCreateRecordsHostedWebSearchToolFailure(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{
					ToolCalls: []ChatToolCall{{
						ID:   "call_search",
						Type: "function",
						Function: ChatToolCallFunction{
							Name:      "web_search",
							Arguments: `{"query":"llm trace replay"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
		},
	}
	providerErr := errors.New("search backend failed")
	provider := &fakeWebSearchProvider{err: providerErr}
	events := &fakeExecutionEventRecorder{}
	rt := New(Config{
		DefaultModel:     "gpt-test",
		WebSearchEnabled: true,
	}, client, NewMemoryStore(), WithWebSearchProvider(provider), WithExecutionEventRecorder(events))

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "search it",
		Tools: []protocol.Tool{{Type: "web_search"}},
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("Create error = %v, want provider error", err)
	}
	if len(events.events) != 2 {
		t.Fatalf("execution events len = %d, want 2: %#v", len(events.events), events.events)
	}
	if events.events[0].Status != "started" || events.events[1].Status != "failed" || events.events[1].Message != "search backend failed" {
		t.Fatalf("execution events mismatch: %#v", events.events)
	}
	if events.events[1].DetailsJSON["tool_name"] != "web_search" || events.events[1].DetailsJSON["call_id"] != "call_search" || events.events[1].DetailsJSON["error"] != "search backend failed" {
		t.Fatalf("failed event details mismatch: %#v", events.events[1].DetailsJSON)
	}
}
