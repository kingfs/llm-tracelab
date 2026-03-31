package proxy

import (
	"bytes"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/recorder"
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

type nopReadCloser struct{ Reader *bytes.Buffer }

func (n nopReadCloser) Read(p []byte) (int, error) { return n.Reader.Read(p) }
func (nopReadCloser) Close() error                 { return nil }
