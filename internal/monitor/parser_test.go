package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestParseLogFileResponsesRendersConversationAndToolCalls(t *testing.T) {
	reqBody := `{"input":[{"type":"message","role":"system","content":[{"type":"input_text","text":"You are helpful."}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"why failed?"}]},{"type":"function_call","call_id":"call_hist","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},{"type":"function_call_output","call_id":"call_hist","output":"{\"cwd\":\"/tmp\"}"}]}`
	resBody := strings.Join([]string{
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"inspect logs","item_id":"rs_1"}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_live","name":"exec_command"}}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":\"ls\"}","item_id":"fc_1"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"final answer"}`,
		"",
	}, "\n")

	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}

	if len(parsed.ChatMessages) != 4 {
		t.Fatalf("len(ChatMessages) = %d, want 4", len(parsed.ChatMessages))
	}
	if parsed.ChatMessages[0].Role != "system" || parsed.ChatMessages[0].Content != "You are helpful." {
		t.Fatalf("system message = %+v", parsed.ChatMessages[0])
	}
	if parsed.ChatMessages[0].MessageType != "message" || parsed.ChatMessages[0].ContentFormat != "markdown" {
		t.Fatalf("system message metadata = %+v", parsed.ChatMessages[0])
	}
	if parsed.ChatMessages[1].Role != "user" || parsed.ChatMessages[1].Content != "why failed?" {
		t.Fatalf("user message = %+v", parsed.ChatMessages[1])
	}
	if parsed.ChatMessages[2].MessageType != "function_call" || len(parsed.ChatMessages[2].ToolCalls) != 1 || parsed.ChatMessages[2].ToolCalls[0].Function.Name != "exec_command" {
		t.Fatalf("historical tool call = %+v", parsed.ChatMessages[2])
	}
	if parsed.ChatMessages[3].Role != "tool" || parsed.ChatMessages[3].MessageType != "function_call_output" || parsed.ChatMessages[3].ContentFormat != "json" || !strings.Contains(parsed.ChatMessages[3].Content, `"/tmp"`) {
		t.Fatalf("tool output = %+v", parsed.ChatMessages[3])
	}
	if parsed.AIReasoning != "inspect logs" {
		t.Fatalf("AIReasoning = %q, want %q", parsed.AIReasoning, "inspect logs")
	}
	if parsed.AIContent != "final answer" {
		t.Fatalf("AIContent = %q, want %q", parsed.AIContent, "final answer")
	}
	if len(parsed.ResponseToolCalls) != 1 {
		t.Fatalf("len(ResponseToolCalls) = %d, want 1", len(parsed.ResponseToolCalls))
	}
	if parsed.ResponseToolCalls[0].ID != "call_live" {
		t.Fatalf("ResponseToolCalls[0].ID = %q, want call_live", parsed.ResponseToolCalls[0].ID)
	}
	if parsed.ResponseToolCalls[0].Function.Arguments != `{"cmd":"ls"}` {
		t.Fatalf("ResponseToolCalls[0].Function.Arguments = %q, want %q", parsed.ResponseToolCalls[0].Function.Arguments, `{"cmd":"ls"}`)
	}
}

func TestParseLogFileResponsesRequestFallbackDoesNotLookLikeEmbedding(t *testing.T) {
	reqBody := `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello from responses"}]}]}`
	content := buildRecordFixture(t, "/v1/responses", false, reqBody, `{"output":[]}`)

	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if len(parsed.ChatMessages) != 1 {
		t.Fatalf("len(ChatMessages) = %d, want 1", len(parsed.ChatMessages))
	}
	if strings.Contains(parsed.ChatMessages[0].Content, "Embedding Input") {
		t.Fatalf("responses request rendered as embedding: %+v", parsed.ChatMessages[0])
	}
}

