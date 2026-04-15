package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestHandlerResponsesUsageEndToEnd(t *testing.T) {
	tests := []struct {
		name                 string
		requestBody          string
		responseContentType  string
		responseBody         string
		wantPromptTokens     int
		wantCompletionTokens int
		wantTotalTokens      int
		wantIsStream         bool
	}{
		{
			name:                "stream_response_completed_event",
			requestBody:         `{"model":"gpt-5.1-codex","stream":true}`,
			responseContentType: "text/event-stream",
			responseBody: "event: response.created\n" +
				"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"event: response.output_text.delta\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
				"event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":7048,\"output_tokens\":28,\"total_tokens\":7076}}}\n\n",
			wantPromptTokens:     7048,
			wantCompletionTokens: 28,
			wantTotalTokens:      7076,
			wantIsStream:         true,
		},
		{
			name:                 "non_stream_top_level_usage",
			requestBody:          `{"model":"gpt-5.1-codex","stream":false}`,
			responseContentType:  "application/json",
			responseBody:         `{"id":"resp_2","object":"response","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			wantPromptTokens:     11,
			wantCompletionTokens: 7,
			wantTotalTokens:      18,
			wantIsStream:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			st, err := store.New(outputDir)
			if err != nil {
				t.Fatalf("store.New() error = %v", err)
			}
			defer st.Close()

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", tt.responseContentType)
				_, _ = io.WriteString(w, tt.responseBody)
			}))
			defer upstream.Close()

			cfg := &config.Config{}
			cfg.Upstream.BaseURL = upstream.URL
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(tt.requestBody))
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")

			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
			}

			recordPath := findRecordedHTTP(t, outputDir)
			parsed, err := waitForRecordedPrelude(recordPath, time.Second)
			if err != nil {
				t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
			}

			got := parsed.Header.Usage
			if got.PromptTokens != tt.wantPromptTokens || got.CompletionTokens != tt.wantCompletionTokens || got.TotalTokens != tt.wantTotalTokens {
				t.Fatalf("recorded usage = %+v, want prompt=%d completion=%d total=%d", got, tt.wantPromptTokens, tt.wantCompletionTokens, tt.wantTotalTokens)
			}
			if parsed.Header.Meta.URL != "/v1/responses" {
				t.Fatalf("recorded URL = %q, want /v1/responses", parsed.Header.Meta.URL)
			}
			if parsed.Header.Layout.IsStream != tt.wantIsStream {
				t.Fatalf("recorded IsStream = %v, want %v", parsed.Header.Layout.IsStream, tt.wantIsStream)
			}
			if tt.wantIsStream && len(parsed.Events) < 3 {
				t.Fatalf("parsed.Events len = %d, want at least 3", len(parsed.Events))
			}
			if tt.wantIsStream {
				foundUsage := false
				for _, event := range parsed.Events {
					if event.Type == "llm.usage" {
						foundUsage = true
						break
					}
				}
				if !foundUsage {
					t.Fatalf("stream recording missing llm.usage event: %+v", parsed.Events)
				}
			}

			entries, err := waitForRecentEntries(st, 1, time.Second)
			if err != nil {
				t.Fatalf("waitForRecentEntries() error = %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("ListRecent() len = %d, want 1", len(entries))
			}
			if entries[0].Header.Usage.TotalTokens != tt.wantTotalTokens {
				t.Fatalf("indexed total tokens = %d, want %d", entries[0].Header.Usage.TotalTokens, tt.wantTotalTokens)
			}
		})
	}
}

func TestHandlerAzurePresetRoutesAndAuths(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAPIVersion string
	var gotAPIKey string

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIVersion = r.URL.Query().Get("api-version")
		gotAPIKey = r.Header.Get("api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_azure","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "azure"
	cfg.Upstream.RoutingProfile = "azure_openai_deployment"
	cfg.Upstream.Deployment = "gpt-4o-mini"
	cfg.Upstream.APIVersion = "2025-03-01-preview"
	cfg.Upstream.ApiKey = "azure-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotPath != "/openai/deployments/gpt-4o-mini/responses" {
		t.Fatalf("upstream path = %q, want /openai/deployments/gpt-4o-mini/responses", gotPath)
	}
	if gotAPIVersion != "2025-03-01-preview" {
		t.Fatalf("api-version = %q, want 2025-03-01-preview", gotAPIVersion)
	}
	if gotAPIKey != "azure-secret" {
		t.Fatalf("api-key = %q, want azure-secret", gotAPIKey)
	}
}

func TestHandlerAnthropicPresetRoutesAndAuths(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAPIKey string
	var gotVersion string
	var gotBeta string

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_123","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "anthropic"
	cfg.Upstream.Headers = map[string]string{
		"anthropic-beta": "tools-2024-04-04",
	}
	cfg.Upstream.ApiKey = "anth-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/messages", bytes.NewBufferString(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"max_tokens":16}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages", gotPath)
	}
	if gotAPIKey != "anth-secret" {
		t.Fatalf("x-api-key = %q, want anth-secret", gotAPIKey)
	}
	if gotVersion != upstream.DefaultAnthropicAPIVersion {
		t.Fatalf("anthropic-version = %q, want %q", gotVersion, upstream.DefaultAnthropicAPIVersion)
	}
	if gotBeta != "tools-2024-04-04" {
		t.Fatalf("anthropic-beta = %q, want tools-2024-04-04", gotBeta)
	}
}

func TestHandlerOpenAICompatiblePresetRoutesAndAuths(t *testing.T) {
	tests := []struct {
		name             string
		baseURLPath      string
		providerPreset   string
		requestPath      string
		requestBody      string
		wantUpstreamPath string
		wantAuthHeader   string
		wantNoAPIKey     bool
	}{
		{
			name:             "openrouter_with_api_v1_prefix",
			baseURLPath:      "/api/v1",
			providerPreset:   "openrouter",
			requestPath:      "/v1/responses",
			requestBody:      `{"model":"openai/gpt-4.1","input":"hello"}`,
			wantUpstreamPath: "/api/v1/responses",
			wantAuthHeader:   "Bearer openrouter-secret",
			wantNoAPIKey:     true,
		},
		{
			name:             "groq_with_openai_v1_prefix",
			baseURLPath:      "/openai/v1",
			providerPreset:   "groq",
			requestPath:      "/v1/chat/completions",
			requestBody:      `{"model":"llama-3.3-70b-versatile","messages":[{"role":"user","content":"hello"}]}`,
			wantUpstreamPath: "/openai/v1/chat/completions",
			wantAuthHeader:   "Bearer groq-secret",
			wantNoAPIKey:     true,
		},
		{
			name:             "github_models_on_azure_host_stays_bearer",
			baseURLPath:      "",
			providerPreset:   "github_models",
			requestPath:      "/v1/responses",
			requestBody:      `{"model":"openai/gpt-4.1","input":"hello"}`,
			wantUpstreamPath: "/v1/responses",
			wantAuthHeader:   "Bearer github-secret",
			wantNoAPIKey:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			st, err := store.New(outputDir)
			if err != nil {
				t.Fatalf("store.New() error = %v", err)
			}
			defer st.Close()

			var gotPath string
			var gotAuthorization string
			var gotAPIKey string

			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuthorization = r.Header.Get("Authorization")
				gotAPIKey = r.Header.Get("api-key")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"resp_compat","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
			}))
			defer upstreamServer.Close()

			cfg := &config.Config{}
			cfg.Upstream.BaseURL = upstreamServer.URL + tt.baseURLPath
			cfg.Upstream.ProviderPreset = tt.providerPreset
			switch tt.providerPreset {
			case "openrouter":
				cfg.Upstream.ApiKey = "openrouter-secret"
			case "groq":
				cfg.Upstream.ApiKey = "groq-secret"
			case "github_models":
				cfg.Upstream.ApiKey = "github-secret"
			}
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			req, err := http.NewRequest(http.MethodPost, proxyServer.URL+tt.requestPath, bytes.NewBufferString(tt.requestBody))
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
			}
			if gotPath != tt.wantUpstreamPath {
				t.Fatalf("upstream path = %q, want %q", gotPath, tt.wantUpstreamPath)
			}
			if gotAuthorization != tt.wantAuthHeader {
				t.Fatalf("Authorization = %q, want %q", gotAuthorization, tt.wantAuthHeader)
			}
			if tt.wantNoAPIKey && gotAPIKey != "" {
				t.Fatalf("api-key = %q, want empty", gotAPIKey)
			}
		})
	}
}

func TestHandlerGoogleGenAIPresetRoutesAndAuths(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAPIKey string

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "google_genai"
	cfg.Upstream.ApiKey = "goog-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1beta/models/gemini-2.5-flash:generateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotPath != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("upstream path = %q, want /v1beta/models/gemini-2.5-flash:generateContent", gotPath)
	}
	if gotAPIKey != "goog-secret" {
		t.Fatalf("x-goog-api-key = %q, want goog-secret", gotAPIKey)
	}
}

func TestHandlerGoogleGenAIStreamAddsAltSSE(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAlt string

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAlt = r.URL.Query().Get("alt")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"}]}}]}\n\n")
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "google_genai"
	cfg.Upstream.ApiKey = "goog-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1beta/models/gemini-2.5-flash:streamGenerateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotPath != "/v1beta/models/gemini-2.5-flash:streamGenerateContent" {
		t.Fatalf("upstream path = %q, want /v1beta/models/gemini-2.5-flash:streamGenerateContent", gotPath)
	}
	if gotAlt != "sse" {
		t.Fatalf("alt = %q, want sse", gotAlt)
	}
}

func waitForRecentEntries(st *store.Store, limit int, timeout time.Duration) ([]store.LogEntry, error) {
	deadline := time.Now().Add(timeout)
	var lastEntries []store.LogEntry
	var lastErr error

	for {
		lastEntries, lastErr = st.ListRecent(limit)
		if lastErr == nil && len(lastEntries) >= limit {
			return lastEntries, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, lastErr
			}
			return lastEntries, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForRecordedPrelude(path string, timeout time.Duration) (*recordfile.ParsedPrelude, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for {
		content, err := os.ReadFile(path)
		if err == nil {
			parsed, parseErr := recordfile.ParsePrelude(content)
			if parseErr == nil {
				return parsed, nil
			}
			lastErr = parseErr
		} else {
			lastErr = err
		}

		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("timed out waiting for parsable recorded prelude")
			}
			return nil, lastErr
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func findRecordedHTTP(t *testing.T, root string) string {
	t.Helper()

	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".http" {
			return nil
		}
		found = path
		return filepath.SkipAll
	})
	if err != nil {
		t.Fatalf("Walk(%q) error = %v", root, err)
	}
	if found == "" {
		t.Fatalf("no recorded .http file found under %q", root)
	}
	return found
}
