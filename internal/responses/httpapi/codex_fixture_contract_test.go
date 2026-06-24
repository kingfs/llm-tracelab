package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/codexfixtures"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

func loadCodexFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := codexfixtures.Load(name)
	if err != nil {
		t.Fatalf("read codex fixture %s: %v", name, err)
	}
	return data
}

func decodeCodexFixture[T any](t *testing.T, name string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(loadCodexFixture(t, name), &out); err != nil {
		t.Fatalf("decode codex fixture %s: %v", name, err)
	}
	return out
}

func TestCodexFixtureRunnerValidatesEveryCurrentFixture(t *testing.T) {
	if err := codexfixtures.ValidateAll(); err != nil {
		t.Fatal(err)
	}

	names, err := codexfixtures.Names()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"codex_chinese_news_stream_absent_tools_request.json",
		"codex_web_search_absent_tools_request.json",
		"forced_web_search_request.json",
		"function_call_expected_response.json",
		"function_call_output_continuation_request.json",
		"function_call_request.json",
		"ordinary_web_search_descriptor_request.json",
		"stream_text_events.ndjson",
		"stream_text_request.json",
		"text_create_expected_response.json",
		"text_create_request.json",
		"unsupported_code_interpreter_request.json",
		"unsupported_computer_use_preview_request.json",
		"unsupported_file_search_request.json",
		"unsupported_hosted_tool_expected_error.json",
		"unsupported_mcp_request.json",
		"web_search_preview_extended_descriptor_request.json",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("codex fixture inventory changed without runner update\nwant: %#v\n got: %#v", want, names)
	}
}

func requireStoreTrue(t *testing.T, req protocol.CreateResponseRequest) {
	t.Helper()
	if req.Store == nil || !*req.Store {
		t.Fatalf("store = %v, want explicit true", req.Store)
	}
}

func requireCodexThread(t *testing.T, req protocol.CreateResponseRequest, want string) {
	t.Helper()
	codex, ok := req.Metadata["codex"].(map[string]any)
	if !ok {
		t.Fatalf("metadata.codex = %#v, want object", req.Metadata["codex"])
	}
	if got, _ := codex["thread_id"].(string); got != want {
		t.Fatalf("metadata.codex.thread_id = %q, want %q", got, want)
	}
}

