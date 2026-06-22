package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	responsesruntime "github.com/kingfs/llm-tracelab/internal/responses/runtime"
	"github.com/kingfs/llm-tracelab/internal/router"
)

func TestResponsesTokenizeEstimatorOptionDisabledByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
	}))
	defer upstream.Close()

	cfg := tokenizeCounterTestConfig(upstream.URL, false)
	rtr := newTokenizeCounterTestRouter(t, cfg)

	option, err := responsesTokenizeEstimatorOption(cfg, rtr, upstream.Client())
	if err != nil {
		t.Fatalf("responsesTokenizeEstimatorOption() error = %v", err)
	}
	if option != nil {
		t.Fatalf("responsesTokenizeEstimatorOption() = non-nil, want nil when profile tokenize_counter is disabled")
	}
}

func TestResponsesProviderTokenizeCounterCallsConfiguredUpstreamTokenize(t *testing.T) {
	const secret = "sk-test-tokenize"
	var gotPath string
	var gotAuth string
	var gotCustom string
	var gotBody struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom-Provider")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode tokenize request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"count":42}`)
	}))
	defer upstream.Close()

	cfg := tokenizeCounterTestConfig(upstream.URL, true)
	cfg.Upstreams[0].Upstream.ApiKey = secret
	cfg.Upstreams[0].Upstream.Headers = map[string]string{"X-Custom-Provider": "configured"}
	rtr := newTokenizeCounterTestRouter(t, cfg)
	counter, err := newResponsesProviderTokenizeCounter(cfg, rtr, upstream.Client())
	if err != nil {
		t.Fatalf("newResponsesProviderTokenizeCounter() error = %v", err)
	}
	if counter == nil {
		t.Fatal("newResponsesProviderTokenizeCounter() = nil, want counter")
	}

	tokens, err := counter.CountChatPromptTokens(responsesruntime.ChatPromptTokenCountRequest{
		Model: "gpt-4o-mini",
		Messages: []responsesruntime.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("CountChatPromptTokens() error = %v", err)
	}
	if tokens != 42 {
		t.Fatalf("CountChatPromptTokens() = %d, want 42", tokens)
	}
	if gotPath != "/tokenize" {
		t.Fatalf("tokenize path = %q, want /tokenize", gotPath)
	}
	if gotAuth != "Bearer "+secret {
		t.Fatalf("Authorization = %q, want bearer secret", gotAuth)
	}
	if gotCustom != "configured" {
		t.Fatalf("X-Custom-Provider = %q, want configured", gotCustom)
	}
	if gotBody.Model != "upstream-gpt-4o-mini" {
		t.Fatalf("tokenize model = %q, want upstream-gpt-4o-mini", gotBody.Model)
	}
	if !strings.Contains(gotBody.Prompt, "hello") {
		t.Fatalf("tokenize prompt = %q, want serialized chat prompt", gotBody.Prompt)
	}
}

func TestResponsesProviderTokenizeCounterFailureFallsBackWithoutLeakingSecret(t *testing.T) {
	const secret = "sk-test-tokenize"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider body includes "+secret, http.StatusUnauthorized)
	}))
	defer upstream.Close()

	cfg := tokenizeCounterTestConfig(upstream.URL, true)
	cfg.Upstreams[0].Upstream.ApiKey = secret
	rtr := newTokenizeCounterTestRouter(t, cfg)
	counter, err := newResponsesProviderTokenizeCounter(cfg, rtr, upstream.Client())
	if err != nil {
		t.Fatalf("newResponsesProviderTokenizeCounter() error = %v", err)
	}

	_, err = counter.CountChatPromptTokens(responsesruntime.ChatPromptTokenCountRequest{Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("CountChatPromptTokens() error = nil, want status error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "provider body includes") {
		t.Fatalf("CountChatPromptTokens() error %q leaks provider body or secret", err)
	}

	req := protocol.CreateResponseRequest{
		Model: "gpt-4o-mini",
		Input: "hello from fallback",
	}
	withTokenize := responsesruntime.NewAdapterBackedTokenEstimator(counter)
	fallback := responsesruntime.NewAdapterBackedTokenEstimator(nil)
	got := withTokenize.EstimateResponsePromptTokens(req, nil, nil, false)
	want := fallback.EstimateResponsePromptTokens(req, nil, nil, false)
	if got != want || got <= 0 {
		t.Fatalf("fallback estimate = %d, want %d and positive", got, want)
	}
}

func tokenizeCounterTestConfig(baseURL string, enabled bool) *config.Config {
	tokenize := true
	upstreamCfg := config.UpstreamConfig{
		BaseURL:        baseURL + "/v1",
		ProviderPreset: "openai",
		Capabilities: config.UpstreamCapabilitiesConfig{
			Tokenize: &tokenize,
		},
	}
	cfg := &config.Config{}
	cfg.ResponsesServer.ModelProfiles = []config.ResponsesModelProfileConfig{{
		Pattern:       "gpt-4o*",
		UpstreamModel: "upstream-gpt-4o-mini",
		TokenizeCounter: config.ResponsesTokenizeCounterConfig{
			Enabled:    enabled,
			UpstreamID: "primary",
			Timeout:    time.Second,
		},
	}}
	cfg.Upstreams = []config.UpstreamTargetConfig{{
		ID:             "primary",
		ModelDiscovery: "static_only",
		StaticModels:   []string{"gpt-4o-mini"},
		Upstream:       upstreamCfg,
	}}
	return cfg
}

func newTokenizeCounterTestRouter(t *testing.T, cfg *config.Config) *router.Router {
	t.Helper()
	rtr, err := router.New(cfg, nil)
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("router.Initialize() error = %v", err)
	}
	return rtr
}
