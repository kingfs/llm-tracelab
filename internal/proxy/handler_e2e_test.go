package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func boolPtr(v bool) *bool { return &v }

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
			cfg.Upstream.BaseURL = upstream.URL + "/v1"
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
			if parsed.Header.Meta.SelectedUpstreamBaseURL != upstream.URL+"/v1" {
				t.Fatalf("SelectedUpstreamBaseURL = %q, want %q", parsed.Header.Meta.SelectedUpstreamBaseURL, upstream.URL+"/v1")
			}
			if parsed.Header.Meta.SelectedUpstreamID == "" {
				t.Fatalf("SelectedUpstreamID is empty")
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

func TestHandlerResponsesEntrypointAliasRoutesToCanonicalPath(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_alias","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL + "/v1"
	cfg.Upstream.ProviderPreset = "openai"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
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
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", gotPath)
	}
	parsed, err := waitForRecordedPrelude(findRecordedHTTP(t, outputDir), time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude() error = %v", err)
	}
	if parsed.Header.Meta.URL != "/v1/responses" || parsed.Header.Meta.Endpoint != "/v1/responses" {
		t.Fatalf("recorded path endpoint = %q/%q, want /v1/responses", parsed.Header.Meta.URL, parsed.Header.Meta.Endpoint)
	}
}

func TestHandlerVLLMTokenizerEndpointsRouteToTopLevelPaths(t *testing.T) {
	tests := []struct {
		name             string
		proxyPath        string
		wantUpstreamPath string
		providerPreset   string
		requestBody      string
		responseBody     string
	}{
		{
			name:             "tokenize",
			proxyPath:        "/v1/tokenize",
			wantUpstreamPath: "/tokenize",
			providerPreset:   "vllm",
			requestBody:      `{"model":"Qwen/Qwen3-0.6B","prompt":"hello","return_token_strs":true}`,
			responseBody:     `{"tokens":[14990],"token_strs":["hello"],"count":1,"max_model_len":32768}`,
		},
		{
			name:             "tokenize default openai compatible",
			proxyPath:        "/v1/tokenize",
			wantUpstreamPath: "/tokenize",
			requestBody:      `{"model":"Qwen/Qwen3-0.6B","prompt":"hello","return_token_strs":true}`,
			responseBody:     `{"tokens":[14990],"token_strs":["hello"],"count":1,"max_model_len":32768}`,
		},
		{
			name:             "detokenize",
			proxyPath:        "/detokenize",
			wantUpstreamPath: "/detokenize",
			providerPreset:   "vllm",
			requestBody:      `{"model":"Qwen/Qwen3-0.6B","tokens":[14990]}`,
			responseBody:     `{"prompt":"hello"}`,
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
			var gotBody string
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.responseBody)
			}))
			defer upstreamServer.Close()

			cfg := &config.Config{}
			cfg.Upstream.BaseURL = upstreamServer.URL + "/v1"
			cfg.Upstream.ProviderPreset = tt.providerPreset
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			req, err := http.NewRequest(http.MethodPost, proxyServer.URL+tt.proxyPath, bytes.NewBufferString(tt.requestBody))
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("resp.StatusCode = %d, want 200; body=%s", resp.StatusCode, string(respBody))
			}
			if gotPath != tt.wantUpstreamPath {
				t.Fatalf("upstream path = %q, want %q", gotPath, tt.wantUpstreamPath)
			}
			if gotBody != tt.requestBody {
				t.Fatalf("upstream body = %q, want %q", gotBody, tt.requestBody)
			}
			if string(respBody) != tt.responseBody {
				t.Fatalf("response body = %q, want %q", string(respBody), tt.responseBody)
			}

			parsed, err := waitForRecordedPrelude(findRecordedHTTP(t, outputDir), time.Second)
			if err != nil {
				t.Fatalf("waitForRecordedPrelude() error = %v", err)
			}
			if parsed.Header.Meta.Endpoint != tt.wantUpstreamPath {
				t.Fatalf("recorded endpoint = %q, want %q", parsed.Header.Meta.Endpoint, tt.wantUpstreamPath)
			}
			if parsed.Header.Meta.Model != "Qwen/Qwen3-0.6B" {
				t.Fatalf("recorded model = %q, want Qwen/Qwen3-0.6B", parsed.Header.Meta.Model)
			}
		})
	}
}

