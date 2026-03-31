package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	_ "modernc.org/sqlite"
)

type LogEntry struct {
	ID      string
	Header  recordfile.RecordHeader
	LogPath string
}

type Stats struct {
	TotalRequest   int
	AvgTTFT        int
	TotalTokens    int
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
}

type Store struct {
	db        *sql.DB
	outputDir string
	dbPath    string
}

type ListPageResult struct {
	Items      []LogEntry
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

func New(outputDir string) (*Store, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(outputDir, "trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	st := &Store{
		db:        db,
		outputDir: outputDir,
		dbPath:    dbPath,
	}
	if err := st.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	return st, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS logs (
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
		);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_recorded_at ON logs(recorded_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_model_recorded_at ON logs(model, recorded_at DESC);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_logs_trace_id ON logs(trace_id) WHERE trace_id <> '';`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn("logs", "trace_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "provider", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "operation", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "endpoint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.backfillTraceIDs(); err != nil {
		return err
	}
	if err := s.backfillSemantics(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(table string, column string, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid        int
		name       string
		typ        string
		notNull    int
		defaultVal sql.NullString
		pk         int
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

func (s *Store) backfillTraceIDs() error {
	rows, err := s.db.Query(`SELECT path FROM logs WHERE trace_id = '' OR trace_id IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, path := range paths {
		if _, err := s.db.Exec(`UPDATE logs SET trace_id = ? WHERE path = ?`, uuid.NewString(), path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backfillSemantics() error {
	rows, err := s.db.Query(`SELECT path, url, provider, operation, endpoint FROM logs`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rowData struct {
		path      string
		url       string
		provider  string
		operation string
		endpoint  string
	}
	var updates []rowData
	for rows.Next() {
		var row rowData
		if err := rows.Scan(&row.path, &row.url, &row.provider, &row.operation, &row.endpoint); err != nil {
			return err
		}
		if row.provider != "" && row.operation != "" && row.endpoint != "" {
			continue
		}
		semantics := llm.ClassifyPath(row.url, "")
		row.provider = semantics.Provider
		row.operation = semantics.Operation
		row.endpoint = semantics.Endpoint
		updates = append(updates, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, update := range updates {
		if _, err := s.db.Exec(
			`UPDATE logs SET provider = ?, operation = ?, endpoint = ? WHERE path = ?`,
			update.provider,
			update.operation,
			update.endpoint,
			update.path,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertLog(path string, header recordfile.RecordHeader) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	traceID, err := s.lookupOrCreateTraceID(path)
	if err != nil {
		return err
	}

	cachedTokens := 0
	if header.Usage.PromptTokenDetails != nil {
		cachedTokens = header.Usage.PromptTokenDetails.CachedTokens
	}
	if header.Meta.Provider == "" || header.Meta.Operation == "" || header.Meta.Endpoint == "" {
		semantics := llm.ClassifyPath(header.Meta.URL, "")
		if header.Meta.Provider == "" {
			header.Meta.Provider = semantics.Provider
		}
		if header.Meta.Operation == "" {
			header.Meta.Operation = semantics.Operation
		}
		if header.Meta.Endpoint == "" {
			header.Meta.Endpoint = semantics.Endpoint
		}
	}

	_, err = s.db.Exec(`
		INSERT INTO logs (
			path, trace_id, mod_time_ns, file_size, version, request_id, recorded_at, model, provider, operation, endpoint, url, method,
			status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			trace_id=CASE WHEN logs.trace_id = '' THEN excluded.trace_id ELSE logs.trace_id END,
			mod_time_ns=excluded.mod_time_ns,
			file_size=excluded.file_size,
			version=excluded.version,
			request_id=excluded.request_id,
			recorded_at=excluded.recorded_at,
			model=excluded.model,
			provider=excluded.provider,
			operation=excluded.operation,
			endpoint=excluded.endpoint,
			url=excluded.url,
			method=excluded.method,
			status_code=excluded.status_code,
			duration_ms=excluded.duration_ms,
			ttft_ms=excluded.ttft_ms,
			client_ip=excluded.client_ip,
			content_length=excluded.content_length,
			error_text=excluded.error_text,
			prompt_tokens=excluded.prompt_tokens,
			completion_tokens=excluded.completion_tokens,
			total_tokens=excluded.total_tokens,
			cached_tokens=excluded.cached_tokens,
			req_header_len=excluded.req_header_len,
			req_body_len=excluded.req_body_len,
			res_header_len=excluded.res_header_len,
			res_body_len=excluded.res_body_len,
			is_stream=excluded.is_stream
	`,
		path,
		traceID,
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
		header.Meta.Error,
		header.Usage.PromptTokens,
		header.Usage.CompletionTokens,
		header.Usage.TotalTokens,
		cachedTokens,
		header.Layout.ReqHeaderLen,
		header.Layout.ReqBodyLen,
		header.Layout.ResHeaderLen,
		header.Layout.ResBodyLen,
		boolToInt(header.Layout.IsStream),
	)

	return err
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Store) Sync() error {
	return filepath.Walk(s.outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if path == s.dbPath || strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".http") {
			return nil
		}

		same, err := s.isFresh(path, info)
		if err != nil {
			return err
		}
		if same {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		parsed, err := recordfile.ParsePrelude(content)
		if err != nil {
			if shouldSkipIncompleteRecord(content, err) {
				return nil
			}
			return fmt.Errorf("parse %s: %w", path, err)
		}

		return s.UpsertLog(path, parsed.Header)
	})
}

func shouldSkipIncompleteRecord(content []byte, err error) bool {
	if err == nil {
		return false
	}

	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return true
	}

	if bytes.HasPrefix(trimmed, []byte(recordfile.FileMagic)) {
		errText := err.Error()
		return strings.Contains(errText, "failed to read prelude") ||
			strings.Contains(errText, "missing v3 meta line") ||
			strings.Contains(errText, "invalid v3")
	}

	httpMethods := [][]byte{
		[]byte("GET "),
		[]byte("POST "),
		[]byte("PUT "),
		[]byte("PATCH "),
		[]byte("DELETE "),
		[]byte("HEAD "),
		[]byte("OPTIONS "),
	}
	for _, method := range httpMethods {
		if bytes.HasPrefix(trimmed, method) {
			return true
		}
	}

	return false
}

func (s *Store) Reset() error {
	_, err := s.db.Exec(`DELETE FROM logs`)
	return err
}

func (s *Store) Rebuild() (int, error) {
	if err := s.Reset(); err != nil {
		return 0, err
	}
	if err := s.Sync(); err != nil {
		return 0, err
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) lookupOrCreateTraceID(path string) (string, error) {
	var traceID string
	err := s.db.QueryRow(`SELECT trace_id FROM logs WHERE path = ?`, path).Scan(&traceID)
	switch {
	case err == nil && traceID != "":
		return traceID, nil
	case err == nil:
		return uuid.NewString(), nil
	case err == sql.ErrNoRows:
		return uuid.NewString(), nil
	default:
		return "", err
	}
}

func (s *Store) isFresh(path string, info os.FileInfo) (bool, error) {
	var modTimeNS int64
	var fileSize int64
	err := s.db.QueryRow(
		`SELECT mod_time_ns, file_size FROM logs WHERE path = ?`,
		path,
	).Scan(&modTimeNS, &fileSize)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return modTimeNS == info.ModTime().UnixNano() && fileSize == info.Size(), nil
}

func (s *Store) ListRecent(limit int) ([]LogEntry, error) {
	rows, err := s.db.Query(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream
		FROM logs
		ORDER BY recorded_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

func (s *Store) ListPage(page int, pageSize int) (ListPageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&total); err != nil {
		return ListPageResult{}, err
	}

	offset := (page - 1) * pageSize
	rows, err := s.db.Query(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream
		FROM logs
		ORDER BY recorded_at DESC
		LIMIT ? OFFSET ?
	`, pageSize, offset)
	if err != nil {
		return ListPageResult{}, err
	}
	defer rows.Close()

	result := ListPageResult{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return ListPageResult{}, err
		}
		result.Items = append(result.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return ListPageResult{}, err
	}
	if total == 0 {
		result.TotalPages = 0
		return result, nil
	}
	result.TotalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	return result, nil
}

func (s *Store) GetByID(traceID string) (LogEntry, error) {
	row := s.db.QueryRow(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream
		FROM logs
		WHERE trace_id = ?
	`, traceID)
	return scanEntry(row)
}

func (s *Store) PathByID(traceID string) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT path FROM logs WHERE trace_id = ?`, traceID).Scan(&path)
	return path, err
}

func (s *Store) Stats() (Stats, error) {
	var stats Stats
	var avgTTFT float64
	var successRate float64
	err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(AVG(CASE WHEN status_code BETWEEN 200 AND 299 THEN ttft_ms END), 0),
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN total_tokens ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0),
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END
		FROM logs
	`).Scan(
		&stats.TotalRequest,
		&avgTTFT,
		&stats.TotalTokens,
		&stats.SuccessRequest,
		&stats.FailedRequest,
		&successRate,
	)
	if err != nil {
		return Stats{}, err
	}

	stats.AvgTTFT = int(math.Round(avgTTFT))
	stats.SuccessRate = successRate
	return stats, nil
}

func scanEntry(scanner interface {
	Scan(dest ...any) error
}) (LogEntry, error) {
	var (
		entry      LogEntry
		recordedAt string
		errorText  string
		cached     int
		isStream   int
	)

	err := scanner.Scan(
		&entry.ID,
		&entry.LogPath,
		&entry.Header.Version,
		&entry.Header.Meta.RequestID,
		&recordedAt,
		&entry.Header.Meta.Model,
		&entry.Header.Meta.Provider,
		&entry.Header.Meta.Operation,
		&entry.Header.Meta.Endpoint,
		&entry.Header.Meta.URL,
		&entry.Header.Meta.Method,
		&entry.Header.Meta.StatusCode,
		&entry.Header.Meta.DurationMs,
		&entry.Header.Meta.TTFTMs,
		&entry.Header.Meta.ClientIP,
		&entry.Header.Meta.ContentLength,
		&errorText,
		&entry.Header.Usage.PromptTokens,
		&entry.Header.Usage.CompletionTokens,
		&entry.Header.Usage.TotalTokens,
		&cached,
		&entry.Header.Layout.ReqHeaderLen,
		&entry.Header.Layout.ReqBodyLen,
		&entry.Header.Layout.ResHeaderLen,
		&entry.Header.Layout.ResBodyLen,
		&isStream,
	)
	if err != nil {
		return LogEntry{}, err
	}

	entry.Header.Meta.Time, err = timeParse(recordedAt)
	if err != nil {
		return LogEntry{}, err
	}
	entry.Header.Meta.Error = errorText
	entry.Header.Layout.IsStream = isStream == 1
	if cached > 0 {
		entry.Header.Usage.PromptTokenDetails = &recordfile.PromptTokenDetails{CachedTokens: cached}
	}

	return entry, nil
}

func timeParse(v string) (time.Time, error) {
	return time.Parse(timeLayout, v)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
