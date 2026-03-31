package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestListAPIHandlerReturnsPagedItems(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"messages":[{"role":"user","content":"Inspect this request."}],"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file.","parameters":{"type":"object","properties":{"file_path":{"type":"string"}}}}}]}`
	content := buildRecordFixture(t, "/v1/chat/completions", false, reqBody, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/traces?page=1&page_size=50", nil)
	rr := httptest.NewRecorder()
	listAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload listResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].ID == "" {
		t.Fatalf("trace id missing from list payload")
	}
	if payload.Items[0].Operation != "chat.completions" {
		t.Fatalf("operation = %q, want chat.completions", payload.Items[0].Operation)
	}
}

func TestTraceDetailAPIHandlerReturnsConversationData(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"messages":[{"role":"system","content":"You are a personal assistant."},{"role":"user","content":"Inspect this request."}],"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file.","parameters":{"type":"object","properties":{"file_path":{"type":"string"}}}}}]}`
	content := buildRecordFixture(t, "/v1/chat/completions", false, reqBody, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Messages) == 0 {
		t.Fatalf("messages missing from detail payload")
	}
	if payload.Messages[0].Content != "You are a personal assistant." {
		t.Fatalf("unexpected first message content: %q", payload.Messages[0].Content)
	}
}

func TestTraceRawAPIHandlerReturnsProtocolAndMeta(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	content := buildRecordFixture(t, "/v1/chat/completions", true, `{"messages":[{"role":"user","content":"hello"}]}`, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n")
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID+"/raw", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload rawDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(payload.RequestProtocol, "POST /v1/chat/completions HTTP/1.1") {
		t.Fatalf("request protocol = %q", payload.RequestProtocol)
	}
	if !strings.Contains(payload.ResponseProtocol, "HTTP/1.1 200 OK") {
		t.Fatalf("response protocol = %q", payload.ResponseProtocol)
	}
	if payload.Header.Version != "LLM_PROXY_V3" {
		t.Fatalf("header version = %q, want LLM_PROXY_V3", payload.Header.Version)
	}
	if len(payload.Events) == 0 {
		t.Fatalf("events missing from payload")
	}
}

func TestTraceRawAPIHandlerReturnsEventAttributes(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	header := buildRecordHeader("/v1/responses", true, `{"input":"hello"}`, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n")
	events := append(recordfile.BuildEvents(header), recordfile.RecordEvent{
		Type: "llm.usage",
		Attributes: map[string]interface{}{
			"total_tokens": 18,
		},
	})
	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	payload := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n" +
		`{"input":"hello"}` + "\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" +
		`data: {"type":"response.output_text.delta","delta":"hi"}` + "\n"
	if err := os.WriteFile(tracePath, append(prelude, []byte(payload)...), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID+"/raw", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payloadResp rawDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payloadResp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	found := false
	for _, event := range payloadResp.Events {
		if event["type"] == "llm.usage" {
			attrs, ok := event["attributes"].(map[string]interface{})
			if !ok {
				t.Fatalf("attributes missing on usage event: %+v", event)
			}
			if attrs["total_tokens"] != float64(18) {
				t.Fatalf("total_tokens = %#v, want 18", attrs["total_tokens"])
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("llm.usage event not found in payload: %+v", payloadResp.Events)
	}
}

func TestTraceDetailAPIHandlerFiltersOutputTextDeltaEvents(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	header := buildRecordHeader("/v1/responses", true, `{"input":"hello"}`, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n")
	events := append(recordfile.BuildEvents(header),
		recordfile.RecordEvent{Type: "llm.output_text.delta", Message: "hi"},
		recordfile.RecordEvent{
			Type: "llm.usage",
			Attributes: map[string]interface{}{
				"total_tokens": 18,
			},
		},
	)
	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	payload := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n" +
		`{"input":"hello"}` + "\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" +
		`data: {"type":"response.output_text.delta","delta":"hi"}` + "\n"
	if err := os.WriteFile(tracePath, append(prelude, []byte(payload)...), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payloadResp detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payloadResp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, event := range payloadResp.Events {
		if event["type"] == "llm.output_text.delta" {
			t.Fatalf("delta event should be filtered from detail timeline: %+v", event)
		}
	}
}

func TestTraceDownloadAPIHandlerSetsAttachmentDisposition(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	content := buildRecordFixture(t, "/v1/chat/completions", false, `{"messages":[{"role":"user","content":"hello"}]}`, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID+"/download", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename="trace.http"` {
		t.Fatalf("Content-Disposition = %q, want attachment download", got)
	}
}

func TestTraceAPIHandlerReturnsNotFoundForUnknownID(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/traces/missing/raw", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func buildRecordHeader(url string, isStream bool, reqBody string, resBody string) recordfile.RecordHeader {
	reqHeader := "POST " + url + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	resHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	if isStream {
		resHeader = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
	}
	return recordfile.RecordHeader{
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
}
