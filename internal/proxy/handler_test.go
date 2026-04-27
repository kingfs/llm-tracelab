package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/recorder"
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

type nopReadCloser struct{ Reader *bytes.Buffer }

func (n nopReadCloser) Read(p []byte) (int, error) { return n.Reader.Read(p) }
func (nopReadCloser) Close() error                 { return nil }
