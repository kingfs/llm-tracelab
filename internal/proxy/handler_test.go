package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/recorder"
	"github.com/kingfs/llm-tracelab/internal/redaction"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/llm"
)

func TestUsageSnifferUsesLLMPipelineForStreamUsage(t *testing.T) {
	var usage recorder.UsageInfo
	sniffer := UsageSniffer{
		Source:   nopReadCloser{Reader: bytes.NewBufferString(`data: {"type":"response.completed","response":{"usage":{"input_tokens":7048,"output_tokens":28,"total_tokens":7076}}}` + "\n")},
		Usage:    &usage,
		Pipeline: llm.NewResponsePipeline(llm.ProviderOpenAICompatible, "/v1/responses", true),
	}

	buf := make([]byte, 512)
	if _, err := sniffer.Read(buf); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if usage.PromptTokens != 7048 || usage.CompletionTokens != 28 || usage.TotalTokens != 7076 {
		t.Fatalf("usage = %+v, want prompt=7048 completion=28 total=7076", usage)
	}
}

func TestHandlerWithNoUpstreamsServesEmptyModelList(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{}
	cfg.Debug.OutputDir = t.TempDir()
	cfg.Trace.OutputDir = cfg.Debug.OutputDir

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler(empty upstreams) error = %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("GET /v1/models body = %s, want empty data array", rec.Body.String())
	}
}

func TestUsageSnifferCloseFinalizesNonStreamUsage(t *testing.T) {
	var usage recorder.UsageInfo
	sniffer := UsageSniffer{
		Source:   nopReadCloser{},
		Usage:    &usage,
		Pipeline: llm.NewResponsePipeline(llm.ProviderOpenAICompatible, "/v1/responses", false),
	}

	sniffer.Pipeline.Feed([]byte(`{"id":"resp_123","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`))
	if err := sniffer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if usage.PromptTokens != 10 || usage.CompletionTokens != 4 || usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v, want prompt=10 completion=4 total=14", usage)
	}
}

func TestEnsureStreamOptionsOnlyAppliesToChatCompletions(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","stream":true}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	ensureStreamOptions(req)

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := payload["stream_options"]; ok {
		t.Fatalf("stream_options unexpectedly injected for responses payload: %s", string(body))
	}
}

func TestRedactRoutingBaseURLRemovesCredentialsAndSensitiveQuery(t *testing.T) {
	raw := "https://user:secret@example.com/v1?api_key=abc&token=def&model=gpt-5&signature=sig"
	got := redaction.DisplayURL(raw)
	if strings.Contains(got, "secret") || strings.Contains(got, "api_key=abc") || strings.Contains(got, "token=def") || strings.Contains(got, "signature=sig") {
		t.Fatalf("DisplayURL leaked sensitive value: %q", got)
	}
	for _, want := range []string{"user:REDACTED@", "api_key=REDACTED", "token=REDACTED", "signature=REDACTED", "model=gpt-5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("DisplayURL() = %q, missing %q", got, want)
		}
	}
}

func TestMCPHostedExecutorOptionsMapsToolConfig(t *testing.T) {
	disabled := false
	options := mcpHostedExecutorOptions(config.MCPToolConfig{
		Enabled:          true,
		DefaultTimeoutMS: 2500,
		MaxResultBytes:   4096,
		Servers: []config.MCPToolServerConfig{{
			ID:             "docs",
			Label:          "Docs",
			URL:            "https://mcp.example.com/mcp",
			BearerTokenEnv: "DOCS_MCP_TOKEN",
			EnabledTools:   []string{"search", "fetch"},
			DisabledTools:  []string{"delete"},
		}, {
			ID:      "disabled",
			URL:     "https://disabled.example.com/mcp",
			Enabled: &disabled,
		}},
	})

	if !options.Enabled {
		t.Fatalf("options.Enabled = false, want true")
	}
	if len(options.Servers) != 2 {
		t.Fatalf("len(options.Servers) = %d, want 2", len(options.Servers))
	}
	first := options.Servers[0]
	if first.ID != "docs" || first.Label != "Docs" || first.URL != "https://mcp.example.com/mcp" || first.BearerTokenEnv != "DOCS_MCP_TOKEN" || !first.Enabled {
		t.Fatalf("first server = %+v, want mapped enabled server", first)
	}
	if first.Timeout != 2500*time.Millisecond || first.MaxResultBytes != 4096 {
		t.Fatalf("first server limits = %s/%d, want 2500ms/4096", first.Timeout, first.MaxResultBytes)
	}
	if strings.Join(first.AllowedTools, ",") != "search,fetch" || strings.Join(first.DeniedTools, ",") != "delete" {
		t.Fatalf("first server filters = %+v/%+v", first.AllowedTools, first.DeniedTools)
	}
	if options.Servers[1].Enabled {
		t.Fatalf("second server Enabled = true, want false")
	}
}

