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

	"github.com/kingfs/llm-tracelab/internal/evals"
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

type createDatasetFromTracesInput struct {
	Name        string   `json:"name" jsonschema:"dataset name"`
	Description string   `json:"description,omitempty" jsonschema:"dataset description"`
	TraceIDs    []string `json:"trace_ids" jsonschema:"trace identifiers to include"`
	Note        string   `json:"note,omitempty" jsonschema:"optional note stored on appended examples"`
}

type createDatasetFromSessionInput struct {
	Name        string `json:"name" jsonschema:"dataset name"`
	Description string `json:"description,omitempty" jsonschema:"dataset description"`
	SessionID   string `json:"session_id" jsonschema:"session identifier to source from"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum traces to include from the session, max 100"`
	Note        string `json:"note,omitempty" jsonschema:"optional note stored on appended examples"`
}

type appendDatasetExamplesInput struct {
	DatasetID string   `json:"dataset_id" jsonschema:"dataset identifier"`
	TraceIDs  []string `json:"trace_ids" jsonschema:"trace identifiers to append"`
	Note      string   `json:"note,omitempty" jsonschema:"optional note stored on appended examples"`
}

type getDatasetInput struct {
	DatasetID string `json:"dataset_id" jsonschema:"dataset identifier"`
}

type runEvalOnDatasetInput struct {
	DatasetID    string `json:"dataset_id" jsonschema:"dataset identifier"`
	EvaluatorSet string `json:"evaluator_set,omitempty" jsonschema:"optional evaluator profile name, default baseline_v2"`
}

type runEvalOnTracesInput struct {
	TraceIDs     []string `json:"trace_ids" jsonschema:"trace identifiers to evaluate"`
	EvaluatorSet string   `json:"evaluator_set,omitempty" jsonschema:"optional evaluator profile name, default baseline_v2"`
}

type evaluatorProfilesInput struct{}

type listEvalRunsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum runs to return, default 20"`
}

type getEvalRunInput struct {
	EvalRunID string `json:"eval_run_id" jsonschema:"evaluation run identifier"`
}

type listScoresInput struct {
	TraceID         string `json:"trace_id,omitempty" jsonschema:"optional trace filter"`
	SessionID       string `json:"session_id,omitempty" jsonschema:"optional session filter"`
	DatasetID       string `json:"dataset_id,omitempty" jsonschema:"optional dataset filter"`
	EvalRunID       string `json:"eval_run_id,omitempty" jsonschema:"optional eval run filter"`
	ExperimentRunID string `json:"experiment_run_id,omitempty" jsonschema:"optional experiment run filter"`
	Limit           int    `json:"limit,omitempty" jsonschema:"maximum scores to return, default 200"`
}

type compareEvalRunsInput struct {
	BaselineEvalRunID  string `json:"baseline_eval_run_id" jsonschema:"baseline evaluation run identifier"`
	CandidateEvalRunID string `json:"candidate_eval_run_id" jsonschema:"candidate evaluation run identifier"`
}

type createExperimentFromEvalRunsInput struct {
	Name               string `json:"name,omitempty" jsonschema:"optional experiment name"`
	Description        string `json:"description,omitempty" jsonschema:"optional experiment description"`
	BaselineEvalRunID  string `json:"baseline_eval_run_id" jsonschema:"baseline evaluation run identifier"`
	CandidateEvalRunID string `json:"candidate_eval_run_id" jsonschema:"candidate evaluation run identifier"`
}

type listExperimentRunsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum runs to return, default 20"`
}

type getExperimentRunInput struct {
	ExperimentRunID string `json:"experiment_run_id" jsonschema:"experiment run identifier"`
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

type datasetSummary struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ExampleCount int       `json:"example_count"`
}

