package unittest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type cassetteSpec struct {
	provider        string
	operation       string
	endpoint        string
	url             string
	method          string
	model           string
	requestProtocol string
	requestBody     string
	responseStatus  string
	responseHeaders string
	responseBody    string
	isStream        bool
	usage           recordfile.UsageInfo
	events          []recordfile.RecordEvent
}

type cassetteExpectation struct {
	replayContains   string
	messageContains  string
	historyContains  []string
	messageCount     int
	aiContent        string
	aiBlockCount     int
	aiBlockTitles    []string
	promptTokens     int
	completionTokens int
	statusCode       int
	aiReasoning      string
	toolCallName     string
	toolResultText   string
	toolResultType   string
	eventTypes       []string
	blockContains    string
	errorContent     string
}

type cassetteCapability string

const (
	capabilityNonStream   cassetteCapability = "non_stream"
	capabilityStream      cassetteCapability = "stream"
	capabilityReasoning   cassetteCapability = "reasoning"
	capabilityToolCall    cassetteCapability = "tool_call"
	capabilityToolResult  cassetteCapability = "tool_result"
	capabilityMultiTurn   cassetteCapability = "multi_turn"
	capabilityHistory     cassetteCapability = "history"
	capabilityMixedBlocks cassetteCapability = "mixed_blocks"
	capabilitySafety      cassetteCapability = "safety"
	capabilityProviderErr cassetteCapability = "provider_error"
	capabilityStreamError cassetteCapability = "stream_error"
	capabilityPartialComp cassetteCapability = "partial_completion"
	capabilityRefusal     cassetteCapability = "refusal"
	capabilityError       cassetteCapability = "error"
	capabilityModelList   cassetteCapability = "model_list"
)

type cassetteFixtureCase struct {
	name         string
	capabilities []cassetteCapability
	spec         cassetteSpec
	want         cassetteExpectation
}

func cassetteFixtureCatalog() []cassetteFixtureCase {
	return []cassetteFixtureCase{
		openAIResponsesNonStreamFixture(),
		openAIResponsesMultiTurnFixture(),
		openAIResponsesToolCallStreamFixture(),
		openAIResponsesToolResultFixture(),
		anthropicMessagesNonStreamFixture(),
		anthropicMessagesStreamFixture(),
		anthropicToolErrorFixture(),
		openAIProviderErrorFixture(),
		anthropicProviderErrorFixture(),
		googleProviderErrorFixture(),
		openAIResponsesStreamErrorFixture(),
		anthropicMessagesStreamErrorFixture(),
		googleGenAIStreamErrorFixture(),
		googleGenAIStreamFixture(),
		googleGenAIMixedBlocksFixture(),
		googleGenAIBlockedFixture(),
		openAIModelsFixture(),
		googleGenAIModelsFixture(),
		vertexNativeNonStreamHistoryFixture(),
		vertexNativeProviderErrorFixture(),
		vertexNativeStreamErrorFixture(),
		vertexNativeModelsFixture(),
		vertexNativeStreamFixture(),
	}
}

func openAIResponsesNonStreamFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_responses_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationResponses,
			endpoint:        "/v1/responses",
			url:             "/v1/responses",
			method:          "POST",
			model:           "gpt-5",
			requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello from openai"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"id":"resp_1","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello from assistant"}]}],"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     3,
				CompletionTokens: 5,
				TotalTokens:      8,
			},
		},
		want: cassetteExpectation{
			replayContains:   `"total_tokens":8`,
			messageContains:  "hello from openai",
			historyContains:  []string{"hello from openai"},
			messageCount:     1,
			aiContent:        "hello from assistant",
			promptTokens:     3,
			completionTokens: 5,
			statusCode:       200,
		},
	}
}

func openAIResponsesMultiTurnFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_responses_multi_turn_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityMultiTurn,
			capabilityHistory,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationResponses,
			endpoint:        "/v1/responses",
			url:             "/v1/responses",
			method:          "POST",
			model:           "gpt-5",
			requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"system","content":[{"type":"input_text","text":"You are concise."}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"summarize our chat"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"id":"resp_multi_turn","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"You said hello and I replied hi there."}]}],"usage":{"input_tokens":14,"output_tokens":9,"total_tokens":23}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     14,
				CompletionTokens: 9,
				TotalTokens:      23,
			},
		},
		want: cassetteExpectation{
			replayContains:   `You said hello and I replied hi there.`,
			messageContains:  "summarize our chat",
			historyContains:  []string{"You are concise.", "hello", "hi there", "summarize our chat"},
			messageCount:     4,
			aiContent:        "You said hello and I replied hi there.",
			promptTokens:     14,
			completionTokens: 9,
			statusCode:       200,
		},
	}
}

func openAIResponsesToolCallStreamFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_responses_tool_call_stream",
		capabilities: []cassetteCapability{
			capabilityStream,
			capabilityReasoning,
			capabilityToolCall,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationResponses,
			endpoint:        "/v1/responses",
			url:             "/v1/responses",
			method:          "POST",
			model:           "gpt-5",
			requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect logs"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: stringsJoin(
				"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"inspect logs\",\"item_id\":\"rs_1\"}",
				"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"exec_command\"}}",
				"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"cmd\\\":\\\"pwd\\\"}\",\"item_id\":\"fc_1\"}",
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}",
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":4,\"total_tokens\":13}}}",
			),
			isStream: true,
			usage: recordfile.UsageInfo{
				PromptTokens:     9,
				CompletionTokens: 4,
				TotalTokens:      13,
			},
			events: []recordfile.RecordEvent{
				{Type: "llm.reasoning.delta", Time: time.Date(2026, 4, 15, 12, 0, 0, 500000000, time.UTC), IsStream: true, Message: "inspect logs"},
				{Type: "llm.tool_call", Time: time.Date(2026, 4, 15, 12, 0, 1, 0, time.UTC), IsStream: true, Attributes: map[string]interface{}{"id": "call_1", "name": "exec_command", "type": "function_call"}},
				{Type: "llm.tool_call.delta", Time: time.Date(2026, 4, 15, 12, 0, 2, 0, time.UTC), IsStream: true, Attributes: map[string]interface{}{"id": "fc_1", "arguments": "{\"cmd\":\"pwd\"}"}},
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 0, 3, 0, time.UTC), IsStream: true, Message: "done"},
				{Type: "llm.usage", Time: time.Date(2026, 4, 15, 12, 0, 4, 0, time.UTC), IsStream: true, Attributes: map[string]interface{}{"prompt_tokens": 9, "completion_tokens": 4, "total_tokens": 13}},
			},
		},
		want: cassetteExpectation{
			replayContains:   "data:",
			messageContains:  "inspect logs",
			historyContains:  []string{"inspect logs"},
			messageCount:     1,
			aiContent:        "done",
			aiReasoning:      "inspect logs",
			promptTokens:     9,
			completionTokens: 4,
			statusCode:       200,
			toolCallName:     "exec_command",
			eventTypes:       []string{"llm.reasoning.delta", "llm.tool_call", "llm.tool_call.delta", "llm.output_text.delta", "llm.usage"},
		},
	}
}

func openAIResponsesToolResultFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_responses_tool_result_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityToolCall,
			capabilityToolResult,
			capabilityHistory,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationResponses,
			endpoint:        "/v1/responses",
			url:             "/v1/responses",
			method:          "POST",
			model:           "gpt-5",
			requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"where am i?"}]},{"type":"function_call","call_id":"call_hist","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},{"type":"function_call_output","call_id":"call_hist","output":{"cwd":"/tmp/project"}}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"id":"resp_tool_result","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"You are in /tmp/project"}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		},
		want: cassetteExpectation{
			replayContains:   `You are in /tmp/project`,
			messageContains:  "where am i?",
			historyContains:  []string{"where am i?", "/tmp/project"},
			messageCount:     3,
			aiContent:        "You are in /tmp/project",
			promptTokens:     11,
			completionTokens: 7,
			statusCode:       200,
			toolResultText:   `/tmp/project`,
			toolResultType:   "function_call_output",
		},
	}
}

func anthropicMessagesNonStreamFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "anthropic_messages_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderAnthropic,
			operation:       llm.OperationMessages,
			endpoint:        "/v1/messages",
			url:             "/v1/messages",
			method:          "POST",
			model:           "claude-sonnet-4-5",
			requestProtocol: "POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"claude-sonnet-4-5","system":"Be concise","messages":[{"role":"user","content":[{"type":"text","text":"hello from anthropic"}]}],"max_tokens":16}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hello from claude"}],"usage":{"input_tokens":4,"output_tokens":6}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     4,
				CompletionTokens: 6,
				TotalTokens:      10,
			},
		},
		want: cassetteExpectation{
			replayContains:   `"output_tokens":6`,
			messageContains:  "hello from anthropic",
			historyContains:  []string{"Be concise", "hello from anthropic"},
			messageCount:     2,
			aiContent:        "hello from claude",
			promptTokens:     4,
			completionTokens: 6,
			statusCode:       200,
		},
	}
}

func anthropicMessagesStreamFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "anthropic_messages_stream",
		capabilities: []cassetteCapability{
			capabilityStream,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderAnthropic,
			operation:       llm.OperationMessages,
			endpoint:        "/v1/messages",
			url:             "/v1/messages",
			method:          "POST",
			model:           "claude-sonnet-4-5",
			requestProtocol: "POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello from anthropic stream"}]}],"max_tokens":16}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: stringsJoin(
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}",
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Claude\"}}",
				"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":5,\"output_tokens\":7}}",
			),
			isStream: true,
			usage: recordfile.UsageInfo{
				PromptTokens:     5,
				CompletionTokens: 7,
				TotalTokens:      12,
			},
			events: []recordfile.RecordEvent{
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 1, 1, 0, time.UTC), IsStream: true, Message: "Hello "},
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 1, 2, 0, time.UTC), IsStream: true, Message: "Claude"},
				{Type: "llm.usage", Time: time.Date(2026, 4, 15, 12, 1, 3, 0, time.UTC), IsStream: true, Attributes: map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12}},
			},
		},
		want: cassetteExpectation{
			replayContains:   "data:",
			messageContains:  "hello from anthropic stream",
			historyContains:  []string{"hello from anthropic stream"},
			messageCount:     1,
			aiContent:        "Hello Claude",
			promptTokens:     5,
			completionTokens: 7,
			statusCode:       200,
			eventTypes:       []string{"llm.output_text.delta", "llm.usage"},
		},
	}
}

func anthropicToolErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "anthropic_tool_error_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityToolResult,
			capabilityError,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderAnthropic,
			operation:       llm.OperationMessages,
			endpoint:        "/v1/messages",
			url:             "/v1/messages",
			method:          "POST",
			model:           "claude-sonnet-4-5",
			requestProtocol: "POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"claude-sonnet-4-5","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_err","name":"Bash","input":{"command":"exit 1"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_err","is_error":true,"content":"stacktrace"}]}],"max_tokens":16}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"id":"msg_err","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"command failed"}],"usage":{"input_tokens":6,"output_tokens":4}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     6,
				CompletionTokens: 4,
				TotalTokens:      10,
			},
		},
		want: cassetteExpectation{
			replayContains:   `"output_tokens":4`,
			messageContains:  "stacktrace",
			historyContains:  []string{"stacktrace"},
			messageCount:     2,
			aiContent:        "command failed",
			promptTokens:     6,
			completionTokens: 4,
			statusCode:       200,
			errorContent:     "stacktrace",
		},
	}
}

func openAIProviderErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_responses_provider_error_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityProviderErr,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationResponses,
			endpoint:        "/v1/responses",
			url:             "/v1/responses",
			method:          "POST",
			model:           "gpt-5",
			requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"trigger rate limit"}]}]}`,
			responseStatus:  "429 Too Many Requests",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
		},
		want: cassetteExpectation{
			replayContains:   `Rate limit exceeded`,
			messageContains:  "trigger rate limit",
			historyContains:  []string{"trigger rate limit"},
			messageCount:     1,
			aiContent:        "",
			aiBlockCount:     1,
			aiBlockTitles:    []string{"Provider Error"},
			statusCode:       429,
			blockContains:    "Rate limit exceeded",
			promptTokens:     0,
			completionTokens: 0,
		},
	}
}

func anthropicProviderErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "anthropic_messages_provider_error_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityProviderErr,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderAnthropic,
			operation:       llm.OperationMessages,
			endpoint:        "/v1/messages",
			url:             "/v1/messages",
			method:          "POST",
			model:           "claude-sonnet-4-5",
			requestProtocol: "POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"trigger overload"}]}],"max_tokens":16}`,
			responseStatus:  "503 Service Unavailable",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"type":"error","error":{"type":"overloaded_error","message":"Anthropic overloaded"}}`,
		},
		want: cassetteExpectation{
			replayContains:   `Anthropic overloaded`,
			messageContains:  "trigger overload",
			historyContains:  []string{"trigger overload"},
			messageCount:     1,
			aiContent:        "",
			aiBlockCount:     1,
			aiBlockTitles:    []string{"Provider Error"},
			statusCode:       503,
			blockContains:    "Anthropic overloaded",
			promptTokens:     0,
			completionTokens: 0,
		},
	}
}

func googleProviderErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "google_genai_provider_error_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityProviderErr,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderGoogleGenAI,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1beta/models:generateContent",
			url:             "/v1beta/models/gemini-2.5-flash:generateContent",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1beta/models/gemini-2.5-flash:generateContent HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"trigger quota"}]}]}`,
			responseStatus:  "429 Too Many Requests",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`,
		},
		want: cassetteExpectation{
			replayContains:   `Quota exceeded`,
			messageContains:  "trigger quota",
			historyContains:  []string{"trigger quota"},
			messageCount:     1,
			aiContent:        "",
			aiBlockCount:     1,
			aiBlockTitles:    []string{"Provider Error"},
			statusCode:       429,
			blockContains:    "Quota exceeded",
			promptTokens:     0,
			completionTokens: 0,
		},
	}
}

func openAIResponsesStreamErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_responses_stream_error",
		capabilities: []cassetteCapability{
			capabilityStream,
			capabilityProviderErr,
			capabilityStreamError,
			capabilityPartialComp,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationResponses,
			endpoint:        "/v1/responses",
			url:             "/v1/responses",
			method:          "POST",
			model:           "gpt-5",
			requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"stream failure"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: stringsJoin(
				`data: {"type":"response.output_text.delta","delta":"partial"}`,
				`data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error","code":"stream_aborted"}}}`,
			),
			isStream: true,
		},
		want: cassetteExpectation{
			replayContains:  `response.failed`,
			messageContains: "stream failure",
			historyContains: []string{"stream failure"},
			messageCount:    1,
			aiContent:       "partial",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Provider Error"},
			statusCode:      200,
			blockContains:   "stream aborted",
		},
	}
}

func anthropicMessagesStreamErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "anthropic_messages_stream_error",
		capabilities: []cassetteCapability{
			capabilityStream,
			capabilityProviderErr,
			capabilityStreamError,
			capabilityPartialComp,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderAnthropic,
			operation:       llm.OperationMessages,
			endpoint:        "/v1/messages",
			url:             "/v1/messages",
			method:          "POST",
			model:           "claude-sonnet-4-5",
			requestProtocol: "POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"stream failure"}]}],"max_tokens":16}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: stringsJoin(
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
				`data: {"type":"error","error":{"type":"overloaded_error","message":"stream overloaded"}}`,
			),
			isStream: true,
		},
		want: cassetteExpectation{
			replayContains:  `stream overloaded`,
			messageContains: "stream failure",
			historyContains: []string{"stream failure"},
			messageCount:    1,
			aiContent:       "partial",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Provider Error"},
			statusCode:      200,
			blockContains:   "stream overloaded",
		},
	}
}

func googleGenAIStreamErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "google_genai_stream_error",
		capabilities: []cassetteCapability{
			capabilityStream,
			capabilityProviderErr,
			capabilityStreamError,
			capabilityPartialComp,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderGoogleGenAI,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1beta/models:streamGenerateContent",
			url:             "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"stream failure"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: stringsJoin(
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`,
				`data: {"error":{"code":429,"message":"stream quota exceeded","status":"RESOURCE_EXHAUSTED"}}`,
			),
			isStream: true,
		},
		want: cassetteExpectation{
			replayContains:  `stream quota exceeded`,
			messageContains: "stream failure",
			historyContains: []string{"stream failure"},
			messageCount:    1,
			aiContent:       "partial",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Provider Error"},
			statusCode:      200,
			blockContains:   "stream quota exceeded",
		},
	}
}

func googleGenAIStreamFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "google_genai_stream",
		capabilities: []cassetteCapability{
			capabilityStream,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderGoogleGenAI,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1beta/models:streamGenerateContent",
			url:             "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"hello from gemini"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello \"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Gemini\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":7,\"totalTokenCount\":10}}\n\n",
			isStream: true,
			usage: recordfile.UsageInfo{
				PromptTokens:     3,
				CompletionTokens: 7,
				TotalTokens:      10,
			},
			events: []recordfile.RecordEvent{
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 2, 1, 0, time.UTC), IsStream: true, Message: "Hello ", Attributes: map[string]interface{}{"role": "model"}},
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 2, 2, 0, time.UTC), IsStream: true, Message: "Gemini", Attributes: map[string]interface{}{"role": "model"}},
				{Type: "llm.usage", Time: time.Date(2026, 4, 15, 12, 2, 3, 0, time.UTC), IsStream: true, Attributes: map[string]interface{}{"prompt_tokens": 3, "completion_tokens": 7, "total_tokens": 10}},
			},
		},
		want: cassetteExpectation{
			replayContains:   "data:",
			messageContains:  "hello from gemini",
			historyContains:  []string{"hello from gemini"},
			messageCount:     1,
			aiContent:        "Hello Gemini",
			promptTokens:     3,
			completionTokens: 7,
			statusCode:       200,
			eventTypes:       []string{"llm.output_text.delta", "llm.usage"},
		},
	}
}

func googleGenAIMixedBlocksFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "google_genai_mixed_blocks_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityMixedBlocks,
			capabilitySafety,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderGoogleGenAI,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1beta/models:generateContent",
			url:             "/v1beta/models/gemini-2.5-flash:generateContent",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1beta/models/gemini-2.5-flash:generateContent HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"systemInstruction":{"role":"system","parts":[{"text":"Be safe"}]},"contents":[{"role":"user","parts":[{"text":"unsafe request"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HATE_SPEECH","probability":"HIGH","blocked":true}]}],"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     2,
				CompletionTokens: 1,
				TotalTokens:      3,
			},
		},
		want: cassetteExpectation{
			replayContains:   `"blockReason":"SAFETY"`,
			messageContains:  "unsafe request",
			historyContains:  []string{"Be safe", "unsafe request"},
			messageCount:     2,
			aiContent:        "partial",
			aiBlockCount:     2,
			aiBlockTitles:    []string{"Prompt Feedback", "Safety Ratings"},
			promptTokens:     2,
			completionTokens: 1,
			statusCode:       200,
		},
	}
}

func googleGenAIBlockedFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "google_genai_blocked_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilitySafety,
			capabilityRefusal,
			capabilityMixedBlocks,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderGoogleGenAI,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1beta/models:generateContent",
			url:             "/v1beta/models/gemini-2.5-flash:generateContent",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1beta/models/gemini-2.5-flash:generateContent HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"unsafe request"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":0,"totalTokenCount":2}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     2,
				CompletionTokens: 0,
				TotalTokens:      2,
			},
		},
		want: cassetteExpectation{
			replayContains:   `"blockReason":"SAFETY"`,
			messageContains:  "unsafe request",
			historyContains:  []string{"unsafe request"},
			messageCount:     1,
			aiContent:        "",
			promptTokens:     2,
			completionTokens: 0,
			statusCode:       200,
			blockContains:    "SAFETY",
		},
	}
}

func openAIModelsFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "openai_models_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityModelList,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderOpenAICompatible,
			operation:       llm.OperationModels,
			endpoint:        "/v1/models",
			url:             "/v1/models",
			method:          "GET",
			model:           "list_models",
			requestProtocol: "GET /v1/models HTTP/1.1\r\nHost: example.com\r\n\r\n",
			requestBody:     `{}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"data":[{"id":"gpt-5","object":"model"},{"id":"gpt-4.1-mini","object":"model"}]}`,
		},
		want: cassetteExpectation{
			replayContains:  `"gpt-5"`,
			messageContains: "List available models",
			historyContains: []string{"List available models"},
			messageCount:    1,
			aiContent:       "gpt-5\ngpt-4.1-mini",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Model List"},
			statusCode:      200,
			blockContains:   "gpt-5",
		},
	}
}

func googleGenAIModelsFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "google_genai_models_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityModelList,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderGoogleGenAI,
			operation:       llm.OperationModels,
			endpoint:        "/v1beta/models",
			url:             "/v1beta/models",
			method:          "GET",
			model:           "list_models",
			requestProtocol: "GET /v1beta/models HTTP/1.1\r\nHost: example.com\r\n\r\n",
			requestBody:     `{}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"models":[{"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"},{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro"}]}`,
		},
		want: cassetteExpectation{
			replayContains:  `"models/gemini-2.5-flash"`,
			messageContains: "List available models",
			historyContains: []string{"List available models"},
			messageCount:    1,
			aiContent:       "models/gemini-2.5-flash\nmodels/gemini-2.5-pro",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Model List"},
			statusCode:      200,
			blockContains:   "gemini-2.5-flash",
		},
	}
}

func vertexNativeNonStreamHistoryFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "vertex_native_non_stream_history",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityHistory,
			capabilityMixedBlocks,
			capabilitySafety,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderVertexNative,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1/publishers/models:generateContent",
			url:             "/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"systemInstruction":{"role":"system","parts":[{"text":"Be safe"}]},"contents":[{"role":"user","parts":[{"text":"hello"}]},{"role":"model","parts":[{"text":"hi there"}]},{"role":"user","parts":[{"text":"unsafe request"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"vertex partial"}]},"finishReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HATE_SPEECH","probability":"HIGH","blocked":true}]}],"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`,
			usage: recordfile.UsageInfo{
				PromptTokens:     5,
				CompletionTokens: 2,
				TotalTokens:      7,
			},
		},
		want: cassetteExpectation{
			replayContains:   `"blockReason":"SAFETY"`,
			messageContains:  "unsafe request",
			historyContains:  []string{"Be safe", "hello", "hi there", "unsafe request"},
			messageCount:     4,
			aiContent:        "vertex partial",
			aiBlockCount:     2,
			aiBlockTitles:    []string{"Prompt Feedback", "Safety Ratings"},
			promptTokens:     5,
			completionTokens: 2,
			statusCode:       200,
		},
	}
}

func vertexNativeProviderErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "vertex_native_provider_error_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityProviderErr,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderVertexNative,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1/publishers/models:generateContent",
			url:             "/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"trigger permission denial"}]}]}`,
			responseStatus:  "403 Forbidden",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"error":{"code":403,"message":"Vertex permission denied","status":"PERMISSION_DENIED"}}`,
		},
		want: cassetteExpectation{
			replayContains:   `Vertex permission denied`,
			messageContains:  "trigger permission denial",
			historyContains:  []string{"trigger permission denial"},
			messageCount:     1,
			aiContent:        "",
			aiBlockCount:     1,
			aiBlockTitles:    []string{"Provider Error"},
			statusCode:       403,
			blockContains:    "Vertex permission denied",
			promptTokens:     0,
			completionTokens: 0,
		},
	}
}

func vertexNativeStreamErrorFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "vertex_native_stream_error",
		capabilities: []cassetteCapability{
			capabilityStream,
			capabilityProviderErr,
			capabilityStreamError,
			capabilityPartialComp,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderVertexNative,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1/publishers/models:streamGenerateContent",
			url:             "/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"stream failure"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: stringsJoin(
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`,
				`data: {"error":{"code":403,"message":"vertex stream denied","status":"PERMISSION_DENIED"}}`,
			),
			isStream: true,
		},
		want: cassetteExpectation{
			replayContains:  `vertex stream denied`,
			messageContains: "stream failure",
			historyContains: []string{"stream failure"},
			messageCount:    1,
			aiContent:       "partial",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Provider Error"},
			statusCode:      200,
			blockContains:   "vertex stream denied",
		},
	}
}

func vertexNativeStreamFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "vertex_native_stream",
		capabilities: []cassetteCapability{
			capabilityStream,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderVertexNative,
			operation:       llm.OperationGenerateContent,
			endpoint:        "/v1/publishers/models:streamGenerateContent",
			url:             "/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			method:          "POST",
			model:           "gemini-2.5-flash",
			requestProtocol: "POST /v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
			requestBody:     `{"contents":[{"role":"user","parts":[{"text":"hello from vertex"}]}]}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: text/event-stream\r\n",
			responseBody: "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello \"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Vertex\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":7,\"totalTokenCount\":10}}\n\n",
			isStream: true,
			usage: recordfile.UsageInfo{
				PromptTokens:     3,
				CompletionTokens: 7,
				TotalTokens:      10,
			},
			events: []recordfile.RecordEvent{
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 3, 1, 0, time.UTC), IsStream: true, Message: "Hello ", Attributes: map[string]interface{}{"role": "model"}},
				{Type: "llm.output_text.delta", Time: time.Date(2026, 4, 15, 12, 3, 2, 0, time.UTC), IsStream: true, Message: "Vertex", Attributes: map[string]interface{}{"role": "model"}},
				{Type: "llm.usage", Time: time.Date(2026, 4, 15, 12, 3, 3, 0, time.UTC), IsStream: true, Attributes: map[string]interface{}{"prompt_tokens": 3, "completion_tokens": 7, "total_tokens": 10}},
			},
		},
		want: cassetteExpectation{
			replayContains:   "data:",
			messageContains:  "hello from vertex",
			historyContains:  []string{"hello from vertex"},
			messageCount:     1,
			aiContent:        "Hello Vertex",
			promptTokens:     3,
			completionTokens: 7,
			statusCode:       200,
			eventTypes:       []string{"llm.output_text.delta", "llm.usage"},
		},
	}
}

func vertexNativeModelsFixture() cassetteFixtureCase {
	return cassetteFixtureCase{
		name: "vertex_native_models_non_stream",
		capabilities: []cassetteCapability{
			capabilityNonStream,
			capabilityModelList,
		},
		spec: cassetteSpec{
			provider:        llm.ProviderVertexNative,
			operation:       llm.OperationModels,
			endpoint:        "/v1/publishers/models",
			url:             "/v1/publishers/google/models",
			method:          "GET",
			model:           "list_models",
			requestProtocol: "GET /v1/publishers/google/models HTTP/1.1\r\nHost: example.com\r\n\r\n",
			requestBody:     `{}`,
			responseStatus:  "200 OK",
			responseHeaders: "Content-Type: application/json\r\n",
			responseBody:    `{"publisherModels":[{"name":"publishers/google/models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash"},{"name":"publishers/google/models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro"}]}`,
		},
		want: cassetteExpectation{
			replayContains:  `"publishers/google/models/gemini-2.5-flash"`,
			messageContains: "List available models",
			historyContains: []string{"List available models"},
			messageCount:    1,
			aiContent:       "publishers/google/models/gemini-2.5-flash\npublishers/google/models/gemini-2.5-pro",
			aiBlockCount:    1,
			aiBlockTitles:   []string{"Model List"},
			statusCode:      200,
			blockContains:   "gemini-2.5-flash",
		},
	}
}

func stringsJoin(lines ...string) string {
	return strings.Join(lines, "\n\n") + "\n\n"
}

func (c cassetteFixtureCase) hasCapability(capability cassetteCapability) bool {
	for _, got := range c.capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

func writeCassetteFixture(t *testing.T, spec cassetteSpec) (string, []byte) {
	t.Helper()

	reqHead := []byte(spec.requestProtocol)
	reqBody := []byte(spec.requestBody)
	resHead := []byte("HTTP/1.1 " + spec.responseStatus + "\r\n" + spec.responseHeaders + "\r\n")
	resBody := []byte(spec.responseBody)

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:  "test-" + spec.provider,
			Time:       time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
			Model:      spec.model,
			Provider:   spec.provider,
			Operation:  spec.operation,
			Endpoint:   spec.endpoint,
			URL:        spec.url,
			Method:     spec.method,
			StatusCode: parseHTTPStatusCode(spec.responseStatus),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHead)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHead)),
			ResBodyLen:   int64(len(resBody)),
			IsStream:     spec.isStream,
		},
		Usage: spec.usage,
	}

	events := recordfile.BuildEvents(header)
	if len(spec.events) > 0 {
		events = append(events, spec.events...)
	}
	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}

	content := append([]byte{}, prelude...)
	content = append(content, reqHead...)
	content = append(content, reqBody...)
	content = append(content, '\n')
	content = append(content, resHead...)
	content = append(content, resBody...)

	path := filepath.Join(t.TempDir(), spec.provider+".http")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path, content
}

func parseHTTPStatusCode(statusLine string) int {
	fields := strings.Fields(statusLine)
	if len(fields) == 0 {
		return 200
	}
	code, err := strconv.Atoi(fields[0])
	if err != nil {
		return 200
	}
	return code
}
