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
	if len(tools.Tools) != 6 {
		t.Fatalf("len(tools.Tools) = %d, want 6", len(tools.Tools))
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

	failureClusters, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "summarize_failure_clusters",
		Arguments: map[string]any{"page_size": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(summarize_failure_clusters) error = %v", err)
	}
	failureClustersPayload := failureClusters.StructuredContent.(map[string]any)
	if got := len(failureClustersPayload["by_reason"].([]any)); got != 1 {
		t.Fatalf("len(summarize_failure_clusters.by_reason) = %d, want 1", got)
	}
	if got := len(failureClustersPayload["top_failures"].([]any)); got != 1 {
		t.Fatalf("len(summarize_failure_clusters.top_failures) = %d, want 1", got)
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