func TestHandlerSelectionFailureIsRecorded(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "openrouter-fallback",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-4.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://openrouter.ai/api/v1",
					ProviderPreset: "openrouter",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"claude-3-7-sonnet","input":"hello"}`))
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

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("resp.StatusCode = %d, want 502", resp.StatusCode)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.RoutingFailureReason != router.SelectionFailureNoSupportingTarget {
		t.Fatalf("RoutingFailureReason = %q, want %q", parsed.Header.Meta.RoutingFailureReason, router.SelectionFailureNoSupportingTarget)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "" {
		t.Fatalf("SelectedUpstreamID = %q, want empty", parsed.Header.Meta.SelectedUpstreamID)
	}
	if parsed.Header.Meta.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502", parsed.Header.Meta.StatusCode)
	}

	entries, err := waitForRecentEntries(st, 1, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1", len(entries))
	}
	if entries[0].Header.Meta.RoutingFailureReason != router.SelectionFailureNoSupportingTarget {
		t.Fatalf("indexed RoutingFailureReason = %q, want %q", entries[0].Header.Meta.RoutingFailureReason, router.SelectionFailureNoSupportingTarget)
	}
}

func TestHandlerSynthesizesAnthropicCountTokensWhenNoSupportingTarget(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	body := `{"model":"glm-5.1","messages":[{"role":"user","content":[{"type":"text","text":"hello count tokens"}]}]}`
	resp, err := proxyServer.Client().Post(proxyServer.URL+"/v1/messages/count_tokens?beta=true", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("client.Post() error = %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200; body=%s", resp.StatusCode, string(respBody))
	}
	var payload struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body=%s", err, string(respBody))
	}
	if payload.InputTokens <= 0 {
		t.Fatalf("input_tokens = %d, want positive", payload.InputTokens)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.Endpoint != "/v1/messages/count_tokens" {
		t.Fatalf("Endpoint = %q, want /v1/messages/count_tokens", parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Meta.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", parsed.Header.Meta.StatusCode)
	}
	if parsed.Header.Meta.RoutingFailureReason != router.SelectionFailureNoSupportingTarget {
		t.Fatalf("RoutingFailureReason = %q, want %q", parsed.Header.Meta.RoutingFailureReason, router.SelectionFailureNoSupportingTarget)
	}
	if parsed.Header.Usage.PromptTokens != payload.InputTokens || parsed.Header.Usage.TotalTokens != payload.InputTokens {
		t.Fatalf("usage = %+v, want input/total %d", parsed.Header.Usage, payload.InputTokens)
	}
	if !hasRecordEvent(parsed.Events, "count_tokens.synthetic") {
		t.Fatalf("synthetic event missing: %+v", parsed.Events)
	}
}

func TestHandlerSynthesizesAnthropicCountTokensOnUpstream404(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:                 "anthropic-primary",
				Enabled:            boolPtr(true),
				Priority:           100,
				ModelDiscovery:     router.ModelDiscoveryStaticOnly,
				StaticModels:       []string{"glm-5.1"},
				AllowUnknownModels: boolPtr(true),
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL,
					ProviderPreset: "anthropic",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	body := `{"model":"glm-5.1","messages":[{"role":"user","content":[{"type":"text","text":"hello count tokens"}]}]}`
	resp, err := proxyServer.Client().Post(proxyServer.URL+"/v1/messages/count_tokens", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("client.Post() error = %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200; body=%s", resp.StatusCode, string(respBody))
	}
	var payload struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body=%s", err, string(respBody))
	}
	if payload.InputTokens <= 0 {
		t.Fatalf("input_tokens = %d, want positive", payload.InputTokens)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "anthropic-primary" {
		t.Fatalf("SelectedUpstreamID = %q, want anthropic-primary", parsed.Header.Meta.SelectedUpstreamID)
	}
	if parsed.Header.Meta.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", parsed.Header.Meta.StatusCode)
	}
	if parsed.Header.Usage.TotalTokens != payload.InputTokens {
		t.Fatalf("TotalTokens = %d, want %d", parsed.Header.Usage.TotalTokens, payload.InputTokens)
	}
	if !hasRecordEvent(parsed.Events, "count_tokens.synthetic") {
		t.Fatalf("synthetic event missing: %+v", parsed.Events)
	}
}

func TestHandlerAggregatesModelListAcrossUpstreams(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1", "gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "anthropic-secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1", "claude-sonnet-4-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.anthropic.com",
					ProviderPreset: "anthropic",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://proxy.local/v1/models", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200", rec.Code)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body=%s", err, rec.Body.String())
	}
	if len(payload.Data) != 3 {
		t.Fatalf("len(data) = %d, want 3; body=%s", len(payload.Data), rec.Body.String())
	}
	want := []string{"claude-sonnet-4-5", "glm-5.1", "gpt-5"}
	for i := range want {
		if payload.Data[i].ID != want[i] {
			t.Fatalf("data[%d].id = %q, want %q", i, payload.Data[i].ID, want[i])
		}
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.Endpoint != "/v1/models" {
		t.Fatalf("recorded Endpoint = %q, want /v1/models", parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Meta.StatusCode != http.StatusOK {
		t.Fatalf("recorded StatusCode = %d, want 200", parsed.Header.Meta.StatusCode)
	}
}

func TestHandlerAllowStaticFallbackRoutesUnknownModel(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_static","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Fallback.OnMissingModel = "allow_static"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"unknown-future-model","input":"hello"}`))
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
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", gotPath)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "openai-primary" {
		t.Fatalf("SelectedUpstreamID = %q, want openai-primary", parsed.Header.Meta.SelectedUpstreamID)
	}
	if parsed.Header.Meta.RoutingFailureReason != "" {
		t.Fatalf("RoutingFailureReason = %q, want empty", parsed.Header.Meta.RoutingFailureReason)
	}
	seenEvents := map[string]bool{}
	for _, event := range parsed.Events {
		seenEvents[event.Type] = true
	}
	for _, eventType := range []string{"routing.classified", "routing.candidates", "routing.selected", "routing.outcome"} {
		if !seenEvents[eventType] {
			t.Fatalf("recorded events missing %s: %+v", eventType, parsed.Events)
		}
	}
}

