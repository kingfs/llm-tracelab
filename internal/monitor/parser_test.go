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

func buildRecordFixture(t *testing.T, url string, isStream bool, reqBody string, resBody string) []byte {
	t.Helper()

	reqHeader := "POST " + url + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	resHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	if isStream {
		resHeader = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
	}

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req_1",
			Time:          time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1-codex",
			URL:           url,
			Method:        "POST",
			StatusCode:    200,
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