type datasetExample struct {
	TraceID    string    `json:"trace_id"`
	Position   int       `json:"position"`
	AddedAt    time.Time `json:"added_at"`
	SourceType string    `json:"source_type,omitempty"`
	SourceID   string    `json:"source_id,omitempty"`
	Note       string    `json:"note,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	Model      string    `json:"model"`
	Provider   string    `json:"provider"`
	Endpoint   string    `json:"endpoint"`
	StatusCode int       `json:"status_code"`
	IsStream   bool      `json:"is_stream"`
	SessionID  string    `json:"session_id,omitempty"`
}

type datasetMutationOutput struct {
	Dataset datasetSummary `json:"dataset"`
	Added   int            `json:"added"`
	Skipped int            `json:"skipped"`
}

type listDatasetsOutput struct {
	Items       []datasetSummary `json:"items"`
	RefreshedAt time.Time        `json:"refreshed_at"`
}

type getDatasetOutput struct {
	Dataset     datasetSummary   `json:"dataset"`
	Examples    []datasetExample `json:"examples"`
	RefreshedAt time.Time        `json:"refreshed_at"`
}

type scoreView struct {
	ID           string    `json:"id"`
	TraceID      string    `json:"trace_id"`
	SessionID    string    `json:"session_id,omitempty"`
	DatasetID    string    `json:"dataset_id,omitempty"`
	EvalRunID    string    `json:"eval_run_id,omitempty"`
	RunRole      string    `json:"run_role,omitempty"`
	EvaluatorKey string    `json:"evaluator_key"`
	Value        float64   `json:"value"`
	Status       string    `json:"status"`
	Label        string    `json:"label"`
	Explanation  string    `json:"explanation"`
	CreatedAt    time.Time `json:"created_at"`
}

type evalRunView struct {
	ID           string    `json:"id"`
	DatasetID    string    `json:"dataset_id,omitempty"`
	SourceType   string    `json:"source_type,omitempty"`
	SourceID     string    `json:"source_id,omitempty"`
	EvaluatorSet string    `json:"evaluator_set"`
	CreatedAt    time.Time `json:"created_at"`
	CompletedAt  time.Time `json:"completed_at"`
	TraceCount   int       `json:"trace_count"`
	ScoreCount   int       `json:"score_count"`
	PassCount    int       `json:"pass_count"`
	FailCount    int       `json:"fail_count"`
}

type runEvalOutput struct {
	Run    evalRunView `json:"run"`
	Scores []scoreView `json:"scores"`
}

type listEvalRunsOutput struct {
	Items       []evalRunView `json:"items"`
	RefreshedAt time.Time     `json:"refreshed_at"`
}

type getEvalRunOutput struct {
	Run         evalRunView `json:"run"`
	Scores      []scoreView `json:"scores"`
	RefreshedAt time.Time   `json:"refreshed_at"`
}

type listScoresOutput struct {
	Items       []scoreView `json:"items"`
	RefreshedAt time.Time   `json:"refreshed_at"`
}

type evalRunComparisonSummary struct {
	ScoreCount   int     `json:"score_count"`
	PassCount    int     `json:"pass_count"`
	FailCount    int     `json:"fail_count"`
	PassRate     float64 `json:"pass_rate"`
	MatchedCount int     `json:"matched_count"`
}

type evaluatorComparison struct {
	EvaluatorKey      string  `json:"evaluator_key"`
	BaselineTotal     int     `json:"baseline_total"`
	BaselinePass      int     `json:"baseline_pass"`
	BaselinePassRate  float64 `json:"baseline_pass_rate"`
	CandidateTotal    int     `json:"candidate_total"`
	CandidatePass     int     `json:"candidate_pass"`
	CandidatePassRate float64 `json:"candidate_pass_rate"`
	PassRateDelta     float64 `json:"pass_rate_delta"`
	MatchedCount      int     `json:"matched_count"`
	ImprovementCount  int     `json:"improvement_count"`
	RegressionCount   int     `json:"regression_count"`
}

type scoreDeltaView struct {
	TraceID         string  `json:"trace_id"`
	EvaluatorKey    string  `json:"evaluator_key"`
	BaselineStatus  string  `json:"baseline_status"`
	CandidateStatus string  `json:"candidate_status"`
	BaselineValue   float64 `json:"baseline_value"`
	CandidateValue  float64 `json:"candidate_value"`
	ValueDelta      float64 `json:"value_delta"`
}

type compareEvalRunsOutput struct {
	Baseline         evalRunView              `json:"baseline"`
	Candidate        evalRunView              `json:"candidate"`
	BaselineSummary  evalRunComparisonSummary `json:"baseline_summary"`
	CandidateSummary evalRunComparisonSummary `json:"candidate_summary"`
	PassRateDelta    float64                  `json:"pass_rate_delta"`
	Evaluators       []evaluatorComparison    `json:"evaluators"`
	Improvements     []scoreDeltaView         `json:"improvements"`
	Regressions      []scoreDeltaView         `json:"regressions"`
	RefreshedAt      time.Time                `json:"refreshed_at"`
}

type experimentRunView struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name,omitempty"`
	Description         string    `json:"description,omitempty"`
	BaselineEvalRunID   string    `json:"baseline_eval_run_id"`
	CandidateEvalRunID  string    `json:"candidate_eval_run_id"`
	CreatedAt           time.Time `json:"created_at"`
	BaselineScoreCount  int       `json:"baseline_score_count"`
	CandidateScoreCount int       `json:"candidate_score_count"`
	BaselinePassRate    float64   `json:"baseline_pass_rate"`
	CandidatePassRate   float64   `json:"candidate_pass_rate"`
	PassRateDelta       float64   `json:"pass_rate_delta"`
	MatchedScoreCount   int       `json:"matched_score_count"`
	ImprovementCount    int       `json:"improvement_count"`
	RegressionCount     int       `json:"regression_count"`
}

type createExperimentOutput struct {
	Experiment experimentRunView     `json:"experiment"`
	Comparison compareEvalRunsOutput `json:"comparison"`
}

