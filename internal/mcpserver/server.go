package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/replay"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultPage     = 1
	defaultPageSize = 50
	maxPageSize     = 200
)

type Options struct {
	Router *router.Router
}

type listTracesInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of items per page, max 200"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model    string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
}

type getTraceInput struct {
	TraceID    string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
	IncludeRaw bool   `json:"include_raw,omitempty" jsonschema:"include raw HTTP request and response bytes"`
}

type listSessionsInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of items per page, max 200"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model    string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
}

type getSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"session identifier from list_sessions"`
}

type listUpstreamsInput struct {
	Window string `json:"window,omitempty" jsonschema:"time window: 1h, 24h, 7d, or all"`
	Model  string `json:"model,omitempty" jsonschema:"optional model substring filter"`
}

type getUpstreamInput struct {
	UpstreamID string `json:"upstream_id" jsonschema:"upstream identifier from list_upstreams"`
	Window     string `json:"window,omitempty" jsonschema:"time window: 1h, 24h, 7d, or all"`
	Model      string `json:"model,omitempty" jsonschema:"optional model substring filter"`
}

type queryFailuresInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number to scan from list_traces"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of traces to scan, max 200"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model    string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
}

type replayTraceInput struct {
	TraceID   string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
	BodyLimit int    `json:"body_limit,omitempty" jsonschema:"maximum response body bytes to include, max 20000"`
}

type replaySessionInput struct {
	SessionID string `json:"session_id" jsonschema:"session identifier from list_sessions"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum traces to replay from the session, max 50"`
	BodyLimit int    `json:"body_limit,omitempty" jsonschema:"maximum response body bytes to include per trace, max 20000"`
}

type traceListOutput struct {
	Items       []map[string]any `json:"items"`
	Stats       map[string]any   `json:"stats"`
	Page        int              `json:"page"`
	PageSize    int              `json:"page_size"`
	Total       int              `json:"total"`
	TotalPages  int              `json:"total_pages"`
	RefreshedAt time.Time        `json:"refreshed_at"`
}

type sessionListOutput struct {
	Items       []map[string]any `json:"items"`
	Page        int              `json:"page"`
	PageSize    int              `json:"page_size"`
	Total       int              `json:"total"`
	TotalPages  int              `json:"total_pages"`
	RefreshedAt time.Time        `json:"refreshed_at"`
}

type upstreamListOutput struct {
	Items           []map[string]any `json:"items"`
	RoutingFailures map[string]any   `json:"routing_failures"`
	RefreshedAt     time.Time        `json:"refreshed_at"`
	Window          string           `json:"window"`
	Model           string           `json:"model"`
}

type queryFailuresOutput struct {
	Items       []map[string]any `json:"items"`
	Page        int              `json:"page"`
	PageSize    int              `json:"page_size"`
	Scanned     int              `json:"scanned"`
	Returned    int              `json:"returned"`
	Provider    string           `json:"provider,omitempty"`
	Model       string           `json:"model,omitempty"`
	Query       string           `json:"q,omitempty"`
	RefreshedAt time.Time        `json:"refreshed_at"`
}

type replayTraceOutput struct {
	TraceID    string         `json:"trace_id"`
	SessionID  string         `json:"session_id,omitempty"`
	RecordedAt time.Time      `json:"recorded_at"`
	Model      string         `json:"model"`
	Provider   string         `json:"provider"`
	Endpoint   string         `json:"endpoint"`
	Replay     replay.Summary `json:"replay"`
}

type replaySessionItem struct {
	TraceID    string         `json:"trace_id"`
	RecordedAt time.Time      `json:"recorded_at"`
	Model      string         `json:"model"`
	Provider   string         `json:"provider"`
	Endpoint   string         `json:"endpoint"`
	Replay     replay.Summary `json:"replay"`
}

type replaySessionOutput struct {
	SessionID   string              `json:"session_id"`
	Requested   int                 `json:"requested"`
	Replayed    int                 `json:"replayed"`
	Truncated   bool                `json:"truncated"`
	Items       []replaySessionItem `json:"items"`
	RefreshedAt time.Time           `json:"refreshed_at"`
}

type serverAPI struct {
	handler http.Handler
	store   *store.Store
}

type sessionLookupResult struct {
	Summary store.SessionSummary
	Traces  []store.LogEntry
}

