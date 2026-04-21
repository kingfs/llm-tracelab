package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerListsAndQueriesReadOnlyTools(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	sessionID := "sess-mcp"
	successPath := filepath.Join(outputDir, "success.http")
	failurePath := filepath.Join(outputDir, "failure.http")

	if err := os.WriteFile(successPath, buildRecordFixture(t, fixtureSpec{
		URL:                            "/v1/responses",
		Status:                         "200 OK",
		SessionID:                      sessionID,
		RequestID:                      "req-success",
		RequestBody:                    `{"input":"hello"}`,
		ResponseBody:                   `{"output_text":"done"}`,
		SelectedUpstreamID:             "openai-primary",
		SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
		SelectedUpstreamProviderPreset: "openai",
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(success) error = %v", err)
	}

	if err := os.WriteFile(failurePath, buildRecordFixture(t, fixtureSpec{
		URL:                            "/v1/chat/completions",
		Status:                         "500 Internal Server Error",
		SessionID:                      sessionID,
		RequestID:                      "req-failure",
		RequestBody:                    `{"messages":[{"role":"user","content":"boom"}]}`,
		ResponseBody:                   `{"error":"failed"}`,
		SelectedUpstreamID:             "openai-primary",
		SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
		SelectedUpstreamProviderPreset: "openai",
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(failure) error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if err := st.UpsertUpstreamTarget(store.UpstreamTargetRecord{
		ID:                "openai-primary",
		BaseURL:           "https://api.openai.com/v1",
		ProviderPreset:    "openai",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          100,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}
	if err := st.ReplaceUpstreamModels("openai-primary", []store.UpstreamModelRecord{{
		UpstreamID: "openai-primary",
		Model:      "gpt-5.1-codex",
		Source:     "static",
		SeenAt:     time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatalf("ReplaceUpstreamModels() error = %v", err)
	}
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	successEntry, err := st.GetByRequestID("req-success")
	if err != nil {
		t.Fatalf("GetByRequestID(req-success) error = %v", err)
	}
	failureEntry, err := st.GetByRequestID("req-failure")
	if err != nil {
		t.Fatalf("GetByRequestID(req-failure) error = %v", err)
	}

	baselineRun, err := st.CreateEvalRun("", "trace_list", "", "baseline_v1", 2)
	if err != nil {
		t.Fatalf("CreateEvalRun(baseline) error = %v", err)
	}
	candidateRun, err := st.CreateEvalRun("", "trace_list", "", "baseline_v1", 2)
	if err != nil {
		t.Fatalf("CreateEvalRun(candidate) error = %v", err)
	}
	for _, score := range []store.ScoreRecord{
		{
			TraceID:      successEntry.ID,
			SessionID:    successEntry.SessionID,
			EvalRunID:    baselineRun.ID,
			EvaluatorKey: "http_status_2xx",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  "baseline success",
		},
		{
			TraceID:      failureEntry.ID,
			SessionID:    failureEntry.SessionID,
			EvalRunID:    baselineRun.ID,
			EvaluatorKey: "http_status_2xx",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "baseline failure",
		},
		{
			TraceID:      successEntry.ID,
			SessionID:    successEntry.SessionID,
			EvalRunID:    candidateRun.ID,
			EvaluatorKey: "http_status_2xx",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "candidate regressed",
		},
		{
			TraceID:      failureEntry.ID,
			SessionID:    failureEntry.SessionID,
			EvalRunID:    candidateRun.ID,
			EvaluatorKey: "http_status_2xx",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  "candidate improved",
		},
	} {
		if _, err := st.AddScore(score); err != nil {
			t.Fatalf("AddScore() error = %v", err)
		}
	}
	if err := st.FinalizeEvalRun(baselineRun.ID, 2, 1, 1); err != nil {
		t.Fatalf("FinalizeEvalRun(baseline) error = %v", err)
	}
	if err := st.FinalizeEvalRun(candidateRun.ID, 2, 1, 1); err != nil {
		t.Fatalf("FinalizeEvalRun(candidate) error = %v", err)
	}

	server := New(st, Options{})
	session, err := connectClient(context.Background(), server)
	if err != nil {
		t.Fatalf("connectClient() error = %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) != 23 {
		t.Fatalf("len(tools.Tools) = %d, want 23", len(tools.Tools))
	}

	traceList, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_traces",
		Arguments: map[string]any{"page_size": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(list_traces) error = %v", err)
	}
	if traceList.IsError {
		t.Fatalf("list_traces returned IsError")
	}
	tracePayload := traceList.StructuredContent.(map[string]any)
	items := tracePayload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("len(list_traces.items) = %d, want 2", len(items))
	}
	traceID := items[0].(map[string]any)["id"].(string)
	if strings.TrimSpace(traceID) == "" {
		t.Fatalf("trace id missing from list_traces")
	}

	traceDetail, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_trace",
		Arguments: map[string]any{"trace_id": traceID, "include_raw": true},
	})
	if err != nil {
		t.Fatalf("CallTool(get_trace) error = %v", err)
	}
	traceDetailPayload := traceDetail.StructuredContent.(map[string]any)
	if traceDetailPayload["id"].(string) != traceID {
		t.Fatalf("get_trace.id = %q, want %q", traceDetailPayload["id"], traceID)
	}
	if _, ok := traceDetailPayload["raw"].(map[string]any); !ok {
		t.Fatalf("get_trace.raw missing")
	}

	sessionList, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_sessions",
		Arguments: map[string]any{"page_size": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(list_sessions) error = %v", err)
	}
	sessionItems := sessionList.StructuredContent.(map[string]any)["items"].([]any)
	if len(sessionItems) != 1 {
		t.Fatalf("len(list_sessions.items) = %d, want 1", len(sessionItems))
	}

	sessionDetail, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_session",
		Arguments: map[string]any{"session_id": sessionID},
	})
	if err != nil {
		t.Fatalf("CallTool(get_session) error = %v", err)
	}
	summary := sessionDetail.StructuredContent.(map[string]any)["summary"].(map[string]any)
	if summary["session_id"].(string) != sessionID {
		t.Fatalf("get_session.summary.session_id = %q, want %q", summary["session_id"], sessionID)
	}

	upstreams, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_upstreams",
		Arguments: map[string]any{"window": "all"},
	})
	if err != nil {
		t.Fatalf("CallTool(list_upstreams) error = %v", err)
	}
	upstreamItems := upstreams.StructuredContent.(map[string]any)["items"].([]any)
	if len(upstreamItems) != 1 {
		t.Fatalf("len(list_upstreams.items) = %d, want 1", len(upstreamItems))
	}

	upstreamDetail, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_upstream",
		Arguments: map[string]any{"upstream_id": "openai-primary", "window": "all"},
	})
	if err != nil {
		t.Fatalf("CallTool(get_upstream) error = %v", err)
	}
	target := upstreamDetail.StructuredContent.(map[string]any)["target"].(map[string]any)
	if target["id"].(string) != "openai-primary" {
		t.Fatalf("get_upstream.target.id = %q, want openai-primary", target["id"])
	}

	failures, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_failures",
		Arguments: map[string]any{"page_size": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(query_failures) error = %v", err)
	}
	failurePayload := failures.StructuredContent.(map[string]any)
	if got := int(failurePayload["returned"].(float64)); got != 1 {
		t.Fatalf("query_failures.returned = %d, want 1", got)
	}

	replayedTrace, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "replay_trace",
		Arguments: map[string]any{"trace_id": traceID, "body_limit": 100},
	})
	if err != nil {
		t.Fatalf("CallTool(replay_trace) error = %v", err)
	}
	replayPayload := replayedTrace.StructuredContent.(map[string]any)
	replayResult := replayPayload["replay"].(map[string]any)
	if replayPayload["trace_id"].(string) != traceID {
		t.Fatalf("replay_trace.trace_id = %q, want %q", replayPayload["trace_id"], traceID)
	}
	if int(replayResult["status_code"].(float64)) < 200 {
		t.Fatalf("replay_trace.replay.status_code = %v, want >= 200", replayResult["status_code"])
	}

	replayedSession, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "replay_session",
		Arguments: map[string]any{"session_id": sessionID, "limit": 5, "body_limit": 100},
	})
	if err != nil {
		t.Fatalf("CallTool(replay_session) error = %v", err)
	}
	replayedSessionPayload := replayedSession.StructuredContent.(map[string]any)
	if got := int(replayedSessionPayload["replayed"].(float64)); got != 2 {
		t.Fatalf("replay_session.replayed = %d, want 2", got)
	}

	createdDataset, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_dataset_from_session",
		Arguments: map[string]any{
			"name":        "session-dataset",
			"description": "from session",
			"session_id":  sessionID,
			"limit":       10,
			"note":        "captured",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(create_dataset_from_session) error = %v", err)
	}
	createdPayload := createdDataset.StructuredContent.(map[string]any)
	dataset := createdPayload["dataset"].(map[string]any)
	datasetID := dataset["id"].(string)
	if got := int(createdPayload["added"].(float64)); got != 2 {
		t.Fatalf("create_dataset_from_session.added = %d, want 2", got)
	}

	appendResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "append_dataset_examples",
		Arguments: map[string]any{"dataset_id": datasetID, "trace_ids": []string{traceID}},
	})
	if err != nil {
		t.Fatalf("CallTool(append_dataset_examples) error = %v", err)
	}
	if got := int(appendResult.StructuredContent.(map[string]any)["skipped"].(float64)); got != 1 {
		t.Fatalf("append_dataset_examples.skipped = %d, want 1", got)
	}

	listedDatasets, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_datasets",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool(list_datasets) error = %v", err)
	}
	listedItems := listedDatasets.StructuredContent.(map[string]any)["items"].([]any)
	if len(listedItems) != 1 {
		t.Fatalf("len(list_datasets.items) = %d, want 1", len(listedItems))
	}

	gotDataset, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_dataset",
		Arguments: map[string]any{"dataset_id": datasetID},
	})
	if err != nil {
		t.Fatalf("CallTool(get_dataset) error = %v", err)
	}
	gotPayload := gotDataset.StructuredContent.(map[string]any)
	examples := gotPayload["examples"].([]any)
	if len(examples) != 2 {
		t.Fatalf("len(get_dataset.examples) = %d, want 2", len(examples))
	}

	evalRunRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "run_eval_on_dataset",
		Arguments: map[string]any{"dataset_id": datasetID},
	})
	if err != nil {
		t.Fatalf("CallTool(run_eval_on_dataset) error = %v", err)
	}
	evalRunPayload := evalRunRes.StructuredContent.(map[string]any)
	run := evalRunPayload["run"].(map[string]any)
	evalRunID := run["id"].(string)
	if got := int(run["trace_count"].(float64)); got != 2 {
		t.Fatalf("run_eval_on_dataset.trace_count = %d, want 2", got)
	}
	if got := len(evalRunPayload["scores"].([]any)); got != 6 {
		t.Fatalf("len(run_eval_on_dataset.scores) = %d, want 6", got)
	}

	listedRuns, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_eval_runs",
		Arguments: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(list_eval_runs) error = %v", err)
	}
	if got := len(listedRuns.StructuredContent.(map[string]any)["items"].([]any)); got != 3 {
		t.Fatalf("len(list_eval_runs.items) = %d, want 3", got)
	}

	gotRun, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_eval_run",
		Arguments: map[string]any{"eval_run_id": evalRunID},
	})
	if err != nil {
		t.Fatalf("CallTool(get_eval_run) error = %v", err)
	}
	gotRunPayload := gotRun.StructuredContent.(map[string]any)
	if got := len(gotRunPayload["scores"].([]any)); got != 6 {
		t.Fatalf("len(get_eval_run.scores) = %d, want 6", got)
	}

	listedScores, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_scores",
		Arguments: map[string]any{"eval_run_id": evalRunID, "limit": 20},
	})
	if err != nil {
		t.Fatalf("CallTool(list_scores) error = %v", err)
	}
	if got := len(listedScores.StructuredContent.(map[string]any)["items"].([]any)); got != 6 {
		t.Fatalf("len(list_scores.items) = %d, want 6", got)
	}

	comparedRuns, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "compare_eval_runs",
		Arguments: map[string]any{
			"baseline_eval_run_id":  baselineRun.ID,
			"candidate_eval_run_id": candidateRun.ID,
		},
	})
	if err != nil {
		t.Fatalf("CallTool(compare_eval_runs) error = %v", err)
	}
	comparePayload := comparedRuns.StructuredContent.(map[string]any)
	if got := int(comparePayload["pass_rate_delta"].(float64)); got != 0 {
		t.Fatalf("compare_eval_runs.pass_rate_delta = %d, want 0", got)
	}
	evaluators := comparePayload["evaluators"].([]any)
	if len(evaluators) != 1 {
		t.Fatalf("len(compare_eval_runs.evaluators) = %d, want 1", len(evaluators))
	}
	evaluator := evaluators[0].(map[string]any)
	if got := int(evaluator["improvement_count"].(float64)); got != 1 {
		t.Fatalf("compare_eval_runs.improvement_count = %d, want 1", got)
	}
	if got := int(evaluator["regression_count"].(float64)); got != 1 {
		t.Fatalf("compare_eval_runs.regression_count = %d, want 1", got)
	}
	if got := len(comparePayload["improvements"].([]any)); got != 1 {
		t.Fatalf("len(compare_eval_runs.improvements) = %d, want 1", got)
	}
	if got := len(comparePayload["regressions"].([]any)); got != 1 {
		t.Fatalf("len(compare_eval_runs.regressions) = %d, want 1", got)
	}

	createdExperiment, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_experiment_from_eval_runs",
		Arguments: map[string]any{
			"name":                  "baseline-vs-candidate",
			"description":           "saved comparison",
			"baseline_eval_run_id":  baselineRun.ID,
			"candidate_eval_run_id": candidateRun.ID,
		},
	})
	if err != nil {
		t.Fatalf("CallTool(create_experiment_from_eval_runs) error = %v", err)
	}
	experimentPayload := createdExperiment.StructuredContent.(map[string]any)
	experiment := experimentPayload["experiment"].(map[string]any)
	experimentID := experiment["id"].(string)
	if got := int(experiment["improvement_count"].(float64)); got != 1 {
		t.Fatalf("create_experiment_from_eval_runs.improvement_count = %d, want 1", got)
	}

	listedExperiments, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_experiment_runs",
		Arguments: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(list_experiment_runs) error = %v", err)
	}
	if got := len(listedExperiments.StructuredContent.(map[string]any)["items"].([]any)); got != 1 {
		t.Fatalf("len(list_experiment_runs.items) = %d, want 1", got)
	}

	gotExperiment, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_experiment_run",
		Arguments: map[string]any{"experiment_run_id": experimentID},
	})
	if err != nil {
		t.Fatalf("CallTool(get_experiment_run) error = %v", err)
	}
	gotExperimentPayload := gotExperiment.StructuredContent.(map[string]any)
	gotComparison := gotExperimentPayload["comparison"].(map[string]any)
	if got := len(gotComparison["improvements"].([]any)); got != 1 {
		t.Fatalf("len(get_experiment_run.comparison.improvements) = %d, want 1", got)
	}

	experimentScores, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_scores",
		Arguments: map[string]any{"experiment_run_id": experimentID, "limit": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(list_scores by experiment) error = %v", err)
	}
	scoreItems := experimentScores.StructuredContent.(map[string]any)["items"].([]any)
	if len(scoreItems) != 4 {
		t.Fatalf("len(list_scores by experiment.items) = %d, want 4", len(scoreItems))
	}
	runRoles := map[string]int{}
	for _, item := range scoreItems {
		runRoles[item.(map[string]any)["run_role"].(string)]++
	}
	if runRoles["baseline"] != 2 || runRoles["candidate"] != 2 {
		t.Fatalf("list_scores by experiment run roles = %#v, want 2 baseline and 2 candidate", runRoles)
	}
}