func TestCodexFixtureCreateRequestContracts(t *testing.T) {
	t.Run("text create request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "text_create_request.json")

		if req.Model != "local-test-model" {
			t.Fatalf("model = %q, want local-test-model", req.Model)
		}
		if req.Instructions == "" {
			t.Fatalf("instructions must be present")
		}
		if got, ok := req.Input.(string); !ok || got == "" {
			t.Fatalf("input = %#v, want non-empty string", req.Input)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_text")
		if len(req.Tools) != 0 || req.Stream {
			t.Fatalf("unexpected tools/stream fields: tools=%#v stream=%v", req.Tools, req.Stream)
		}
	})

	t.Run("stream text request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "stream_text_request.json")

		if !req.Stream {
			t.Fatalf("stream = false, want true")
		}
		if req.Instructions == "" {
			t.Fatalf("instructions must be present")
		}
		if got, ok := req.Input.(string); !ok || got == "" {
			t.Fatalf("input = %#v, want non-empty string", req.Input)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_stream")
	})

	t.Run("function call request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "function_call_request.json")

		if len(req.Tools) != 1 {
			t.Fatalf("tools len = %d, want 1", len(req.Tools))
		}
		tool := req.Tools[0]
		if tool.Type != "function" || tool.Name != "read_file" {
			t.Fatalf("tool = %#v, want function read_file", tool)
		}
		if tool.Parameters["type"] != "object" {
			t.Fatalf("parameters.type = %#v, want object", tool.Parameters["type"])
		}
		if req.ToolChoice != "auto" {
			t.Fatalf("tool_choice = %#v, want auto", req.ToolChoice)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_tool")
	})

	t.Run("function call output continuation request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "function_call_output_continuation_request.json")

		if req.PreviousResponseID != "resp_fixture_tool_call" {
			t.Fatalf("previous_response_id = %q, want resp_fixture_tool_call", req.PreviousResponseID)
		}
		items, ok := req.Input.([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("input = %#v, want one array item", req.Input)
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("input[0] = %#v, want object", items[0])
		}
		if item["type"] != "function_call_output" || item["call_id"] != "call_read_file_1" {
			t.Fatalf("input[0] = %#v, want function_call_output for call_read_file_1", item)
		}
		output, ok := item["output"].(string)
		if !ok || !json.Valid([]byte(output)) {
			t.Fatalf("input[0].output = %#v, want JSON string", item["output"])
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_tool")
	})

	t.Run("ordinary web_search descriptor request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "ordinary_web_search_descriptor_request.json")

		if !req.Stream {
			t.Fatalf("stream = false, want true")
		}
		if len(req.Tools) != 1 {
			t.Fatalf("tools len = %d, want 1", len(req.Tools))
		}
		tool := req.Tools[0]
		if tool.Type != "web_search" || tool.MaxNumResults != 2 {
			t.Fatalf("tool = %#v, want web_search with max_num_results=2", tool)
		}
		if req.ToolChoice != "auto" {
			t.Fatalf("tool_choice = %#v, want auto", req.ToolChoice)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_web_search")
	})

	t.Run("Codex web search absent tools request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "codex_web_search_absent_tools_request.json")

		if req.Model != "local-test-model" {
			t.Fatalf("model = %q, want local-test-model", req.Model)
		}
		if got, ok := req.Input.(string); !ok || !strings.Contains(got, "Search the web") {
			t.Fatalf("input = %#v, want web search prompt", req.Input)
		}
		if len(req.Tools) != 0 || req.ToolChoice != nil {
			t.Fatalf("raw Codex request must omit tools and tool_choice: tools=%#v tool_choice=%#v", req.Tools, req.ToolChoice)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_web_search_absent_tools")
	})

	t.Run("Codex Chinese news stream absent tools request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "codex_chinese_news_stream_absent_tools_request.json")

		if !req.Stream {
			t.Fatalf("stream = false, want true")
		}
		if got, ok := req.Input.(string); !ok || !strings.Contains(got, "搜索今日新闻") {
			t.Fatalf("input = %#v, want Chinese today's news prompt", req.Input)
		}
		if len(req.Tools) != 0 || req.ToolChoice != nil {
			t.Fatalf("raw Codex request must omit tools and tool_choice: tools=%#v tool_choice=%#v", req.Tools, req.ToolChoice)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_chinese_news_stream_absent_tools")
	})

	t.Run("web_search_preview extended descriptor request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "web_search_preview_extended_descriptor_request.json")

		if !req.Stream {
			t.Fatalf("stream = false, want true")
		}
		if len(req.Tools) != 1 {
			t.Fatalf("tools len = %d, want 1", len(req.Tools))
		}
		tool := req.Tools[0]
		if tool.Type != "web_search_preview" || tool.MaxNumResults != 4 {
			t.Fatalf("tool = %#v, want web_search_preview with max_num_results=4", tool)
		}
		if tool.Extra["external_web_access"] != true {
			t.Fatalf("external_web_access extra = %#v, want true", tool.Extra["external_web_access"])
		}
		if include, ok := tool.Extra["include"].([]any); !ok || len(include) != 1 || include[0] != "web_search_call.action.sources" {
			t.Fatalf("include extra = %#v, want sources include", tool.Extra["include"])
		}
		if contentTypes, ok := tool.Extra["search_content_types"].([]any); !ok || len(contentTypes) != 2 || contentTypes[0] != "news" || contentTypes[1] != "webpage" {
			t.Fatalf("search_content_types extra = %#v, want news/webpage", tool.Extra["search_content_types"])
		}
		if req.ToolChoice != "auto" {
			t.Fatalf("tool_choice = %#v, want auto", req.ToolChoice)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_web_search_preview_extended")
	})

	t.Run("forced web_search request shape", func(t *testing.T) {
		req := decodeCodexFixture[protocol.CreateResponseRequest](t, "forced_web_search_request.json")

		if len(req.Tools) != 1 {
			t.Fatalf("tools len = %d, want 1", len(req.Tools))
		}
		tool := req.Tools[0]
		if tool.Type != "web_search" || tool.MaxNumResults != 3 || tool.UserLocation == nil {
			t.Fatalf("tool = %#v, want forced web_search descriptor", tool)
		}
		choice, ok := req.ToolChoice.(map[string]any)
		if !ok || choice["type"] != "web_search" {
			t.Fatalf("tool_choice = %#v, want web_search object", req.ToolChoice)
		}
		requireStoreTrue(t, req)
		requireCodexThread(t, req, "thread_fixture_forced_web_search")
	})

	for _, tc := range []struct {
		name       string
		toolType   string
		threadID   string
		extraField string
	}{
		{name: "unsupported_mcp_request.json", toolType: "mcp", threadID: "thread_fixture_unsupported_mcp"},
		{name: "unsupported_file_search_request.json", toolType: "file_search", threadID: "thread_fixture_unsupported_file_search", extraField: "vector_store_ids"},
		{name: "unsupported_code_interpreter_request.json", toolType: "code_interpreter", threadID: "thread_fixture_unsupported_code_interpreter", extraField: "container"},
		{name: "unsupported_computer_use_preview_request.json", toolType: "computer_use_preview", threadID: "thread_fixture_unsupported_computer_use_preview", extraField: "display_width"},
	} {
		t.Run(tc.name+" shape", func(t *testing.T) {
			req := decodeCodexFixture[protocol.CreateResponseRequest](t, tc.name)

			if len(req.Tools) != 1 || req.Tools[0].Type != tc.toolType {
				t.Fatalf("tools = %#v, want one %s descriptor", req.Tools, tc.toolType)
			}
			choice, ok := req.ToolChoice.(map[string]any)
			if !ok || choice["type"] != tc.toolType {
				t.Fatalf("tool_choice = %#v, want %s object", req.ToolChoice, tc.toolType)
			}
			if tc.extraField != "" && req.Tools[0].Extra[tc.extraField] == nil {
				t.Fatalf("%s was not preserved in Extra: %#v", tc.extraField, req.Tools[0].Extra)
			}
			requireStoreTrue(t, req)
			requireCodexThread(t, req, tc.threadID)
		})
	}
}

func TestCodexFixtureExpectedResponseContracts(t *testing.T) {
	text := decodeCodexFixture[protocol.Response](t, "text_create_expected_response.json")
	if text.ID != "resp_fixture_text" || text.Object != "response" || text.Status != "completed" || text.Model != "local-test-model" {
		t.Fatalf("text expected response header mismatch: %#v", text)
	}
	if len(text.Output) != 1 || text.Output[0].Type != "message" || text.Output[0].Role != "assistant" || len(text.Output[0].Content) != 1 || text.Output[0].Content[0].Type != "output_text" || text.Output[0].Content[0].Text == "" {
		t.Fatalf("text expected output mismatch: %#v", text.Output)
	}

	functionCall := decodeCodexFixture[protocol.Response](t, "function_call_expected_response.json")
	if functionCall.ID != "resp_fixture_tool_call" || functionCall.Status != "completed" || functionCall.Model != "local-test-model" {
		t.Fatalf("function expected response header mismatch: %#v", functionCall)
	}
	if len(functionCall.Output) != 1 {
		t.Fatalf("function expected output len = %d, want 1", len(functionCall.Output))
	}
	item := functionCall.Output[0]
	if item.Type != "function_call" || item.CallID != "call_read_file_1" || item.Name != "read_file" || !json.Valid([]byte(item.Arguments)) {
		t.Fatalf("function expected output mismatch: %#v", item)
	}
}

func TestCodexStreamTextEventFixtureContract(t *testing.T) {
	data := loadCodexFixture(t, "stream_text_events.ndjson")
	scanner := bufio.NewScanner(bytes.NewReader(data))
	gotTypes := []string{}
	var deltaText strings.Builder
	var doneText string
	var completed *protocol.Response

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			t.Fatalf("stream_text_events.ndjson must not contain blank lines")
		}
		var event protocol.StreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode stream event %q: %v", string(line), err)
		}
		gotTypes = append(gotTypes, event.Type)
		if event.Type == "response.output_text.delta" {
			deltaText.WriteString(event.Delta)
		}
		if event.Type == "response.output_text.done" {
			doneText = event.Text
		}
		if event.Type == "response.completed" {
			completed = event.Response
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stream_text_events.ndjson: %v", err)
	}

	wantTypes := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event types mismatch\nwant: %#v\n got: %#v", wantTypes, gotTypes)
	}
	if deltaText.String() != doneText || doneText != "Hello" {
		t.Fatalf("stream text mismatch: deltas=%q done=%q", deltaText.String(), doneText)
	}
	if completed == nil || completed.ID != "resp_fixture_stream" || completed.Status != "completed" || len(completed.Output) != 1 {
		t.Fatalf("completed event response mismatch: %#v", completed)
	}
}