func New(traceStore *store.Store, opts Options) *mcp.Server {
	mux := http.NewServeMux()
	monitor.RegisterRoutes(mux, traceStore, monitor.RouteOptions{Router: opts.Router})

	api := &serverAPI{handler: mux, store: traceStore}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "llm-tracelab",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_traces",
		Description: "List recorded traces with pagination and optional provider/model/query filters.",
	}, api.listTraces)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_trace",
		Description: "Get one trace detail by trace_id, optionally including raw HTTP request and response bytes.",
	}, api.getTrace)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sessions",
		Description: "List grouped sessions with pagination and optional provider/model/query filters.",
	}, api.listSessions)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_session",
		Description: "Get one grouped session detail by session_id.",
	}, api.getSession)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_upstreams",
		Description: "List upstream analytics with an optional time window and model filter.",
	}, api.listUpstreams)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_upstream",
		Description: "Get one upstream drilldown by upstream_id with an optional time window and model filter.",
	}, api.getUpstream)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_failures",
		Description: "Return failed traces from a paginated trace scan using the same filters as list_traces.",
	}, api.queryFailures)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "replay_trace",
		Description: "Replay one recorded trace locally and return a structured HTTP response summary.",
	}, api.replayTrace)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "replay_session",
		Description: "Replay multiple traces from one session locally and return bounded response summaries.",
	}, api.replaySession)

	return server
}

func (a *serverAPI) listTraces(ctx context.Context, req *mcp.CallToolRequest, in *listTracesInput) (*mcp.CallToolResult, *traceListOutput, error) {
	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", normalizePage(in.Page)))
	values.Set("page_size", fmt.Sprintf("%d", normalizePageSize(in.PageSize)))
	setIfNotEmpty(values, "provider", in.Provider)
	setIfNotEmpty(values, "model", in.Model)
	setIfNotEmpty(values, "q", in.Query)

	var out traceListOutput
	if err := a.getJSON(ctx, "/api/traces", values, &out); err != nil {
		return nil, nil, err
	}
	return nil, &out, nil
}

func (a *serverAPI) getTrace(ctx context.Context, req *mcp.CallToolRequest, in *getTraceInput) (*mcp.CallToolResult, map[string]any, error) {
	traceID := strings.TrimSpace(in.TraceID)
	if traceID == "" {
		return nil, nil, fmt.Errorf("trace_id is required")
	}

	var out map[string]any
	if err := a.getJSON(ctx, "/api/traces/"+url.PathEscape(traceID), nil, &out); err != nil {
		return nil, nil, err
	}
	if in.IncludeRaw {
		var raw map[string]any
		if err := a.getJSON(ctx, "/api/traces/"+url.PathEscape(traceID)+"/raw", nil, &raw); err != nil {
			return nil, nil, err
		}
		out["raw"] = raw
	}
	return nil, out, nil
}

func (a *serverAPI) listSessions(ctx context.Context, req *mcp.CallToolRequest, in *listSessionsInput) (*mcp.CallToolResult, *sessionListOutput, error) {
	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", normalizePage(in.Page)))
	values.Set("page_size", fmt.Sprintf("%d", normalizePageSize(in.PageSize)))
	setIfNotEmpty(values, "provider", in.Provider)
	setIfNotEmpty(values, "model", in.Model)
	setIfNotEmpty(values, "q", in.Query)

	var out sessionListOutput
	if err := a.getJSON(ctx, "/api/sessions", values, &out); err != nil {
		return nil, nil, err
	}
	return nil, &out, nil
}

