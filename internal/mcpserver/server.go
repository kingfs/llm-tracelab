package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/reanalysis"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
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
	Page              int    `json:"page,omitempty" jsonschema:"1-based page number"`
	PageSize          int    `json:"page_size,omitempty" jsonschema:"number of items per page, max 200"`
	Provider          string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model             string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query             string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
	ObservationStatus string `json:"observation,omitempty" jsonschema:"optional Observation IR status filter: parsed, failed, queued, running, or unparsed"`
}

type getTraceInput struct {
	TraceID    string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
	IncludeRaw bool   `json:"include_raw,omitempty" jsonschema:"include raw HTTP request and response bytes"`
}

type queryRoutingDecisionsInput struct {
	TraceID string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
}

type queryStickyRoutingInput struct {
	Page                 int    `json:"page,omitempty" jsonschema:"1-based page number"`
	PageSize             int    `json:"page_size,omitempty" jsonschema:"number of matching sticky rows per page, max 200"`
	Status               string `json:"status,omitempty" jsonschema:"optional sticky status filter: hit, miss, bind, or break"`
	UpstreamID           string `json:"upstream_id,omitempty" jsonschema:"optional upstream id filter"`
	PreviousUpstreamID   string `json:"previous_upstream_id,omitempty" jsonschema:"optional previous upstream id filter"`
	StickyKeyFingerprint string `json:"sticky_key_fingerprint,omitempty" jsonschema:"optional sticky key fingerprint filter"`
}

type listTraceFindingsInput struct {
	TraceID  string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
	Severity string `json:"severity,omitempty" jsonschema:"optional severity filter"`
	Category string `json:"category,omitempty" jsonschema:"optional category filter"`
}

type queryDangerousToolCallsInput struct {
	TraceID string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
}

type querySensitiveDataFindingsInput struct {
	TraceID string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
}

type listSessionsInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of items per page, max 200"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider filter"`
	Model    string `json:"model,omitempty" jsonschema:"optional model substring filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
}

type listUpstreamsInput struct {
	Window string `json:"window,omitempty" jsonschema:"time window: today, 7d, 30d, or all"`
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

type listSystemEventsInput struct {
	Page     int    `json:"page,omitempty" jsonschema:"1-based page number"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"number of events per page, max 200"`
	Status   string `json:"status,omitempty" jsonschema:"optional status filter: unread, read, resolved, ignored, or all"`
	Severity string `json:"severity,omitempty" jsonschema:"optional severity filter: info, warning, error, or critical"`
	Source   string `json:"source,omitempty" jsonschema:"optional source filter such as parser, analyzer, router, upstream, monitor, store, or mcp"`
	Category string `json:"category,omitempty" jsonschema:"optional category filter"`
	Query    string `json:"q,omitempty" jsonschema:"optional free-text query filter"`
	Window   string `json:"window,omitempty" jsonschema:"time window: today, 7d, 30d, or all"`
}

type getSystemEventInput struct {
	EventID        string `json:"event_id" jsonschema:"system event id from list_system_events"`
	IncludeDetails bool   `json:"include_details,omitempty" jsonschema:"include details_json in the response"`
}

type summarizeSystemEventsInput struct {
	Window string `json:"window,omitempty" jsonschema:"time window: today, 7d, 30d, or all"`
	Status string `json:"status,omitempty" jsonschema:"optional status filter for newest events, default unread"`
}

type queryUnreadSystemEventsInput struct {
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum unread events to return, default 20, max 200"`
	MinSeverity string `json:"min_severity,omitempty" jsonschema:"minimum severity: info, warning, error, or critical"`
}

type reanalyzeTraceInput struct {
	TraceID     string `json:"trace_id" jsonschema:"trace identifier from list_traces"`
	RepairUsage bool   `json:"repair_usage,omitempty" jsonschema:"repair indexed usage before reparse/scan"`
	Reparse     bool   `json:"reparse,omitempty" jsonschema:"rebuild Observation IR, default true"`
	Scan        bool   `json:"scan,omitempty" jsonschema:"run deterministic audit scan, default true"`
	Async       bool   `json:"async,omitempty" jsonschema:"enqueue and return without executing immediately"`
}

type reanalyzeSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"session identifier from list_sessions"`
	Reparse   bool   `json:"reparse,omitempty" jsonschema:"rebuild Observation IR for session traces, default true"`
	Scan      bool   `json:"scan,omitempty" jsonschema:"run deterministic audit scan for session traces, default true"`
	Async     bool   `json:"async,omitempty" jsonschema:"enqueue and return without executing immediately, default true"`
}

type listAnalysisJobsInput struct {
	Status     string `json:"status,omitempty" jsonschema:"optional job status filter"`
	TargetType string `json:"target_type,omitempty" jsonschema:"optional target type filter: trace, session, or batch"`
	TargetID   string `json:"target_id,omitempty" jsonschema:"optional target identifier filter"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum jobs to return, default 50"`
}