func TestMCPHostedExecutorOptionsDisabledWithoutEnabledServers(t *testing.T) {
	disabled := false
	options := mcpHostedExecutorOptions(config.MCPToolConfig{
		Enabled: true,
		Servers: []config.MCPToolServerConfig{{
			ID:      "disabled",
			Enabled: &disabled,
		}},
	})

	if options.Enabled {
		t.Fatalf("options.Enabled = true, want false without enabled servers")
	}
}

func TestResponsesCodexCompatHTTPOptionsMapsEnabledWebSearch(t *testing.T) {
	injectWhenAbsent := true
	preserveClientTools := true
	cfg := &config.Config{}
	cfg.ResponsesServer.CodexCompat.Enabled = true
	cfg.ResponsesServer.CodexCompat.InjectWhenToolsAbsent = &injectWhenAbsent
	cfg.ResponsesServer.CodexCompat.PreserveClientTools = &preserveClientTools
	cfg.ResponsesServer.CodexCompat.AutoInjectHostedTools = []string{"web_search_preview", "web_search", "mcp"}
	cfg.Tools.WebSearch.Enabled = true
	cfg.Tools.WebSearch.Provider = "mock"
	cfg.Tools.WebSearch.MaxResults = 7
	cfg.Tools.MCP.Enabled = true

	options := responsesCodexCompatHTTPOptions(cfg)

	if !options.Enabled || !options.InjectWhenToolsAbsent || !options.PreserveClientTools || options.DefaultToolChoice != "auto" {
		t.Fatalf("codex compat options = %+v", options)
	}
	if len(options.AvailableHostedTools) != 1 {
		t.Fatalf("available hosted tools = %#v, want one web_search tool", options.AvailableHostedTools)
	}
	tool := options.AvailableHostedTools[0]
	if tool.Type != "web_search" || tool.MaxNumResults != 7 {
		t.Fatalf("available hosted tool = %#v, want web_search max_num_results=7", tool)
	}
}

func TestResponsesCodexCompatHTTPOptionsDisabledWithoutWebSearch(t *testing.T) {
	injectWhenAbsent := true
	cfg := &config.Config{}
	cfg.ResponsesServer.CodexCompat.Enabled = true
	cfg.ResponsesServer.CodexCompat.InjectWhenToolsAbsent = &injectWhenAbsent
	cfg.ResponsesServer.CodexCompat.AutoInjectHostedTools = []string{"web_search"}
	cfg.Tools.WebSearch.Enabled = false

	options := responsesCodexCompatHTTPOptions(cfg)

	if !options.Enabled || !options.InjectWhenToolsAbsent {
		t.Fatalf("codex compat options = %+v", options)
	}
	if len(options.AvailableHostedTools) != 0 {
		t.Fatalf("available hosted tools = %#v, want none when web_search is disabled", options.AvailableHostedTools)
	}
}

func TestCandidateEventAttributesRedactsBaseURL(t *testing.T) {
	attrs := candidateEventAttributes([]router.CandidateDecision{{
		ID:             "primary",
		ProviderPreset: "openai",
		APIType:        "chat_completions",
		Mode:           "responses_server",
		BaseURL:        "https://user:secret@example.com/v1?api_key=abc&region=us",
		SupportsPath:   true,
		SupportsModel:  true,
		Selectable:     true,
	}})
	if len(attrs) != 1 {
		t.Fatalf("len(attrs) = %d, want 1", len(attrs))
	}
	baseURL, _ := attrs[0]["base_url"].(string)
	if strings.Contains(baseURL, "secret") || strings.Contains(baseURL, "abc") {
		t.Fatalf("candidateEventAttributes leaked sensitive base_url: %q", baseURL)
	}
	if !strings.Contains(baseURL, "api_key=REDACTED") || !strings.Contains(baseURL, "region=us") {
		t.Fatalf("candidateEventAttributes base_url = %q, want redacted api_key and preserved region", baseURL)
	}
	if attrs[0]["api_type"] != "chat_completions" || attrs[0]["mode"] != "responses_server" {
		t.Fatalf("candidateEventAttributes API surface attrs = %+v", attrs[0])
	}
}

