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
	"sync/atomic"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/executionevent"
	"github.com/kingfs/llm-tracelab/ent/dao/requestaudit"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamexchange"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	responsesruntime "github.com/kingfs/llm-tracelab/internal/responses/runtime"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func boolPtr(v bool) *bool { return &v }

func TestProxyExternalCommandExecutorHelper(t *testing.T) {
	if os.Getenv("LLM_TRACELAB_PROXY_EXTERNAL_EXECUTOR_HELPER") != "1" {
		return
	}
	args := os.Args
	modeIndex := -1
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			modeIndex = i + 1
			break
		}
	}
	if modeIndex < 0 {
		fmt.Fprintln(os.Stderr, "missing mode")
		os.Exit(2)
	}
	switch args[modeIndex] {
	case "responses-e2e":
		var input struct {
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			fmt.Fprintf(os.Stderr, "decode stdin: %v", err)
			os.Exit(2)
		}
		var arguments map[string]any
		if err := json.Unmarshal([]byte(input.Arguments), &arguments); err != nil {
			fmt.Fprintf(os.Stderr, "decode arguments: %v", err)
			os.Exit(2)
		}
		q, _ := arguments["q"].(string)
		fmt.Printf(`{"source":"external_command","call_id":%q,"name":%q,"q":%q}`, input.CallID, input.Name, q)
	case "responses-e2e-fail":
		fmt.Fprintln(os.Stderr, "intentional executor failure")
		os.Exit(3)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q", args[modeIndex])
		os.Exit(2)
	}
}

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
	req.Header.Set("X-Client-Request-Id", "client-audit-1")

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
	responseID, ok := responsePayload["id"].(string)
	if !ok || responseID == "" {
		t.Fatalf("response id missing from payload: %+v", responsePayload)
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

	audits, err := st.EntClient().RequestAudit.Query().
		Order(requestaudit.ByCreatedAt()).
		All(context.Background())
	if err != nil {
		t.Fatalf("query request audits: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("request audits len = %d, want 1: %+v", len(audits), audits)
	}
	audit := audits[0]
	if audit.Status != "completed" || audit.ResponseID != responseID {
		t.Fatalf("request audit status/response_id = %q/%q, want completed/%q", audit.Status, audit.ResponseID, responseID)
	}
	if audit.Method != http.MethodPost || audit.Path != "/v1/responses" {
		t.Fatalf("request audit method/path = %q/%q, want POST /v1/responses", audit.Method, audit.Path)
	}
	if audit.ClientRequestID != "client-audit-1" {
		t.Fatalf("request audit client_request_id = %q, want client-audit-1", audit.ClientRequestID)
	}
	if audit.BodySha256 == "" || audit.BodyPreview == "" {
		t.Fatalf("request audit missing body fields: %#v", audit)
	}
	if audit.HeaderJSON["content-type"] != "application/json" || audit.HeaderJSON["x-client-request-id"] != "client-audit-1" {
		t.Fatalf("request audit headers = %#v", audit.HeaderJSON)
	}

	exchanges, err := st.EntClient().UpstreamExchange.Query().
		Order(upstreamexchange.ByStartedAt()).
		All(context.Background())
	if err != nil {
		t.Fatalf("query upstream exchanges: %v", err)
	}
	if len(exchanges) != 1 {
		t.Fatalf("upstream exchanges len = %d, want 1: %+v", len(exchanges), exchanges)
	}
	exchange := exchanges[0]
	if exchange.RequestAuditID != audit.ID {
		t.Fatalf("upstream exchange request_audit_id = %q, want %q", exchange.RequestAuditID, audit.ID)
	}
	if exchange.ResponseID != responseID {
		t.Fatalf("upstream exchange response_id = %q, want %q", exchange.ResponseID, responseID)
	}
	if exchange.TraceID != parsed.Header.Meta.RequestID {
		t.Fatalf("upstream exchange trace_id = %q, want recorder request_id %q", exchange.TraceID, parsed.Header.Meta.RequestID)
	}
	if exchange.CassettePath != recordPath {
		t.Fatalf("upstream exchange cassette_path = %q, want %q", exchange.CassettePath, recordPath)
	}
	if exchange.UpstreamID != "openai-chat" {
		t.Fatalf("upstream exchange upstream_id = %q, want openai-chat", exchange.UpstreamID)
	}
	if exchange.RouteTarget != upstreamServer.URL+"/v1" {
		t.Fatalf("upstream exchange route_target = %q, want %q", exchange.RouteTarget, upstreamServer.URL+"/v1")
	}
	if exchange.Model != "gpt-5" || exchange.Endpoint != "/v1/chat/completions" || exchange.StatusCode != http.StatusOK {
		t.Fatalf("upstream exchange model/endpoint/status = %q/%q/%d, want gpt-5//v1/chat/completions/200", exchange.Model, exchange.Endpoint, exchange.StatusCode)
	}
	if exchange.StartedAt.IsZero() || exchange.CompletedAt.IsZero() || exchange.CompletedAt.Before(exchange.StartedAt) {
		t.Fatalf("upstream exchange timestamps invalid: started=%s completed=%s", exchange.StartedAt, exchange.CompletedAt)
	}
	if exchange.ErrorText != "" {
		t.Fatalf("upstream exchange error_text = %q, want empty", exchange.ErrorText)
	}

	events, err := st.EntClient().ExecutionEvent.Query().
		Order(executionevent.ByOccurredAt()).
		All(context.Background())
	if err != nil {
		t.Fatalf("query execution events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("execution events len = %d, want 4: %+v", len(events), events)
	}
	eventsByKey := map[string]int{}
	for i, event := range events {
		key := event.EventType + "/" + event.Phase + "/" + event.Status
		eventsByKey[key] = i
		if event.DetailsJSON["request_audit_id"] != audit.ID {
			t.Fatalf("execution event[%d] request_audit_id = %#v, want %q", i, event.DetailsJSON["request_audit_id"], audit.ID)
		}
	}
	for _, key := range []string{
		"response.request/request/accepted",
		"response.model_call/model_call/started",
		"response.model_call/model_call/completed",
		"response.request/request/completed",
	} {
		if _, ok := eventsByKey[key]; !ok {
			t.Fatalf("missing execution event %q in %+v", key, events)
		}
	}
	modelCompleted := events[eventsByKey["response.model_call/model_call/completed"]]
	if modelCompleted.DetailsJSON["cassette_path"] != recordPath || modelCompleted.DetailsJSON["trace_id"] != parsed.Header.Meta.RequestID {
		t.Fatalf("model call completed event details = %#v, want cassette_path=%q trace_id=%q", modelCompleted.DetailsJSON, recordPath, parsed.Header.Meta.RequestID)
	}
	requestCompleted := events[eventsByKey["response.request/request/completed"]]
	if requestCompleted.ResponseID != responseID {
		t.Fatalf("completed request event response_id = %q, want %q", requestCompleted.ResponseID, responseID)
	}
}

func TestHandlerResponsesServerModeRequiresChatCompletionsCompatibleUpstream(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
		},
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "google-genai",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gemini-2.5-pro"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://generativelanguage.googleapis.com/v1beta",
					ProviderPreset: "google_genai",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir

	_, err = NewHandler(cfg, st)
	if err == nil {
		t.Fatal("NewHandler() error = nil, want local Responses server backend validation error")
	}
	if !strings.Contains(err.Error(), router.LocalResponsesServerBackendRequiredError) {
		t.Fatalf("NewHandler() error = %q, want contain %q", err.Error(), router.LocalResponsesServerBackendRequiredError)
	}
}

func TestHandlerResponsesServerModeDoesNotUseNativeResponsesTargetAsChatBackend(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var nativeRequests atomic.Int32
	nativeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeRequests.Add(1)
		http.Error(w, "native responses target must not be used by local runtime chat adapter", http.StatusTeapot)
	}))
	defer nativeServer.Close()

	var chatRequests atomic.Int32
	var gotChatPath string
	var gotChatBody map[string]any
	chatServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatRequests.Add(1)
		gotChatPath = r.URL.Path
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotChatBody); err != nil {
			t.Errorf("decode upstream chat request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_boundary","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer chatServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
		},
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "native-responses",
				Enabled:        boolPtr(true),
				Priority:       200,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        nativeServer.URL + "/v1",
					ProviderPreset: "openai",
					APIType:        "responses_native",
					Mode:           "proxy",
					Capabilities: config.UpstreamCapabilitiesConfig{
						Responses:       boolPtr(true),
						ChatCompletions: boolPtr(false),
					},
				},
			},
			{
				ID:             "chat-backend",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        chatServer.URL + "/v1",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Mode:           "responses_server",
					Capabilities: config.UpstreamCapabilitiesConfig{
						ChatCompletions: boolPtr(true),
					},
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
	if got := nativeRequests.Load(); got != 0 {
		t.Fatalf("native responses target received %d requests, want 0", got)
	}
	if got := chatRequests.Load(); got != 1 {
		t.Fatalf("chat backend received %d requests, want 1", got)
	}
	if gotChatPath != "/v1/chat/completions" {
		t.Fatalf("chat upstream path = %q, want /v1/chat/completions", gotChatPath)
	}
	if gotChatBody["model"] != "gpt-5" {
		t.Fatalf("chat body model = %v, want gpt-5; body=%+v", gotChatBody["model"], gotChatBody)
	}

	parsed, err := waitForRecordedPrelude(findRecordedHTTP(t, outputDir), time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude() error = %v", err)
	}
	if parsed.Header.Meta.URL != "/v1/chat/completions" || parsed.Header.Meta.Endpoint != "/v1/chat/completions" {
		t.Fatalf("recorded path endpoint = %q/%q, want /v1/chat/completions", parsed.Header.Meta.URL, parsed.Header.Meta.Endpoint)
	}
	if parsed.Header.Meta.SelectedUpstreamID != "chat-backend" {
		t.Fatalf("SelectedUpstreamID = %q, want chat-backend", parsed.Header.Meta.SelectedUpstreamID)
	}
}