func TestParseLogFileChatCompletionsRequestRendersStructuredMessages(t *testing.T) {
	reqBody := `{"messages":[{"role":"system","content":"You are a personal assistant."},{"role":"user","content":[{"type":"text","text":"A new session "}]}],"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file.","parameters":{"type":"object","properties":{"file_path":{"type":"string"}}}}}]}`
	content := buildRecordFixture(t, "/v1/chat/completions", false, reqBody, `{"choices":[{"message":{"content":"hello"}}]}`)

	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if len(parsed.ChatMessages) != 2 {
		t.Fatalf("len(ChatMessages) = %d, want 2", len(parsed.ChatMessages))
	}
	if parsed.ChatMessages[0].Role != "system" || parsed.ChatMessages[0].Content != "You are a personal assistant." {
		t.Fatalf("system message = %+v", parsed.ChatMessages[0])
	}
	if parsed.ChatMessages[1].Role != "user" || parsed.ChatMessages[1].Content != "A new session" {
		t.Fatalf("user message = %+v", parsed.ChatMessages[1])
	}
	if len(parsed.RequestTools) != 1 {
		t.Fatalf("len(RequestTools) = %d, want 1", len(parsed.RequestTools))
	}
	if parsed.RequestTools[0].Name != "read" || parsed.RequestTools[0].Description != "Read the contents of a file." {
		t.Fatalf("request tool = %+v", parsed.RequestTools[0])
	}
	if parsed.RequestTools[0].Source != "openai" {
		t.Fatalf("request tool source = %q, want openai", parsed.RequestTools[0].Source)
	}
	if !strings.Contains(parsed.RequestTools[0].Parameters, `"file_path"`) {
		t.Fatalf("request tool parameters = %q", parsed.RequestTools[0].Parameters)
	}
	if len(parsed.OpenAITools) != 1 || len(parsed.AnthropicTools) != 0 {
		t.Fatalf("tool grouping = openai:%d anthropic:%d", len(parsed.OpenAITools), len(parsed.AnthropicTools))
	}
}

