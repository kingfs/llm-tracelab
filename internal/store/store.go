package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	_ "modernc.org/sqlite"
)

type LogEntry struct {
	ID              string
	Header          recordfile.RecordHeader
	LogPath         string
	SessionID       string
	SessionSource   string
	WindowID        string
	ClientRequestID string
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

type ListFilter struct {
	Query    string
	Provider string
	Model    string
}

type GroupingInfo struct {
	SessionID       string
	SessionSource   string
	WindowID        string
	ClientRequestID string
}

type SessionSummary struct {
	SessionID      string
	SessionSource  string
	RequestCount   int
	FirstSeen      time.Time
	LastSeen       time.Time
	LastModel      string
	Providers      []string
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
	TotalTokens    int
	AvgTTFT        int
	TotalDuration  int64
	StreamCount    int
}

type SessionPageResult struct {
	Items      []SessionSummary
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

type UpstreamTargetRecord struct {
	ID                string
	BaseURL           string
	ProviderPreset    string
	ProtocolFamily    string
	RoutingProfile    string
	Enabled           bool
	Priority          int
	Weight            float64
	CapacityHint      float64
	LastRefreshAt     time.Time
	LastRefreshStatus string
	LastRefreshError  string
}

type UpstreamModelRecord struct {
	UpstreamID string
	Model      string
	Source     string
	SeenAt     time.Time
}

type UpstreamAnalyticsRecord struct {
	UpstreamID     string
	RequestCount   int
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
	TotalTokens    int
	AvgTTFT        int
	LastSeen       time.Time
	Models         []string
	LastModel      string
	RecentErrors   []string
	RecentFailures []UpstreamFailureRecord
}

type UpstreamFailureRecord struct {
	TraceID    string
	Model      string
	Endpoint   string
	StatusCode int
	RecordedAt time.Time
	Reason     string
	ErrorText  string
}

type UpstreamDetail struct {
	Analytics      UpstreamAnalyticsRecord
	Traces         []LogEntry
	Models         []CountItem
	Endpoints      []CountItem
	FailureReasons []CountItem
	Timeline       []TimeCountItem
}

type CountItem struct {
	Label string
	Count int
}

type RoutingFailureAnalytics struct {
	Total    int
	Reasons  []CountItem
	Recent   []RoutingFailureRecord
	Timeline []TimeCountItem
}

type RoutingFailureRecord struct {
	TraceID    string
	Model      string
	Endpoint   string
	RecordedAt time.Time
	Reason     string
	ErrorText  string
	StatusCode int
}

type TimeCountItem struct {
	Time  time.Time
	Count int
}

func (s *Store) ListUpstreamTargets() ([]UpstreamTargetRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, base_url, provider_preset, protocol_family, routing_profile, enabled,
		       priority, weight, capacity_hint, last_refresh_at, last_refresh_status, last_refresh_error
		FROM upstream_targets
		ORDER BY priority DESC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UpstreamTargetRecord
	for rows.Next() {
		var (
			record        UpstreamTargetRecord
			enabled       int
			lastRefreshAt string
		)
		if err := rows.Scan(
			&record.ID,
			&record.BaseURL,
			&record.ProviderPreset,
			&record.ProtocolFamily,
			&record.RoutingProfile,
			&enabled,
			&record.Priority,
			&record.Weight,
			&record.CapacityHint,
			&lastRefreshAt,
			&record.LastRefreshStatus,
			&record.LastRefreshError,
		); err != nil {
			return nil, err
		}
		record.Enabled = enabled == 1
		if strings.TrimSpace(lastRefreshAt) != "" {
			record.LastRefreshAt, err = timeParse(lastRefreshAt)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ListUpstreamModels() ([]UpstreamModelRecord, error) {
	rows, err := s.db.Query(`
		SELECT upstream_id, model, source, seen_at
		FROM upstream_models
		ORDER BY upstream_id ASC, model ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UpstreamModelRecord
	for rows.Next() {
		var (
			record UpstreamModelRecord
			seenAt string
		)
		if err := rows.Scan(&record.UpstreamID, &record.Model, &record.Source, &seenAt); err != nil {
			return nil, err
		}
		record.SeenAt, err = timeParse(seenAt)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ListUpstreamAnalytics(limitModels int, limitErrors int, since time.Time, modelFilter string) ([]UpstreamAnalyticsRecord, error) {
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	rows, err := s.db.Query(`
		SELECT
			selected_upstream_id,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END AS success_rate,
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN total_tokens ELSE 0 END), 0) AS total_tokens,
			COALESCE(AVG(CASE WHEN status_code BETWEEN 200 AND 299 THEN ttft_ms END), 0) AS avg_ttft,
			MAX(recorded_at) AS last_seen
		FROM logs
		WHERE selected_upstream_id <> ''`+whereSQL+`
		GROUP BY selected_upstream_id
		ORDER BY request_count DESC, selected_upstream_id ASC
	`, whereArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UpstreamAnalyticsRecord
	for rows.Next() {
		var (
			record   UpstreamAnalyticsRecord
			lastSeen string
			avgTTFT  float64
		)
		if err := rows.Scan(
			&record.UpstreamID,
			&record.RequestCount,
			&record.SuccessRequest,
			&record.FailedRequest,
			&record.SuccessRate,
			&record.TotalTokens,
			&avgTTFT,
			&lastSeen,
		); err != nil {
			return nil, err
		}
		record.AvgTTFT = int(math.Round(avgTTFT))
		record.LastSeen, err = timeParse(lastSeen)
		if err != nil {
			return nil, err
		}
		record.Models, record.LastModel, err = s.upstreamModelCoverage(record.UpstreamID, limitModels, since, modelFilter)
		if err != nil {
			return nil, err
		}
		record.RecentErrors, err = s.upstreamRecentErrors(record.UpstreamID, limitErrors, since, modelFilter)
		if err != nil {
			return nil, err
		}
		record.RecentFailures, err = s.upstreamRecentFailures(record.UpstreamID, limitErrors, since, modelFilter)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) GetRoutingFailureAnalytics(since time.Time, modelFilter string, limitReasons int, limitRecent int, bucketSize time.Duration, bucketCount int) (RoutingFailureAnalytics, error) {
	if limitReasons <= 0 {
		limitReasons = 5
	}
	if limitRecent <= 0 {
		limitRecent = 5
	}
	if bucketSize <= 0 {
		bucketSize = time.Hour
	}
	if bucketCount <= 0 {
		bucketCount = 12
	}

	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	baseWhere := `routing_failure_reason <> ''`
	if strings.TrimSpace(whereSQL) != "" {
		baseWhere += whereSQL
	}

	var analytics RoutingFailureAnalytics
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM logs WHERE `+baseWhere, whereArgs...).Scan(&analytics.Total); err != nil {
		return RoutingFailureAnalytics{}, err
	}

	reasonArgs := append([]any{}, whereArgs...)
	reasonArgs = append(reasonArgs, limitReasons)
	reasonRows, err := s.db.Query(`
		SELECT routing_failure_reason, COUNT(*) AS count
		FROM logs
		WHERE `+baseWhere+`
		GROUP BY routing_failure_reason
		ORDER BY count DESC, routing_failure_reason ASC
		LIMIT ?
	`, reasonArgs...)
	if err != nil {
		return RoutingFailureAnalytics{}, err
	}
	defer reasonRows.Close()
	for reasonRows.Next() {
		var item CountItem
		if err := reasonRows.Scan(&item.Label, &item.Count); err != nil {
			return RoutingFailureAnalytics{}, err
		}
		analytics.Reasons = append(analytics.Reasons, item)
	}
	if err := reasonRows.Err(); err != nil {
		return RoutingFailureAnalytics{}, err
	}

	recentArgs := append([]any{}, whereArgs...)
	recentArgs = append(recentArgs, limitRecent)
	recentRows, err := s.db.Query(`
		SELECT trace_id, model, endpoint, recorded_at, routing_failure_reason, error_text, status_code
		FROM logs
		WHERE `+baseWhere+`
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, recentArgs...)
	if err != nil {
		return RoutingFailureAnalytics{}, err
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var (
			item       RoutingFailureRecord
			recordedAt string
		)
		if err := recentRows.Scan(&item.TraceID, &item.Model, &item.Endpoint, &recordedAt, &item.Reason, &item.ErrorText, &item.StatusCode); err != nil {
			return RoutingFailureAnalytics{}, err
		}
		item.RecordedAt, err = timeParse(recordedAt)
		if err != nil {
			return RoutingFailureAnalytics{}, err
		}
		analytics.Recent = append(analytics.Recent, item)
	}
	if err := recentRows.Err(); err != nil {
		return RoutingFailureAnalytics{}, err
	}

	referenceTime := time.Now().UTC()
	var latestRecordedAt string
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(recorded_at), '') FROM logs WHERE `+baseWhere, whereArgs...).Scan(&latestRecordedAt); err != nil {
		return RoutingFailureAnalytics{}, err
	}
	if strings.TrimSpace(latestRecordedAt) != "" {
		latestTime, err := timeParse(latestRecordedAt)
		if err != nil {
			return RoutingFailureAnalytics{}, err
		}
		if latestTime.After(referenceTime) {
			referenceTime = latestTime
		}
	}
	bucketStart := referenceTime.UTC().Truncate(bucketSize).Add(-time.Duration(bucketCount-1) * bucketSize)
	timelineArgs := append([]any{bucketStart.Format(timeLayout)}, whereArgs...)
	timelineRows, err := s.db.Query(`
		SELECT recorded_at
		FROM logs
		WHERE recorded_at >= ? AND `+baseWhere+`
		ORDER BY recorded_at ASC
	`, timelineArgs...)
	if err != nil {
		return RoutingFailureAnalytics{}, err
	}
	defer timelineRows.Close()

	buckets := make(map[time.Time]int, bucketCount)
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		buckets[slot] = 0
	}
	for timelineRows.Next() {
		var recordedAt string
		if err := timelineRows.Scan(&recordedAt); err != nil {
			return RoutingFailureAnalytics{}, err
		}
		recordedTime, err := timeParse(recordedAt)
		if err != nil {
			return RoutingFailureAnalytics{}, err
		}
		slot := recordedTime.UTC().Truncate(bucketSize)
		if slot.Before(bucketStart) {
			continue
		}
		if _, ok := buckets[slot]; ok {
			buckets[slot]++
		}
	}
	if err := timelineRows.Err(); err != nil {
		return RoutingFailureAnalytics{}, err
	}
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		analytics.Timeline = append(analytics.Timeline, TimeCountItem{
			Time:  slot,
			Count: buckets[slot],
		})
	}

	return analytics, nil
}

func (s *Store) GetUpstreamDetail(upstreamID string, since time.Time, modelFilter string, traceLimit int, bucketSize time.Duration, bucketCount int) (UpstreamDetail, error) {
	if traceLimit <= 0 {
		traceLimit = 50
	}
	if bucketSize <= 0 {
		bucketSize = 2 * time.Hour
	}
	if bucketCount <= 0 {
		bucketCount = 12
	}
	analytics, err := s.ListUpstreamAnalytics(8, 5, since, modelFilter)
	if err != nil {
		return UpstreamDetail{}, err
	}
	var detail UpstreamDetail
	for _, item := range analytics {
		if item.UpstreamID == upstreamID {
			detail.Analytics = item
			break
		}
	}
	if detail.Analytics.UpstreamID == "" {
		return UpstreamDetail{}, sql.ErrNoRows
	}

	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	queryArgs := append([]any{upstreamID}, whereArgs...)
	queryArgs = append(queryArgs, traceLimit)
	rows, err := s.db.Query(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		FROM logs
		WHERE selected_upstream_id = ?`+whereSQL+`
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, queryArgs...)
	if err != nil {
		return UpstreamDetail{}, err
	}
	defer rows.Close()

	modelCounts := map[string]int{}
	endpointCounts := map[string]int{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return UpstreamDetail{}, err
		}
		detail.Traces = append(detail.Traces, entry)
		if entry.Header.Meta.Model != "" {
			modelCounts[entry.Header.Meta.Model]++
		}
		if entry.Header.Meta.Endpoint != "" {
			endpointCounts[entry.Header.Meta.Endpoint]++
		}
	}
	if err := rows.Err(); err != nil {
		return UpstreamDetail{}, err
	}
	detail.Models = sortedCountItems(modelCounts)
	detail.Endpoints = sortedCountItems(endpointCounts)
	detail.FailureReasons, err = s.upstreamFailureReasons(upstreamID, 5, since, modelFilter)
	if err != nil {
		return UpstreamDetail{}, err
	}

	timelineArgs := append([]any{upstreamID}, whereArgs...)
	var latestRecordedAt string
	err = s.db.QueryRow(`
		SELECT COALESCE(MAX(recorded_at), '')
		FROM logs
		WHERE selected_upstream_id = ? AND status_code >= 400`+whereSQL,
		timelineArgs...,
	).Scan(&latestRecordedAt)
	if err != nil {
		return UpstreamDetail{}, err
	}
	referenceTime := time.Now().UTC()
	if latestRecordedAt != "" {
		latestTime, err := timeParse(latestRecordedAt)
		if err != nil {
			return UpstreamDetail{}, err
		}
		if latestTime.After(referenceTime) {
			referenceTime = latestTime
		}
	}
	bucketStart := referenceTime.Truncate(bucketSize).Add(-time.Duration(bucketCount-1) * bucketSize)
	buckets := make(map[time.Time]int, bucketCount)
	failureTimelineArgs := append([]any{upstreamID}, whereArgs...)
	timelineRows, err := s.db.Query(`
		SELECT recorded_at
		FROM logs
		WHERE selected_upstream_id = ? AND status_code >= 400`+whereSQL+`
		ORDER BY recorded_at ASC
	`, failureTimelineArgs...)
	if err != nil {
		return UpstreamDetail{}, err
	}
	defer timelineRows.Close()
	for timelineRows.Next() {
		var recordedAt string
		if err := timelineRows.Scan(&recordedAt); err != nil {
			return UpstreamDetail{}, err
		}
		recordedTime, err := timeParse(recordedAt)
		if err != nil {
			return UpstreamDetail{}, err
		}
		slot := recordedTime.UTC().Truncate(bucketSize)
		if slot.Before(bucketStart) {
			continue
		}
		if slot.After(referenceTime.Truncate(bucketSize)) {
			continue
		}
		if _, ok := buckets[slot]; ok {
			buckets[slot]++
			continue
		}
		buckets[slot] = 1
	}
	if err := timelineRows.Err(); err != nil {
		return UpstreamDetail{}, err
	}
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		detail.Timeline = append(detail.Timeline, TimeCountItem{
			Time:  slot,
			Count: buckets[slot],
		})
	}
	return detail, nil
}

