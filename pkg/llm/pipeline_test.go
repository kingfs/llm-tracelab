package llm

import (
	"net/http"
	"strings"
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

func TestResponsePipelineStreamParsesUsageFromLongResponsesCompletedLine(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderOpenAICompatible, "/v1/responses", true)
	filler := strings.Repeat("x", 70*1024)
	line := `data: {"type":"response.completed","response":{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"` +
		filler +
		`"}]}],"usage":{"input_tokens":7048,"output_tokens":28,"total_tokens":7076}}}` + "\n"

	pipeline.Feed([]byte(line[:1024]))
	pipeline.Feed([]byte(line[1024:]))

	usage, ok := pipeline.Usage()
	require.True(t, ok)
	assert.Equal(t, 7048, usage.PromptTokens)
	assert.Equal(t, 28, usage.CompletionTokens)
	assert.Equal(t, 7076, usage.TotalTokens)
}

func TestResponsePipelineResponsesCustomToolCallPreservesArguments(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderOpenAICompatible, "/v1/responses", true)
	pipeline.Feed([]byte(`data: {"type":"response.output_item.done","item":{"id":"ct_1","type":"custom_tool_call","call_id":"call_custom","name":"policy_guard","arguments":"{\"topic\":\"restricted\"}"}}` + "\n"))

	events := pipeline.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "llm.tool_call", events[0].Type)
	assert.Equal(t, "call_custom", events[0].Attributes["id"])
	assert.Equal(t, "custom_tool_call", events[0].Attributes["type"])
	assert.Equal(t, "policy_guard", events[0].Attributes["name"])
	assert.Equal(t, `{"topic":"restricted"}`, events[0].Attributes["arguments"])
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

func TestResponsePipelineVertexStream(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderVertexNative, "/v1/publishers/models:streamGenerateContent", true)
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

func TestResponsePipelineGoogleStreamEmitsSafetyBlocks(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderGoogleGenAI, "/v1beta/models:streamGenerateContent", true)
	pipeline.Feed([]byte(`data: {"promptFeedback":{"blockReason":"SAFETY"}}` + "\n"))
	pipeline.Feed([]byte(`data: {"candidates":[{"finishReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HATE_SPEECH","probability":"HIGH","blocked":true}]}]}` + "\n"))

	events := pipeline.Events()
	require.Len(t, events, 3)
	assert.Equal(t, "llm.output_block", events[0].Type)
	assert.Contains(t, events[0].Message, "blockReason")
	assert.Equal(t, "prompt_feedback", events[0].Attributes["kind"])
	assert.Equal(t, "llm.output_block", events[1].Type)
	assert.Equal(t, "safety", events[1].Attributes["kind"])
	assert.Equal(t, "HARM_CATEGORY_HATE_SPEECH", events[1].Attributes["category"])
	assert.Equal(t, "llm.output_block", events[2].Type)
	assert.Equal(t, "SAFETY", events[2].Message)
}

func TestResponsePipelineVertexStreamEmitsSafetyBlocks(t *testing.T) {
	pipeline := NewResponsePipeline(ProviderVertexNative, "/v1/publishers/models:streamGenerateContent", true)
	pipeline.Feed([]byte(`data: {"promptFeedback":{"blockReason":"SAFETY"}}` + "\n"))
	pipeline.Feed([]byte(`data: {"candidates":[{"finishReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HATE_SPEECH","probability":"HIGH","blocked":true}]}]}` + "\n"))

	events := pipeline.Events()
	require.Len(t, events, 3)
	assert.Equal(t, "llm.output_block", events[0].Type)
	assert.Contains(t, events[0].Message, "blockReason")
	assert.Equal(t, "prompt_feedback", events[0].Attributes["kind"])
	assert.Equal(t, "llm.output_block", events[1].Type)
	assert.Equal(t, "safety", events[1].Attributes["kind"])
	assert.Equal(t, "HARM_CATEGORY_HATE_SPEECH", events[1].Attributes["category"])
	assert.Equal(t, "llm.output_block", events[2].Type)
	assert.Equal(t, "SAFETY", events[2].Message)
}

func TestResponsePipelineStreamErrorsEmitProviderErrorBlocks(t *testing.T) {
	testCases := []struct {
		name     string
		provider string
		endpoint string
		line     string
		wantText string
	}{
		{
			name:     "openai responses",
			provider: ProviderOpenAICompatible,
			endpoint: "/v1/responses",
			line:     `data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error","code":"stream_aborted"}}}` + "\n",
			wantText: "stream aborted",
		},
		{
			name:     "anthropic",
			provider: ProviderAnthropic,
			endpoint: "/v1/messages",
			line:     `data: {"type":"error","error":{"type":"overloaded_error","message":"stream overloaded"}}` + "\n",
			wantText: "stream overloaded",
		},
		{
			name:     "google",
			provider: ProviderGoogleGenAI,
			endpoint: "/v1beta/models:streamGenerateContent",
			line:     `data: {"error":{"code":429,"message":"stream quota exceeded","status":"RESOURCE_EXHAUSTED"}}` + "\n",
			wantText: "stream quota exceeded",
		},
		{
			name:     "vertex",
			provider: ProviderVertexNative,
			endpoint: "/v1/publishers/models:streamGenerateContent",
			line:     `data: {"error":{"code":403,"message":"vertex stream denied","status":"PERMISSION_DENIED"}}` + "\n",
			wantText: "vertex stream denied",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := NewResponsePipeline(tt.provider, tt.endpoint, true)
			pipeline.Feed([]byte(tt.line))

			events := pipeline.Events()
			require.Len(t, events, 1)
			assert.Equal(t, "llm.output_block", events[0].Type)
			assert.Equal(t, "provider_error", events[0].Attributes["kind"])
			assert.Contains(t, events[0].Message, tt.wantText)
		})
	}
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
