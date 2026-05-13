package recorder

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestPrepareLogFileUsesAdapterModelExtraction(t *testing.T) {
	dir := t.TempDir()
	rec := New(dir, true, nil)

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")

	info, err := rec.PrepareLogFile(req, "https://api.openai.com")
	if err != nil {
		t.Fatalf("PrepareLogFile() error = %v", err)
	}
	defer info.File.Close()

	if info.Header.Meta.Model != "gpt-5" {
		t.Fatalf("model = %q, want gpt-5", info.Header.Meta.Model)
	}
	if info.Header.Meta.Operation != "responses" {
		t.Fatalf("operation = %q, want responses", info.Header.Meta.Operation)
	}
}

func TestUpdateLogFilePersistsPipelineEvents(t *testing.T) {
	dir := t.TempDir()
	rec := New(dir, false, nil)

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	info, err := rec.PrepareLogFile(req, "https://api.openai.com")
	if err != nil {
		t.Fatalf("PrepareLogFile() error = %v", err)
	}

	info.Header.Meta.StatusCode = 200
	info.Header.Meta.DurationMs = 42
	info.Header.Meta.TTFTMs = 10
	info.Header.Layout.ResHeaderLen = int64(len("HTTP/1.1 200 OK\r\n\r\n"))
	info.Header.Layout.ResBodyLen = int64(len(`{"ok":true}`))
	info.Events = []RecordEvent{
		{
			Type: "llm.usage",
			Time: time.Date(2026, 3, 31, 12, 0, 1, 0, time.UTC),
			Attributes: map[string]interface{}{
				"total_tokens": 18,
			},
		},
	}
	if _, err := info.File.Write([]byte("\nHTTP/1.1 200 OK\r\n\r\n{\"ok\":true}")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if err := rec.UpdateLogFile(info); err != nil {
		t.Fatalf("UpdateLogFile() error = %v", err)
	}

	content, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		t.Fatalf("ParsePrelude() error = %v", err)
	}
	found := false
	for _, event := range parsed.Events {
		if event.Type == "llm.usage" {
			found = true
			if event.Attributes["total_tokens"] != float64(18) {
				t.Fatalf("event total_tokens = %#v, want 18", event.Attributes["total_tokens"])
			}
		}
	}
	if !found {
		t.Fatalf("llm.usage event not found: %+v", parsed.Events)
	}
	if !strings.Contains(string(content), "# event:") {
		t.Fatalf("recorded file missing event lines")
	}
}

func TestUpdateLogFileIndexesGroupingFromCodexHeaders(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	rec := New(dir, false, st)

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.4","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Session_id", "sess-codex-live")
	req.Header.Set("X-Client-Request-Id", "req-codex-live")
	req.Header.Set("X-Codex-Window-Id", "sess-codex-live:3")

	info, err := rec.PrepareLogFile(req, "https://api.openai.com")
	if err != nil {
		t.Fatalf("PrepareLogFile() error = %v", err)
	}

	info.Header.Meta.StatusCode = 200
	info.Header.Meta.DurationMs = 42
	info.Header.Meta.TTFTMs = 10
	info.Header.Layout.ResHeaderLen = int64(len("HTTP/1.1 200 OK\r\n\r\n"))
	info.Header.Layout.ResBodyLen = int64(len(`{"ok":true}`))
	if _, err := info.File.Write([]byte("\nHTTP/1.1 200 OK\r\n\r\n{\"ok\":true}")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if err := rec.UpdateLogFile(info); err != nil {
		t.Fatalf("UpdateLogFile() error = %v", err)
	}

	entry, err := st.GetByRequestID(info.Header.Meta.RequestID)
	if err != nil {
		t.Fatalf("GetByRequestID() error = %v", err)
	}
	if entry.SessionID != "sess-codex-live" {
		t.Fatalf("SessionID = %q, want sess-codex-live", entry.SessionID)
	}
	if entry.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", entry.SessionSource)
	}
	if entry.WindowID != "sess-codex-live:3" {
		t.Fatalf("WindowID = %q, want sess-codex-live:3", entry.WindowID)
	}
	if entry.ClientRequestID != "req-codex-live" {
		t.Fatalf("ClientRequestID = %q, want req-codex-live", entry.ClientRequestID)
	}
}

func TestUpdateLogFileEnqueuesParseJob(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	rec := New(dir, false, st)
	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.1","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	info, err := rec.PrepareLogFile(req, "https://api.openai.com")
	if err != nil {
		t.Fatalf("PrepareLogFile() error = %v", err)
	}
	resHead := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	resBody := `{"id":"resp_1","object":"response","created_at":1741476777,"status":"completed","model":"gpt-5.1","output":[]}`
	info.Header.Meta.StatusCode = 200
	info.Header.Layout.ResHeaderLen = int64(len(resHead))
	info.Header.Layout.ResBodyLen = int64(len(resBody))
	if _, err := info.File.Write([]byte("\n" + resHead + resBody)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := rec.UpdateLogFile(info); err != nil {
		t.Fatalf("UpdateLogFile() error = %v", err)
	}
	entry, err := st.GetByRequestID(info.Header.Meta.RequestID)
	if err != nil {
		t.Fatalf("GetByRequestID() error = %v", err)
	}
	jobs, err := st.ListParseJobs("queued", 10)
	if err != nil {
		t.Fatalf("ListParseJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].TraceID != entry.ID {
		t.Fatalf("jobs = %+v, want trace %s", jobs, entry.ID)
	}
}