func TestCredentialRoutingEventFieldsAreAdditiveAndSafe(t *testing.T) {
	credentialSelectable := false
	attrs := candidateEventAttributes([]router.CandidateDecision{{
		ID:                     "route-channel-a-cred-1",
		RouteTargetID:          "channel-a:cred-1",
		ChannelID:              "channel-a",
		CredentialID:           "cred-1",
		CredentialHint:         "Bearer sk-leaky-token",
		CredentialHealthState:  "open",
		CredentialSelectable:   &credentialSelectable,
		CredentialFilterReason: "credential_health_open",
		ProviderPreset:         "openai",
		SupportsPath:           true,
		SupportsModel:          true,
		Selectable:             false,
		FilterReason:           "credential_health_open",
	}})
	if len(attrs) != 1 {
		t.Fatalf("len(attrs) = %d, want 1", len(attrs))
	}
	got := attrs[0]
	for key, want := range map[string]interface{}{
		"route_target_id":          "channel-a:cred-1",
		"channel_id":               "channel-a",
		"credential_id":            "cred-1",
		"credential_hint":          "Bearer REDACTED",
		"credential_health_state":  "open",
		"credential_selectable":    false,
		"credential_filter_reason": "credential_health_open",
	} {
		if got[key] != want {
			t.Fatalf("attrs[%q] = %#v, want %#v; attrs=%+v", key, got[key], want, got)
		}
	}
	if strings.Contains(fmt.Sprint(got), "sk-leaky-token") {
		t.Fatalf("credential event attrs leaked secret: %+v", got)
	}

	legacyAttrs := candidateEventAttributes([]router.CandidateDecision{{
		ID:            "primary",
		SupportsPath:  true,
		SupportsModel: true,
		Selectable:    true,
	}})
	for _, key := range []string{"route_target_id", "channel_id", "credential_id", "credential_hint"} {
		if _, ok := legacyAttrs[0][key]; ok {
			t.Fatalf("legacy attrs unexpectedly included %s: %+v", key, legacyAttrs[0])
		}
	}
}

func TestRoutingDecisionEventsIncludeCredentialFields(t *testing.T) {
	selectable := true
	decision := &router.DecisionTrace{
		ModelName:              "gpt-5",
		Endpoint:               "/v1/responses",
		Policy:                 router.PolicyFirstAvailable,
		SelectedID:             "route-channel-a-cred-1",
		SelectedRouteTargetID:  "channel-a:cred-1",
		SelectedChannelID:      "channel-a",
		SelectedCredentialID:   "cred-1",
		SelectedCredentialHint: "acct-1234",
		Candidates: []router.CandidateDecision{{
			ID:                   "route-channel-a-cred-1",
			RouteTargetID:        "channel-a:cred-1",
			ChannelID:            "channel-a",
			CredentialID:         "cred-1",
			CredentialHint:       "acct-1234",
			CredentialSelectable: &selectable,
			SupportsPath:         true,
			SupportsModel:        true,
			Selectable:           true,
		}},
		StickyEvents: []router.StickyDecision{{
			Status:         "hit",
			Key:            "sticky-secret",
			TargetID:       "route-channel-a-cred-1",
			RouteTargetID:  "channel-a:cred-1",
			ChannelID:      "channel-a",
			CredentialID:   "cred-1",
			CredentialHint: "acct-1234",
		}},
	}

	events := routingDecisionEvents(decision, time.Now())
	selected := eventAttrsByType(t, events, "routing.selected")
	if selected["route_target_id"] != "channel-a:cred-1" || selected["channel_id"] != "channel-a" || selected["credential_id"] != "cred-1" || selected["credential_hint"] != "acct-1234" {
		t.Fatalf("selected attrs missing credential fields: %+v", selected)
	}
	sticky := eventAttrsByType(t, events, "routing.sticky.hit")
	if sticky["route_target_id"] != "channel-a:cred-1" || sticky["channel_id"] != "channel-a" || sticky["credential_id"] != "cred-1" {
		t.Fatalf("sticky attrs missing credential fields: %+v", sticky)
	}
	if _, ok := sticky["sticky_key"]; ok {
		t.Fatalf("sticky attrs leaked raw sticky key: %+v", sticky)
	}
}