func (s *Store) upstreamModelCoverage(upstreamID string, limit int, since time.Time, modelFilter string) ([]string, string, error) {
	if limit <= 0 {
		limit = 5
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT model, COUNT(*) AS count
		FROM logs
		WHERE selected_upstream_id = ? AND model <> ''`+whereSQL+`
		GROUP BY model
		ORDER BY count DESC, model ASC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var models []string
	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, "", err
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var lastModel string
	lastModelArgs := append([]any{upstreamID}, whereArgs...)
	if err := s.db.QueryRow(`
		SELECT model
		FROM logs
		WHERE selected_upstream_id = ? AND model <> ''`+whereSQL+`
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT 1
	`, lastModelArgs...).Scan(&lastModel); err != nil && err != sql.ErrNoRows {
		return nil, "", err
	}

	return models, lastModel, nil
}

func (s *Store) upstreamRecentErrors(upstreamID string, limit int, since time.Time, modelFilter string) ([]string, error) {
	if limit <= 0 {
		limit = 3
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT error_text, status_code, endpoint
		FROM logs
		WHERE selected_upstream_id = ?
		  `+whereSQL+`
		  AND (status_code NOT BETWEEN 200 AND 299 OR error_text <> '')
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var (
			errorText  string
			statusCode int
			endpoint   string
		)
		if err := rows.Scan(&errorText, &statusCode, &endpoint); err != nil {
			return nil, err
		}
		switch {
		case strings.TrimSpace(errorText) != "":
			out = append(out, errorText)
		case strings.TrimSpace(endpoint) != "":
			out = append(out, fmt.Sprintf("%s HTTP %d", endpoint, statusCode))
		default:
			out = append(out, fmt.Sprintf("HTTP %d", statusCode))
		}
	}
	return out, rows.Err()
}

func (s *Store) upstreamRecentFailures(upstreamID string, limit int, since time.Time, modelFilter string) ([]UpstreamFailureRecord, error) {
	if limit <= 0 {
		limit = 3
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT trace_id, model, endpoint, status_code, recorded_at, error_text
		FROM logs
		WHERE selected_upstream_id = ?
		  `+whereSQL+`
		  AND (status_code NOT BETWEEN 200 AND 299 OR error_text <> '')
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UpstreamFailureRecord
	for rows.Next() {
		var (
			record     UpstreamFailureRecord
			recordedAt string
		)
		if err := rows.Scan(&record.TraceID, &record.Model, &record.Endpoint, &record.StatusCode, &recordedAt, &record.ErrorText); err != nil {
			return nil, err
		}
		record.RecordedAt, err = timeParse(recordedAt)
		if err != nil {
			return nil, err
		}
		record.Reason = classifyUpstreamFailure(record.StatusCode, record.ErrorText)
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) upstreamFailureReasons(upstreamID string, limit int, since time.Time, modelFilter string) ([]CountItem, error) {
	if limit <= 0 {
		limit = 5
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	rows, err := s.db.Query(`
		SELECT status_code, error_text
		FROM logs
		WHERE selected_upstream_id = ?
		  `+whereSQL+`
		  AND (status_code NOT BETWEEN 200 AND 299 OR error_text <> '')
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var (
			statusCode int
			errorText  string
		)
		if err := rows.Scan(&statusCode, &errorText); err != nil {
			return nil, err
		}
		counts[classifyUpstreamFailure(statusCode, errorText)]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := sortedCountItems(counts)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func classifyUpstreamFailure(statusCode int, errorText string) string {
	text := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case statusCode == 408 || statusCode == 504 || strings.Contains(text, "timeout") || strings.Contains(text, "timed out") || strings.Contains(text, "deadline exceeded") || strings.Contains(text, "context deadline exceeded"):
		return "timeout"
	case statusCode == 429 || strings.Contains(text, "rate limit") || strings.Contains(text, "too many requests"):
		return "rate_limited"
	case statusCode == 401 || statusCode == 403 || strings.Contains(text, "unauthorized") || strings.Contains(text, "forbidden") || strings.Contains(text, "invalid api key") || strings.Contains(text, "authentication"):
		return "auth_denied"
	case statusCode == 503 || strings.Contains(text, "overloaded") || strings.Contains(text, "overload") || strings.Contains(text, "capacity") || strings.Contains(text, "unavailable"):
		return "upstream_overloaded"
	case statusCode >= 500:
		return "upstream_error"
	case statusCode >= 400:
		return "request_rejected"
	case text != "":
		return "transport_error"
	default:
		return "unknown_failure"
	}
}

func buildUpstreamAnalyticsWhere(since time.Time, modelFilter string) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if !since.IsZero() {
		clauses = append(clauses, `recorded_at >= ?`)
		args = append(args, since.UTC().Format(timeLayout))
	}
	if model := strings.TrimSpace(modelFilter); model != "" {
		clauses = append(clauses, `LOWER(model) LIKE LOWER(?) ESCAPE '\'`)
		args = append(args, "%"+escapeLike(model)+"%")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

func sortedCountItems(counts map[string]int) []CountItem {
	items := make([]CountItem, 0, len(counts))
	for label, count := range counts {
		items = append(items, CountItem{Label: label, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Label < items[j].Label
	})
	return items
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
			is_stream INTEGER NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			session_source TEXT NOT NULL DEFAULT '',
			window_id TEXT NOT NULL DEFAULT '',
			client_request_id TEXT NOT NULL DEFAULT '',
			selected_upstream_id TEXT NOT NULL DEFAULT '',
			selected_upstream_base_url TEXT NOT NULL DEFAULT '',
			selected_upstream_provider_preset TEXT NOT NULL DEFAULT '',
			routing_policy TEXT NOT NULL DEFAULT '',
			routing_score REAL NOT NULL DEFAULT 0,
			routing_candidate_count INTEGER NOT NULL DEFAULT 0,
			routing_failure_reason TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_recorded_at ON logs(recorded_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_model_recorded_at ON logs(model, recorded_at DESC);`,
		`CREATE TABLE IF NOT EXISTS upstream_targets (
			id TEXT PRIMARY KEY,
			base_url TEXT NOT NULL,
			provider_preset TEXT NOT NULL,
			protocol_family TEXT NOT NULL,
			routing_profile TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			priority INTEGER NOT NULL,
			weight REAL NOT NULL,
			capacity_hint REAL NOT NULL,
			last_refresh_at TEXT NOT NULL DEFAULT '',
			last_refresh_status TEXT NOT NULL DEFAULT '',
			last_refresh_error TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS upstream_models (
			upstream_id TEXT NOT NULL,
			model TEXT NOT NULL,
			source TEXT NOT NULL,
			seen_at TEXT NOT NULL,
			PRIMARY KEY (upstream_id, model)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_upstream_models_model ON upstream_models(model);`,
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
	if err := s.ensureColumn("logs", "session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "session_source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "window_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "client_request_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "selected_upstream_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "selected_upstream_base_url", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "selected_upstream_provider_preset", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_policy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_score", "REAL NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_candidate_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_failure_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	postColumnStmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_logs_trace_id ON logs(trace_id) WHERE trace_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_logs_session_id_recorded_at ON logs(session_id, recorded_at DESC) WHERE session_id <> '';`,
	}
	for _, stmt := range postColumnStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.backfillTraceIDs(); err != nil {
		return err
	}
	if err := s.backfillSemantics(); err != nil {
		return err
	}
	if err := s.backfillGrouping(); err != nil {
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
	return s.UpsertLogWithGrouping(path, header, GroupingInfo{})
}

func (s *Store) UpsertUpstreamTarget(record UpstreamTargetRecord) error {
	lastRefreshAt := ""
	if !record.LastRefreshAt.IsZero() {
		lastRefreshAt = record.LastRefreshAt.UTC().Format(timeLayout)
	}
	_, err := s.db.Exec(`
		INSERT INTO upstream_targets (
			id, base_url, provider_preset, protocol_family, routing_profile, enabled,
			priority, weight, capacity_hint, last_refresh_at, last_refresh_status, last_refresh_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			base_url=excluded.base_url,
			provider_preset=excluded.provider_preset,
			protocol_family=excluded.protocol_family,
			routing_profile=excluded.routing_profile,
			enabled=excluded.enabled,
			priority=excluded.priority,
			weight=excluded.weight,
			capacity_hint=excluded.capacity_hint,
			last_refresh_at=excluded.last_refresh_at,
			last_refresh_status=excluded.last_refresh_status,
			last_refresh_error=excluded.last_refresh_error
	`,
		record.ID,
		record.BaseURL,
		record.ProviderPreset,
		record.ProtocolFamily,
		record.RoutingProfile,
		boolToInt(record.Enabled),
		record.Priority,
		record.Weight,
		record.CapacityHint,
		lastRefreshAt,
		record.LastRefreshStatus,
		record.LastRefreshError,
	)
	return err
}

func (s *Store) ReplaceUpstreamModels(upstreamID string, records []UpstreamModelRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM upstream_models WHERE upstream_id = ?`, upstreamID); err != nil {
		return err
	}
	for _, record := range records {
		seenAt := record.SeenAt
		if seenAt.IsZero() {
			seenAt = time.Now().UTC()
		}
		if _, err := tx.Exec(`
			INSERT INTO upstream_models (upstream_id, model, source, seen_at)
			VALUES (?, ?, ?, ?)
		`,
			upstreamID,
			record.Model,
			record.Source,
			seenAt.UTC().Format(timeLayout),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) UpsertLogWithGrouping(path string, header recordfile.RecordHeader, grouping GroupingInfo) error {
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
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			is_stream=excluded.is_stream,
			session_id=excluded.session_id,
			session_source=excluded.session_source,
			window_id=excluded.window_id,
			client_request_id=excluded.client_request_id,
			selected_upstream_id=excluded.selected_upstream_id,
			selected_upstream_base_url=excluded.selected_upstream_base_url,
			selected_upstream_provider_preset=excluded.selected_upstream_provider_preset,
			routing_policy=excluded.routing_policy,
			routing_score=excluded.routing_score,
			routing_candidate_count=excluded.routing_candidate_count,
			routing_failure_reason=excluded.routing_failure_reason
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
		grouping.SessionID,
		grouping.SessionSource,
		grouping.WindowID,
		grouping.ClientRequestID,
		header.Meta.SelectedUpstreamID,
		header.Meta.SelectedUpstreamBaseURL,
		header.Meta.SelectedUpstreamProviderPreset,
		header.Meta.RoutingPolicy,
		header.Meta.RoutingScore,
		header.Meta.RoutingCandidateCount,
		header.Meta.RoutingFailureReason,
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

		grouping, err := ExtractGroupingInfo(content, parsed)
		if err != nil {
			return fmt.Errorf("extract grouping %s: %w", path, err)
		}

		return s.UpsertLogWithGrouping(path, parsed.Header, grouping)
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
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
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

func (s *Store) ListPage(page int, pageSize int, filter ListFilter) (ListPageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	whereSQL, whereArgs := buildLogFilterClause(filter, "")
	var total int
	countSQL := `SELECT COUNT(*) FROM logs`
	if whereSQL != "" {
		countSQL += " WHERE " + whereSQL
	}
	if err := s.db.QueryRow(countSQL, whereArgs...).Scan(&total); err != nil {
		return ListPageResult{}, err
	}

	offset := (page - 1) * pageSize
	queryArgs := append([]any{}, whereArgs...)
	queryArgs = append(queryArgs, pageSize, offset)
	listSQL := `
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		FROM logs
	`
	if whereSQL != "" {
		listSQL += " WHERE " + whereSQL
	}
	listSQL += `
		ORDER BY recorded_at DESC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.Query(listSQL, queryArgs...)
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
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		FROM logs
		WHERE trace_id = ?
	`, traceID)
	return scanEntry(row)
}

func (s *Store) ListSessionPage(page int, pageSize int, filter ListFilter) (SessionPageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	whereSQL, whereArgs := buildLogFilterClause(filter, "s")
	sessionWhere := `s.session_id <> ''`
	if whereSQL != "" {
		sessionWhere += " AND " + whereSQL
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT session_id FROM logs s WHERE `+sessionWhere+` GROUP BY session_id)`, whereArgs...).Scan(&total); err != nil {
		return SessionPageResult{}, err
	}

	offset := (page - 1) * pageSize
	queryArgs := append([]any{}, whereArgs...)
	queryArgs = append(queryArgs, pageSize, offset)
	listSQL := `
		SELECT
			s.session_id,
			MIN(s.session_source) AS session_source,
			COUNT(*) AS request_count,
			MIN(s.recorded_at) AS first_seen,
			MAX(s.recorded_at) AS last_seen,
			COALESCE((
				SELECT model FROM logs l2
				WHERE l2.session_id = s.session_id
				ORDER BY l2.recorded_at DESC, l2.trace_id DESC
				LIMIT 1
			), '') AS last_model,
			COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN s.provider <> '' THEN s.provider END), '') AS providers,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN s.status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END AS success_rate,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.total_tokens ELSE 0 END), 0) AS total_tokens,
			COALESCE(AVG(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.ttft_ms END), 0) AS avg_ttft,
			COALESCE(SUM(s.duration_ms), 0) AS total_duration,
			COALESCE(SUM(CASE WHEN s.is_stream = 1 THEN 1 ELSE 0 END), 0) AS stream_count
		FROM logs s
		WHERE ` + sessionWhere + `
		GROUP BY s.session_id
		ORDER BY MAX(s.recorded_at) DESC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.Query(listSQL, queryArgs...)
	if err != nil {
		return SessionPageResult{}, err
	}
	defer rows.Close()

	result := SessionPageResult{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
	for rows.Next() {
		summary, err := scanSessionSummary(rows)
		if err != nil {
			return SessionPageResult{}, err
		}
		result.Items = append(result.Items, summary)
	}
	if err := rows.Err(); err != nil {
		return SessionPageResult{}, err
	}
	if total == 0 {
		return result, nil
	}
	result.TotalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	return result, nil
}

func (s *Store) GetSession(sessionID string) (SessionSummary, error) {
	row := s.db.QueryRow(`
		SELECT
			s.session_id,
			MIN(s.session_source) AS session_source,
			COUNT(*) AS request_count,
			MIN(s.recorded_at) AS first_seen,
			MAX(s.recorded_at) AS last_seen,
			COALESCE((
				SELECT model FROM logs l2
				WHERE l2.session_id = s.session_id
				ORDER BY l2.recorded_at DESC, l2.trace_id DESC
				LIMIT 1
			), '') AS last_model,
			COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN s.provider <> '' THEN s.provider END), '') AS providers,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN s.status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END AS success_rate,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.total_tokens ELSE 0 END), 0) AS total_tokens,
			COALESCE(AVG(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.ttft_ms END), 0) AS avg_ttft,
			COALESCE(SUM(s.duration_ms), 0) AS total_duration,
			COALESCE(SUM(CASE WHEN s.is_stream = 1 THEN 1 ELSE 0 END), 0) AS stream_count
		FROM logs s
		WHERE s.session_id = ?
		GROUP BY s.session_id
	`, sessionID)
	return scanSessionSummary(row)
}

func (s *Store) ListTracesBySession(sessionID string) ([]LogEntry, error) {
	rows, err := s.db.Query(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		FROM logs
		WHERE session_id = ?
		ORDER BY recorded_at DESC, trace_id DESC
	`, sessionID)
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
		entry        LogEntry
		recordedAt   string
		errorText    string
		cached       int
		isStream     int
		routingScore float64
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
		&entry.SessionID,
		&entry.SessionSource,
		&entry.WindowID,
		&entry.ClientRequestID,
		&entry.Header.Meta.SelectedUpstreamID,
		&entry.Header.Meta.SelectedUpstreamBaseURL,
		&entry.Header.Meta.SelectedUpstreamProviderPreset,
		&entry.Header.Meta.RoutingPolicy,
		&routingScore,
		&entry.Header.Meta.RoutingCandidateCount,
		&entry.Header.Meta.RoutingFailureReason,
	)
	if err != nil {
		return LogEntry{}, err
	}

	entry.Header.Meta.Time, err = timeParse(recordedAt)
	if err != nil {
		return LogEntry{}, err
	}
	entry.Header.Meta.Error = errorText
	entry.Header.Meta.RoutingScore = routingScore
	entry.Header.Layout.IsStream = isStream == 1
	if cached > 0 {
		entry.Header.Usage.PromptTokenDetails = &recordfile.PromptTokenDetails{CachedTokens: cached}
	}

	return entry, nil
}

func scanSessionSummary(scanner interface {
	Scan(dest ...any) error
}) (SessionSummary, error) {
	var (
		summary      SessionSummary
		firstSeen    string
		lastSeen     string
		providersCSV string
		avgTTFT      float64
	)
	err := scanner.Scan(
		&summary.SessionID,
		&summary.SessionSource,
		&summary.RequestCount,
		&firstSeen,
		&lastSeen,
		&summary.LastModel,
		&providersCSV,
		&summary.SuccessRequest,
		&summary.FailedRequest,
		&summary.SuccessRate,
		&summary.TotalTokens,
		&avgTTFT,
		&summary.TotalDuration,
		&summary.StreamCount,
	)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.FirstSeen, err = timeParse(firstSeen)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.LastSeen, err = timeParse(lastSeen)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.AvgTTFT = int(math.Round(avgTTFT))
	summary.Providers = splitProviders(providersCSV)
	return summary, nil
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

func ExtractGroupingInfo(content []byte, parsed *recordfile.ParsedPrelude) (GroupingInfo, error) {
	reqFull, _, _, _ := recordfile.ExtractSections(content, parsed)
	return extractGroupingInfoFromRequest(reqFull)
}

func extractGroupingInfoFromRequest(reqFull []byte) (GroupingInfo, error) {
	headers := parseRawRequestHeaders(reqFull)
	info := GroupingInfo{
		WindowID:        strings.TrimSpace(headers.Get("X-Codex-Window-Id")),
		ClientRequestID: strings.TrimSpace(headers.Get("X-Client-Request-Id")),
	}
	if sessionID := strings.TrimSpace(headers.Get("Session_id")); sessionID != "" {
		info.SessionID = sessionID
		info.SessionSource = "header.session_id"
		return info, nil
	}

	if rawMetadata := strings.TrimSpace(headers.Get("X-Codex-Turn-Metadata")); rawMetadata != "" {
		var metadata struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(rawMetadata), &metadata); err == nil && strings.TrimSpace(metadata.SessionID) != "" {
			info.SessionID = strings.TrimSpace(metadata.SessionID)
			info.SessionSource = "header.x_codex_turn_metadata.session_id"
			return info, nil
		}
	}

	if info.WindowID != "" {
		info.SessionID = normalizeWindowSessionID(info.WindowID)
		if info.SessionID != "" {
			info.SessionSource = "header.x_codex_window_id"
			return info, nil
		}
	}

	info.SessionSource = "none"
	return info, nil
}

func parseRawRequestHeaders(reqFull []byte) textproto.MIMEHeader {
	headers := make(textproto.MIMEHeader)
	lines := strings.Split(string(reqFull), "\r\n")
	for idx, line := range lines {
		if idx == 0 || line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		headers.Add(textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name)), strings.TrimSpace(value))
	}
	return headers
}

func normalizeWindowSessionID(windowID string) string {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return ""
	}
	sessionID, _, found := strings.Cut(windowID, ":")
	if !found {
		return windowID
	}
	return strings.TrimSpace(sessionID)
}

