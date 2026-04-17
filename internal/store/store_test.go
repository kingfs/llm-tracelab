package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	_ "modernc.org/sqlite"
)

func TestNewUpgradesLegacySchemaWithoutSessionColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	legacySchema := `
	CREATE TABLE logs (
		path TEXT PRIMARY KEY,
		trace_id TEXT NOT NULL DEFAULT '',
		mod_time_ns INTEGER NOT NULL,
		file_size INTEGER NOT NULL,
		version TEXT NOT NULL,
		request_id TEXT NOT NULL,
		recorded_at TEXT NOT NULL,
		model TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		operation TEXT NOT NULL DEFAULT '',
		endpoint TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL,
		method TEXT NOT NULL,
		status_code INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL,
		ttft_ms INTEGER NOT NULL,
		client_ip TEXT NOT NULL,
		content_length INTEGER NOT NULL,
		error_text TEXT NOT NULL,
		prompt_tokens INTEGER NOT NULL,
		completion_tokens INTEGER NOT NULL,
		total_tokens INTEGER NOT NULL,
		cached_tokens INTEGER NOT NULL,
		req_header_len INTEGER NOT NULL,
		req_body_len INTEGER NOT NULL,
		res_header_len INTEGER NOT NULL,
		res_body_len INTEGER NOT NULL,
		is_stream INTEGER NOT NULL
	);
	CREATE INDEX idx_logs_recorded_at ON logs(recorded_at DESC);
	CREATE INDEX idx_logs_model_recorded_at ON logs(model, recorded_at DESC);
	`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("db.Exec(legacySchema) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	rows, err := st.db.Query(`PRAGMA table_info(logs)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(logs) error = %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() error = %v", err)
	}

	for _, name := range []string{"session_id", "session_source", "window_id", "client_request_id"} {
		if !columns[name] {
			t.Fatalf("column %q missing after upgrade", name)
		}
	}
}

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

	result, err := st.ListSessionPage(1, 50, ListFilter{})
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

func TestListPageAppliesFilters(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, model string, provider string, sessionID string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 4, 16, 9, 0, 0, 0, time.UTC),
				Model:         model,
				Provider:      provider,
				Operation:     "responses",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    200,
				DurationMs:    30,
				TTFTMs:        10,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "header.session_id",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	writeLog("alpha.http", "gpt-alpha", "openai_compatible", "sess-alpha")
	writeLog("beta.http", "gemini-pro", "google_genai", "sess-beta")

	result, err := st.ListPage(1, 50, ListFilter{Provider: "google_genai", Query: "sess-beta"})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(result.Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].Header.Meta.Model != "gemini-pro" {
		t.Fatalf("model = %q, want gemini-pro", result.Items[0].Header.Meta.Model)
	}
}

func TestListSessionPageAppliesFilters(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, model string, provider string, sessionID string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC),
				Model:         model,
				Provider:      provider,
				Operation:     "responses",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    200,
				DurationMs:    30,
				TTFTMs:        10,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "header.session_id",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	writeLog("codex.http", "gpt-5-codex", "openai_compatible", "sess-codex")
	writeLog("google.http", "gemini-pro", "google_genai", "sess-google")

	result, err := st.ListSessionPage(1, 50, ListFilter{Model: "codex"})
	if err != nil {
		t.Fatalf("ListSessionPage() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(result.Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].SessionID != "sess-codex" {
		t.Fatalf("SessionID = %q, want sess-codex", result.Items[0].SessionID)
	}
}

func len64(v string) int64 {
	return int64(len(v))
}

