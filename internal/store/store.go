package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/datasetexample"
	"github.com/kingfs/llm-tracelab/ent/dao/evalrun"
	"github.com/kingfs/llm-tracelab/ent/dao/predicate"
	"github.com/kingfs/llm-tracelab/ent/dao/tracelog"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreammodel"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamtarget"
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
	client    *dao.Client
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

type DatasetRecord struct {
	ID           string
	Name         string
	Description  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExampleCount int
}

type DatasetExampleRecord struct {
	DatasetID  string
	TraceID    string
	Position   int
	AddedAt    time.Time
	SourceType string
	SourceID   string
	Note       string
	Trace      LogEntry
}

type EvalRunRecord struct {
	ID           string
	DatasetID    string
	SourceType   string
	SourceID     string
	EvaluatorSet string
	CreatedAt    time.Time
	CompletedAt  time.Time
	TraceCount   int
	ScoreCount   int
	PassCount    int
	FailCount    int
}

type ScoreRecord struct {
	ID           string
	TraceID      string
	SessionID    string
	DatasetID    string
	EvalRunID    string
	EvaluatorKey string
	Value        float64
	Status       string
	Label        string
	Explanation  string
	CreatedAt    time.Time
}

type ExperimentRunRecord struct {
	ID                  string
	Name                string
	Description         string
	BaselineEvalRunID   string
	CandidateEvalRunID  string
	CreatedAt           time.Time
	BaselineScoreCount  int
	CandidateScoreCount int
	BaselinePassRate    float64
	CandidatePassRate   float64
	PassRateDelta       float64
	MatchedScoreCount   int
	ImprovementCount    int
	RegressionCount     int
}

type ScoreFilter struct {
	TraceID   string
	SessionID string
	DatasetID string
	EvalRunID string
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
			enabled       any
			lastRefreshAt any
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
		record.Enabled = boolValue(enabled)
		if !isEmptyTimeValue(lastRefreshAt) {
			record.LastRefreshAt, err = timeParseValue(lastRefreshAt)
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
			seenAt any
		)
		if err := rows.Scan(&record.UpstreamID, &record.Model, &record.Source, &seenAt); err != nil {
			return nil, err
		}
		record.SeenAt, err = timeParseValue(seenAt)
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
		referenceTime = latestTime
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
		referenceTime = latestTime
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
	return NewWithDatabase(outputDir, "sqlite", filepath.Join(outputDir, "trace_index.sqlite3"), 4, 4)
}

func NewWithDatabase(outputDir string, driver string, dsn string, maxOpenConns int, maxIdleConns int) (*Store, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" {
		return nil, fmt.Errorf("store driver %q is not supported yet", driver)
	}
	dbPath := dsn
	if strings.TrimSpace(dbPath) == "" {
		dbPath = filepath.Join(outputDir, "llm_tracelab.sqlite3")
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	if maxOpenConns > 0 {
		db.SetMaxOpenConns(maxOpenConns)
	}
	if maxIdleConns > 0 {
		db.SetMaxIdleConns(maxIdleConns)
	}

	st := &Store{
		db:        db,
		client:    dao.NewClient(dao.Driver(entsql.OpenDB(dialect.SQLite, db))),
		outputDir: outputDir,
		dbPath:    dbPath,
	}
	if err := st.initSchema(); err != nil {
		_ = st.Close()
		return nil, err
	}

	return st, nil
}

