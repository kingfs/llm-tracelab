package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/websearch"
)

type fakeChatClient struct {
	reqs               []ChatCompletionRequest
	req                ChatCompletionRequest
	resps              []ChatCompletionResponse
	resp               ChatCompletionResponse
	errs               []error
	err                error
	streamReqs         []ChatCompletionRequest
	streamEvents       []ChatStreamEvent
	streamEventBatches [][]ChatStreamEvent
	streamResp         ChatCompletionResponse
	streamResps        []ChatCompletionResponse
	streamErr          error
}

type blockingFirstToolCallClient struct {
	mu      sync.Mutex
	reqs    []ChatCompletionRequest
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingFirstToolCallClient() *blockingFirstToolCallClient {
	return &blockingFirstToolCallClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (f *blockingFirstToolCallClient) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	index := len(f.reqs) - 1
	f.mu.Unlock()

	if index == 0 {
		f.once.Do(func() {
			close(f.started)
		})
		select {
		case <-f.release:
		case <-ctx.Done():
			return ChatCompletionResponse{}, ctx.Err()
		}
		return functionToolChatResponse("call_lookup", "lookup"), nil
	}
	return finalChatResponse("complete"), nil
}

func (f *blockingFirstToolCallClient) requests() []ChatCompletionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ChatCompletionRequest, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func functionToolChatResponse(callID, name string) ChatCompletionResponse {
	return ChatCompletionResponse{
		Choices: []ChatChoice{{
			Message: ChatMessage{
				ToolCalls: []ChatToolCall{{
					ID:   callID,
					Type: "function",
					Function: ChatToolCallFunction{
						Name:      name,
						Arguments: `{"q":"codex"}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: ChatUsage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
	}
}

func finalChatResponse(content string) ChatCompletionResponse {
	return ChatCompletionResponse{
		Choices: []ChatChoice{{
			Message:      ChatMessage{Content: content},
			FinishReason: "stop",
		}},
		Usage: ChatUsage{PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11},
	}
}

func functionToolCreateRequest(input string) protocol.CreateResponseRequest {
	return protocol.CreateResponseRequest{
		Input: input,
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	}
}

type fixedTokenEstimator struct {
	tokens int
	calls  int
}

func (f *fixedTokenEstimator) EstimateResponsePromptTokens(req protocol.CreateResponseRequest, history []LedgerItem, inputItems []protocol.InputItem, webSearchReady bool) int {
	f.calls++
	return f.tokens
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

func (f *fakeChatClient) ChatCompletionStream(ctx context.Context, req ChatCompletionRequest, handle ChatStreamCallback) (ChatCompletionResponse, error) {
	f.streamReqs = append(f.streamReqs, req)
	index := len(f.streamReqs) - 1
	if f.streamErr != nil {
		return ChatCompletionResponse{}, f.streamErr
	}
	events := f.streamEvents
	if index < len(f.streamEventBatches) {
		events = f.streamEventBatches[index]
	}
	for _, event := range events {
		if err := handle(event); err != nil {
			return ChatCompletionResponse{}, err
		}
	}
	if index < len(f.streamResps) {
		return f.streamResps[index], nil
	}
	return f.streamResp, nil
}

type fakeResponseStreamSink struct {
	created       []protocol.Response
	deltas        []ResponseTextDelta
	functionDelta []ResponseFunctionCallArgumentsDelta
	functionDone  []ResponseFunctionCallArgumentsDone
	outputAdded   []ResponseOutputItemAdded
	outputDone    []ResponseOutputItemDone
	completed     []protocol.Response
	events        []string
	err           error
}

func (f *fakeResponseStreamSink) ResponseCreated(resp protocol.Response) error {
	f.created = append(f.created, resp)
	f.events = append(f.events, "response.created")
	return f.err
}

func (f *fakeResponseStreamSink) OutputTextDelta(delta ResponseTextDelta) error {
	f.deltas = append(f.deltas, delta)
	f.events = append(f.events, "response.output_text.delta")
	return f.err
}

func (f *fakeResponseStreamSink) FunctionCallArgumentsDelta(delta ResponseFunctionCallArgumentsDelta) error {
	f.functionDelta = append(f.functionDelta, delta)
	f.events = append(f.events, "response.function_call_arguments.delta")
	return f.err
}

func (f *fakeResponseStreamSink) FunctionCallArgumentsDone(done ResponseFunctionCallArgumentsDone) error {
	f.functionDone = append(f.functionDone, done)
	f.events = append(f.events, "response.function_call_arguments.done")
	return f.err
}

func (f *fakeResponseStreamSink) OutputItemAdded(added ResponseOutputItemAdded) error {
	f.outputAdded = append(f.outputAdded, added)
	f.events = append(f.events, "response.output_item.added")
	return f.err
}

func (f *fakeResponseStreamSink) OutputItemDone(done ResponseOutputItemDone) error {
	f.outputDone = append(f.outputDone, done)
	f.events = append(f.events, "response.output_item.done")
	return f.err
}

func (f *fakeResponseStreamSink) ResponseCompleted(resp protocol.Response) error {
	f.completed = append(f.completed, resp)
	f.events = append(f.events, "response.completed")
	return f.err
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

type fakeToolCallAuditRecorder struct {
	entries            []audit.ToolCallAudit
	requestAuditIDSeen []string
}

func (f *fakeToolCallAuditRecorder) RecordToolCallAudit(ctx context.Context, entry audit.ToolCallAudit) (string, error) {
	if requestAuditID, ok := audit.RequestAuditIDFromContext(ctx); ok {
		f.requestAuditIDSeen = append(f.requestAuditIDSeen, requestAuditID)
	}
	f.entries = append(f.entries, entry)
	return "tcaud_test", nil
}

func toJSONForTest(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test value: %v", err)
	}
	return string(data)
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

func TestRuntimeCreateUsesProfileUpstreamModelForChatRequest(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "done"},
				FinishReason: "stop",
			}},
		},
	}
	rt := New(Config{
		DefaultModel: "public-model",
		ModelProfiles: []ModelProfile{{
			Name:          "public-model",
			UpstreamModel: "provider/private-model",
		}},
	}, client, NewMemoryStore())

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(client.reqs))
	}
	if client.reqs[0].Model != "provider/private-model" {
		t.Fatalf("chat request model = %q, want provider/private-model", client.reqs[0].Model)
	}
	if resp.Model != "public-model" {
		t.Fatalf("response model = %q, want public-model", resp.Model)
	}
}

func TestRuntimeCreateUsesProfileMaxOutputTokensWhenRequestOmitsLimit(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "done"},
				FinishReason: "stop",
			}},
		},
	}
	rt := New(Config{
		DefaultModel: "public-model",
		ModelProfiles: []ModelProfile{{
			Name: "public-model",
			Budget: ContextBudget{
				MaxOutputTokens: 321,
			},
		}},
	}, client, NewMemoryStore())

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(client.reqs))
	}
	if client.reqs[0].MaxTokens != 321 {
		t.Fatalf("chat request max tokens = %d, want profile max 321", client.reqs[0].MaxTokens)
	}
}

func TestRuntimeCreateRequestMaxOutputTokensOverridesProfileLimit(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "done"},
				FinishReason: "stop",
			}},
		},
	}
	rt := New(Config{
		DefaultModel: "public-model",
		ModelProfiles: []ModelProfile{{
			Name: "public-model",
			Budget: ContextBudget{
				MaxOutputTokens: 321,
			},
		}},
	}, client, NewMemoryStore())

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input:           "hello",
		MaxOutputTokens: 123,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(client.reqs))
	}
	if client.reqs[0].MaxTokens != 123 {
		t.Fatalf("chat request max tokens = %d, want request max 123", client.reqs[0].MaxTokens)
	}
}

func TestRuntimeCreateKeepsModelWhenProfileHasNoUpstreamModel(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "done"},
				FinishReason: "stop",
			}},
		},
	}
	rt := New(Config{
		DefaultModel: "fallback-model",
		ModelProfiles: []ModelProfile{{
			Pattern: "public-*",
		}},
	}, client, NewMemoryStore())

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Model: "public-model",
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(client.reqs))
	}
	if client.reqs[0].Model != "public-model" {
		t.Fatalf("chat request model = %q, want public-model", client.reqs[0].Model)
	}
	if resp.Model != "public-model" {
		t.Fatalf("response model = %q, want public-model", resp.Model)
	}
}

func TestRuntimeCreateStreamRequestsStreamingChatCompletion(t *testing.T) {
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "done"},
				FinishReason: "stop",
			}},
		},
	}
	rt := New(Config{DefaultModel: "fallback-model"}, client, NewMemoryStore())

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input:  "hello",
		Stream: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(client.reqs))
	}
	if !client.reqs[0].Stream {
		t.Fatalf("chat request stream = false, want true")
	}
}

func TestRuntimeCreateStreamEmitsDeltasAndStoresFinalResponse(t *testing.T) {
	client := &fakeChatClient{
		streamEvents: []ChatStreamEvent{
			{ChoiceIndex: 0, ContentDelta: "Hello "},
			{ChoiceIndex: 0, ContentDelta: "world"},
		},
		streamResp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "Hello world"},
				FinishReason: "stop",
			}},
			Usage: ChatUsage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
		},
	}
	store := NewMemoryStore()
	rt := New(Config{DefaultModel: "fallback-model"}, client, store)
	sink := &fakeResponseStreamSink{}

	resp, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "hello",
		Stream: true,
	}, sink)
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}

	if len(client.streamReqs) != 1 || !client.streamReqs[0].Stream {
		t.Fatalf("stream chat requests = %#v, want one streaming request", client.streamReqs)
	}
	if len(sink.created) != 1 || sink.created[0].ID != resp.ID || sink.created[0].Status != "in_progress" {
		t.Fatalf("created events = %#v, want in_progress response with final id %q", sink.created, resp.ID)
	}
	if len(sink.deltas) != 2 || sink.deltas[0].Delta != "Hello " || sink.deltas[1].Delta != "world" {
		t.Fatalf("deltas = %#v, want separate content chunks", sink.deltas)
	}
	if len(sink.completed) != 1 || sink.completed[0].ID != resp.ID {
		t.Fatalf("completed events = %#v, want final response id %q", sink.completed, resp.ID)
	}
	if resp.Usage != (protocol.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}) {
		t.Fatalf("usage = %#v, want chat usage", resp.Usage)
	}
	if len(resp.Output) != 1 || resp.Output[0].Content[0].Text != "Hello world" {
		t.Fatalf("final output = %#v, want aggregated text", resp.Output)
	}
	if sink.deltas[0].ItemID != resp.Output[0].ID {
		t.Fatalf("delta item_id = %q, want final message id %q", sink.deltas[0].ItemID, resp.Output[0].ID)
	}

	stored, ok, err := store.Get(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
	if len(stored.Output) != 1 || stored.Output[0].Content[0].Text != "Hello world" {
		t.Fatalf("stored output = %#v, want full response text", stored.Output)
	}
}

func TestRuntimeCreateStreamEmitsFunctionCallArgumentDeltas(t *testing.T) {
	client := &fakeChatClient{
		streamEvents: []ChatStreamEvent{
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:        0,
				ID:           "call_lookup",
				Type:         "function",
				FunctionName: "lookup",
			}}},
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ArgumentsDelta: `{"q"`,
			}}},
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ArgumentsDelta: `:"codex"}`,
			}}},
		},
		streamResp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{{
					ID:   "call_lookup",
					Type: "function",
					Function: ChatToolCallFunction{
						Name:      "lookup",
						Arguments: `{"q":"codex"}`,
					},
				}}},
				FinishReason: "tool_calls",
			}},
			Usage: ChatUsage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
		},
	}
	store := NewMemoryStore()
	rt := New(Config{DefaultModel: "fallback-model"}, client, store)
	sink := &fakeResponseStreamSink{}

	resp, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "lookup codex",
		Stream: true,
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	}, sink)
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if len(client.streamReqs) != 1 || len(client.streamReqs[0].Tools) != 1 || client.streamReqs[0].Tools[0].Function.Name != "lookup" {
		t.Fatalf("stream chat tools = %#v, want lookup function tool", client.streamReqs)
	}
	if len(sink.created) != 1 || sink.created[0].ID != resp.ID {
		t.Fatalf("created events = %#v, want one response.created", sink.created)
	}
	if len(sink.functionDelta) != 2 {
		t.Fatalf("function deltas = %#v, want two argument chunks", sink.functionDelta)
	}
	if sink.functionDelta[0].ItemID != "fc_call_lookup" || sink.functionDelta[0].Delta != `{"q"` || sink.functionDelta[0].Arguments != `{"q"` {
		t.Fatalf("first function delta = %#v", sink.functionDelta[0])
	}
	if sink.functionDelta[1].ItemID != "fc_call_lookup" || sink.functionDelta[1].Delta != `:"codex"}` || sink.functionDelta[1].Arguments != `{"q":"codex"}` {
		t.Fatalf("second function delta = %#v", sink.functionDelta[1])
	}
	if len(sink.functionDone) != 1 || sink.functionDone[0].ItemID != "fc_call_lookup" || sink.functionDone[0].Arguments != `{"q":"codex"}` {
		t.Fatalf("function done = %#v, want full arguments", sink.functionDone)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "function_call" || resp.Output[0].CallID != "call_lookup" || resp.Output[0].Arguments != `{"q":"codex"}` {
		t.Fatalf("final output = %#v, want function_call", resp.Output)
	}
	stored, ok, err := store.Get(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
	if len(stored.Output) != 1 || stored.Output[0].Type != "function_call" {
		t.Fatalf("stored output = %#v, want function_call", stored.Output)
	}
}

func TestRuntimeCreateStreamLeavesUnregisteredFunctionToolClientOwned(t *testing.T) {
	client := &fakeChatClient{
		streamEvents: []ChatStreamEvent{
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ID:             "call_lookup",
				Type:           "function",
				FunctionName:   "lookup",
				ArgumentsDelta: `{"q":"codex"}`,
			}}},
		},
		streamResp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{{
					ID:   "call_lookup",
					Type: "function",
					Function: ChatToolCallFunction{
						Name:      "lookup",
						Arguments: `{"q":"codex"}`,
					},
				}}},
				FinishReason: "tool_calls",
			}},
		},
	}
	events := &fakeExecutionEventRecorder{}
	rt := New(
		Config{DefaultModel: "fallback-model"},
		client,
		NewMemoryStore(),
		WithExecutionEventRecorder(events),
		WithFunctionToolExecutor("other_tool", StaticFunctionToolExecutor{Output: "must not run"}),
	)
	sink := &fakeResponseStreamSink{}

	resp, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "lookup codex",
		Stream: true,
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	}, sink)
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if len(client.streamReqs) != 1 {
		t.Fatalf("stream requests = %d, want only client-owned function call round", len(client.streamReqs))
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "function_call" || resp.Output[0].CallID != "call_lookup" || resp.Output[0].Name != "lookup" {
		t.Fatalf("response output = %#v, want client-owned function_call", resp.Output)
	}
	if len(sink.outputAdded) != 0 || len(sink.outputDone) != 0 {
		t.Fatalf("server-side output events added=%#v done=%#v, want none for unregistered function", sink.outputAdded, sink.outputDone)
	}
	if len(events.events) != 1 || events.events[0].Status != "requested" || events.events[0].DetailsJSON["tool_name"] != "lookup" {
		t.Fatalf("execution events = %#v, want only requested event for client-owned function", events.events)
	}
}

func TestRuntimeCreateStreamExecutesRegisteredFunctionToolExecutor(t *testing.T) {
	client := &fakeChatClient{
		streamEventBatches: [][]ChatStreamEvent{
			{
				{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
					Index:          0,
					ID:             "call_lookup",
					FunctionName:   "lookup",
					ArgumentsDelta: `{"q"`,
				}}},
				{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
					Index:          0,
					ArgumentsDelta: `:"codex"}`,
				}}},
			},
			{
				{ChoiceIndex: 0, ContentDelta: "Lookup "},
				{ChoiceIndex: 0, ContentDelta: "complete."},
			},
		},
		streamResps: []ChatCompletionResponse{
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
					Message:      ChatMessage{Content: "Lookup complete."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11},
			},
		},
	}
	store := NewMemoryStore()
	sink := &fakeResponseStreamSink{}
	events := &fakeExecutionEventRecorder{}
	rt := New(
		Config{DefaultModel: "fallback-model"},
		client,
		store,
		WithExecutionEventRecorder(events),
		WithFunctionToolExecutor("lookup", StaticFunctionToolExecutor{Output: map[string]any{"ok": true}}),
	)

	resp, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "lookup codex",
		Stream: true,
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	}, sink)
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if len(client.streamReqs) != 2 {
		t.Fatalf("stream requests = %d, want tool call + final call", len(client.streamReqs))
	}
	secondMessages := client.streamReqs[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second stream messages len = %d, want 3: %#v", len(secondMessages), secondMessages)
	}
	if secondMessages[1].Role != "assistant" || len(secondMessages[1].ToolCalls) != 1 || secondMessages[1].ToolCalls[0].ID != "call_lookup" {
		t.Fatalf("second assistant tool call message mismatch: %#v", secondMessages[1])
	}
	if secondMessages[2].Role != "tool" || secondMessages[2].ToolCallID != "call_lookup" || secondMessages[2].Content != `{"ok":true}` {
		t.Fatalf("second tool message mismatch: %#v", secondMessages[2])
	}
	if len(sink.functionDelta) != 2 || sink.functionDelta[1].Arguments != `{"q":"codex"}` {
		t.Fatalf("function argument deltas = %#v", sink.functionDelta)
	}
	if len(sink.functionDone) != 1 || sink.functionDone[0].CallID != "call_lookup" || sink.functionDone[0].Arguments != `{"q":"codex"}` {
		t.Fatalf("function argument done = %#v", sink.functionDone)
	}
	if len(sink.outputAdded) != 1 || sink.outputAdded[0].OutputIndex != 0 || sink.outputAdded[0].Item.Type != "function_call_output" || sink.outputAdded[0].Item.Status != "in_progress" || sink.outputAdded[0].Item.CallID != "call_lookup" || sink.outputAdded[0].Item.Output != nil {
		t.Fatalf("output item added = %#v, want started function_call_output call_lookup without output", sink.outputAdded)
	}
	if len(sink.outputDone) != 1 || sink.outputDone[0].OutputIndex != 0 || sink.outputDone[0].Item.Type != "function_call_output" || sink.outputDone[0].Item.CallID != "call_lookup" {
		t.Fatalf("output item done = %#v, want function_call_output call_lookup", sink.outputDone)
	}
	if len(sink.deltas) != 2 || sink.deltas[0].OutputIndex != 1 || sink.deltas[0].Delta != "Lookup " || sink.deltas[1].Delta != "complete." {
		t.Fatalf("text deltas = %#v", sink.deltas)
	}
	wantEvents := []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.completed",
	}
	if !reflect.DeepEqual(sink.events, wantEvents) {
		t.Fatalf("stream events mismatch\nwant: %#v\n got: %#v", wantEvents, sink.events)
	}
	if resp.Usage != (protocol.Usage{InputTokens: 17, OutputTokens: 5, TotalTokens: 22}) {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("output len = %d, want function_call_output + final message: %#v", len(resp.Output), resp.Output)
	}
	if resp.Output[0].Type != "function_call_output" || resp.Output[0].CallID != "call_lookup" {
		t.Fatalf("first output = %#v", resp.Output[0])
	}
	if resp.Output[1].Type != "message" || resp.Output[1].Content[0].Text != "Lookup complete." {
		t.Fatalf("second output = %#v", resp.Output[1])
	}
	stored, ok, err := store.Get(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(stored.Output, resp.Output) {
		t.Fatalf("stored output mismatch\nwant: %#v\n got: %#v", resp.Output, stored.Output)
	}
	if len(events.events) != 3 {
		t.Fatalf("execution events len = %d, want requested/started/completed: %#v", len(events.events), events.events)
	}
	if events.events[0].Status != "requested" || events.events[0].DetailsJSON["tool_name"] != "lookup" || events.events[0].DetailsJSON["call_id"] != "call_lookup" {
		t.Fatalf("requested event mismatch: %#v", events.events[0])
	}
	if events.events[1].Status != "started" || events.events[1].DetailsJSON["tool_name"] != "lookup" || events.events[1].DetailsJSON["call_id"] != "call_lookup" || events.events[1].DetailsJSON["stream"] != true {
		t.Fatalf("started event mismatch: %#v", events.events[1])
	}
	if events.events[2].Status != "completed" || events.events[2].DetailsJSON["tool_name"] != "lookup" || events.events[2].DetailsJSON["call_id"] != "call_lookup" || events.events[2].DetailsJSON["stream"] != true {
		t.Fatalf("completed event mismatch: %#v", events.events[2])
	}
}

func TestRuntimeCreateStreamExecutesMultipleFunctionToolCallsInOrder(t *testing.T) {
	client := &fakeChatClient{
		streamEventBatches: [][]ChatStreamEvent{
			{
				{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{
					{
						Index:          0,
						ID:             "call_lookup",
						FunctionName:   "lookup",
						ArgumentsDelta: `{"q"`,
					},
					{
						Index:          1,
						ID:             "call_summarize",
						FunctionName:   "summarize",
						ArgumentsDelta: `{"text"`,
					},
				}},
				{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{
					{
						Index:          0,
						ArgumentsDelta: `:"codex"}`,
					},
					{
						Index:          1,
						ArgumentsDelta: `:"trace"}`,
					},
				}},
			},
			{
				{ChoiceIndex: 0, ContentDelta: "Both "},
				{ChoiceIndex: 0, ContentDelta: "complete."},
			},
		},
		streamResps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message: ChatMessage{
						ToolCalls: []ChatToolCall{
							{
								ID:   "call_lookup",
								Type: "function",
								Function: ChatToolCallFunction{
									Name:      "lookup",
									Arguments: `{"q":"codex"}`,
								},
							},
							{
								ID:   "call_summarize",
								Type: "function",
								Function: ChatToolCallFunction{
									Name:      "summarize",
									Arguments: `{"text":"trace"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				}},
				Usage: ChatUsage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Content: "Both complete."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
			},
		},
	}
	sink := &fakeResponseStreamSink{}
	events := &fakeExecutionEventRecorder{}
	rt := New(
		Config{DefaultModel: "fallback-model"},
		client,
		NewMemoryStore(),
		WithExecutionEventRecorder(events),
		WithFunctionToolExecutor("lookup", StaticFunctionToolExecutor{Output: map[string]any{"found": true}}),
		WithFunctionToolExecutor("summarize", StaticFunctionToolExecutor{Output: "short"}),
	)

	resp, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "run both",
		Stream: true,
		Tools: []protocol.Tool{
			{Type: "function", Name: "lookup", Parameters: map[string]any{"type": "object"}},
			{Type: "function", Name: "summarize", Parameters: map[string]any{"type": "object"}},
		},
	}, sink)
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if len(client.streamReqs) != 2 {
		t.Fatalf("stream requests = %d, want tool call + final call", len(client.streamReqs))
	}
	secondMessages := client.streamReqs[1].Messages
	if len(secondMessages) != 4 {
		t.Fatalf("second stream messages len = %d, want 4: %#v", len(secondMessages), secondMessages)
	}
	if secondMessages[1].Role != "assistant" || len(secondMessages[1].ToolCalls) != 2 || secondMessages[1].ToolCalls[0].ID != "call_lookup" || secondMessages[1].ToolCalls[1].ID != "call_summarize" {
		t.Fatalf("second assistant tool call message mismatch: %#v", secondMessages[1])
	}
	if secondMessages[2].Role != "tool" || secondMessages[2].ToolCallID != "call_lookup" || secondMessages[3].Role != "tool" || secondMessages[3].ToolCallID != "call_summarize" {
		t.Fatalf("second tool message order mismatch: %#v", secondMessages[2:])
	}
	if len(sink.functionDone) != 2 || sink.functionDone[0].OutputIndex != 0 || sink.functionDone[0].CallID != "call_lookup" || sink.functionDone[1].OutputIndex != 1 || sink.functionDone[1].CallID != "call_summarize" {
		t.Fatalf("function argument done = %#v, want lookup then summarize", sink.functionDone)
	}
	if len(sink.outputAdded) != 2 {
		t.Fatalf("output item added = %#v, want two started items", sink.outputAdded)
	}
	if sink.outputAdded[0].OutputIndex != 0 || sink.outputAdded[0].Item.CallID != "call_lookup" || sink.outputAdded[0].Item.Status != "in_progress" {
		t.Fatalf("first output item added = %#v, want started lookup", sink.outputAdded[0])
	}
	if sink.outputAdded[1].OutputIndex != 1 || sink.outputAdded[1].Item.CallID != "call_summarize" || sink.outputAdded[1].Item.Status != "in_progress" {
		t.Fatalf("second output item added = %#v, want started summarize", sink.outputAdded[1])
	}
	if len(sink.outputDone) != 2 {
		t.Fatalf("output item done = %#v, want two completed items", sink.outputDone)
	}
	if sink.outputDone[0].OutputIndex != 0 || sink.outputDone[0].Item.CallID != "call_lookup" || sink.outputDone[0].Item.Status != "completed" {
		t.Fatalf("first output item done = %#v, want completed lookup", sink.outputDone[0])
	}
	if sink.outputDone[1].OutputIndex != 1 || sink.outputDone[1].Item.CallID != "call_summarize" || sink.outputDone[1].Item.Status != "completed" {
		t.Fatalf("second output item done = %#v, want completed summarize", sink.outputDone[1])
	}
	wantEvents := []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.completed",
	}
	if !reflect.DeepEqual(sink.events, wantEvents) {
		t.Fatalf("stream events mismatch\nwant: %#v\n got: %#v", wantEvents, sink.events)
	}
	if len(resp.Output) != 3 {
		t.Fatalf("output len = %d, want two tool outputs + final message: %#v", len(resp.Output), resp.Output)
	}
	if resp.Output[0].Type != "function_call_output" || resp.Output[0].CallID != "call_lookup" || resp.Output[1].Type != "function_call_output" || resp.Output[1].CallID != "call_summarize" {
		t.Fatalf("tool output order mismatch: %#v", resp.Output)
	}
	if resp.Output[2].Type != "message" || resp.Output[2].Content[0].Text != "Both complete." {
		t.Fatalf("final output = %#v, want final message", resp.Output[2])
	}
	if len(events.events) != 6 {
		t.Fatalf("execution events len = %d, want two requested plus two started/completed pairs: %#v", len(events.events), events.events)
	}
	wantStatuses := []string{"requested", "requested", "started", "completed", "started", "completed"}
	for i, want := range wantStatuses {
		if events.events[i].Status != want {
			t.Fatalf("execution event %d status = %q, want %q: %#v", i, events.events[i].Status, want, events.events[i])
		}
	}
}

func TestRuntimeCreateStreamEmitsFailedOutputItemDoneForFunctionToolExecutorFailure(t *testing.T) {
	client := &fakeChatClient{
		streamEvents: []ChatStreamEvent{
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ID:             "call_lookup",
				FunctionName:   "lookup",
				ArgumentsDelta: `{"secret"`,
			}}},
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ArgumentsDelta: `:"do-not-leak"}`,
			}}},
		},
		streamResp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{
					ToolCalls: []ChatToolCall{{
						ID:   "call_lookup",
						Type: "function",
						Function: ChatToolCallFunction{
							Name:      "lookup",
							Arguments: `{"secret":"do-not-leak"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: ChatUsage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
		},
	}
	executorErr := errors.New("lookup backend failed")
	sink := &fakeResponseStreamSink{}
	rt := New(
		Config{DefaultModel: "fallback-model"},
		client,
		NewMemoryStore(),
		WithFunctionToolExecutor("lookup", FunctionToolExecutorFunc(func(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error) {
			return FunctionToolResult{Output: "sensitive-output"}, executorErr
		})),
	)

	_, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "lookup codex",
		Stream: true,
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	}, sink)
	if !errors.Is(err, executorErr) {
		t.Fatalf("CreateStream() error = %v, want executor error", err)
	}
	wantEvents := []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
	}
	if !reflect.DeepEqual(sink.events, wantEvents) {
		t.Fatalf("stream events mismatch\nwant: %#v\n got: %#v", wantEvents, sink.events)
	}
	if len(sink.outputAdded) != 1 || sink.outputAdded[0].Item.Status != "in_progress" || sink.outputAdded[0].Item.CallID != "call_lookup" {
		t.Fatalf("output item added = %#v, want started call_lookup", sink.outputAdded)
	}
	if len(sink.outputDone) != 1 {
		t.Fatalf("output item done = %#v, want one failed item", sink.outputDone)
	}
	done := sink.outputDone[0].Item
	if done.Type != "function_call_output" || done.Status != "failed" || done.CallID != "call_lookup" || done.Name != "lookup" {
		t.Fatalf("failed output item = %#v, want failed lookup call output", done)
	}
	if done.Output != nil || strings.Contains(toJSONForTest(t, done), "sensitive-output") {
		t.Fatalf("failed output item leaked output: %#v", done)
	}
}

func TestRuntimeCreateStreamEmitsFailedOutputItemDoneForSecondFunctionToolExecutorFailure(t *testing.T) {
	client := &fakeChatClient{
		streamEvents: []ChatStreamEvent{
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{
				{
					Index:          0,
					ID:             "call_lookup",
					FunctionName:   "lookup",
					ArgumentsDelta: `{"q":"codex"}`,
				},
				{
					Index:          1,
					ID:             "call_summarize",
					FunctionName:   "summarize",
					ArgumentsDelta: `{"text":"trace"}`,
				},
			}},
		},
		streamResp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message: ChatMessage{
					ToolCalls: []ChatToolCall{
						{
							ID:   "call_lookup",
							Type: "function",
							Function: ChatToolCallFunction{
								Name:      "lookup",
								Arguments: `{"q":"codex"}`,
							},
						},
						{
							ID:   "call_summarize",
							Type: "function",
							Function: ChatToolCallFunction{
								Name:      "summarize",
								Arguments: `{"text":"trace"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			}},
			Usage: ChatUsage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12},
		},
	}
	executorErr := errors.New("summarize backend failed")
	sink := &fakeResponseStreamSink{}
	rt := New(
		Config{DefaultModel: "fallback-model"},
		client,
		NewMemoryStore(),
		WithFunctionToolExecutor("lookup", StaticFunctionToolExecutor{Output: map[string]any{"found": true}}),
		WithFunctionToolExecutor("summarize", FunctionToolExecutorFunc(func(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error) {
			return FunctionToolResult{Output: "sensitive-output"}, executorErr
		})),
	)

	_, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:  "run both",
		Stream: true,
		Tools: []protocol.Tool{
			{Type: "function", Name: "lookup", Parameters: map[string]any{"type": "object"}},
			{Type: "function", Name: "summarize", Parameters: map[string]any{"type": "object"}},
		},
	}, sink)
	if !errors.Is(err, executorErr) {
		t.Fatalf("CreateStream() error = %v, want executor error", err)
	}
	if len(client.streamReqs) != 1 {
		t.Fatalf("stream requests = %d, want only failed tool-call round", len(client.streamReqs))
	}
	if len(sink.outputAdded) != 2 {
		t.Fatalf("output item added = %#v, want started lookup and summarize", sink.outputAdded)
	}
	if sink.outputAdded[0].OutputIndex != 0 || sink.outputAdded[0].Item.CallID != "call_lookup" || sink.outputAdded[0].Item.Status != "in_progress" {
		t.Fatalf("first output item added = %#v, want started lookup", sink.outputAdded[0])
	}
	if sink.outputAdded[1].OutputIndex != 1 || sink.outputAdded[1].Item.CallID != "call_summarize" || sink.outputAdded[1].Item.Status != "in_progress" {
		t.Fatalf("second output item added = %#v, want started summarize", sink.outputAdded[1])
	}
	if len(sink.outputDone) != 2 {
		t.Fatalf("output item done = %#v, want completed lookup and failed summarize", sink.outputDone)
	}
	if sink.outputDone[0].OutputIndex != 0 || sink.outputDone[0].Item.CallID != "call_lookup" || sink.outputDone[0].Item.Status != "completed" {
		t.Fatalf("first output item done = %#v, want completed lookup", sink.outputDone[0])
	}
	failed := sink.outputDone[1].Item
	if sink.outputDone[1].OutputIndex != 1 || failed.Type != "function_call_output" || failed.CallID != "call_summarize" || failed.Name != "summarize" || failed.Status != "failed" {
		t.Fatalf("second output item done = %#v, want failed summarize", sink.outputDone[1])
	}
	if failed.Output != nil || strings.Contains(toJSONForTest(t, failed), "sensitive-output") {
		t.Fatalf("failed output item leaked output: %#v", failed)
	}
	wantEvents := []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.output_item.added",
		"response.output_item.done",
	}
	if !reflect.DeepEqual(sink.events, wantEvents) {
		t.Fatalf("stream events mismatch\nwant: %#v\n got: %#v", wantEvents, sink.events)
	}
}