func TestCodexUnsupportedHostedToolExpectedErrorFixtureContract(t *testing.T) {
	fx := decodeCodexFixture[codexfixtures.ExpectedErrorFixture](t, "unsupported_hosted_tool_expected_error.json")

	wantTypes := []string{"mcp", "file_search", "code_interpreter", "computer_use_preview"}
	if len(fx.RequestExamples) != len(wantTypes) {
		t.Fatalf("request_examples len = %d, want %d", len(fx.RequestExamples), len(wantTypes))
	}
	for i, req := range fx.RequestExamples {
		if len(req.Tools) != 1 {
			t.Fatalf("request_examples[%d].tools len = %d, want 1", i, len(req.Tools))
		}
		if got := req.Tools[0].Type; got != wantTypes[i] {
			t.Fatalf("request_examples[%d].tools[0].type = %q, want %q", i, got, wantTypes[i])
		}
		choice, ok := req.ToolChoice.(map[string]any)
		if !ok {
			t.Fatalf("request_examples[%d].tool_choice = %#v, want object", i, req.ToolChoice)
		}
		if got, _ := choice["type"].(string); got != wantTypes[i] {
			t.Fatalf("request_examples[%d].tool_choice.type = %q, want %q", i, got, wantTypes[i])
		}
		requireStoreTrue(t, req)
	}
	if fx.RequestExamples[0].Tools[0].ServerLabel != "workspace" {
		t.Fatalf("mcp server_label = %q, want workspace", fx.RequestExamples[0].Tools[0].ServerLabel)
	}
	if _, ok := fx.RequestExamples[1].Tools[0].Extra["vector_store_ids"]; !ok {
		t.Fatalf("file_search vector_store_ids was not preserved in Extra: %#v", fx.RequestExamples[1].Tools[0].Extra)
	}
	if _, ok := fx.RequestExamples[2].Tools[0].Extra["container"]; !ok {
		t.Fatalf("code_interpreter container was not preserved in Extra: %#v", fx.RequestExamples[2].Tools[0].Extra)
	}
	if fx.RequestExamples[3].Tools[0].Extra["display_width"] == nil || fx.RequestExamples[3].Tools[0].Extra["display_height"] == nil {
		t.Fatalf("computer_use_preview display fields were not preserved in Extra: %#v", fx.RequestExamples[3].Tools[0].Extra)
	}
	if !strings.Contains(fx.ExpectedError.Error.Message, "unsupported hosted tool") || fx.ExpectedError.Error.Type != "invalid_request_error" || fx.ExpectedError.Error.Code != "unsupported_tool" {
		t.Fatalf("expected_error mismatch: %#v", fx.ExpectedError)
	}
	if len(fx.Notes) == 0 {
		t.Fatalf("notes must document this as a pending runtime compatibility target")
	}
}

