package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOpenAIResponsesStreamResponse(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"inspect logs","item_id":"rs_1"}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_live","name":"exec_command"}}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":\"ls\"}","item_id":"fc_1"}`,
		`data: {"type":"response.output_text.delta","delta":"final answer"}`,
		`data: [DONE]`,
	}, "\n")

	resp, err := ParseStreamResponse(ProviderOpenAICompatible, "/v1/responses", []byte(body))
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, "final answer", resp.Candidates[0].Content[0].Text)
	assert.Equal(t, "inspect logs", resp.Candidates[0].Content[1].Text)
	require.Len(t, resp.Candidates[0].ToolCalls, 1)
	assert.Equal(t, "exec_command", resp.Candidates[0].ToolCalls[0].Name)
	assert.Equal(t, `{"cmd":"ls"}`, resp.Candidates[0].ToolCalls[0].ArgsText)
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
