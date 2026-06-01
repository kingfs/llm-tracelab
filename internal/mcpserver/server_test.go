package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerListsAndQueriesReadOnlyTools(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	sessionID := "sess-mcp"
	successPath := filepath.Join(outputDir, "success.http")
	failurePath := filepath.Join(outputDir, "failure.http")
	stickyPath := filepath.Join(outputDir, "sticky-break.http")
	credentialPath := filepath.Join(outputDir, "credential-failure.http")

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
		RoutingEvents:                  true,
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
		HeaderRoutingFailureReason:     "header_route_failure",
		RoutingFailureEventReason:      "all_targets_filtered",
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(failure) error = %v", err)
	}

	if err := os.WriteFile(stickyPath, buildRecordFixture(t, fixtureSpec{
		URL:                            "/v1/responses",
		Status:                         "200 OK",
		SessionID:                      sessionID,
		RequestID:                      "req-sticky-break",
		RequestBody:                    `{"input":"continue"}`,
		ResponseBody:                   `{"output_text":"rebound"}`,
		SelectedUpstreamID:             "openai-fallback",
		SelectedUpstreamBaseURL:        "https://fallback.example.com/v1",
		SelectedUpstreamProviderPreset: "openai",
		StickyEvents: []stickyFixtureEvent{{
			Status:               "break",
			UpstreamID:           "openai-fallback",
			PreviousUpstreamID:   "openai-primary",
			StickyKeyFingerprint: "sticky-fp-001",
			RawStickyKey:         "raw-session-id-must-not-leak",
		}},
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(sticky) error = %v", err)
	}
	if err := os.WriteFile(credentialPath, buildRecordFixture(t, fixtureSpec{
		URL:                            "/v1/responses",
		Status:                         "500 Internal Server Error",
		SessionID:                      sessionID,
		RequestID:                      "req-credential-failure",
		RequestBody:                    `{"model":"gpt-5.1-codex","input":"credential failure"}`,
		ResponseBody:                   `{"error":{"message":"credential limit"}}`,
		SelectedUpstreamID:             "openai-primary",
		SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
		SelectedUpstreamProviderPreset: "openai",
		RoutingEvents:                  true,
		RoutingFailureEventReason:      "credential_concurrency_full",
		RouteTargetID:                  "openai-primary:cred-a",
		ChannelID:                      "openai-primary",
		CredentialID:                   "cred-a",
		StickyEvents: []stickyFixtureEvent{{
			Status:                "break",
			UpstreamID:            "openai-primary",
			PreviousUpstreamID:    "openai-primary",
			RouteTargetID:         "openai-primary:cred-b",
			PreviousRouteTargetID: "openai-primary:cred-a",
			ChannelID:             "openai-primary",
			PreviousChannelID:     "openai-primary",
			CredentialID:          "cred-b",
			PreviousCredentialID:  "cred-a",
			StickyKeyFingerprint:  "sticky-fp-credential",
		}},
	}), 0o644); err != nil {
		t.Fatalf("WriteFile(credential) error = %v", err)
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
	if err := st.SaveObservation(observe.TraceObservation{
		TraceID:       successEntry.ID,
		Parser:        "openai",
		ParserVersion: "0.1.0",
		Status:        observe.ParseStatusParsed,
	}); err != nil {
		t.Fatalf("SaveObservation(success) error = %v", err)
	}
	failureEntry, err := st.GetByRequestID("req-failure")
	if err != nil {
		t.Fatalf("GetByRequestID(req-failure) error = %v", err)
	}
	credentialEntry, err := st.GetByRequestID("req-credential-failure")
	if err != nil {
		t.Fatalf("GetByRequestID(req-credential-failure) error = %v", err)
	}
	if err := st.SaveFindings(failureEntry.ID, []observe.Finding{{
		ID:              "finding-danger",
		TraceID:         failureEntry.ID,
		Category:        "dangerous_command",
		Severity:        observe.SeverityHigh,
		Confidence:      0.95,
		Title:           "Dangerous command",
		EvidencePath:    "trace#" + failureEntry.ID + "#node#node-shell",
		EvidenceExcerpt: "rm -rf /",
		NodeID:          "node-shell",
		Detector:        "dangerous_shell",
		DetectorVersion: "0.1.0",
	}, {
		ID:              "finding-secret",
		TraceID:         failureEntry.ID,
		Category:        "credential_leak",
		Severity:        observe.SeverityHigh,
		Confidence:      0.9,
		Title:           "Credential exposure",
		EvidencePath:    "trace#" + failureEntry.ID + "#node#node-secret",
		NodeID:          "node-secret",
		Detector:        "credential",
		DetectorVersion: "0.1.0",
	}}); err != nil {
		t.Fatalf("SaveFindings() error = %v", err)
	}
	parserEvent, err := st.UpsertSystemEvent(store.SystemEvent{
		Fingerprint: "parser:save_observation:semantic_nodes_unique_constraint",
		Source:      "parser",
		Category:    "parse_failure",
		Severity:    "error",
		Title:       "Observation parse job failed",
		Message:     "constraint failed",
		TraceID:     failureEntry.ID,
		JobID:       "36",
		DetailsJSON: []byte(`{"operation":"save_observation"}`),
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(parser) error = %v", err)
	}
	if _, err := st.UpsertSystemEvent(store.SystemEvent{
		Fingerprint: "upstream:openai_primary:v1_responses:client_disconnect",
		Source:      "upstream",
		Category:    "transport_error",
		Severity:    "warning",
		Title:       "Upstream transport error",
		Message:     "broken pipe",
		UpstreamID:  "openai-primary",
	}); err != nil {
		t.Fatalf("UpsertSystemEvent(upstream) error = %v", err)
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
	if len(tools.Tools) != 19 {
		t.Fatalf("len(tools.Tools) = %d, want 19", len(tools.Tools))
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
	if len(items) != 4 {
		t.Fatalf("len(list_traces.items) = %d, want 4", len(items))
	}
	traceID := items[0].(map[string]any)["id"].(string)
	if strings.TrimSpace(traceID) == "" {
		t.Fatalf("trace id missing from list_traces")
	}
	if _, ok := items[0].(map[string]any)["observation"].(map[string]any); !ok {
		t.Fatalf("list_traces observation metadata missing: %+v", items[0])
	}

	unparsedTraces, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_traces",
		Arguments: map[string]any{"page_size": 10, "observation": "unparsed"},
	})
	if err != nil {
		t.Fatalf("CallTool(list_traces unparsed) error = %v", err)
	}
	unparsedItems := unparsedTraces.StructuredContent.(map[string]any)["items"].([]any)
	if len(unparsedItems) != 3 {
		t.Fatalf("len(list_traces unparsed.items) = %d, want 3", len(unparsedItems))
	}
	seenFailureUnparsed := false
	for _, item := range unparsedItems {
		unparsedItem := item.(map[string]any)
		if got := unparsedItem["observation"].(map[string]any)["status"].(string); got != "unparsed" {
			t.Fatalf("list_traces unparsed observation = %q, want unparsed", got)
		}
		if got := unparsedItem["id"].(string); got == failureEntry.ID {
			seenFailureUnparsed = true
		}
	}
	if !seenFailureUnparsed {
		t.Fatalf("list_traces unparsed missing failure trace %q", failureEntry.ID)
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

	routingDecisions, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_routing_decisions",
		Arguments: map[string]any{"trace_id": successEntry.ID},
	})
	if err != nil {
		t.Fatalf("CallTool(query_routing_decisions) error = %v", err)
	}
	routingPayload := routingDecisions.StructuredContent.(map[string]any)
	if got := routingPayload["selected_upstream_id"].(string); got != "openai-primary" {
		t.Fatalf("query_routing_decisions selected_upstream_id = %q, want openai-primary", got)
	}
	if got := int(routingPayload["candidate_count"].(float64)); got != 1 {
		t.Fatalf("query_routing_decisions candidate_count = %d, want 1", got)
	}
	if got := int(routingPayload["available_count"].(float64)); got != 1 {
		t.Fatalf("query_routing_decisions available_count = %d, want 1", got)
	}
	if len(routingPayload["events"].([]any)) == 0 {
		t.Fatalf("query_routing_decisions events empty")
	}

	credentialRouting, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_routing_decisions",
		Arguments: map[string]any{"trace_id": credentialEntry.ID},
	})
	if err != nil {
		t.Fatalf("CallTool(query_routing_decisions credential) error = %v", err)
	}
	credentialRoutingPayload := credentialRouting.StructuredContent.(map[string]any)
	if got := credentialRoutingPayload["selected_upstream_id"].(string); got != "openai-primary" {
		t.Fatalf("credential selected_upstream_id = %q, want openai-primary", got)
	}
	if got := credentialRoutingPayload["selected_route_target_id"].(string); got != "openai-primary:cred-a" {
		t.Fatalf("credential selected_route_target_id = %q, want openai-primary:cred-a", got)
	}
	if got := credentialRoutingPayload["selected_channel_id"].(string); got != "openai-primary" {
		t.Fatalf("credential selected_channel_id = %q, want openai-primary", got)
	}
	if got := credentialRoutingPayload["selected_credential_id"].(string); got != "cred-a" {
		t.Fatalf("credential selected_credential_id = %q, want cred-a", got)
	}

	stickyRouting, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "query_sticky_routing",
		Arguments: map[string]any{
			"status":                 "break",
			"upstream_id":            "openai-fallback",
			"previous_upstream_id":   "openai-primary",
			"sticky_key_fingerprint": "sticky-fp-001",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(query_sticky_routing) error = %v", err)
	}
	stickyPayload := stickyRouting.StructuredContent.(map[string]any)
	if got := int(stickyPayload["total"].(float64)); got != 1 {
		t.Fatalf("query_sticky_routing.total = %d, want 1", got)
	}
	stickyItems := stickyPayload["items"].([]any)
	stickyItem := stickyItems[0].(map[string]any)
	if got := stickyItem["sticky_status"].(string); got != "break" {
		t.Fatalf("query_sticky_routing sticky_status = %q, want break", got)
	}
	if got := stickyItem["upstream_id"].(string); got != "openai-fallback" {
		t.Fatalf("query_sticky_routing upstream_id = %q, want openai-fallback", got)
	}
	if got := stickyItem["previous_upstream_id"].(string); got != "openai-primary" {
		t.Fatalf("query_sticky_routing previous_upstream_id = %q, want openai-primary", got)
	}
	if got := stickyItem["sticky_key_fingerprint"].(string); got != "sticky-fp-001" {
		t.Fatalf("query_sticky_routing sticky_key_fingerprint = %q, want sticky-fp-001", got)
	}
	if _, ok := stickyItem["cassette_path"].(string); !ok {
		t.Fatalf("query_sticky_routing cassette_path missing: %+v", stickyItem)
	}
	if _, ok := stickyItem["sticky_key"]; ok {
		t.Fatalf("query_sticky_routing leaked sticky_key: %+v", stickyItem)
	}
	if strings.Contains(fmt.Sprintf("%v", stickyPayload), "raw-session-id-must-not-leak") {
		t.Fatalf("query_sticky_routing leaked raw sticky key: %+v", stickyPayload)
	}

	credentialSticky, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_sticky_routing",
		Arguments: map[string]any{"sticky_key_fingerprint": "sticky-fp-credential"},
	})
	if err != nil {
		t.Fatalf("CallTool(query_sticky_routing credential) error = %v", err)
	}
	credentialStickyItems := credentialSticky.StructuredContent.(map[string]any)["items"].([]any)
	if len(credentialStickyItems) != 1 {
		t.Fatalf("len(query_sticky_routing credential items) = %d, want 1", len(credentialStickyItems))
	}
	credentialStickyItem := credentialStickyItems[0].(map[string]any)
	if got := credentialStickyItem["route_target_id"].(string); got != "openai-primary:cred-b" {
		t.Fatalf("credential sticky route_target_id = %q, want openai-primary:cred-b", got)
	}
	if got := credentialStickyItem["previous_route_target_id"].(string); got != "openai-primary:cred-a" {
		t.Fatalf("credential sticky previous_route_target_id = %q, want openai-primary:cred-a", got)
	}
	if got := credentialStickyItem["channel_id"].(string); got != "openai-primary" {
		t.Fatalf("credential sticky channel_id = %q, want openai-primary", got)
	}
	if got := credentialStickyItem["credential_id"].(string); got != "cred-b" {
		t.Fatalf("credential sticky credential_id = %q, want cred-b", got)
	}
	if got := credentialStickyItem["previous_credential_id"].(string); got != "cred-a" {
		t.Fatalf("credential sticky previous_credential_id = %q, want cred-a", got)
	}

	stickyNoMatch, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_sticky_routing",
		Arguments: map[string]any{"sticky_key_fingerprint": "missing-fp"},
	})
	if err != nil {
		t.Fatalf("CallTool(query_sticky_routing no match) error = %v", err)
	}
	if got := int(stickyNoMatch.StructuredContent.(map[string]any)["total"].(float64)); got != 0 {
		t.Fatalf("query_sticky_routing no match total = %d, want 0", got)
	}

	traceFindings, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_trace_findings",
		Arguments: map[string]any{"trace_id": failureEntry.ID, "severity": "high"},
	})
	if err != nil {
		t.Fatalf("CallTool(list_trace_findings) error = %v", err)
	}
	traceFindingsPayload := traceFindings.StructuredContent.(map[string]any)
	if got := int(traceFindingsPayload["total"].(float64)); got != 2 {
		t.Fatalf("list_trace_findings.total = %d, want 2", got)
	}

	dangerous, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_dangerous_tool_calls",
		Arguments: map[string]any{"trace_id": failureEntry.ID},
	})
	if err != nil {
		t.Fatalf("CallTool(query_dangerous_tool_calls) error = %v", err)
	}
	if got := int(dangerous.StructuredContent.(map[string]any)["total"].(float64)); got != 1 {
		t.Fatalf("query_dangerous_tool_calls.total = %d, want 1", got)
	}

	sensitive, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_sensitive_data_findings",
		Arguments: map[string]any{"trace_id": failureEntry.ID},
	})
	if err != nil {
		t.Fatalf("CallTool(query_sensitive_data_findings) error = %v", err)
	}
	if got := int(sensitive.StructuredContent.(map[string]any)["total"].(float64)); got != 1 {
		t.Fatalf("query_sensitive_data_findings.total = %d, want 1", got)
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
	if got := int(failurePayload["returned"].(float64)); got != 2 {
		t.Fatalf("query_failures.returned = %d, want 2", got)
	}
	failureItems := failurePayload["items"].([]any)
	legacyFailure := findMCPItemByID(t, failureItems, failureEntry.ID)
	if got := legacyFailure["failure_reason"].(string); got != "all_targets_filtered" {
		t.Fatalf("query_failures legacy failure_reason = %q, want all_targets_filtered", got)
	}
	if got := legacyFailure["routing_event_reason"].(string); got != "all_targets_filtered" {
		t.Fatalf("query_failures legacy routing_event_reason = %q, want all_targets_filtered", got)
	}
	credentialFailure := findMCPItemByID(t, failureItems, credentialEntry.ID)
	if got := credentialFailure["failure_reason"].(string); got != "credential_concurrency_full" {
		t.Fatalf("query_failures credential failure_reason = %q, want credential_concurrency_full", got)
	}
	if got := credentialFailure["route_target_id"].(string); got != "openai-primary:cred-a" {
		t.Fatalf("query_failures route_target_id = %q, want openai-primary:cred-a", got)
	}
	if got := credentialFailure["channel_id"].(string); got != "openai-primary" {
		t.Fatalf("query_failures channel_id = %q, want openai-primary", got)
	}
	if got := credentialFailure["credential_id"].(string); got != "cred-a" {
		t.Fatalf("query_failures credential_id = %q, want cred-a", got)
	}

	failureClusters, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "summarize_failure_clusters",
		Arguments: map[string]any{"page_size": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(summarize_failure_clusters) error = %v", err)
	}
	failureClustersPayload := failureClusters.StructuredContent.(map[string]any)
	assertMCPCountItem(t, failureClustersPayload["by_reason"].([]any), "all_targets_filtered", 1)
	assertMCPCountItem(t, failureClustersPayload["by_reason"].([]any), "credential_concurrency_full", 1)
	assertMCPCountItem(t, failureClustersPayload["by_route_target"].([]any), "openai-primary:cred-a", 1)
	assertMCPCountItem(t, failureClustersPayload["by_channel"].([]any), "openai-primary", 1)
	assertMCPCountItem(t, failureClustersPayload["by_credential"].([]any), "cred-a", 1)
	if got := len(failureClustersPayload["top_failures"].([]any)); got != 2 {
		t.Fatalf("len(summarize_failure_clusters.top_failures) = %d, want 2", got)
	}
	topCredentialFailure := findMCPTraceItem(t, failureClustersPayload["top_failures"].([]any), credentialEntry.ID)
	if got := topCredentialFailure["route_target_id"].(string); got != "openai-primary:cred-a" {
		t.Fatalf("summarize_failure_clusters credential route_target_id = %q, want openai-primary:cred-a", got)
	}

	systemEvents, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_system_events",
		Arguments: map[string]any{"status": "unread", "source": "parser", "page_size": 10, "window": "all"},
	})
	if err != nil {
		t.Fatalf("CallTool(list_system_events) error = %v", err)
	}
	systemEventsPayload := systemEvents.StructuredContent.(map[string]any)
	systemEventItems := systemEventsPayload["items"].([]any)
	if len(systemEventItems) != 1 {
		t.Fatalf("len(list_system_events.items) = %d, want 1", len(systemEventItems))
	}
	if got := systemEventItems[0].(map[string]any)["id"].(string); got != parserEvent.ID {
		t.Fatalf("list_system_events id = %q, want %q", got, parserEvent.ID)
	}

	systemEventDetail, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_system_event",
		Arguments: map[string]any{"event_id": parserEvent.ID, "include_details": true},
	})
	if err != nil {
		t.Fatalf("CallTool(get_system_event) error = %v", err)
	}
	systemEventDetailPayload := systemEventDetail.StructuredContent.(map[string]any)
	if systemEventDetailPayload["id"].(string) != parserEvent.ID {
		t.Fatalf("get_system_event.id = %q, want %q", systemEventDetailPayload["id"], parserEvent.ID)
	}
	if _, ok := systemEventDetailPayload["details_json"]; !ok {
		t.Fatalf("get_system_event.details_json missing")
	}

	systemEventSummary, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "summarize_system_events",
		Arguments: map[string]any{"window": "all"},
	})
	if err != nil {
		t.Fatalf("CallTool(summarize_system_events) error = %v", err)
	}
	systemEventSummaryPayload := systemEventSummary.StructuredContent.(map[string]any)
	if got := int(systemEventSummaryPayload["unread"].(float64)); got < 2 {
		t.Fatalf("summarize_system_events.unread = %d, want at least 2", got)
	}
	if got := len(systemEventSummaryPayload["newest"].([]any)); got == 0 {
		t.Fatalf("summarize_system_events.newest empty")
	}

	unreadEvents, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "query_unread_system_events",
		Arguments: map[string]any{"limit": 10, "min_severity": "error"},
	})
	if err != nil {
		t.Fatalf("CallTool(query_unread_system_events) error = %v", err)
	}
	unreadPayload := unreadEvents.StructuredContent.(map[string]any)
	unreadItems := unreadPayload["items"].([]any)
	if len(unreadItems) == 0 {
		t.Fatalf("query_unread_system_events.items empty")
	}
	for _, item := range unreadItems {
		if got := item.(map[string]any)["severity"].(string); got != "error" && got != "critical" {
			t.Fatalf("query_unread_system_events severity = %q, want error or critical", got)
		}
	}

	reanalyzeTrace, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "reanalyze_trace",
		Arguments: map[string]any{"trace_id": failureEntry.ID},
	})
	if err != nil {
		t.Fatalf("CallTool(reanalyze_trace) error = %v", err)
	}
	reanalyzeTracePayload := reanalyzeTrace.StructuredContent.(map[string]any)
	traceJob := reanalyzeTracePayload["job"].(map[string]any)
	if traceJob["job_type"].(string) != "trace_reanalyze" || traceJob["status"].(string) != "completed" {
		t.Fatalf("reanalyze_trace job = %+v", traceJob)
	}

	reanalyzeSession, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "reanalyze_session",
		Arguments: map[string]any{"session_id": sessionID, "async": true},
	})
	if err != nil {
		t.Fatalf("CallTool(reanalyze_session) error = %v", err)
	}
	sessionJobID := int64(reanalyzeSession.StructuredContent.(map[string]any)["id"].(float64))
	if sessionJobID == 0 {
		t.Fatalf("reanalyze_session job id = 0")
	}

	analysisJobs, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_analysis_jobs",
		Arguments: map[string]any{"target_type": "session", "target_id": sessionID},
	})
	if err != nil {
		t.Fatalf("CallTool(list_analysis_jobs) error = %v", err)
	}
	jobItems := analysisJobs.StructuredContent.(map[string]any)["items"].([]any)
	if len(jobItems) != 1 {
		t.Fatalf("len(list_analysis_jobs.items) = %d, want 1", len(jobItems))
	}

	analysisJob, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_analysis_job",
		Arguments: map[string]any{"job_id": sessionJobID},
	})
	if err != nil {
		t.Fatalf("CallTool(get_analysis_job) error = %v", err)
	}
	if got := analysisJob.StructuredContent.(map[string]any)["job_type"].(string); got != "session_reanalyze" {
		t.Fatalf("get_analysis_job.job_type = %q, want session_reanalyze", got)
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

func findMCPItemByID(t *testing.T, items []any, id string) map[string]any {
	t.Helper()
	for _, item := range items {
		mapped := item.(map[string]any)
		if mapped["id"] == id {
			return mapped
		}
	}
	t.Fatalf("missing MCP item id %q in %+v", id, items)
	return nil
}

func findMCPTraceItem(t *testing.T, items []any, traceID string) map[string]any {
	t.Helper()
	for _, item := range items {
		mapped := item.(map[string]any)
		if mapped["trace_id"] == traceID {
			return mapped
		}
	}
	t.Fatalf("missing MCP trace item %q in %+v", traceID, items)
	return nil
}

func assertMCPCountItem(t *testing.T, items []any, label string, count int) {
	t.Helper()
	for _, item := range items {
		mapped := item.(map[string]any)
		if mapped["label"] == label {
			if got := int(mapped["count"].(float64)); got != count {
				t.Fatalf("count item %q = %d, want %d in %+v", label, got, count, items)
			}
			return
		}
	}
	t.Fatalf("missing count item %q in %+v", label, items)
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
	RoutingEvents                  bool
	HeaderRoutingFailureReason     string
	RoutingFailureEventReason      string
	RetryQueueSaturatedEvent       bool
	StickyEvents                   []stickyFixtureEvent
	RouteTargetID                  string
	ChannelID                      string
	CredentialID                   string
}

type stickyFixtureEvent struct {
	Status                string
	UpstreamID            string
	PreviousUpstreamID    string
	RouteTargetID         string
	PreviousRouteTargetID string
	ChannelID             string
	PreviousChannelID     string
	CredentialID          string
	PreviousCredentialID  string
	StickyKeyFingerprint  string
	RawStickyKey          string
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
			RoutingFailureReason:           spec.HeaderRoutingFailureReason,
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

	events := recordfile.BuildEvents(header)
	if spec.RoutingEvents {
		events = append(events, routingFixtureEvents(header, spec)...)
	}
	if spec.RoutingFailureEventReason != "" {
		attrs := map[string]interface{}{
			"routing_failure_reason": spec.RoutingFailureEventReason,
		}
		addCredentialFixtureAttrs(attrs, spec.RouteTargetID, spec.ChannelID, spec.CredentialID)
		events = append(events, recordfile.RecordEvent{
			Type:       "routing.filtered",
			Time:       header.Meta.Time,
			Attributes: attrs,
		})
	}
	if spec.RetryQueueSaturatedEvent {
		events = append(events, recordfile.RecordEvent{
			Type: "routing.retry_queue_saturated",
			Time: header.Meta.Time,
		})
	}
	for _, sticky := range spec.StickyEvents {
		attrs := map[string]interface{}{
			"sticky_status": sticky.Status,
		}
		if sticky.UpstreamID != "" {
			attrs["upstream_id"] = sticky.UpstreamID
		}
		if sticky.PreviousUpstreamID != "" {
			attrs["previous_upstream_id"] = sticky.PreviousUpstreamID
		}
		addCredentialFixtureAttrs(attrs, sticky.RouteTargetID, sticky.ChannelID, sticky.CredentialID)
		if sticky.PreviousRouteTargetID != "" {
			attrs["previous_route_target_id"] = sticky.PreviousRouteTargetID
		}
		if sticky.PreviousChannelID != "" {
			attrs["previous_channel_id"] = sticky.PreviousChannelID
		}
		if sticky.PreviousCredentialID != "" {
			attrs["previous_credential_id"] = sticky.PreviousCredentialID
		}
		if sticky.StickyKeyFingerprint != "" {
			attrs["sticky_key_fingerprint"] = sticky.StickyKeyFingerprint
		}
		if sticky.RawStickyKey != "" {
			attrs["sticky_key"] = sticky.RawStickyKey
		}
		events = append(events, recordfile.RecordEvent{
			Type:       "routing.sticky." + sticky.Status,
			Time:       header.Meta.Time,
			Attributes: attrs,
		})
	}
	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	return append(prelude, []byte(request+"\n"+responseHeader+spec.ResponseBody)...)
}

func routingFixtureEvents(header recordfile.RecordHeader, spec fixtureSpec) []recordfile.RecordEvent {
	eventTime := header.Meta.Time
	candidate := map[string]any{
		"id":              header.Meta.SelectedUpstreamID,
		"provider_preset": header.Meta.SelectedUpstreamProviderPreset,
		"base_url":        header.Meta.SelectedUpstreamBaseURL,
		"priority":        100,
		"weight":          1,
		"health_state":    "healthy",
		"supports_path":   true,
		"supports_model":  true,
		"selectable":      true,
	}
	addCredentialFixtureAttrs(candidate, spec.RouteTargetID, spec.ChannelID, spec.CredentialID)
	selectedAttrs := map[string]interface{}{
		"upstream_id":   header.Meta.SelectedUpstreamID,
		"routing_score": header.Meta.RoutingScore,
	}
	addCredentialFixtureAttrs(selectedAttrs, spec.RouteTargetID, spec.ChannelID, spec.CredentialID)
	outcomeAttrs := map[string]interface{}{
		"upstream_id": header.Meta.SelectedUpstreamID,
		"model":       header.Meta.Model,
		"status_code": header.Meta.StatusCode,
		"duration_ms": header.Meta.DurationMs,
	}
	addCredentialFixtureAttrs(outcomeAttrs, spec.RouteTargetID, spec.ChannelID, spec.CredentialID)
	return []recordfile.RecordEvent{
		{
			Type: "routing.classified",
			Time: eventTime,
			Attributes: map[string]interface{}{
				"model":           header.Meta.Model,
				"endpoint":        header.Meta.Endpoint,
				"routing_policy":  header.Meta.RoutingPolicy,
				"fallback_policy": "reject",
			},
		},
		{
			Type: "routing.candidates",
			Time: eventTime,
			Attributes: map[string]interface{}{
				"available_count": 1,
				"candidates":      []any{candidate},
			},
		},
		{
			Type:       "routing.selected",
			Time:       eventTime,
			Attributes: selectedAttrs,
		},
		{
			Type:       "routing.outcome",
			Time:       eventTime.Add(time.Duration(header.Meta.DurationMs) * time.Millisecond),
			Attributes: outcomeAttrs,
		},
	}
}

func addCredentialFixtureAttrs(attrs map[string]any, routeTargetID string, channelID string, credentialID string) {
	if routeTargetID != "" {
		attrs["route_target_id"] = routeTargetID
	}
	if channelID != "" {
		attrs["channel_id"] = channelID
	}
	if credentialID != "" {
		attrs["credential_id"] = credentialID
	}
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

func TestClassifyFailureReasonSeparatesRetryQueueSaturation(t *testing.T) {
	got := classifyFailureReason(http.StatusServiceUnavailable, "Proxy overloaded: upstream retry wait queue saturated")
	if got != "retry_queue_saturated" {
		t.Fatalf("classifyFailureReason() = %q, want retry_queue_saturated", got)
	}
}