func TestHandlerResponsesServerModeRoutesToChatCompletionsUpstream(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAuth string
	var gotChatBody map[string]any
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotChatBody); err != nil {
			t.Errorf("decode upstream request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
		},
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-chat",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ApiKey:         "upstream-secret",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Mode:           "server",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"ping"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("resp.StatusCode = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	var responsePayload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&responsePayload); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if responsePayload["object"] != "response" || responsePayload["status"] != "completed" {
		t.Fatalf("unexpected responses payload: %+v", responsePayload)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer upstream-secret" {
		t.Fatalf("Authorization = %q, want Bearer upstream-secret", gotAuth)
	}
	if gotChatBody["model"] != "gpt-5" {
		t.Fatalf("chat body model = %v, want gpt-5; body=%+v", gotChatBody["model"], gotChatBody)
	}
	messages, ok := gotChatBody["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("chat body messages = %#v, want one user message", gotChatBody["messages"])
	}
	firstMessage, ok := messages[0].(map[string]any)
	if !ok || firstMessage["role"] != "user" || firstMessage["content"] != "ping" {
		t.Fatalf("first chat message = %#v, want user ping", messages[0])
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.URL != "/v1/chat/completions" || parsed.Header.Meta.Endpoint != "/v1/chat/completions" {
		t.Fatalf("recorded path endpoint = %q/%q, want /v1/chat/completions", parsed.Header.Meta.URL, parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Meta.Operation != "chat.completions" {
		t.Fatalf("recorded operation = %q, want chat.completions", parsed.Header.Meta.Operation)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "openai-chat" {
		t.Fatalf("SelectedUpstreamID = %q, want openai-chat", parsed.Header.Meta.SelectedUpstreamID)
	}
	if parsed.Header.Meta.SelectedUpstreamBaseURL != upstreamServer.URL+"/v1" {
		t.Fatalf("SelectedUpstreamBaseURL = %q, want %q", parsed.Header.Meta.SelectedUpstreamBaseURL, upstreamServer.URL+"/v1")
	}
	if parsed.Header.Meta.SelectedUpstreamProviderPreset != "openai" {
		t.Fatalf("SelectedUpstreamProviderPreset = %q, want openai", parsed.Header.Meta.SelectedUpstreamProviderPreset)
	}
	if parsed.Header.Meta.Model != "gpt-5" {
		t.Fatalf("recorded model = %q, want gpt-5", parsed.Header.Meta.Model)
	}
	if parsed.Header.Meta.StatusCode != http.StatusOK {
		t.Fatalf("recorded status = %d, want 200", parsed.Header.Meta.StatusCode)
	}
	if parsed.Header.Layout.IsStream {
		t.Fatalf("recorded IsStream = true, want false")
	}
	if parsed.Header.Usage.PromptTokens != 3 || parsed.Header.Usage.CompletionTokens != 2 || parsed.Header.Usage.TotalTokens != 5 {
		t.Fatalf("recorded usage = %+v, want prompt=3 completion=2 total=5", parsed.Header.Usage)
	}

	entries, err := waitForRecentEntries(st, 1, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1", len(entries))
	}
	if entries[0].Header.Meta.Endpoint != "/v1/chat/completions" {
		t.Fatalf("indexed endpoint = %q, want /v1/chat/completions", entries[0].Header.Meta.Endpoint)
	}
}

func TestHandlerResponsesServerModeContinuationHistoryPersistsAcrossHandlerRestart(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamCalls := 0
	var secondChatBody map[string]any
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var chatBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
			t.Errorf("decode upstream request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if upstreamCalls == 2 {
			secondChatBody = chatBody
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
		},
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-chat",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ApiKey:         "upstream-secret",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Mode:           "server",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"persist me"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create StatusCode = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	var created protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		resp.Body.Close()
		t.Fatalf("decode created response: %v", err)
	}
	resp.Body.Close()
	proxyServer.Close()

	restartedHandler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() after restart error = %v", err)
	}
	restartedServer := httptest.NewServer(restartedHandler)
	defer restartedServer.Close()

	secondReq, err := http.NewRequest(http.MethodPost, restartedServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","previous_response_id":"`+created.ID+`","input":"second"}`))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	secondReq.Header.Set("Content-Type", "application/json")
	secondResp, err := restartedServer.Client().Do(secondReq)
	if err != nil {
		t.Fatalf("second Do() error = %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("second StatusCode = %d, want 200; body=%s", secondResp.StatusCode, string(body))
	}
	if secondChatBody == nil {
		t.Fatalf("second upstream chat body was not captured")
	}
	messages, ok := secondChatBody["messages"].([]any)
	if !ok {
		t.Fatalf("second chat body messages = %#v, want array", secondChatBody["messages"])
	}
	gotRoles := make([]string, 0, len(messages))
	gotContent := make([]string, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("message = %#v, want object", raw)
		}
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		gotRoles = append(gotRoles, role)
		gotContent = append(gotContent, content)
	}
	wantRoles := []string{"user", "assistant", "user"}
	wantContent := []string{"persist me", "pong", "second"}
	if fmt.Sprint(gotRoles) != fmt.Sprint(wantRoles) || fmt.Sprint(gotContent) != fmt.Sprint(wantContent) {
		t.Fatalf("second messages roles=%v content=%v, want roles=%v content=%v", gotRoles, gotContent, wantRoles, wantContent)
	}
	if upstreamCalls != 2 {
		t.Fatalf("upstreamCalls = %d, want create and continuation calls", upstreamCalls)
	}
}

func TestHandlerResponsesServerModeRecordsChatCompletionFailures(t *testing.T) {
	tests := []struct {
		name              string
		firstStatus       int
		firstBody         string
		wantRecordedError string
	}{
		{
			name:              "upstream_non_2xx",
			firstStatus:       http.StatusBadGateway,
			firstBody:         `{"error":{"message":"upstream unavailable"}}`,
			wantRecordedError: "chat completion request failed: status 502",
		},
		{
			name:              "invalid_json",
			firstStatus:       http.StatusOK,
			firstBody:         `{"id":`,
			wantRecordedError: "decode chat completion response JSON",
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

			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.firstStatus)
				_, _ = io.WriteString(w, tt.firstBody)
			}))
			defer upstreamServer.Close()

			cfg := &config.Config{
				ResponsesServer: config.ResponsesServerConfig{
					Enabled: true,
				},
				Upstreams: []config.UpstreamTargetConfig{
					{
						ID:             "openai-chat",
						Enabled:        boolPtr(true),
						Priority:       100,
						ModelDiscovery: router.ModelDiscoveryStaticOnly,
						StaticModels:   []string{"gpt-5"},
						Upstream: config.UpstreamConfig{
							BaseURL:        upstreamServer.URL + "/v1",
							ApiKey:         "upstream-secret",
							ProviderPreset: "openai",
							APIType:        "chat_completions",
							Mode:           "server",
						},
					},
				},
			}
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"fail"}`))
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("first resp.StatusCode = %d, want 500; body=%s", resp.StatusCode, string(body))
			}

			entries, err := waitForRecentEntries(st, 1, time.Second)
			if err != nil {
				t.Fatalf("waitForRecentEntries() error = %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("ListRecent() len after failure = %d, want 1", len(entries))
			}
			parsed, err := waitForRecordedPrelude(entries[0].LogPath, time.Second)
			if err != nil {
				t.Fatalf("waitForRecordedPrelude(%q) error = %v", entries[0].LogPath, err)
			}
			if parsed.Header.Meta.Endpoint != "/v1/chat/completions" {
				t.Fatalf("recorded endpoint = %q, want /v1/chat/completions", parsed.Header.Meta.Endpoint)
			}
			if parsed.Header.Meta.StatusCode != tt.firstStatus {
				t.Fatalf("recorded status = %d, want %d", parsed.Header.Meta.StatusCode, tt.firstStatus)
			}
			if !strings.Contains(parsed.Header.Meta.Error, tt.wantRecordedError) {
				t.Fatalf("recorded error = %q, want contains %q", parsed.Header.Meta.Error, tt.wantRecordedError)
			}

		})
	}
}

func TestHandlerResponsesServerModeDisabledProxiesResponsesPath(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode upstream request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_upstream","object":"response","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-responses",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ApiKey:         "upstream-secret",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"ping"}`))
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
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want /v1/responses", gotPath)
	}
	if gotAuth != "Bearer upstream-secret" {
		t.Fatalf("Authorization = %q, want Bearer upstream-secret", gotAuth)
	}
	if gotBody["input"] != "ping" {
		t.Fatalf("upstream body input = %v, want ping; body=%+v", gotBody["input"], gotBody)
	}

	parsed, err := waitForRecordedPrelude(findRecordedHTTP(t, outputDir), time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude() error = %v", err)
	}
	if parsed.Header.Meta.URL != "/v1/responses" || parsed.Header.Meta.Endpoint != "/v1/responses" {
		t.Fatalf("recorded path endpoint = %q/%q, want /v1/responses", parsed.Header.Meta.URL, parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "openai-responses" {
		t.Fatalf("SelectedUpstreamID = %q, want openai-responses", parsed.Header.Meta.SelectedUpstreamID)
	}
}

func TestHandlerRecordsStickyRoutingEvents(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_primary","usage":{"total_tokens":2}}`)
	}))
	defer upstreamPrimary.Close()

	upstreamSecondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_secondary","usage":{"total_tokens":2}}`)
	}))
	defer upstreamSecondary.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream:       config.UpstreamConfig{BaseURL: upstreamPrimary.URL + "/v1", ProviderPreset: "openai"},
			},
			{
				ID:             "secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream:       config.UpstreamConfig{BaseURL: upstreamSecondary.URL + "/v1", ProviderPreset: "openai"},
			},
		},
	}
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
		if err != nil {
			t.Fatalf("http.NewRequest() error = %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Session_id", "session-e2e")
		resp, err := proxyServer.Client().Do(req)
		if err != nil {
			t.Fatalf("client.Do() error = %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
		}
	}

	entries, err := waitForRecentEntries(st, 2, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	var seenMiss, seenBind, seenHit bool
	for _, entry := range entries {
		parsed, err := waitForRecordedPrelude(entry.LogPath, time.Second)
		if err != nil {
			t.Fatalf("waitForRecordedPrelude(%q) error = %v", entry.LogPath, err)
		}
		for _, event := range parsed.Events {
			if event.Type == "routing.sticky.miss" {
				seenMiss = true
			}
			if event.Type == "routing.sticky.bind" {
				seenBind = true
			}
			if event.Type == "routing.sticky.hit" {
				seenHit = true
			}
			if strings.HasPrefix(event.Type, "routing.sticky.") {
				if _, ok := event.Attributes["sticky_key"]; ok {
					t.Fatalf("sticky event leaked raw sticky_key: %+v", event.Attributes)
				}
				if _, ok := event.Attributes["sticky_key_fingerprint"]; !ok {
					t.Fatalf("sticky event missing sticky_key_fingerprint: %+v", event.Attributes)
				}
			}
		}
	}
	if !seenMiss || !seenBind || !seenHit {
		t.Fatalf("sticky events seen miss=%v bind=%v hit=%v; entries=%+v", seenMiss, seenBind, seenHit, entries)
	}
}

func TestHandlerCassetteMetadataDoesNotLeakCredentialMaterial(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-secret-token" {
			t.Fatalf("upstream Authorization = %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "upstream-api-key" {
			t.Fatalf("upstream X-Api-Key = %q", got)
		}
		if got := r.Header.Get("X-Custom-Auth"); got != "custom-auth-secret" {
			t.Fatalf("upstream X-Custom-Auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","usage":{"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL + "/v1?api_key=query-secret"
	cfg.Upstream.ApiKey = "upstream-secret-token"
	cfg.Upstream.Headers = map[string]string{
		"X-Api-Key":     "upstream-api-key",
		"X-Custom-Auth": "custom-auth-secret",
		"X-OAuth-Token": "oauth-token-secret",
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = false

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses?access_token=client-query-token", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer client-token-raw")

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
	content, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", recordPath, err)
	}
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	payload := content[parsed.PayloadOffset:]
	requestHeaderEnd := bytes.Index(payload, []byte("\r\n\r\n"))
	if requestHeaderEnd < 0 {
		t.Fatalf("record payload missing request header/body separator")
	}
	requestHeaders := string(payload[:requestHeaderEnd])
	prelude := string(content[:parsed.PayloadOffset])
	for _, leaked := range []string{
		"upstream-secret-token",
		"upstream-api-key",
		"custom-auth-secret",
		"oauth-token-secret",
		"query-secret",
	} {
		if strings.Contains(prelude, leaked) {
			t.Fatalf("cassette metadata leaked %q in prelude:\n%s", leaked, prelude)
		}
	}
	if !strings.Contains(requestHeaders, "Authorization: Bearer client-token-raw") {
		t.Fatalf("raw request headers did not preserve client Authorization:\n%s", requestHeaders)
	}
	if strings.Contains(requestHeaders, "upstream-secret-token") || strings.Contains(requestHeaders, "upstream-api-key") || strings.Contains(requestHeaders, "custom-auth-secret") {
		t.Fatalf("raw client request unexpectedly contains upstream credential material:\n%s", requestHeaders)
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

func TestHandlerAnthropicEntrypointAliasRoutesToCanonicalPath(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_alias","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "anthropic"
	cfg.Upstream.ApiKey = "anth-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/anthropic/messages", bytes.NewBufferString(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"max_tokens":16}`))
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
	parsed, err := waitForRecordedPrelude(findRecordedHTTP(t, outputDir), time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude() error = %v", err)
	}
	if parsed.Header.Meta.URL != "/v1/messages" || parsed.Header.Meta.Endpoint != "/v1/messages" {
		t.Fatalf("recorded path endpoint = %q/%q, want /v1/messages", parsed.Header.Meta.URL, parsed.Header.Meta.Endpoint)
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
			baseURLPath:      "/v1",
			providerPreset:   "github_models",
			requestPath:      "/v1/responses",
			requestBody:      `{"model":"openai/gpt-4.1","input":"hello"}`,
			wantUpstreamPath: "/v1/responses",
			wantAuthHeader:   "Bearer github-secret",
			wantNoAPIKey:     true,
		},
		{
			name:             "vllm_tokenize_uses_root_endpoint",
			baseURLPath:      "/v1",
			providerPreset:   "vllm",
			requestPath:      "/tokenize",
			requestBody:      `{"model":"local-model","prompt":"hello","add_special_tokens":false}`,
			wantUpstreamPath: "/tokenize",
			wantNoAPIKey:     true,
		},
		{
			name:             "vllm_tokenize_accepts_v1_prefixed_entrypoint",
			baseURLPath:      "/v1",
			providerPreset:   "vllm",
			requestPath:      "/v1/tokenize",
			requestBody:      `{"model":"local-model","prompt":"hello","add_special_tokens":false}`,
			wantUpstreamPath: "/tokenize",
			wantNoAPIKey:     true,
		},
		{
			name:             "vllm_detokenize_accepts_v1_prefixed_entrypoint",
			baseURLPath:      "/v1",
			providerPreset:   "vllm",
			requestPath:      "/v1/detokenize",
			requestBody:      `{"model":"local-model","tokens":[14556]}`,
			wantUpstreamPath: "/detokenize",
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

func TestHandlerVertexExpressRoutesAuthsAndRecords(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAuthorization string

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello from vertex"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"totalTokenCount":10}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "vertex"
	cfg.Upstream.RoutingProfile = upstream.RoutingProfileVertexExpress
	cfg.Upstream.ModelResource = "publishers/google/models/gemini-2.5-flash"
	cfg.Upstream.ApiKey = "vertex-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/publishers/google/models/gemini-2.5-flash:generateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
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
	if gotPath != "/v1/publishers/google/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("upstream path = %q, want /v1/publishers/google/models/gemini-2.5-flash:generateContent", gotPath)
	}
	if gotAuthorization != "Bearer vertex-secret" {
		t.Fatalf("Authorization = %q, want Bearer vertex-secret", gotAuthorization)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.Provider != upstream.ProtocolFamilyVertexNative {
		t.Fatalf("recorded provider = %q, want %q", parsed.Header.Meta.Provider, upstream.ProtocolFamilyVertexNative)
	}
	if parsed.Header.Meta.Endpoint != "/v1/publishers/models:generateContent" {
		t.Fatalf("recorded endpoint = %q, want /v1/publishers/models:generateContent", parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Usage.TotalTokens != 10 {
		t.Fatalf("recorded total tokens = %d, want 10", parsed.Header.Usage.TotalTokens)
	}
}

func TestHandlerVertexProjectLocationStreamAddsAltSSEAndRecordsEvents(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotPath string
	var gotAlt string
	var gotAuthorization string

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAlt = r.URL.Query().Get("alt")
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello \"}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Vertex\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":7,\"totalTokenCount\":10}}\n\n")
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL
	cfg.Upstream.ProviderPreset = "vertex"
	cfg.Upstream.RoutingProfile = upstream.RoutingProfileVertexProject
	cfg.Upstream.Project = "demo-project"
	cfg.Upstream.Location = "us-central1"
	cfg.Upstream.ModelResource = "publishers/google/models/gemini-2.5-flash"
	cfg.Upstream.ApiKey = "vertex-secret"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	reqPath := "/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent"
	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+reqPath, bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
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
	if gotPath != reqPath {
		t.Fatalf("upstream path = %q, want %q", gotPath, reqPath)
	}
	if gotAlt != "sse" {
		t.Fatalf("alt = %q, want sse", gotAlt)
	}
	if gotAuthorization != "Bearer vertex-secret" {
		t.Fatalf("Authorization = %q, want Bearer vertex-secret", gotAuthorization)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if !parsed.Header.Layout.IsStream {
		t.Fatalf("recorded IsStream = false, want true")
	}
	if parsed.Header.Meta.Provider != upstream.ProtocolFamilyVertexNative {
		t.Fatalf("recorded provider = %q, want %q", parsed.Header.Meta.Provider, upstream.ProtocolFamilyVertexNative)
	}
	if parsed.Header.Meta.Endpoint != "/v1/publishers/models:streamGenerateContent" {
		t.Fatalf("recorded endpoint = %q, want /v1/publishers/models:streamGenerateContent", parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Usage.TotalTokens != 10 {
		t.Fatalf("recorded total tokens = %d, want 10", parsed.Header.Usage.TotalTokens)
	}
	foundOutput := false
	foundUsage := false
	for _, event := range parsed.Events {
		switch event.Type {
		case "llm.output_text.delta":
			foundOutput = true
		case "llm.usage":
			foundUsage = true
		}
	}
	if !foundOutput || !foundUsage {
		t.Fatalf("recorded events missing output/usage: %+v", parsed.Events)
	}

	entries, err := waitForRecentEntries(st, 1, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1", len(entries))
	}
	if entries[0].Header.Meta.Provider != upstream.ProtocolFamilyVertexNative {
		t.Fatalf("indexed provider = %q, want %q", entries[0].Header.Meta.Provider, upstream.ProtocolFamilyVertexNative)
	}
	if entries[0].Header.Usage.TotalTokens != 10 {
		t.Fatalf("indexed total tokens = %d, want 10", entries[0].Header.Usage.TotalTokens)
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

func TestHandlerRetryOnUpstreamModelNotFound(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	// upstream-primary: returns 404 for the requested model.
	var primaryCalled bool
	upstreamPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"model not found","type":"not_found_error"}}`)
	}))
	defer upstreamPrimary.Close()

	// upstream-secondary: returns 200.
	var secondaryCalled bool
	upstreamSecondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_secondary","object":"response","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`)
	}))
	defer upstreamSecondary.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamPrimary.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamSecondary.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	// Use first_available for deterministic selection order.
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
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

	// The handler should have retried on the primary 404 and succeeded on secondary.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200 (should have fallen back to secondary)", resp.StatusCode)
	}
	if !primaryCalled {
		t.Errorf("primary upstream was not called, expected one 404 attempt")
	}
	if !secondaryCalled {
		t.Fatalf("secondary upstream was not called, expected fallback after primary 404")
	}

	// Verify the recording attributes the request to secondary.
	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "secondary" {
		t.Fatalf("SelectedUpstreamID = %q, want secondary (recording should reflect the successful upstream)", parsed.Header.Meta.SelectedUpstreamID)
	}
	if parsed.Header.Meta.RoutingFailureReason != "" {
		t.Fatalf("RoutingFailureReason = %q, want empty", parsed.Header.Meta.RoutingFailureReason)
	}
}

func TestHandlerRetryOnUpstream500(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	// upstream-primary: returns 503.
	var primaryCalled bool
	upstreamPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"service unavailable"}`)
	}))
	defer upstreamPrimary.Close()

	// upstream-secondary: returns 200.
	var secondaryCalled bool
	upstreamSecondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_secondary","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamSecondary.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamPrimary.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamSecondary.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200 (should have fallen back to secondary)", resp.StatusCode)
	}
	if !primaryCalled || !secondaryCalled {
		t.Fatalf("primary=%v secondary=%v, want both called", primaryCalled, secondaryCalled)
	}
}

