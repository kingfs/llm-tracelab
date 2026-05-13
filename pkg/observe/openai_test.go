package observe

import (
	"context"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestOpenAIParserParsesChatCompletion(t *testing.T) {
	parser := NewOpenAIParser()
	input := ParseInput{
		TraceID: "trace-chat",
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationChatCompletions,
				Endpoint:  "/v1/chat/completions",
			},
		},
		RequestBody: []byte(`{
			"model":"gpt-4o",
			"messages":[
				{"role":"system","content":"You are helpful."},
				{"role":"user","content":"weather?"},
				{"role":"tool","tool_call_id":"call_1","name":"weather","content":"{\"temp\":22}"}
			],
			"tools":[{"type":"function","function":{"name":"weather","description":"Get weather","parameters":{"type":"object"}}}]
		}`),
		ResponseBody: []byte(`{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"created":1741570283,
			"model":"gpt-4o",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"It is 22C.","tool_calls":[{"id":"call_2","type":"function","function":{"name":"summarize","arguments":"{\"city\":\"Paris\"}"}}]},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":1}}
		}`),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Model != "gpt-4o" {
		t.Fatalf("Model = %q", obs.Model)
	}
	if len(obs.Request.Messages) != 3 {
		t.Fatalf("request messages = %d, want 3", len(obs.Request.Messages))
	}
	if obs.Request.Messages[2].NormalizedType != NodeToolResult {
		t.Fatalf("tool message normalized type = %q", obs.Request.Messages[2].NormalizedType)
	}
	if len(obs.Tools.Declarations) != 1 || obs.Tools.Declarations[0].Name != "weather" {
		t.Fatalf("tool declarations = %+v", obs.Tools.Declarations)
	}
	if len(obs.Response.Candidates) != 1 {
		t.Fatalf("response candidates = %d", len(obs.Response.Candidates))
	}
	if len(obs.Response.ToolCalls) != 1 || obs.Tools.Calls[0].Name != "summarize" {
		t.Fatalf("tool calls = %+v / %+v", obs.Response.ToolCalls, obs.Tools.Calls)
	}
	if obs.Usage.InputTokens != 10 || obs.Usage.OutputTokens != 5 || obs.Usage.CacheReadTokens != 3 || obs.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func TestOpenAIParserParsesResponsesOutputAndUnknown(t *testing.T) {
	parser := NewOpenAIParser()
	input := ParseInput{
		TraceID: "trace-responses",
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationResponses,
				Endpoint:  "/v1/responses",
			},
		},
		RequestBody: []byte(`{
			"model":"gpt-5.1",
			"instructions":"Be concise.",
			"input":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"run pwd"}]},
				{"type":"function_call_output","call_id":"call_hist","output":{"cwd":"/tmp"}}
			],
			"tools":[{"type":"function","name":"exec_command","description":"Run command","parameters":{"type":"object"}}]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_1",
			"object":"response",
			"created_at":1741476777,
			"status":"completed",
			"model":"gpt-5.1",
			"output":[
				{"type":"reasoning","id":"rs_1","content":[{"type":"summary_text","text":"Checking cwd"}]},
				{"type":"local_shell_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}","status":"completed"},
				{"type":"function_call_output","call_id":"call_1","output":{"cwd":"/repo"}},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"You are in /repo."}]},
				{"type":"future_item","value":true}
			],
			"usage":{"input_tokens":11,"output_tokens":6,"total_tokens":17,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":4}}
		}`),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(obs.Request.Instructions) != 1 {
		t.Fatalf("instructions = %d", len(obs.Request.Instructions))
	}
	if len(obs.Response.Outputs) != 5 {
		t.Fatalf("outputs = %d", len(obs.Response.Outputs))
	}
	if obs.Response.Outputs[0].NormalizedType != NodeReasoning {
		t.Fatalf("first output type = %q", obs.Response.Outputs[0].NormalizedType)
	}
	if len(obs.Tools.Calls) != 1 || obs.Tools.Calls[0].Owner != ToolOwnerModelRequested {
		t.Fatalf("tool calls = %+v", obs.Tools.Calls)
	}
	if len(obs.Tools.Results) != 1 || obs.Tools.Results[0].Owner != ToolOwnerClientExecuted {
		t.Fatalf("tool results = %+v", obs.Tools.Results)
	}
	if got := obs.Response.Outputs[4]; got.NormalizedType != NodeUnknown || got.ProviderType != "future_item" {
		t.Fatalf("unknown node = %+v", got)
	}
	if len(obs.Warnings) != 1 || obs.Warnings[0].Code != "unknown_output_item" {
		t.Fatalf("warnings = %+v", obs.Warnings)
	}
	if obs.Usage.InputTokens != 11 || obs.Usage.ReasoningTokens != 4 || obs.Usage.CacheReadTokens != 2 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func TestOpenAIParserParsesResponsesRefusalAndError(t *testing.T) {
	parser := NewOpenAIParser()
	input := ParseInput{
		TraceID: "trace-refusal",
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationResponses,
				Endpoint:  "/v1/responses",
			},
		},
		RequestBody: []byte(`{"model":"gpt-5.1","input":"help"}`),
		ResponseBody: []byte(`{
			"id":"resp_2",
			"object":"response",
			"created_at":1741476777,
			"status":"incomplete",
			"model":"gpt-5.1",
			"incomplete_details":{"reason":"content_filter"},
			"output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I can't help with that."}]}],
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
		}`),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !obs.Safety.Blocked {
		t.Fatalf("Safety.Blocked = false")
	}
	if len(obs.Warnings) == 0 {
		t.Fatalf("expected status warning")
	}
	if got := obs.Response.Outputs[0].Children[0]; got.NormalizedType != NodeRefusal {
		t.Fatalf("refusal node = %+v", got)
	}
}
