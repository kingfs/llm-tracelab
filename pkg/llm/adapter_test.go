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

	adapter, err = AdapterForPath("/v1beta/models/gemini-2.5-flash:generateContent", "https://generativelanguage.googleapis.com")
	require.NoError(t, err)
	assert.Equal(t, ProviderGoogleGenAI, adapter.Semantics().Provider)
	assert.Equal(t, OperationGenerateContent, adapter.Semantics().Operation)
	assert.Equal(t, "/v1beta/models:generateContent", adapter.Semantics().Endpoint)

	adapter, err = AdapterForPath("/v1/models", "https://api.openai.com")
	require.NoError(t, err)
	assert.Equal(t, ProviderOpenAICompatible, adapter.Semantics().Provider)
	assert.Equal(t, OperationModels, adapter.Semantics().Operation)
	assert.Equal(t, "/v1/models", adapter.Semantics().Endpoint)

	adapter, err = AdapterForPath("/v1/projects/demo/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent", "https://us-central1-aiplatform.googleapis.com")
	require.NoError(t, err)
	assert.Equal(t, ProviderVertexNative, adapter.Semantics().Provider)
	assert.Equal(t, OperationGenerateContent, adapter.Semantics().Operation)
	assert.Equal(t, "/v1/publishers/models:generateContent", adapter.Semantics().Endpoint)
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

func TestParseOpenAIResponsesResponseCustomToolAndRefusal(t *testing.T) {
	body := []byte(`{
		"id":"resp_custom",
		"model":"gpt-5",
		"output":[
			{"type":"reasoning","content":[{"type":"summary_text","text":"checking policy"}]},
			{"type":"custom_tool_call","call_id":"call_custom","name":"policy_guard","arguments":"{\"topic\":\"restricted\"}"},
			{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I can't help with that."}]}
		]
	}`)

	resp, err := ParseResponse(ProviderOpenAICompatible, "/v1/responses", body)
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	require.NotNil(t, resp.Candidates[0].Refusal)
	assert.Equal(t, "I can't help with that.", resp.Candidates[0].Refusal.Message)
	require.Len(t, resp.Candidates[0].ToolCalls, 1)
	assert.Equal(t, "custom_tool_call", resp.Candidates[0].ToolCalls[0].Type)
	assert.Equal(t, "policy_guard", resp.Candidates[0].ToolCalls[0].Name)
	assert.Equal(t, "checking policy", resp.Candidates[0].Content[0].Text)
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

func TestParseGeminiRequestAndResponseForPath(t *testing.T) {
	reqBody := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"Hello Gemini"}]}],
		"systemInstruction":{"role":"system","parts":[{"text":"Be concise"}]},
		"generationConfig":{"temperature":0.2,"maxOutputTokens":64}
	}`)

	req, err := ParseRequestForPath("/v1beta/models/gemini-2.5-flash:generateContent", "https://generativelanguage.googleapis.com", reqBody)
	require.NoError(t, err)
	assert.Equal(t, "gemini-2.5-flash", req.Model)
	require.Len(t, req.System, 1)
	assert.Equal(t, "Be concise", req.System[0].Text)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, "Hello Gemini", req.Messages[0].Content[0].Text)

	respBody := []byte(`{
		"candidates":[{"content":{"role":"model","parts":[{"text":"Hello from Gemini"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}
	}`)
	resp, err := ParseResponse(ProviderGoogleGenAI, "/v1beta/models:generateContent", respBody)
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "Hello from Gemini", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, 10, resp.Usage.TotalTokens)
}

func TestParseVertexRequestAndResponseForPath(t *testing.T) {
	reqBody := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"Hello Vertex"}]}],
		"systemInstruction":{"role":"system","parts":[{"text":"Be precise"}]},
		"generationConfig":{"temperature":0.1,"maxOutputTokens":32}
	}`)

	req, err := ParseRequestForPath("/v1/projects/demo/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent", "https://us-central1-aiplatform.googleapis.com", reqBody)
	require.NoError(t, err)
	assert.Equal(t, "gemini-2.5-flash", req.Model)
	require.Len(t, req.System, 1)
	assert.Equal(t, "Be precise", req.System[0].Text)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, "Hello Vertex", req.Messages[0].Content[0].Text)

	respBody := []byte(`{
		"candidates":[{"content":{"role":"model","parts":[{"text":"Hello from Vertex"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6,"totalTokenCount":10}
	}`)
	resp, err := ParseResponse(ProviderVertexNative, "/v1/publishers/models:generateContent", respBody)
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "Hello from Vertex", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, 10, resp.Usage.TotalTokens)
}

func TestParseModelListRequestAndResponseForPath(t *testing.T) {
	req, err := ParseRequestForPath("/v1/models", "https://api.openai.com", nil)
	require.NoError(t, err)
	assert.Equal(t, "list_models", req.Model)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, "List available models", req.Messages[0].Content[0].Text)

	respBody := []byte(`{
		"data":[
			{"id":"gpt-5","object":"model"},
			{"id":"gpt-4.1-mini","object":"model"}
		]
	}`)
	resp, err := ParseResponse(ProviderOpenAICompatible, "/v1/models", respBody)
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "gpt-5\ngpt-4.1-mini", resp.Candidates[0].Content[0].Text)
	assert.Contains(t, resp.Extensions, "model_list")

	googleResp, err := ParseResponse(ProviderGoogleGenAI, "/v1beta/models", []byte(`{
		"models":[
			{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"}
		]
	}`))
	require.NoError(t, err)
	require.Len(t, googleResp.Candidates, 1)
	assert.Equal(t, "models/gemini-2.5-flash", googleResp.Candidates[0].Content[0].Text)
}

func TestParseProviderErrorResponses(t *testing.T) {
	testCases := []struct {
		name     string
		provider string
		endpoint string
		body     []byte
		wantText string
	}{
		{
			name:     "openai compatible",
			provider: ProviderOpenAICompatible,
			endpoint: "/v1/responses",
			body:     []byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`),
			wantText: "Rate limit exceeded",
		},
		{
			name:     "anthropic",
			provider: ProviderAnthropic,
			endpoint: "/v1/messages",
			body:     []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Anthropic overloaded"}}`),
			wantText: "Anthropic overloaded",
		},
		{
			name:     "google",
			provider: ProviderGoogleGenAI,
			endpoint: "/v1beta/models:generateContent",
			body:     []byte(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`),
			wantText: "Quota exceeded",
		},
		{
			name:     "vertex",
			provider: ProviderVertexNative,
			endpoint: "/v1/publishers/models:generateContent",
			body:     []byte(`{"error":{"code":403,"message":"Vertex permission denied","status":"PERMISSION_DENIED"}}`),
			wantText: "Vertex permission denied",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := ParseResponse(tt.provider, tt.endpoint, tt.body)
			require.NoError(t, err)
			require.Len(t, resp.Candidates, 1)
			assert.Equal(t, "error", resp.Candidates[0].FinishReason)
			require.Contains(t, resp.Extensions, "error")
			assert.Contains(t, marshalCompactString(resp.Extensions["error"]), tt.wantText)
		})
	}
}