type getAnalysisJobInput struct {
	JobID int64 `json:"job_id" jsonschema:"analysis job id from list_analysis_jobs"`
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

type routingDecisionOutput struct {
	TraceID               string           `json:"trace_id"`
	Model                 string           `json:"model,omitempty"`
	Endpoint              string           `json:"endpoint,omitempty"`
	RoutingPolicy         string           `json:"routing_policy,omitempty"`
	FallbackPolicy        string           `json:"fallback_policy,omitempty"`
	SelectedUpstreamID    string           `json:"selected_upstream_id,omitempty"`
	SelectedRouteTargetID string           `json:"selected_route_target_id,omitempty"`
	SelectedChannelID     string           `json:"selected_channel_id,omitempty"`
	SelectedCredentialID  string           `json:"selected_credential_id,omitempty"`
	FailureReason         string           `json:"failure_reason,omitempty"`
	StatusCode            int              `json:"status_code,omitempty"`
	CandidateCount        int              `json:"candidate_count"`
	AvailableCount        int              `json:"available_count"`
	Events                []map[string]any `json:"events"`
	Candidates            []map[string]any `json:"candidates,omitempty"`
	Outcome               map[string]any   `json:"outcome,omitempty"`
}

type stickyRoutingOutput struct {
	Items                []stickyRoutingRow `json:"items"`
	Page                 int                `json:"page"`
	PageSize             int                `json:"page_size"`
	Total                int                `json:"total"`
	TotalPages           int                `json:"total_pages"`
	Scanned              int                `json:"scanned"`
	Skipped              int                `json:"skipped"`
	Errors               []string           `json:"errors,omitempty"`
	Status               string             `json:"status,omitempty"`
	UpstreamID           string             `json:"upstream_id,omitempty"`
	PreviousUpstreamID   string             `json:"previous_upstream_id,omitempty"`
	StickyKeyFingerprint string             `json:"sticky_key_fingerprint,omitempty"`
	RefreshedAt          time.Time          `json:"refreshed_at"`
}

type stickyRoutingRow struct {
	TraceID               string    `json:"trace_id"`
	CreatedAt             time.Time `json:"created_at,omitempty"`
	StickyStatus          string    `json:"sticky_status"`
	UpstreamID            string    `json:"upstream_id,omitempty"`
	PreviousUpstreamID    string    `json:"previous_upstream_id,omitempty"`
	RouteTargetID         string    `json:"route_target_id,omitempty"`
	PreviousRouteTargetID string    `json:"previous_route_target_id,omitempty"`
	ChannelID             string    `json:"channel_id,omitempty"`
	PreviousChannelID     string    `json:"previous_channel_id,omitempty"`
	CredentialID          string    `json:"credential_id,omitempty"`
	PreviousCredentialID  string    `json:"previous_credential_id,omitempty"`
	StickyKeyFingerprint  string    `json:"sticky_key_fingerprint,omitempty"`
	CassettePath          string    `json:"cassette_path"`
	LogPath               string    `json:"log_path"`
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
	RouteTargetID      string    `json:"route_target_id,omitempty"`
	ChannelID          string    `json:"channel_id,omitempty"`
	CredentialID       string    `json:"credential_id,omitempty"`
	RoutingEventReason string    `json:"routing_event_reason,omitempty"`
}

type summarizeFailureClustersOutput struct {
	Page          int                  `json:"page"`
	PageSize      int                  `json:"page_size"`
	Scanned       int                  `json:"scanned"`
	Returned      int                  `json:"returned"`
	Provider      string               `json:"provider,omitempty"`
	Model         string               `json:"model,omitempty"`
	Query         string               `json:"q,omitempty"`
	ByReason      []failureSummaryItem `json:"by_reason"`
	ByStatus      []failureSummaryItem `json:"by_status"`
	ByModel       []failureSummaryItem `json:"by_model"`
	ByProvider    []failureSummaryItem `json:"by_provider"`
	ByEndpoint    []failureSummaryItem `json:"by_endpoint"`
	ByUpstream    []failureSummaryItem `json:"by_upstream"`
	ByRouteTarget []failureSummaryItem `json:"by_route_target"`
	ByChannel     []failureSummaryItem `json:"by_channel"`
	ByCredential  []failureSummaryItem `json:"by_credential"`
	TopFailures   []failureTraceItem   `json:"top_failures"`
	RefreshedAt   time.Time            `json:"refreshed_at"`
}

type systemEventListOutput struct {
	Items       []map[string]any `json:"items"`
	Page        int              `json:"page"`
	PageSize    int              `json:"page_size"`
	Total       int              `json:"total"`
	TotalPages  int              `json:"total_pages"`
	Window      string           `json:"window"`
	RefreshedAt time.Time        `json:"refreshed_at"`
}

type systemEventSummaryOutput struct {
	Total      int              `json:"total"`
	Unread     int              `json:"unread"`
	Critical   int              `json:"critical"`
	Error      int              `json:"error"`
	Warning    int              `json:"warning"`
	LastSeenAt string           `json:"last_seen_at,omitempty"`
	BySource   []map[string]any `json:"by_source"`
	ByCategory []map[string]any `json:"by_category"`
	Window     string           `json:"window"`
	Newest     []map[string]any `json:"newest,omitempty"`
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
		Description: "List recorded traces with pagination and optional provider/model/query/Observation status filters.",
	}, api.listTraces)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_trace",
		Description: "Get one trace detail by trace_id, optionally including raw HTTP request and response bytes.",
	}, api.getTrace)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_routing_decisions",
		Description: "Return routing decision events for one trace, including candidates, selected upstream, outcome, and failure reason.",
	}, api.queryRoutingDecisions)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_sticky_routing",
		Description: "Find traces with routing.sticky.* cassette events, with optional status, upstream, previous upstream, and fingerprint filters.",
	}, api.queryStickyRouting)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_trace_findings",
		Description: "List deterministic audit findings for one trace, with optional severity and category filters.",
	}, api.listTraceFindings)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_dangerous_tool_calls",
		Description: "Return dangerous command and unsafe tool-call findings for one trace.",
	}, api.queryDangerousToolCalls)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_sensitive_data_findings",
		Description: "Return credential and sensitive-data findings for one trace.",
	}, api.querySensitiveDataFindings)
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_system_events",
		Description: "List TraceLab runtime and analysis exception events with pagination and filters.",
	}, api.listSystemEvents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_system_event",
		Description: "Get one TraceLab system event detail by event_id.",
	}, api.getSystemEvent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "summarize_system_events",
		Description: "Return compact TraceLab system event counts and newest events for agent triage.",
	}, api.summarizeSystemEvents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_unread_system_events",
		Description: "Return unread warning/error/critical TraceLab system events ordered by severity and recency.",
	}, api.queryUnreadSystemEvents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reanalyze_trace",
		Description: "Run or enqueue controlled reanalysis for one trace.",
	}, api.reanalyzeTrace)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reanalyze_session",
		Description: "Run or enqueue controlled reanalysis for one session.",
	}, api.reanalyzeSession)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_analysis_jobs",
		Description: "List reanalysis jobs with optional status and target filters.",
	}, api.listAnalysisJobs)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_analysis_job",
		Description: "Get one reanalysis job by job_id.",
	}, api.getAnalysisJob)

	return server
}