func TestHandlerResponsesServerModeRejectsNativeResponsesOnlyUpstream(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
		},
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-responses",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
					APIType:        "responses_native",
					Mode:           "server",
					Capabilities: config.UpstreamCapabilitiesConfig{
						ChatCompletions: boolPtr(false),
						Responses:       boolPtr(true),
					},
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir

	_, err = NewHandler(cfg, st)
	if err == nil {
		t.Fatal("NewHandler() error = nil, want local Responses server backend validation error")
	}
	if !strings.Contains(err.Error(), router.LocalResponsesServerBackendRequiredError) {
		t.Fatalf("NewHandler() error = %q, want contain %q", err.Error(), router.LocalResponsesServerBackendRequiredError)
	}
}

func TestHandlerResponsesServerModeAllowsChatCompletionsCompatibleUpstream(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

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
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
				},
			},
		},
	}
	cfg.Debug.OutputDir = outputDir

	if _, err := NewHandler(cfg, st); err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
}

func TestHandlerResponsesServerModeStreamReturnsSSE(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	var gotUpstreamStream bool
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var chatReq map[string]any
		if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		gotUpstreamStream, _ = chatReq["stream"].(bool)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"id":"chatcmpl_stream_1","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"stream "},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl_stream_1","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":"stop"}]}`,
			`data: {"id":"chatcmpl_stream_1","object":"chat.completion.chunk","model":"gpt-5","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
			`data: [DONE]`,
		}, "\n"))
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

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"ping","stream":true}`))
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
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	body := string(bodyBytes)
	for _, event := range []string{"response.created", "response.output_text.delta", "response.completed"} {
		if !strings.Contains(body, "event: "+event+"\n") {
			t.Fatalf("stream body missing event %q:\n%s", event, body)
		}
	}
	if !strings.Contains(body, `"delta":"stream "`) || !strings.Contains(body, `"delta":"pong"`) {
		t.Fatalf("stream body missing incremental output text deltas:\n%s", body)
	}
	if !gotUpstreamStream {
		t.Fatal("upstream chat request stream = false, want true")
	}

	recordPath := findRecordedHTTP(t, outputDir)
	parsed, err := waitForRecordedPrelude(recordPath, time.Second)
	if err != nil {
		t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
	}
	if parsed.Header.Meta.Endpoint != "/v1/chat/completions" {
		t.Fatalf("recorded endpoint = %q, want /v1/chat/completions", parsed.Header.Meta.Endpoint)
	}
	if !parsed.Header.Layout.IsStream {
		t.Fatalf("internal chat cassette IsStream = false, want true for upstream Chat Completions SSE")
	}
	recordContent, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read recorded cassette: %v", err)
	}
	_, _, _, recordedResponseBody := recordfile.ExtractSections(recordContent, parsed)
	if !strings.Contains(string(recordedResponseBody), `data: {"id":"chatcmpl_stream_1"`) {
		t.Fatalf("recorded response body missing raw SSE data:\n%s", string(recordedResponseBody))
	}

	streamEvents, err := st.EntClient().ExecutionEvent.Query().
		Where(executionevent.EventTypeEQ("response.stream")).
		Order(executionevent.ByOccurredAt(), executionevent.ByID()).
		All(context.Background())
	if err != nil {
		t.Fatalf("query stream execution events: %v", err)
	}
	if len(streamEvents) != 2 {
		t.Fatalf("stream events len = %d, want started/completed: %+v", len(streamEvents), streamEvents)
	}
	if streamEvents[0].Status != "started" || streamEvents[1].Status != "completed" {
		t.Fatalf("stream event statuses = %q/%q, want started/completed", streamEvents[0].Status, streamEvents[1].Status)
	}
}

func TestHandlerResponsesServerModeCancelPropagatesToChatCompletionsUpstream(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamStarted := make(chan struct{})
	upstreamCanceled := make(chan struct{})
	releaseUpstream := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var chatReq map[string]any
		if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
			t.Errorf("decode upstream request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		close(upstreamStarted)
		select {
		case <-r.Context().Done():
			close(upstreamCanceled)
		case <-releaseUpstream:
		}
	}))
	defer upstreamServer.Close()
	defer close(releaseUpstream)

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

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"cancel me"}`))
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	clientDone := make(chan error, 1)
	go func() {
		resp, err := proxyServer.Client().Do(req)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		clientDone <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream request")
	}
	cancel()

	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream request context cancellation")
	}

	select {
	case err := <-clientDone:
		if err == nil {
			t.Fatal("client.Do() error = nil, want cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancelled client request")
	}

	audits, err := waitForRequestAuditsWithStatus(st, 1, "cancelled", time.Second)
	if err != nil {
		t.Fatalf("waitForRequestAuditsWithStatus() error = %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("request audits len = %d, want 1: %+v", len(audits), audits)
	}
	audit := audits[0]
	if audit.Status != "cancelled" {
		t.Fatalf("request audit status = %q, want cancelled", audit.Status)
	}
	if !strings.Contains(audit.ErrorText, context.Canceled.Error()) {
		t.Fatalf("request audit error_text = %q, want contains %q", audit.ErrorText, context.Canceled.Error())
	}

	events, err := waitForExecutionEvents(st, 4, time.Second)
	if err != nil {
		t.Fatalf("waitForExecutionEvents() error = %v", err)
	}
	eventsByKey := map[string]bool{}
	for _, event := range events {
		eventsByKey[event.EventType+"/"+event.Phase+"/"+event.Status] = true
		eventAuditID := event.RequestAuditID
		if eventAuditID == "" {
			eventAuditID, _ = event.DetailsJSON["request_audit_id"].(string)
		}
		if eventAuditID != audit.ID {
			t.Fatalf("execution event request_audit_id = %q, details=%#v, want %q", event.RequestAuditID, event.DetailsJSON, audit.ID)
		}
	}
	for _, key := range []string{
		"response.request/request/accepted",
		"response.model_call/model_call/started",
		"response.model_call/model_call/cancelled",
		"response.request/request/cancelled",
	} {
		if !eventsByKey[key] {
			t.Fatalf("missing execution event %q in %+v", key, events)
		}
	}

	exchanges, err := waitForUpstreamExchanges(st, 1, time.Second)
	if err != nil {
		t.Fatalf("waitForUpstreamExchanges() error = %v", err)
	}
	if len(exchanges) != 1 {
		t.Fatalf("upstream exchanges len = %d, want 1: %+v", len(exchanges), exchanges)
	}
	if exchanges[0].RequestAuditID != audit.ID || exchanges[0].StatusCode != 0 {
		t.Fatalf("upstream exchange audit/status = %q/%d, want %q/0", exchanges[0].RequestAuditID, exchanges[0].StatusCode, audit.ID)
	}
	if !strings.Contains(exchanges[0].ErrorText, context.Canceled.Error()) {
		t.Fatalf("upstream exchange error_text = %q, want contains %q", exchanges[0].ErrorText, context.Canceled.Error())
	}
}

func TestHandlerResponsesServerModeCompactCreatesSummaryResponse(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	callCount := 0
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_first","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Use cassettes for deterministic replay."},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`)
		case 2:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_compact","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"The user is working on llm-tracelab Responses server mode and must preserve cassette replay."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`)
		default:
			t.Fatalf("unexpected upstream call %d", callCount)
		}
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

	createResp, err := http.Post(proxyServer.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5","input":"what matters?","metadata":{"codex":{"thread_id":"thread_compact"}}}`))
	if err != nil {
		t.Fatalf("create response request: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create status = %d, want 200; body=%s", createResp.StatusCode, string(body))
	}
	var created protocol.Response
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created response id empty: %#v", created)
	}

	compactBody := fmt.Sprintf(`{"response_id":%q}`, created.ID)
	compactResp, err := http.Post(proxyServer.URL+"/v1/responses/compact", "application/json", strings.NewReader(compactBody))
	if err != nil {
		t.Fatalf("compact response request: %v", err)
	}
	defer compactResp.Body.Close()
	if compactResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(compactResp.Body)
		t.Fatalf("compact status = %d, want 200; body=%s", compactResp.StatusCode, string(body))
	}
	var compacted protocol.Response
	if err := json.NewDecoder(compactResp.Body).Decode(&compacted); err != nil {
		t.Fatalf("decode compact response: %v", err)
	}
	if compacted.PreviousResponseID != created.ID || len(compacted.Output) != 1 || compacted.Output[0].Type != "summary" {
		t.Fatalf("compact response = %#v, want summary linked to %q", compacted, created.ID)
	}
	if !strings.Contains(compacted.Output[0].Content[0].Text, "Responses server mode") {
		t.Fatalf("compact summary text = %q, want upstream summary", compacted.Output[0].Content[0].Text)
	}

	events, err := st.EntClient().ExecutionEvent.Query().
		Where(executionevent.EventTypeEQ("response.compact")).
		Order(executionevent.ByOccurredAt(), executionevent.ByID()).
		All(context.Background())
	if err != nil {
		t.Fatalf("query compact execution events: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("compact events len = %d, want started/completed: %+v", len(events), events)
	}
	if events[0].Status != "started" || events[len(events)-1].Status != "completed" || events[len(events)-1].ResponseID != compacted.ID {
		t.Fatalf("compact event statuses/response = %+v", events)
	}
}