type listExperimentRunsOutput struct {
	Items       []experimentRunView `json:"items"`
	RefreshedAt time.Time           `json:"refreshed_at"`
}

type getExperimentRunOutput struct {
	Experiment  experimentRunView     `json:"experiment"`
	Comparison  compareEvalRunsOutput `json:"comparison"`
	RefreshedAt time.Time             `json:"refreshed_at"`
}

type evaluatorProfileView struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Deterministic    bool     `json:"deterministic"`
	TTFTBudgetMS     int      `json:"ttft_budget_ms,omitempty"`
	TotalTokenBudget int      `json:"total_token_budget,omitempty"`
	EvaluatorKeys    []string `json:"evaluator_keys"`
}

type listEvaluatorProfilesOutput struct {
	Items       []evaluatorProfileView `json:"items"`
	RefreshedAt time.Time              `json:"refreshed_at"`
}

type evaluatorAggregate struct {
	EvaluatorKey     string
	BaselineTotal    int
	BaselinePass     int
	CandidateTotal   int
	CandidatePass    int
	MatchedCount     int
	ImprovementCount int
	RegressionCount  int
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
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_dataset_from_traces",
		Description: "Create a local dataset from a list of trace IDs.",
	}, api.createDatasetFromTraces)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_dataset_from_session",
		Description: "Create a local dataset from traces in one session.",
	}, api.createDatasetFromSession)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "append_dataset_examples",
		Description: "Append more trace IDs to an existing local dataset.",
	}, api.appendDatasetExamples)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_datasets",
		Description: "List local datasets curated from recorded traces.",
	}, api.listDatasets)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_dataset",
		Description: "Get one dataset and its ordered examples.",
	}, api.getDataset)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "run_eval_on_dataset",
		Description: "Run the baseline deterministic evaluator set on one dataset.",
	}, api.runEvalOnDataset)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "run_eval_on_traces",
		Description: "Run the baseline deterministic evaluator set on explicit trace IDs.",
	}, api.runEvalOnTraces)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_evaluator_profiles",
		Description: "List built-in deterministic evaluator profiles and their thresholds.",
	}, api.listEvaluatorProfiles)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_eval_runs",
		Description: "List recent evaluation runs.",
	}, api.listEvalRuns)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_eval_run",
		Description: "Get one evaluation run and its recorded scores.",
	}, api.getEvalRun)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_scores",
		Description: "List recorded scores with optional trace/session/dataset/eval_run filters.",
	}, api.listScores)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "compare_eval_runs",
		Description: "Compare two evaluation runs and return aggregate pass-rate deltas plus per-trace improvements and regressions.",
	}, api.compareEvalRuns)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_experiment_from_eval_runs",
		Description: "Persist a local experiment run that links one baseline eval run and one candidate eval run.",
	}, api.createExperimentFromEvalRuns)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_experiment_runs",
		Description: "List recent persisted experiment runs.",
	}, api.listExperimentRuns)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_experiment_run",
		Description: "Get one persisted experiment run plus its derived comparison detail.",
	}, api.getExperimentRun)

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

func (a *serverAPI) createDatasetFromTraces(ctx context.Context, req *mcp.CallToolRequest, in *createDatasetFromTracesInput) (*mcp.CallToolResult, *datasetMutationOutput, error) {
	dataset, err := a.createDataset(in.Name, in.Description)
	if err != nil {
		return nil, nil, err
	}
	added, skipped, err := a.store.AppendDatasetExamples(dataset.ID, in.TraceIDs, "trace_list", "", in.Note)
	if err != nil {
		return nil, nil, err
	}
	updated, err := a.store.GetDataset(dataset.ID)
	if err != nil {
		return nil, nil, err
	}
	return nil, &datasetMutationOutput{
		Dataset: toDatasetSummary(updated),
		Added:   added,
		Skipped: skipped,
	}, nil
}

func (a *serverAPI) createDatasetFromSession(ctx context.Context, req *mcp.CallToolRequest, in *createDatasetFromSessionInput) (*mcp.CallToolResult, *datasetMutationOutput, error) {
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
		limit = len(sessionDetail.Traces)
	}
	if limit > 100 {
		limit = 100
	}
	traceIDs := make([]string, 0, min(limit, len(sessionDetail.Traces)))
	for i, entry := range sessionDetail.Traces {
		if i >= limit {
			break
		}
		traceIDs = append(traceIDs, entry.ID)
	}
	dataset, err := a.createDataset(in.Name, in.Description)
	if err != nil {
		return nil, nil, err
	}
	added, skipped, err := a.store.AppendDatasetExamples(dataset.ID, traceIDs, "session", sessionID, in.Note)
	if err != nil {
		return nil, nil, err
	}
	updated, err := a.store.GetDataset(dataset.ID)
	if err != nil {
		return nil, nil, err
	}
	return nil, &datasetMutationOutput{
		Dataset: toDatasetSummary(updated),
		Added:   added,
		Skipped: skipped,
	}, nil
}

