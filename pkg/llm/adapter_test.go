package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdapterForPath(t *testing.T) {
	adapter, err := AdapterForPath("/v1/chat/completions", "")
	require.NoError(t, err)
	assert.Equal(t, OperationChatCompletions, adapter.Semantics().Operation)

	adapter, err = AdapterForPath("/v1/responses", "")
	require.NoError(t, err)
	assert.Equal(t, OperationResponses, adapter.Semantics().Operation)

	adapter, err = AdapterForPath("/v1/messages", "https://api.anthropic.com")
	require.NoError(t, err)
	assert.Equal(t, ProviderAnthropic, adapter.Semantics().Provider)

	adapter, err = AdapterForPath("/openai/v1/responses?api-version=preview", "https://example-resource.openai.azure.com/openai/v1")
	require.NoError(t, err)
	assert.Equal(t, ProviderAzureOpenAI, adapter.Semantics().Provider)
	assert.Equal(t, "/v1/responses", adapter.Semantics().Endpoint)
}

func TestParseOpenAIResponsesRequest(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"instructions":"You are concise.",
		"tool_choice":{"type":"auto"},
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Find weather in Shanghai"}]},
			{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}"},
			{"type":"function_call_output","call_id":"call_1","name":"weather","output":{"temp_c":22}}
		],
		"tools":[{"type":"function","function":{"name":"weather","description":"Lookup weather","parameters":{"type":"object"}}}]
	}`)

	req, err := ParseRequest(ProviderOpenAICompatible, "/v1/responses", body)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5", req.Model)
	require.Len(t, req.System, 1)
	assert.Equal(t, "You are concise.", req.System[0].Text)
	require.Len(t, req.Messages, 3)
	assert.Equal(t, "user", req.Messages[0].Role)
	assert.Equal(t, "Find weather in Shanghai", req.Messages[0].Content[0].Text)
	assert.Equal(t, "assistant", req.Messages[1].Role)
	assert.Equal(t, "tool_use", req.Messages[1].Content[0].Type)
	assert.Equal(t, "weather", req.Messages[1].Content[0].ToolName)
	assert.Equal(t, "tool", req.Messages[2].Role)
	assert.Equal(t, "tool_result", req.Messages[2].Content[0].Type)
	require.Len(t, req.Tools, 1)
	assert.Equal(t, "weather", req.Tools[0].Name)
}

func TestParseOpenAIResponsesResponse(t *testing.T) {
	body := []byte(`{
		"id":"resp_123",
		"model":"gpt-5",
		"created_at":123,
		"output":[
			{"type":"reasoning","content":[{"type":"summary_text","text":"Inspecting weather backend"}]},
			{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Shanghai\"}"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It is 22C."}]}
		],
		"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16,"reasoning_tokens":2}
	}`)

	resp, err := ParseResponse(ProviderOpenAICompatible, "/v1/responses", body)
	require.NoError(t, err)
	assert.Equal(t, "resp_123", resp.ID)
	assert.Equal(t, "gpt-5", resp.Model)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "assistant", resp.Candidates[0].Role)
	assert.Equal(t, "Inspecting weather backend", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, "thinking", resp.Candidates[0].Content[0].Type)
	assert.Equal(t, "It is 22C.", resp.Candidates[0].Content[1].Text)
	require.Len(t, resp.Candidates[0].ToolCalls, 1)
	assert.Equal(t, "weather", resp.Candidates[0].ToolCalls[0].Name)
	assert.Equal(t, 16, resp.Usage.TotalTokens)
	assert.Equal(t, 2, resp.Usage.ReasoningTokens)
}

func TestParseOpenAIResponsesRequestSingleObjectInput(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5",
		"input":{"type":"message","role":"developer","content":[{"type":"input_text","text":"Prefer tables."}]}
	}`)

	req, err := ParseRequest(ProviderOpenAICompatible, "/v1/responses", body)
	require.NoError(t, err)
	require.Len(t, req.System, 1)
	assert.Equal(t, "Prefer tables.", req.System[0].Text)
	assert.Empty(t, req.Messages)
}

func TestOpenAIResponsesRoundTripMarshal(t *testing.T) {
	req := LLMRequest{
		Model: "gpt-5",
		Messages: []LLMMessage{
			{
				Role: "user",
				Content: []LLMContent{
					{Type: "text", Text: "Hello"},
				},
			},
			{
				Role: "assistant",
				Content: []LLMContent{
					{Type: "tool_use", ToolCallID: "call_1", ToolName: "search", ToolArgs: map[string]any{"q": "llm-tracelab"}},
				},
			},
		},
	}

	data, err := json.Marshal(req.ToOpenAIResponses())
	require.NoError(t, err)

	parsed, err := ParseRequest(ProviderOpenAICompatible, "/v1/responses", data)
	require.NoError(t, err)
	assert.Equal(t, req.Model, parsed.Model)
	assert.Equal(t, "Hello", parsed.Messages[0].Content[0].Text)
	assert.Equal(t, "search", parsed.Messages[1].Content[0].ToolName)
}