func TestRuntimeCreateStreamExecutesHostedWebSearchToolLoop(t *testing.T) {
	client := &fakeChatClient{
		streamEventBatches: [][]ChatStreamEvent{
			{
				{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
					Index:          0,
					ID:             "call_search",
					FunctionName:   "web_search",
					ArgumentsDelta: `{"query"`,
				}}},
				{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
					Index:          0,
					ArgumentsDelta: `:"llm trace replay"}`,
				}}},
			},
			{
				{ChoiceIndex: 0, ContentDelta: "Use "},
				{ChoiceIndex: 0, ContentDelta: "cassettes."},
			},
		},
		streamResps: []ChatCompletionResponse{
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
					Message:      ChatMessage{Content: "Use cassettes."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16},
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
	store := NewMemoryStore()
	sink := &fakeResponseStreamSink{}
	events := &fakeExecutionEventRecorder{}
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(Config{
		DefaultModel:        "fallback-model",
		WebSearchEnabled:    true,
		WebSearchMaxResults: 2,
	}, client, store, WithWebSearchProvider(provider), WithExecutionEventRecorder(events), WithToolCallAuditRecorder(toolAudits))

	resp, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:      "search docs",
		Stream:     true,
		ToolChoice: "web_search",
		Tools:      []protocol.Tool{{Type: "web_search_preview"}},
	}, sink)
	if err != nil {
		t.Fatalf("CreateStream() error = %v", err)
	}
	if len(client.streamReqs) != 2 {
		t.Fatalf("stream requests = %d, want tool call + final call", len(client.streamReqs))
	}
	firstTools := client.streamReqs[0].Tools
	if len(firstTools) != 1 || firstTools[0].Function.Name != "web_search" {
		t.Fatalf("first stream tools = %#v, want web_search", firstTools)
	}
	if len(provider.queries) != 1 || provider.queries[0].Text != "llm trace replay" || provider.queries[0].MaxResults != 2 {
		t.Fatalf("provider queries = %#v", provider.queries)
	}
	secondMessages := client.streamReqs[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second stream messages len = %d, want 3: %#v", len(secondMessages), secondMessages)
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
	if len(sink.functionDelta) != 2 || sink.functionDelta[1].Arguments != `{"query":"llm trace replay"}` {
		t.Fatalf("function argument deltas = %#v", sink.functionDelta)
	}
	if len(sink.functionDone) != 1 || sink.functionDone[0].CallID != "call_search" || sink.functionDone[0].Arguments != `{"query":"llm trace replay"}` {
		t.Fatalf("function argument done = %#v", sink.functionDone)
	}
	if len(sink.outputAdded) != 1 || sink.outputAdded[0].OutputIndex != 0 || sink.outputAdded[0].Item.Type != "web_search_call" || sink.outputAdded[0].Item.Status != "in_progress" || sink.outputAdded[0].Item.CallID != "call_search" {
		t.Fatalf("output item added = %#v, want started web_search_call call_search", sink.outputAdded)
	}
	if got := sink.outputAdded[0].Item.Action["query"]; got != "llm trace replay" {
		t.Fatalf("output item added query = %#v", got)
	}
	if len(sink.outputDone) != 1 || sink.outputDone[0].OutputIndex != 0 || sink.outputDone[0].Item.Type != "web_search_call" || sink.outputDone[0].Item.CallID != "call_search" {
		t.Fatalf("output item done = %#v, want web_search_call call_search", sink.outputDone)
	}
	if len(sink.deltas) != 2 || sink.deltas[0].OutputIndex != 1 || sink.deltas[0].Delta != "Use " || sink.deltas[1].Delta != "cassettes." {
		t.Fatalf("text deltas = %#v", sink.deltas)
	}
	wantEvents := []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.completed",
	}
	if !reflect.DeepEqual(sink.events, wantEvents) {
		t.Fatalf("stream events mismatch\nwant: %#v\n got: %#v", wantEvents, sink.events)
	}
	if resp.Usage != (protocol.Usage{InputTokens: 22, OutputTokens: 7, TotalTokens: 29}) {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("output len = %d, want web_search_call + final message: %#v", len(resp.Output), resp.Output)
	}
	if resp.Output[0].Type != "web_search_call" || resp.Output[0].CallID != "call_search" {
		t.Fatalf("first output = %#v", resp.Output[0])
	}
	if got := resp.Output[0].Action["query"]; got != "llm trace replay" {
		t.Fatalf("web_search action query = %#v", got)
	}
	if resp.Output[1].Type != "message" || resp.Output[1].Content[0].Text != "Use cassettes." {
		t.Fatalf("second output = %#v", resp.Output[1])
	}
	if len(events.events) != 3 {
		t.Fatalf("execution events len = %d, want requested/started/completed: %#v", len(events.events), events.events)
	}
	if events.events[0].Status != "requested" || events.events[0].DetailsJSON["tool_name"] != "web_search" || events.events[0].DetailsJSON["call_id"] != "call_search" {
		t.Fatalf("requested event mismatch: %#v", events.events[0])
	}
	if events.events[1].Status != "started" || events.events[1].DetailsJSON["tool_name"] != "web_search" || events.events[1].DetailsJSON["stream"] != true {
		t.Fatalf("started event mismatch: %#v", events.events[1])
	}
	if events.events[2].Status != "completed" || events.events[2].DetailsJSON["tool_name"] != "web_search" || events.events[2].DetailsJSON["call_id"] != "call_search" || events.events[2].DetailsJSON["stream"] != true {
		t.Fatalf("completed event mismatch: %#v", events.events[2])
	}
	if len(toolAudits.entries) != 2 || toolAudits.entries[0].Status != "started" || toolAudits.entries[1].Status != "completed" {
		t.Fatalf("tool audits = %#v, want started/completed", toolAudits.entries)
	}
	if toolAudits.entries[1].ResponseID != resp.ID || toolAudits.entries[1].MetadataJSON["stream"] != true {
		t.Fatalf("stream tool audit = %#v, want response id and stream metadata", toolAudits.entries[1])
	}
	stored, ok, err := store.Get(context.Background(), resp.ID)
	if err != nil || !ok {
		t.Fatalf("stored response lookup ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(stored.Output, resp.Output) {
		t.Fatalf("stored output mismatch\nwant: %#v\n got: %#v", resp.Output, stored.Output)
	}
}