func TestRoutingOutcomeEventRedactsCredentialError(t *testing.T) {
	selection := &router.Selection{
		Target: &router.Target{ID: "route-channel-a-cred-1"},
		Request: router.RequestFeatures{
			ModelName: "gpt-5",
		},
		Credential: router.CredentialDecisionInfo{
			RouteTargetID:  "channel-a:cred-1",
			ChannelID:      "channel-a",
			CredentialID:   "cred-1",
			CredentialHint: "acct-1234",
		},
	}
	event := routingOutcomeEvent(selection, http.StatusUnauthorized, time.Millisecond, "provider returned Authorization: Bearer sk-live-token")
	if event.Attributes["route_target_id"] != "channel-a:cred-1" || event.Attributes["channel_id"] != "channel-a" || event.Attributes["credential_id"] != "cred-1" {
		t.Fatalf("outcome attrs missing credential fields: %+v", event.Attributes)
	}
	if got := fmt.Sprint(event.Attributes["error"]); strings.Contains(got, "sk-live-token") || !strings.Contains(got, "REDACTED") {
		t.Fatalf("outcome error not safely redacted: %q", got)
	}
}

func TestLimitDecisionScopes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("X-Limit-Bucket", "raw-secret-bucket")

	h := &Handler{cfg: &config.Config{}}
	h.cfg.Limits.Scope = "header"
	h.cfg.Limits.ChannelKeyHeader = "X-Limit-Bucket"
	decision, ok := h.preSelectionLimitDecision(req)
	if !ok || decision.Scope != "header" || decision.Key != "header:raw-secret-bucket" {
		t.Fatalf("preSelectionLimitDecision() = %+v, %v", decision, ok)
	}

	h.cfg.Limits.Scope = "credential"
	selection := &router.Selection{Credential: router.CredentialDecisionInfo{
		RouteTargetID: "channel-a:cred-1",
		ChannelID:     "channel-a",
		CredentialID:  "cred-1",
	}}
	decision, ok = h.postSelectionLimitDecision(selection)
	if !ok || decision.Scope != "credential" || decision.Key != "credential:channel-a:cred-1" || decision.Identity.CredentialID != "cred-1" {
		t.Fatalf("postSelectionLimitDecision() = %+v, %v", decision, ok)
	}
}

func TestLimitEventAttributesAreScopedAndSafe(t *testing.T) {
	attrs := limitEventAttributes(config.LimitConfig{MaxConcurrent: 1, MaxQueued: 2}, http.StatusTooManyRequests, limitDecision{
		Scope: "header",
		Key:   "header:raw-secret-bucket",
	})
	if attrs["scope"] != "header" {
		t.Fatalf("scope = %v, want header", attrs["scope"])
	}
	if strings.Contains(fmt.Sprint(attrs), "raw-secret-bucket") {
		t.Fatalf("limit attrs leaked raw key: %+v", attrs)
	}
	if attrs["limit_key_fingerprint"] == "" || attrs["max_concurrent"] != 1 || attrs["max_queued"] != 2 {
		t.Fatalf("limit attrs missing expected fields: %+v", attrs)
	}

	credentialAttrs := limitEventAttributes(config.LimitConfig{MaxConcurrent: 1}, http.StatusTooManyRequests, limitDecision{
		Scope: "credential",
		Key:   "credential:channel-a:cred-1",
		Identity: router.CredentialDecisionInfo{
			RouteTargetID: "channel-a:cred-1",
			ChannelID:     "channel-a",
			CredentialID:  "cred-1",
		},
	})
	if credentialAttrs["route_target_id"] != "channel-a:cred-1" || credentialAttrs["channel_id"] != "channel-a" || credentialAttrs["credential_id"] != "cred-1" {
		t.Fatalf("credential attrs missing identity: %+v", credentialAttrs)
	}
}

