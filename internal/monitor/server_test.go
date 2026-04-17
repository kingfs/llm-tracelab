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

func TestSessionListAPIHandlerReturnsGroupedItems(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	traceA := filepath.Join(outputDir, "trace-a.http")
	traceB := filepath.Join(outputDir, "trace-b.http")
	sessionID := "019d9659-34ca-7b03-917e-9f6bb8bc550b"
	contentA := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: " + sessionID},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	contentB := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		true,
		[]string{`X-Codex-Turn-Metadata: {"session_id":"` + sessionID + `"}`},
		`{"input":"hello again"}`,
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n",
	)
	if err := os.WriteFile(traceA, contentA, 0o644); err != nil {
		t.Fatalf("WriteFile(traceA) error = %v", err)
	}
	if err := os.WriteFile(traceB, contentB, 0o644); err != nil {
		t.Fatalf("WriteFile(traceB) error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?page=1&page_size=50", nil)
	rr := httptest.NewRecorder()
	sessionListAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].SessionID != sessionID {
		t.Fatalf("SessionID = %q, want %q", payload.Items[0].SessionID, sessionID)
	}
	if payload.Items[0].RequestCount != 2 {
		t.Fatalf("RequestCount = %d, want 2", payload.Items[0].RequestCount)
	}
}

func TestSessionListAPIHandlerAppliesFilters(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	traceA := filepath.Join(outputDir, "trace-a.http")
	traceB := filepath.Join(outputDir, "trace-b.http")
	contentA := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: sess-openai"},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	contentB := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/chat/completions",
		false,
		nil,
		`{"model":"gemini-pro","messages":[{"role":"user","content":"hello"}]}`,
		`{"choices":[{"message":{"content":"done"}}]}`,
	)
	if err := os.WriteFile(traceA, contentA, 0o644); err != nil {
		t.Fatalf("WriteFile(traceA) error = %v", err)
	}
	if err := os.WriteFile(traceB, contentB, 0o644); err != nil {
		t.Fatalf("WriteFile(traceB) error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=openai_compatible&q=sess-openai", nil)
	rr := httptest.NewRecorder()
	sessionListAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].SessionID != "sess-openai" {
		t.Fatalf("SessionID = %q, want sess-openai", payload.Items[0].SessionID)
	}
}

func TestSessionDetailAPIHandlerReturnsTraceList(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	sessionID := "sess-window"
	content := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"X-Codex-Window-Id: " + sessionID + ":0"},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	rr := httptest.NewRecorder()
	sessionDetailAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Summary.SessionID != sessionID {
		t.Fatalf("summary session id = %q, want %q", payload.Summary.SessionID, sessionID)
	}
	if payload.Summary.SessionSource != "header.x_codex_window_id" {
		t.Fatalf("summary session source = %q", payload.Summary.SessionSource)
	}
	if len(payload.Traces) != 1 {
		t.Fatalf("len(payload.Traces) = %d, want 1", len(payload.Traces))
	}
	if payload.Traces[0].SessionID != sessionID {
		t.Fatalf("trace session id = %q, want %q", payload.Traces[0].SessionID, sessionID)
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

func TestTraceDetailAPIHandlerReturnsSessionContext(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	sessionID := "sess-trace-detail"
	content := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: " + sessionID},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
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
	if payload.Session == nil {
		t.Fatalf("session missing from detail payload")
	}
	if payload.Session.SessionID != sessionID {
		t.Fatalf("SessionID = %q, want %q", payload.Session.SessionID, sessionID)
	}
	if payload.Session.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", payload.Session.SessionSource)
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

func TestTraceDetailAPIHandlerFiltersNoisyDeltaEvents(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	header := buildRecordHeader("/v1/responses", true, `{"input":"hello"}`, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n")
	events := append(recordfile.BuildEvents(header),
		recordfile.RecordEvent{Type: "llm.output_text.delta", Message: "hi"},
		recordfile.RecordEvent{Type: "llm.reasoning.delta", Message: "think"},
		recordfile.RecordEvent{
			Type: "llm.tool_call.delta",
			Attributes: map[string]interface{}{
				"id":   "call_1",
				"name": "search",
			},
		},
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
		if strings.HasSuffix(event["type"].(string), ".delta") {
			t.Fatalf("noisy delta event should be filtered from detail timeline: %+v", event)
		}
	}
}

func TestTraceDetailAPIHandlerEnrichesRequestAndResponseTimeline(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
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

	var (
		requestMessage  string
		responseMessage string
		requestItems    []interface{}
		responseItems   []interface{}
	)
	for _, event := range payload.Events {
		switch event["type"] {
		case "request":
			requestMessage, _ = event["message"].(string)
			requestItems, _ = event["timeline_items"].([]interface{})
		case "response":
			responseMessage, _ = event["message"].(string)
			responseItems, _ = event["timeline_items"].([]interface{})
		}
	}

	if !strings.Contains(requestMessage, "├─ system: You are helpful.") {
		t.Fatalf("request timeline missing system message: %q", requestMessage)
	}
	if !strings.Contains(requestMessage, `function call exec_command [call_hist]: {"cmd":"pwd"}`) {
		t.Fatalf("request timeline missing tool call: %q", requestMessage)
	}
	if !strings.Contains(requestMessage, `tool response exec_command [call_hist]: {"cwd":"/tmp"}`) {
		t.Fatalf("request timeline missing tool result: %q", requestMessage)
	}
	if !strings.Contains(responseMessage, "Thinking: inspect logs") {
		t.Fatalf("response timeline missing thinking: %q", responseMessage)
	}
	if !strings.Contains(responseMessage, `tool call exec_command [call_live]: {"cmd":"ls"}`) {
		t.Fatalf("response timeline missing tool call: %q", responseMessage)
	}
	if !strings.Contains(responseMessage, "Final output: final answer") {
		t.Fatalf("response timeline missing final output: %q", responseMessage)
	}
	if len(requestItems) != 4 {
		t.Fatalf("len(request timeline_items) = %d, want 4", len(requestItems))
	}
	if len(responseItems) != 3 {
		t.Fatalf("len(response timeline_items) = %d, want 3", len(responseItems))
	}
	firstRequest, ok := requestItems[0].(map[string]interface{})
	if !ok || firstRequest["kind"] != "message" || firstRequest["role"] != "system" {
		t.Fatalf("unexpected first request timeline item: %#v", requestItems[0])
	}
	secondResponse, ok := responseItems[1].(map[string]interface{})
	if !ok || secondResponse["kind"] != "tool_call" || secondResponse["name"] != "exec_command" {
		t.Fatalf("unexpected response tool call item: %#v", responseItems[1])
	}
}

func TestTraceDetailAPIHandlerSurfacesProviderErrorBlocks(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"stream failure"}]}]}`
	resBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		`data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error","code":"stream_aborted"}}}`,
	}, "\n")
	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
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
	if payload.AIContent != "partial" {
		t.Fatalf("AIContent = %q, want partial", payload.AIContent)
	}
	if len(payload.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(payload.AIBlocks))
	}
	if payload.AIBlocks[0].Title != "Provider Error" || !strings.Contains(payload.AIBlocks[0].Text, "stream aborted") {
		t.Fatalf("AIBlocks[0] = %+v", payload.AIBlocks[0])
	}

	var responseItems []interface{}
	for _, event := range payload.Events {
		if event["type"] == "response" {
			responseItems, _ = event["timeline_items"].([]interface{})
			break
		}
	}
	if len(responseItems) != 2 {
		t.Fatalf("len(response timeline_items) = %d, want 2", len(responseItems))
	}
	second, ok := responseItems[1].(map[string]interface{})
	if !ok || second["kind"] != "provider_error" || second["label"] != "Provider Error" {
		t.Fatalf("unexpected provider error timeline item: %#v", responseItems[1])
	}
	if body, _ := second["body"].(string); !strings.Contains(body, "stream aborted") {
		t.Fatalf("provider error body = %q, want contain stream aborted", body)
	}
}

