package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOpenAIResponsesStreamResponse(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.reasoning_text.delta","delta":"inspect logs","item_id":"rs_1"}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_live","name":"exec_command"}}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":\"ls\"}","item_id":"fc_1"}`,
		`data: {"type":"response.output_item.done","item":{"id":"ws_1","type":"web_search_call"}}`,
		`data: {"type":"response.output_text.delta","delta":"final answer"}`,
		`data: [DONE]`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderOpenAICompatible, "/v1/responses", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "final answer", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, "inspect logs", resp.Candidates[0].Content[1].Text)
	require.Len(t, resp.Candidates[0].ToolCalls, 2)
	assert.Equal(t, "exec_command", resp.Candidates[0].ToolCalls[0].Name)
	assert.Equal(t, `{"cmd":"ls"}`, resp.Candidates[0].ToolCalls[0].ArgsText)
	assert.Equal(t, "web_search_call", resp.Candidates[0].ToolCalls[1].Name)
}

func TestParseOpenAIResponsesStreamResponsePreservesRefusal(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"checking safety"}`,
		`data: {"type":"response.refusal.delta","delta":"I can't help with that."}`,
		`data: [DONE]`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderOpenAICompatible, "/v1/responses", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	require.NotNil(t, resp.Candidates[0].Refusal)
	assert.Equal(t, "I can't help with that.", resp.Candidates[0].Refusal.Message)
	assert.Equal(t, "checking safety", resp.Candidates[0].Content[0].Text)
}

func TestParseOpenAIResponsesStreamResponsePreservesCustomToolCall(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"checking policy"}`,
		`data: {"type":"response.output_item.done","item":{"id":"ct_1","type":"custom_tool_call","call_id":"call_custom","name":"policy_guard","arguments":"{\"topic\":\"restricted\"}"}}`,
		`data: {"type":"response.output_text.delta","delta":"blocked"}`,
		`data: [DONE]`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderOpenAICompatible, "/v1/responses", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "blocked", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, "checking policy", resp.Candidates[0].Content[1].Text)
	require.Len(t, resp.Candidates[0].ToolCalls, 1)
	assert.Equal(t, "call_custom", resp.Candidates[0].ToolCalls[0].ID)
	assert.Equal(t, "custom_tool_call", resp.Candidates[0].ToolCalls[0].Type)
	assert.Equal(t, "policy_guard", resp.Candidates[0].ToolCalls[0].Name)
	assert.Equal(t, `{"topic":"restricted"}`, resp.Candidates[0].ToolCalls[0].ArgsText)
}

func TestParseAnthropicStreamResponse(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"final answer"}}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"inspect logs"}}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_live","name":"Bash","input":{}}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"pwd\"}"}}`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderAnthropic, "/v1/messages", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "final answer", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, "inspect logs", resp.Candidates[0].Content[1].Text)
	require.Len(t, resp.Candidates[0].ToolCalls, 1)
	assert.Equal(t, "Bash", resp.Candidates[0].ToolCalls[0].Name)
	assert.Equal(t, `{"command":"pwd"}`, resp.Candidates[0].ToolCalls[0].ArgsText)
}

func TestParseGoogleGenerateContentStreamResponse(t *testing.T) {
	body := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]}}]}`,
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Gemini"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}}`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderGoogleGenAI, "/v1beta/models:streamGenerateContent", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "model", resp.Candidates[0].Role)
	assert.Equal(t, "Hello Gemini", resp.Candidates[0].Content[0].Text)
}

func TestParseVertexGenerateContentStreamResponse(t *testing.T) {
	body := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]}}]}`,
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Vertex"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}}`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderVertexNative, "/v1/publishers/models:streamGenerateContent", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "model", resp.Candidates[0].Role)
	assert.Equal(t, "Hello Vertex", resp.Candidates[0].Content[0].Text)
}

func TestParseStreamProviderErrors(t *testing.T) {
	testCases := []struct {
		name     string
		provider string
		endpoint string
		body     string
		wantText string
	}{
		{
			name:     "openai responses",
			provider: ProviderOpenAICompatible,
			endpoint: "/v1/responses",
			body: strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"partial"}`,
				`data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error","code":"stream_aborted"}}}`,
			}, "\n"),
			wantText: "stream aborted",
		},
		{
			name:     "anthropic",
			provider: ProviderAnthropic,
			endpoint: "/v1/messages",
			body: strings.Join([]string{
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
				`data: {"type":"error","error":{"type":"overloaded_error","message":"stream overloaded"}}`,
			}, "\n"),
			wantText: "stream overloaded",
		},
		{
			name:     "google",
			provider: ProviderGoogleGenAI,
			endpoint: "/v1beta/models:streamGenerateContent",
			body: strings.Join([]string{
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`,
				`data: {"error":{"code":429,"message":"stream quota exceeded","status":"RESOURCE_EXHAUSTED"}}`,
			}, "\n"),
			wantText: "stream quota exceeded",
		},
		{
			name:     "vertex",
			provider: ProviderVertexNative,
			endpoint: "/v1/publishers/models:streamGenerateContent",
			body: strings.Join([]string{
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`,
				`data: {"error":{"code":403,"message":"vertex stream denied","status":"PERMISSION_DENIED"}}`,
			}, "\n"),
			wantText: "vertex stream denied",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := ParseStreamResponse(tt.provider, tt.endpoint, []byte(tt.body))
			require.NoError(t, err)
			require.Len(t, resp.Candidates, 1)
			assert.Equal(t, "error", resp.Candidates[0].FinishReason)
			require.Contains(t, resp.Extensions, "error")
			assert.Contains(t, marshalCompactString(resp.Extensions["error"]), tt.wantText)
		})
	}
}