func TestRuntimeCreateStreamEmitsFailedOutputItemDoneForHostedWebSearchProviderFailure(t *testing.T) {
	client := &fakeChatClient{
		streamEvents: []ChatStreamEvent{
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ID:             "call_search",
				FunctionName:   "web_search",
				ArgumentsDelta: `{"query"`,
			}}},
			{ChoiceIndex: 0, ToolCallDeltas: []ChatStreamToolCallDelta{{
				Index:          0,
				ArgumentsDelta: `:"llm trace replay"}`,
			}}},
		},
		streamResp: ChatCompletionResponse{
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
	}
	providerErr := errors.New("search backend failed")
	provider := &fakeWebSearchProvider{err: providerErr}
	sink := &fakeResponseStreamSink{}
	rt := New(Config{
		DefaultModel:     "fallback-model",
		WebSearchEnabled: true,
	}, client, NewMemoryStore(), WithWebSearchProvider(provider))

	_, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:      "search docs",
		Stream:     true,
		ToolChoice: "web_search",
		Tools:      []protocol.Tool{{Type: "web_search_preview"}},
	}, sink)
	if !errors.Is(err, providerErr) {
		t.Fatalf("CreateStream() error = %v, want provider error", err)
	}
	wantEvents := []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
	}
	if !reflect.DeepEqual(sink.events, wantEvents) {
		t.Fatalf("stream events mismatch\nwant: %#v\n got: %#v", wantEvents, sink.events)
	}
	if len(provider.queries) != 1 || provider.queries[0].Text != "llm trace replay" {
		t.Fatalf("provider queries = %#v, want llm trace replay", provider.queries)
	}
	if len(sink.outputDone) != 1 {
		t.Fatalf("output item done = %#v, want one failed item", sink.outputDone)
	}
	done := sink.outputDone[0].Item
	if done.Type != "web_search_call" || done.Status != "failed" || done.CallID != "call_search" {
		t.Fatalf("failed output item = %#v, want failed web_search_call call_search", done)
	}
	if got := done.Action["query"]; got != "llm trace replay" {
		t.Fatalf("failed output item query = %#v", got)
	}
	if _, ok := done.Action["sources"]; ok {
		t.Fatalf("failed output item leaked sources: %#v", done)
	}
}