func eventAttrsByType(t *testing.T, events []recorder.RecordEvent, eventType string) map[string]interface{} {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event.Attributes
		}
	}
	t.Fatalf("event %s not found: %+v", eventType, events)
	return nil
}

func TestHandlerRejectsMissingProxyTokenBeforeRouting(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, authVerifier: proxyTestVerifier{}}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"input":"hello"}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != `Bearer realm="llm-tracelab-proxy"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

type proxyTestVerifier struct{}

func (proxyTestVerifier) VerifyToken(context.Context, string) (auth.Principal, bool, error) {
	return auth.Principal{}, false, nil
}

func TestHandlerTransportVerifiesUpstreamTLSByDefault(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = "https://api.openai.com/v1"
	cfg.Upstream.ApiKey = "sk-test"
	cfg.Upstream.ProviderPreset = "openai"
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	transport, ok := handler.proxy.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", handler.proxy.Transport)
	}
	if transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("proxy transport disables upstream TLS certificate verification")
	}
}

func TestRetryBackoffCapsAtFiveSeconds(t *testing.T) {
	if got := retryBackoff(0); got != 250*time.Millisecond {
		t.Fatalf("retryBackoff(0) = %s, want 250ms", got)
	}
	if got := retryBackoff(5); got != 5*time.Second {
		t.Fatalf("retryBackoff(5) = %s, want 5s", got)
	}
	if got := retryBackoff(20); got != 5*time.Second {
		t.Fatalf("retryBackoff(20) = %s, want 5s", got)
	}
}

func TestRetryBackoffWithJitterStaysWithinBounds(t *testing.T) {
	base := retryBackoff(2)
	for i := 0; i < 100; i++ {
		got := retryBackoffWithJitter(2)
		if got < base {
			t.Fatalf("retryBackoffWithJitter(2) = %s, below base %s", got, base)
		}
		if got > base+base/5 {
			t.Fatalf("retryBackoffWithJitter(2) = %s, above max %s", got, base+base/5)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 5, 19, 3, 20, 0, 0, time.UTC)

	seconds := parseRetryAfter("2", now)
	if seconds == nil || *seconds != 2*time.Second {
		t.Fatalf("parseRetryAfter seconds = %v, want 2s", seconds)
	}

	date := parseRetryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now)
	if date == nil || *date != 3*time.Second {
		t.Fatalf("parseRetryAfter date = %v, want 3s", date)
	}

	if got := parseRetryAfter("invalid", now); got != nil {
		t.Fatalf("parseRetryAfter invalid = %v, want nil", *got)
	}
}

func TestRetryDelayHonorsRetryAfterWithinDeadline(t *testing.T) {
	retryAfter := 2 * time.Second
	got := retryDelay(0, &retryAfter, time.Now().Add(10*time.Second))
	if got < retryAfter {
		t.Fatalf("retryDelay = %s, want at least Retry-After %s", got, retryAfter)
	}

	shortDeadline := time.Now().Add(100 * time.Millisecond)
	got = retryDelay(0, &retryAfter, shortDeadline)
	if got > 150*time.Millisecond {
		t.Fatalf("retryDelay with short deadline = %s, want capped near deadline", got)
	}
}

func TestRetryWaitSlotsBoundConcurrentWaiters(t *testing.T) {
	for {
		select {
		case <-upstreamRetryWaitSlots:
		default:
			goto drained
		}
	}

drained:
	acquired := 0
	for i := 0; i < upstreamRetryWaitCapacity; i++ {
		if !tryAcquireRetryWaitSlot() {
			t.Fatalf("tryAcquireRetryWaitSlot() = false at slot %d", i)
		}
		acquired++
	}
	if tryAcquireRetryWaitSlot() {
		t.Fatalf("tryAcquireRetryWaitSlot() = true after capacity exhausted")
	}
	for i := 0; i < acquired; i++ {
		releaseRetryWaitSlot()
	}
	if !tryAcquireRetryWaitSlot() {
		t.Fatalf("tryAcquireRetryWaitSlot() = false after release")
	}
	releaseRetryWaitSlot()
}

type nopReadCloser struct{ Reader *bytes.Buffer }

func (n nopReadCloser) Read(p []byte) (int, error) { return n.Reader.Read(p) }
func (nopReadCloser) Close() error                 { return nil }
