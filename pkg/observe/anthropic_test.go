package observe

import (
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestAnthropicParserParsesMessagesAndTools(t *testing.T) {
	parser := NewAnthropicParser()
	input := ParseInput{
		TraceID: "trace-claude",
		Header:  anthropicTestHeader(false),
		RequestBody: []byte(`{
			"model":"claude-sonnet-4-5",
			"system":"You are helpful.",
			"messages":[
				{"role":"user","content":[{"type":"text","text":"run pwd"}]},
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"pwd"}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok","is_error":false}]}
			],
			"tools":[{"name":"Bash","description":"Run shell","input_schema":{"type":"object"}}],
			"max_tokens":64
		}`),
		ResponseBody: []byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"stop_reason":"tool_use",
			"content":[
				{"type":"thinking","thinking":"Need command."},
				{"type":"text","text":"I will run it."},
				{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"ls"}},
				{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"docs"}},
				{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[{"type":"web_search_result","title":"Docs","url":"https://example.com"}]}
			],
			"usage":{"input_tokens":10,"output_tokens":8,"cache_read_input_tokens":3}
		}`),
	}
	obs, err := parser.Parse(t.Context(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Parser != "anthropic" || obs.Model != "claude-sonnet-4-5" {
		t.Fatalf("obs parser/model = %s/%s", obs.Parser, obs.Model)
	}
	if len(obs.Request.Instructions) != 1 || obs.Request.Instructions[0].Text != "You are helpful." {
		t.Fatalf("instructions = %+v", obs.Request.Instructions)
	}
	if len(obs.Tools.Declarations) != 1 || obs.Tools.Declarations[0].Name != "Bash" {
		t.Fatalf("tool declarations = %+v", obs.Tools.Declarations)
	}
	if len(obs.Response.Reasoning) != 1 || obs.Response.Reasoning[0].Text != "Need command." {
		t.Fatalf("reasoning = %+v", obs.Response.Reasoning)
	}
	if len(obs.Tools.Calls) < 3 {
		t.Fatalf("tool calls = %+v", obs.Tools.Calls)
	}
	var foundServer bool
	for _, call := range obs.Tools.Calls {
		if call.Kind == "server_tool_use" && call.Owner == ToolOwnerProviderExecuted {
			foundServer = true
		}
	}
	if !foundServer {
		t.Fatalf("server tool call missing in %+v", obs.Tools.Calls)
	}
	if len(obs.Tools.Results) < 2 {
		t.Fatalf("tool results = %+v", obs.Tools.Results)
	}
	if obs.Usage.InputTokens != 10 || obs.Usage.OutputTokens != 8 || obs.Usage.CacheReadTokens != 3 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func anthropicTestHeader(isStream bool) recordfile.RecordHeader {
	return recordfile.RecordHeader{
		Meta: recordfile.MetaData{
			Provider:  "anthropic",
			Operation: "messages",
			Endpoint:  "/v1/messages",
		},
		Layout: recordfile.LayoutInfo{IsStream: isStream},
	}
}

func joinSSE(lines ...string) string {
	return strings.Join(lines, "\n")
}

func TestAnthropicParserParsesStreamingMessages(t *testing.T) {
	parser := NewAnthropicParser()
	body := joinSSE(
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[]}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan."}}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_live","name":"Bash","input":{}}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"pwd\"}"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":5,"output_tokens":6}}`,
		`data: {"type":"message_stop"}`,
	)
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID:      "trace-claude-stream",
		Header:       anthropicTestHeader(true),
		RequestBody:  []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":64}`),
		ResponseBody: []byte(body),
		IsStream:     true,
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Stream.AccumulatedText != "Hello" || obs.Stream.AccumulatedReasoning != "Plan." {
		t.Fatalf("stream accumulators = text %q reasoning %q", obs.Stream.AccumulatedText, obs.Stream.AccumulatedReasoning)
	}
	if len(obs.Stream.AccumulatedToolCalls) != 1 {
		t.Fatalf("stream tool calls = %+v", obs.Stream.AccumulatedToolCalls)
	}
	if len(obs.Tools.Calls) != 1 || obs.Tools.Calls[0].Name != "Bash" || obs.Tools.Calls[0].ArgsText != `{"command":"pwd"}` {
		t.Fatalf("tool calls = %+v", obs.Tools.Calls)
	}
	if obs.Usage.InputTokens != 5 || obs.Usage.OutputTokens != 6 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func TestAnthropicParserPreservesRedactedThinkingAndSignature(t *testing.T) {
	parser := NewAnthropicParser()
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID: "trace-claude-redacted-thinking",
		Header:  anthropicTestHeader(false),
		RequestBody: []byte(`{
			"model":"claude-sonnet-4-5",
			"messages":[{"role":"user","content":"think privately"}],
			"thinking":{"type":"enabled","budget_tokens":1024},
			"max_tokens":64
		}`),
		ResponseBody: []byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-5",
			"content":[
				{"type":"redacted_thinking","data":"opaque-signature-bound-payload"},
				{"type":"text","text":"Done"}
			],
			"usage":{"input_tokens":7,"output_tokens":9}
		}`),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(obs.Response.Reasoning) != 1 {
		t.Fatalf("reasoning = %+v", obs.Response.Reasoning)
	}
	reasoning := obs.Response.Reasoning[0]
	if reasoning.ProviderType != "redacted_thinking" || reasoning.NormalizedType != NodeReasoning || reasoning.Text != "[redacted_thinking]" {
		t.Fatalf("redacted reasoning = %+v", reasoning)
	}
	if !strings.Contains(string(reasoning.Raw), "opaque-signature-bound-payload") {
		t.Fatalf("redacted thinking raw not preserved: %s", reasoning.Raw)
	}

	streamBody := joinSSE(
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[]}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_123"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Done"}}`,
		`data: {"type":"message_stop"}`,
	)
	streamObs, err := parser.Parse(t.Context(), ParseInput{
		TraceID:      "trace-claude-signature-delta",
		Header:       anthropicTestHeader(true),
		RequestBody:  []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":64}`),
		ResponseBody: []byte(streamBody),
		IsStream:     true,
	})
	if err != nil {
		t.Fatalf("Parse() stream error = %v", err)
	}
	if streamObs.Stream.AccumulatedText != "Done" || streamObs.Stream.AccumulatedReasoning != "" {
		t.Fatalf("stream accumulators = text %q reasoning %q", streamObs.Stream.AccumulatedText, streamObs.Stream.AccumulatedReasoning)
	}
	var foundSignature bool
	for _, event := range streamObs.Stream.Events {
		if event.EventType == "content_block_delta" && strings.Contains(string(event.JSON), `"signature_delta"`) {
			foundSignature = true
			if event.NormalizedType != NodeUnknown || event.Delta != "" {
				t.Fatalf("signature event = %+v", event)
			}
		}
	}
	if !foundSignature {
		t.Fatalf("signature_delta event missing in %+v", streamObs.Stream.Events)
	}
}

func TestAnthropicParserParsesNonStreamProviderError(t *testing.T) {
	parser := NewAnthropicParser()
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID:      "trace-claude-error",
		Header:       anthropicTestHeader(false),
		RequestBody:  []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`),
		ResponseBody: []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(obs.Response.Errors) != 1 || obs.Response.Errors[0].NormalizedType != NodeError {
		t.Fatalf("errors = %+v", obs.Response.Errors)
	}
	if !strings.Contains(obs.Response.Errors[0].Text, "Overloaded") {
		t.Fatalf("error text = %q", obs.Response.Errors[0].Text)
	}
	if len(obs.Response.Outputs) != 0 {
		t.Fatalf("outputs = %+v", obs.Response.Outputs)
	}
}