func TestRuntimeCompactStoresSummaryBoundaryForContinuation(t *testing.T) {
	store := NewMemoryStore()
	target := protocol.Response{
		ID:        "resp_target",
		Object:    "response",
		Status:    "completed",
		Model:     "gpt-test",
		CreatedAt: 100,
		Metadata: map[string]any{
			"codex": map[string]any{"thread_id": "thread_1"},
		},
		Output: []protocol.OutputItem{{
			ID:      "msg_target",
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "assistant answer"}},
		}},
	}
	if err := store.Put(context.Background(), target, protocol.CreateResponseRequest{Input: "first"}, []protocol.InputItem{messageInput("in_target", "first")}, target.Output); err != nil {
		t.Fatalf("seed target response: %v", err)
	}
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "User wants deterministic replay. Keep the cassette constraint."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 30, CompletionTokens: 10, TotalTokens: 40},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "continued"},
					FinishReason: "stop",
				}},
			},
		},
	}
	rt := New(Config{DefaultModel: "gpt-test"}, client, store)

	compactResp, err := rt.Compact(context.Background(), protocol.CompactResponseRequest{
		ResponseID: "resp_target",
		Metadata:   map[string]any{"source": "test"},
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if compactResp.PreviousResponseID != "resp_target" || compactResp.Model != "gpt-test" {
		t.Fatalf("compact response link/model = %q/%q, want resp_target/gpt-test", compactResp.PreviousResponseID, compactResp.Model)
	}
	if len(compactResp.Output) != 1 || compactResp.Output[0].Type != "summary" || compactResp.Output[0].Content[0].Text == "" {
		t.Fatalf("compact output = %#v, want summary item", compactResp.Output)
	}
	if compactResp.Usage.TotalTokens != 40 {
		t.Fatalf("compact usage = %#v, want chat usage", compactResp.Usage)
	}
	assertCompactV2Provenance(t, compactResp.Metadata, compactV2ProvenanceWant{
		sourceResponseID:      "resp_target",
		compactResponseID:     compactResp.ID,
		trigger:               "manual",
		sourceItemCount:       2,
		sourceInputItemCount:  1,
		sourceOutputItemCount: 1,
		retainedItemCount:     2,
		summaryItemID:         compactResp.Output[0].ID,
	})
	storedCompact, ok, err := store.Get(context.Background(), compactResp.ID)
	if err != nil || !ok {
		t.Fatalf("stored compact lookup ok=%v err=%v", ok, err)
	}
	assertCompactV2Provenance(t, storedCompact.Metadata, compactV2ProvenanceWant{
		sourceResponseID:      "resp_target",
		compactResponseID:     compactResp.ID,
		trigger:               "manual",
		sourceItemCount:       2,
		sourceInputItemCount:  1,
		sourceOutputItemCount: 1,
		retainedItemCount:     2,
		summaryItemID:         compactResp.Output[0].ID,
	})
	inputs, ok, err := store.InputItems(context.Background(), compactResp.ID)
	if err != nil || !ok {
		t.Fatalf("compact input lookup ok=%v err=%v", ok, err)
	}
	if len(inputs) != 1 || inputs[0].Type != "compact_request" {
		t.Fatalf("compact inputs = %#v, want compact_request", inputs)
	}

	_, err = rt.Create(context.Background(), protocol.CreateResponseRequest{
		PreviousResponseID: compactResp.ID,
		Input:              "next",
	})
	if err != nil {
		t.Fatalf("Create after compact error = %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat requests = %d, want compact + continuation", len(client.reqs))
	}
	continuationMessages := client.reqs[1].Messages
	if len(continuationMessages) < 2 {
		t.Fatalf("continuation messages = %#v, want summary system and user", continuationMessages)
	}
	if continuationMessages[0].Role != "system" || !strings.Contains(chatMessageContentText(continuationMessages[0].Content), "Previous conversation summary") {
		t.Fatalf("first continuation message = %#v, want summary system", continuationMessages[0])
	}
	for _, message := range continuationMessages {
		if chatMessageContentText(message.Content) == "first" {
			t.Fatalf("continuation included pre-compact user message: %#v", continuationMessages)
		}
	}
}

func TestRuntimeCreateAutoCompactsWhenHistoryExceedsThreshold(t *testing.T) {
	store := NewMemoryStore()
	target := protocol.Response{
		ID:        "resp_auto_target",
		Object:    "response",
		Status:    "completed",
		Model:     "gpt-test",
		CreatedAt: 100,
		Output: []protocol.OutputItem{{
			ID:      "msg_auto_target",
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "old answer"}},
		}},
	}
	if err := store.Put(context.Background(), target, protocol.CreateResponseRequest{Input: "old question"}, []protocol.InputItem{messageInput("in_auto_target", "old question")}, target.Output); err != nil {
		t.Fatalf("seed target response: %v", err)
	}
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "Auto compact summary."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "new answer"},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 6, CompletionTokens: 3, TotalTokens: 9},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	rt := New(Config{
		DefaultModel:                "gpt-test",
		AutoCompact:                 true,
		CompactHistoryItemThreshold: 1,
	}, client, store, WithExecutionEventRecorder(events))

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		PreviousResponseID: "resp_auto_target",
		Input:              "new question",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat requests = %d, want compact + create", len(client.reqs))
	}
	if resp.PreviousResponseID == "" || resp.PreviousResponseID == "resp_auto_target" {
		t.Fatalf("response previous_response_id = %q, want generated compact response id", resp.PreviousResponseID)
	}
	compactResp, ok, err := store.Get(context.Background(), resp.PreviousResponseID)
	if err != nil || !ok {
		t.Fatalf("compact response lookup ok=%v err=%v", ok, err)
	}
	if compactResp.PreviousResponseID != "resp_auto_target" || compactResp.Output[0].Type != "summary" {
		t.Fatalf("compact response = %#v, want summary linked to target", compactResp)
	}
	assertCompactV2Provenance(t, compactResp.Metadata, compactV2ProvenanceWant{
		sourceResponseID:      "resp_auto_target",
		compactResponseID:     compactResp.ID,
		trigger:               "auto",
		triggerReason:         "history_items",
		sourceItemCount:       2,
		sourceInputItemCount:  1,
		sourceOutputItemCount: 1,
		retainedItemCount:     2,
		summaryItemID:         compactResp.Output[0].ID,
	})
	if len(client.reqs[1].Messages) < 2 || !strings.Contains(chatMessageContentText(client.reqs[1].Messages[0].Content), "Auto compact summary") {
		t.Fatalf("post-compact chat messages = %#v, want compact summary in context", client.reqs[1].Messages)
	}
	if got := findExecutionEvent(events.events, "response.compact", "auto_triggered"); got == nil {
		t.Fatalf("execution events missing auto_triggered compact event: %#v", events.events)
	} else {
		assertAutoCompactProvenance(t, got, autoCompactProvenanceWant{
			trigger:                    "history_items",
			originalInputItemCount:     2,
			retainedInputItemCount:     1,
			droppedInputItemCount:      1,
			retainedWindowStart:        1,
			retainedWindowEnd:          2,
			historyItemThresholdSource: "config",
			contextWindowLimitSource:   "unset",
			reservedOutputLimitSource:  "unset",
		})
	}
}