func (a *serverAPI) getSession(ctx context.Context, req *mcp.CallToolRequest, in *getSessionInput) (*mcp.CallToolResult, map[string]any, error) {
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	var out map[string]any
	if err := a.getJSON(ctx, "/api/sessions/"+url.PathEscape(sessionID), nil, &out); err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func (a *serverAPI) listUpstreams(ctx context.Context, req *mcp.CallToolRequest, in *listUpstreamsInput) (*mcp.CallToolResult, *upstreamListOutput, error) {
	values := url.Values{}
	setIfNotEmpty(values, "window", in.Window)
	setIfNotEmpty(values, "model", in.Model)

	var out upstreamListOutput
	if err := a.getJSON(ctx, "/api/upstreams", values, &out); err != nil {
		return nil, nil, err
	}
	return nil, &out, nil
}

func (a *serverAPI) getUpstream(ctx context.Context, req *mcp.CallToolRequest, in *getUpstreamInput) (*mcp.CallToolResult, map[string]any, error) {
	upstreamID := strings.TrimSpace(in.UpstreamID)
	if upstreamID == "" {
		return nil, nil, fmt.Errorf("upstream_id is required")
	}

	values := url.Values{}
	setIfNotEmpty(values, "window", in.Window)
	setIfNotEmpty(values, "model", in.Model)

	var out map[string]any
	if err := a.getJSON(ctx, "/api/upstreams/"+url.PathEscape(upstreamID), values, &out); err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func (a *serverAPI) queryFailures(ctx context.Context, req *mcp.CallToolRequest, in *queryFailuresInput) (*mcp.CallToolResult, *queryFailuresOutput, error) {
	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", normalizePage(in.Page)))
	values.Set("page_size", fmt.Sprintf("%d", normalizePageSize(in.PageSize)))
	setIfNotEmpty(values, "provider", in.Provider)
	setIfNotEmpty(values, "model", in.Model)
	setIfNotEmpty(values, "q", in.Query)

	var page traceListOutput
	if err := a.getJSON(ctx, "/api/traces", values, &page); err != nil {
		return nil, nil, err
	}

	out := &queryFailuresOutput{
		Page:        page.Page,
		PageSize:    page.PageSize,
		Scanned:     len(page.Items),
		Provider:    strings.TrimSpace(in.Provider),
		Model:       strings.TrimSpace(in.Model),
		Query:       strings.TrimSpace(in.Query),
		RefreshedAt: time.Now().UTC(),
	}
	for _, item := range page.Items {
		statusCode, _ := item["status_code"].(float64)
		errText, _ := item["error"].(string)
		if statusCode < 200 || statusCode >= 300 || strings.TrimSpace(errText) != "" {
			out.Items = append(out.Items, item)
		}
	}
	out.Returned = len(out.Items)
	return nil, out, nil
}

func (a *serverAPI) replayTrace(ctx context.Context, req *mcp.CallToolRequest, in *replayTraceInput) (*mcp.CallToolResult, *replayTraceOutput, error) {
	traceID := strings.TrimSpace(in.TraceID)
	if traceID == "" {
		return nil, nil, fmt.Errorf("trace_id is required")
	}

	entry, err := a.lookupTrace(traceID)
	if err != nil {
		return nil, nil, err
	}
	summary, err := replay.ReplayFile(entry.LogPath, replay.SummaryOptions{BodyLimit: in.BodyLimit})
	if err != nil {
		return nil, nil, err
	}

	return nil, &replayTraceOutput{
		TraceID:    entry.ID,
		SessionID:  entry.SessionID,
		RecordedAt: entry.Header.Meta.Time,
		Model:      entry.Header.Meta.Model,
		Provider:   entry.Header.Meta.Provider,
		Endpoint:   firstNonEmpty(entry.Header.Meta.Endpoint, entry.Header.Meta.URL),
		Replay:     *summary,
	}, nil
}

func (a *serverAPI) replaySession(ctx context.Context, req *mcp.CallToolRequest, in *replaySessionInput) (*mcp.CallToolResult, *replaySessionOutput, error) {
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}

	sessionDetail, err := a.lookupSession(sessionID)
	if err != nil {
		return nil, nil, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	out := &replaySessionOutput{
		SessionID:   sessionID,
		Requested:   limit,
		RefreshedAt: time.Now().UTC(),
	}

	for i, entry := range sessionDetail.Traces {
		if i >= limit {
			out.Truncated = true
			break
		}
		summary, err := replay.ReplayFile(entry.LogPath, replay.SummaryOptions{BodyLimit: in.BodyLimit})
		if err != nil {
			return nil, nil, fmt.Errorf("replay trace %s: %w", entry.ID, err)
		}
		out.Items = append(out.Items, replaySessionItem{
			TraceID:    entry.ID,
			RecordedAt: entry.Header.Meta.Time,
			Model:      entry.Header.Meta.Model,
			Provider:   entry.Header.Meta.Provider,
			Endpoint:   firstNonEmpty(entry.Header.Meta.Endpoint, entry.Header.Meta.URL),
			Replay:     *summary,
		})
	}
	out.Replayed = len(out.Items)
	return nil, out, nil
}

func (a *serverAPI) getJSON(ctx context.Context, path string, query url.Values, out interface{}) error {
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	a.handler.ServeHTTP(rr, req)

	if rr.Code < 200 || rr.Code >= 300 {
		var payload map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err == nil {
			if msg, ok := payload["error"].(string); ok && strings.TrimSpace(msg) != "" {
				return errors.New(msg)
			}
		}
		return fmt.Errorf("monitor api returned status %d", rr.Code)
	}

	if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
		return fmt.Errorf("decode monitor response: %w", err)
	}
	return nil
}

func (a *serverAPI) lookupTrace(traceID string) (store.LogEntry, error) {
	if a.store == nil {
		return store.LogEntry{}, fmt.Errorf("store not configured")
	}
	if err := a.store.Sync(); err != nil {
		return store.LogEntry{}, fmt.Errorf("sync error: %w", err)
	}
	entry, err := a.store.GetByID(traceID)
	if err != nil {
		return store.LogEntry{}, err
	}
	return entry, nil
}

func (a *serverAPI) lookupSession(sessionID string) (*sessionLookupResult, error) {
	if a.store == nil {
		return nil, fmt.Errorf("store not configured")
	}
	if err := a.store.Sync(); err != nil {
		return nil, fmt.Errorf("sync error: %w", err)
	}
	summary, err := a.store.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	traces, err := a.store.ListTracesBySession(sessionID)
	if err != nil {
		return nil, err
	}
	return &sessionLookupResult{
		Summary: summary,
		Traces:  traces,
	}, nil
}

func normalizePage(page int) int {
	if page <= 0 {
		return defaultPage
	}
	return page
}

func normalizePageSize(pageSize int) int {
	switch {
	case pageSize <= 0:
		return defaultPageSize
	case pageSize > maxPageSize:
		return maxPageSize
	default:
		return pageSize
	}
}

func setIfNotEmpty(values url.Values, key string, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		values.Set(key, trimmed)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
