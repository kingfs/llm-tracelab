package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

type fakeRuntime struct {
	createCtx       context.Context
	createReq       protocol.CreateResponseRequest
	createResp      protocol.Response
	createErr       error
	compactCtx      context.Context
	compactReq      protocol.CompactResponseRequest
	compactResp     protocol.Response
	compactErr      error
	inputItemsID    string
	inputItemsResp  protocol.InputItemList
	inputItemsFound bool
	inputItemsErr   error
}

type fakeIncrementalRuntime struct {
	fakeRuntime
	streamCtx            context.Context
	streamReq            protocol.CreateResponseRequest
	streamResp           protocol.Response
	streamDeltas         []string
	streamFunctionDeltas []runtime.ResponseFunctionCallArgumentsDelta
	streamFunctionDone   []runtime.ResponseFunctionCallArgumentsDone
	streamOutputAdded    []runtime.ResponseOutputItemAdded
	streamOutputDone     []runtime.ResponseOutputItemDone
	streamErr            error
	streamErrAfterOutput error
}

func (f *fakeIncrementalRuntime) CreateStream(ctx context.Context, req protocol.CreateResponseRequest, sink runtime.ResponseStreamSink) (protocol.Response, error) {
	f.streamCtx = ctx
	f.streamReq = req
	if f.streamErr != nil {
		return protocol.Response{}, f.streamErr
	}
	created := f.streamResp
	created.Status = "in_progress"
	created.Output = nil
	if err := sink.ResponseCreated(created); err != nil {
		return protocol.Response{}, err
	}
	if progressSink, ok := sink.(runtime.ResponseInProgressStreamSink); ok {
		if err := progressSink.ResponseInProgress(created); err != nil {
			return protocol.Response{}, err
		}
	}
	if len(f.streamDeltas) > 0 && len(f.streamResp.Output) > 0 {
		item := f.streamResp.Output[0]
		part := protocol.ContentPart{Type: "output_text"}
		if itemSink, ok := sink.(runtime.OutputItemAddedStreamSink); ok {
			startedItem := item
			startedItem.Status = "in_progress"
			startedItem.Content = []protocol.ContentPart{part}
			if err := itemSink.OutputItemAdded(runtime.ResponseOutputItemAdded{OutputIndex: 0, Item: startedItem}); err != nil {
				return protocol.Response{}, err
			}
		}
		if contentSink, ok := sink.(runtime.ContentPartStreamSink); ok {
			if err := contentSink.ContentPartAdded(runtime.ResponseContentPartAdded{OutputIndex: 0, ItemID: item.ID, ContentIndex: 0, Part: part}); err != nil {
				return protocol.Response{}, err
			}
		}
	}
	for _, delta := range f.streamDeltas {
		if err := sink.OutputTextDelta(runtime.ResponseTextDelta{
			OutputIndex:  0,
			ItemID:       f.streamResp.Output[0].ID,
			ContentIndex: 0,
			Delta:        delta,
		}); err != nil {
			return protocol.Response{}, err
		}
	}
	if len(f.streamDeltas) > 0 && len(f.streamResp.Output) > 0 {
		item := f.streamResp.Output[0]
		text := ""
		if len(item.Content) > 0 {
			text = item.Content[0].Text
		}
		if textDoneSink, ok := sink.(runtime.OutputTextDoneStreamSink); ok {
			if err := textDoneSink.OutputTextDone(runtime.ResponseTextDone{OutputIndex: 0, ItemID: item.ID, ContentIndex: 0, Text: text}); err != nil {
				return protocol.Response{}, err
			}
		}
		if contentSink, ok := sink.(runtime.ContentPartStreamSink); ok {
			part := protocol.ContentPart{Type: "output_text", Text: text}
			if err := contentSink.ContentPartDone(runtime.ResponseContentPartDone{OutputIndex: 0, ItemID: item.ID, ContentIndex: 0, Part: part}); err != nil {
				return protocol.Response{}, err
			}
		}
		if itemSink, ok := sink.(runtime.OutputItemStreamSink); ok {
			if err := itemSink.OutputItemDone(runtime.ResponseOutputItemDone{OutputIndex: 0, Item: item}); err != nil {
				return protocol.Response{}, err
			}
		}
	}
	if functionSink, ok := sink.(runtime.FunctionCallArgumentStreamSink); ok {
		for _, delta := range f.streamFunctionDeltas {
			if err := functionSink.FunctionCallArgumentsDelta(delta); err != nil {
				return protocol.Response{}, err
			}
		}
		for _, done := range f.streamFunctionDone {
			if err := functionSink.FunctionCallArgumentsDone(done); err != nil {
				return protocol.Response{}, err
			}
		}
	}
	if outputSink, ok := sink.(runtime.OutputItemAddedStreamSink); ok {
		for _, added := range f.streamOutputAdded {
			if err := outputSink.OutputItemAdded(added); err != nil {
				return protocol.Response{}, err
			}
			if f.streamErrAfterOutput != nil {
				return protocol.Response{}, f.streamErrAfterOutput
			}
		}
	}
	if outputSink, ok := sink.(runtime.OutputItemStreamSink); ok {
		for _, done := range f.streamOutputDone {
			if err := outputSink.OutputItemDone(done); err != nil {
				return protocol.Response{}, err
			}
		}
	}
	if err := sink.ResponseCompleted(f.streamResp); err != nil {
		return protocol.Response{}, err
	}
	return f.streamResp, nil
}

