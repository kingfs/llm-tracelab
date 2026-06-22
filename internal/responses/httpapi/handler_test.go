package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	inputItemsID    string
	inputItemsResp  protocol.InputItemList
	inputItemsFound bool
	inputItemsErr   error
}

func (f *fakeRuntime) Create(ctx context.Context, req protocol.CreateResponseRequest) (protocol.Response, error) {
	f.createCtx = ctx
	f.createReq = req
	return f.createResp, f.createErr
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
	NewHandler(rt, WithRequestAuditor(auditor)).ServeHTTP(rec, req)

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

func TestCreateResponseStreamUnsupported(t *testing.T) {
	rt := &fakeRuntime{}
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(rt, WithRequestAuditor(auditor)).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if rt.createReq.Input != nil {
		t.Fatalf("runtime Create called for unsupported stream request: %#v", rt.createReq)
	}
	if auditor.acceptedCalls != 1 || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "rejected" || auditor.rejected.ErrorText == "" {
		t.Fatalf("rejected audit mismatch: calls=%d id=%q failure=%#v", auditor.acceptedCalls, auditor.rejectedID, auditor.rejected)
	}
	assertError(t, rec, "invalid_request_error", "unsupported_stream")
}

func TestCreateResponseRuntimeError(t *testing.T) {
	auditor := &fakeAuditor{}
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{createErr: errors.New("upstream failed")}, WithRequestAuditor(auditor)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if auditor.acceptedCalls != 1 || auditor.rejectedID != "audit_1" || auditor.rejected.Status != "failed" || auditor.rejected.ErrorText != "upstream failed" {
		t.Fatalf("failed audit mismatch: calls=%d id=%q failure=%#v", auditor.acceptedCalls, auditor.rejectedID, auditor.rejected)
	}
	assertError(t, rec, "server_error", "server_error")
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
