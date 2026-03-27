package proxy

import (
	"testing"

	"github.com/kingfs/llm-tracelab/internal/recorder"
)

func TestExtractUsageFromJSONChatCompletions(t *testing.T) {
	data := []byte(`{"usage":{"prompt_tokens":26,"completion_tokens":93,"total_tokens":119,"prompt_tokens_details":{"cached_tokens":3}}}`)

	usage, ok := extractUsageFromJSON(data)
	if !ok {
		t.Fatal("extractUsageFromJSON() ok = false, want true")
	}
	if usage.PromptTokens != 26 || usage.CompletionTokens != 93 || usage.TotalTokens != 119 {
		t.Fatalf("usage = %+v, want prompt=26 completion=93 total=119", usage)
	}
	if usage.PromptTokenDetails == nil || usage.PromptTokenDetails.CachedTokens != 3 {
		t.Fatalf("PromptTokenDetails = %+v, want cached_tokens=3", usage.PromptTokenDetails)
	}
}

func TestExtractUsageFromJSONResponsesCompletedEvent(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":7048,"output_tokens":28,"total_tokens":7076}}}`)

	usage, ok := extractUsageFromJSON(data)
	if !ok {
		t.Fatal("extractUsageFromJSON() ok = false, want true")
	}
	if usage.PromptTokens != 7048 || usage.CompletionTokens != 28 || usage.TotalTokens != 7076 {
		t.Fatalf("usage = %+v, want prompt=7048 completion=28 total=7076", usage)
	}
}

func TestUsageSnifferStreamResponsesCompletedEvent(t *testing.T) {
	var usage recorder.UsageInfo
	sniffer := UsageSniffer{
		Usage:    &usage,
		IsStream: true,
	}

	sniffer.sniffStream([]byte("event: response.completed\n"))
	sniffer.sniffStream([]byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":7048,"output_tokens":28,"total_tokens":7076}}}` + "\n"))

	if usage.PromptTokens != 7048 || usage.CompletionTokens != 28 || usage.TotalTokens != 7076 {
		t.Fatalf("usage = %+v, want prompt=7048 completion=28 total=7076", usage)
	}
}

func TestExtractUsageFromTailResponsesNonStream(t *testing.T) {
	var usage recorder.UsageInfo

	extractUsageFromTail([]byte(`{"id":"resp_123","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`), &usage)

	if usage.PromptTokens != 10 || usage.CompletionTokens != 4 || usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v, want prompt=10 completion=4 total=14", usage)
	}
}