func TestUpstreamCatalogPersistenceAndRoutingMetadata(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	if err := st.UpsertUpstreamTarget(UpstreamTargetRecord{
		ID:                "openai-primary",
		BaseURL:           "https://api.openai.com/v1",
		ProviderPreset:    "openai",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          100,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     time.Date(2026, 4, 17, 7, 0, 0, 0, time.UTC),
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}
	if err := st.ReplaceUpstreamModels("openai-primary", []UpstreamModelRecord{
		{UpstreamID: "openai-primary", Model: "gpt-5", Source: "catalog", SeenAt: time.Date(2026, 4, 17, 7, 0, 0, 0, time.UTC)},
		{UpstreamID: "openai-primary", Model: "gpt-4.1", Source: "catalog", SeenAt: time.Date(2026, 4, 17, 7, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatalf("ReplaceUpstreamModels() error = %v", err)
	}

	logPath := filepath.Join(dir, "trace.http")
	if err := os.WriteFile(logPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:                      "req-1",
			Time:                           time.Date(2026, 4, 17, 7, 1, 0, 0, time.UTC),
			Model:                          "gpt-5",
			Provider:                       "openai_compatible",
			Operation:                      "responses",
			Endpoint:                       "/v1/responses",
			URL:                            "/v1/responses",
			Method:                         "POST",
			StatusCode:                     200,
			DurationMs:                     42,
			TTFTMs:                         11,
			ClientIP:                       "127.0.0.1",
			ContentLength:                  12,
			SelectedUpstreamID:             "openai-primary",
			SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
			SelectedUpstreamProviderPreset: "openai",
			RoutingPolicy:                  "p2c",
			RoutingScore:                   1.25,
			RoutingCandidateCount:          2,
		},
	}
	if err := st.UpsertLog(logPath, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}

	entry, err := st.GetByID(mustTraceID(t, st, logPath))
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if entry.Header.Meta.SelectedUpstreamID != "openai-primary" {
		t.Fatalf("SelectedUpstreamID = %q, want openai-primary", entry.Header.Meta.SelectedUpstreamID)
	}
	if entry.Header.Meta.RoutingPolicy != "p2c" {
		t.Fatalf("RoutingPolicy = %q, want p2c", entry.Header.Meta.RoutingPolicy)
	}
	if entry.Header.Meta.RoutingCandidateCount != 2 {
		t.Fatalf("RoutingCandidateCount = %d, want 2", entry.Header.Meta.RoutingCandidateCount)
	}

	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE upstream_id = ?`, "openai-primary").Scan(&count); err != nil {
		t.Fatalf("QueryRow() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("upstream_models count = %d, want 2", count)
	}
}

func TestListUpstreamAnalyticsAggregatesLogs(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, statusCode int, model string, tokens int, ttft int64, errText string) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      name,
				Time:                           time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC).Add(time.Duration(len(name)) * time.Minute),
				Model:                          model,
				Provider:                       "openai_compatible",
				Operation:                      "responses",
				Endpoint:                       "/v1/responses",
				URL:                            "/v1/responses",
				Method:                         "POST",
				StatusCode:                     statusCode,
				DurationMs:                     40,
				TTFTMs:                         ttft,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  8,
				Error:                          errText,
				SelectedUpstreamID:             "openai-primary",
				SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
				RoutingCandidateCount:          2,
			},
			Usage: recordfile.UsageInfo{
				PromptTokens:     tokens / 2,
				CompletionTokens: tokens / 2,
				TotalTokens:      tokens,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	writeLog("ok-a.http", 200, "gpt-5", 20, 100, "")
	writeLog("ok-b.http", 200, "gpt-5", 30, 120, "")
	writeLog("fail.http", 503, "gpt-4.1", 0, 0, "upstream overloaded")

	analytics, err := st.ListUpstreamAnalytics(5, 3, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListUpstreamAnalytics() error = %v", err)
	}
	if len(analytics) != 1 {
		t.Fatalf("len(analytics) = %d, want 1", len(analytics))
	}
	got := analytics[0]
	if got.UpstreamID != "openai-primary" {
		t.Fatalf("UpstreamID = %q, want openai-primary", got.UpstreamID)
	}
	if got.RequestCount != 3 || got.SuccessRequest != 2 || got.FailedRequest != 1 {
		t.Fatalf("counts = %+v", got)
	}
	if got.TotalTokens != 50 {
		t.Fatalf("TotalTokens = %d, want 50", got.TotalTokens)
	}
	if got.AvgTTFT != 110 {
		t.Fatalf("AvgTTFT = %d, want 110", got.AvgTTFT)
	}
	if got.LastModel == "" || len(got.Models) == 0 {
		t.Fatalf("model coverage missing: %+v", got)
	}
	if len(got.RecentErrors) != 1 {
		t.Fatalf("RecentErrors = %#v, want 1 error", got.RecentErrors)
	}
	if len(got.RecentFailures) != 1 || got.RecentFailures[0].TraceID == "" {
		t.Fatalf("RecentFailures = %#v, want 1 traced failure", got.RecentFailures)
	}
}

func TestGetUpstreamDetailReturnsBreakdownAndRecentTraces(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, endpoint string, model string, statusCode int, tokens int, errText string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      name,
				Time:                           recordedAt,
				Model:                          model,
				Provider:                       "openai_compatible",
				Operation:                      "responses",
				Endpoint:                       endpoint,
				URL:                            endpoint,
				Method:                         "POST",
				StatusCode:                     statusCode,
				DurationMs:                     40,
				TTFTMs:                         90,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  6,
				Error:                          errText,
				SelectedUpstreamID:             "openai-primary",
				SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
			},
			Usage: recordfile.UsageInfo{
				PromptTokens:     tokens / 2,
				CompletionTokens: tokens / 2,
				TotalTokens:      tokens,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	base := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	writeLog("match-a.http", base.Add(1*time.Minute), "/v1/responses", "gpt-5", 200, 20, "")
	writeLog("match-b.http", base.Add(2*time.Minute), "/v1/chat/completions", "gpt-5", 503, 0, "upstream overloaded")
	writeLog("other-model.http", base.Add(3*time.Minute), "/v1/responses", "gemini-2.5-flash", 200, 10, "")

	detail, err := st.GetUpstreamDetail("openai-primary", time.Time{}, "gpt-5", 10, time.Minute, 4)
	if err != nil {
		t.Fatalf("GetUpstreamDetail() error = %v", err)
	}
	if detail.Analytics.UpstreamID != "openai-primary" {
		t.Fatalf("UpstreamID = %q, want openai-primary", detail.Analytics.UpstreamID)
	}
	if len(detail.Traces) != 2 {
		t.Fatalf("len(Traces) = %d, want 2", len(detail.Traces))
	}
	if detail.Traces[0].Header.Meta.RequestID != "match-b.http" {
		t.Fatalf("most recent trace = %q, want match-b.http", detail.Traces[0].Header.Meta.RequestID)
	}
	if len(detail.Models) != 1 || detail.Models[0].Label != "gpt-5" || detail.Models[0].Count != 2 {
		t.Fatalf("Models = %#v, want gpt-5 x 2", detail.Models)
	}
	if len(detail.Endpoints) != 2 {
		t.Fatalf("Endpoints = %#v, want 2 items", detail.Endpoints)
	}
	if len(detail.FailureReasons) != 1 || detail.FailureReasons[0].Label != "upstream_overloaded" || detail.FailureReasons[0].Count != 1 {
		t.Fatalf("FailureReasons = %#v, want upstream_overloaded x 1", detail.FailureReasons)
	}
	if len(detail.Analytics.RecentFailures) != 1 || detail.Analytics.RecentFailures[0].Model != "gpt-5" {
		t.Fatalf("RecentFailures = %#v, want one gpt-5 failure", detail.Analytics.RecentFailures)
	}
	if detail.Analytics.RecentFailures[0].Reason != "upstream_overloaded" {
		t.Fatalf("RecentFailure reason = %q, want upstream_overloaded", detail.Analytics.RecentFailures[0].Reason)
	}
	if len(detail.Timeline) != 4 {
		t.Fatalf("len(Timeline) = %d, want 4", len(detail.Timeline))
	}
	totalTimeline := 0
	for _, item := range detail.Timeline {
		totalTimeline += item.Count
	}
	if totalTimeline != 1 {
		t.Fatalf("timeline total = %d, want 1", totalTimeline)
	}
}

func TestGetRoutingFailureAnalyticsAggregatesReasonsAndRecent(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, model string, reason string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:            name,
				Time:                 recordedAt,
				Model:                model,
				Provider:             "openai_compatible",
				Operation:            "responses",
				Endpoint:             "/v1/responses",
				URL:                  "/v1/responses",
				Method:               "POST",
				StatusCode:           502,
				DurationMs:           12,
				TTFTMs:               0,
				ClientIP:             "127.0.0.1",
				ContentLength:        8,
				Error:                "selection failed",
				RoutingPolicy:        "p2c",
				RoutingFailureReason: reason,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	base := time.Date(2026, 4, 18, 11, 0, 0, 0, time.UTC)
	writeLog("reason-a.http", base.Add(1*time.Minute), "gpt-5", "no_supporting_target")
	writeLog("reason-b.http", base.Add(2*time.Minute), "gpt-5", "no_supporting_target")
	writeLog("reason-c.http", base.Add(3*time.Minute), "gpt-5", "all_targets_open")
	writeLog("other-model.http", base.Add(4*time.Minute), "gemini-2.5-flash", "no_supporting_target")

	analytics, err := st.GetRoutingFailureAnalytics(time.Time{}, "gpt-5", 5, 5, time.Hour, 6)
	if err != nil {
		t.Fatalf("GetRoutingFailureAnalytics() error = %v", err)
	}
	if analytics.Total != 3 {
		t.Fatalf("Total = %d, want 3", analytics.Total)
	}
	if len(analytics.Reasons) != 2 {
		t.Fatalf("Reasons = %#v, want 2 items", analytics.Reasons)
	}
	if analytics.Reasons[0].Label != "no_supporting_target" || analytics.Reasons[0].Count != 2 {
		t.Fatalf("top reason = %#v, want no_supporting_target x2", analytics.Reasons[0])
	}
	if len(analytics.Recent) != 3 {
		t.Fatalf("Recent = %#v, want 3 items", analytics.Recent)
	}
	if analytics.Recent[0].Reason != "all_targets_open" {
		t.Fatalf("most recent reason = %q, want all_targets_open", analytics.Recent[0].Reason)
	}
	if len(analytics.Timeline) != 6 {
		t.Fatalf("Timeline = %#v, want 6 buckets", analytics.Timeline)
	}
	totalTimeline := 0
	for _, item := range analytics.Timeline {
		totalTimeline += item.Count
	}
	if totalTimeline != 3 {
		t.Fatalf("timeline total = %d, want 3", totalTimeline)
	}
}

func mustTraceID(t *testing.T, st *Store, path string) string {
	t.Helper()
	var traceID string
	if err := st.db.QueryRow(`SELECT trace_id FROM logs WHERE path = ?`, path).Scan(&traceID); err != nil {
		t.Fatalf("trace id query error = %v", err)
	}
	return traceID
}