func connectClient(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	return client.Connect(ctx, t2, nil)
}

type fixtureSpec struct {
	URL                            string
	Status                         string
	SessionID                      string
	RequestID                      string
	RequestBody                    string
	ResponseBody                   string
	SelectedUpstreamID             string
	SelectedUpstreamBaseURL        string
	SelectedUpstreamProviderPreset string
}

func buildRecordFixture(t *testing.T, spec fixtureSpec) []byte {
	t.Helper()

	requestHeaderLines := []string{
		"POST " + spec.URL + " HTTP/1.1",
		"Host: example.com",
		"Content-Type: application/json",
	}
	if strings.TrimSpace(spec.SessionID) != "" {
		requestHeaderLines = append(requestHeaderLines, "Session_id: "+spec.SessionID)
	}
	request := strings.Join(requestHeaderLines, "\r\n") + "\r\n\r\n" + spec.RequestBody
	responseHeader := "HTTP/1.1 " + spec.Status + "\r\nContent-Type: application/json\r\n\r\n"

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:                      spec.RequestID,
			Time:                           time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			Model:                          "gpt-5.1-codex",
			Provider:                       "openai_compatible",
			Operation:                      operationForURL(spec.URL),
			Endpoint:                       endpointForURL(spec.URL),
			URL:                            spec.URL,
			Method:                         "POST",
			StatusCode:                     parseStatusCode(spec.Status),
			DurationMs:                     100,
			TTFTMs:                         10,
			ClientIP:                       "127.0.0.1",
			ContentLength:                  int64(len(spec.ResponseBody)),
			Error:                          errorForStatus(spec.Status),
			SelectedUpstreamID:             spec.SelectedUpstreamID,
			SelectedUpstreamBaseURL:        spec.SelectedUpstreamBaseURL,
			SelectedUpstreamProviderPreset: spec.SelectedUpstreamProviderPreset,
			RoutingPolicy:                  "p2c",
			RoutingCandidateCount:          1,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(strings.Join(requestHeaderLines, "\r\n") + "\r\n\r\n")),
			ReqBodyLen:   int64(len(spec.RequestBody)),
			ResHeaderLen: int64(len(responseHeader)),
			ResBodyLen:   int64(len(spec.ResponseBody)),
		},
		Usage: recordfile.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 12,
			TotalTokens:      22,
		},
	}

	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	return append(prelude, []byte(request+"\n"+responseHeader+spec.ResponseBody)...)
}

func operationForURL(path string) string {
	switch path {
	case "/v1/responses":
		return "responses.create"
	case "/v1/chat/completions":
		return "chat.completions"
	default:
		return strings.Trim(path, "/")
	}
}

func endpointForURL(path string) string {
	return strings.Trim(path, "/")
}

func parseStatusCode(status string) int {
	if strings.HasPrefix(status, "500") {
		return 500
	}
	return 200
}

func errorForStatus(status string) string {
	if strings.HasPrefix(status, "500") {
		return "failed"
	}
	return ""
}