func (a *serverAPI) appendDatasetExamples(ctx context.Context, req *mcp.CallToolRequest, in *appendDatasetExamplesInput) (*mcp.CallToolResult, *datasetMutationOutput, error) {
	datasetID := strings.TrimSpace(in.DatasetID)
	if datasetID == "" {
		return nil, nil, fmt.Errorf("dataset_id is required")
	}
	added, skipped, err := a.store.AppendDatasetExamples(datasetID, in.TraceIDs, "trace_list", "", in.Note)
	if err != nil {
		return nil, nil, err
	}
	updated, err := a.store.GetDataset(datasetID)
	if err != nil {
		return nil, nil, err
	}
	return nil, &datasetMutationOutput{
		Dataset: toDatasetSummary(updated),
		Added:   added,
		Skipped: skipped,
	}, nil
}

func (a *serverAPI) listDatasets(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *listDatasetsOutput, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	records, err := a.store.ListDatasets()
	if err != nil {
		return nil, nil, err
	}
	out := &listDatasetsOutput{RefreshedAt: time.Now().UTC()}
	for _, record := range records {
		out.Items = append(out.Items, toDatasetSummary(record))
	}
	return nil, out, nil
}

func (a *serverAPI) getDataset(ctx context.Context, req *mcp.CallToolRequest, in *getDatasetInput) (*mcp.CallToolResult, *getDatasetOutput, error) {
	datasetID := strings.TrimSpace(in.DatasetID)
	if datasetID == "" {
		return nil, nil, fmt.Errorf("dataset_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	record, err := a.store.GetDataset(datasetID)
	if err != nil {
		return nil, nil, err
	}
	examples, err := a.store.GetDatasetExamples(datasetID)
	if err != nil {
		return nil, nil, err
	}
	out := &getDatasetOutput{
		Dataset:     toDatasetSummary(record),
		RefreshedAt: time.Now().UTC(),
	}
	for _, example := range examples {
		out.Examples = append(out.Examples, datasetExample{
			TraceID:    example.TraceID,
			Position:   example.Position,
			AddedAt:    example.AddedAt,
			SourceType: example.SourceType,
			SourceID:   example.SourceID,
			Note:       example.Note,
			RecordedAt: example.Trace.Header.Meta.Time,
			Model:      example.Trace.Header.Meta.Model,
			Provider:   example.Trace.Header.Meta.Provider,
			Endpoint:   firstNonEmpty(example.Trace.Header.Meta.Endpoint, example.Trace.Header.Meta.URL),
			StatusCode: example.Trace.Header.Meta.StatusCode,
			IsStream:   example.Trace.Header.Layout.IsStream,
			SessionID:  example.Trace.SessionID,
		})
	}
	return nil, out, nil
}

func (a *serverAPI) runEvalOnDataset(ctx context.Context, req *mcp.CallToolRequest, in *runEvalOnDatasetInput) (*mcp.CallToolResult, *runEvalOutput, error) {
	datasetID := strings.TrimSpace(in.DatasetID)
	if datasetID == "" {
		return nil, nil, fmt.Errorf("dataset_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	examples, err := a.store.GetDatasetExamples(datasetID)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]store.LogEntry, 0, len(examples))
	for _, example := range examples {
		entries = append(entries, example.Trace)
	}
	return a.runEval(entries, datasetID, "dataset", datasetID, in.EvaluatorSet)
}

func (a *serverAPI) runEvalOnTraces(ctx context.Context, req *mcp.CallToolRequest, in *runEvalOnTracesInput) (*mcp.CallToolResult, *runEvalOutput, error) {
	if len(in.TraceIDs) == 0 {
		return nil, nil, fmt.Errorf("trace_ids is required")
	}
	entries := make([]store.LogEntry, 0, len(in.TraceIDs))
	for _, traceID := range dedupeStrings(in.TraceIDs) {
		entry, err := a.lookupTrace(traceID)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}
	return a.runEval(entries, "", "trace_list", "", in.EvaluatorSet)
}

func (a *serverAPI) listEvaluatorProfiles(ctx context.Context, req *mcp.CallToolRequest, _ *evaluatorProfilesInput) (*mcp.CallToolResult, *listEvaluatorProfilesOutput, error) {
	out := &listEvaluatorProfilesOutput{RefreshedAt: time.Now().UTC()}
	for _, profile := range evals.ListProfiles() {
		out.Items = append(out.Items, evaluatorProfileView{
			Name:             profile.Name,
			Description:      profile.Description,
			Deterministic:    profile.Deterministic,
			TTFTBudgetMS:     profile.TTFTBudgetMS,
			TotalTokenBudget: profile.TotalTokenBudget,
			EvaluatorKeys:    append([]string(nil), profile.EvaluatorKeys...),
		})
	}
	return nil, out, nil
}

func (a *serverAPI) listEvalRuns(ctx context.Context, req *mcp.CallToolRequest, in *listEvalRunsInput) (*mcp.CallToolResult, *listEvalRunsOutput, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	records, err := a.store.ListEvalRuns(in.Limit)
	if err != nil {
		return nil, nil, err
	}
	out := &listEvalRunsOutput{RefreshedAt: time.Now().UTC()}
	for _, record := range records {
		out.Items = append(out.Items, toEvalRunView(record))
	}
	return nil, out, nil
}

func (a *serverAPI) getEvalRun(ctx context.Context, req *mcp.CallToolRequest, in *getEvalRunInput) (*mcp.CallToolResult, *getEvalRunOutput, error) {
	evalRunID := strings.TrimSpace(in.EvalRunID)
	if evalRunID == "" {
		return nil, nil, fmt.Errorf("eval_run_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	run, err := a.store.GetEvalRun(evalRunID)
	if err != nil {
		return nil, nil, err
	}
	scores, err := a.store.ListScores(store.ScoreFilter{EvalRunID: evalRunID}, 1000)
	if err != nil {
		return nil, nil, err
	}
	out := &getEvalRunOutput{
		Run:         toEvalRunView(run),
		RefreshedAt: time.Now().UTC(),
	}
	for _, score := range scores {
		out.Scores = append(out.Scores, toScoreView(score))
	}
	return nil, out, nil
}

func (a *serverAPI) listScores(ctx context.Context, req *mcp.CallToolRequest, in *listScoresInput) (*mcp.CallToolResult, *listScoresOutput, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.ExperimentRunID) != "" && strings.TrimSpace(in.EvalRunID) != "" {
		return nil, nil, fmt.Errorf("experiment_run_id and eval_run_id cannot be combined")
	}
	out := &listScoresOutput{RefreshedAt: time.Now().UTC()}
	if experimentRunID := strings.TrimSpace(in.ExperimentRunID); experimentRunID != "" {
		experiment, err := a.store.GetExperimentRun(experimentRunID)
		if err != nil {
			return nil, nil, err
		}
		baselineScores, err := a.store.ListScores(store.ScoreFilter{
			TraceID:   in.TraceID,
			SessionID: in.SessionID,
			DatasetID: in.DatasetID,
			EvalRunID: experiment.BaselineEvalRunID,
		}, in.Limit)
		if err != nil {
			return nil, nil, err
		}
		candidateScores, err := a.store.ListScores(store.ScoreFilter{
			TraceID:   in.TraceID,
			SessionID: in.SessionID,
			DatasetID: in.DatasetID,
			EvalRunID: experiment.CandidateEvalRunID,
		}, in.Limit)
		if err != nil {
			return nil, nil, err
		}
		for _, score := range baselineScores {
			out.Items = append(out.Items, toScoreViewWithRole(score, "baseline"))
		}
		for _, score := range candidateScores {
			out.Items = append(out.Items, toScoreViewWithRole(score, "candidate"))
		}
		sort.Slice(out.Items, func(i, j int) bool {
			if !out.Items[i].CreatedAt.Equal(out.Items[j].CreatedAt) {
				return out.Items[i].CreatedAt.After(out.Items[j].CreatedAt)
			}
			return out.Items[i].ID > out.Items[j].ID
		})
		return nil, out, nil
	}
	scores, err := a.store.ListScores(store.ScoreFilter{
		TraceID:   in.TraceID,
		SessionID: in.SessionID,
		DatasetID: in.DatasetID,
		EvalRunID: in.EvalRunID,
	}, in.Limit)
	if err != nil {
		return nil, nil, err
	}
	for _, score := range scores {
		out.Items = append(out.Items, toScoreView(score))
	}
	return nil, out, nil
}

func (a *serverAPI) compareEvalRuns(ctx context.Context, req *mcp.CallToolRequest, in *compareEvalRunsInput) (*mcp.CallToolResult, *compareEvalRunsOutput, error) {
	baselineEvalRunID := strings.TrimSpace(in.BaselineEvalRunID)
	candidateEvalRunID := strings.TrimSpace(in.CandidateEvalRunID)
	if baselineEvalRunID == "" {
		return nil, nil, fmt.Errorf("baseline_eval_run_id is required")
	}
	if candidateEvalRunID == "" {
		return nil, nil, fmt.Errorf("candidate_eval_run_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	out, err := a.compareEvalRunIDs(baselineEvalRunID, candidateEvalRunID)
	if err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func (a *serverAPI) createExperimentFromEvalRuns(ctx context.Context, req *mcp.CallToolRequest, in *createExperimentFromEvalRunsInput) (*mcp.CallToolResult, *createExperimentOutput, error) {
	baselineEvalRunID := strings.TrimSpace(in.BaselineEvalRunID)
	candidateEvalRunID := strings.TrimSpace(in.CandidateEvalRunID)
	if baselineEvalRunID == "" {
		return nil, nil, fmt.Errorf("baseline_eval_run_id is required")
	}
	if candidateEvalRunID == "" {
		return nil, nil, fmt.Errorf("candidate_eval_run_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	comparison, err := a.compareEvalRunIDs(baselineEvalRunID, candidateEvalRunID)
	if err != nil {
		return nil, nil, err
	}
	experiment, err := a.store.CreateExperimentRun(store.ExperimentRunRecord{
		Name:                strings.TrimSpace(in.Name),
		Description:         strings.TrimSpace(in.Description),
		BaselineEvalRunID:   baselineEvalRunID,
		CandidateEvalRunID:  candidateEvalRunID,
		BaselineScoreCount:  comparison.BaselineSummary.ScoreCount,
		CandidateScoreCount: comparison.CandidateSummary.ScoreCount,
		BaselinePassRate:    comparison.BaselineSummary.PassRate,
		CandidatePassRate:   comparison.CandidateSummary.PassRate,
		PassRateDelta:       comparison.PassRateDelta,
		MatchedScoreCount:   comparison.BaselineSummary.MatchedCount,
		ImprovementCount:    len(comparison.Improvements),
		RegressionCount:     len(comparison.Regressions),
	})
	if err != nil {
		return nil, nil, err
	}
	return nil, &createExperimentOutput{
		Experiment: toExperimentRunView(experiment),
		Comparison: *comparison,
	}, nil
}

func (a *serverAPI) listExperimentRuns(ctx context.Context, req *mcp.CallToolRequest, in *listExperimentRunsInput) (*mcp.CallToolResult, *listExperimentRunsOutput, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	records, err := a.store.ListExperimentRuns(in.Limit)
	if err != nil {
		return nil, nil, err
	}
	out := &listExperimentRunsOutput{RefreshedAt: time.Now().UTC()}
	for _, record := range records {
		out.Items = append(out.Items, toExperimentRunView(record))
	}
	return nil, out, nil
}

func (a *serverAPI) getExperimentRun(ctx context.Context, req *mcp.CallToolRequest, in *getExperimentRunInput) (*mcp.CallToolResult, *getExperimentRunOutput, error) {
	experimentRunID := strings.TrimSpace(in.ExperimentRunID)
	if experimentRunID == "" {
		return nil, nil, fmt.Errorf("experiment_run_id is required")
	}
	if err := a.requireStoreSync(); err != nil {
		return nil, nil, err
	}
	record, err := a.store.GetExperimentRun(experimentRunID)
	if err != nil {
		return nil, nil, err
	}
	comparison, err := a.compareEvalRunIDs(record.BaselineEvalRunID, record.CandidateEvalRunID)
	if err != nil {
		return nil, nil, err
	}
	return nil, &getExperimentRunOutput{
		Experiment:  toExperimentRunView(record),
		Comparison:  *comparison,
		RefreshedAt: time.Now().UTC(),
	}, nil
}

func (a *serverAPI) compareEvalRunIDs(baselineEvalRunID string, candidateEvalRunID string) (*compareEvalRunsOutput, error) {
	baselineRun, err := a.store.GetEvalRun(baselineEvalRunID)
	if err != nil {
		return nil, err
	}
	candidateRun, err := a.store.GetEvalRun(candidateEvalRunID)
	if err != nil {
		return nil, err
	}
	baselineScores, err := a.store.ListScores(store.ScoreFilter{EvalRunID: baselineEvalRunID}, 5000)
	if err != nil {
		return nil, err
	}
	candidateScores, err := a.store.ListScores(store.ScoreFilter{EvalRunID: candidateEvalRunID}, 5000)
	if err != nil {
		return nil, err
	}

	out := &compareEvalRunsOutput{
		Baseline:         toEvalRunView(baselineRun),
		Candidate:        toEvalRunView(candidateRun),
		BaselineSummary:  buildEvalRunComparisonSummary(baselineScores),
		CandidateSummary: buildEvalRunComparisonSummary(candidateScores),
		RefreshedAt:      time.Now().UTC(),
	}
	out.PassRateDelta = out.CandidateSummary.PassRate - out.BaselineSummary.PassRate

	evaluators, improvements, regressions := compareScoreSets(baselineScores, candidateScores)
	out.Evaluators = evaluators
	out.Improvements = improvements
	out.Regressions = regressions
	out.BaselineSummary.MatchedCount = matchedScoreCount(evaluators)
	out.CandidateSummary.MatchedCount = out.BaselineSummary.MatchedCount

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

func (a *serverAPI) lookupSession(sessionID string) (*sessionLookupResult, error) {
	if err := a.requireStoreSync(); err != nil {
		return nil, err
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

func (a *serverAPI) requireStoreSync() error {
	if a.store == nil {
		return fmt.Errorf("store not configured")
	}
	if err := a.store.Sync(); err != nil {
		return fmt.Errorf("sync error: %w", err)
	}
	return nil
}

func (a *serverAPI) createDataset(name string, description string) (store.DatasetRecord, error) {
	if err := a.requireStoreSync(); err != nil {
		return store.DatasetRecord{}, err
	}
	return a.store.CreateDataset(name, description)
}

func (a *serverAPI) runEval(entries []store.LogEntry, datasetID string, sourceType string, sourceID string, evaluatorSet string) (*mcp.CallToolResult, *runEvalOutput, error) {
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("no traces to evaluate")
	}
	selectedSet := strings.TrimSpace(evaluatorSet)
	if selectedSet == "" {
		selectedSet = evals.BaselineEvaluatorSet
	}
	if _, ok := evals.GetProfile(selectedSet); !ok {
		return nil, nil, fmt.Errorf("unknown evaluator_set %q", selectedSet)
	}
	run, err := a.store.CreateEvalRun(datasetID, sourceType, sourceID, selectedSet, len(entries))
	if err != nil {
		return nil, nil, err
	}
	out := &runEvalOutput{Run: toEvalRunView(run)}
	scoreCount := 0
	passCount := 0
	failCount := 0
	for _, entry := range entries {
		summary, err := replay.ReplayFile(entry.LogPath, replay.SummaryOptions{})
		if err != nil {
			return nil, nil, err
		}
		results, err := evals.Evaluate(entry, summary, selectedSet)
		if err != nil {
			return nil, nil, err
		}
		for _, result := range results {
			score, err := a.store.AddScore(store.ScoreRecord{
				TraceID:      entry.ID,
				SessionID:    entry.SessionID,
				DatasetID:    datasetID,
				EvalRunID:    run.ID,
				EvaluatorKey: result.EvaluatorKey,
				Value:        result.Value,
				Status:       result.Status,
				Label:        result.Label,
				Explanation:  result.Explanation,
			})
			if err != nil {
				return nil, nil, err
			}
			out.Scores = append(out.Scores, toScoreView(score))
			scoreCount++
			if score.Status == "pass" {
				passCount++
			} else {
				failCount++
			}
		}
	}
	if err := a.store.FinalizeEvalRun(run.ID, scoreCount, passCount, failCount); err != nil {
		return nil, nil, err
	}
	updated, err := a.store.GetEvalRun(run.ID)
	if err != nil {
		return nil, nil, err
	}
	out.Run = toEvalRunView(updated)
	return nil, out, nil
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

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func toDatasetSummary(record store.DatasetRecord) datasetSummary {
	return datasetSummary{
		ID:           record.ID,
		Name:         record.Name,
		Description:  record.Description,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
		ExampleCount: record.ExampleCount,
	}
}

func toEvalRunView(record store.EvalRunRecord) evalRunView {
	return evalRunView{
		ID:           record.ID,
		DatasetID:    record.DatasetID,
		SourceType:   record.SourceType,
		SourceID:     record.SourceID,
		EvaluatorSet: record.EvaluatorSet,
		CreatedAt:    record.CreatedAt,
		CompletedAt:  record.CompletedAt,
		TraceCount:   record.TraceCount,
		ScoreCount:   record.ScoreCount,
		PassCount:    record.PassCount,
		FailCount:    record.FailCount,
	}
}

func toScoreView(record store.ScoreRecord) scoreView {
	return toScoreViewWithRole(record, "")
}

func toScoreViewWithRole(record store.ScoreRecord, runRole string) scoreView {
	return scoreView{
		ID:           record.ID,
		TraceID:      record.TraceID,
		SessionID:    record.SessionID,
		DatasetID:    record.DatasetID,
		EvalRunID:    record.EvalRunID,
		RunRole:      strings.TrimSpace(runRole),
		EvaluatorKey: record.EvaluatorKey,
		Value:        record.Value,
		Status:       record.Status,
		Label:        record.Label,
		Explanation:  record.Explanation,
		CreatedAt:    record.CreatedAt,
	}
}

func toExperimentRunView(record store.ExperimentRunRecord) experimentRunView {
	return experimentRunView{
		ID:                  record.ID,
		Name:                record.Name,
		Description:         record.Description,
		BaselineEvalRunID:   record.BaselineEvalRunID,
		CandidateEvalRunID:  record.CandidateEvalRunID,
		CreatedAt:           record.CreatedAt,
		BaselineScoreCount:  record.BaselineScoreCount,
		CandidateScoreCount: record.CandidateScoreCount,
		BaselinePassRate:    record.BaselinePassRate,
		CandidatePassRate:   record.CandidatePassRate,
		PassRateDelta:       record.PassRateDelta,
		MatchedScoreCount:   record.MatchedScoreCount,
		ImprovementCount:    record.ImprovementCount,
		RegressionCount:     record.RegressionCount,
	}
}

func buildEvalRunComparisonSummary(scores []store.ScoreRecord) evalRunComparisonSummary {
	summary := evalRunComparisonSummary{ScoreCount: len(scores)}
	for _, score := range scores {
		if isPassingStatus(score.Status) {
			summary.PassCount++
			continue
		}
		summary.FailCount++
	}
	summary.PassRate = percent(summary.PassCount, summary.ScoreCount)
	return summary
}

func compareScoreSets(baseline []store.ScoreRecord, candidate []store.ScoreRecord) ([]evaluatorComparison, []scoreDeltaView, []scoreDeltaView) {
	baselineByKey := make(map[string]store.ScoreRecord, len(baseline))
	candidateByKey := make(map[string]store.ScoreRecord, len(candidate))
	evaluatorMap := map[string]*evaluatorAggregate{}

	for _, score := range baseline {
		key := scoreComparisonKey(score)
		baselineByKey[key] = score
		aggregate := ensureEvaluatorAggregate(evaluatorMap, score.EvaluatorKey)
		aggregate.BaselineTotal++
		if isPassingStatus(score.Status) {
			aggregate.BaselinePass++
		}
	}
	for _, score := range candidate {
		key := scoreComparisonKey(score)
		candidateByKey[key] = score
		aggregate := ensureEvaluatorAggregate(evaluatorMap, score.EvaluatorKey)
		aggregate.CandidateTotal++
		if isPassingStatus(score.Status) {
			aggregate.CandidatePass++
		}
	}

	var improvements []scoreDeltaView
	var regressions []scoreDeltaView
	for key, baselineScore := range baselineByKey {
		candidateScore, ok := candidateByKey[key]
		if !ok {
			continue
		}
		aggregate := ensureEvaluatorAggregate(evaluatorMap, baselineScore.EvaluatorKey)
		aggregate.MatchedCount++
		delta := scoreDeltaView{
			TraceID:         baselineScore.TraceID,
			EvaluatorKey:    baselineScore.EvaluatorKey,
			BaselineStatus:  baselineScore.Status,
			CandidateStatus: candidateScore.Status,
			BaselineValue:   baselineScore.Value,
			CandidateValue:  candidateScore.Value,
			ValueDelta:      candidateScore.Value - baselineScore.Value,
		}
		switch compareScoreOutcome(baselineScore, candidateScore) {
		case 1:
			aggregate.ImprovementCount++
			improvements = append(improvements, delta)
		case -1:
			aggregate.RegressionCount++
			regressions = append(regressions, delta)
		}
	}

	keys := make([]string, 0, len(evaluatorMap))
	for key := range evaluatorMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]evaluatorComparison, 0, len(keys))
	for _, key := range keys {
		item := evaluatorMap[key]
		out = append(out, evaluatorComparison{
			EvaluatorKey:      item.EvaluatorKey,
			BaselineTotal:     item.BaselineTotal,
			BaselinePass:      item.BaselinePass,
			BaselinePassRate:  percent(item.BaselinePass, item.BaselineTotal),
			CandidateTotal:    item.CandidateTotal,
			CandidatePass:     item.CandidatePass,
			CandidatePassRate: percent(item.CandidatePass, item.CandidateTotal),
			PassRateDelta:     percent(item.CandidatePass, item.CandidateTotal) - percent(item.BaselinePass, item.BaselineTotal),
			MatchedCount:      item.MatchedCount,
			ImprovementCount:  item.ImprovementCount,
			RegressionCount:   item.RegressionCount,
		})
	}

	sort.Slice(improvements, func(i, j int) bool {
		if improvements[i].TraceID != improvements[j].TraceID {
			return improvements[i].TraceID < improvements[j].TraceID
		}
		return improvements[i].EvaluatorKey < improvements[j].EvaluatorKey
	})
	sort.Slice(regressions, func(i, j int) bool {
		if regressions[i].TraceID != regressions[j].TraceID {
			return regressions[i].TraceID < regressions[j].TraceID
		}
		return regressions[i].EvaluatorKey < regressions[j].EvaluatorKey
	})

	return out, improvements, regressions
}

func ensureEvaluatorAggregate(aggregates map[string]*evaluatorAggregate, evaluatorKey string) *evaluatorAggregate {
	if item, ok := aggregates[evaluatorKey]; ok {
		return item
	}
	item := &evaluatorAggregate{EvaluatorKey: evaluatorKey}
	aggregates[evaluatorKey] = item
	return item
}

func scoreComparisonKey(score store.ScoreRecord) string {
	return score.TraceID + "\x00" + score.EvaluatorKey
}

func compareScoreOutcome(baseline store.ScoreRecord, candidate store.ScoreRecord) int {
	baselinePass := isPassingStatus(baseline.Status)
	candidatePass := isPassingStatus(candidate.Status)
	switch {
	case !baselinePass && candidatePass:
		return 1
	case baselinePass && !candidatePass:
		return -1
	case candidate.Value > baseline.Value:
		return 1
	case candidate.Value < baseline.Value:
		return -1
	default:
		return 0
	}
}

func isPassingStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "pass")
}

func percent(numerator int, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) * 100 / float64(denominator)
}

func matchedScoreCount(items []evaluatorComparison) int {
	total := 0
	for _, item := range items {
		total += item.MatchedCount
	}
	return total
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