func (f *fakeRuntime) Create(ctx context.Context, req protocol.CreateResponseRequest) (protocol.Response, error) {
	f.createCtx = ctx
	f.createReq = req
	return f.createResp, f.createErr
}

func (f *fakeRuntime) Compact(ctx context.Context, req protocol.CompactResponseRequest) (protocol.Response, error) {
	f.compactCtx = ctx
	f.compactReq = req
	return f.compactResp, f.compactErr
}

func (f *fakeRuntime) InputItems(ctx context.Context, id string) (protocol.InputItemList, bool, error) {
	f.inputItemsID = id
	return f.inputItemsResp, f.inputItemsFound, f.inputItemsErr
}

type fakeAuditor struct {
	acceptedEntry audit.RequestEntry
	acceptedCalls int
	completedID   string
	completed     audit.Completion
	rejectedID    string
	rejected      audit.Failure
	events        []audit.ExecutionEvent
}

func (f *fakeAuditor) Accepted(ctx context.Context, entry audit.RequestEntry) (string, error) {
	f.acceptedCalls++
	f.acceptedEntry = entry
	return "audit_1", nil
}

func (f *fakeAuditor) Completed(ctx context.Context, id string, result audit.Completion) error {
	f.completedID = id
	f.completed = result
	return nil
}

func (f *fakeAuditor) Rejected(ctx context.Context, id string, failure audit.Failure) error {
	f.rejectedID = id
	f.rejected = failure
	return nil
}

func (f *fakeAuditor) RecordExecutionEvent(ctx context.Context, event audit.ExecutionEvent) error {
	f.events = append(f.events, event)
	return nil
}

func TestCreateResponseSuccess(t *testing.T) {
	rt := &fakeRuntime{
		createResp: protocol.Response{
			ID:        "resp_1",
			Object:    "response",
			Status:    "completed",
			Model:     "gpt-test",
			CreatedAt: 123,
			Output: []protocol.OutputItem{{
				ID:      "msg_1",
				Type:    "message",
				Status:  "completed",
				Role:    "assistant",
				Content: []protocol.ContentPart{{Type: "output_text", Text: "done"}},
			}},
		},
	}
	body := strings.NewReader(`{"model":"gpt-test","input":"hello"}`)
	rec := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rt.createReq.Model != "gpt-test" || rt.createReq.Input != "hello" {
		t.Fatalf("create request mismatch: %#v", rt.createReq)
	}
	var got protocol.Response
	decodeBody(t, rec, &got)
	if got.ID != "resp_1" || got.Output[0].Content[0].Text != "done" {
		t.Fatalf("response mismatch: %#v", got)
	}
}

func TestCreateResponseAuditsAcceptedAndCompleted(t *testing.T) {
	rt := &fakeRuntime{
		createResp: protocol.Response{
			ID:     "resp_1",
			Object: "response",
			Status: "completed",
			Model:  "gpt-test",
			Metadata: map[string]any{
				"codex": map[string]any{"thread_id": "thread_1"},
			},
		},
	}
	auditor := &fakeAuditor{}
	body := `{"model":"gpt-test","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tracelab-test")
	req.Header.Set("X-Client-Request-Id", "client-1")
	req.Header.Set("X-Codex-Window-Id", "window-1")

	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if auditor.acceptedCalls != 1 {
		t.Fatalf("Accepted calls = %d, want 1", auditor.acceptedCalls)
	}
	entry := auditor.acceptedEntry
	if entry.Method != http.MethodPost || entry.Path != "/v1/responses" || entry.ClientRequestID != "client-1" {
		t.Fatalf("accepted entry mismatch: %#v", entry)
	}
	if entry.BodySha256 != audit.BodySHA256([]byte(body)) || entry.BodyPreview != body {
		t.Fatalf("body audit mismatch: %#v", entry)
	}
	if entry.HeaderJSON["content-type"] != "application/json" || entry.HeaderJSON["user-agent"] != "tracelab-test" || entry.HeaderJSON["x-client-request-id"] != "client-1" || entry.HeaderJSON["x-codex-window-id"] != "window-1" {
		t.Fatalf("header audit mismatch: %#v", entry.HeaderJSON)
	}
	if auditor.completedID != "audit_1" {
		t.Fatalf("completed id = %q, want audit_1", auditor.completedID)
	}
	if auditor.completed.ResponseID != "resp_1" || auditor.completed.ConversationID != "thread_1" {
		t.Fatalf("completed audit mismatch: %#v", auditor.completed)
	}
	if auditID, ok := audit.RequestAuditIDFromContext(rt.createCtx); !ok || auditID != "audit_1" {
		t.Fatalf("runtime context audit id = %q/%v, want audit_1/true", auditID, ok)
	}
	if auditor.rejectedID != "" {
		t.Fatalf("unexpected rejected audit: id=%q failure=%#v", auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 2 {
		t.Fatalf("execution events = %d, want 2: %#v", len(auditor.events), auditor.events)
	}
	if auditor.events[0].EventType != "response.request" || auditor.events[0].Phase != "request" || auditor.events[0].Status != "accepted" {
		t.Fatalf("accepted event mismatch: %#v", auditor.events[0])
	}
	if auditor.events[0].DetailsJSON["request_audit_id"] != "audit_1" {
		t.Fatalf("accepted event details mismatch: %#v", auditor.events[0].DetailsJSON)
	}
	if auditor.events[1].EventType != "response.request" || auditor.events[1].Status != "completed" || auditor.events[1].ResponseID != "resp_1" || auditor.events[1].ConversationID != "thread_1" {
		t.Fatalf("completed event mismatch: %#v", auditor.events[1])
	}
}

func TestCodexCompatInjectsAvailableHostedToolWhenToolsAbsent(t *testing.T) {
	rt := &fakeRuntime{
		createResp: protocol.Response{
			ID:     "resp_codex_compat",
			Object: "response",
			Status: "completed",
			Model:  "gpt-test",
		},
	}
	auditor := &fakeAuditor{}
	body := `{"model":"gpt-test","input":"Search today's AI news."}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()

	NewHandler(rt,
		WithRequestAuditor(auditor),
		WithExecutionEventRecorder(auditor),
		WithCodexCompat(CodexCompatOptions{
			Enabled: true,
			AvailableHostedTools: []protocol.Tool{{
				Type:          "web_search",
				MaxNumResults: 3,
			}},
		}),
	).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(rt.createReq.Tools) != 1 || rt.createReq.Tools[0].Type != "web_search" || rt.createReq.Tools[0].MaxNumResults != 3 {
		t.Fatalf("runtime tools = %#v, want injected web_search", rt.createReq.Tools)
	}
	if rt.createReq.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %#v, want auto", rt.createReq.ToolChoice)
	}
	var audited protocol.CreateResponseRequest
	if err := json.Unmarshal([]byte(auditor.acceptedEntry.BodyPreview), &audited); err != nil {
		t.Fatalf("decode audited normalized body: %v; body=%s", err, auditor.acceptedEntry.BodyPreview)
	}
	if len(audited.Tools) != 1 || audited.Tools[0].Type != "web_search" || audited.ToolChoice != "auto" {
		t.Fatalf("audited request = %#v, want normalized tools and tool_choice", audited)
	}
	if len(auditor.events) == 0 {
		t.Fatalf("missing execution events")
	}
	compat, ok := auditor.events[0].DetailsJSON["codex_compat"].(map[string]any)
	if !ok || compat["tool_choice_defaulted"] != true {
		t.Fatalf("codex compat event details = %#v, want injected detail", auditor.events[0].DetailsJSON)
	}
	types, ok := compat["injected_hosted_tools"].([]string)
	if !ok || len(types) != 1 || types[0] != "web_search" {
		t.Fatalf("injected_hosted_tools = %#v, want web_search", compat["injected_hosted_tools"])
	}
}