func TestHandlerResponsesServerModeAutoCompactsByHistoryThreshold(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	callCount := 0
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		switch callCount {
		case 1:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_first","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"first answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		case 2:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_auto_compact","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Auto summary keeps replay constraints."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
		case 3:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_second","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"second answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
		default:
			t.Fatalf("unexpected upstream call %d", callCount)
		}
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled:                     true,
			AutoCompact:                 true,
			CompactHistoryItemThreshold: 1,
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

	firstHTTPResp, err := http.Post(proxyServer.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5","input":"first"}`))
	if err != nil {
		t.Fatalf("first response request: %v", err)
	}
	defer firstHTTPResp.Body.Close()
	if firstHTTPResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(firstHTTPResp.Body)
		t.Fatalf("first status = %d, want 200; body=%s", firstHTTPResp.StatusCode, string(body))
	}
	var first protocol.Response
	if err := json.NewDecoder(firstHTTPResp.Body).Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	secondBody := fmt.Sprintf(`{"model":"gpt-5","previous_response_id":%q,"input":"second"}`, first.ID)
	secondHTTPResp, err := http.Post(proxyServer.URL+"/v1/responses", "application/json", strings.NewReader(secondBody))
	if err != nil {
		t.Fatalf("second response request: %v", err)
	}
	defer secondHTTPResp.Body.Close()
	if secondHTTPResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(secondHTTPResp.Body)
		t.Fatalf("second status = %d, want 200; body=%s", secondHTTPResp.StatusCode, string(body))
	}
	var second protocol.Response
	if err := json.NewDecoder(secondHTTPResp.Body).Decode(&second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if second.PreviousResponseID == "" || second.PreviousResponseID == first.ID {
		t.Fatalf("second previous_response_id = %q, want generated compact response id", second.PreviousResponseID)
	}
	compact, ok, err := responsesruntime.NewEntStore(st.EntClient()).Get(context.Background(), second.PreviousResponseID)
	if err != nil || !ok {
		t.Fatalf("compact response lookup ok=%v err=%v", ok, err)
	}
	if compact.PreviousResponseID != first.ID || len(compact.Output) != 1 || compact.Output[0].Type != "summary" {
		t.Fatalf("compact response = %#v, want summary linked to first", compact)
	}

	events, err := st.EntClient().ExecutionEvent.Query().
		Where(executionevent.EventTypeEQ("response.compact"), executionevent.StatusEQ("auto_triggered")).
		All(context.Background())
	if err != nil {
		t.Fatalf("query auto compact events: %v", err)
	}
	if len(events) != 1 || events[0].ResponseID != compact.ID {
		t.Fatalf("auto compact events = %+v, want one event for compact response %q", events, compact.ID)
	}
}