func TestRuntimeCreateAutoCompactUsesMatchedModelProfileThreshold(t *testing.T) {
	store := NewMemoryStore()
	seedResponseForAutoCompactTest(t, store, "resp_profile_target", "gpt-4o-mini")
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "Profile compact summary."},
					FinishReason: "stop",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "new answer"},
					FinishReason: "stop",
				}},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	rt := New(Config{
		DefaultModel:                "fallback-model",
		AutoCompact:                 true,
		CompactHistoryItemThreshold: 10,
		ModelProfiles: []ModelProfile{{
			Pattern: "gpt-4o*",
			Budget: ContextBudget{
				ContextWindowTokens:         128000,
				MaxOutputTokens:             4096,
				CompactHistoryItemThreshold: 1,
			},
		}},
	}, client, store, WithExecutionEventRecorder(events))

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Model:              "gpt-4o-mini",
		PreviousResponseID: "resp_profile_target",
		Input:              "new question",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat requests = %d, want compact + create", len(client.reqs))
	}
	if resp.PreviousResponseID == "" || resp.PreviousResponseID == "resp_profile_target" {
		t.Fatalf("response previous_response_id = %q, want generated compact response id", resp.PreviousResponseID)
	}
	if got := findExecutionEvent(events.events, "response.compact", "auto_triggered"); got == nil || got.DetailsJSON["history_item_threshold"] != 1 {
		t.Fatalf("auto_triggered event = %#v, want profile threshold 1", got)
	} else if got.DetailsJSON["history_item_threshold_source"] != "model_profile" {
		t.Fatalf("auto_triggered event = %#v, want model_profile threshold source", got)
	}
}

func TestRuntimeCreateAutoCompactsWhenEstimatedTokensExceedContextWindow(t *testing.T) {
	store := NewMemoryStore()
	seedResponseForAutoCompactTest(t, store, "resp_token_target", "gpt-4o-mini")
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "Token compact summary."},
					FinishReason: "stop",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "new answer"},
					FinishReason: "stop",
				}},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	rt := New(Config{
		DefaultModel: "fallback-model",
		AutoCompact:  true,
		ModelProfiles: []ModelProfile{{
			Pattern: "gpt-4o*",
			Budget: ContextBudget{
				ContextWindowTokens: 10,
				MaxOutputTokens:     8,
			},
		}},
	}, client, store, WithExecutionEventRecorder(events))

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Model:              "gpt-4o-mini",
		PreviousResponseID: "resp_token_target",
		Input:              "new question",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat requests = %d, want compact + create", len(client.reqs))
	}
	if resp.PreviousResponseID == "" || resp.PreviousResponseID == "resp_token_target" {
		t.Fatalf("response previous_response_id = %q, want generated compact response id", resp.PreviousResponseID)
	}
	if got := findExecutionEvent(events.events, "response.compact", "auto_triggered"); got == nil ||
		got.DetailsJSON["trigger"] != "context_window_tokens" ||
		got.DetailsJSON["estimated_input_tokens"] == nil ||
		got.DetailsJSON["context_window_tokens"] != 10 ||
		got.DetailsJSON["reserved_output_tokens"] != 8 {
		t.Fatalf("auto_triggered event = %#v, want context_window token trigger", got)
	} else {
		assertAutoCompactProvenance(t, got, autoCompactProvenanceWant{
			trigger:                    "context_window_tokens",
			originalInputItemCount:     2,
			retainedInputItemCount:     1,
			droppedInputItemCount:      1,
			retainedWindowStart:        1,
			retainedWindowEnd:          2,
			historyItemThresholdSource: "unset",
			contextWindowLimitSource:   "model_profile",
			reservedOutputLimitSource:  "model_profile",
		})
	}
	if client.reqs[1].MaxTokens != 8 {
		t.Fatalf("post-compact chat request max tokens = %d, want profile max 8", client.reqs[1].MaxTokens)
	}
}

