package chatclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

func TestChatCompletionSuccessSendsRequestAndDecodesResponse(t *testing.T) {
	var gotPath, gotMethod, gotAuth, gotContentType, gotExtraHeader string
	var gotReq runtime.ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotExtraHeader = r.Header.Get("X-TraceLab-Test")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_123",
			"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Headers: http.Header{"X-TraceLab-Test": []string{"extra"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	temp := 0.2
	resp, err := client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{
		Model:       "test-model",
		Messages:    []runtime.ChatMessage{{Role: "user", Content: "hi"}},
		Temperature: &temp,
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotExtraHeader != "extra" {
		t.Fatalf("extra header = %q, want extra", gotExtraHeader)
	}
	if gotReq.Model != "test-model" || len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != "user" || gotReq.Messages[0].Content != "hi" {
		t.Fatalf("request body = %+v, want model and user message", gotReq)
	}
	if gotReq.Temperature == nil || *gotReq.Temperature != temp {
		t.Fatalf("temperature = %v, want %v", gotReq.Temperature, temp)
	}
	if resp.ID != "chatcmpl_123" || resp.Choices[0].Message.Content != "hello" || resp.Usage.TotalTokens != 7 {
		t.Fatalf("response = %+v, want decoded chat completion", resp)
	}
}

func TestChatCompletionHTTPErrorIncludesStatusAndTruncatedBody(t *testing.T) {
	body := strings.Repeat("x", maxErrorBodyBytes+20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, body, http.StatusBadGateway)
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want HTTPError")
	}
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T, want HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("status code = %d, want %d", httpErr.StatusCode, http.StatusBadGateway)
	}
	if !httpErr.BodyTruncated {
		t.Fatal("BodyTruncated = false, want true")
	}
	if len(httpErr.Body) != maxErrorBodyBytes {
		t.Fatalf("body length = %d, want %d", len(httpErr.Body), maxErrorBodyBytes)
	}
}

func TestChatCompletionInvalidJSONReturnsDiagnosticError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":`))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want decode error")
	}
	if !strings.Contains(err.Error(), "decode chat completion response JSON") {
		t.Fatalf("error = %q, want decode diagnostic", err)
	}
}

func TestChatCompletionStreamAggregatesSSEChunks(t *testing.T) {
	var gotReq runtime.ChatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":"stop"}]}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
			`data: [DONE]`,
		}, "\n")))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []runtime.ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	if !gotReq.Stream {
		t.Fatal("request stream = false, want true")
	}
	if resp.ID != "chatcmpl_stream" || resp.Model != "gpt-4o" {
		t.Fatalf("response identity = %q/%q, want stream id/model", resp.ID, resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content != "Hello world" || choice.FinishReason != "stop" {
		t.Fatalf("choice = %+v, want aggregated assistant text", choice)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v, want total_tokens 7", resp.Usage)
	}
}

func TestChatCompletionStreamCallbackReceivesContentDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}, "\n")))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var deltas []string
	resp, err := client.ChatCompletionStream(context.Background(), runtime.ChatCompletionRequest{Model: "gpt-4o"}, func(event runtime.ChatStreamEvent) error {
		if event.ContentDelta != "" {
			deltas = append(deltas, event.ContentDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream() error = %v", err)
	}

	if strings.Join(deltas, "|") != "Hello |world" {
		t.Fatalf("deltas = %#v, want separate stream chunks", deltas)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello world" {
		t.Fatalf("aggregated response = %+v, want full content", resp)
	}
}

func TestChatCompletionStreamAggregatesToolCallDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_tools","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\""}}]},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl_tools","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}, "\n")))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{Model: "gpt-4o", Stream: true})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	if len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("choices = %+v, want one tool call", resp.Choices)
	}
	call := resp.Choices[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Type != "function" || call.Function.Name != "weather" || call.Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("tool call = %+v, want aggregated function call", call)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
}

func TestChatCompletionStreamInvalidJSONReturnsDiagnosticError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {bad json}\n"))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{Model: "test", Stream: true})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want stream decode error")
	}
	if !strings.Contains(err.Error(), "decode chat completion stream chunk JSON") {
		t.Fatalf("error = %q, want stream decode diagnostic", err)
	}
}

func TestChatCompletionTrimsBaseURLTrailingSlash(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":"ok","model":"test","choices":[],"usage":{}}`))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL + "/"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), runtime.ChatCompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
}
