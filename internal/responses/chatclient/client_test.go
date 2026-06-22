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
