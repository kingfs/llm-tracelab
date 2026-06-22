package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPTokenizeChatPromptTokenCounterPostsStablePromptEnvelope(t *testing.T) {
	const secret = "sk-test-secret"
	var gotPath string
	var gotAuth string
	var gotTrace string
	var gotBody tokenizeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotTrace = r.Header.Get("X-Trace-ID")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":123}`))
	}))
	defer server.Close()
	counter, err := NewHTTPTokenizeChatPromptTokenCounter(HTTPTokenizeChatPromptTokenCounterConfig{
		BaseURL: server.URL + "/v1",
		Model:   "configured-model",
		APIKey:  secret,
		Headers: http.Header{"X-Trace-ID": []string{"trace-1"}},
		Timeout: time.Second,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPTokenizeChatPromptTokenCounter() error = %v", err)
	}

	tokens, err := counter.CountChatPromptTokens(ChatPromptTokenCountRequest{
		Model: "request-model",
		Messages: []ChatMessage{
			{Role: "system", Content: "system rule"},
			{Role: "user", Content: []map[string]string{{"type": "text", "text": "hello"}}},
		},
		Tools: []ChatTool{{
			Type: "function",
			Function: ChatFunction{
				Name:        "lookup",
				Description: "find data",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}},
	})
	if err != nil {
		t.Fatalf("CountChatPromptTokens() error = %v", err)
	}
	if tokens != 123 {
		t.Fatalf("CountChatPromptTokens() = %d, want 123", tokens)
	}
	if gotPath != "/tokenize" {
		t.Fatalf("request path = %q, want /tokenize", gotPath)
	}
	if gotAuth != "Bearer "+secret {
		t.Fatalf("authorization header = %q, want bearer secret", gotAuth)
	}
	if gotTrace != "trace-1" {
		t.Fatalf("x-trace-id header = %q, want trace-1", gotTrace)
	}
	if gotBody.Model != "configured-model" {
		t.Fatalf("request model = %q, want configured-model", gotBody.Model)
	}
	var envelope chatPromptTokenizeEnvelope
	if err := json.Unmarshal([]byte(gotBody.Prompt), &envelope); err != nil {
		t.Fatalf("decode prompt envelope: %v; prompt=%s", err, gotBody.Prompt)
	}
	if len(envelope.Messages) != 2 || envelope.Messages[0].Role != "system" || envelope.Messages[1].Role != "user" {
		t.Fatalf("prompt messages = %#v, want system and user messages", envelope.Messages)
	}
	if len(envelope.Tools) != 1 || envelope.Tools[0].Function.Name != "lookup" {
		t.Fatalf("prompt tools = %#v, want lookup tool", envelope.Tools)
	}
	if envelope.ToolChoice == nil {
		t.Fatalf("prompt tool_choice is nil, want configured tool choice")
	}
}

func TestHTTPTokenizeChatPromptTokenCounterUsesRequestModelWhenConfigModelEmpty(t *testing.T) {
	var gotBody tokenizeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"token_count":7}`))
	}))
	defer server.Close()
	counter, err := NewHTTPTokenizeChatPromptTokenCounter(HTTPTokenizeChatPromptTokenCounterConfig{
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPTokenizeChatPromptTokenCounter() error = %v", err)
	}

	tokens, err := counter.CountChatPromptTokens(ChatPromptTokenCountRequest{Model: "request-model"})
	if err != nil {
		t.Fatalf("CountChatPromptTokens() error = %v", err)
	}
	if tokens != 7 {
		t.Fatalf("CountChatPromptTokens() = %d, want 7", tokens)
	}
	if gotBody.Model != "request-model" {
		t.Fatalf("request model = %q, want request-model", gotBody.Model)
	}
}

func TestParseTokenizeTokenCountResponseShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "count", body: `{"count":123}`, want: 123},
		{name: "token_count", body: `{"token_count":456}`, want: 456},
		{name: "tokens", body: `{"tokens":["a","b","c"]}`, want: 3},
		{name: "token_ids", body: `{"token_ids":[1,2,3,4]}`, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTokenizeTokenCount([]byte(tt.body))
			if err != nil {
				t.Fatalf("parseTokenizeTokenCount() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseTokenizeTokenCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHTTPTokenizeChatPromptTokenCounterRejectsUnparseableResponseWithoutSecret(t *testing.T) {
	const secret = "sk-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != secret {
			t.Fatalf("x-api-key header = %q, want secret", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":99},"secret":"sk-test-secret"}`))
	}))
	defer server.Close()
	counter, err := NewHTTPTokenizeChatPromptTokenCounter(HTTPTokenizeChatPromptTokenCounterConfig{
		BaseURL:      server.URL,
		APIKey:       secret,
		APIKeyHeader: "X-API-Key",
		Client:       server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPTokenizeChatPromptTokenCounter() error = %v", err)
	}

	_, err = counter.CountChatPromptTokens(ChatPromptTokenCountRequest{})
	if err == nil {
		t.Fatal("CountChatPromptTokens() error = nil, want unparseable response error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error %q contains API key", err)
	}
}

func TestHTTPTokenizeChatPromptTokenCounterStatusErrorDoesNotIncludeSecret(t *testing.T) {
	const secret = "sk-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("authorization header = %q, want bearer secret", got)
		}
		http.Error(w, "bad secret: "+secret, http.StatusUnauthorized)
	}))
	defer server.Close()
	counter, err := NewHTTPTokenizeChatPromptTokenCounter(HTTPTokenizeChatPromptTokenCounterConfig{
		BaseURL: server.URL,
		APIKey:  secret,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPTokenizeChatPromptTokenCounter() error = %v", err)
	}

	_, err = counter.CountChatPromptTokens(ChatPromptTokenCountRequest{})
	if err == nil {
		t.Fatal("CountChatPromptTokens() error = nil, want status error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error %q contains API key", err)
	}
}

func TestNewHTTPTokenizeChatPromptTokenCounterRejectsInvalidBaseURL(t *testing.T) {
	_, err := NewHTTPTokenizeChatPromptTokenCounter(HTTPTokenizeChatPromptTokenCounterConfig{BaseURL: "/v1"})
	if err == nil {
		t.Fatal("NewHTTPTokenizeChatPromptTokenCounter() error = nil, want invalid base URL error")
	}
}
