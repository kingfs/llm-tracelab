package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestStatsHandlesAverageTTFTAsFloat(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, statusCode int, ttftMs int64, totalTokens int) {
		t.Helper()

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}

		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
				Model:         "gpt-test",
				URL:           "/v1/chat/completions",
				Method:        "POST",
				StatusCode:    statusCode,
				DurationMs:    ttftMs,
				TTFTMs:        ttftMs,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
			Layout: recordfile.LayoutInfo{},
			Usage: recordfile.UsageInfo{
				TotalTokens: totalTokens,
			},
		}

		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
	}

	writeLog("success-a.http", 200, 800, 10)
	writeLog("success-b.http", 201, 813, 20)
	writeLog("failed.http", 500, 999, 99)

	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.TotalRequest != 3 {
		t.Fatalf("TotalRequest = %d, want 3", stats.TotalRequest)
	}
	if stats.SuccessRequest != 2 {
		t.Fatalf("SuccessRequest = %d, want 2", stats.SuccessRequest)
	}
	if stats.FailedRequest != 1 {
		t.Fatalf("FailedRequest = %d, want 1", stats.FailedRequest)
	}
	if stats.TotalTokens != 30 {
		t.Fatalf("TotalTokens = %d, want 30", stats.TotalTokens)
	}
	if stats.AvgTTFT != 807 {
		t.Fatalf("AvgTTFT = %d, want 807", stats.AvgTTFT)
	}
}

func TestSyncSkipsIncompleteHTTPFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	incompletePath := filepath.Join(dir, "in-progress.http")
	if err := os.WriteFile(incompletePath, []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", incompletePath, err)
	}

	validPath := filepath.Join(dir, "complete.http")
	validHeader := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "complete",
			Time:          time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
			Model:         "gpt-test",
			URL:           "/v1/chat/completions",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    20,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: 2,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: len64("POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\n\r\n"),
			ReqBodyLen:   len64(`{"x":1}`),
			ResHeaderLen: len64("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"),
			ResBodyLen:   len64(`{}`),
		},
	}
	prelude, err := recordfile.MarshalPrelude(validHeader, recordfile.BuildEvents(validHeader))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	validContent := string(prelude) +
		"POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\n\r\n" +
		`{"x":1}` + "\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}"
	if err := os.WriteFile(validPath, []byte(validContent), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", validPath, err)
	}

	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	entries, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].LogPath != validPath {
		t.Fatalf("entries[0].LogPath = %q, want %q", entries[0].LogPath, validPath)
	}
}

func TestExtractGroupingInfoPrefersSessionIDHeader(t *testing.T) {
	req := []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nSession_id: sess-123\r\nX-Codex-Window-Id: sess-123:0\r\nX-Client-Request-Id: req-123\r\n\r\n{}")
	info, err := extractGroupingInfoFromRequest(req)
	if err != nil {
		t.Fatalf("extractGroupingInfoFromRequest() error = %v", err)
	}
	if info.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want sess-123", info.SessionID)
	}
	if info.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", info.SessionSource)
	}
	if info.WindowID != "sess-123:0" {
		t.Fatalf("WindowID = %q, want sess-123:0", info.WindowID)
	}
	if info.ClientRequestID != "req-123" {
		t.Fatalf("ClientRequestID = %q, want req-123", info.ClientRequestID)
	}
}

func TestExtractGroupingInfoFallsBackToCodexMetadata(t *testing.T) {
	req := []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nX-Codex-Turn-Metadata: {\"session_id\":\"sess-meta\"}\r\n\r\n{}")
	info, err := extractGroupingInfoFromRequest(req)
	if err != nil {
		t.Fatalf("extractGroupingInfoFromRequest() error = %v", err)
	}
	if info.SessionID != "sess-meta" {
		t.Fatalf("SessionID = %q, want sess-meta", info.SessionID)
	}
	if info.SessionSource != "header.x_codex_turn_metadata.session_id" {
		t.Fatalf("SessionSource = %q", info.SessionSource)
	}
}

func TestListSessionPageAggregatesBySession(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, statusCode int, ttftMs int64, totalTokens int, sessionID string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          recordedAt,
				Model:         "gpt-test",
				Provider:      "openai_compatible",
				Operation:     "responses",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    statusCode,
				DurationMs:    ttftMs + 200,
				TTFTMs:        ttftMs,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
			Layout: recordfile.LayoutInfo{
				IsStream: true,
			},
			Usage: recordfile.UsageInfo{
				TotalTokens: totalTokens,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "header.session_id",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	base := time.Date(2026, 4, 16, 8, 0, 0, 0, time.UTC)
	writeLog("a.http", base, 200, 120, 10, "sess-a")
	writeLog("b.http", base.Add(2*time.Minute), 500, 200, 99, "sess-a")
	writeLog("c.http", base.Add(3*time.Minute), 200, 150, 30, "sess-b")

	result, err := st.ListSessionPage(1, 50)
	if err != nil {
		t.Fatalf("ListSessionPage() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(result.Items) = %d, want 2", len(result.Items))
	}
	if result.Items[0].SessionID != "sess-b" {
		t.Fatalf("first SessionID = %q, want sess-b", result.Items[0].SessionID)
	}
	if result.Items[1].SessionID != "sess-a" {
		t.Fatalf("second SessionID = %q, want sess-a", result.Items[1].SessionID)
	}
	if result.Items[1].RequestCount != 2 {
		t.Fatalf("RequestCount = %d, want 2", result.Items[1].RequestCount)
	}
	if result.Items[1].SuccessRequest != 1 || result.Items[1].FailedRequest != 1 {
		t.Fatalf("success/failed = %d/%d, want 1/1", result.Items[1].SuccessRequest, result.Items[1].FailedRequest)
	}
	if result.Items[1].TotalTokens != 10 {
		t.Fatalf("TotalTokens = %d, want 10", result.Items[1].TotalTokens)
	}
}

func len64(v string) int64 {
	return int64(len(v))
}
