package observe

import (
	"context"
	"strings"
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

func TestOpenAIParserPreservesResponsesBuiltInToolItems(t *testing.T) {
	parser := NewOpenAIParser()
	input := ParseInput{
		TraceID: "trace-responses-builtins",
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationResponses,
				Endpoint:  "/v1/responses",
			},
		},
		RequestBody: []byte(`{
			"model":"gpt-5.1",
			"input":"search docs and run code",
			"tools":[
				{"type":"web_search_preview"},
				{"type":"file_search","vector_store_ids":["vs_123"]},
				{"type":"code_interpreter","container":{"type":"auto"}}
			],
			"include":["web_search_call.action.sources","file_search_call.results","code_interpreter_call.outputs"]
		}`),
		ResponseBody: []byte(`{
			"id":"resp_builtin",
			"object":"response",
			"status":"completed",
			"model":"gpt-5.1",
			"output":[
				{"type":"web_search_call","id":"ws_1","status":"completed","action":{"query":"llm tracing","sources":[{"url":"https://example.com/docs","title":"Docs"}]}},
				{"type":"file_search_call","id":"fs_1","status":"completed","queries":["trace"],"results":[{"file_id":"file_1","filename":"trace.md","score":0.9}]},
				{"type":"code_interpreter_call","id":"ci_1","status":"completed","code":"print(1)","outputs":[{"type":"logs","logs":"1\n"}]},
				{"type":"code_interpreter_call_output","id":"cio_1","call_id":"ci_1","output":{"type":"logs","logs":"1\n"}},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Found docs and executed code."}]}
			]
		}`),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(obs.Response.Outputs) != 5 {
		t.Fatalf("outputs = %d", len(obs.Response.Outputs))
	}
	if len(obs.Response.ToolCalls) != 3 || len(obs.Tools.Calls) != 3 {
		t.Fatalf("tool calls = response %+v tools %+v", obs.Response.ToolCalls, obs.Tools.Calls)
	}
	for _, call := range obs.Tools.Calls {
		if call.Owner != ToolOwnerProviderExecuted {
			t.Fatalf("built-in call owner = %q for %+v", call.Owner, call)
		}
	}
	if len(obs.Response.ToolResults) != 1 || len(obs.Tools.Results) != 1 {
		t.Fatalf("tool results = response %+v tools %+v", obs.Response.ToolResults, obs.Tools.Results)
	}
	if obs.Tools.Results[0].Owner != ToolOwnerProviderExecuted {
		t.Fatalf("built-in result owner = %q", obs.Tools.Results[0].Owner)
	}
	if got := obs.Response.Outputs[2]; got.NormalizedType != NodeServerToolCall || got.ProviderType != "code_interpreter_call" || !strings.Contains(string(got.Raw), `"outputs"`) {
		t.Fatalf("code interpreter node = %+v raw=%s", got, string(got.Raw))
	}
	if len(obs.Warnings) != 0 {
		t.Fatalf("warnings = %+v", obs.Warnings)
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

func TestOpenAIParserParsesChatStream(t *testing.T) {
	parser := NewOpenAIParser()
	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello "},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		`data: [DONE]`,
	}, "\n")
	input := ParseInput{
		TraceID:  "trace-chat-stream",
		IsStream: true,
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationChatCompletions,
				Endpoint:  "/v1/chat/completions",
			},
		},
		RequestBody:  []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`),
		ResponseBody: []byte(body),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Stream.AccumulatedText != "Hello " {
		t.Fatalf("stream text = %q", obs.Stream.AccumulatedText)
	}
	if obs.Stream.AccumulatedReasoning != "thinking" {
		t.Fatalf("stream reasoning = %q", obs.Stream.AccumulatedReasoning)
	}
	if len(obs.Stream.AccumulatedToolCalls) != 1 {
		t.Fatalf("stream tool calls = %+v", obs.Stream.AccumulatedToolCalls)
	}
	if obs.Tools.Calls[0].ArgsText != `{"city":"Paris"}` {
		t.Fatalf("tool args = %q", obs.Tools.Calls[0].ArgsText)
	}
	if obs.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func TestOpenAIParserParsesResponsesStream(t *testing.T) {
	parser := NewOpenAIParser()
	body := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"checking"}`,
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_live","name":"exec_command"}}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":\"ls\"}","item_id":"fc_1"}`,
		`data: {"type":"response.output_text.delta","delta":"done"}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5,"output_tokens_details":{"reasoning_tokens":1}}}}`,
		`data: [DONE]`,
	}, "\n")
	input := ParseInput{
		TraceID:  "trace-responses-stream",
		IsStream: true,
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationResponses,
				Endpoint:  "/v1/responses",
			},
		},
		RequestBody:  []byte(`{"model":"gpt-5.1","input":"run ls","stream":true}`),
		ResponseBody: []byte(body),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Stream.AccumulatedText != "done" {
		t.Fatalf("stream text = %q", obs.Stream.AccumulatedText)
	}
	if obs.Stream.AccumulatedReasoning != "checking" {
		t.Fatalf("stream reasoning = %q", obs.Stream.AccumulatedReasoning)
	}
	if len(obs.Stream.Events) != 5 {
		t.Fatalf("stream events = %d", len(obs.Stream.Events))
	}
	if len(obs.Tools.Calls) != 1 || obs.Tools.Calls[0].Name != "exec_command" {
		t.Fatalf("tool calls = %+v", obs.Tools.Calls)
	}
	if obs.Tools.Calls[0].ArgsText != `{"cmd":"ls"}` {
		t.Fatalf("tool args = %q", obs.Tools.Calls[0].ArgsText)
	}
	if obs.Usage.TotalTokens != 5 || obs.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func TestOpenAIParserParsesResponsesStreamRefusalAndError(t *testing.T) {
	parser := NewOpenAIParser()
	body := strings.Join([]string{
		`data: {"type":"response.refusal.delta","delta":"I can't help."}`,
		`data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error"}}}`,
	}, "\n")
	input := ParseInput{
		TraceID:  "trace-responses-stream-error",
		IsStream: true,
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Provider:  llm.ProviderOpenAICompatible,
				Operation: llm.OperationResponses,
				Endpoint:  "/v1/responses",
			},
		},
		RequestBody:  []byte(`{"model":"gpt-5.1","input":"help","stream":true}`),
		ResponseBody: []byte(body),
	}

	obs, err := parser.Parse(context.Background(), input)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !obs.Safety.Refused {
		t.Fatalf("Safety.Refused = false")
	}
	if len(obs.Response.Refusals) != 1 {
		t.Fatalf("refusals = %+v", obs.Response.Refusals)
	}
	if len(obs.Response.Errors) != 1 {
		t.Fatalf("errors = %+v", obs.Response.Errors)
	}
}
