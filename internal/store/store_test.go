package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/ent/dao/tracelog"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	_ "modernc.org/sqlite"
)

func TestNewConfiguresSQLiteRuntimePragmas(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	var journalMode string
	if err := st.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := st.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want at least 5000", busyTimeout)
	}

	stats := st.db.Stats()
	if stats.MaxOpenConnections != 4 {
		t.Fatalf("MaxOpenConnections = %d, want 4", stats.MaxOpenConnections)
	}
}

func TestNewWithDatabaseAcceptsSQLiteFileDSN(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "llm_tracelab.sqlite3")
	st, err := NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	defer st.Close()
	if st.dbPath != dbPath {
		t.Fatalf("dbPath = %q, want %q", st.dbPath, dbPath)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file stat error = %v", err)
	}
}

func TestNewWithDatabaseAcceptsRelativeSQLitePath(t *testing.T) {
	t.Chdir(t.TempDir())

	st, err := NewWithDatabase("logs", "sqlite", filepath.Join("docker-data", "database.sqlite3"), 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(relative) error = %v", err)
	}
	defer st.Close()
	if st.dbPath != filepath.Join("docker-data", "database.sqlite3") {
		t.Fatalf("dbPath = %q, want relative config path", st.dbPath)
	}
	if _, err := os.Stat(filepath.Join("docker-data", "database.sqlite3")); err != nil {
		t.Fatalf("database file stat error = %v", err)
	}
}

func TestChannelConfigAndModelsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	record, err := st.UpsertChannelConfig(ChannelConfigRecord{
		ID:               "openai-primary",
		Name:             "OpenAI Primary",
		BaseURL:          "https://api.openai.com/v1",
		ProviderPreset:   "openai",
		APIKeyCiphertext: []byte("sk-secret"),
		APIKeyHint:       "sk-...cret",
		HeadersJSON:      `{"Authorization":"Bearer hidden","X-Test":"true"}`,
		Enabled:          true,
		Priority:         100,
		Weight:           1,
		CapacityHint:     1,
		ModelDiscovery:   "list_models",
	})
	if err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	if record.ID != "openai-primary" {
		t.Fatalf("record.ID = %q", record.ID)
	}
	if string(record.APIKeyCiphertext) != "sk-secret" {
		t.Fatalf("APIKeyCiphertext = %q", string(record.APIKeyCiphertext))
	}
	if record.HeadersJSON != `{"Authorization":"Bearer hidden","X-Test":"true"}` {
		t.Fatalf("HeadersJSON = %q", record.HeadersJSON)
	}

	var rawAPIKey []byte
	var rawHeaders string
	if err := st.db.QueryRow(`SELECT api_key_ciphertext, headers_json FROM channel_configs WHERE id = ?`, "openai-primary").Scan(&rawAPIKey, &rawHeaders); err != nil {
		t.Fatalf("query raw channel config error = %v", err)
	}
	if string(rawAPIKey) == "sk-secret" || !strings.HasPrefix(string(rawAPIKey), secretEnvelopeV1) {
		t.Fatalf("raw api_key_ciphertext = %q, want encrypted envelope", string(rawAPIKey))
	}
	if strings.Contains(rawHeaders, "Bearer hidden") || !strings.Contains(rawHeaders, secretEnvelopeV1) || !strings.Contains(rawHeaders, `"X-Test":"true"`) {
		t.Fatalf("raw headers_json = %q, want encrypted secret header and plaintext non-secret header", rawHeaders)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatalf("reopen New() error = %v", err)
	}
	defer reopened.Close()
	reopenedRecord, err := reopened.GetChannelConfig("openai-primary")
	if err != nil {
		t.Fatalf("reopened.GetChannelConfig() error = %v", err)
	}
	if string(reopenedRecord.APIKeyCiphertext) != "sk-secret" || reopenedRecord.HeadersJSON != record.HeadersJSON {
		t.Fatalf("reopened secrets = api_key %q headers %q", string(reopenedRecord.APIKeyCiphertext), reopenedRecord.HeadersJSON)
	}

	if err := st.ReplaceChannelModels("openai-primary", []ChannelModelRecord{
		{Model: "GPT-5", DisplayName: "GPT-5", Source: "manual", Enabled: true},
		{Model: "gpt-4.1", Source: "manual", Enabled: false},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}

	models, err := st.ListChannelModels("openai-primary", false)
	if err != nil {
		t.Fatalf("ListChannelModels(false) error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}

	enabledModels, err := st.ListChannelModels("openai-primary", true)
	if err != nil {
		t.Fatalf("ListChannelModels(true) error = %v", err)
	}
	if len(enabledModels) != 1 || enabledModels[0].Model != "gpt-5" {
		t.Fatalf("enabledModels = %#v", enabledModels)
	}

	if err := st.SetChannelModelEnabled("openai-primary", "gpt-4.1", true); err != nil {
		t.Fatalf("SetChannelModelEnabled() error = %v", err)
	}
	enabledModels, err = st.ListChannelModels("openai-primary", true)
	if err != nil {
		t.Fatalf("ListChannelModels(true after enable) error = %v", err)
	}
	if len(enabledModels) != 2 {
		t.Fatalf("len(enabledModels after enable) = %d, want 2", len(enabledModels))
	}
}

func TestModelCatalogAnalyticsCombinesChannelsAndLogs(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	for _, channel := range []ChannelConfigRecord{
		{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", ProviderPreset: "openai", HeadersJSON: "{}", Enabled: true},
		{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", ProviderPreset: "openrouter", HeadersJSON: "{}", Enabled: true},
	} {
		if _, err := st.UpsertChannelConfig(channel); err != nil {
			t.Fatalf("UpsertChannelConfig(%s) error = %v", channel.ID, err)
		}
	}
	if err := st.ReplaceChannelModels("openai", []ChannelModelRecord{{Model: "gpt-5", Source: "manual", Enabled: true}}); err != nil {
		t.Fatalf("ReplaceChannelModels(openai) error = %v", err)
	}
	if err := st.ReplaceChannelModels("openrouter", []ChannelModelRecord{{Model: "gpt-5", Source: "manual", Enabled: false}}); err != nil {
		t.Fatalf("ReplaceChannelModels(openrouter) error = %v", err)
	}

	writeAnalyticsLog := func(name string, upstreamID string, statusCode int, totalTokens int, recordedAt time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:          name,
				Time:               recordedAt,
				Model:              "gpt-5",
				URL:                "/v1/responses",
				Method:             "POST",
				StatusCode:         statusCode,
				DurationMs:         100,
				TTFTMs:             20,
				SelectedUpstreamID: upstreamID,
			},
			Usage: recordfile.UsageInfo{TotalTokens: totalTokens},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}
	now := time.Now().UTC()
	writeAnalyticsLog("success.http", "openai", 200, 100, now)
	writeAnalyticsLog("failed.http", "openrouter", 500, 50, now)

	items, err := st.ListModelCatalogAnalytics(now.Add(-24*time.Hour), startOfDayForTest(now))
	if err != nil {
		t.Fatalf("ListModelCatalogAnalytics() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item.Model != "gpt-5" || item.ChannelCount != 2 || item.EnabledChannelCount != 1 || item.ProviderCount != 2 {
		t.Fatalf("item = %+v", item)
	}
	if item.Summary.RequestCount != 2 || item.Summary.FailedRequest != 1 || item.Summary.TotalTokens != 150 {
		t.Fatalf("summary = %+v", item.Summary)
	}

	detail, err := st.GetModelDetailAnalytics("gpt-5", now.Add(-24*time.Hour), startOfDayForTest(now), time.Hour, 24)
	if err != nil {
		t.Fatalf("GetModelDetailAnalytics() error = %v", err)
	}
	if len(detail.Channels) != 2 {
		t.Fatalf("len(detail.Channels) = %d, want 2", len(detail.Channels))
	}
	if len(detail.Trends) != 24 {
		t.Fatalf("len(detail.Trends) = %d, want 24", len(detail.Trends))
	}
}

func startOfDayForTest(now time.Time) time.Time {
	year, month, day := now.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

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

func TestNewBackfillsGroupingForLegacyNoneRows(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "codex.http")
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-codex",
			Time:          time.Date(2026, 4, 21, 9, 2, 55, 0, time.UTC),
			Model:         "gpt-5.4",
			Provider:      "openai_compatible",
			Operation:     "responses.create",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    100,
			TTFTMs:        20,
			ClientIP:      "127.0.0.1",
			ContentLength: 2,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: len64("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nSession_id: sess-fixed\r\nX-Client-Request-Id: req-fixed\r\nX-Codex-Window-Id: sess-fixed:2\r\n\r\n"),
			ReqBodyLen:   len64(`{}`),
			ResHeaderLen: len64("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"),
			ResBodyLen:   len64(`{}`),
			IsStream:     true,
		},
	}
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	content := string(prelude) +
		"POST /v1/responses HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Session_id: sess-fixed\r\n" +
		"X-Client-Request-Id: req-fixed\r\n" +
		"X-Codex-Window-Id: sess-fixed:2\r\n" +
		"\r\n" +
		"{}\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", logPath, err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", logPath, err)
	}

	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacyInsert := `
	CREATE TABLE IF NOT EXISTS logs (
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
		is_stream INTEGER NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		session_source TEXT NOT NULL DEFAULT '',
		window_id TEXT NOT NULL DEFAULT '',
		client_request_id TEXT NOT NULL DEFAULT ''
	);`
	if _, err := db.Exec(legacyInsert); err != nil {
		t.Fatalf("db.Exec(schema) error = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO logs (
			path, trace_id, mod_time_ns, file_size, version, request_id, recorded_at, model, provider, operation, endpoint, url, method,
			status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		logPath,
		"trace-legacy",
		info.ModTime().UnixNano(),
		info.Size(),
		header.Version,
		header.Meta.RequestID,
		header.Meta.Time.UTC().Format(timeLayout),
		header.Meta.Model,
		header.Meta.Provider,
		header.Meta.Operation,
		header.Meta.Endpoint,
		header.Meta.URL,
		header.Meta.Method,
		header.Meta.StatusCode,
		header.Meta.DurationMs,
		header.Meta.TTFTMs,
		header.Meta.ClientIP,
		header.Meta.ContentLength,
		"",
		0,
		0,
		0,
		0,
		header.Layout.ReqHeaderLen,
		header.Layout.ReqBodyLen,
		header.Layout.ResHeaderLen,
		header.Layout.ResBodyLen,
		boolToInt(header.Layout.IsStream),
		"",
		"none",
		"",
		"",
	); err != nil {
		t.Fatalf("db.Exec(insert) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	entry, err := st.GetByID("trace-legacy")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if entry.SessionID != "sess-fixed" {
		t.Fatalf("SessionID = %q, want sess-fixed", entry.SessionID)
	}
	if entry.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", entry.SessionSource)
	}
	if entry.WindowID != "sess-fixed:2" {
		t.Fatalf("WindowID = %q, want sess-fixed:2", entry.WindowID)
	}
	if entry.ClientRequestID != "req-fixed" {
		t.Fatalf("ClientRequestID = %q, want req-fixed", entry.ClientRequestID)
	}

	var recordedAtType string
	rows, err := st.db.Query(`PRAGMA table_info(logs)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(logs) error = %v", err)
	}
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
		if name == "recorded_at" {
			recordedAtType = typ
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close() error = %v", err)
	}
	if !strings.EqualFold(recordedAtType, "datetime") {
		t.Fatalf("recorded_at type = %q, want datetime", recordedAtType)
	}
	row, err := st.client.TraceLog.Query().Where(tracelog.TraceIDEQ("trace-legacy")).Only(context.Background())
	if err != nil {
		t.Fatalf("ent TraceLog.Query() error = %v", err)
	}
	if !row.RecordedAt.Equal(header.Meta.Time.UTC()) {
		t.Fatalf("ent RecordedAt = %s, want %s", row.RecordedAt, header.Meta.Time.UTC())
	}
}

func TestDatasetRoundTripAndDedupAppend(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, requestID string) string {
		t.Helper()

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     requestID,
				Time:          time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
				Model:         "gpt-5.1-codex",
				Provider:      "openai_compatible",
				Operation:     "responses.create",
				Endpoint:      "v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    200,
				DurationMs:    100,
				TTFTMs:        10,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
		entry, err := st.GetByRequestID(requestID)
		if err != nil {
			t.Fatalf("GetByRequestID(%q) error = %v", requestID, err)
		}
		return entry.ID
	}

	traceA := writeLog("a.http", "req-a")
	traceB := writeLog("b.http", "req-b")

	dataset, err := st.CreateDataset("smoke", "dataset desc")
	if err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}
	added, skipped, err := st.AppendDatasetExamples(dataset.ID, []string{traceA, traceB, traceA}, "trace_list", "", "note")
	if err != nil {
		t.Fatalf("AppendDatasetExamples() error = %v", err)
	}
	if added != 2 || skipped != 0 {
		t.Fatalf("AppendDatasetExamples() added/skipped = %d/%d, want 2/0", added, skipped)
	}
	added, skipped, err = st.AppendDatasetExamples(dataset.ID, []string{traceB}, "trace_list", "", "")
	if err != nil {
		t.Fatalf("AppendDatasetExamples() second error = %v", err)
	}
	if added != 0 || skipped != 1 {
		t.Fatalf("second append added/skipped = %d/%d, want 0/1", added, skipped)
	}

	got, err := st.GetDataset(dataset.ID)
	if err != nil {
		t.Fatalf("GetDataset() error = %v", err)
	}
	if got.ExampleCount != 2 {
		t.Fatalf("GetDataset().ExampleCount = %d, want 2", got.ExampleCount)
	}

	items, err := st.GetDatasetExamples(dataset.ID)
	if err != nil {
		t.Fatalf("GetDatasetExamples() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(GetDatasetExamples()) = %d, want 2", len(items))
	}
	if items[0].Position != 1 || items[1].Position != 2 {
		t.Fatalf("positions = %d,%d, want 1,2", items[0].Position, items[1].Position)
	}
	if items[0].TraceID != traceA || items[1].TraceID != traceB {
		t.Fatalf("trace order = %q,%q, want %q,%q", items[0].TraceID, items[1].TraceID, traceA, traceB)
	}

	list, err := st.ListDatasets()
	if err != nil {
		t.Fatalf("ListDatasets() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != dataset.ID {
		t.Fatalf("ListDatasets() = %#v, want one dataset %q", list, dataset.ID)
	}
}

func TestEvalRunAndScoresRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	path := filepath.Join(dir, "trace.http")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-score",
			Time:          time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1-codex",
			Provider:      "openai_compatible",
			Operation:     "responses.create",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    100,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: 4,
		},
	}
	if err := st.UpsertLog(path, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}
	entry, err := st.GetByRequestID("req-score")
	if err != nil {
		t.Fatalf("GetByRequestID() error = %v", err)
	}

	dataset, err := st.CreateDataset("eval-dataset", "")
	if err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}
	run, err := st.CreateEvalRun(dataset.ID, "dataset", dataset.ID, "baseline_v1", 1)
	if err != nil {
		t.Fatalf("CreateEvalRun() error = %v", err)
	}
	score, err := st.AddScore(ScoreRecord{
		TraceID:      entry.ID,
		SessionID:    entry.SessionID,
		DatasetID:    dataset.ID,
		EvalRunID:    run.ID,
		EvaluatorKey: "http_status_2xx",
		Value:        1,
		Status:       "pass",
		Label:        "pass",
		Explanation:  "ok",
	})
	if err != nil {
		t.Fatalf("AddScore() error = %v", err)
	}
	if err := st.FinalizeEvalRun(run.ID, 1, 1, 0); err != nil {
		t.Fatalf("FinalizeEvalRun() error = %v", err)
	}

	gotRun, err := st.GetEvalRun(run.ID)
	if err != nil {
		t.Fatalf("GetEvalRun() error = %v", err)
	}
	if gotRun.ScoreCount != 1 || gotRun.PassCount != 1 || gotRun.FailCount != 0 {
		t.Fatalf("GetEvalRun() = %#v, want score/pass/fail = 1/1/0", gotRun)
	}

	runs, err := st.ListEvalRuns(10)
	if err != nil {
		t.Fatalf("ListEvalRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("ListEvalRuns() = %#v, want run %q", runs, run.ID)
	}

	scores, err := st.ListScores(ScoreFilter{EvalRunID: run.ID}, 10)
	if err != nil {
		t.Fatalf("ListScores(eval_run) error = %v", err)
	}
	if len(scores) != 1 || scores[0].ID != score.ID {
		t.Fatalf("ListScores(eval_run) = %#v, want score %q", scores, score.ID)
	}

	scores, err = st.ListScores(ScoreFilter{DatasetID: dataset.ID, TraceID: entry.ID}, 10)
	if err != nil {
		t.Fatalf("ListScores(dataset+trace) error = %v", err)
	}
	if len(scores) != 1 || scores[0].EvaluatorKey != "http_status_2xx" {
		t.Fatalf("ListScores(dataset+trace) = %#v, want http_status_2xx", scores)
	}
}

func TestExperimentRunRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	baselineRun, err := st.CreateEvalRun("", "trace_list", "", "baseline_v1", 2)
	if err != nil {
		t.Fatalf("CreateEvalRun(baseline) error = %v", err)
	}
	candidateRun, err := st.CreateEvalRun("", "trace_list", "", "baseline_v1", 2)
	if err != nil {
		t.Fatalf("CreateEvalRun(candidate) error = %v", err)
	}

	experiment, err := st.CreateExperimentRun(ExperimentRunRecord{
		Name:                "baseline-vs-candidate",
		Description:         "first experiment",
		BaselineEvalRunID:   baselineRun.ID,
		CandidateEvalRunID:  candidateRun.ID,
		BaselineScoreCount:  6,
		CandidateScoreCount: 6,
		BaselinePassRate:    50,
		CandidatePassRate:   66.67,
		PassRateDelta:       16.67,
		MatchedScoreCount:   6,
		ImprovementCount:    2,
		RegressionCount:     1,
	})
	if err != nil {
		t.Fatalf("CreateExperimentRun() error = %v", err)
	}

	got, err := st.GetExperimentRun(experiment.ID)
	if err != nil {
		t.Fatalf("GetExperimentRun() error = %v", err)
	}
	if got.BaselineEvalRunID != baselineRun.ID || got.CandidateEvalRunID != candidateRun.ID {
		t.Fatalf("GetExperimentRun() = %#v, want eval runs %q and %q", got, baselineRun.ID, candidateRun.ID)
	}
	if got.ImprovementCount != 2 || got.RegressionCount != 1 || got.MatchedScoreCount != 6 {
		t.Fatalf("GetExperimentRun() = %#v, want counts 2/1/6", got)
	}

	runs, err := st.ListExperimentRuns(10)
	if err != nil {
		t.Fatalf("ListExperimentRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != experiment.ID {
		t.Fatalf("ListExperimentRuns() = %#v, want experiment %q", runs, experiment.ID)
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

func TestSaveObservationPersistsSummaryAndSemanticNodes(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	root := observe.SemanticNode{
		ID:             "node-root",
		ProviderType:   "message",
		NormalizedType: observe.NodeMessage,
		Path:           "$.output[0]",
		Index:          0,
		Children: []observe.SemanticNode{{
			ID:             "node-child",
			ProviderType:   "output_text",
			NormalizedType: observe.NodeText,
			Path:           "$.output[0].content[0]",
			Index:          0,
			Text:           "hello",
		}},
	}
	obs := observe.TraceObservation{
		TraceID:       "trace-observe",
		Provider:      "openai_compatible",
		Operation:     "responses",
		Model:         "gpt-5.1",
		Parser:        "openai",
		ParserVersion: "0.1.0",
		Status:        observe.ParseStatusParsed,
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{root},
		},
		Warnings: []observe.ParseWarning{{Code: "fixture", Message: "test warning"}},
	}
	if err := st.SaveObservation(obs); err != nil {
		t.Fatalf("SaveObservation() error = %v", err)
	}

	summary, err := st.GetObservationSummary("trace-observe")
	if err != nil {
		t.Fatalf("GetObservationSummary() error = %v", err)
	}
	if summary.Parser != "openai" || summary.Status != string(observe.ParseStatusParsed) {
		t.Fatalf("summary = %+v", summary)
	}

	nodes, err := st.ListSemanticNodes("trace-observe")
	if err != nil {
		t.Fatalf("ListSemanticNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	tree := observe.RebuildNodeTree(nodes)
	if len(tree) != 1 || len(tree[0].Children) != 1 || tree[0].Children[0].Text != "hello" {
		t.Fatalf("tree = %+v", tree)
	}
}

func TestSaveFindingsRebuildsTraceFindings(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	longEvidence := strings.Repeat("x", 600)
	first := observe.Finding{
		ID:              "finding-1",
		TraceID:         "trace-findings",
		Category:        "credential_leak",
		Severity:        observe.SeverityHigh,
		Confidence:      0.95,
		Title:           "Credential exposure",
		EvidencePath:    "trace#trace-findings#node#node-secret",
		EvidenceExcerpt: longEvidence,
		NodeID:          "node-secret",
		Detector:        "credential",
		DetectorVersion: "0.1.0",
	}
	if err := st.SaveFindings("trace-findings", []observe.Finding{first}); err != nil {
		t.Fatalf("SaveFindings() error = %v", err)
	}
	findings, err := st.ListFindings("trace-findings", FindingFilter{Severity: string(observe.SeverityHigh)})
	if err != nil {
		t.Fatalf("ListFindings() error = %v", err)
	}
	if len(findings) != 1 || findings[0].ID != "finding-1" {
		t.Fatalf("findings = %+v", findings)
	}
	if len(findings[0].EvidenceExcerpt) != 500 {
		t.Fatalf("evidence excerpt length = %d, want 500", len(findings[0].EvidenceExcerpt))
	}

	second := first
	second.ID = "finding-2"
	second.Category = "tool_result_error"
	second.Severity = observe.SeverityMedium
	if err := st.SaveFindings("trace-findings", []observe.Finding{second}); err != nil {
		t.Fatalf("SaveFindings(rebuild) error = %v", err)
	}
	findings, err = st.ListFindings("trace-findings", FindingFilter{})
	if err != nil {
		t.Fatalf("ListFindings(rebuild) error = %v", err)
	}
	if len(findings) != 1 || findings[0].ID != "finding-2" {
		t.Fatalf("findings after rebuild = %+v", findings)
	}
	allFindings, err := st.ListAllFindings(FindingFilter{Category: "tool_result_error"}, 10)
	if err != nil {
		t.Fatalf("ListAllFindings() error = %v", err)
	}
	if len(allFindings) != 1 || allFindings[0].TraceID != "trace-findings" {
		t.Fatalf("all findings = %+v", allFindings)
	}
}

func TestSaveAndListAnalysisRuns(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	id, err := st.SaveAnalysisRun(AnalysisRunRecord{
		SessionID:       "sess-analysis",
		Kind:            "session_summary",
		Analyzer:        "session_summary",
		AnalyzerVersion: "0.1.0",
		InputRef:        "session:sess-analysis",
		OutputJSON:      `{"session_id":"sess-analysis"}`,
		Status:          "completed",
	})
	if err != nil {
		t.Fatalf("SaveAnalysisRun() error = %v", err)
	}
	if id == 0 {
		t.Fatalf("analysis run id = 0")
	}
	runs, err := st.ListAnalysisRuns("sess-analysis", "", "session_summary", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != id || runs[0].OutputJSON == "" {
		t.Fatalf("runs = %+v, want id %d", runs, id)
	}
	allRuns, err := st.ListAnalysisRuns("", "", "session_summary", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns(all) error = %v", err)
	}
	if len(allRuns) != 1 || allRuns[0].ID != id {
		t.Fatalf("all runs = %+v, want id %d", allRuns, id)
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