func (a *serverAPI) listTraces(ctx context.Context, req *mcp.CallToolRequest, in *listTracesInput) (*mcp.CallToolResult, *traceListOutput, error) {
	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", normalizePage(in.Page)))
	values.Set("page_size", fmt.Sprintf("%d", normalizePageSize(in.PageSize)))
	setIfNotEmpty(values, "provider", in.Provider)
	setIfNotEmpty(values, "model", in.Model)
	setIfNotEmpty(values, "q", in.Query)
	setIfNotEmpty(values, "observation", in.ObservationStatus)

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

func (a *serverAPI) queryRoutingDecisions(ctx context.Context, req *mcp.CallToolRequest, in *queryRoutingDecisionsInput) (*mcp.CallToolResult, *routingDecisionOutput, error) {
	traceID := strings.TrimSpace(in.TraceID)
	if traceID == "" {
		return nil, nil, fmt.Errorf("trace_id is required")
	}
	entry, err := a.lookupTrace(traceID)
	if err != nil {
		return nil, nil, err
	}
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read trace cassette: %w", err)
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return nil, nil, fmt.Errorf("parse trace prelude: %w", err)
	}
	out := routingDecisionOutput{
		TraceID:            entry.ID,
		Model:              parsed.Header.Meta.Model,
		Endpoint:           firstNonEmpty(parsed.Header.Meta.Endpoint, parsed.Header.Meta.URL),
		RoutingPolicy:      parsed.Header.Meta.RoutingPolicy,
		SelectedUpstreamID: parsed.Header.Meta.SelectedUpstreamID,
		FailureReason:      parsed.Header.Meta.RoutingFailureReason,
		StatusCode:         parsed.Header.Meta.StatusCode,
		CandidateCount:     parsed.Header.Meta.RoutingCandidateCount,
		Events:             []map[string]any{},
	}
	for _, event := range parsed.Events {
		if !strings.HasPrefix(event.Type, "routing.") {
			continue
		}
		eventMap := routingEventMap(event)
		out.Events = append(out.Events, eventMap)
		switch event.Type {
		case "routing.classified":
			if out.Model == "" {
				out.Model, _ = event.Attributes["model"].(string)
			}
			if out.Endpoint == "" {
				out.Endpoint, _ = event.Attributes["endpoint"].(string)
			}
			if out.RoutingPolicy == "" {
				out.RoutingPolicy, _ = event.Attributes["routing_policy"].(string)
			}
			out.FallbackPolicy, _ = event.Attributes["fallback_policy"].(string)
		case "routing.candidates":
			out.AvailableCount = intFromAny(event.Attributes["available_count"])
			out.Candidates = mapsFromAny(event.Attributes["candidates"])
			if out.CandidateCount == 0 {
				out.CandidateCount = len(out.Candidates)
			}
		case "routing.selected":
			if out.SelectedUpstreamID == "" {
				out.SelectedUpstreamID, _ = event.Attributes["upstream_id"].(string)
			}
			identity := routingIdentityFromAttrs(event.Attributes)
			if out.SelectedRouteTargetID == "" {
				out.SelectedRouteTargetID = identity.RouteTargetID
			}
			if out.SelectedChannelID == "" {
				out.SelectedChannelID = identity.ChannelID
			}
			if out.SelectedCredentialID == "" {
				out.SelectedCredentialID = identity.CredentialID
			}
		case "routing.filtered":
			if out.FailureReason == "" {
				out.FailureReason, _ = event.Attributes["routing_failure_reason"].(string)
			}
		case "routing.outcome":
			identity := routingIdentityFromAttrs(event.Attributes)
			if out.SelectedRouteTargetID == "" {
				out.SelectedRouteTargetID = identity.RouteTargetID
			}
			if out.SelectedChannelID == "" {
				out.SelectedChannelID = identity.ChannelID
			}
			if out.SelectedCredentialID == "" {
				out.SelectedCredentialID = identity.CredentialID
			}
			out.Outcome = eventMap
		}
	}
	return nil, &out, nil
}

