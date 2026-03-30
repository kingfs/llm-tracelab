package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetailHandlerRendersConversationServerSide(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"messages":[{"role":"system","content":"You are a personal assistant."},{"role":"user","content":"Inspect this request."}],"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file.","parameters":{"type":"object","properties":{"file_path":{"type":"string"}}}}}]}`
	content := buildRecordFixture(t, "/v1/chat/completions", false, reqBody, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/view?path="+url.QueryEscape(tracePath), nil)
	rr := httptest.NewRecorder()

	DetailHandler(outputDir).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "You are a personal assistant.") {
		t.Fatalf("detail body missing rendered conversation: %s", body)
	}
	if strings.Contains(body, "marked.min.js") {
		t.Fatalf("detail body still depends on marked runtime: %s", body)
	}
	if strings.Contains(body, "raw-msg-") {
		t.Fatalf("detail body still contains client-side markdown placeholders: %s", body)
	}
}

func TestDetailRawHandlerReturnsProtocolAndMeta(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	content := buildRecordFixture(t, "/v1/chat/completions", true, `{"messages":[{"role":"user","content":"hello"}]}`, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n")
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/detail/raw?path="+url.QueryEscape(tracePath), nil)
	rr := httptest.NewRecorder()

	DetailRawHandler(outputDir).ServeHTTP(rr, req)
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

func TestDetailRawHandlerRejectsPathOutsideOutputDir(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	outsideDir := t.TempDir()
	tracePath := filepath.Join(outsideDir, "trace.http")
	content := buildRecordFixture(t, "/v1/chat/completions", false, `{"messages":[{"role":"user","content":"hello"}]}`, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/detail/raw?path="+url.QueryEscape(tracePath), nil)
	rr := httptest.NewRecorder()

	DetailRawHandler(outputDir).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}
