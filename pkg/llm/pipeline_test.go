package llm

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractUsageFromJSON(t *testing.T) {
	usage, ok := ExtractUsageFromJSON([]byte(`{"usage":{"prompt_tokens":26,"completion_tokens":93,"total_tokens":119,"prompt_tokens_details":{"cached_tokens":3}}}`))
	require.True(t, ok)
	assert.Equal(t, 26, usage.PromptTokens)
	assert.Equal(t, 93, usage.CompletionTokens)
	assert.Equal(t, 119, usage.TotalTokens)
	require.NotNil(t, usage.PromptTokenDetails)
	assert.Equal(t, 3, usage.PromptTokenDetails.CachedTokens)
}

func TestExtractUsageFromJSONResponsesCompletedEvent(t *testing.T) {
	usage, ok := ExtractUsageFromJSON([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":7048,"output_tokens":28,"total_tokens":7076}}}`))
	require.True(t, ok)
	assert.Equal(t, 7048, usage.PromptTokens)
	assert.Equal(t, 28, usage.CompletionTokens)
	assert.Equal(t, 7076, usage.TotalTokens)
}

func TestExtractUsageFromJSONGoogleUsageMetadata(t *testing.T) {
	usage, ok := ExtractUsageFromJSON([]byte(`{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}}`))
	require.True(t, ok)
	assert.Equal(t, 3, usage.PromptTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
	assert.Equal(t, 10, usage.TotalTokens)
}

func TestResponsePipelineStream(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderOpenAICompatible, "/v1/responses", true)
	pipeline.Feed([]byte("event: response.completed\n"))
	pipeline.Feed([]byte(`data: {"type":"response.output_text.delta","delta":"hello"}` + "\n"))
	pipeline.Feed([]byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":7048,"output_tokens":28,"total_tokens":7076}}}` + "\n"))
	usage, ok := pipeline.Usage()
	require.True(t, ok)
	assert.Equal(t, 7048, usage.PromptTokens)
	assert.Equal(t, 28, usage.CompletionTokens)
	assert.Equal(t, 7076, usage.TotalTokens)
	events := pipeline.Events()
	require.NotEmpty(t, events)
	assert.Equal(t, "llm.output_text.delta", events[0].Type)
	assert.Equal(t, "hello", events[0].Message)
	assert.Equal(t, "llm.usage", events[len(events)-1].Type)
}

func TestResponsePipelineNonStream(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderOpenAICompatible, "/v1/responses", false)
	pipeline.Feed([]byte(`{"id":"resp_123","usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}`))
	pipeline.Finalize()
	usage, ok := pipeline.Usage()
	require.True(t, ok)
	assert.Equal(t, 10, usage.PromptTokens)
	assert.Equal(t, 4, usage.CompletionTokens)
	assert.Equal(t, 14, usage.TotalTokens)
}

func TestResponsePipelineGoogleStream(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderGoogleGenAI, "/v1beta/models:streamGenerateContent", true)
	pipeline.Feed([]byte(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}` + "\n"))
	pipeline.Feed([]byte(`data: {"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}}` + "\n"))

	usage, ok := pipeline.Usage()
	require.True(t, ok)
	assert.Equal(t, 3, usage.PromptTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
	assert.Equal(t, 10, usage.TotalTokens)
	events := pipeline.Events()
	require.NotEmpty(t, events)
	assert.Equal(t, "llm.output_text.delta", events[0].Type)
}

func TestDetectStreamingResponse(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "text/event-stream")
	assert.True(t, DetectStreamingResponse(header))

	header = http.Header{}
	header.Set("Transfer-Encoding", "chunked")
	assert.True(t, DetectStreamingResponse(header))

	header = http.Header{}
	header.Set("Content-Type", "application/json")
	assert.False(t, DetectStreamingResponse(header))
}