func TestHandlerBackoffRetriesSingleTransientUpstream(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var calls int
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"temporary overload"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"resp_retry","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Hour
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200 after transient retry", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestHandlerRetryAfterRecordedForTransientRetry(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var calls int
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"temporary overload"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"resp_retry","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	var slept []time.Duration
	oldSleeper := sleepForRetry
	sleepForRetry = func(ctx context.Context, delay time.Duration) bool {
		slept = append(slept, delay)
		return true
	}
	t.Cleanup(func() {
		sleepForRetry = oldSleeper
	})

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Hour
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resp.StatusCode = %d, want 200 after Retry-After retry", resp.StatusCode)
	}
	if len(slept) != 1 || slept[0] < 2*time.Second {
		t.Fatalf("slept = %v, want one delay >= 2s", slept)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	var foundWait bool
	for _, event := range parsed.Events {
		if event.Type != "routing.retry_wait" {
			continue
		}
		foundWait = true
		if got := event.Attributes["delay_ms"]; got == nil || got.(float64) < 2000 {
			t.Fatalf("retry wait delay_ms = %v, want >= 2000", got)
		}
		if got := event.Attributes["status_code"]; got != float64(http.StatusServiceUnavailable) {
			t.Fatalf("retry wait status_code = %v, want %d", got, http.StatusServiceUnavailable)
		}
	}
	if !foundWait {
		t.Fatalf("routing.retry_wait event not found: %+v", parsed.Events)
	}
}

