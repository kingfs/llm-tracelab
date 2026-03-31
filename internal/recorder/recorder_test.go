package recorder

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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