func TestCodexCompatDisabledDoesNotInjectHostedTool(t *testing.T) {
	rt := &fakeRuntime{
		createResp: protocol.Response{
			ID:     "resp_codex_compat_disabled",
			Object: "response",
			Status: "completed",
			Model:  "gpt-test",
		},
	}
	rec := httptest.NewRecorder()

	NewHandler(rt, WithCodexCompat(CodexCompatOptions{
		Enabled: true,
		AvailableHostedTools: []protocol.Tool{{
			Type: "mcp",
		}},
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hello"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(rt.createReq.Tools) != 0 || rt.createReq.ToolChoice != nil {
		t.Fatalf("runtime request = %#v, want no injected tools", rt.createReq)
	}

	rt = &fakeRuntime{
		createResp: protocol.Response{
			ID:     "resp_codex_compat_disabled",
			Object: "response",
			Status: "completed",
			Model:  "gpt-test",
		},
	}
	rec = httptest.NewRecorder()
	NewHandler(rt, WithCodexCompat(CodexCompatOptions{
		AvailableHostedTools: []protocol.Tool{{
			Type: "web_search",
		}},
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hello"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(rt.createReq.Tools) != 0 || rt.createReq.ToolChoice != nil {
		t.Fatalf("runtime request = %#v, want no injected tools when disabled", rt.createReq)
	}
}

func TestCodexCompatPreservesExistingClientTools(t *testing.T) {
	rt := &fakeRuntime{
		createResp: protocol.Response{
			ID:     "resp_codex_compat_client_tools",
			Object: "response",
			Status: "completed",
			Model:  "gpt-test",
		},
	}
	body := `{"model":"gpt-test","input":"hello","tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}}],"tool_choice":"auto"}`
	rec := httptest.NewRecorder()

	NewHandler(rt, WithCodexCompat(CodexCompatOptions{
		Enabled:             true,
		PreserveClientTools: true,
		AvailableHostedTools: []protocol.Tool{{
			Type: "web_search",
		}},
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(rt.createReq.Tools) != 1 || rt.createReq.Tools[0].Type != "function" || rt.createReq.Tools[0].Name != "read_file" {
		t.Fatalf("runtime tools = %#v, want preserved function tool", rt.createReq.Tools)
	}
	if rt.createReq.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %#v, want preserved auto", rt.createReq.ToolChoice)
	}
}

func TestCreateResponseBadJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertError(t, rec, "invalid_request_error", "invalid_json")
}

func TestCreateResponseBodyLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{}, WithMaxBodyBytes(4)).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello"}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertError(t, rec, "invalid_request_error", "invalid_json")
}

func TestCreateResponseUnsupportedHostedToolError(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{createErr: runtime.UnsupportedHostedToolError{Tool: "mcp", Reason: "hosted tool runtime is not implemented"}}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","tools":[{"type":"mcp"}],"tool_choice":{"type":"mcp"}}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertError(t, rec, "invalid_request_error", "unsupported_tool")
	if !strings.Contains(rec.Body.String(), `unsupported hosted tool`) || !strings.Contains(rec.Body.String(), `mcp`) {
		t.Fatalf("body = %s, want unsupported hosted tool message", rec.Body.String())
	}
}

func TestRuntimeErrorBodyUnsupportedHostedTool(t *testing.T) {
	body := runtimeErrorBody(runtime.UnsupportedHostedToolError{Tool: "file_search", Reason: "hosted tool runtime is not implemented"})
	if body.Type != "invalid_request_error" || body.Code != "unsupported_tool" || !strings.Contains(body.Message, `unsupported hosted tool "file_search"`) {
		t.Fatalf("runtimeErrorBody = %+v, want unsupported_tool invalid request", body)
	}
}

func TestCreateResponseStreamSuccess(t *testing.T) {
	rt := &fakeRuntime{
		createResp: protocol.Response{
			ID:        "resp_stream",
			Object:    "response",
			Status:    "completed",
			Model:     "gpt-test",
			CreatedAt: 123,
			Output: []protocol.OutputItem{
				{
					ID:      "msg_1",
					Type:    "message",
					Status:  "completed",
					Role:    "assistant",
					Content: []protocol.ContentPart{{Type: "output_text", Text: "done"}},
				},
				{
					ID:        "fc_call_1",
					Type:      "function_call",
					Status:    "completed",
					CallID:    "call_1",
					Name:      "lookup",
					Arguments: `{"q":"codex"}`,
				},
			},
		},
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	if !rt.createReq.Stream || rt.createReq.Input != "hello" {
		t.Fatalf("runtime Create request mismatch: %#v", rt.createReq)
	}
	body := rec.Body.String()
	for _, event := range []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	} {
		if !strings.Contains(body, "event: "+event+"\n") {
			t.Fatalf("stream body missing event %q:\n%s", event, body)
		}
	}
	if !strings.Contains(body, `"delta":"done"`) || !strings.Contains(body, `"arguments":"{\"q\":\"codex\"}"`) {
		t.Fatalf("stream body missing text/function payload:\n%s", body)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "audit_1" || auditor.rejectedID != "" {
		t.Fatalf("stream audit mismatch: accepted=%d completed=%q rejected=%q/%#v", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 4 {
		t.Fatalf("execution events = %d, want accepted/stream started/stream completed/request completed: %#v", len(auditor.events), auditor.events)
	}
	if auditor.events[1].EventType != "response.stream" || auditor.events[1].Status != "started" {
		t.Fatalf("stream started event mismatch: %#v", auditor.events[1])
	}
	if auditor.events[1].DetailsJSON["mode"] != "deferred" {
		t.Fatalf("deferred stream started details = %#v, want mode=deferred", auditor.events[1].DetailsJSON)
	}
	if auditor.events[2].EventType != "response.stream" || auditor.events[2].Status != "completed" {
		t.Fatalf("stream completed event mismatch: %#v", auditor.events[2])
	}
	if auditor.events[2].DetailsJSON["mode"] != "deferred" {
		t.Fatalf("deferred stream completed details = %#v, want mode=deferred", auditor.events[2].DetailsJSON)
	}
	if auditor.events[3].EventType != "response.request" || auditor.events[3].Status != "completed" || auditor.events[3].ResponseID != "resp_stream" {
		t.Fatalf("request completed event mismatch: %#v", auditor.events[3])
	}
}

func TestCreateResponseStreamFallbackRecordsUnsupportedIncrementalEvent(t *testing.T) {
	rt := &fakeIncrementalRuntime{
		fakeRuntime: fakeRuntime{
			createResp: protocol.Response{
				ID:        "resp_deferred",
				Object:    "response",
				Status:    "completed",
				Model:     "gpt-test",
				CreatedAt: 123,
				Output: []protocol.OutputItem{{
					ID:      "msg_1",
					Type:    "message",
					Status:  "completed",
					Role:    "assistant",
					Content: []protocol.ContentPart{{Type: "output_text", Text: "deferred"}},
				}},
			},
		},
		streamErr: runtime.ErrIncrementalStreamUnsupported,
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true,"tools":[{"type":"web_search"}]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !rt.streamReq.Stream || rt.createReq.Input != "hello" || !rt.createReq.Stream {
		t.Fatalf("runtime fallback requests mismatch: stream=%#v create=%#v", rt.streamReq, rt.createReq)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "audit_1" || auditor.rejectedID != "" {
		t.Fatalf("stream fallback audit mismatch: accepted=%d completed=%q rejected=%q/%#v", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 5 {
		t.Fatalf("execution events = %d, want accepted/fallback/stream started/stream completed/request completed: %#v", len(auditor.events), auditor.events)
	}
	fallback := auditor.events[1]
	if fallback.EventType != "response.stream" || fallback.Status != "fallback" || fallback.Message != runtime.ErrIncrementalStreamUnsupported.Error() {
		t.Fatalf("fallback event mismatch: %#v", fallback)
	}
	if fallback.DetailsJSON["from_mode"] != "incremental" || fallback.DetailsJSON["to_mode"] != "deferred" || fallback.DetailsJSON["reason"] != runtime.ErrIncrementalStreamUnsupported.Error() {
		t.Fatalf("fallback event details = %#v", fallback.DetailsJSON)
	}
	if auditor.events[2].EventType != "response.stream" || auditor.events[2].Status != "started" || auditor.events[2].DetailsJSON["mode"] != "deferred" {
		t.Fatalf("deferred started event mismatch: %#v", auditor.events[2])
	}
	if auditor.events[3].EventType != "response.stream" || auditor.events[3].Status != "completed" || auditor.events[3].DetailsJSON["mode"] != "deferred" {
		t.Fatalf("deferred completed event mismatch: %#v", auditor.events[3])
	}
	if auditor.events[4].EventType != "response.request" || auditor.events[4].Status != "completed" || auditor.events[4].ResponseID != "resp_deferred" {
		t.Fatalf("request completed event mismatch: %#v", auditor.events[4])
	}
}

func TestCreateResponseStreamFallbackPreservesWrappedUnsupportedReason(t *testing.T) {
	wrappedErr := fmt.Errorf("%w: auto compact tool combination requires deferred stream", runtime.ErrIncrementalStreamUnsupported)
	rt := &fakeIncrementalRuntime{
		fakeRuntime: fakeRuntime{
			createResp: protocol.Response{
				ID:        "resp_deferred_wrapped",
				Object:    "response",
				Status:    "completed",
				Model:     "gpt-test",
				CreatedAt: 123,
				Output: []protocol.OutputItem{{
					ID:      "msg_1",
					Type:    "message",
					Status:  "completed",
					Role:    "assistant",
					Content: []protocol.ContentPart{{Type: "output_text", Text: "deferred wrapped"}},
				}},
			},
		},
		streamErr: wrappedErr,
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true,"tools":[{"type":"web_search"}]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !rt.streamReq.Stream || rt.createReq.Input != "hello" || !rt.createReq.Stream {
		t.Fatalf("runtime fallback requests mismatch: stream=%#v create=%#v", rt.streamReq, rt.createReq)
	}
	if auditor.rejectedID != "" || auditor.completedID != "audit_1" {
		t.Fatalf("stream fallback audit mismatch: completed=%q rejected=%q/%#v", auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 5 {
		t.Fatalf("execution events = %d, want accepted/fallback/stream started/stream completed/request completed: %#v", len(auditor.events), auditor.events)
	}
	fallback := auditor.events[1]
	if fallback.EventType != "response.stream" || fallback.Status != "fallback" {
		t.Fatalf("fallback event mismatch: %#v", fallback)
	}
	reason, _ := fallback.DetailsJSON["reason"].(string)
	if !strings.Contains(reason, runtime.ErrIncrementalStreamUnsupported.Error()) || !strings.Contains(reason, "auto compact tool combination requires deferred stream") {
		t.Fatalf("fallback reason = %q, want wrapped unsupported stream reason", reason)
	}
	if auditor.events[4].EventType != "response.request" || auditor.events[4].Status != "completed" || auditor.events[4].ResponseID != "resp_deferred_wrapped" {
		t.Fatalf("request completed event mismatch: %#v", auditor.events[4])
	}
}

func TestCreateResponseStreamUsesIncrementalRuntimeDeltas(t *testing.T) {
	rt := &fakeIncrementalRuntime{
		streamResp: protocol.Response{
			ID:        "resp_stream",
			Object:    "response",
			Status:    "completed",
			Model:     "gpt-test",
			CreatedAt: 123,
			Output: []protocol.OutputItem{{
				ID:      "msg_1",
				Type:    "message",
				Status:  "completed",
				Role:    "assistant",
				Content: []protocol.ContentPart{{Type: "output_text", Text: "Hello world"}},
			}},
		},
		streamDeltas: []string{"Hello ", "world"},
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !rt.streamReq.Stream || rt.streamReq.Input != "hello" {
		t.Fatalf("runtime CreateStream request mismatch: %#v", rt.streamReq)
	}
	if rt.createReq.Stream {
		t.Fatalf("fallback Create unexpectedly called: %#v", rt.createReq)
	}
	body := rec.Body.String()
	for _, event := range []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	} {
		if !strings.Contains(body, "event: "+event+"\n") {
			t.Fatalf("incremental stream missing event %q:\n%s", event, body)
		}
	}
	if !strings.Contains(body, `"delta":"Hello "`) || !strings.Contains(body, `"delta":"world"`) {
		t.Fatalf("incremental stream missing separate deltas:\n%s", body)
	}
	if !strings.Contains(body, `"text":"Hello world"`) || !strings.Contains(body, `"status":"in_progress"`) {
		t.Fatalf("incremental stream missing text lifecycle payloads:\n%s", body)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "audit_1" || auditor.rejectedID != "" {
		t.Fatalf("stream audit mismatch: accepted=%d completed=%q rejected=%q/%#v", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 4 {
		t.Fatalf("execution events = %d, want accepted/stream started/stream completed/request completed: %#v", len(auditor.events), auditor.events)
	}
	if auditor.events[1].EventType != "response.stream" || auditor.events[1].Status != "started" {
		t.Fatalf("stream started event mismatch: %#v", auditor.events[1])
	}
	if auditor.events[2].EventType != "response.stream" || auditor.events[2].Status != "completed" {
		t.Fatalf("stream completed event mismatch: %#v", auditor.events[2])
	}
	if auditor.events[3].EventType != "response.request" || auditor.events[3].Status != "completed" || auditor.events[3].ResponseID != "resp_stream" {
		t.Fatalf("request completed event mismatch: %#v", auditor.events[3])
	}
}

func TestCreateResponseStreamUsesIncrementalFunctionCallArgumentDeltas(t *testing.T) {
	rt := &fakeIncrementalRuntime{
		streamResp: protocol.Response{
			ID:        "resp_stream_tool",
			Object:    "response",
			Status:    "completed",
			Model:     "gpt-test",
			CreatedAt: 123,
			Output: []protocol.OutputItem{{
				ID:        "fc_call_lookup",
				Type:      "function_call",
				Status:    "completed",
				CallID:    "call_lookup",
				Name:      "lookup",
				Arguments: `{"q":"codex"}`,
			}},
		},
		streamFunctionDeltas: []runtime.ResponseFunctionCallArgumentsDelta{
			{OutputIndex: 0, ItemID: "fc_call_lookup", CallID: "call_lookup", Delta: `{"q"`, Arguments: `{"q"`},
			{OutputIndex: 0, ItemID: "fc_call_lookup", CallID: "call_lookup", Delta: `:"codex"}`, Arguments: `{"q":"codex"}`},
		},
		streamFunctionDone: []runtime.ResponseFunctionCallArgumentsDone{
			{OutputIndex: 0, ItemID: "fc_call_lookup", CallID: "call_lookup", Arguments: `{"q":"codex"}`},
		},
		streamOutputAdded: []runtime.ResponseOutputItemAdded{{
			OutputIndex: 0,
			Item: protocol.OutputItem{
				ID:     "fco_call_lookup",
				Type:   "function_call_output",
				Status: "in_progress",
				CallID: "call_lookup",
				Name:   "lookup",
			},
		}},
		streamOutputDone: []runtime.ResponseOutputItemDone{{
			OutputIndex: 0,
			Item: protocol.OutputItem{
				ID:     "fco_call_lookup",
				Type:   "function_call_output",
				Status: "completed",
				CallID: "call_lookup",
				Name:   "lookup",
				Output: map[string]any{"ok": true},
			},
		}},
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, event := range []string{"response.created", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.added", "response.output_item.done", "response.completed"} {
		if !strings.Contains(body, "event: "+event+"\n") {
			t.Fatalf("incremental function stream missing event %q:\n%s", event, body)
		}
	}
	if !strings.Contains(body, `"delta":"{\"q\""`) || !strings.Contains(body, `"delta":":\"codex\"}"`) || !strings.Contains(body, `"arguments":"{\"q\":\"codex\"}"`) || !strings.Contains(body, `"type":"function_call_output"`) || !strings.Contains(body, `"status":"in_progress"`) {
		t.Fatalf("incremental function stream missing argument payloads:\n%s", body)
	}
	doneIndex := strings.Index(body, "event: response.function_call_arguments.done\n")
	itemAddedIndex := strings.Index(body, "event: response.output_item.added\n")
	itemDoneIndex := strings.Index(body, "event: response.output_item.done\n")
	completedIndex := strings.Index(body, "event: response.completed\n")
	if doneIndex < 0 || itemAddedIndex < 0 || itemDoneIndex < 0 || completedIndex < 0 || doneIndex >= itemAddedIndex || itemAddedIndex >= itemDoneIndex || itemDoneIndex >= completedIndex {
		t.Fatalf("incremental function stream event order mismatch:\n%s", body)
	}
	if rt.createReq.Stream {
		t.Fatalf("fallback Create unexpectedly called: %#v", rt.createReq)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "audit_1" || auditor.rejectedID != "" {
		t.Fatalf("stream audit mismatch: accepted=%d completed=%q rejected=%q/%#v", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
}

func TestCreateResponseIncrementalStreamWritesFailedEventAfterOutput(t *testing.T) {
	rt := &fakeIncrementalRuntime{
		fakeRuntime: fakeRuntime{
			createResp: protocol.Response{
				ID:        "resp_deferred_unexpected",
				Object:    "response",
				Status:    "completed",
				Model:     "gpt-test",
				CreatedAt: 123,
			},
		},
		streamResp: protocol.Response{
			ID:        "resp_stream_tool_failed",
			Object:    "response",
			Status:    "completed",
			Model:     "gpt-test",
			CreatedAt: 123,
			Output: []protocol.OutputItem{{
				ID:        "fc_call_lookup",
				Type:      "function_call",
				Status:    "completed",
				CallID:    "call_lookup",
				Name:      "lookup",
				Arguments: `{"q":"codex"}`,
			}},
		},
		streamOutputAdded: []runtime.ResponseOutputItemAdded{{
			OutputIndex: 0,
			Item: protocol.OutputItem{
				ID:     "fco_call_lookup",
				Type:   "function_call_output",
				Status: "in_progress",
				CallID: "call_lookup",
				Name:   "lookup",
			},
		}},
		streamErrAfterOutput: errors.New("executor failed after stream write"),
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rt.createReq.Stream {
		t.Fatalf("fallback Create unexpectedly called: %#v", rt.createReq)
	}
	body := rec.Body.String()
	for _, event := range []string{"response.created", "response.output_item.added", "response.failed"} {
		if !strings.Contains(body, "event: "+event+"\n") {
			t.Fatalf("incremental failed stream missing event %q:\n%s", event, body)
		}
	}
	if strings.Contains(body, "event: response.completed\n") {
		t.Fatalf("incremental failed stream unexpectedly completed:\n%s", body)
	}
	if !strings.Contains(body, `"message":"executor failed after stream write"`) || !strings.Contains(body, `"code":"server_error"`) {
		t.Fatalf("incremental failed stream missing error payload:\n%s", body)
	}
	addedIndex := strings.Index(body, "event: response.output_item.added\n")
	failedIndex := strings.Index(body, "event: response.failed\n")
	if addedIndex < 0 || failedIndex < 0 || addedIndex >= failedIndex {
		t.Fatalf("incremental failed stream event order mismatch:\n%s", body)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "" || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "failed" || auditor.rejected.ErrorText != "executor failed after stream write" {
		t.Fatalf("stream failed audit mismatch: accepted=%d completed=%q rejected=%q/%#v", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 3 {
		t.Fatalf("execution events = %d, want accepted/stream started/stream failed: %#v", len(auditor.events), auditor.events)
	}
	if auditor.events[1].EventType != "response.stream" || auditor.events[1].Status != "started" {
		t.Fatalf("stream started event mismatch: %#v", auditor.events[1])
	}
	if auditor.events[2].EventType != "response.stream" || auditor.events[2].Status != "failed" || auditor.events[2].Message != "executor failed after stream write" {
		t.Fatalf("stream failed event mismatch: %#v", auditor.events[2])
	}
}

func TestCreateResponseIncrementalStreamAuditsCancelledAfterOutput(t *testing.T) {
	rt := &fakeIncrementalRuntime{
		streamResp: protocol.Response{
			ID:        "resp_stream_cancelled",
			Object:    "response",
			Status:    "completed",
			Model:     "gpt-test",
			CreatedAt: 123,
			Output: []protocol.OutputItem{{
				ID:        "fc_call_lookup",
				Type:      "function_call",
				Status:    "completed",
				CallID:    "call_lookup",
				Name:      "lookup",
				Arguments: `{"q":"codex"}`,
			}},
		},
		streamOutputAdded: []runtime.ResponseOutputItemAdded{{
			OutputIndex: 0,
			Item: protocol.OutputItem{
				ID:     "fco_call_lookup",
				Type:   "function_call_output",
				Status: "in_progress",
				CallID: "call_lookup",
				Name:   "lookup",
			},
		}},
		streamErrAfterOutput: context.Canceled,
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d after partial stream write; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, event := range []string{"response.created", "response.output_item.added", "response.failed"} {
		if !strings.Contains(body, "event: "+event+"\n") {
			t.Fatalf("incremental cancelled stream missing event %q:\n%s", event, body)
		}
	}
	if strings.Contains(body, "event: response.completed\n") {
		t.Fatalf("incremental cancelled stream unexpectedly completed:\n%s", body)
	}
	if !strings.Contains(body, `"message":"request cancelled"`) || !strings.Contains(body, `"code":"cancelled"`) {
		t.Fatalf("incremental cancelled stream missing cancelled payload:\n%s", body)
	}
	addedIndex := strings.Index(body, "event: response.output_item.added\n")
	failedIndex := strings.Index(body, "event: response.failed\n")
	if addedIndex < 0 || failedIndex < 0 || addedIndex >= failedIndex {
		t.Fatalf("incremental cancelled stream event order mismatch:\n%s", body)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "" || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "cancelled" || auditor.rejected.ErrorText != context.Canceled.Error() {
		t.Fatalf("stream cancelled audit mismatch: accepted=%d completed=%q rejected=%q/%#v", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 3 {
		t.Fatalf("execution events = %d, want accepted/stream started/stream cancelled: %#v", len(auditor.events), auditor.events)
	}
	if auditor.events[2].EventType != "response.stream" || auditor.events[2].Status != "cancelled" || auditor.events[2].Message != context.Canceled.Error() || auditor.events[2].DetailsJSON["stream"] != true {
		t.Fatalf("stream cancelled event mismatch: %#v", auditor.events[2])
	}
}

func TestCreateResponseRuntimeError(t *testing.T) {
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{createErr: errors.New("upstream failed")}, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if auditor.acceptedCalls != 1 || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "failed" || auditor.rejected.ErrorText != "upstream failed" {
		t.Fatalf("failed audit mismatch: calls=%d id=%q failure=%#v", auditor.acceptedCalls, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 2 || auditor.events[1].Status != "failed" || auditor.events[1].Message != "upstream failed" {
		t.Fatalf("failed events mismatch: %#v", auditor.events)
	}
	assertError(t, rec, "server_error", "server_error")
}

func TestCreateResponseRuntimeContextCanceledAuditsCancelled(t *testing.T) {
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{createErr: context.Canceled}, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello"}`)))

	if rec.Code != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, statusClientClosedRequest, rec.Body.String())
	}
	if auditor.acceptedCalls != 1 || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "cancelled" {
		t.Fatalf("cancelled audit mismatch: calls=%d id=%q failure=%#v", auditor.acceptedCalls, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 2 || auditor.events[1].Status != "cancelled" || auditor.events[1].Message != context.Canceled.Error() {
		t.Fatalf("cancelled events mismatch: %#v", auditor.events)
	}
	assertError(t, rec, "server_error", "cancelled")
}

func TestCreateResponseStreamRuntimeContextCanceledAuditsCancelled(t *testing.T) {
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{createErr: context.Canceled}, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, statusClientClosedRequest, rec.Body.String())
	}
	if auditor.acceptedCalls != 1 || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "cancelled" {
		t.Fatalf("stream cancelled audit mismatch: calls=%d id=%q failure=%#v", auditor.acceptedCalls, auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 2 || auditor.events[1].Status != "cancelled" || auditor.events[1].DetailsJSON["stream"] != true {
		t.Fatalf("stream cancelled events mismatch: %#v", auditor.events)
	}
	assertError(t, rec, "server_error", "cancelled")
}

func TestCompactResponseSuccessAuditsLifecycle(t *testing.T) {
	rt := &fakeRuntime{
		compactResp: protocol.Response{
			ID:                 "resp_compact",
			Object:             "response",
			Status:             "completed",
			Model:              "gpt-test",
			PreviousResponseID: "resp_source",
			Metadata: map[string]any{
				"codex": map[string]any{"thread_id": "thread_1"},
			},
			Output: []protocol.OutputItem{{
				ID:      "summary_1",
				Type:    "summary",
				Status:  "completed",
				Content: []protocol.ContentPart{{Type: "summary_text", Text: "summary"}},
			}},
		},
	}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"response_id":"resp_source","model":"gpt-test"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rt.compactReq.ResponseID != "resp_source" || rt.compactReq.Model != "gpt-test" {
		t.Fatalf("compact request = %#v, want source/model", rt.compactReq)
	}
	if auditID, ok := audit.RequestAuditIDFromContext(rt.compactCtx); !ok || auditID != "audit_1" {
		t.Fatalf("compact context audit id = %q/%v, want audit_1/true", auditID, ok)
	}
	if auditor.acceptedCalls != 1 || auditor.completedID != "audit_1" || auditor.rejectedID != "" {
		t.Fatalf("compact audit mismatch: accepted=%d completed=%q rejected=%q", auditor.acceptedCalls, auditor.completedID, auditor.rejectedID)
	}
	if len(auditor.events) != 2 {
		t.Fatalf("compact events = %d, want started/completed: %#v", len(auditor.events), auditor.events)
	}
	if auditor.events[0].EventType != "response.compact" || auditor.events[0].Status != "started" || auditor.events[0].DetailsJSON["target_response_id"] != "resp_source" {
		t.Fatalf("compact started event mismatch: %#v", auditor.events[0])
	}
	if auditor.events[1].EventType != "response.compact" || auditor.events[1].Status != "completed" || auditor.events[1].ResponseID != "resp_compact" || auditor.events[1].ConversationID != "thread_1" {
		t.Fatalf("compact completed event mismatch: %#v", auditor.events[1])
	}
	var got protocol.Response
	decodeBody(t, rec, &got)
	if got.ID != "resp_compact" || got.Output[0].Type != "summary" {
		t.Fatalf("compact response = %#v, want summary response", got)
	}
}

func TestCompactResponseRuntimeErrorAuditsFailure(t *testing.T) {
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{compactErr: errors.New("compact failed")}, WithRequestAuditor(auditor), WithExecutionEventRecorder(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"response_id":"resp_source"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if auditor.rejectedID != "audit_1" || auditor.rejected.Status != "failed" {
		t.Fatalf("compact rejected audit = %q/%#v, want failed", auditor.rejectedID, auditor.rejected)
	}
	if len(auditor.events) != 2 || auditor.events[1].EventType != "response.compact" || auditor.events[1].Status != "failed" {
		t.Fatalf("compact failed events = %#v", auditor.events)
	}
}

func TestInputItemsSuccess(t *testing.T) {
	rt := &fakeRuntime{
		inputItemsFound: true,
		inputItemsResp: protocol.InputItemList{
			Object: "list",
			Data: []protocol.InputItem{{
				ID:      "item_1",
				Type:    "message",
				Role:    "user",
				Content: []protocol.ContentPart{{Type: "input_text", Text: "hello"}},
			}},
			FirstID: "item_1",
			LastID:  "item_1",
		},
	}
	rec := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses/resp_1/input_items", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rt.inputItemsID != "resp_1" {
		t.Fatalf("input items id = %q, want resp_1", rt.inputItemsID)
	}
	var got protocol.InputItemList
	decodeBody(t, rec, &got)
	if got.Object != "list" || len(got.Data) != 1 || got.Data[0].ID != "item_1" {
		t.Fatalf("input item list mismatch: %#v", got)
	}
}

func TestInputItemsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses/missing/input_items", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	assertError(t, rec, "invalid_request_error", "not_found")
}

func TestInputItemsRuntimeNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{inputItemsErr: runtime.ResponseNotFoundError{ID: "missing"}}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses/missing/input_items", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	assertError(t, rec, "invalid_request_error", "not_found")
}

func TestMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", got, http.MethodPost)
	}
	assertError(t, rec, "invalid_request_error", "method_not_allowed")
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	if err := decoder.Decode(dst); err != nil {
		t.Fatalf("decode response body: %v; body=%s", err, rec.Body.String())
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, wantType, wantCode string) {
	t.Helper()
	var got protocol.ErrorResponse
	decodeBody(t, rec, &got)
	if got.Error.Type != wantType || got.Error.Code != wantCode || got.Error.Message == "" {
		t.Fatalf("error mismatch: %#v", got.Error)
	}
}