func TestParseLogFileAnthropicMessagesRendersConversationAndStreamOutput(t *testing.T) {
	reqBody := `{"system":"You are helpful.","messages":[{"role":"user","content":[{"type":"text","text":"inspect this repo"}]},{"role":"assistant","content":[{"type":"text","text":"Reading files."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/README.md"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"# read ok"}]}]}`
	resBody := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"final "}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"answer"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"inspect logs"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_live","name":"Bash","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"pwd\"}"}}`,
		"",
	}, "\n")

	content := buildRecordFixture(t, "/v1/messages?beta=true", true, reqBody, resBody)
	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}

	if len(parsed.ChatMessages) != 4 {
		t.Fatalf("len(ChatMessages) = %d, want 4", len(parsed.ChatMessages))
	}
	if parsed.ChatMessages[0].Role != "system" || parsed.ChatMessages[0].Content != "You are helpful." {
		t.Fatalf("system message = %+v", parsed.ChatMessages[0])
	}
	if parsed.ChatMessages[1].Role != "user" || parsed.ChatMessages[1].Content != "inspect this repo" {
		t.Fatalf("user message = %+v", parsed.ChatMessages[1])
	}
	if parsed.ChatMessages[2].Role != "assistant" || parsed.ChatMessages[2].Content != "Reading files." || len(parsed.ChatMessages[2].ToolCalls) != 1 {
		t.Fatalf("assistant message = %+v", parsed.ChatMessages[2])
	}
	if parsed.ChatMessages[2].ToolCalls[0].Function.Name != "Read" || parsed.ChatMessages[2].ToolCalls[0].Function.Arguments != `{"file_path":"/tmp/README.md"}` {
		t.Fatalf("assistant tool call = %+v", parsed.ChatMessages[2].ToolCalls[0])
	}
	if parsed.ChatMessages[3].Role != "tool" || parsed.ChatMessages[3].ToolCallID != "toolu_1" || parsed.ChatMessages[3].Content != "# read ok" {
		t.Fatalf("tool result = %+v", parsed.ChatMessages[3])
	}
	if parsed.ChatMessages[2].MessageType != "tool_use" {
		t.Fatalf("assistant message type = %q, want tool_use", parsed.ChatMessages[2].MessageType)
	}
	if parsed.AIContent != "final answer" {
		t.Fatalf("AIContent = %q, want final answer", parsed.AIContent)
	}
	if parsed.AIReasoning != "inspect logs" {
		t.Fatalf("AIReasoning = %q, want inspect logs", parsed.AIReasoning)
	}
	if len(parsed.ResponseToolCalls) != 1 {
		t.Fatalf("len(ResponseToolCalls) = %d, want 1", len(parsed.ResponseToolCalls))
	}
	if parsed.ResponseToolCalls[0].ID != "toolu_live" || parsed.ResponseToolCalls[0].Function.Name != "Bash" || parsed.ResponseToolCalls[0].Function.Arguments != `{"command":"pwd"}` {
		t.Fatalf("ResponseToolCalls[0] = %+v", parsed.ResponseToolCalls[0])
	}
}

func TestParseLogFileAnthropicRequestRendersToolDefinitions(t *testing.T) {
	reqBody := `{"system":"You are helpful.","tools":[{"name":"read","description":"Read the contents of a file.","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"inspect this repo"}]}]}`
	content := buildRecordFixture(t, "/v1/messages", false, reqBody, `{"content":[{"type":"text","text":"done"}]}`)

	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if len(parsed.RequestTools) != 1 {
		t.Fatalf("len(RequestTools) = %d, want 1", len(parsed.RequestTools))
	}
	if parsed.RequestTools[0].Name != "read" || parsed.RequestTools[0].Description != "Read the contents of a file." {
		t.Fatalf("request tool = %+v", parsed.RequestTools[0])
	}
	if parsed.RequestTools[0].Source != "anthropic" {
		t.Fatalf("request tool source = %q, want anthropic", parsed.RequestTools[0].Source)
	}
	if !strings.Contains(parsed.RequestTools[0].Parameters, `"file_path"`) {
		t.Fatalf("request tool parameters = %q", parsed.RequestTools[0].Parameters)
	}
	if len(parsed.OpenAITools) != 0 || len(parsed.AnthropicTools) != 1 {
		t.Fatalf("tool grouping = openai:%d anthropic:%d", len(parsed.OpenAITools), len(parsed.AnthropicTools))
	}
}

func TestParseLogFileAnthropicDecoratesThinkingAndToolErrors(t *testing.T) {
	reqBody := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"step through the stack"},{"type":"tool_use","id":"toolu_err","name":"Bash","input":{"command":"exit 1"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_err","is_error":true,"content":[{"type":"document","source":{"type":"text","media_type":"text/plain","data":"stacktrace"}}]}]}]}`
	content := buildRecordFixture(t, "/v1/messages", false, reqBody, `{"content":[{"type":"text","text":"done"}]}`)

	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if len(parsed.ChatMessages) != 2 {
		t.Fatalf("len(ChatMessages) = %d, want 2", len(parsed.ChatMessages))
	}
	if len(parsed.ChatMessages[0].Blocks) != 1 || parsed.ChatMessages[0].Blocks[0].Kind != "thinking" {
		t.Fatalf("assistant thinking blocks = %+v", parsed.ChatMessages[0].Blocks)
	}
	if !parsed.ChatMessages[1].IsError {
		t.Fatalf("tool result should be marked as error: %+v", parsed.ChatMessages[1])
	}
	if parsed.ChatMessages[1].Name != "Bash" {
		t.Fatalf("tool result name = %q, want Bash", parsed.ChatMessages[1].Name)
	}
	if len(parsed.ChatMessages[1].Blocks) == 0 || parsed.ChatMessages[1].Blocks[0].Kind != "attachment" {
		t.Fatalf("tool result blocks = %+v", parsed.ChatMessages[1].Blocks)
	}
}

func TestParseLogFileGoogleDecoratesPromptFeedbackAndSafety(t *testing.T) {
	reqBody := `{"systemInstruction":{"role":"system","parts":[{"text":"Be safe"}]},"contents":[{"role":"user","parts":[{"text":"unsafe request"}]}]}`
	resBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HATE_SPEECH","probability":"HIGH","blocked":true}]}],"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`

	content := buildRecordFixture(t, "/v1beta/models/gemini-2.5-flash:generateContent", false, reqBody, resBody)
	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if parsed.AIContent != "partial" {
		t.Fatalf("AIContent = %q, want partial", parsed.AIContent)
	}
	if len(parsed.AIBlocks) != 2 {
		t.Fatalf("len(AIBlocks) = %d, want 2", len(parsed.AIBlocks))
	}
	if parsed.AIBlocks[0].Title != "Prompt Feedback" || !strings.Contains(parsed.AIBlocks[0].Text, "blockReason") {
		t.Fatalf("prompt feedback block = %+v", parsed.AIBlocks[0])
	}
	if parsed.AIBlocks[1].Title != "Safety Ratings" || !strings.Contains(parsed.AIBlocks[1].Text, "HARM_CATEGORY_HATE_SPEECH") {
		t.Fatalf("safety ratings block = %+v", parsed.AIBlocks[1])
	}
}

func TestParseLogFileProviderErrorDecoratesErrorBlock(t *testing.T) {
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"trigger rate limit"}]}]}`
	resBody := `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`

	content := buildRecordFixtureWithStatus(t, "/v1/responses", false, "429 Too Many Requests", reqBody, resBody)
	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if parsed.Header.Meta.StatusCode != 429 {
		t.Fatalf("status code = %d, want 429", parsed.Header.Meta.StatusCode)
	}
	if parsed.AIContent != "" {
		t.Fatalf("AIContent = %q, want empty", parsed.AIContent)
	}
	if len(parsed.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(parsed.AIBlocks))
	}
	if parsed.AIBlocks[0].Title != "Provider Error" || !strings.Contains(parsed.AIBlocks[0].Text, "Rate limit exceeded") {
		t.Fatalf("provider error block = %+v", parsed.AIBlocks[0])
	}
}

func TestParseLogFileStreamProviderErrorDecoratesErrorBlock(t *testing.T) {
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"stream failure"}]}]}`
	resBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		`data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error","code":"stream_aborted"}}}`,
	}, "\n")

	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if parsed.AIContent != "partial" {
		t.Fatalf("AIContent = %q, want partial", parsed.AIContent)
	}
	if len(parsed.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(parsed.AIBlocks))
	}
	if parsed.AIBlocks[0].Title != "Provider Error" || !strings.Contains(parsed.AIBlocks[0].Text, "stream aborted") {
		t.Fatalf("provider error block = %+v", parsed.AIBlocks[0])
	}
}