func TestHandlerRetryQueueSaturationReturns503(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	for {
		select {
		case <-upstreamRetryWaitSlots:
		default:
			goto drained
		}
	}

drained:
	for i := 0; i < upstreamRetryWaitCapacity; i++ {
		if !tryAcquireRetryWaitSlot() {
			t.Fatalf("tryAcquireRetryWaitSlot() failed while pre-filling slot %d", i)
		}
	}
	t.Cleanup(func() {
		for i := 0; i < upstreamRetryWaitCapacity; i++ {
			releaseRetryWaitSlot()
		}
	})

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Hour
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	firstReq, _ := http.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	selection, err := handler.router.Select(firstReq)
	if err != nil {
		t.Fatalf("router.Select() error = %v", err)
	}
	handler.router.Complete(selection, router.Outcome{Success: false, StatusCode: 0})

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("resp.StatusCode = %d, want 503", resp.StatusCode)
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	for _, event := range parsed.Events {
		if event.Type == "routing.retry_queue_saturated" {
			return
		}
	}
	t.Fatalf("routing.retry_queue_saturated event not found: %+v", parsed.Events)
}

func TestHandlerLocalConcurrencyLimitRecordsRejectionEvent(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	releaseUpstream := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseUpstream
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL + "/v1"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true
	cfg.Limits.Enabled = true
	cfg.Limits.MaxConcurrent = 1

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	firstDone := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hold"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := proxyServer.Client().Do(req)
		if err != nil {
			firstDone <- err
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			firstDone <- fmt.Errorf("first status = %d, want 200", resp.StatusCode)
			return
		}
		firstDone <- nil
	}()

	for i := 0; i < 100; i++ {
		inflight, _ := handler.limiter.Counts("global")
		if inflight > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"reject"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("resp.StatusCode = %d, want 429", resp.StatusCode)
	}

	close(releaseUpstream)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request error = %v", err)
	}

	entries, err := waitForRecentEntries(st, 2, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	for _, entry := range entries {
		parsed, err := waitForRecordedPrelude(entry.LogPath, time.Second)
		if err != nil {
			t.Fatalf("waitForRecordedPrelude(%q) error = %v", entry.LogPath, err)
		}
		for _, event := range parsed.Events {
			if event.Type == "limit.concurrency_rejected" {
				if got := event.Attributes["http_status"]; got != float64(http.StatusTooManyRequests) {
					t.Fatalf("http_status = %v, want %d", got, http.StatusTooManyRequests)
				}
				if _, ok := event.Attributes["limit_key_fingerprint"]; !ok {
					t.Fatalf("limit event missing limit_key_fingerprint: %+v", event.Attributes)
				}
				return
			}
		}
	}
	t.Fatalf("limit.concurrency_rejected event not found in entries: %+v", entries)
}