func (a *serverAPI) queryStickyRouting(ctx context.Context, req *mcp.CallToolRequest, in *queryStickyRoutingInput) (*mcp.CallToolResult, *stickyRoutingOutput, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	page := normalizePage(in.Page)
	pageSize := normalizePageSize(in.PageSize)
	status := strings.TrimSpace(in.Status)
	upstreamID := strings.TrimSpace(in.UpstreamID)
	previousUpstreamID := strings.TrimSpace(in.PreviousUpstreamID)
	fingerprint := strings.TrimSpace(in.StickyKeyFingerprint)

	out := &stickyRoutingOutput{
		Page:                 page,
		PageSize:             pageSize,
		Status:               status,
		UpstreamID:           upstreamID,
		PreviousUpstreamID:   previousUpstreamID,
		StickyKeyFingerprint: fingerprint,
		RefreshedAt:          time.Now().UTC(),
	}
	entries, err := a.allTraceEntries()
	if err != nil {
		return nil, nil, err
	}
	out.Scanned = len(entries)

	var matches []stickyRoutingRow
	for _, entry := range entries {
		content, err := os.ReadFile(entry.LogPath)
		if err != nil {
			out.Skipped++
			out.Errors = append(out.Errors, fmt.Sprintf("%s: read cassette: %v", entry.ID, err))
			continue
		}
		parsed, err := recordfile.ParsePrelude(content)
		if err != nil {
			out.Skipped++
			out.Errors = append(out.Errors, fmt.Sprintf("%s: parse prelude failed", entry.ID))
			continue
		}
		for _, event := range parsed.Events {
			row, ok := stickyRoutingRowFromEvent(entry, event)
			if !ok {
				continue
			}
			if !stickyRoutingRowMatches(row, status, upstreamID, previousUpstreamID, fingerprint) {
				continue
			}
			matches = append(matches, row)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if !matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].CreatedAt.After(matches[j].CreatedAt)
		}
		if matches[i].TraceID != matches[j].TraceID {
			return matches[i].TraceID < matches[j].TraceID
		}
		return matches[i].StickyStatus < matches[j].StickyStatus
	})
	out.Total = len(matches)
	out.TotalPages = totalPages(out.Total, pageSize)
	start := (page - 1) * pageSize
	if start < len(matches) {
		end := start + pageSize
		if end > len(matches) {
			end = len(matches)
		}
		out.Items = matches[start:end]
	} else {
		out.Items = []stickyRoutingRow{}
	}
	return nil, out, nil
}

func (a *serverAPI) listTraceFindings(ctx context.Context, req *mcp.CallToolRequest, in *listTraceFindingsInput) (*mcp.CallToolResult, map[string]any, error) {
	return a.traceFindings(ctx, in.TraceID, in.Severity, in.Category)
}

func (a *serverAPI) queryDangerousToolCalls(ctx context.Context, req *mcp.CallToolRequest, in *queryDangerousToolCallsInput) (*mcp.CallToolResult, map[string]any, error) {
	out, err := a.mergeTraceFindingCategories(ctx, in.TraceID, "", []string{
		"dangerous_command",
		"filesystem_destructive_operation",
		"unsafe_code_execution",
		"network_exfiltration",
		"unexpected_tool_call",
	})
	return nil, out, err
}