func sqliteDSN(dbPath string) string {
	values := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"busy_timeout(5000)",
		"wal_autocheckpoint(1000)",
	} {
		values.Add("_pragma", pragma)
	}
	u := url.URL{
		Scheme:   "file",
		Path:     dbPath,
		RawQuery: values.Encode(),
	}
	return u.String()
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.client != nil {
		return s.client.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
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
			recorded_at datetime NOT NULL,
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
			is_stream bool NOT NULL DEFAULT false,
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
		`CREATE TABLE IF NOT EXISTS upstream_targets (
			id TEXT PRIMARY KEY,
			base_url TEXT NOT NULL,
			provider_preset TEXT NOT NULL,
			protocol_family TEXT NOT NULL,
			routing_profile TEXT NOT NULL,
			enabled bool NOT NULL DEFAULT true,
			priority INTEGER NOT NULL,
			weight REAL NOT NULL,
			capacity_hint REAL NOT NULL,
			last_refresh_at datetime NULL,
			last_refresh_status TEXT NOT NULL DEFAULT '',
			last_refresh_error TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS upstream_models (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			upstream_id TEXT NOT NULL,
			model TEXT NOT NULL,
			source TEXT NOT NULL,
			seen_at datetime NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS upstreammodel_upstream_id_model ON upstream_models(upstream_id, model);`,
		`CREATE INDEX IF NOT EXISTS idx_upstream_models_model ON upstream_models(model);`,
		`CREATE TABLE IF NOT EXISTS datasets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL,
			updated_at datetime NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS dataset_examples (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			dataset_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			added_at datetime NOT NULL,
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS datasetexample_dataset_id_trace_id ON dataset_examples(dataset_id, trace_id);`,
		`CREATE INDEX IF NOT EXISTS idx_dataset_examples_dataset_position ON dataset_examples(dataset_id, position ASC);`,
		`CREATE TABLE IF NOT EXISTS eval_runs (
			id TEXT PRIMARY KEY,
			dataset_id TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			evaluator_set TEXT NOT NULL,
			created_at datetime NOT NULL,
			completed_at datetime NOT NULL,
			trace_count INTEGER NOT NULL DEFAULT 0,
			score_count INTEGER NOT NULL DEFAULT 0,
			pass_count INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS scores (
			id TEXT PRIMARY KEY,
			trace_id TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			dataset_id TEXT NOT NULL DEFAULT '',
			eval_run_id TEXT NOT NULL DEFAULT '',
			evaluator_key TEXT NOT NULL,
			value REAL NOT NULL,
			status TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			explanation TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_scores_trace_id ON scores(trace_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_scores_session_id ON scores(session_id, created_at DESC) WHERE session_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_scores_dataset_id ON scores(dataset_id, created_at DESC) WHERE dataset_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_scores_eval_run_id ON scores(eval_run_id, created_at DESC) WHERE eval_run_id <> '';`,
		`CREATE TABLE IF NOT EXISTS experiment_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			baseline_eval_run_id TEXT NOT NULL,
			candidate_eval_run_id TEXT NOT NULL,
			created_at datetime NOT NULL,
			baseline_score_count INTEGER NOT NULL DEFAULT 0,
			candidate_score_count INTEGER NOT NULL DEFAULT 0,
			baseline_pass_rate REAL NOT NULL DEFAULT 0,
			candidate_pass_rate REAL NOT NULL DEFAULT 0,
			pass_rate_delta REAL NOT NULL DEFAULT 0,
			matched_score_count INTEGER NOT NULL DEFAULT 0,
			improvement_count INTEGER NOT NULL DEFAULT 0,
			regression_count INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_experiment_runs_created_at ON experiment_runs(created_at DESC, id DESC);`,
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
	if err := s.backfillTraceIDs(); err != nil {
		return err
	}
	if err := s.ensureLogsDatetimeTable(); err != nil {
		return err
	}
	postColumnStmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS logs_trace_id_key ON logs(trace_id);`,
		`CREATE INDEX IF NOT EXISTS tracelog_recorded_at ON logs(recorded_at);`,
		`CREATE INDEX IF NOT EXISTS tracelog_model_recorded_at ON logs(model, recorded_at);`,
		`CREATE INDEX IF NOT EXISTS tracelog_session_id_recorded_at ON logs(session_id, recorded_at);`,
		`CREATE INDEX IF NOT EXISTS tracelog_request_id ON logs(request_id);`,
	}
	for _, stmt := range postColumnStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureEntCompatibleTables(); err != nil {
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

func (s *Store) ensureEntCompatibleTables() error {
	if err := s.ensureAutoIDTable(
		"upstream_models",
		`CREATE TABLE upstream_models (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			upstream_id TEXT NOT NULL,
			model TEXT NOT NULL,
			source TEXT NOT NULL,
			seen_at datetime NOT NULL
		)`,
		`INSERT INTO upstream_models (upstream_id, model, source, seen_at)
		 SELECT upstream_id, model, source, seen_at FROM upstream_models_old`,
		[]string{
			`CREATE UNIQUE INDEX IF NOT EXISTS upstreammodel_upstream_id_model ON upstream_models(upstream_id, model)`,
			`CREATE INDEX IF NOT EXISTS idx_upstream_models_model ON upstream_models(model)`,
		},
	); err != nil {
		return err
	}
	return s.ensureAutoIDTable(
		"dataset_examples",
		`CREATE TABLE dataset_examples (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			dataset_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			added_at TEXT NOT NULL,
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO dataset_examples (dataset_id, trace_id, position, added_at, source_type, source_id, note)
		 SELECT dataset_id, trace_id, position, added_at, source_type, source_id, note FROM dataset_examples_old`,
		[]string{
			`CREATE UNIQUE INDEX IF NOT EXISTS datasetexample_dataset_id_trace_id ON dataset_examples(dataset_id, trace_id)`,
			`CREATE INDEX IF NOT EXISTS idx_dataset_examples_dataset_position ON dataset_examples(dataset_id, position ASC)`,
		},
	)
}

func (s *Store) ensureLogsDatetimeTable() error {
	recordedAtType, err := s.columnType("logs", "recorded_at")
	if err != nil {
		return err
	}
	isStreamType, err := s.columnType("logs", "is_stream")
	if err != nil {
		return err
	}
	if strings.EqualFold(recordedAtType, "datetime") && strings.EqualFold(isStreamType, "bool") {
		return nil
	}

	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_logs_recorded_at`,
		`DROP INDEX IF EXISTS idx_logs_model_recorded_at`,
		`DROP INDEX IF EXISTS idx_logs_trace_id`,
		`DROP INDEX IF EXISTS idx_logs_session_id_recorded_at`,
		`DROP INDEX IF EXISTS logs_trace_id_key`,
		`DROP INDEX IF EXISTS tracelog_recorded_at`,
		`DROP INDEX IF EXISTS tracelog_model_recorded_at`,
		`DROP INDEX IF EXISTS tracelog_session_id_recorded_at`,
		`DROP INDEX IF EXISTS tracelog_request_id`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE logs RENAME TO logs_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE logs (
		path TEXT PRIMARY KEY,
		trace_id TEXT NOT NULL DEFAULT '',
		mod_time_ns INTEGER NOT NULL,
		file_size INTEGER NOT NULL,
		version TEXT NOT NULL,
		request_id TEXT NOT NULL DEFAULT '',
		recorded_at datetime NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		operation TEXT NOT NULL DEFAULT '',
		endpoint TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		ttft_ms INTEGER NOT NULL DEFAULT 0,
		client_ip TEXT NOT NULL DEFAULT '',
		content_length INTEGER NOT NULL DEFAULT 0,
		error_text TEXT NOT NULL DEFAULT '',
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		req_header_len INTEGER NOT NULL DEFAULT 0,
		req_body_len INTEGER NOT NULL DEFAULT 0,
		res_header_len INTEGER NOT NULL DEFAULT 0,
		res_body_len INTEGER NOT NULL DEFAULT 0,
		is_stream bool NOT NULL DEFAULT false,
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
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO logs (
		path, trace_id, mod_time_ns, file_size, version, request_id, recorded_at, model, provider, operation, endpoint, url, method,
		status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
		prompt_tokens, completion_tokens, total_tokens, cached_tokens,
		req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
		session_id, session_source, window_id, client_request_id,
		selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
		routing_policy, routing_score, routing_candidate_count, routing_failure_reason
	)
	SELECT
		path, trace_id, mod_time_ns, file_size, version, request_id,
		CASE WHEN recorded_at IS NULL OR TRIM(CAST(recorded_at AS text)) = '' THEN '1970-01-01T00:00:00Z' ELSE recorded_at END,
		model, provider, operation, endpoint, url, method,
		status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
		prompt_tokens, completion_tokens, total_tokens, cached_tokens,
		req_header_len, req_body_len, res_header_len, res_body_len,
		CASE WHEN is_stream IN (1, '1', 'true', 'TRUE') THEN true ELSE false END,
		session_id, session_source, window_id, client_request_id,
		selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
		routing_policy, routing_score, routing_candidate_count, routing_failure_reason
	FROM logs_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE logs_old`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ensureAutoIDTable(table string, createSQL string, copySQL string, indexes []string) error {
	hasID, err := s.hasColumn(table, "id")
	if err != nil {
		return err
	}
	if hasID {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE ` + table + ` RENAME TO ` + table + `_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(createSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(copySQL); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE ` + table + `_old`); err != nil {
		return err
	}
	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ensureColumn(table string, column string, definition string) error {
	exists, err := s.hasColumn(table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

func (s *Store) hasColumn(table string, column string) (bool, error) {
	typ, err := s.columnType(table, column)
	if err != nil {
		return false, err
	}
	return typ != "", nil
}

func (s *Store) columnType(table string, column string) (string, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return "", err
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
			return "", err
		}
		if name == column {
			return typ, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", nil
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
	create := s.client.UpstreamTarget.Create().
		SetID(record.ID).
		SetBaseURL(record.BaseURL).
		SetProviderPreset(record.ProviderPreset).
		SetProtocolFamily(record.ProtocolFamily).
		SetRoutingProfile(record.RoutingProfile).
		SetEnabled(record.Enabled).
		SetPriority(record.Priority).
		SetWeight(record.Weight).
		SetCapacityHint(record.CapacityHint).
		SetLastRefreshStatus(record.LastRefreshStatus).
		SetLastRefreshError(record.LastRefreshError)
	if !record.LastRefreshAt.IsZero() {
		create.SetLastRefreshAt(record.LastRefreshAt.UTC())
	}
	return create.
		OnConflictColumns(upstreamtarget.FieldID).
		UpdateNewValues().
		Exec(context.Background())
}

func (s *Store) CreateDataset(name string, description string) (DatasetRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return DatasetRecord{}, fmt.Errorf("dataset name is required")
	}
	now := time.Now().UTC()
	record := DatasetRecord{
		ID:          uuid.NewString(),
		Name:        name,
		Description: strings.TrimSpace(description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.client.Dataset.Create().
		SetID(record.ID).
		SetName(record.Name).
		SetDescription(record.Description).
		SetCreatedAt(record.CreatedAt).
		SetUpdatedAt(record.UpdatedAt).
		Exec(context.Background()); err != nil {
		return DatasetRecord{}, err
	}
	return record, nil
}

func (s *Store) ListDatasets() ([]DatasetRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			d.id,
			d.name,
			d.description,
			d.created_at,
			d.updated_at,
			COUNT(de.trace_id) AS example_count
		FROM datasets d
		LEFT JOIN dataset_examples de ON de.dataset_id = d.id
		GROUP BY d.id, d.name, d.description, d.created_at, d.updated_at
		ORDER BY d.updated_at DESC, d.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DatasetRecord
	for rows.Next() {
		record, err := scanDatasetRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) GetDataset(datasetID string) (DatasetRecord, error) {
	row := s.db.QueryRow(`
		SELECT
			d.id,
			d.name,
			d.description,
			d.created_at,
			d.updated_at,
			COUNT(de.trace_id) AS example_count
		FROM datasets d
		LEFT JOIN dataset_examples de ON de.dataset_id = d.id
		WHERE d.id = ?
		GROUP BY d.id, d.name, d.description, d.created_at, d.updated_at
	`, datasetID)
	return scanDatasetRecord(row)
}

func (s *Store) AppendDatasetExamples(datasetID string, traceIDs []string, sourceType string, sourceID string, note string) (int, int, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return 0, 0, fmt.Errorf("dataset id is required")
	}
	if _, err := s.GetDataset(datasetID); err != nil {
		return 0, 0, err
	}

	seen := map[string]struct{}{}
	ordered := make([]string, 0, len(traceIDs))
	for _, traceID := range traceIDs {
		traceID = strings.TrimSpace(traceID)
		if traceID == "" {
			continue
		}
		if _, ok := seen[traceID]; ok {
			continue
		}
		seen[traceID] = struct{}{}
		ordered = append(ordered, traceID)
	}
	if len(ordered) == 0 {
		return 0, 0, nil
	}

	ctx := context.Background()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	nextPosition := 0
	existingCount, err := tx.DatasetExample.Query().
		Where(datasetexample.DatasetIDEQ(datasetID)).
		Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	if existingCount > 0 {
		nextPosition, err = tx.DatasetExample.Query().
			Where(datasetexample.DatasetIDEQ(datasetID)).
			Aggregate(dao.Max(datasetexample.FieldPosition)).
			Int(ctx)
		if err != nil {
			return 0, 0, err
		}
	}
	now := time.Now().UTC()
	added := 0
	skipped := 0
	creates := make([]*dao.DatasetExampleCreate, 0, len(ordered))
	for _, traceID := range ordered {
		if _, err := s.GetByID(traceID); err != nil {
			return 0, 0, err
		}
		exists, err := tx.DatasetExample.Query().
			Where(datasetexample.DatasetIDEQ(datasetID), datasetexample.TraceIDEQ(traceID)).
			Count(ctx)
		if err != nil {
			return 0, 0, err
		}
		if exists > 0 {
			skipped++
			continue
		}
		nextPosition++
		added++
		creates = append(creates, tx.DatasetExample.Create().
			SetDatasetID(datasetID).
			SetTraceID(traceID).
			SetPosition(nextPosition).
			SetAddedAt(now).
			SetSourceType(strings.TrimSpace(sourceType)).
			SetSourceID(strings.TrimSpace(sourceID)).
			SetNote(strings.TrimSpace(note)))
	}
	if len(creates) > 0 {
		if err := tx.DatasetExample.CreateBulk(creates...).
			OnConflictColumns(datasetexample.FieldDatasetID, datasetexample.FieldTraceID).
			DoNothing().
			Exec(ctx); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Dataset.UpdateOneID(datasetID).SetUpdatedAt(now).Exec(ctx); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return added, skipped, nil
}

func (s *Store) GetDatasetExamples(datasetID string) ([]DatasetExampleRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			de.dataset_id,
			de.trace_id,
			de.position,
			de.added_at,
			de.source_type,
			de.source_id,
			de.note,
			l.trace_id, l.path, l.version, l.request_id, l.recorded_at, l.model, l.provider, l.operation, l.endpoint, l.url, l.method, l.status_code,
			l.duration_ms, l.ttft_ms, l.client_ip, l.content_length, l.error_text,
			l.prompt_tokens, l.completion_tokens, l.total_tokens, l.cached_tokens,
			l.req_header_len, l.req_body_len, l.res_header_len, l.res_body_len, l.is_stream,
			l.session_id, l.session_source, l.window_id, l.client_request_id,
			l.selected_upstream_id, l.selected_upstream_base_url, l.selected_upstream_provider_preset,
			l.routing_policy, l.routing_score, l.routing_candidate_count, l.routing_failure_reason
		FROM dataset_examples de
		JOIN logs l ON l.trace_id = de.trace_id
		WHERE de.dataset_id = ?
		ORDER BY de.position ASC, de.trace_id ASC
	`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DatasetExampleRecord
	for rows.Next() {
		record, err := scanDatasetExampleRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) CreateEvalRun(datasetID string, sourceType string, sourceID string, evaluatorSet string, traceCount int) (EvalRunRecord, error) {
	evaluatorSet = strings.TrimSpace(evaluatorSet)
	if evaluatorSet == "" {
		return EvalRunRecord{}, fmt.Errorf("evaluator set is required")
	}
	now := time.Now().UTC()
	record := EvalRunRecord{
		ID:           uuid.NewString(),
		DatasetID:    strings.TrimSpace(datasetID),
		SourceType:   strings.TrimSpace(sourceType),
		SourceID:     strings.TrimSpace(sourceID),
		EvaluatorSet: evaluatorSet,
		CreatedAt:    now,
		CompletedAt:  now,
		TraceCount:   traceCount,
	}
	if err := s.client.EvalRun.Create().
		SetID(record.ID).
		SetDatasetID(record.DatasetID).
		SetSourceType(record.SourceType).
		SetSourceID(record.SourceID).
		SetEvaluatorSet(record.EvaluatorSet).
		SetCreatedAt(record.CreatedAt).
		SetCompletedAt(record.CompletedAt).
		SetTraceCount(record.TraceCount).
		SetScoreCount(0).
		SetPassCount(0).
		SetFailCount(0).
		Exec(context.Background()); err != nil {
		return EvalRunRecord{}, err
	}
	return record, nil
}

func (s *Store) FinalizeEvalRun(evalRunID string, scoreCount int, passCount int, failCount int) error {
	_, err := s.client.EvalRun.Update().
		Where(evalrun.IDEQ(evalRunID)).
		SetCompletedAt(time.Now().UTC()).
		SetScoreCount(scoreCount).
		SetPassCount(passCount).
		SetFailCount(failCount).
		Save(context.Background())
	return err
}

func (s *Store) AddScore(record ScoreRecord) (ScoreRecord, error) {
	if strings.TrimSpace(record.TraceID) == "" {
		return ScoreRecord{}, fmt.Errorf("trace id is required")
	}
	if strings.TrimSpace(record.EvaluatorKey) == "" {
		return ScoreRecord{}, fmt.Errorf("evaluator key is required")
	}
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	record.CreatedAt = now
	if err := s.client.Score.Create().
		SetID(record.ID).
		SetTraceID(record.TraceID).
		SetSessionID(record.SessionID).
		SetDatasetID(record.DatasetID).
		SetEvalRunID(record.EvalRunID).
		SetEvaluatorKey(record.EvaluatorKey).
		SetValue(record.Value).
		SetStatus(record.Status).
		SetLabel(record.Label).
		SetExplanation(record.Explanation).
		SetCreatedAt(record.CreatedAt).
		Exec(context.Background()); err != nil {
		return ScoreRecord{}, err
	}
	return record, nil
}

func (s *Store) GetEvalRun(evalRunID string) (EvalRunRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, dataset_id, source_type, source_id, evaluator_set, created_at, completed_at,
		       trace_count, score_count, pass_count, fail_count
		FROM eval_runs
		WHERE id = ?
	`, evalRunID)
	return scanEvalRunRecord(row)
}

func (s *Store) ListEvalRuns(limit int) ([]EvalRunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, dataset_id, source_type, source_id, evaluator_set, created_at, completed_at,
		       trace_count, score_count, pass_count, fail_count
		FROM eval_runs
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvalRunRecord
	for rows.Next() {
		record, err := scanEvalRunRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ListScores(filter ScoreFilter, limit int) ([]ScoreRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	whereParts := []string{}
	args := []any{}
	if traceID := strings.TrimSpace(filter.TraceID); traceID != "" {
		whereParts = append(whereParts, "trace_id = ?")
		args = append(args, traceID)
	}
	if sessionID := strings.TrimSpace(filter.SessionID); sessionID != "" {
		whereParts = append(whereParts, "session_id = ?")
		args = append(args, sessionID)
	}
	if datasetID := strings.TrimSpace(filter.DatasetID); datasetID != "" {
		whereParts = append(whereParts, "dataset_id = ?")
		args = append(args, datasetID)
	}
	if evalRunID := strings.TrimSpace(filter.EvalRunID); evalRunID != "" {
		whereParts = append(whereParts, "eval_run_id = ?")
		args = append(args, evalRunID)
	}
	query := `
		SELECT id, trace_id, session_id, dataset_id, eval_run_id, evaluator_key, value, status, label, explanation, created_at
		FROM scores
	`
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScoreRecord
	for rows.Next() {
		record, err := scanScoreRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) CreateExperimentRun(record ExperimentRunRecord) (ExperimentRunRecord, error) {
	if strings.TrimSpace(record.BaselineEvalRunID) == "" {
		return ExperimentRunRecord{}, fmt.Errorf("baseline eval run id is required")
	}
	if strings.TrimSpace(record.CandidateEvalRunID) == "" {
		return ExperimentRunRecord{}, fmt.Errorf("candidate eval run id is required")
	}
	record.ID = uuid.NewString()
	record.CreatedAt = time.Now().UTC()
	record.Name = strings.TrimSpace(record.Name)
	record.Description = strings.TrimSpace(record.Description)
	record.BaselineEvalRunID = strings.TrimSpace(record.BaselineEvalRunID)
	record.CandidateEvalRunID = strings.TrimSpace(record.CandidateEvalRunID)
	if err := s.client.ExperimentRun.Create().
		SetID(record.ID).
		SetName(record.Name).
		SetDescription(record.Description).
		SetBaselineEvalRunID(record.BaselineEvalRunID).
		SetCandidateEvalRunID(record.CandidateEvalRunID).
		SetCreatedAt(record.CreatedAt).
		SetBaselineScoreCount(record.BaselineScoreCount).
		SetCandidateScoreCount(record.CandidateScoreCount).
		SetBaselinePassRate(record.BaselinePassRate).
		SetCandidatePassRate(record.CandidatePassRate).
		SetPassRateDelta(record.PassRateDelta).
		SetMatchedScoreCount(record.MatchedScoreCount).
		SetImprovementCount(record.ImprovementCount).
		SetRegressionCount(record.RegressionCount).
		Exec(context.Background()); err != nil {
		return ExperimentRunRecord{}, err
	}
	return record, nil
}

func (s *Store) GetExperimentRun(experimentRunID string) (ExperimentRunRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, name, description, baseline_eval_run_id, candidate_eval_run_id, created_at,
		       baseline_score_count, candidate_score_count, baseline_pass_rate, candidate_pass_rate,
		       pass_rate_delta, matched_score_count, improvement_count, regression_count
		FROM experiment_runs
		WHERE id = ?
	`, experimentRunID)
	return scanExperimentRunRecord(row)
}

func (s *Store) ListExperimentRuns(limit int) ([]ExperimentRunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, name, description, baseline_eval_run_id, candidate_eval_run_id, created_at,
		       baseline_score_count, candidate_score_count, baseline_pass_rate, candidate_pass_rate,
		       pass_rate_delta, matched_score_count, improvement_count, regression_count
		FROM experiment_runs
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExperimentRunRecord
	for rows.Next() {
		record, err := scanExperimentRunRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceUpstreamModels(upstreamID string, records []UpstreamModelRecord) error {
	ctx := context.Background()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.UpstreamModel.Delete().Where(upstreammodel.UpstreamIDEQ(upstreamID)).Exec(ctx); err != nil {
		return err
	}
	creates := make([]*dao.UpstreamModelCreate, 0, len(records))
	for _, record := range records {
		seenAt := record.SeenAt
		if seenAt.IsZero() {
			seenAt = time.Now().UTC()
		}
		creates = append(creates, tx.UpstreamModel.Create().
			SetUpstreamID(upstreamID).
			SetModel(record.Model).
			SetSource(record.Source).
			SetSeenAt(seenAt.UTC()))
	}
	if len(creates) > 0 {
		if err := tx.UpstreamModel.CreateBulk(creates...).
			OnConflictColumns(upstreammodel.FieldUpstreamID, upstreammodel.FieldModel).
			UpdateNewValues().
			Exec(ctx); err != nil {
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
	_, err := s.client.TraceLog.Delete().Exec(context.Background())
	return err
}

func (s *Store) Rebuild() (int, error) {
	if err := s.Reset(); err != nil {
		return 0, err
	}
	if err := s.Sync(); err != nil {
		return 0, err
	}

	count, err := s.client.TraceLog.Query().Count(context.Background())
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) lookupOrCreateTraceID(path string) (string, error) {
	traceID, err := s.client.TraceLog.Query().
		Where(tracelog.IDEQ(path)).
		Select(tracelog.FieldTraceID).
		String(context.Background())
	switch {
	case err == nil && traceID != "":
		return traceID, nil
	case err == nil:
		return uuid.NewString(), nil
	case dao.IsNotFound(err):
		return uuid.NewString(), nil
	default:
		return "", err
	}
}

func (s *Store) isFresh(path string, info os.FileInfo) (bool, error) {
	row, err := s.client.TraceLog.Query().
		Where(tracelog.IDEQ(path)).
		Only(context.Background())
	if dao.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return row.ModTimeNs == info.ModTime().UnixNano() && row.FileSize == info.Size(), nil
}

func (s *Store) ListRecent(limit int) ([]LogEntry, error) {
	rows, err := s.client.TraceLog.Query().
		Order(tracelog.ByRecordedAt(entsql.OrderDesc())).
		Limit(limit).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	entries := make([]LogEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, logEntryFromTraceLog(row))
	}
	return entries, nil
}

func (s *Store) ListPage(page int, pageSize int, filter ListFilter) (ListPageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	ctx := context.Background()
	predicates := buildTraceLogPredicates(filter)
	total, err := s.client.TraceLog.Query().Where(predicates...).Count(ctx)
	if err != nil {
		return ListPageResult{}, err
	}

	offset := (page - 1) * pageSize
	rows, err := s.client.TraceLog.Query().
		Where(predicates...).
		Order(tracelog.ByRecordedAt(entsql.OrderDesc())).
		Limit(pageSize).
		Offset(offset).
		All(ctx)
	if err != nil {
		return ListPageResult{}, err
	}

	result := ListPageResult{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
	for _, row := range rows {
		result.Items = append(result.Items, logEntryFromTraceLog(row))
	}
	if total == 0 {
		result.TotalPages = 0
		return result, nil
	}
	result.TotalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	return result, nil
}

func (s *Store) GetByID(traceID string) (LogEntry, error) {
	row, err := s.client.TraceLog.Query().
		Where(tracelog.TraceIDEQ(traceID)).
		Only(context.Background())
	if err != nil {
		if dao.IsNotFound(err) {
			return LogEntry{}, sql.ErrNoRows
		}
		return LogEntry{}, err
	}
	return logEntryFromTraceLog(row), nil
}

func (s *Store) GetByRequestID(requestID string) (LogEntry, error) {
	row, err := s.client.TraceLog.Query().
		Where(tracelog.RequestIDEQ(requestID)).
		Order(tracelog.ByRecordedAt(entsql.OrderDesc()), tracelog.ByTraceID(entsql.OrderDesc())).
		First(context.Background())
	if err != nil {
		if dao.IsNotFound(err) {
			return LogEntry{}, sql.ErrNoRows
		}
		return LogEntry{}, err
	}
	return logEntryFromTraceLog(row), nil
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
	path, err := s.client.TraceLog.Query().
		Where(tracelog.TraceIDEQ(traceID)).
		OnlyID(context.Background())
	if dao.IsNotFound(err) {
		return "", sql.ErrNoRows
	}
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

func logEntryFromTraceLog(row *dao.TraceLog) LogEntry {
	entry := LogEntry{
		ID:              row.TraceID,
		LogPath:         row.ID,
		SessionID:       row.SessionID,
		SessionSource:   row.SessionSource,
		WindowID:        row.WindowID,
		ClientRequestID: row.ClientRequestID,
	}
	entry.Header.Version = row.Version
	entry.Header.Meta.RequestID = row.RequestID
	entry.Header.Meta.Time = row.RecordedAt
	entry.Header.Meta.Model = row.Model
	entry.Header.Meta.Provider = row.Provider
	entry.Header.Meta.Operation = row.Operation
	entry.Header.Meta.Endpoint = row.Endpoint
	entry.Header.Meta.URL = row.URL
	entry.Header.Meta.Method = row.Method
	entry.Header.Meta.StatusCode = row.StatusCode
	entry.Header.Meta.DurationMs = row.DurationMs
	entry.Header.Meta.TTFTMs = row.TtftMs
	entry.Header.Meta.ClientIP = row.ClientIP
	entry.Header.Meta.ContentLength = row.ContentLength
	entry.Header.Meta.Error = row.ErrorText
	entry.Header.Meta.SelectedUpstreamID = row.SelectedUpstreamID
	entry.Header.Meta.SelectedUpstreamBaseURL = row.SelectedUpstreamBaseURL
	entry.Header.Meta.SelectedUpstreamProviderPreset = row.SelectedUpstreamProviderPreset
	entry.Header.Meta.RoutingPolicy = row.RoutingPolicy
	entry.Header.Meta.RoutingScore = row.RoutingScore
	entry.Header.Meta.RoutingCandidateCount = row.RoutingCandidateCount
	entry.Header.Meta.RoutingFailureReason = row.RoutingFailureReason
	entry.Header.Usage.PromptTokens = row.PromptTokens
	entry.Header.Usage.CompletionTokens = row.CompletionTokens
	entry.Header.Usage.TotalTokens = row.TotalTokens
	if row.CachedTokens > 0 {
		entry.Header.Usage.PromptTokenDetails = &recordfile.PromptTokenDetails{CachedTokens: row.CachedTokens}
	}
	entry.Header.Layout.ReqHeaderLen = row.ReqHeaderLen
	entry.Header.Layout.ReqBodyLen = row.ReqBodyLen
	entry.Header.Layout.ResHeaderLen = row.ResHeaderLen
	entry.Header.Layout.ResBodyLen = row.ResBodyLen
	entry.Header.Layout.IsStream = row.IsStream
	return entry
}

func scanEntry(scanner interface {
	Scan(dest ...any) error
}) (LogEntry, error) {
	var (
		entry        LogEntry
		recordedAt   any
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

	entry.Header.Meta.Time, err = timeParseValue(recordedAt)
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

func scanDatasetRecord(scanner interface {
	Scan(dest ...any) error
}) (DatasetRecord, error) {
	var (
		record    DatasetRecord
		createdAt any
		updatedAt any
		err       error
	)
	if err := scanner.Scan(
		&record.ID,
		&record.Name,
		&record.Description,
		&createdAt,
		&updatedAt,
		&record.ExampleCount,
	); err != nil {
		return DatasetRecord{}, err
	}
	record.CreatedAt, err = timeParseValue(createdAt)
	if err != nil {
		return DatasetRecord{}, err
	}
	record.UpdatedAt, err = timeParseValue(updatedAt)
	if err != nil {
		return DatasetRecord{}, err
	}
	return record, nil
}

func scanDatasetExampleRecord(scanner interface {
	Scan(dest ...any) error
}) (DatasetExampleRecord, error) {
	var (
		record       DatasetExampleRecord
		addedAt      any
		recordedAt   any
		errorText    string
		cached       int
		isStream     int
		routingScore float64
		err          error
	)
	if err := scanner.Scan(
		&record.DatasetID,
		&record.TraceID,
		&record.Position,
		&addedAt,
		&record.SourceType,
		&record.SourceID,
		&record.Note,
		&record.Trace.ID,
		&record.Trace.LogPath,
		&record.Trace.Header.Version,
		&record.Trace.Header.Meta.RequestID,
		&recordedAt,
		&record.Trace.Header.Meta.Model,
		&record.Trace.Header.Meta.Provider,
		&record.Trace.Header.Meta.Operation,
		&record.Trace.Header.Meta.Endpoint,
		&record.Trace.Header.Meta.URL,
		&record.Trace.Header.Meta.Method,
		&record.Trace.Header.Meta.StatusCode,
		&record.Trace.Header.Meta.DurationMs,
		&record.Trace.Header.Meta.TTFTMs,
		&record.Trace.Header.Meta.ClientIP,
		&record.Trace.Header.Meta.ContentLength,
		&errorText,
		&record.Trace.Header.Usage.PromptTokens,
		&record.Trace.Header.Usage.CompletionTokens,
		&record.Trace.Header.Usage.TotalTokens,
		&cached,
		&record.Trace.Header.Layout.ReqHeaderLen,
		&record.Trace.Header.Layout.ReqBodyLen,
		&record.Trace.Header.Layout.ResHeaderLen,
		&record.Trace.Header.Layout.ResBodyLen,
		&isStream,
		&record.Trace.SessionID,
		&record.Trace.SessionSource,
		&record.Trace.WindowID,
		&record.Trace.ClientRequestID,
		&record.Trace.Header.Meta.SelectedUpstreamID,
		&record.Trace.Header.Meta.SelectedUpstreamBaseURL,
		&record.Trace.Header.Meta.SelectedUpstreamProviderPreset,
		&record.Trace.Header.Meta.RoutingPolicy,
		&routingScore,
		&record.Trace.Header.Meta.RoutingCandidateCount,
		&record.Trace.Header.Meta.RoutingFailureReason,
	); err != nil {
		return DatasetExampleRecord{}, err
	}
	record.AddedAt, err = timeParseValue(addedAt)
	if err != nil {
		return DatasetExampleRecord{}, err
	}
	record.Trace.Header.Meta.Time, err = timeParseValue(recordedAt)
	if err != nil {
		return DatasetExampleRecord{}, err
	}
	record.Trace.Header.Meta.Error = errorText
	record.Trace.Header.Meta.RoutingScore = routingScore
	record.Trace.Header.Layout.IsStream = isStream == 1
	if cached > 0 {
		record.Trace.Header.Usage.PromptTokenDetails = &recordfile.PromptTokenDetails{CachedTokens: cached}
	}
	return record, nil
}

func scanEvalRunRecord(scanner interface {
	Scan(dest ...any) error
}) (EvalRunRecord, error) {
	var (
		record      EvalRunRecord
		createdAt   any
		completedAt any
		err         error
	)
	if err := scanner.Scan(
		&record.ID,
		&record.DatasetID,
		&record.SourceType,
		&record.SourceID,
		&record.EvaluatorSet,
		&createdAt,
		&completedAt,
		&record.TraceCount,
		&record.ScoreCount,
		&record.PassCount,
		&record.FailCount,
	); err != nil {
		return EvalRunRecord{}, err
	}
	record.CreatedAt, err = timeParseValue(createdAt)
	if err != nil {
		return EvalRunRecord{}, err
	}
	record.CompletedAt, err = timeParseValue(completedAt)
	if err != nil {
		return EvalRunRecord{}, err
	}
	return record, nil
}

func scanScoreRecord(scanner interface {
	Scan(dest ...any) error
}) (ScoreRecord, error) {
	var (
		record    ScoreRecord
		createdAt any
		err       error
	)
	if err := scanner.Scan(
		&record.ID,
		&record.TraceID,
		&record.SessionID,
		&record.DatasetID,
		&record.EvalRunID,
		&record.EvaluatorKey,
		&record.Value,
		&record.Status,
		&record.Label,
		&record.Explanation,
		&createdAt,
	); err != nil {
		return ScoreRecord{}, err
	}
	record.CreatedAt, err = timeParseValue(createdAt)
	if err != nil {
		return ScoreRecord{}, err
	}
	return record, nil
}

func scanExperimentRunRecord(scanner interface {
	Scan(dest ...any) error
}) (ExperimentRunRecord, error) {
	var (
		record    ExperimentRunRecord
		createdAt any
		err       error
	)
	if err := scanner.Scan(
		&record.ID,
		&record.Name,
		&record.Description,
		&record.BaselineEvalRunID,
		&record.CandidateEvalRunID,
		&createdAt,
		&record.BaselineScoreCount,
		&record.CandidateScoreCount,
		&record.BaselinePassRate,
		&record.CandidatePassRate,
		&record.PassRateDelta,
		&record.MatchedScoreCount,
		&record.ImprovementCount,
		&record.RegressionCount,
	); err != nil {
		return ExperimentRunRecord{}, err
	}
	record.CreatedAt, err = timeParseValue(createdAt)
	if err != nil {
		return ExperimentRunRecord{}, err
	}
	return record, nil
}

func scanSessionSummary(scanner interface {
	Scan(dest ...any) error
}) (SessionSummary, error) {
	var (
		summary      SessionSummary
		firstSeen    any
		lastSeen     any
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
	summary.FirstSeen, err = timeParseValue(firstSeen)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.LastSeen, err = timeParseValue(lastSeen)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.AvgTTFT = int(math.Round(avgTTFT))
	summary.Providers = splitProviders(providersCSV)
	return summary, nil
}

func timeParse(v string) (time.Time, error) {
	for _, layout := range []string{
		timeLayout,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed, nil
		}
	}
	return time.Parse(timeLayout, v)
}

func timeParseValue(v any) (time.Time, error) {
	switch value := v.(type) {
	case time.Time:
		return value, nil
	case string:
		return timeParse(value)
	case []byte:
		return timeParse(string(value))
	case nil:
		return time.Time{}, nil
	default:
		return timeParse(fmt.Sprint(value))
	}
}

func isEmptyTimeValue(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(value) == ""
	case []byte:
		return strings.TrimSpace(string(value)) == ""
	case time.Time:
		return value.IsZero()
	default:
		return strings.TrimSpace(fmt.Sprint(value)) == ""
	}
}

func boolValue(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case int:
		return value != 0
	case int64:
		return value != 0
	case string:
		return value == "1" || strings.EqualFold(value, "true")
	case []byte:
		s := string(value)
		return s == "1" || strings.EqualFold(s, "true")
	default:
		return false
	}
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

func buildTraceLogPredicates(filter ListFilter) []predicate.TraceLog {
	var predicates []predicate.TraceLog
	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		predicates = append(predicates, tracelog.ProviderEqualFold(provider))
	}
	if model := strings.TrimSpace(filter.Model); model != "" {
		predicates = append(predicates, tracelog.ModelContainsFold(model))
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		predicates = append(predicates, tracelog.Or(
			tracelog.SessionIDContainsFold(query),
			tracelog.TraceIDContainsFold(query),
			tracelog.ModelContainsFold(query),
			tracelog.ProviderContainsFold(query),
			tracelog.EndpointContainsFold(query),
			tracelog.URLContainsFold(query),
		))
	}
	return predicates
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
	rows, err := s.db.Query(`SELECT path FROM logs WHERE session_source = '' OR session_source = 'none'`)
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