func splitProviders(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	seen := map[string]struct{}{}
	var providers []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		providers = append(providers, part)
	}
	sort.Strings(providers)
	return providers
}

func buildLogFilterClause(filter ListFilter, alias string) (string, []any) {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}

	var (
		clauses []string
		args    []any
	)

	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		clauses = append(clauses, `LOWER(`+column("provider")+`) = LOWER(?)`)
		args = append(args, provider)
	}
	if model := strings.TrimSpace(filter.Model); model != "" {
		clauses = append(clauses, `LOWER(`+column("model")+`) LIKE LOWER(?)`)
		args = append(args, "%"+escapeLike(model)+"%")
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		pattern := "%" + escapeLike(query) + "%"
		clauses = append(clauses, `(
			LOWER(`+column("session_id")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("trace_id")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("model")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("provider")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("endpoint")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("url")+`) LIKE LOWER(?) ESCAPE '\'
		)`)
		for range 6 {
			args = append(args, pattern)
		}
	}

	return strings.Join(clauses, " AND "), args
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func (s *Store) backfillGrouping() error {
	rows, err := s.db.Query(`SELECT path FROM logs WHERE session_source = ''`)
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
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		parsed, err := recordfile.ParsePrelude(content)
		if err != nil {
			if shouldSkipIncompleteRecord(content, err) {
				continue
			}
			return err
		}
		grouping, err := ExtractGroupingInfo(content, parsed)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(
			`UPDATE logs SET session_id = ?, session_source = ?, window_id = ?, client_request_id = ? WHERE path = ?`,
			grouping.SessionID,
			grouping.SessionSource,
			grouping.WindowID,
			grouping.ClientRequestID,
			path,
		); err != nil {
			return err
		}
	}
	return nil
}
