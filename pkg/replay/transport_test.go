package replay

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestReplayFileReturnsStructuredSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "trace.http")
	content := buildReplayFixture(t, "200 OK", `{"output":"done"}`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	summary, err := ReplayFile(path, SummaryOptions{BodyLimit: 8})
	if err != nil {
		t.Fatalf("ReplayFile() error = %v", err)
	}
	if summary.RequestMethod != "POST" {
		t.Fatalf("RequestMethod = %q, want POST", summary.RequestMethod)
	}
	if summary.StatusCode != 200 {
		t.Fatalf("StatusCode = %d, want 200", summary.StatusCode)
	}
	if !summary.BodyTruncated {
		t.Fatalf("BodyTruncated = false, want true")
	}
	if summary.BodyBytes <= len(summary.Body) {
		t.Fatalf("BodyBytes = %d, want > len(Body)=%d", summary.BodyBytes, len(summary.Body))
	}
	if !strings.Contains(summary.ContentType, "application/json") {
		t.Fatalf("ContentType = %q, want application/json", summary.ContentType)
	}
}

func TestTransportInvalidatesCacheWhenCassetteChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "trace.http")
	if err := os.WriteFile(path, buildReplayFixture(t, "200 OK", `{"output":"done"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	tr := NewTransport(path)
	req, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{"input":"hello"}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}

	if err := os.WriteFile(path, buildReplayFixture(t, "201 Created", `{"output":"changed"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(changed) error = %v", err)
	}
	changedAt := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	resp, err = tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() after change error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("StatusCode after change = %d, want 201", resp.StatusCode)
	}
}

func buildReplayFixture(t *testing.T, status string, resBody string) []byte {
	t.Helper()

	reqBody := `{"input":"hello"}`
	reqHeader := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	resHeader := "HTTP/1.1 " + status + "\r\nContent-Type: application/json\r\n\r\n"

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req_1",
			Time:          time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1-codex",
			URL:           "/v1/responses",
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
		},
	}

	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	return append(prelude, []byte(reqHeader+reqBody+"\n"+resHeader+resBody)...)
}