func (a *serverAPI) querySensitiveDataFindings(ctx context.Context, req *mcp.CallToolRequest, in *querySensitiveDataFindingsInput) (*mcp.CallToolResult, map[string]any, error) {
	out, err := a.mergeTraceFindingCategories(ctx, in.TraceID, "", []string{
		"credential_leak",
		"sensitive_data",
	})
	return nil, out, err
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
			traceID, _ := item["id"].(string)
			entry, err := a.lookupTrace(traceID)
			if err != nil {
				return nil, nil, err
			}
			routingEvidence, err := a.traceRoutingEvidence(entry)
			if err != nil {
				return nil, nil, err
			}
			reason, routingEventReason := failureReasonFromEvidence(entry, int(statusCode), errText, routingEvidence)
			if reason != "" {
				item["failure_reason"] = reason
			}
			if routingEventReason != "" {
				item["routing_event_reason"] = routingEventReason
			}
			addRoutingIdentityFields(item, routingEvidence.Identity)
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
	byRouteTarget := map[string]int{}
	byChannel := map[string]int{}
	byCredential := map[string]int{}

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
		routingEvidence, err := a.traceRoutingEvidence(entry)
		if err != nil {
			return nil, nil, err
		}
		reason, routingEventReason := failureReasonFromEvidence(entry, int(statusCode), errorText, routingEvidence)
		incrementCount(byReason, reason)
		incrementCount(byStatus, fmt.Sprintf("%d", int(statusCode)))
		incrementCount(byModel, entry.Header.Meta.Model)
		incrementCount(byProvider, entry.Header.Meta.Provider)
		incrementCount(byEndpoint, firstNonEmpty(entry.Header.Meta.Endpoint, entry.Header.Meta.URL))
		incrementCount(byUpstream, entry.Header.Meta.SelectedUpstreamID)
		incrementCount(byRouteTarget, routingEvidence.Identity.RouteTargetID)
		incrementCount(byChannel, routingEvidence.Identity.ChannelID)
		incrementCount(byCredential, routingEvidence.Identity.CredentialID)
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
			RouteTargetID:      routingEvidence.Identity.RouteTargetID,
			ChannelID:          routingEvidence.Identity.ChannelID,
			CredentialID:       routingEvidence.Identity.CredentialID,
			RoutingEventReason: routingEventReason,
		})
	}
	out.Returned = len(out.TopFailures)
	out.ByReason = toFailureSummaryItems(byReason, limit)
	out.ByStatus = toFailureSummaryItems(byStatus, limit)
	out.ByModel = toFailureSummaryItems(byModel, limit)
	out.ByProvider = toFailureSummaryItems(byProvider, limit)
	out.ByEndpoint = toFailureSummaryItems(byEndpoint, limit)
	out.ByUpstream = toFailureSummaryItems(byUpstream, limit)
	out.ByRouteTarget = toFailureSummaryItems(byRouteTarget, limit)
	out.ByChannel = toFailureSummaryItems(byChannel, limit)
	out.ByCredential = toFailureSummaryItems(byCredential, limit)
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

func failureReasonFromEvidence(entry store.LogEntry, statusCode int, errorText string, evidence routingEvidence) (reason string, routingEventReason string) {
	if evidence.FailureReason != "" {
		return evidence.FailureReason, evidence.FailureReason
	}
	if reason := strings.TrimSpace(entry.Header.Meta.RoutingFailureReason); reason != "" {
		return reason, ""
	}
	return classifyFailureReason(statusCode, errorText), ""
}

type routingIdentity struct {
	RouteTargetID string
	ChannelID     string
	CredentialID  string
}

type routingEvidence struct {
	FailureReason string
	Identity      routingIdentity
}

func (a *serverAPI) traceRoutingEvidence(entry store.LogEntry) (routingEvidence, error) {
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		return routingEvidence{}, fmt.Errorf("read trace cassette %q: %w", entry.ID, err)
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return routingEvidence{}, fmt.Errorf("parse trace prelude %q: %w", entry.ID, err)
	}
	evidence := routingEvidence{}
	for _, event := range parsed.Events {
		switch event.Type {
		case "routing.selected", "routing.outcome":
			evidence.Identity = evidence.Identity.merge(routingIdentityFromAttrs(event.Attributes))
		}
	}
	for _, event := range parsed.Events {
		if event.Type != "routing.filtered" {
			continue
		}
		if reason, _ := event.Attributes["routing_failure_reason"].(string); strings.TrimSpace(reason) != "" {
			evidence.FailureReason = strings.TrimSpace(reason)
			if isEmptyRoutingIdentity(evidence.Identity) {
				evidence.Identity = routingIdentityFromAttrs(event.Attributes)
			}
			return evidence, nil
		}
	}
	for _, event := range parsed.Events {
		if event.Type == "routing.retry_queue_saturated" {
			evidence.FailureReason = "retry_queue_saturated"
			return evidence, nil
		}
	}
	return evidence, nil
}

func (a *serverAPI) listSystemEvents(ctx context.Context, req *mcp.CallToolRequest, in *listSystemEventsInput) (*mcp.CallToolResult, *systemEventListOutput, error) {
	values := systemEventValues(in.Page, in.PageSize, in.Status, in.Severity, in.Source, in.Category, in.Query, in.Window)
	var out systemEventListOutput
	if err := a.getJSON(ctx, "/api/events", values, &out); err != nil {
		return nil, nil, err
	}
	return nil, &out, nil
}

func (a *serverAPI) getSystemEvent(ctx context.Context, req *mcp.CallToolRequest, in *getSystemEventInput) (*mcp.CallToolResult, map[string]any, error) {
	eventID := strings.TrimSpace(in.EventID)
	if eventID == "" {
		return nil, nil, fmt.Errorf("event_id is required")
	}
	if a.store == nil {
		return nil, nil, fmt.Errorf("store not configured")
	}
	event, err := a.store.GetSystemEvent(eventID)
	if err != nil {
		return nil, nil, err
	}
	out := systemEventMap(event, in.IncludeDetails)
	return nil, out, nil
}

func (a *serverAPI) summarizeSystemEvents(ctx context.Context, req *mcp.CallToolRequest, in *summarizeSystemEventsInput) (*mcp.CallToolResult, *systemEventSummaryOutput, error) {
	values := url.Values{}
	setIfNotEmpty(values, "window", in.Window)
	var out systemEventSummaryOutput
	if err := a.getJSON(ctx, "/api/events/summary", values, &out); err != nil {
		return nil, nil, err
	}

	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = store.SystemEventStatusUnread
	}
	listValues := systemEventValues(1, 5, status, "", "", "", "", in.Window)
	var page systemEventListOutput
	if err := a.getJSON(ctx, "/api/events", listValues, &page); err != nil {
		return nil, nil, err
	}
	out.Newest = conciseSystemEvents(page.Items, false)
	return nil, &out, nil
}

