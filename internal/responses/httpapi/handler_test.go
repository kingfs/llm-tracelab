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

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

type fakeRuntime struct {
	createReq       protocol.CreateResponseRequest
	createResp      protocol.Response
	createErr       error
	inputItemsID    string
	inputItemsResp  protocol.InputItemList
	inputItemsFound bool
	inputItemsErr   error
}

func (f *fakeRuntime) Create(ctx context.Context, req protocol.CreateResponseRequest) (protocol.Response, error) {
	f.createReq = req
	return f.createResp, f.createErr
}

func (f *fakeRuntime) InputItems(ctx context.Context, id string) (protocol.InputItemList, bool, error) {
	f.inputItemsID = id
	return f.inputItemsResp, f.inputItemsFound, f.inputItemsErr
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

func TestCreateResponseRuntimeError(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeRuntime{createErr: errors.New("upstream failed")}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
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