func TestCodexFixtureRequestsReachHTTPAPI(t *testing.T) {
	names, err := codexfixtures.RequestNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			rt := &fakeRuntime{
				createResp: protocol.Response{
					ID:     "resp_http_fixture",
					Object: "response",
					Status: "completed",
					Model:  "local-test-model",
				},
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(loadCodexFixture(t, name)))
			req.Header.Set("Content-Type", "application/json")

			NewHandler(rt).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if rt.createReq.Stream && !strings.Contains(rec.Body.String(), `"type":"response.completed"`) {
				t.Fatalf("stream body did not include response.completed: %s", rec.Body.String())
			}
			if rt.createReq.Model != "local-test-model" {
				t.Fatalf("runtime request model = %q, want local-test-model", rt.createReq.Model)
			}
			requireStoreTrue(t, rt.createReq)
		})
	}
}

func TestCodexOrdinaryWebSearchStreamFixtureReachesHTTPAPI(t *testing.T) {
	rt := &fakeIncrementalRuntime{
		streamResp: protocol.Response{
			ID:     "resp_http_fixture_stream",
			Object: "response",
			Status: "completed",
			Model:  "local-test-model",
			Output: []protocol.OutputItem{{
				ID:      "msg_http_fixture_stream",
				Type:    "message",
				Status:  "completed",
				Role:    "assistant",
				Content: []protocol.ContentPart{{Type: "output_text", Text: "Hello"}},
			}},
		},
		streamDeltas: []string{"Hello"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(loadCodexFixture(t, "ordinary_web_search_descriptor_request.json")))
	req.Header.Set("Content-Type", "application/json")

	NewHandler(rt).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !rt.streamReq.Stream || len(rt.streamReq.Tools) != 1 || rt.streamReq.Tools[0].Type != "web_search" {
		t.Fatalf("stream runtime request mismatch: %#v", rt.streamReq)
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.completed"`) {
		t.Fatalf("stream body did not include response.completed: %s", rec.Body.String())
	}
}