func TestParseLogFileModelListDecoratesModelListBlock(t *testing.T) {
	reqBody := `{}`
	resBody := `{"data":[{"id":"gpt-5","object":"model"},{"id":"gpt-4.1-mini","object":"model"}]}`

	content := buildRecordFixture(t, "/v1/models", false, reqBody, resBody)
	parsed, err := ParseLogFile(content)
	if err != nil {
		t.Fatalf("ParseLogFile() error = %v", err)
	}
	if len(parsed.ChatMessages) != 1 {
		t.Fatalf("len(ChatMessages) = %d, want 1", len(parsed.ChatMessages))
	}
	if parsed.ChatMessages[0].Content != "List available models" {
		t.Fatalf("request content = %q, want List available models", parsed.ChatMessages[0].Content)
	}
	if parsed.AIContent != "gpt-5\ngpt-4.1-mini" {
		t.Fatalf("AIContent = %q, want gpt-5\\ngpt-4.1-mini", parsed.AIContent)
	}
	if len(parsed.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(parsed.AIBlocks))
	}
	if parsed.AIBlocks[0].Title != "Model List" || !strings.Contains(parsed.AIBlocks[0].Text, "gpt-5") {
		t.Fatalf("model list block = %+v", parsed.AIBlocks[0])
	}
}

func buildRecordFixture(t *testing.T, url string, isStream bool, reqBody string, resBody string) []byte {
	return buildRecordFixtureWithStatus(t, url, isStream, "200 OK", reqBody, resBody)
}

func buildRecordFixtureWithStatus(t *testing.T, url string, isStream bool, status string, reqBody string, resBody string) []byte {
	t.Helper()

	reqHeader := "POST " + url + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	resHeader := "HTTP/1.1 " + status + "\r\nContent-Type: application/json\r\n\r\n"
	if isStream {
		resHeader = "HTTP/1.1 " + status + "\r\nContent-Type: text/event-stream\r\n\r\n"
	}

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req_1",
			Time:          time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1-codex",
			URL:           url,
			Method:        "POST",
			StatusCode:    parseHTTPStatusCode(status),
			DurationMs:    100,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: int64(len(resBody)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHeader)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHeader)),
			ResBodyLen:   int64(len(resBody)),
			IsStream:     isStream,
		},
	}

	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}

	var payload strings.Builder
	payload.WriteString(reqHeader)
	payload.WriteString(reqBody)
	payload.WriteByte('\n')
	payload.WriteString(resHeader)
	payload.WriteString(resBody)

	return append(prelude, []byte(payload.String())...)
}

func parseHTTPStatusCode(status string) int {
	fields := strings.Fields(status)
	if len(fields) == 0 {
		return 200
	}
	switch fields[0] {
	case "429":
		return 429
	case "503":
		return 503
	default:
		return 200
	}
}