func TestTraceDetailAPIHandlerSurfacesRefusalBlocks(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"give disallowed instructions"}]}]}`
	resBody := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"checking safety"}`,
		`data: {"type":"response.refusal.delta","delta":"I can't help with that."}`,
	}, "\n")
	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
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
	if payload.AIReasoning != "checking safety" {
		t.Fatalf("AIReasoning = %q, want checking safety", payload.AIReasoning)
	}
	if len(payload.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(payload.AIBlocks))
	}
	if payload.AIBlocks[0].Title != "Refusal" || !strings.Contains(payload.AIBlocks[0].Text, "can't help") {
		t.Fatalf("AIBlocks[0] = %+v", payload.AIBlocks[0])
	}

	var responseItems []interface{}
	for _, event := range payload.Events {
		if event["type"] == "response" {
			responseItems, _ = event["timeline_items"].([]interface{})
			break
		}
	}
	if len(responseItems) != 2 {
		t.Fatalf("len(response timeline_items) = %d, want 2", len(responseItems))
	}
	second, ok := responseItems[1].(map[string]interface{})
	if !ok || second["kind"] != "refusal" || second["label"] != "Refusal" {
		t.Fatalf("unexpected refusal timeline item: %#v", responseItems[1])
	}
	if body, _ := second["body"].(string); !strings.Contains(body, "can't help") {
		t.Fatalf("refusal body = %q, want contain refusal text", body)
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

func TestListAPIHandlerReturnsStoreNotConfiguredError(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
	rr := httptest.NewRecorder()
	listAPIHandler(nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["error"] != "store not configured" {
		t.Fatalf("error = %q, want store not configured", payload["error"])
	}
}

func TestTraceDetailAPIHandlerReturnsParseErrorForInvalidCassette(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "broken.http")
	if err := os.WriteFile(tracePath, []byte("not a valid cassette"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	entry := store.LogEntry{
		ID:      "broken",
		LogPath: tracePath,
	}
	rr := httptest.NewRecorder()
	handleTraceDetail(rr, tracePath, entry)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(payload["error"], "parse error:") {
		t.Fatalf("error = %q, want parse error prefix", payload["error"])
	}
}

func TestTraceRawAPIHandlerReturnsFileNotFoundError(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	handleTraceRaw(rr, filepath.Join(t.TempDir(), "missing.http"), store.LogEntry{ID: "missing"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["error"] != "file not found" {
		t.Fatalf("error = %q, want file not found", payload["error"])
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

func buildRecordFixtureWithRequestHeaders(t *testing.T, url string, isStream bool, extraHeaders []string, reqBody string, resBody string) []byte {
	t.Helper()

	requestHeaderLines := []string{
		"POST " + url + " HTTP/1.1",
		"Host: example.com",
		"Content-Type: application/json",
	}
	requestHeaderLines = append(requestHeaderLines, extraHeaders...)
	request := strings.Join(requestHeaderLines, "\r\n") + "\r\n\r\n" + reqBody
	responseHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	if isStream {
		responseHeader = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
	}
	header := buildRecordHeader(url, isStream, reqBody, resBody)
	header.Layout.ReqHeaderLen = int64(len(strings.Join(requestHeaderLines, "\r\n") + "\r\n\r\n"))
	header.Layout.ResHeaderLen = int64(len(responseHeader))
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	payload := request + "\n" + responseHeader + resBody
	return append(prelude, []byte(payload)...)
}