func TestHandlerResponsesServerModeHostedWebSearchToolLoopRecordsInternalChatCompletions(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamCalls := 0
	var firstChatBody map[string]any
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
		switch upstreamCalls {
		case 1:
			firstChatBody = chatBody
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl_search","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_search","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"llm-tracelab replay\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
		case 2:
			secondChatBody = chatBody
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl_final","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Mock search says replay cassettes keep tests deterministic."},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
		default:
			http.Error(w, "unexpected extra call", http.StatusInternalServerError)
		}
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
		},
		Tools: config.ToolsConfig{
			WebSearch: config.WebSearchToolConfig{
				Enabled:    true,
				Provider:   "mock",
				MaxResults: 2,
			},
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

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"find docs","tools":[{"type":"web_search"}]}`))
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
	var responsePayload protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&responsePayload); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if upstreamCalls != 2 {
		t.Fatalf("upstreamCalls = %d, want 2", upstreamCalls)
	}
	if firstChatBody == nil || secondChatBody == nil {
		t.Fatalf("missing captured chat bodies first=%v second=%v", firstChatBody != nil, secondChatBody != nil)
	}
	tools, ok := firstChatBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("first chat tools = %#v, want one web_search function", firstChatBody["tools"])
	}
	firstTool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("first chat tool = %#v, want object", tools[0])
	}
	firstFunction, ok := firstTool["function"].(map[string]any)
	if !ok || firstFunction["name"] != "web_search" {
		t.Fatalf("first chat function tool = %#v, want web_search", firstTool["function"])
	}

	messages, ok := secondChatBody["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("second chat messages = %#v, want user + assistant tool_call + tool result", secondChatBody["messages"])
	}
	toolMessage, ok := messages[2].(map[string]any)
	if !ok || toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_search" {
		t.Fatalf("second chat tool message = %#v", messages[2])
	}
	toolContent, _ := toolMessage["content"].(string)
	if !strings.Contains(toolContent, "Mock search result 1") || !strings.Contains(toolContent, "llm-tracelab replay") {
		t.Fatalf("second chat tool content missing mock result: %q", toolContent)
	}

	if responsePayload.Usage != (protocol.Usage{InputTokens: 13, OutputTokens: 6, TotalTokens: 19}) {
		t.Fatalf("response usage = %#v, want accumulated 13/6/19", responsePayload.Usage)
	}
	if len(responsePayload.Output) != 2 {
		t.Fatalf("response output len = %d, want 2: %#v", len(responsePayload.Output), responsePayload.Output)
	}
	if got := responsePayload.Output[0]; got.Type != "web_search_call" || got.Status != "completed" {
		t.Fatalf("first response output = %#v, want completed web_search_call", got)
	}
	if got := responsePayload.Output[1]; got.Type != "message" || got.Content[0].Text != "Mock search says replay cassettes keep tests deterministic." {
		t.Fatalf("second response output = %#v, want final assistant message", got)
	}

	entries, err := waitForRecentEntries(st, 2, time.Second)
	if err != nil {
		t.Fatalf("waitForRecentEntries() error = %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("indexed entries = %d, want at least 2", len(entries))
	}
	for _, entry := range entries[:2] {
		if entry.Header.Meta.Endpoint != "/v1/chat/completions" {
			t.Fatalf("recorded endpoint = %q, want /v1/chat/completions", entry.Header.Meta.Endpoint)
		}
	}

	toolEvents, err := st.EntClient().ExecutionEvent.Query().
		Where(
			executionevent.EventTypeEQ("response.tool_call"),
			executionevent.ResponseIDEQ(responsePayload.ID),
		).
		Order(executionevent.ByOccurredAt(), executionevent.ByID()).
		All(context.Background())
	if err != nil {
		t.Fatalf("query tool execution events: %v", err)
	}
	if len(toolEvents) != 2 {
		t.Fatalf("tool execution events len = %d, want 2: %+v", len(toolEvents), toolEvents)
	}
	if toolEvents[0].Status != "started" || toolEvents[1].Status != "completed" {
		t.Fatalf("tool execution event statuses = %q/%q, want started/completed", toolEvents[0].Status, toolEvents[1].Status)
	}
	if toolEvents[1].DetailsJSON["tool_name"] != "web_search" || toolEvents[1].DetailsJSON["call_id"] != "call_search" || toolEvents[1].DetailsJSON["query"] != "llm-tracelab replay" {
		t.Fatalf("completed tool event details = %#v", toolEvents[1].DetailsJSON)
	}
}

func TestHandlerResponsesServerModeConfiguredStaticFunctionExecutor(t *testing.T) {
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
		w.Header().Set("Content-Type", "application/json")
		switch upstreamCalls {
		case 1:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_lookup","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"codex\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
		case 2:
			secondChatBody = chatBody
			_, _ = io.WriteString(w, `{"id":"chatcmpl_final","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Lookup completed."},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
		default:
			http.Error(w, "unexpected extra call", http.StatusInternalServerError)
		}
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
			FunctionExecutors: config.ResponsesFunctionExecutorConfig{
				Enabled:        true,
				Timeout:        time.Second,
				MaxResultBytes: 256,
				Redaction: config.ResponsesFunctionRedactionConfig{
					Arguments: true,
				},
				Executors: []config.ResponsesFunctionExecutorBinding{
					{
						Name:   "lookup",
						Type:   "static_response",
						Output: map[string]any{"source": "configured", "ok": true},
					},
				},
			},
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

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"lookup","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`))
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
	var responsePayload protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&responsePayload); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if upstreamCalls != 2 {
		t.Fatalf("upstreamCalls = %d, want 2", upstreamCalls)
	}
	messages, ok := secondChatBody["messages"].([]any)
	if !ok || len(messages) < 3 {
		t.Fatalf("second chat messages missing: %#v", secondChatBody)
	}
	toolMessage, ok := messages[len(messages)-1].(map[string]any)
	if !ok || toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_lookup" {
		t.Fatalf("tool message mismatch: %#v", messages[len(messages)-1])
	}
	content, _ := toolMessage["content"].(string)
	if !strings.Contains(content, `"source":"configured"`) {
		t.Fatalf("tool message content = %q, want configured output", content)
	}
	if len(responsePayload.Output) != 2 || responsePayload.Output[0].Type != "function_call_output" {
		t.Fatalf("response output = %#v, want function_call_output + final message", responsePayload.Output)
	}
}