func TestRuntimeCreateAutoCompactUsesInjectedTokenEstimator(t *testing.T) {
	store := NewMemoryStore()
	seedResponseForAutoCompactTest(t, store, "resp_estimator_target", "gpt-4o-mini")
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "Injected estimator compact summary."},
					FinishReason: "stop",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "new answer"},
					FinishReason: "stop",
				}},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	estimator := &fixedTokenEstimator{tokens: 7}
	rt := New(Config{
		DefaultModel: "fallback-model",
		AutoCompact:  true,
		ModelProfiles: []ModelProfile{{
			Pattern: "gpt-4o*",
			Budget: ContextBudget{
				ContextWindowTokens: 10,
				MaxOutputTokens:     4,
			},
		}},
	}, client, store, WithExecutionEventRecorder(events), WithTokenEstimator(estimator))

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Model:              "gpt-4o-mini",
		PreviousResponseID: "resp_estimator_target",
		Input:              "new question",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat requests = %d, want compact + create", len(client.reqs))
	}
	if estimator.calls != 1 {
		t.Fatalf("token estimator calls = %d, want 1", estimator.calls)
	}
	if resp.PreviousResponseID == "" || resp.PreviousResponseID == "resp_estimator_target" {
		t.Fatalf("response previous_response_id = %q, want generated compact response id", resp.PreviousResponseID)
	}
	if got := findExecutionEvent(events.events, "response.compact", "auto_triggered"); got == nil ||
		got.DetailsJSON["trigger"] != "context_window_tokens" ||
		got.DetailsJSON["estimated_input_tokens"] != 7 ||
		got.DetailsJSON["context_window_tokens"] != 10 ||
		got.DetailsJSON["reserved_output_tokens"] != 4 {
		t.Fatalf("auto_triggered event = %#v, want injected estimator token trigger", got)
	} else {
		assertAutoCompactProvenance(t, got, autoCompactProvenanceWant{
			trigger:                    "context_window_tokens",
			originalInputItemCount:     2,
			retainedInputItemCount:     1,
			droppedInputItemCount:      1,
			retainedWindowStart:        1,
			retainedWindowEnd:          2,
			historyItemThresholdSource: "unset",
			contextWindowLimitSource:   "model_profile",
			reservedOutputLimitSource:  "model_profile",
		})
	}
}

func TestRuntimeCreateAutoCompactFallsBackToGlobalThresholdWhenProfileDoesNotMatch(t *testing.T) {
	store := NewMemoryStore()
	seedResponseForAutoCompactTest(t, store, "resp_fallback_target", "gpt-test")
	client := &fakeChatClient{
		resp: ChatCompletionResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "new answer"},
				FinishReason: "stop",
			}},
		},
	}
	rt := New(Config{
		DefaultModel:                "fallback-model",
		AutoCompact:                 true,
		CompactHistoryItemThreshold: 10,
		ModelProfiles: []ModelProfile{{
			Name: "other-model",
			Budget: ContextBudget{
				CompactHistoryItemThreshold: 1,
			},
		}},
	}, client, store)

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Model:              "gpt-test",
		PreviousResponseID: "resp_fallback_target",
		Input:              "new question",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("chat requests = %d, want create only", len(client.reqs))
	}
	if resp.PreviousResponseID != "resp_fallback_target" {
		t.Fatalf("response previous_response_id = %q, want original target", resp.PreviousResponseID)
	}
}

func TestRuntimeCreateStreamAutoCompactRequiresDeferredFallbackBeforeWriting(t *testing.T) {
	store := NewMemoryStore()
	seedResponseForAutoCompactTest(t, store, "resp_stream_compact_target", "gpt-test")
	client := &fakeChatClient{streamResp: finalChatResponse("should not stream")}
	rt := New(Config{
		DefaultModel:                "fallback-model",
		AutoCompact:                 true,
		CompactHistoryItemThreshold: 1,
	}, client, store)
	sink := &fakeResponseStreamSink{}

	_, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Model:              "gpt-test",
		PreviousResponseID: "resp_stream_compact_target",
		Input:              "new question",
	}, sink)
	if !errors.Is(err, ErrIncrementalStreamUnsupported) {
		t.Fatalf("CreateStream() error = %v, want ErrIncrementalStreamUnsupported", err)
	}
	if !strings.Contains(err.Error(), "auto compact requires deferred stream") {
		t.Fatalf("CreateStream() error = %q, want auto compact fallback reason", err.Error())
	}
	if len(client.streamReqs) != 0 || len(client.reqs) != 0 {
		t.Fatalf("chat requests = stream:%d nonstream:%d, want none before deferred fallback", len(client.streamReqs), len(client.reqs))
	}
	if len(sink.events) != 0 {
		t.Fatalf("stream events = %#v, want none before deferred fallback", sink.events)
	}
}

func TestRuntimeCreateStreamUnsupportedToolCombinationRequiresDeferredFallbackBeforeWriting(t *testing.T) {
	client := &fakeChatClient{streamResp: finalChatResponse("should not stream")}
	rt := New(Config{DefaultModel: "gpt-test"}, client, NewMemoryStore())
	sink := &fakeResponseStreamSink{}

	_, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input: "use unknown tool",
		Tools: []protocol.Tool{{
			Type: "unknown_tool",
		}},
	}, sink)
	if !errors.Is(err, ErrIncrementalStreamUnsupported) {
		t.Fatalf("CreateStream() error = %v, want ErrIncrementalStreamUnsupported", err)
	}
	if !strings.Contains(err.Error(), "tool combination requires deferred stream") {
		t.Fatalf("CreateStream() error = %q, want tool fallback reason", err.Error())
	}
	if len(client.streamReqs) != 0 || len(client.reqs) != 0 {
		t.Fatalf("chat requests = stream:%d nonstream:%d, want none before deferred fallback", len(client.streamReqs), len(client.reqs))
	}
	if len(sink.events) != 0 {
		t.Fatalf("stream events = %#v, want none before deferred fallback", sink.events)
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

func TestRuntimeFunctionToolExecutorRegistryReplaceAffectsNextCreate(t *testing.T) {
	client := &fakeChatClient{
		resps: []ChatCompletionResponse{
			functionToolChatResponse("call_lookup_1", "lookup"),
			finalChatResponse("first complete"),
			functionToolChatResponse("call_lookup_2", "lookup"),
			finalChatResponse("second complete"),
		},
	}
	rt := New(
		Config{DefaultModel: "gpt-test"},
		client,
		NewMemoryStore(),
		WithFunctionToolExecutorRegistry(NewFunctionToolExecutorRegistry(map[string]FunctionToolExecutorRegistration{
			"lookup": {Executor: StaticFunctionToolExecutor{Output: "old"}},
		})),
	)

	if _, err := rt.Create(context.Background(), functionToolCreateRequest("first")); err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	rt.ReplaceFunctionToolExecutorRegistry(NewFunctionToolExecutorRegistry(map[string]FunctionToolExecutorRegistration{
		"lookup": {Executor: StaticFunctionToolExecutor{Output: "new"}},
	}))
	if _, err := rt.Create(context.Background(), functionToolCreateRequest("second")); err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}

	if len(client.reqs) != 4 {
		t.Fatalf("chat calls = %d, want 4", len(client.reqs))
	}
	if got := client.reqs[1].Messages[2].Content; got != "old" {
		t.Fatalf("first tool output = %q, want old", got)
	}
	if got := client.reqs[3].Messages[2].Content; got != "new" {
		t.Fatalf("second tool output = %q, want new", got)
	}
}

func TestRuntimeFunctionToolExecutorRegistrySnapshotPreservesInFlightCreate(t *testing.T) {
	client := newBlockingFirstToolCallClient()
	rt := New(
		Config{DefaultModel: "gpt-test"},
		client,
		NewMemoryStore(),
		WithFunctionToolExecutor("lookup", StaticFunctionToolExecutor{Output: "old"}),
	)
	errCh := make(chan error, 1)
	go func() {
		_, err := rt.Create(context.Background(), functionToolCreateRequest("lookup"))
		errCh <- err
	}()

	<-client.started
	rt.ReplaceFunctionToolExecutorRegistry(NewFunctionToolExecutorRegistry(map[string]FunctionToolExecutorRegistration{
		"lookup": {Executor: StaticFunctionToolExecutor{Output: "new"}},
	}))
	close(client.release)

	if err := <-errCh; err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	reqs := client.requests()
	if len(reqs) != 2 {
		t.Fatalf("chat calls = %d, want 2", len(reqs))
	}
	if got := reqs[1].Messages[2].Content; got != "old" {
		t.Fatalf("in-flight tool output = %q, want old snapshot", got)
	}
}

func TestRuntimeCreateExecutesRegisteredFunctionTool(t *testing.T) {
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
					Message:      ChatMessage{Content: "The server-side lookup result is ready."},
					FinishReason: "stop",
				}},
				Usage: ChatUsage{PromptTokens: 13, CompletionTokens: 7, TotalTokens: 20},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	var executed []FunctionToolCall
	rt := New(
		Config{DefaultModel: "gpt-test"},
		client,
		NewMemoryStore(),
		WithExecutionEventRecorder(events),
		WithFunctionToolExecutor("lookup", FunctionToolExecutorFunc(func(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error) {
			executed = append(executed, call)
			return FunctionToolResult{Output: map[string]any{"ok": true, "value": "42"}}, nil
		})),
	)

	resp, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "lookup codex",
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if len(executed) != 1 || executed[0].CallID != "call_lookup" || executed[0].Name != "lookup" || executed[0].Arguments != `{"q":"codex"}` {
		t.Fatalf("executed calls = %#v, want lookup call", executed)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("chat calls = %d, want tool call + final call", len(client.reqs))
	}
	secondMessages := client.reqs[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second messages len = %d, want 3: %#v", len(secondMessages), secondMessages)
	}
	if secondMessages[1].Role != "assistant" || len(secondMessages[1].ToolCalls) != 1 || secondMessages[1].ToolCalls[0].ID != "call_lookup" {
		t.Fatalf("assistant tool call message mismatch: %#v", secondMessages[1])
	}
	if secondMessages[2].Role != "tool" || secondMessages[2].ToolCallID != "call_lookup" || secondMessages[2].Content != `{"ok":true,"value":"42"}` {
		t.Fatalf("server-side tool output message mismatch: %#v", secondMessages[2])
	}
	if resp.Usage != (protocol.Usage{InputTokens: 21, OutputTokens: 10, TotalTokens: 31}) {
		t.Fatalf("usage mismatch: %#v", resp.Usage)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("output len = %d, want function_call_output + final message: %#v", len(resp.Output), resp.Output)
	}
	if got := resp.Output[0]; got.Type != "function_call_output" || got.Status != "completed" || got.CallID != "call_lookup" || got.Name != "lookup" {
		t.Fatalf("unexpected function_call_output: %#v", got)
	}
	if got := resp.Output[1]; got.Type != "message" || got.Content[0].Text != "The server-side lookup result is ready." {
		t.Fatalf("unexpected final message: %#v", got)
	}
	if len(events.events) != 2 {
		t.Fatalf("execution events len = %d, want started/completed: %#v", len(events.events), events.events)
	}
	if events.events[0].Status != "started" || events.events[0].DetailsJSON["tool_name"] != "lookup" || events.events[0].DetailsJSON["call_id"] != "call_lookup" {
		t.Fatalf("started event mismatch: %#v", events.events[0])
	}
	if events.events[1].Status != "completed" || events.events[1].DetailsJSON["tool_name"] != "lookup" || events.events[1].DetailsJSON["call_id"] != "call_lookup" {
		t.Fatalf("completed event mismatch: %#v", events.events[1])
	}
}