func (a *serverAPI) queryUnreadSystemEvents(ctx context.Context, req *mcp.CallToolRequest, in *queryUnreadSystemEventsInput) (*mcp.CallToolResult, *systemEventListOutput, error) {
	limit := normalizePageSize(firstPositive(in.Limit, 20))
	values := systemEventValues(1, maxPageSize, store.SystemEventStatusUnread, "", "", "", "", "all")
	var page systemEventListOutput
	if err := a.getJSON(ctx, "/api/events", values, &page); err != nil {
		return nil, nil, err
	}
	minRank := severityRank(firstNonEmpty(in.MinSeverity, "warning"))
	filtered := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		severity, _ := item["severity"].(string)
		if severityRank(severity) >= minRank {
			delete(item, "details_json")
			filtered = append(filtered, item)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		leftSeverity, _ := filtered[i]["severity"].(string)
		rightSeverity, _ := filtered[j]["severity"].(string)
		if severityRank(leftSeverity) != severityRank(rightSeverity) {
			return severityRank(leftSeverity) > severityRank(rightSeverity)
		}
		leftSeen, _ := filtered[i]["last_seen_at"].(string)
		rightSeen, _ := filtered[j]["last_seen_at"].(string)
		return leftSeen > rightSeen
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	page.Items = filtered
	page.Page = 1
	page.PageSize = limit
	page.Total = len(filtered)
	page.TotalPages = totalPages(len(filtered), limit)
	return nil, &page, nil
}

func (a *serverAPI) reanalyzeTrace(ctx context.Context, req *mcp.CallToolRequest, in *reanalyzeTraceInput) (*mcp.CallToolResult, map[string]any, error) {
	traceID := strings.TrimSpace(in.TraceID)
	if traceID == "" {
		return nil, nil, fmt.Errorf("trace_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	reparse, scan := defaultReanalysisSteps(in.Reparse, in.Scan)
	svc := reanalysis.New(a.store, reanalysis.Options{})
	if in.Async {
		if in.RepairUsage {
			job, err := svc.EnqueueTraceRepairUsage(traceID)
			if err != nil {
				return nil, nil, err
			}
			return nil, analysisJobMap(job), nil
		}
		job, err := enqueueTraceSteps(svc, traceID, reparse, scan)
		if err != nil {
			return nil, nil, err
		}
		return nil, analysisJobMap(job), nil
	}
	var result reanalysis.Result
	var err error
	if in.RepairUsage {
		if result, err = svc.RepairTraceUsage(ctx, traceID, reanalysis.RepairUsageOptions{}); err != nil {
			return nil, nil, err
		}
	}
	switch {
	case reparse && scan:
		result, err = svc.ReanalyzeTrace(ctx, traceID)
	case reparse:
		result, err = svc.ReparseTrace(ctx, traceID, reanalysis.TraceOptions{})
	case scan:
		result, err = svc.RescanTrace(ctx, traceID)
	default:
		if result.Job.ID == 0 {
			return nil, nil, fmt.Errorf("at least one reanalysis step is required")
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return nil, reanalysisResultMap(result), nil
}

func (a *serverAPI) reanalyzeSession(ctx context.Context, req *mcp.CallToolRequest, in *reanalyzeSessionInput) (*mcp.CallToolResult, map[string]any, error) {
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("session_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	reparse, scan := defaultReanalysisSteps(in.Reparse, in.Scan)
	svc := reanalysis.New(a.store, reanalysis.Options{})
	if in.Async {
		job, err := svc.EnqueueSessionReanalyze(sessionID, reanalysis.SessionOptions{Reparse: reparse, Scan: scan})
		if err != nil {
			return nil, nil, err
		}
		return nil, analysisJobMap(job), nil
	}
	result, err := svc.ReanalyzeSession(ctx, sessionID, reanalysis.SessionOptions{Reparse: reparse, Scan: scan})
	if err != nil {
		return nil, nil, err
	}
	return nil, reanalysisResultMap(result), nil
}

func (a *serverAPI) listAnalysisJobs(ctx context.Context, req *mcp.CallToolRequest, in *listAnalysisJobsInput) (*mcp.CallToolResult, map[string]any, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	jobs, err := a.store.ListAnalysisJobs(in.Status, in.TargetType, in.TargetID, limit)
	if err != nil {
		return nil, nil, err
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, analysisJobMap(job))
	}
	return nil, map[string]any{"items": items, "total": len(items)}, nil
}

func (a *serverAPI) getAnalysisJob(ctx context.Context, req *mcp.CallToolRequest, in *getAnalysisJobInput) (*mcp.CallToolResult, map[string]any, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	if in.JobID <= 0 {
		return nil, nil, fmt.Errorf("job_id is required")
	}
	job, err := a.store.GetAnalysisJob(in.JobID)
	if err != nil {
		return nil, nil, err
	}
	return nil, analysisJobMap(job), nil
}

func (a *serverAPI) traceFindings(ctx context.Context, traceID string, severity string, category string) (*mcp.CallToolResult, map[string]any, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return nil, nil, fmt.Errorf("trace_id is required")
	}
	values := url.Values{}
	setIfNotEmpty(values, "severity", severity)
	setIfNotEmpty(values, "category", category)

	var out map[string]any
	if err := a.getJSON(ctx, "/api/traces/"+url.PathEscape(traceID)+"/findings", values, &out); err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func (a *serverAPI) mergeTraceFindingCategories(ctx context.Context, traceID string, severity string, categories []string) (map[string]any, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return nil, fmt.Errorf("trace_id is required")
	}
	out := map[string]any{
		"id":    traceID,
		"items": []any{},
		"total": float64(0),
	}
	var merged []any
	for _, category := range categories {
		_, payload, err := a.traceFindings(ctx, traceID, severity, category)
		if err != nil {
			return nil, err
		}
		items, _ := payload["items"].([]any)
		merged = append(merged, items...)
	}
	out["items"] = merged
	out["total"] = float64(len(merged))
	return out, nil
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

func (a *serverAPI) allTraceEntries() ([]store.LogEntry, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, err
	}
	var entries []store.LogEntry
	for page := 1; ; page++ {
		result, err := a.store.ListPage(page, maxPageSize, store.ListFilter{})
		if err != nil {
			return nil, err
		}
		entries = append(entries, result.Items...)
		if result.TotalPages == 0 || page >= result.TotalPages {
			break
		}
	}
	return entries, nil
}

func (a *serverAPI) requireStoreSync() error {
	if a.store == nil {
		return fmt.Errorf("store not configured")
	}
	return nil
}

func routingEventMap(event recordfile.RecordEvent) map[string]any {
	out := map[string]any{
		"type":       event.Type,
		"time":       event.Time,
		"attributes": event.Attributes,
	}
	if event.Message != "" {
		out["message"] = event.Message
	}
	if event.StatusCode > 0 {
		out["status_code"] = event.StatusCode
	}
	return out
}

func stickyRoutingRowFromEvent(entry store.LogEntry, event recordfile.RecordEvent) (stickyRoutingRow, bool) {
	if !strings.HasPrefix(event.Type, "routing.sticky.") {
		return stickyRoutingRow{}, false
	}
	attrs := event.Attributes
	if attrs == nil {
		attrs = map[string]interface{}{}
	}
	status := strings.TrimSpace(strings.TrimPrefix(event.Type, "routing.sticky."))
	if attrStatus, _ := attrs["sticky_status"].(string); strings.TrimSpace(attrStatus) != "" {
		status = strings.TrimSpace(attrStatus)
	}
	row := stickyRoutingRow{
		TraceID:               entry.ID,
		CreatedAt:             entry.Header.Meta.Time,
		StickyStatus:          status,
		CassettePath:          entry.LogPath,
		LogPath:               entry.LogPath,
		UpstreamID:            stringAttr(attrs, "upstream_id"),
		PreviousUpstreamID:    stringAttr(attrs, "previous_upstream_id"),
		RouteTargetID:         stringAttr(attrs, "route_target_id"),
		PreviousRouteTargetID: stringAttr(attrs, "previous_route_target_id"),
		ChannelID:             stringAttr(attrs, "channel_id"),
		PreviousChannelID:     stringAttr(attrs, "previous_channel_id"),
		CredentialID:          stringAttr(attrs, "credential_id"),
		PreviousCredentialID:  stringAttr(attrs, "previous_credential_id"),
		StickyKeyFingerprint:  stringAttr(attrs, "sticky_key_fingerprint"),
	}
	return row, true
}

func stickyRoutingRowMatches(row stickyRoutingRow, status string, upstreamID string, previousUpstreamID string, fingerprint string) bool {
	if status != "" && row.StickyStatus != status {
		return false
	}
	if upstreamID != "" && row.UpstreamID != upstreamID {
		return false
	}
	if previousUpstreamID != "" && row.PreviousUpstreamID != previousUpstreamID {
		return false
	}
	if fingerprint != "" && row.StickyKeyFingerprint != fingerprint {
		return false
	}
	return true
}

func stringAttr(attrs map[string]interface{}, key string) string {
	value, _ := attrs[key].(string)
	return strings.TrimSpace(value)
}

func routingIdentityFromAttrs(attrs map[string]interface{}) routingIdentity {
	if len(attrs) == 0 {
		return routingIdentity{}
	}
	return routingIdentity{
		RouteTargetID: stringAttr(attrs, "route_target_id"),
		ChannelID:     stringAttr(attrs, "channel_id"),
		CredentialID:  stringAttr(attrs, "credential_id"),
	}
}

func (identity routingIdentity) merge(next routingIdentity) routingIdentity {
	if identity.RouteTargetID == "" {
		identity.RouteTargetID = next.RouteTargetID
	}
	if identity.ChannelID == "" {
		identity.ChannelID = next.ChannelID
	}
	if identity.CredentialID == "" {
		identity.CredentialID = next.CredentialID
	}
	return identity
}

func isEmptyRoutingIdentity(identity routingIdentity) bool {
	return identity.RouteTargetID == "" && identity.ChannelID == "" && identity.CredentialID == ""
}

func addRoutingIdentityFields(item map[string]any, identity routingIdentity) {
	if identity.RouteTargetID != "" {
		item["route_target_id"] = identity.RouteTargetID
	}
	if identity.ChannelID != "" {
		item["channel_id"] = identity.ChannelID
	}
	if identity.CredentialID != "" {
		item["credential_id"] = identity.CredentialID
	}
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func mapsFromAny(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]interface{}); ok {
			out := make([]map[string]any, 0, len(typed))
			out = append(out, typed...)
			return out
		}
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
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

func systemEventValues(page int, pageSize int, status string, severity string, source string, category string, query string, window string) url.Values {
	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", normalizePage(page)))
	values.Set("page_size", fmt.Sprintf("%d", normalizePageSize(pageSize)))
	setIfNotEmpty(values, "status", status)
	setIfNotEmpty(values, "severity", severity)
	setIfNotEmpty(values, "source", source)
	setIfNotEmpty(values, "category", category)
	setIfNotEmpty(values, "q", query)
	setIfNotEmpty(values, "window", window)
	return values
}

func conciseSystemEvents(items []map[string]any, includeDetails bool) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		copyItem := make(map[string]any, len(item))
		for key, value := range item {
			if key == "details_json" && !includeDetails {
				continue
			}
			copyItem[key] = value
		}
		out = append(out, copyItem)
	}
	return out
}

func defaultReanalysisSteps(reparse bool, scan bool) (bool, bool) {
	if !reparse && !scan {
		return true, true
	}
	return reparse, scan
}

func enqueueTraceSteps(svc *reanalysis.Service, traceID string, reparse bool, scan bool) (store.AnalysisJobRecord, error) {
	switch {
	case reparse && scan:
		return svc.EnqueueTraceReanalyze(traceID)
	case reparse:
		return svc.EnqueueTraceReparse(traceID, reanalysis.TraceOptions{})
	case scan:
		return svc.EnqueueTraceRescan(traceID)
	default:
		return store.AnalysisJobRecord{}, fmt.Errorf("at least one reanalysis step is required")
	}
}

func reanalysisResultMap(result reanalysis.Result) map[string]any {
	out := map[string]any{
		"job": analysisJobMap(result.Job),
	}
	if result.Usage != nil {
		out["usage"] = result.Usage
	}
	if result.Observation != nil {
		out["observation"] = result.Observation
		out["request_nodes"] = result.RequestNodes
		out["response_nodes"] = result.ResponseNodes
		out["stream_events"] = result.StreamEvents
	}
	if result.Findings != nil {
		out["findings"] = result.Findings
	}
	if result.Session != nil {
		out["session"] = result.Session
	}
	if result.Batch != nil {
		out["batch"] = result.Batch
	}
	return out
}

func analysisJobMap(job store.AnalysisJobRecord) map[string]any {
	return map[string]any{
		"id":          job.ID,
		"job_type":    job.JobType,
		"target_type": job.TargetType,
		"target_id":   job.TargetID,
		"status":      job.Status,
		"steps":       jsonRawOrString(job.StepsJSON),
		"request":     jsonRawOrString(job.RequestJSON),
		"result":      jsonRawOrString(job.ResultJSON),
		"last_error":  job.LastError,
		"attempts":    job.Attempts,
		"created_at":  job.CreatedAt,
		"updated_at":  job.UpdatedAt,
		"started_at":  job.StartedAt,
		"finished_at": job.FinishedAt,
	}
}

func jsonRawOrString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return value
	}
	return out
}