func TestHandlerResponsesServerModeConfiguredExternalCommandFunctionExecutor(t *testing.T) {
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
		w.Header().Set("Content-Type", "application/json")
		switch upstreamCalls {
		case 1:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_lookup","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_external_lookup","type":"function","function":{"name":"lookup_external","arguments":"{\"q\":\"codex\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
		case 2:
			secondChatBody = chatBody
			_, _ = io.WriteString(w, `{"id":"chatcmpl_final","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"External lookup completed."},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
		default:
			http.Error(w, "unexpected extra call", http.StatusInternalServerError)
		}
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled: true,
			FunctionExecutors: config.ResponsesFunctionExecutorConfig{
				Enabled:        true,
				Timeout:        time.Second,
				MaxResultBytes: 512,
				Executors: []config.ResponsesFunctionExecutorBinding{
					{
						Name:    "lookup_external",
						Type:    "external_command",
						Command: os.Args[0],
						Args:    []string{"-test.run=TestProxyExternalCommandExecutorHelper", "--", "responses-e2e"},
						Env:     map[string]string{"LLM_TRACELAB_PROXY_EXTERNAL_EXECUTOR_HELPER": "1"},
					},
				},
			},
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

	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"lookup","tools":[{"type":"function","name":"lookup_external","parameters":{"type":"object"}}]}`))
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
	var responsePayload protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&responsePayload); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if upstreamCalls != 2 {
		t.Fatalf("upstreamCalls = %d, want 2", upstreamCalls)
	}
	messages, ok := secondChatBody["messages"].([]any)
	if !ok || len(messages) < 3 {
		t.Fatalf("second chat messages missing: %#v", secondChatBody)
	}
	toolMessage, ok := messages[len(messages)-1].(map[string]any)
	if !ok || toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_external_lookup" {
		t.Fatalf("tool message mismatch: %#v", messages[len(messages)-1])
	}
	content, _ := toolMessage["content"].(string)
	for _, want := range []string{`"source":"external_command"`, `"call_id":"call_external_lookup"`, `"name":"lookup_external"`, `"q":"codex"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("tool message content = %q, want %s", content, want)
		}
	}
	if len(responsePayload.Output) != 2 || responsePayload.Output[0].Type != "function_call_output" {
		t.Fatalf("response output = %#v, want function_call_output + final message", responsePayload.Output)
	}
	output, _ := responsePayload.Output[0].Output.(string)
	if !strings.Contains(output, `"source":"external_command"`) {
		t.Fatalf("function_call_output output = %#v, want external command stdout", responsePayload.Output[0].Output)
	}
	if responsePayload.Output[1].Type != "message" || len(responsePayload.Output[1].Content) == 0 || responsePayload.Output[1].Content[0].Text != "External lookup completed." {
		t.Fatalf("final response output = %#v, want final assistant message", responsePayload.Output[1])
	}
}

func TestHandlerResponsesServerModeAdoptsChannelModelProfileWhenEnabled(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "openai-chat",
		Name:           "OpenAI Chat",
		BaseURL:        "https://api.openai.example/v1",
		HeadersJSON:    "{}",
		Enabled:        true,
		Priority:       10,
		Weight:         1,
		CapacityHint:   1,
		ModelDiscovery: "manual",
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	contextWindow := 128000
	maxOutputTokens := 777
	compactThreshold := 3
	supportsChat := 1
	if err := st.ReplaceChannelModels("openai-chat", []store.ChannelModelRecord{
		{
			Model:                       "gpt-5",
			DisplayName:                 "GPT-5",
			Source:                      "manual",
			Enabled:                     true,
			SupportsChatCompletions:     &supportsChat,
			ContextWindow:               &contextWindow,
			MaxOutputTokens:             &maxOutputTokens,
			CompactHistoryItemThreshold: &compactThreshold,
			UpstreamModel:               "provider/private-gpt-5",
			ProfileSource:               "test",
			ProfileAdoptionStatus:       "adopted",
		},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}

	var chatBody map[string]any
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
			t.Errorf("decode upstream request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_profile","model":"provider/private-gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"profile adopted"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled:                   true,
			AdoptChannelModelProfiles: true,
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

	resp, err := http.Post(proxyServer.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5","input":"profile"}`))
	if err != nil {
		t.Fatalf("response request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	if chatBody == nil {
		t.Fatalf("upstream chat body was not captured")
	}
	if chatBody["model"] != "provider/private-gpt-5" {
		t.Fatalf("chat model = %#v, want adopted upstream model", chatBody["model"])
	}
	if got := int(chatBody["max_tokens"].(float64)); got != maxOutputTokens {
		t.Fatalf("max_tokens = %d, want adopted max output %d", got, maxOutputTokens)
	}
}