func TestHandlerHeaderScopedLimitDoesNotLeakHeaderValue(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	releaseUpstream := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseUpstream
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL + "/v1"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true
	cfg.Limits.Enabled = true
	cfg.Limits.Scope = "header"
	cfg.Limits.ChannelKeyHeader = "X-Limit-Bucket"
	cfg.Limits.MaxConcurrent = 1

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	firstDone := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hold"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Limit-Bucket", "raw-secret-header-value")
		resp, err := proxyServer.Client().Do(req)
		if err != nil {
			firstDone <- err
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		firstDone <- nil
	}()

	for i := 0; i < 100; i++ {
		inflight, _ := handler.limiter.Counts("header:raw-secret-header-value")
		if inflight > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"reject"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Limit-Bucket", "raw-secret-header-value")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("resp.StatusCode = %d, want 429", resp.StatusCode)
	}

	close(releaseUpstream)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request error = %v", err)
	}

	entries, err := waitForRecentEntries(st, 2, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	for _, entry := range entries {
		parsed, err := waitForRecordedPrelude(entry.LogPath, time.Second)
		if err != nil {
			t.Fatalf("waitForRecordedPrelude(%q) error = %v", entry.LogPath, err)
		}
		for _, event := range parsed.Events {
			if event.Type != "limit.concurrency_rejected" {
				continue
			}
			if event.Attributes["scope"] != "header" {
				t.Fatalf("scope = %v, want header", event.Attributes["scope"])
			}
			if strings.Contains(fmt.Sprint(event.Attributes), "raw-secret-header-value") {
				t.Fatalf("limit event leaked raw header value: %+v", event.Attributes)
			}
			return
		}
	}
	t.Fatalf("limit.concurrency_rejected event not found in entries: %+v", entries)
}