func systemEventMap(event store.SystemEvent, includeDetails bool) map[string]any {
	out := map[string]any{
		"id":               event.ID,
		"fingerprint":      event.Fingerprint,
		"source":           event.Source,
		"category":         event.Category,
		"severity":         event.Severity,
		"status":           event.Status,
		"title":            event.Title,
		"message":          event.Message,
		"trace_id":         event.TraceID,
		"session_id":       event.SessionID,
		"job_id":           event.JobID,
		"upstream_id":      event.UpstreamID,
		"model":            event.Model,
		"occurrence_count": event.OccurrenceCount,
		"first_seen_at":    event.FirstSeenAt,
		"last_seen_at":     event.LastSeenAt,
		"created_at":       event.CreatedAt,
		"updated_at":       event.UpdatedAt,
	}
	if !event.ReadAt.IsZero() {
		out["read_at"] = event.ReadAt
	}
	if !event.ResolvedAt.IsZero() {
		out["resolved_at"] = event.ResolvedAt
	}
	if includeDetails {
		out["details_json"] = json.RawMessage(firstNonEmpty(string(event.DetailsJSON), "{}"))
	}
	return out
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 4
	case "error":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func totalPages(total int, pageSize int) int {
	if pageSize <= 0 || total <= 0 {
		return 0
	}
	pages := total / pageSize
	if total%pageSize != 0 {
		pages++
	}
	return pages
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
	case strings.Contains(text, "retry wait queue saturated") || strings.Contains(text, "retry_queue_saturated"):
		return "retry_queue_saturated"
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