func TestResponsesRuntimeModelProfilesChannelAdoptionBoundaries(t *testing.T) {
	newStore := func(t *testing.T, recordsByChannel map[string][]store.ChannelModelRecord) *store.Store {
		t.Helper()
		st, err := store.New(t.TempDir())
		if err != nil {
			t.Fatalf("store.New() error = %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		for channelID, records := range recordsByChannel {
			if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
				ID:             channelID,
				Name:           channelID,
				BaseURL:        "https://api.openai.example/v1",
				HeadersJSON:    "{}",
				Enabled:        true,
				Priority:       10,
				Weight:         1,
				CapacityHint:   1,
				ModelDiscovery: "manual",
			}); err != nil {
				t.Fatalf("UpsertChannelConfig(%s) error = %v", channelID, err)
			}
			if err := st.ReplaceChannelModels(channelID, records); err != nil {
				t.Fatalf("ReplaceChannelModels(%s) error = %v", channelID, err)
			}
		}
		return st
	}
	adoptedRecord := func(upstreamModel string, contextWindow int, maxOutputTokens int) store.ChannelModelRecord {
		supportsChat := 1
		return store.ChannelModelRecord{
			Model:                   "gpt-5",
			DisplayName:             "GPT-5",
			Source:                  "manual",
			Enabled:                 true,
			SupportsChatCompletions: &supportsChat,
			ContextWindow:           &contextWindow,
			MaxOutputTokens:         &maxOutputTokens,
			UpstreamModel:           upstreamModel,
			ProfileSource:           "test",
			ProfileAdoptionStatus:   "adopted",
		}
	}

	t.Run("default disabled ignores adopted channel profiles", func(t *testing.T) {
		st := newStore(t, map[string][]store.ChannelModelRecord{
			"openai-chat": {adoptedRecord("provider/private-gpt-5", 128000, 777)},
		})
		profiles, err := responsesRuntimeModelProfiles(&config.Config{}, st)
		if err != nil {
			t.Fatalf("responsesRuntimeModelProfiles() error = %v", err)
		}
		if len(profiles) != 0 {
			t.Fatalf("profiles = %+v, want no adopted profiles when config flag is disabled", profiles)
		}
	})

	t.Run("explicit yaml profile wins over adopted channel profile", func(t *testing.T) {
		st := newStore(t, map[string][]store.ChannelModelRecord{
			"openai-chat": {adoptedRecord("provider/private-gpt-5", 128000, 777)},
		})
		cfg := &config.Config{}
		cfg.ResponsesServer.AdoptChannelModelProfiles = true
		cfg.ResponsesServer.ModelProfiles = []config.ResponsesModelProfileConfig{
			{
				Name:            "gpt-5",
				UpstreamModel:   "yaml/gpt-5",
				MaxOutputTokens: 99,
			},
		}
		profiles, err := responsesRuntimeModelProfiles(cfg, st)
		if err != nil {
			t.Fatalf("responsesRuntimeModelProfiles() error = %v", err)
		}
		if len(profiles) != 1 {
			t.Fatalf("profiles = %+v, want only explicit yaml profile", profiles)
		}
		if profiles[0].UpstreamModel != "yaml/gpt-5" || profiles[0].Budget.MaxOutputTokens != 99 {
			t.Fatalf("profile = %+v, want yaml profile to win", profiles[0])
		}
	})

	t.Run("conflicting adopted channel profiles are skipped", func(t *testing.T) {
		st := newStore(t, map[string][]store.ChannelModelRecord{
			"openai-chat-a": {adoptedRecord("provider/a-gpt-5", 128000, 777)},
			"openai-chat-b": {adoptedRecord("provider/b-gpt-5", 64000, 512)},
		})
		cfg := &config.Config{}
		cfg.ResponsesServer.AdoptChannelModelProfiles = true
		profiles, err := responsesRuntimeModelProfiles(cfg, st)
		if err != nil {
			t.Fatalf("responsesRuntimeModelProfiles() error = %v", err)
		}
		if len(profiles) != 0 {
			t.Fatalf("profiles = %+v, want conflicting adopted profile skipped", profiles)
		}
	})
}