func TestHandlerChannelScopedLimitRecordsIdentity(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	releaseUpstream := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseUpstream
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        upstreamServer.URL + "/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true
	cfg.Limits.Enabled = true
	cfg.Limits.Scope = "channel"
	cfg.Limits.MaxConcurrent = 1

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	firstDone := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hold"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := proxyServer.Client().Do(req)
		if err != nil {
			firstDone <- err
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		firstDone <- nil
	}()

	for i := 0; i < 100; i++ {
		inflight, _ := handler.limiter.Counts("channel:openai-primary")
		if inflight > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"reject"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("resp.StatusCode = %d, want 429", resp.StatusCode)
	}

	close(releaseUpstream)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request error = %v", err)
	}

	entries, err := waitForRecentEntries(st, 2, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	for _, entry := range entries {
		parsed, err := waitForRecordedPrelude(entry.LogPath, time.Second)
		if err != nil {
			t.Fatalf("waitForRecordedPrelude(%q) error = %v", entry.LogPath, err)
		}
		for _, event := range parsed.Events {
			if event.Type != "limit.concurrency_rejected" {
				continue
			}
			if event.Attributes["scope"] != "channel" || event.Attributes["channel_id"] != "openai-primary" || event.Attributes["route_target_id"] != "openai-primary:default" || event.Attributes["credential_id"] != "default" {
				t.Fatalf("limit event attrs = %+v, want scoped identity", event.Attributes)
			}
			return
		}
	}
	t.Fatalf("limit.concurrency_rejected event not found in entries: %+v", entries)
}