func TestRuntimeCreateRecordsConfiguredFunctionToolCallAudits(t *testing.T) {
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
								Arguments: `{"secret":"argument-token"}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Content: "done"},
					FinishReason: "stop",
				}},
			},
		},
	}
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(
		Config{DefaultModel: "gpt-test"},
		client,
		NewMemoryStore(),
		WithToolCallAuditRecorder(toolAudits),
		WithFunctionToolExecutorPolicy("lookup", StaticFunctionToolExecutor{Output: "secret output token"}, FunctionToolExecutorPolicy{
			RedactArguments: true,
			RedactOutput:    true,
		}),
	)

	ctx := audit.ContextWithRequestAuditID(context.Background(), "audit_function")
	resp, err := rt.Create(ctx, protocol.CreateResponseRequest{
		Input: "lookup codex",
		Metadata: map[string]any{
			"codex": map[string]any{"thread_id": "thread_function"},
		},
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if len(toolAudits.entries) != 2 {
		t.Fatalf("tool audits len = %d, want started/completed: %#v", len(toolAudits.entries), toolAudits.entries)
	}
	started, completed := toolAudits.entries[0], toolAudits.entries[1]
	if started.Status != "started" || completed.Status != "completed" {
		t.Fatalf("audit statuses = %q/%q, want started/completed", started.Status, completed.Status)
	}
	if completed.ResponseID != resp.ID || completed.ConversationID != "thread_function" || completed.CallID != "call_lookup" {
		t.Fatalf("completed audit identity = %#v, want response/thread/call", completed)
	}
	if completed.ToolType != "function" || completed.ToolName != "lookup" || completed.Executor != "function_executor:lookup" || completed.Phase != "tool_call" {
		t.Fatalf("completed audit tool fields = %#v", completed)
	}
	if completed.InputJSON["argument_bytes"] != len([]byte(`{"secret":"argument-token"}`)) || completed.InputJSON["arguments_redacted"] != true {
		t.Fatalf("completed audit input summary = %#v", completed.InputJSON)
	}
	if completed.OutputJSON["output_chars"] != 0 || completed.OutputJSON["output_redacted"] != true {
		t.Fatalf("completed audit output summary = %#v", completed.OutputJSON)
	}
	if completed.MetadataJSON["iteration"] != 1 || completed.MetadataJSON["stream"] != false {
		t.Fatalf("completed audit metadata = %#v", completed.MetadataJSON)
	}
	if completed.StartedAt.IsZero() || completed.CompletedAt.IsZero() || completed.CompletedAt.Before(completed.StartedAt) {
		t.Fatalf("completed audit timestamps = started %v completed %v", completed.StartedAt, completed.CompletedAt)
	}
	if len(toolAudits.requestAuditIDSeen) != 2 || toolAudits.requestAuditIDSeen[0] != "audit_function" || toolAudits.requestAuditIDSeen[1] != "audit_function" {
		t.Fatalf("request audit ids seen = %#v", toolAudits.requestAuditIDSeen)
	}
	data := toJSONForTest(t, toolAudits.entries)
	if strings.Contains(data, "argument-token") || strings.Contains(data, "secret output token") {
		t.Fatalf("tool call audit leaked raw arguments/output: %s", data)
	}
}

func TestRuntimeFunctionToolExecutorPolicyRedactsAndLimitsResult(t *testing.T) {
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
								Arguments: `{"secret":"value"}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: ChatUsage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
			},
		},
	}
	events := &fakeExecutionEventRecorder{}
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(
		Config{DefaultModel: "gpt-test"},
		client,
		NewMemoryStore(),
		WithExecutionEventRecorder(events),
		WithToolCallAuditRecorder(toolAudits),
		WithFunctionToolExecutorPolicy("lookup", StaticFunctionToolExecutor{Output: "too long"}, FunctionToolExecutorPolicy{
			Timeout:         time.Second,
			MaxResultBytes:  3,
			RedactArguments: true,
		}),
	)

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input: "lookup codex",
		Tools: []protocol.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
	})
	if err == nil {
		t.Fatalf("Create returned nil error, want result too large")
	}
	if _, ok := err.(FunctionToolResultTooLargeError); !ok {
		t.Fatalf("Create error = %T %v, want FunctionToolResultTooLargeError", err, err)
	}
	if len(events.events) != 2 {
		t.Fatalf("execution events len = %d, want started/failed: %#v", len(events.events), events.events)
	}
	if got := events.events[0].DetailsJSON["arguments"]; got != "<redacted>" {
		t.Fatalf("started event arguments = %q, want redacted", got)
	}
	if events.events[1].Status != "failed" || events.events[1].DetailsJSON["result_bytes"] != 8 {
		t.Fatalf("failed event mismatch: %#v", events.events[1])
	}
	if len(toolAudits.entries) != 2 || toolAudits.entries[0].Status != "started" || toolAudits.entries[1].Status != "failed" {
		t.Fatalf("tool audits = %#v, want started/failed", toolAudits.entries)
	}
	if toolAudits.entries[1].ErrorText == "" || toolAudits.entries[1].OutputJSON["result_bytes"] != 8 {
		t.Fatalf("failed tool audit = %#v", toolAudits.entries[1])
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

func TestRuntimeCreateRejectsForcedUnsupportedHostedTool(t *testing.T) {
	client := &fakeChatClient{resp: finalChatResponse("should not be called")}
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(Config{DefaultModel: "gpt-test"}, client, NewMemoryStore(), WithToolCallAuditRecorder(toolAudits))

	_, err := rt.Create(context.Background(), protocol.CreateResponseRequest{
		Input:      "use workspace",
		Tools:      []protocol.Tool{{Type: "mcp", ServerLabel: "workspace"}},
		ToolChoice: map[string]any{"type": "mcp"},
		Metadata: map[string]any{
			"codex": map[string]any{"thread_id": "thread_non_stream"},
		},
	})
	if err == nil {
		t.Fatal("Create returned nil error, want unsupported hosted tool")
	}
	var unsupported UnsupportedHostedToolError
	if !errors.As(err, &unsupported) || unsupported.Tool != "mcp" {
		t.Fatalf("Create error = %T %v, want UnsupportedHostedToolError for mcp", err, err)
	}
	if len(client.reqs) != 0 {
		t.Fatalf("chat requests = %d, want 0 for rejected hosted tool", len(client.reqs))
	}
	if len(toolAudits.entries) != 1 {
		t.Fatalf("tool audits = %#v, want one rejected hosted tool audit", toolAudits.entries)
	}
	entry := toolAudits.entries[0]
	if entry.ResponseID != "" || entry.ConversationID != "thread_non_stream" {
		t.Fatalf("tool audit identity = %#v, want empty response and conversation", entry)
	}
	if entry.ToolType != "hosted" || entry.ToolName != "mcp" || entry.Executor != "hosted:mcp" {
		t.Fatalf("tool audit tool fields = %#v, want hosted mcp", entry)
	}
	if entry.Status != "rejected" || entry.Phase != "tool_call" || entry.ErrorText != err.Error() {
		t.Fatalf("tool audit status/error = %#v, want rejected tool_call with unsupported error", entry)
	}
	if entry.CreatedAt.IsZero() {
		t.Fatalf("tool audit CreatedAt is zero: %#v", entry)
	}
	if entry.MetadataJSON["stream"] != false || entry.MetadataJSON["reason"] != unsupported.Reason || entry.MetadataJSON["forced_tool_choice"] != true {
		t.Fatalf("tool audit metadata = %#v, want stream/reason/forced_tool_choice", entry.MetadataJSON)
	}
	if len(entry.InputJSON) != 0 || len(entry.OutputJSON) != 0 {
		t.Fatalf("tool audit leaked payload summaries: input=%#v output=%#v", entry.InputJSON, entry.OutputJSON)
	}
}

func TestRuntimeCreateStreamRejectsForcedUnsupportedHostedTool(t *testing.T) {
	client := &fakeChatClient{streamResp: finalChatResponse("should not be called")}
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(Config{DefaultModel: "gpt-test"}, client, NewMemoryStore(), WithToolCallAuditRecorder(toolAudits))

	_, err := rt.CreateStream(context.Background(), protocol.CreateResponseRequest{
		Input:      "search files",
		Tools:      []protocol.Tool{{Type: "file_search"}},
		ToolChoice: map[string]any{"type": "file_search"},
		Metadata: map[string]any{
			"_gateway": map[string]any{
				"codex": map[string]any{"thread_id": "thread_stream"},
			},
		},
	}, &fakeResponseStreamSink{})
	if err == nil {
		t.Fatal("CreateStream returned nil error, want unsupported hosted tool")
	}
	var unsupported UnsupportedHostedToolError
	if !errors.As(err, &unsupported) || unsupported.Tool != "file_search" {
		t.Fatalf("CreateStream error = %T %v, want UnsupportedHostedToolError for file_search", err, err)
	}
	if len(client.streamReqs) != 0 {
		t.Fatalf("stream chat requests = %d, want 0 for rejected hosted tool", len(client.streamReqs))
	}
	if len(toolAudits.entries) != 1 {
		t.Fatalf("stream tool audits = %#v, want one rejected hosted tool audit", toolAudits.entries)
	}
	entry := toolAudits.entries[0]
	if entry.ResponseID != "" || entry.ConversationID != "thread_stream" {
		t.Fatalf("stream tool audit identity = %#v, want empty response and conversation", entry)
	}
	if entry.ToolType != "hosted" || entry.ToolName != "file_search" || entry.Executor != "hosted:file_search" {
		t.Fatalf("stream tool audit tool fields = %#v, want hosted file_search", entry)
	}
	if entry.Status != "rejected" || entry.Phase != "tool_call" || entry.ErrorText != err.Error() {
		t.Fatalf("stream tool audit status/error = %#v, want rejected tool_call with unsupported error", entry)
	}
	if entry.CreatedAt.IsZero() {
		t.Fatalf("stream tool audit CreatedAt is zero: %#v", entry)
	}
	if entry.MetadataJSON["stream"] != true || entry.MetadataJSON["reason"] != unsupported.Reason || entry.MetadataJSON["forced_tool_choice"] != true {
		t.Fatalf("stream tool audit metadata = %#v, want stream/reason/forced_tool_choice", entry.MetadataJSON)
	}
	if len(entry.InputJSON) != 0 || len(entry.OutputJSON) != 0 {
		t.Fatalf("stream tool audit leaked payload summaries: input=%#v output=%#v", entry.InputJSON, entry.OutputJSON)
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

func TestRuntimeCreateRecordsHostedWebSearchToolCallAudits(t *testing.T) {
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
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Content: "Use cassettes."},
					FinishReason: "stop",
				}},
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
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(Config{
		DefaultModel:        "gpt-test",
		WebSearchEnabled:    true,
		WebSearchMaxResults: 2,
	}, client, NewMemoryStore(), WithWebSearchProvider(provider), WithToolCallAuditRecorder(toolAudits))

	ctx := audit.ContextWithRequestAuditID(context.Background(), "audit_search")
	resp, err := rt.Create(ctx, protocol.CreateResponseRequest{
		Input: "search it",
		Metadata: map[string]any{
			"codex": map[string]any{"thread_id": "thread_search"},
		},
		Tools: []protocol.Tool{{Type: "web_search_preview"}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if len(toolAudits.entries) != 2 {
		t.Fatalf("tool audits len = %d, want started/completed: %#v", len(toolAudits.entries), toolAudits.entries)
	}
	started, completed := toolAudits.entries[0], toolAudits.entries[1]
	if started.Status != "started" || completed.Status != "completed" {
		t.Fatalf("audit statuses = %q/%q, want started/completed", started.Status, completed.Status)
	}
	if completed.ResponseID != resp.ID || completed.ConversationID != "thread_search" || completed.CallID != "call_search" {
		t.Fatalf("completed audit identity = %#v, want response/thread/call", completed)
	}
	if completed.ToolType != "hosted" || completed.ToolName != "web_search" || completed.Executor != "hosted:web_search" || completed.Phase != "tool_call" {
		t.Fatalf("completed audit tool fields = %#v", completed)
	}
	if completed.InputJSON["query_chars"] != len("llm trace replay") || completed.InputJSON["max_results"] != 2 {
		t.Fatalf("completed audit input summary = %#v", completed.InputJSON)
	}
	if completed.OutputJSON["result_count"] != 1 {
		t.Fatalf("completed audit output summary = %#v", completed.OutputJSON)
	}
	if completed.MetadataJSON["iteration"] != 1 || completed.MetadataJSON["stream"] != false {
		t.Fatalf("completed audit metadata = %#v", completed.MetadataJSON)
	}
	if completed.StartedAt.IsZero() || completed.CompletedAt.IsZero() || completed.CompletedAt.Before(completed.StartedAt) {
		t.Fatalf("completed audit timestamps = started %v completed %v", completed.StartedAt, completed.CompletedAt)
	}
	if len(toolAudits.requestAuditIDSeen) != 2 || toolAudits.requestAuditIDSeen[0] != "audit_search" || toolAudits.requestAuditIDSeen[1] != "audit_search" {
		t.Fatalf("request audit ids seen = %#v", toolAudits.requestAuditIDSeen)
	}
	data := toJSONForTest(t, toolAudits.entries)
	if strings.Contains(data, "llm trace replay") || strings.Contains(data, "TraceLab docs") || strings.Contains(data, "Record and replay") {
		t.Fatalf("tool call audit leaked raw query/output: %s", data)
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
	toolAudits := &fakeToolCallAuditRecorder{}
	rt := New(Config{
		DefaultModel:     "gpt-test",
		WebSearchEnabled: true,
	}, client, NewMemoryStore(), WithWebSearchProvider(provider), WithExecutionEventRecorder(events), WithToolCallAuditRecorder(toolAudits))

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
	if len(toolAudits.entries) != 2 || toolAudits.entries[0].Status != "started" || toolAudits.entries[1].Status != "failed" {
		t.Fatalf("tool audits = %#v, want started/failed", toolAudits.entries)
	}
	if toolAudits.entries[1].ErrorText != "search backend failed" || toolAudits.entries[1].OutputJSON["result_count"] != nil {
		t.Fatalf("failed tool audit = %#v", toolAudits.entries[1])
	}
}

func findExecutionEvent(events []audit.ExecutionEvent, eventType string, status string) *audit.ExecutionEvent {
	for i := range events {
		if events[i].EventType == eventType && events[i].Status == status {
			return &events[i]
		}
	}
	return nil
}

type compactV2ProvenanceWant struct {
	sourceResponseID      string
	compactResponseID     string
	trigger               string
	triggerReason         string
	sourceItemCount       int
	sourceInputItemCount  int
	sourceOutputItemCount int
	retainedItemCount     int
	summaryItemID         string
}

func assertCompactV2Provenance(t *testing.T, metadata map[string]any, want compactV2ProvenanceWant) map[string]any {
	t.Helper()
	gateway, ok := stringAnyMap(metadata["_gateway"])
	if !ok {
		t.Fatalf("metadata missing _gateway: %#v", metadata)
	}
	compact, ok := stringAnyMap(gateway["compact"])
	if !ok {
		t.Fatalf("metadata missing _gateway.compact: %#v", metadata)
	}
	if compact["version"] != "compact_v2_provenance_first_cut" ||
		compact["source_response_id"] != want.sourceResponseID ||
		compact["compact_response_id"] != want.compactResponseID ||
		compact["trigger"] != want.trigger ||
		compact["source_item_count"] != want.sourceItemCount ||
		compact["source_input_item_count"] != want.sourceInputItemCount ||
		compact["source_output_item_count"] != want.sourceOutputItemCount ||
		compact["retained_item_count"] != want.retainedItemCount ||
		compact["provenance_redaction_policy"] != "ids_types_status_only" {
		t.Fatalf("compact provenance = %#v, want %#v", compact, want)
	}
	if want.triggerReason != "" && compact["trigger_reason"] != want.triggerReason {
		t.Fatalf("compact trigger reason = %#v, want %q", compact["trigger_reason"], want.triggerReason)
	}
	summaryIDs, ok := compact["summary_item_ids"].([]string)
	if !ok || len(summaryIDs) != 1 || summaryIDs[0] != want.summaryItemID {
		t.Fatalf("summary_item_ids = %#v, want %q", compact["summary_item_ids"], want.summaryItemID)
	}
	sourceRefs, ok := compact["source_item_refs"].([]map[string]any)
	if !ok || len(sourceRefs) != want.sourceItemCount {
		t.Fatalf("source_item_refs = %#v, want len %d", compact["source_item_refs"], want.sourceItemCount)
	}
	retainedRefs, ok := compact["retained_item_refs"].([]map[string]any)
	if !ok || len(retainedRefs) != want.retainedItemCount {
		t.Fatalf("retained_item_refs = %#v, want len %d", compact["retained_item_refs"], want.retainedItemCount)
	}
	for _, ref := range append(sourceRefs, retainedRefs...) {
		if _, ok := ref["content"]; ok {
			t.Fatalf("compact provenance leaked content in ref: %#v", ref)
		}
		if _, ok := ref["arguments"]; ok {
			t.Fatalf("compact provenance leaked arguments in ref: %#v", ref)
		}
		if _, ok := ref["output"]; ok {
			t.Fatalf("compact provenance leaked output in ref: %#v", ref)
		}
	}
	return compact
}

type autoCompactProvenanceWant struct {
	trigger                    string
	originalInputItemCount     int
	retainedInputItemCount     int
	droppedInputItemCount      int
	retainedWindowStart        int
	retainedWindowEnd          int
	historyItemThresholdSource string
	contextWindowLimitSource   string
	reservedOutputLimitSource  string
}

func assertAutoCompactProvenance(t *testing.T, event *audit.ExecutionEvent, want autoCompactProvenanceWant) {
	t.Helper()
	if event.DetailsJSON["trigger"] != want.trigger ||
		event.DetailsJSON["previous_response_id_present"] != true ||
		event.DetailsJSON["compact_response_id_present"] != true ||
		event.DetailsJSON["original_input_item_count"] != want.originalInputItemCount ||
		event.DetailsJSON["retained_input_item_count"] != want.retainedInputItemCount ||
		event.DetailsJSON["dropped_input_item_count"] != want.droppedInputItemCount ||
		event.DetailsJSON["retained_window_start"] != want.retainedWindowStart ||
		event.DetailsJSON["retained_window_end"] != want.retainedWindowEnd ||
		event.DetailsJSON["history_item_threshold_source"] != want.historyItemThresholdSource ||
		event.DetailsJSON["context_window_limit_source"] != want.contextWindowLimitSource ||
		event.DetailsJSON["reserved_output_limit_source"] != want.reservedOutputLimitSource {
		t.Fatalf("auto_triggered provenance = %#v, want %#v", event.DetailsJSON, want)
	}
	if event.DetailsJSON["target_response_id"] == "" || event.DetailsJSON["compact_response_id"] == "" {
		t.Fatalf("auto_triggered provenance missing compact ids: %#v", event.DetailsJSON)
	}
	if event.DetailsJSON["metadata_compact_provenance"] != true ||
		event.DetailsJSON["metadata_compact_provenance_id"] != event.DetailsJSON["compact_response_id"] ||
		event.DetailsJSON["provenance_version"] != "compact_v2_provenance_first_cut" {
		t.Fatalf("auto_triggered event missing metadata provenance reference: %#v", event.DetailsJSON)
	}
}

func seedResponseForAutoCompactTest(t *testing.T, store Store, id string, model string) {
	t.Helper()
	target := protocol.Response{
		ID:        id,
		Object:    "response",
		Status:    "completed",
		Model:     model,
		CreatedAt: 100,
		Output: []protocol.OutputItem{{
			ID:      "msg_" + id,
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "old answer"}},
		}},
	}
	inputs := []protocol.InputItem{messageInput("in_"+id, "old question")}
	if err := store.Put(context.Background(), target, protocol.CreateResponseRequest{Input: "old question"}, inputs, target.Output); err != nil {
		t.Fatalf("seed target response: %v", err)
	}
}
