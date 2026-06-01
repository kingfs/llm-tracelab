package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/recorder"
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
	got := redactRoutingBaseURL(raw)
	if strings.Contains(got, "secret") || strings.Contains(got, "api_key=abc") || strings.Contains(got, "token=def") || strings.Contains(got, "signature=sig") {
		t.Fatalf("redactRoutingBaseURL leaked sensitive value: %q", got)
	}
	for _, want := range []string{"user:REDACTED@", "api_key=REDACTED", "token=REDACTED", "signature=REDACTED", "model=gpt-5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redactRoutingBaseURL() = %q, missing %q", got, want)
		}
	}
}

func TestCandidateEventAttributesRedactsBaseURL(t *testing.T) {
	attrs := candidateEventAttributes([]router.CandidateDecision{{
		ID:             "primary",
		ProviderPreset: "openai",
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
