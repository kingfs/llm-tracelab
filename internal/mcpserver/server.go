package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
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

type listUpstreamsInput struct {
	Window string `json:"window,omitempty" jsonschema:"time window: 1h, 24h, 7d, or all"`
	Model  string `json:"model,omitempty" jsonschema:"optional model substring filter"`
}

type queryFailuresInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number to scan from list_traces"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of traces to scan, max 200"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model    string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
}

type summarizeFailureClustersInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number to scan from list_traces"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of traces to scan, max 200"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model    string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum grouped items per section, default 10"`
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

type failureSummaryItem struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type failureTraceItem struct {
	TraceID            string    `json:"trace_id"`
	SessionID          string    `json:"session_id,omitempty"`
	Model              string    `json:"model"`
	Provider           string    `json:"provider"`
	Endpoint           string    `json:"endpoint"`
	RecordedAt         time.Time `json:"recorded_at"`
	StatusCode         int       `json:"status_code"`
	Reason             string    `json:"reason"`
	Error              string    `json:"error,omitempty"`
	SelectedUpstreamID string    `json:"selected_upstream_id,omitempty"`
}

type summarizeFailureClustersOutput struct {
	Page        int                  `json:"page"`
	PageSize    int                  `json:"page_size"`
	Scanned     int                  `json:"scanned"`
	Returned    int                  `json:"returned"`
	Provider    string               `json:"provider,omitempty"`
	Model       string               `json:"model,omitempty"`
	Query       string               `json:"q,omitempty"`
	ByReason    []failureSummaryItem `json:"by_reason"`
	ByStatus    []failureSummaryItem `json:"by_status"`
	ByModel     []failureSummaryItem `json:"by_model"`
	ByProvider  []failureSummaryItem `json:"by_provider"`
	ByEndpoint  []failureSummaryItem `json:"by_endpoint"`
	ByUpstream  []failureSummaryItem `json:"by_upstream"`
	TopFailures []failureTraceItem   `json:"top_failures"`
	RefreshedAt time.Time            `json:"refreshed_at"`
}

type serverAPI struct {
	handler http.Handler
	store   *store.Store
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
		Name:        "list_upstreams",
		Description: "List upstream analytics with an optional time window and model filter.",
	}, api.listUpstreams)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_failures",
		Description: "Return failed traces from a paginated trace scan using the same filters as list_traces.",
	}, api.queryFailures)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "summarize_failure_clusters",
		Description: "Summarize clustered failures from a filtered trace scan by reason, status, model, provider, endpoint, upstream, and top failed traces.",
	}, api.summarizeFailureClusters)

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

func (a *serverAPI) summarizeFailureClusters(ctx context.Context, req *mcp.CallToolRequest, in *summarizeFailureClustersInput) (*mcp.CallToolResult, *summarizeFailureClustersOutput, error) {
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

	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	out := &summarizeFailureClustersOutput{
		Page:        page.Page,
		PageSize:    page.PageSize,
		Scanned:     len(page.Items),
		Provider:    strings.TrimSpace(in.Provider),
		Model:       strings.TrimSpace(in.Model),
		Query:       strings.TrimSpace(in.Query),
		RefreshedAt: time.Now().UTC(),
	}
	byReason := map[string]int{}
	byStatus := map[string]int{}
	byModel := map[string]int{}
	byProvider := map[string]int{}
	byEndpoint := map[string]int{}
	byUpstream := map[string]int{}

	for _, item := range page.Items {
		statusCode, _ := item["status_code"].(float64)
		errorText, _ := item["error"].(string)
		if statusCode >= 200 && statusCode < 300 && strings.TrimSpace(errorText) == "" {
			continue
		}
		traceID, _ := item["id"].(string)
		entry, err := a.lookupTrace(traceID)
		if err != nil {
			return nil, nil, err
		}
		reason := classifyFailureReason(int(statusCode), errorText)
		incrementCount(byReason, reason)
		incrementCount(byStatus, fmt.Sprintf("%d", int(statusCode)))
		incrementCount(byModel, entry.Header.Meta.Model)
		incrementCount(byProvider, entry.Header.Meta.Provider)
		incrementCount(byEndpoint, firstNonEmpty(entry.Header.Meta.Endpoint, entry.Header.Meta.URL))
		incrementCount(byUpstream, entry.Header.Meta.SelectedUpstreamID)
		out.TopFailures = append(out.TopFailures, failureTraceItem{
			TraceID:            entry.ID,
			SessionID:          entry.SessionID,
			Model:              entry.Header.Meta.Model,
			Provider:           entry.Header.Meta.Provider,
			Endpoint:           firstNonEmpty(entry.Header.Meta.Endpoint, entry.Header.Meta.URL),
			RecordedAt:         entry.Header.Meta.Time,
			StatusCode:         entry.Header.Meta.StatusCode,
			Reason:             reason,
			Error:              entry.Header.Meta.Error,
			SelectedUpstreamID: entry.Header.Meta.SelectedUpstreamID,
		})
	}
	out.Returned = len(out.TopFailures)
	out.ByReason = toFailureSummaryItems(byReason, limit)
	out.ByStatus = toFailureSummaryItems(byStatus, limit)
	out.ByModel = toFailureSummaryItems(byModel, limit)
	out.ByProvider = toFailureSummaryItems(byProvider, limit)
	out.ByEndpoint = toFailureSummaryItems(byEndpoint, limit)
	out.ByUpstream = toFailureSummaryItems(byUpstream, limit)
	sort.Slice(out.TopFailures, func(i, j int) bool {
		if out.TopFailures[i].Reason != out.TopFailures[j].Reason {
			return out.TopFailures[i].Reason < out.TopFailures[j].Reason
		}
		if !out.TopFailures[i].RecordedAt.Equal(out.TopFailures[j].RecordedAt) {
			return out.TopFailures[i].RecordedAt.After(out.TopFailures[j].RecordedAt)
		}
		return out.TopFailures[i].TraceID < out.TopFailures[j].TraceID
	})
	if len(out.TopFailures) > limit {
		out.TopFailures = out.TopFailures[:limit]
	}
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
	if err := a.requireStoreSync(); err != nil {
		return store.LogEntry{}, err
	}
	entry, err := a.store.GetByID(traceID)
	if err != nil {
		return store.LogEntry{}, err
	}
	return entry, nil
}

func (a *serverAPI) requireStoreSync() error {
	if a.store == nil {
		return fmt.Errorf("store not configured")
	}
	return nil
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

func incrementCount(counts map[string]int, label string) {
	key := strings.TrimSpace(label)
	if key == "" {
		key = "(empty)"
	}
	counts[key]++
}

func toFailureSummaryItems(counts map[string]int, limit int) []failureSummaryItem {
	items := make([]failureSummaryItem, 0, len(counts))
	for label, count := range counts {
		items = append(items, failureSummaryItem{Label: label, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Label < items[j].Label
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func classifyFailureReason(statusCode int, errorText string) string {
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