func TestHandlerLocalQueueSaturationRecordsEvent(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	releaseUpstream := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseUpstream
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","object":"response","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstreamServer.URL + "/v1"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true
	cfg.Limits.Enabled = true
	cfg.Limits.MaxConcurrent = 1
	cfg.Limits.MaxQueued = 1

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	startRequest := func(input string) chan error {
		done := make(chan error, 1)
		go func() {
			req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"`+input+`"}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				done <- err
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			done <- nil
		}()
		return done
	}

	firstDone := startRequest("hold")
	for i := 0; i < 100; i++ {
		inflight, _ := handler.limiter.Counts("global")
		if inflight > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	queuedDone := startRequest("queue")
	for i := 0; i < 100; i++ {
		_, queued := handler.limiter.Counts("global")
		if queued > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"saturate"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("resp.StatusCode = %d, want 503", resp.StatusCode)
	}

	close(releaseUpstream)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request error = %v", err)
	}
	if err := <-queuedDone; err != nil {
		t.Fatalf("queued request error = %v", err)
	}

	entries, err := waitForRecentEntries(st, 3, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	for _, entry := range entries {
		parsed, err := waitForRecordedPrelude(entry.LogPath, time.Second)
		if err != nil {
			t.Fatalf("waitForRecordedPrelude(%q) error = %v", entry.LogPath, err)
		}
		for _, event := range parsed.Events {
			if event.Type == "limit.queue_saturated" {
				if got := event.Attributes["http_status"]; got != float64(http.StatusServiceUnavailable) {
					t.Fatalf("http_status = %v, want %d", got, http.StatusServiceUnavailable)
				}
				return
			}
		}
	}
	t.Fatalf("limit.queue_saturated event not found in entries: %+v", entries)
}

func TestHandlerRetryExhaustedReturns502(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	// Both upstreams return 404 — handler should eventually give up.
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}))
	defer upstream1.Close()

	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}))
	defer upstream2.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID: "primary", Enabled: boolPtr(true), Priority: 100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream:       config.UpstreamConfig{BaseURL: upstream1.URL + "/v1", ProviderPreset: "openai"},
			},
			{
				ID: "secondary", Enabled: boolPtr(true), Priority: 90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream:       config.UpstreamConfig{BaseURL: upstream2.URL + "/v1", ProviderPreset: "openai"},
			},
		},
	}
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("resp.StatusCode = %d, want 502 (all upstreams exhausted)", resp.StatusCode)
	}
}

func TestHandlerNoRetryOnClientError4xx(t *testing.T) {
	// 4xx errors other than 404 and 429 should NOT be retried — they indicate
	// a client mistake that would fail on any upstream.
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var primaryCalled, secondaryCalled bool
	upstreamPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad request"}`)
	}))
	defer upstreamPrimary.Close()

	upstreamSecondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp","usage":{"total_tokens":2}}`)
	}))
	defer upstreamSecondary.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID: "primary", Enabled: boolPtr(true), Priority: 100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream:       config.UpstreamConfig{BaseURL: upstreamPrimary.URL + "/v1", ProviderPreset: "openai"},
			},
			{
				ID: "secondary", Enabled: boolPtr(true), Priority: 90,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream:       config.UpstreamConfig{BaseURL: upstreamSecondary.URL + "/v1", ProviderPreset: "openai"},
			},
		},
	}
	cfg.Router.Selection.Policy = router.PolicyFirstAvailable
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, _ := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("resp.StatusCode = %d, want 400 (should NOT retry on 400)", resp.StatusCode)
	}
	if secondaryCalled {
		t.Fatalf("secondary was called, but client 4xx errors should not trigger retry")
	}
	if !primaryCalled {
		t.Fatalf("primary was not called, expected 400 response forwarded from primary")
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

func hasRecordEvent(events []recordfile.RecordEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