func TestHandlerResponsesServerModeStreamAutoCompactFunctionExecutorFailure(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamCalls := 0
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
		switch upstreamCalls {
		case 1:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl_first","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"first answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl_auto_compact","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Compact summary before failing tool."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
		case 3:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, strings.Join([]string{
				`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_lookup_fail","type":"function","function":{"name":"lookup_fail","arguments":"{\"q\""}}]},"finish_reason":null}]}`,
				`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"codex\"}"}}]},"finish_reason":"tool_calls"}]}`,
				`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`,
				`data: [DONE]`,
			}, "\n"))
		default:
			http.Error(w, "unexpected extra call", http.StatusInternalServerError)
		}
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled:                     true,
			AutoCompact:                 true,
			CompactHistoryItemThreshold: 1,
			FunctionExecutors: config.ResponsesFunctionExecutorConfig{
				Enabled:        true,
				Timeout:        time.Second,
				MaxResultBytes: 512,
				Executors: []config.ResponsesFunctionExecutorBinding{
					{
						Name:    "lookup_fail",
						Type:    "external_command",
						Command: os.Args[0],
						Args:    []string{"-test.run=TestProxyExternalCommandExecutorHelper", "--", "responses-e2e-fail"},
						Env:     map[string]string{"LLM_TRACELAB_PROXY_EXTERNAL_EXECUTOR_HELPER": "1"},
					},
				},
			},
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

	firstResp, err := http.Post(proxyServer.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5","input":"first"}`))
	if err != nil {
		t.Fatalf("first response request: %v", err)
	}
	defer firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(firstResp.Body)
		t.Fatalf("first status = %d, want 200; body=%s", firstResp.StatusCode, string(body))
	}
	var first protocol.Response
	if err := json.NewDecoder(firstResp.Body).Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	secondBody := fmt.Sprintf(`{"model":"gpt-5","previous_response_id":%q,"input":"lookup","stream":true,"tools":[{"type":"function","name":"lookup_fail","parameters":{"type":"object"}}]}`, first.ID)
	secondReq, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", strings.NewReader(secondBody))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	secondReq.Header.Set("Content-Type", "application/json")
	secondResp, err := proxyServer.Client().Do(secondReq)
	if err != nil {
		t.Fatalf("second Do() error = %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("second status = %d, want 200 SSE; body=%s", secondResp.StatusCode, string(body))
	}
	if contentType := secondResp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	bodyBytes, err := io.ReadAll(secondResp.Body)
	if err != nil {
		t.Fatalf("read second SSE body: %v", err)
	}
	streamBody := string(bodyBytes)
	for _, event := range []string{
		"response.created",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.added",
		"response.output_item.done",
		"response.failed",
	} {
		if !strings.Contains(streamBody, "event: "+event+"\n") {
			t.Fatalf("stream body missing event %q:\n%s", event, streamBody)
		}
	}
	if strings.Contains(streamBody, "event: response.completed\n") {
		t.Fatalf("stream body unexpectedly completed:\n%s", streamBody)
	}
	if !strings.Contains(streamBody, `"type":"function_call_output"`) ||
		!strings.Contains(streamBody, `"call_id":"call_lookup_fail"`) ||
		!strings.Contains(streamBody, `"status":"failed"`) ||
		!strings.Contains(streamBody, `"message":"external command function executor failed`) {
		t.Fatalf("stream body missing failed function output/error:\n%s", streamBody)
	}
	addedIndex := strings.Index(streamBody, "event: response.output_item.added\n")
	doneIndex := strings.Index(streamBody, "event: response.output_item.done\n")
	failedIndex := strings.Index(streamBody, "event: response.failed\n")
	if addedIndex < 0 || doneIndex < 0 || failedIndex < 0 || addedIndex >= doneIndex || doneIndex >= failedIndex {
		t.Fatalf("stream event order mismatch:\n%s", streamBody)
	}
	if upstreamCalls != 3 {
		t.Fatalf("upstreamCalls = %d, want first + compact + stream tool call", upstreamCalls)
	}

	audits, err := waitForRequestAuditsWithStatus(st, 1, "failed", time.Second)
	if err != nil {
		t.Fatalf("waitForRequestAuditsWithStatus(failed) error = %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("failed audits = %d, want 1: %+v", len(audits), audits)
	}
	failedAudit := audits[0]
	if !strings.Contains(failedAudit.ErrorText, "external command function executor failed") {
		t.Fatalf("failed audit error = %q, want executor failure", failedAudit.ErrorText)
	}

	events, err := waitForExecutionEvents(st, 8, time.Second)
	if err != nil {
		t.Fatalf("waitForExecutionEvents() error = %v", err)
	}
	hasCompact := false
	hasToolFailed := false
	hasStreamFailed := false
	for _, event := range events {
		switch {
		case event.EventType == "response.compact" && event.Status == "auto_triggered":
			hasCompact = true
		case event.EventType == "response.tool_call" && event.Status == "failed" && event.DetailsJSON["tool_name"] == "lookup_fail" && event.DetailsJSON["stream"] == true:
			hasToolFailed = true
		case event.EventType == "response.stream" && event.Status == "failed":
			hasStreamFailed = true
		}
	}
	if !hasCompact || !hasToolFailed || !hasStreamFailed {
		t.Fatalf("execution events missing compact/tool_failed/stream_failed: compact=%v tool=%v stream=%v events=%+v", hasCompact, hasToolFailed, hasStreamFailed, events)
	}

	exchanges, err := waitForUpstreamExchanges(st, 3, time.Second)
	if err != nil {
		t.Fatalf("waitForUpstreamExchanges() error = %v", err)
	}
	if len(exchanges) < 3 {
		t.Fatalf("upstream exchanges len = %d, want at least 3: %+v", len(exchanges), exchanges)
	}
	secondAuditExchangeCount := 0
	for _, exchange := range exchanges {
		if exchange.RequestAuditID == failedAudit.ID {
			secondAuditExchangeCount++
		}
	}
	if secondAuditExchangeCount < 2 {
		t.Fatalf("second request upstream exchanges = %d, want compact + stream exchanges linked to failed audit %q: %+v", secondAuditExchangeCount, failedAudit.ID, exchanges)
	}
}

func TestHandlerResponsesServerModeStreamAutoCompactForcedWebSearchFallsBackToDeferred(t *testing.T) {
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	upstreamCalls := 0
	var searchChatBody map[string]any
	var finalChatBody map[string]any
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
		w.Header().Set("Content-Type", "application/json")
		switch upstreamCalls {
		case 1:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_first","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"first answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
		case 2:
			_, _ = io.WriteString(w, `{"id":"chatcmpl_auto_compact","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Search compact summary."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
		case 3:
			searchChatBody = chatBody
			_, _ = io.WriteString(w, `{"id":"chatcmpl_search","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_search_forced","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"llm tracelab\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":6,"completion_tokens":3,"total_tokens":9}}`)
		case 4:
			finalChatBody = chatBody
			_, _ = io.WriteString(w, `{"id":"chatcmpl_final","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"Forced search completed after deferred fallback."},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":5,"total_tokens":14}}`)
		default:
			http.Error(w, "unexpected extra call", http.StatusInternalServerError)
		}
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			Enabled:                     true,
			AutoCompact:                 true,
			CompactHistoryItemThreshold: 1,
		},
		Tools: config.ToolsConfig{
			WebSearch: config.WebSearchToolConfig{
				Enabled:    true,
				Provider:   "mock",
				MaxResults: 2,
			},
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

	firstResp, err := http.Post(proxyServer.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-5","input":"first"}`))
	if err != nil {
		t.Fatalf("first response request: %v", err)
	}
	defer firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(firstResp.Body)
		t.Fatalf("first status = %d, want 200; body=%s", firstResp.StatusCode, string(body))
	}
	var first protocol.Response
	if err := json.NewDecoder(firstResp.Body).Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	secondBody := fmt.Sprintf(`{"model":"gpt-5","previous_response_id":%q,"input":"search now","stream":true,"tools":[{"type":"web_search_preview"}],"tool_choice":"web_search"}`, first.ID)
	secondReq, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", strings.NewReader(secondBody))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	secondReq.Header.Set("Content-Type", "application/json")
	secondResp, err := proxyServer.Client().Do(secondReq)
	if err != nil {
		t.Fatalf("second Do() error = %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("second status = %d, want 200 SSE; body=%s", secondResp.StatusCode, string(body))
	}
	if contentType := secondResp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	bodyBytes, err := io.ReadAll(secondResp.Body)
	if err != nil {
		t.Fatalf("read second SSE body: %v", err)
	}
	streamBody := string(bodyBytes)
	for _, event := range []string{
		"response.created",
		"response.output_item.added",
		"response.output_item.done",
		"response.output_text.delta",
		"response.completed",
	} {
		if !strings.Contains(streamBody, "event: "+event+"\n") {
			t.Fatalf("stream body missing deferred event %q:\n%s", event, streamBody)
		}
	}
	if strings.Contains(streamBody, "response.function_call_arguments.delta") {
		t.Fatalf("stream body unexpectedly used incremental argument deltas after fallback:\n%s", streamBody)
	}
	if !strings.Contains(streamBody, `"type":"web_search_call"`) ||
		!strings.Contains(streamBody, `"call_id":"call_search_forced"`) ||
		!strings.Contains(streamBody, `"status":"completed"`) ||
		!strings.Contains(streamBody, "Forced search completed after deferred fallback.") {
		t.Fatalf("stream body missing deferred web_search output/final text:\n%s", streamBody)
	}
	if upstreamCalls != 4 {
		t.Fatalf("upstreamCalls = %d, want first + compact + search + final", upstreamCalls)
	}
	if searchChatBody == nil || finalChatBody == nil {
		t.Fatalf("missing captured deferred chat bodies search=%v final=%v", searchChatBody != nil, finalChatBody != nil)
	}
	if got := searchChatBody["tool_choice"]; got == nil {
		t.Fatalf("search chat tool_choice = nil, want forced web_search")
	}
	finalMessages, ok := finalChatBody["messages"].([]any)
	if !ok || len(finalMessages) < 4 {
		t.Fatalf("final chat messages = %#v, want compact context plus web_search tool loop", finalChatBody["messages"])
	}
	toolMessage, ok := finalMessages[len(finalMessages)-1].(map[string]any)
	if !ok || toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_search_forced" {
		t.Fatalf("final tool message mismatch: %#v", finalMessages[len(finalMessages)-1])
	}
	toolContent, _ := toolMessage["content"].(string)
	if !strings.Contains(toolContent, "Mock search result 1") || !strings.Contains(toolContent, "llm tracelab") {
		t.Fatalf("final tool content missing mock result: %q", toolContent)
	}

	completedAudits, err := waitForRequestAuditsWithStatus(st, 2, "completed", time.Second)
	if err != nil {
		t.Fatalf("waitForRequestAuditsWithStatus(completed) error = %v", err)
	}
	if len(completedAudits) < 2 {
		t.Fatalf("completed audits = %d, want first and fallback request: %+v", len(completedAudits), completedAudits)
	}
	secondAudit := completedAudits[1]
	events, err := waitForExecutionEvents(st, 9, time.Second)
	if err != nil {
		t.Fatalf("waitForExecutionEvents() error = %v", err)
	}
	hasFallback := false
	hasDeferredStarted := false
	hasDeferredCompleted := false
	hasCompact := false
	hasToolCompleted := false
	for _, event := range events {
		if event.RequestAuditID != secondAudit.ID && event.DetailsJSON["request_audit_id"] != secondAudit.ID {
			continue
		}
		switch {
		case event.EventType == "response.stream" && event.Status == "fallback":
			hasFallback = event.DetailsJSON["from_mode"] == "incremental" &&
				event.DetailsJSON["to_mode"] == "deferred" &&
				strings.Contains(event.Message, "auto compact tool combination requires deferred stream")
		case event.EventType == "response.stream" && event.Status == "started" && event.DetailsJSON["mode"] == "deferred":
			hasDeferredStarted = true
		case event.EventType == "response.stream" && event.Status == "completed" && event.DetailsJSON["mode"] == "deferred":
			hasDeferredCompleted = true
		case event.EventType == "response.compact" && event.Status == "auto_triggered":
			hasCompact = true
		case event.EventType == "response.tool_call" && event.Status == "completed" && event.DetailsJSON["tool_name"] == "web_search" && event.DetailsJSON["stream"] == false:
			hasToolCompleted = true
		}
	}
	if !hasFallback || !hasDeferredStarted || !hasDeferredCompleted || !hasCompact || !hasToolCompleted {
		t.Fatalf("events missing fallback/deferred/compact/tool completion: fallback=%v started=%v completed=%v compact=%v tool=%v events=%+v", hasFallback, hasDeferredStarted, hasDeferredCompleted, hasCompact, hasToolCompleted, events)
	}

	exchanges, err := waitForUpstreamExchanges(st, 4, time.Second)
	if err != nil {
		t.Fatalf("waitForUpstreamExchanges() error = %v", err)
	}
	secondAuditExchangeCount := 0
	for _, exchange := range exchanges {
		if exchange.RequestAuditID == secondAudit.ID {
			secondAuditExchangeCount++
		}
	}
	if secondAuditExchangeCount != 3 {
		t.Fatalf("second request upstream exchanges = %d, want compact + search + final linked to completed audit %q: %+v", secondAuditExchangeCount, secondAudit.ID, exchanges)
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
	tests := []struct {
		name string
		mode string
	}{
		{name: "default_mode", mode: ""},
		{name: "proxy_mode", mode: "proxy"},
		{name: "record_only_mode", mode: "record_only"},
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
							APIType:        "responses_native",
							Mode:           tt.mode,
							Capabilities: config.UpstreamCapabilitiesConfig{
								ChatCompletions: boolPtr(false),
							},
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
			if parsed.Header.Meta.Operation != "responses" {
				t.Fatalf("recorded operation = %q, want responses", parsed.Header.Meta.Operation)
			}
			if parsed.Header.Meta.SelectedUpstreamID != "openai-responses" {
				t.Fatalf("SelectedUpstreamID = %q, want openai-responses", parsed.Header.Meta.SelectedUpstreamID)
			}
		})
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

func waitForRequestAuditsWithStatus(st *store.Store, limit int, status string, timeout time.Duration) ([]*dao.RequestAudit, error) {
	deadline := time.Now().Add(timeout)
	var lastAudits []*dao.RequestAudit
	var lastErr error

	for {
		lastAudits, lastErr = st.EntClient().RequestAudit.Query().
			Where(requestaudit.StatusEQ(status)).
			Order(requestaudit.ByCreatedAt(), requestaudit.ByID()).
			All(context.Background())
		if lastErr == nil && len(lastAudits) >= limit {
			return lastAudits, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, lastErr
			}
			return lastAudits, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForExecutionEvents(st *store.Store, limit int, timeout time.Duration) ([]*dao.ExecutionEvent, error) {
	deadline := time.Now().Add(timeout)
	var lastEvents []*dao.ExecutionEvent
	var lastErr error

	for {
		lastEvents, lastErr = st.EntClient().ExecutionEvent.Query().
			Order(executionevent.ByOccurredAt(), executionevent.ByID()).
			All(context.Background())
		if lastErr == nil && len(lastEvents) >= limit {
			return lastEvents, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, lastErr
			}
			return lastEvents, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForUpstreamExchanges(st *store.Store, limit int, timeout time.Duration) ([]*dao.UpstreamExchange, error) {
	deadline := time.Now().Add(timeout)
	var lastExchanges []*dao.UpstreamExchange
	var lastErr error

	for {
		lastExchanges, lastErr = st.EntClient().UpstreamExchange.Query().
			Order(upstreamexchange.ByStartedAt(), upstreamexchange.ByID()).
			All(context.Background())
		if lastErr == nil && len(lastExchanges) >= limit {
			return lastExchanges, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, lastErr
			}
			return lastExchanges, nil
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
