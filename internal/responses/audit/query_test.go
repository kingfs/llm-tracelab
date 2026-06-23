package audit

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/enttest"
	_ "modernc.org/sqlite"
)

func TestQueryServiceGetRequestAuditTrace(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)

	mustCreateRequestAudit(t, client, requestAuditSeed{
		id:              "audit_trace",
		responseID:      "resp_trace",
		conversationID:  "conv_trace",
		method:          "POST",
		path:            "/v1/responses",
		clientRequestID: "client_1",
		status:          "completed",
		createdAt:       base,
	})
	mustCreateRequestAudit(t, client, requestAuditSeed{
		id:         "audit_other",
		responseID: "resp_other",
		method:     "POST",
		path:       "/v1/responses",
		status:     "completed",
		createdAt:  base.Add(time.Minute),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "event_late",
		requestAuditID: "audit_trace",
		responseID:     "resp_trace",
		eventType:      "tool_call",
		phase:          "tool",
		status:         "completed",
		message:        "late",
		occurredAt:     base.Add(3 * time.Second),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "event_early",
		requestAuditID: "audit_trace",
		eventType:      "model_call",
		phase:          "model",
		status:         "started",
		message:        "early",
		occurredAt:     base.Add(time.Second),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:         "event_other",
		responseID: "resp_other",
		eventType:  "ignored",
		occurredAt: base,
	})
	mustCreateUpstreamExchange(t, client, upstreamExchangeSeed{
		id:             "upex_request",
		requestAuditID: "audit_trace",
		upstreamID:     "upstream_b",
		model:          "gpt-4.1",
		endpoint:       "/v1/chat/completions",
		statusCode:     200,
		startedAt:      base.Add(2 * time.Second),
		completedAt:    base.Add(2500 * time.Millisecond),
	})
	mustCreateUpstreamExchange(t, client, upstreamExchangeSeed{
		id:         "upex_response",
		responseID: "resp_trace",
		upstreamID: "upstream_a",
		model:      "gpt-4.1-mini",
		endpoint:   "/v1/responses",
		statusCode: 200,
		startedAt:  base.Add(time.Second),
	})
	mustCreateUpstreamExchange(t, client, upstreamExchangeSeed{
		id:             "upex_other",
		requestAuditID: "audit_other",
		responseID:     "resp_other",
		upstreamID:     "ignored",
		startedAt:      base,
	})

	service := NewQueryService(client)
	trace, found, err := service.GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		RequestAuditID:        "audit_trace",
		EventLimit:            10,
		UpstreamExchangeLimit: 10,
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace() error = %v", err)
	}
	if !found {
		t.Fatalf("GetRequestAuditTrace() found = false, want true")
	}
	if trace.RequestAudit.ID != "audit_trace" || trace.RequestAudit.ResponseID != "resp_trace" {
		t.Fatalf("RequestAudit = %+v, want audit_trace/resp_trace", trace.RequestAudit)
	}
	if trace.RequestAudit.HeaderJSON["content-type"] != "application/json" {
		t.Fatalf("HeaderJSON = %#v, want content-type", trace.RequestAudit.HeaderJSON)
	}
	if got := executionEventIDs(trace.ExecutionEvents); !reflect.DeepEqual(got, []string{"event_early", "event_late"}) {
		t.Fatalf("ExecutionEvents ids = %v, want chronological events", got)
	}
	if got := upstreamExchangeIDs(trace.UpstreamExchanges); !reflect.DeepEqual(got, []string{"upex_response", "upex_request"}) {
		t.Fatalf("UpstreamExchanges ids = %v, want chronological exchanges", got)
	}
}

func TestQueryServiceGetRequestAuditTraceFiltersByClientRequestAndConversation(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 10, 30, 0, 0, time.UTC)

	for _, seed := range []requestAuditSeed{
		{
			id:              "audit_old",
			responseID:      "resp_old",
			conversationID:  "conv_shared",
			method:          "POST",
			path:            "/v1/responses",
			clientRequestID: "client_shared",
			status:          "completed",
			createdAt:       base,
		},
		{
			id:              "audit_new",
			responseID:      "resp_new",
			conversationID:  "conv_shared",
			method:          "POST",
			path:            "/v1/responses",
			clientRequestID: "client_shared",
			status:          "completed",
			createdAt:       base.Add(time.Minute),
		},
		{
			id:              "audit_other",
			responseID:      "resp_other",
			conversationID:  "conv_other",
			method:          "POST",
			path:            "/v1/responses",
			clientRequestID: "client_other",
			status:          "completed",
			createdAt:       base.Add(2 * time.Minute),
		},
	} {
		mustCreateRequestAudit(t, client, seed)
	}

	service := NewQueryService(client)
	trace, found, err := service.GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		ClientRequestID: "client_shared",
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace(client) error = %v", err)
	}
	if !found || trace.RequestAudit.ID != "audit_new" {
		t.Fatalf("GetRequestAuditTrace(client) = %+v/%v, want latest audit_new", trace.RequestAudit, found)
	}

	trace, found, err = service.GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		ConversationID: "conv_shared",
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace(conversation) error = %v", err)
	}
	if !found || trace.RequestAudit.ID != "audit_new" {
		t.Fatalf("GetRequestAuditTrace(conversation) = %+v/%v, want latest audit_new", trace.RequestAudit, found)
	}

	trace, found, err = service.GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		ResponseID:      "resp_old",
		ClientRequestID: "client_shared",
		ConversationID:  "conv_shared",
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace(and filters) error = %v", err)
	}
	if !found || trace.RequestAudit.ID != "audit_old" {
		t.Fatalf("GetRequestAuditTrace(and filters) = %+v/%v, want audit_old", trace.RequestAudit, found)
	}

	trace, found, err = service.GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		ClientRequestID: "client_shared",
		ConversationID:  "conv_other",
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace(mismatched filters) error = %v", err)
	}
	if found || trace.RequestAudit.ID != "" {
		t.Fatalf("GetRequestAuditTrace(mismatched filters) = %+v/%v, want zero/false", trace, found)
	}
}

func TestQueryServiceDerivesToolCallDiagnostics(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	const secretMarker = "SECRET_TOOL_MARKER"

	mustCreateRequestAudit(t, client, requestAuditSeed{
		id:             "audit_tools",
		responseID:     "resp_tools",
		conversationID: "conv_tools",
		method:         "POST",
		path:           "/v1/responses",
		status:         "completed",
		createdAt:      base,
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "tool_started",
		requestAuditID: "audit_tools",
		responseID:     "resp_tools",
		conversationID: "conv_tools",
		eventType:      "response.tool_call",
		phase:          "tool_call",
		status:         "started",
		detailsJSON: map[string]any{
			"call_id":   "call_search",
			"tool_name": "web_search",
			"executor":  "hosted:web_search",
			"query":     "find " + secretMarker,
		},
		occurredAt: base.Add(time.Second),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "tool_completed",
		requestAuditID: "audit_tools",
		responseID:     "resp_tools",
		conversationID: "conv_tools",
		eventType:      "response.tool_call",
		phase:          "tool_call",
		status:         "completed",
		detailsJSON: map[string]any{
			"call_id":      "call_search",
			"tool_name":    "web_search",
			"executor":     "hosted:web_search",
			"query":        "find " + secretMarker,
			"result_count": 2,
		},
		occurredAt: base.Add(2 * time.Second),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "tool_failed",
		requestAuditID: "audit_tools",
		responseID:     "resp_tools",
		conversationID: "conv_tools",
		eventType:      "response.tool_call",
		phase:          "tool_call",
		status:         "failed",
		message:        "failed with " + secretMarker,
		detailsJSON: map[string]any{
			"call_id":   "call_func",
			"tool_name": "lookup_secret",
			"executor":  "function",
			"arguments": `{"token":"` + secretMarker + `"}`,
			"error":     "tool failed: " + secretMarker,
		},
		occurredAt: base.Add(3 * time.Second),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "model_ignored",
		requestAuditID: "audit_tools",
		responseID:     "resp_tools",
		eventType:      "model_call",
		status:         "completed",
		occurredAt:     base.Add(4 * time.Second),
	})

	trace, found, err := NewQueryService(client).GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		RequestAuditID: "audit_tools",
		EventLimit:     10,
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace() error = %v", err)
	}
	if !found {
		t.Fatal("GetRequestAuditTrace() found = false, want true")
	}
	if len(trace.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d, want 2: %+v", len(trace.ToolCalls), trace.ToolCalls)
	}
	search := trace.ToolCalls[0]
	if search.CallID != "call_search" || search.ToolName != "web_search" || search.Executor != "hosted:web_search" {
		t.Fatalf("search tool call = %+v, want call_search/web_search/hosted:web_search", search)
	}
	if !reflect.DeepEqual(search.StatusesSeen, []string{"started", "completed"}) || search.LatestStatus != "completed" {
		t.Fatalf("search statuses = %v latest=%q, want started/completed", search.StatusesSeen, search.LatestStatus)
	}
	if !search.StartedAt.Equal(base.Add(time.Second)) || !search.CompletedAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("search times = %s/%s, want started/completed times", search.StartedAt, search.CompletedAt)
	}
	if search.QuerySummary == "" || search.OutputSummary != "results=2" || search.EventCount != 2 {
		t.Fatalf("search summaries = query:%q output:%q events:%d", search.QuerySummary, search.OutputSummary, search.EventCount)
	}
	failed := trace.ToolCalls[1]
	if failed.CallID != "call_func" || failed.LatestStatus != "failed" || failed.ErrorText == "" || failed.ArgumentsSummary == "" {
		t.Fatalf("failed tool call = %+v, want failed redacted summaries", failed)
	}
	payload, err := json.Marshal(trace.ToolCalls)
	if err != nil {
		t.Fatalf("json.Marshal(ToolCalls) error = %v", err)
	}
	if strings.Contains(string(payload), secretMarker) {
		t.Fatalf("ToolCalls leaked secret marker: %s", payload)
	}
}

func TestQueryServiceDerivesRequestAuditDiagnostics(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 14, 0, 0, 0, time.UTC)
	const secretMarker = "SECRET_DIAGNOSTIC_MARKER"

	mustCreateRequestAudit(t, client, requestAuditSeed{
		id:             "audit_diag",
		responseID:     "resp_diag",
		conversationID: "conv_diag",
		method:         "POST",
		path:           "/v1/responses",
		status:         "cancelled",
		createdAt:      base,
	})
	for _, seed := range []executionEventSeed{
		{
			id:             "diag_stream_started",
			requestAuditID: "audit_diag",
			responseID:     "resp_diag",
			conversationID: "conv_diag",
			eventType:      "response.stream",
			phase:          "stream",
			status:         "started",
			detailsJSON:    map[string]any{"stream": true},
			occurredAt:     base.Add(time.Second),
		},
		{
			id:             "diag_tool_requested",
			requestAuditID: "audit_diag",
			responseID:     "resp_diag",
			conversationID: "conv_diag",
			eventType:      "response.tool_call",
			phase:          "tool_call",
			status:         "requested",
			detailsJSON: map[string]any{
				"call_id":   "call_pending",
				"tool_name": "lookup",
				"executor":  "client",
				"arguments": `{"token":"` + secretMarker + `"}`,
			},
			occurredAt: base.Add(2 * time.Second),
		},
		{
			id:             "diag_tool_submitted",
			requestAuditID: "audit_diag",
			responseID:     "resp_diag",
			conversationID: "conv_diag",
			eventType:      "response.tool_call",
			phase:          "tool_call",
			status:         "submitted",
			detailsJSON: map[string]any{
				"call_id":   "call_done",
				"tool_name": "lookup",
				"executor":  "client",
			},
			occurredAt: base.Add(3 * time.Second),
		},
		{
			id:             "diag_compact",
			requestAuditID: "audit_diag",
			responseID:     "resp_diag",
			conversationID: "conv_diag",
			eventType:      "response.compact",
			phase:          "compact",
			status:         "completed",
			detailsJSON:    map[string]any{"auto_triggered": true, "summary": secretMarker},
			occurredAt:     base.Add(4 * time.Second),
		},
		{
			id:             "diag_model_failed",
			requestAuditID: "audit_diag",
			responseID:     "resp_diag",
			conversationID: "conv_diag",
			eventType:      "model_call",
			phase:          "upstream",
			status:         "failed",
			message:        "failed with " + secretMarker,
			occurredAt:     base.Add(5 * time.Second),
		},
	} {
		mustCreateExecutionEvent(t, client, seed)
	}
	mustCreateUpstreamExchange(t, client, upstreamExchangeSeed{
		id:             "diag_upex",
		requestAuditID: "audit_diag",
		responseID:     "resp_diag",
		statusCode:     499,
		startedAt:      base.Add(time.Second),
		completedAt:    base.Add(2 * time.Second),
	})

	trace, found, err := NewQueryService(client).GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{
		RequestAuditID:        "audit_diag",
		EventLimit:            10,
		UpstreamExchangeLimit: 10,
	})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace() error = %v", err)
	}
	if !found {
		t.Fatal("GetRequestAuditTrace() found = false, want true")
	}
	diagnostics := trace.Diagnostics
	if diagnostics.EventCount != 5 || diagnostics.UpstreamExchangeCount != 1 || diagnostics.ToolCallCount != 2 {
		t.Fatalf("diagnostic counts = %+v, want events=5 upstream=1 tools=2", diagnostics)
	}
	if diagnostics.LatestStatus != "cancelled" || !diagnostics.HasCancelled || !diagnostics.HasFailed || !diagnostics.HasStreamEvents || !diagnostics.HasCompactEvents {
		t.Fatalf("diagnostic status flags = %+v, want cancelled/failed/stream/compact", diagnostics)
	}
	if diagnostics.PendingToolCallCount != 1 || len(diagnostics.PendingToolCalls) != 1 || diagnostics.PendingToolCalls[0].CallID != "call_pending" {
		t.Fatalf("pending tool calls = %+v, want call_pending only", diagnostics.PendingToolCalls)
	}
	if !diagnostics.CompactCandidate || diagnostics.CompactSummary == nil || !diagnostics.CompactSummary.AutoTriggered || diagnostics.CompactSummary.LatestEventID != "diag_compact" {
		t.Fatalf("compact diagnostics = candidate:%t summary:%+v, want auto-triggered diag_compact", diagnostics.CompactCandidate, diagnostics.CompactSummary)
	}
	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatalf("json.Marshal(Diagnostics) error = %v", err)
	}
	if strings.Contains(string(payload), secretMarker) {
		t.Fatalf("Diagnostics leaked secret marker: %s", payload)
	}
}

func TestQueryServiceListRequestAuditsLimitAndEmptyResults(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)

	for _, seed := range []requestAuditSeed{
		{id: "audit_1", responseID: "resp_list", method: "POST", path: "/v1/responses", status: "completed", createdAt: base},
		{id: "audit_2", responseID: "resp_list", method: "POST", path: "/v1/responses", status: "completed", createdAt: base.Add(time.Second)},
		{id: "audit_3", responseID: "resp_list", method: "POST", path: "/v1/responses", status: "completed", createdAt: base.Add(2 * time.Second)},
		{id: "audit_other", responseID: "resp_other", method: "POST", path: "/v1/responses", status: "completed", createdAt: base.Add(3 * time.Second)},
	} {
		mustCreateRequestAudit(t, client, seed)
	}

	service := NewQueryService(client)
	audits, err := service.ListRequestAudits(ctx, ListRequestAuditsParams{
		ResponseID: "resp_list",
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListRequestAudits() error = %v", err)
	}
	if got := requestAuditIDs(audits); !reflect.DeepEqual(got, []string{"audit_3", "audit_2"}) {
		t.Fatalf("ListRequestAudits ids = %v, want latest two", got)
	}

	empty, err := service.ListRequestAudits(ctx, ListRequestAuditsParams{ResponseID: "missing"})
	if err != nil {
		t.Fatalf("ListRequestAudits(missing) error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListRequestAudits(missing) len = %d, want 0", len(empty))
	}
	trace, found, err := service.GetRequestAuditTrace(ctx, GetRequestAuditTraceParams{RequestAuditID: "missing"})
	if err != nil {
		t.Fatalf("GetRequestAuditTrace(missing) error = %v", err)
	}
	if found || trace.RequestAudit.ID != "" {
		t.Fatalf("GetRequestAuditTrace(missing) = %+v/%v, want zero/false", trace, found)
	}
}

func TestQueryServiceListRequestAuditsFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 11, 15, 0, 0, time.UTC)

	for _, seed := range []requestAuditSeed{
		{id: "audit_completed", responseID: "resp_status_1", conversationID: "conv_status", method: "POST", path: "/v1/responses", status: "completed", createdAt: base},
		{id: "audit_failed_old", responseID: "resp_status_2", conversationID: "conv_status", method: "POST", path: "/v1/responses", status: "failed", createdAt: base.Add(time.Second)},
		{id: "audit_failed_new", responseID: "resp_status_3", conversationID: "conv_status", method: "POST", path: "/v1/responses", status: "failed", createdAt: base.Add(2 * time.Second)},
		{id: "audit_failed_other", responseID: "resp_status_4", conversationID: "conv_other", method: "POST", path: "/v1/responses", status: "failed", createdAt: base.Add(3 * time.Second)},
	} {
		mustCreateRequestAudit(t, client, seed)
	}

	audits, err := NewQueryService(client).ListRequestAudits(ctx, ListRequestAuditsParams{
		ConversationID: "conv_status",
		Status:         "failed",
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListRequestAudits(status) error = %v", err)
	}
	if got := requestAuditIDs(audits); !reflect.DeepEqual(got, []string{"audit_failed_new", "audit_failed_old"}) {
		t.Fatalf("ListRequestAudits(status) ids = %v, want failed audits in latest order", got)
	}

	empty, err := NewQueryService(client).ListRequestAudits(ctx, ListRequestAuditsParams{
		ConversationID: "conv_status",
		Status:         "cancelled",
	})
	if err != nil {
		t.Fatalf("ListRequestAudits(cancelled) error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListRequestAudits(cancelled) len = %d, want 0", len(empty))
	}
}

func TestQueryServiceListRequestAuditsFiltersByOperation(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	base := time.Date(2026, 6, 22, 11, 25, 0, 0, time.UTC)

	for _, seed := range []requestAuditSeed{
		{id: "audit_create", responseID: "resp_create", conversationID: "conv_ops", method: "POST", path: "/v1/responses", status: "completed", createdAt: base},
		{id: "audit_compact", responseID: "resp_compact", conversationID: "conv_ops", method: "POST", path: "/v1/responses/compact", status: "completed", createdAt: base.Add(time.Second)},
		{id: "audit_input_items", responseID: "resp_items", conversationID: "conv_ops", method: "GET", path: "/v1/responses/resp_items/input_items", status: "completed", createdAt: base.Add(2 * time.Second)},
		{id: "audit_wrong_method", responseID: "resp_wrong", conversationID: "conv_ops", method: "POST", path: "/v1/responses/resp_wrong/input_items", status: "completed", createdAt: base.Add(3 * time.Second)},
	} {
		mustCreateRequestAudit(t, client, seed)
	}

	service := NewQueryService(client)
	for _, tt := range []struct {
		name      string
		operation string
		wantIDs   []string
	}{
		{name: "create", operation: "create", wantIDs: []string{"audit_create"}},
		{name: "compact", operation: "compact", wantIDs: []string{"audit_compact"}},
		{name: "input items", operation: "input_items", wantIDs: []string{"audit_input_items"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			audits, err := service.ListRequestAudits(ctx, ListRequestAuditsParams{
				ConversationID: "conv_ops",
				Operation:      tt.operation,
				Limit:          10,
			})
			if err != nil {
				t.Fatalf("ListRequestAudits(operation=%s) error = %v", tt.operation, err)
			}
			if got := requestAuditIDs(audits); !reflect.DeepEqual(got, tt.wantIDs) {
				t.Fatalf("ListRequestAudits(operation=%s) ids = %v, want %v", tt.operation, got, tt.wantIDs)
			}
			if len(audits) != 1 || audits[0].Operation != tt.operation {
				t.Fatalf("ListRequestAudits(operation=%s) view = %+v, want derived operation", tt.operation, audits)
			}
		})
	}
}

func openAuditTestClient(t *testing.T) *dao.Client {
	t.Helper()
	db, err := stdsql.Open("sqlite", filepath.Join(t.TempDir(), "audit.sqlite")+"?_fk=1")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign_keys pragma: %v", err)
	}
	client := enttest.NewClient(t, enttest.WithOptions(dao.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	})
	return client
}

type requestAuditSeed struct {
	id              string
	responseID      string
	conversationID  string
	method          string
	path            string
	clientRequestID string
	status          string
	createdAt       time.Time
}

func mustCreateRequestAudit(t *testing.T, client *dao.Client, seed requestAuditSeed) {
	t.Helper()
	create := client.RequestAudit.Create().
		SetID(seed.id).
		SetMethod(seed.method).
		SetPath(seed.path).
		SetHeaderJSON(map[string]any{"content-type": "application/json"}).
		SetBodyPreview(`{"model":"test"}`).
		SetBodySha256("sha").
		SetStatus(seed.status).
		SetCreatedAt(seed.createdAt)
	if seed.responseID != "" {
		create.SetResponseID(seed.responseID)
	}
	if seed.conversationID != "" {
		create.SetConversationID(seed.conversationID)
	}
	if seed.clientRequestID != "" {
		create.SetClientRequestID(seed.clientRequestID)
	}
	if err := create.Exec(context.Background()); err != nil {
		t.Fatalf("create request audit %s: %v", seed.id, err)
	}
}

type executionEventSeed struct {
	id             string
	responseID     string
	requestAuditID string
	conversationID string
	eventType      string
	phase          string
	status         string
	message        string
	detailsJSON    map[string]any
	occurredAt     time.Time
}

func mustCreateExecutionEvent(t *testing.T, client *dao.Client, seed executionEventSeed) {
	t.Helper()
	create := client.ExecutionEvent.Create().
		SetID(seed.id).
		SetEventType(seed.eventType).
		SetOccurredAt(seed.occurredAt)
	if seed.detailsJSON != nil {
		create.SetDetailsJSON(seed.detailsJSON)
	} else {
		create.SetDetailsJSON(map[string]any{"seed": seed.id})
	}
	if seed.responseID != "" {
		create.SetResponseID(seed.responseID)
	}
	if seed.requestAuditID != "" {
		create.SetRequestAuditID(seed.requestAuditID)
	}
	if seed.conversationID != "" {
		create.SetConversationID(seed.conversationID)
	}
	if seed.phase != "" {
		create.SetPhase(seed.phase)
	}
	if seed.status != "" {
		create.SetStatus(seed.status)
	}
	if seed.message != "" {
		create.SetMessage(seed.message)
	}
	if err := create.Exec(context.Background()); err != nil {
		t.Fatalf("create execution event %s: %v", seed.id, err)
	}
}

type upstreamExchangeSeed struct {
	id             string
	responseID     string
	requestAuditID string
	upstreamID     string
	model          string
	endpoint       string
	statusCode     int
	startedAt      time.Time
	completedAt    time.Time
}

func mustCreateUpstreamExchange(t *testing.T, client *dao.Client, seed upstreamExchangeSeed) {
	t.Helper()
	create := client.UpstreamExchange.Create().SetID(seed.id)
	if seed.responseID != "" {
		create.SetResponseID(seed.responseID)
	}
	if seed.requestAuditID != "" {
		create.SetRequestAuditID(seed.requestAuditID)
	}
	if seed.upstreamID != "" {
		create.SetUpstreamID(seed.upstreamID)
	}
	if seed.model != "" {
		create.SetModel(seed.model)
	}
	if seed.endpoint != "" {
		create.SetEndpoint(seed.endpoint)
	}
	if seed.statusCode != 0 {
		create.SetStatusCode(seed.statusCode)
	}
	if !seed.startedAt.IsZero() {
		create.SetStartedAt(seed.startedAt)
	}
	if !seed.completedAt.IsZero() {
		create.SetCompletedAt(seed.completedAt)
	}
	if err := create.Exec(context.Background()); err != nil {
		t.Fatalf("create upstream exchange %s: %v", seed.id, err)
	}
}

func requestAuditIDs(audits []RequestAuditView) []string {
	ids := make([]string, 0, len(audits))
	for _, audit := range audits {
		ids = append(ids, audit.ID)
	}
	return ids
}

func executionEventIDs(events []ExecutionEventView) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}

func upstreamExchangeIDs(exchanges []UpstreamExchangeView) []string {
	ids := make([]string, 0, len(exchanges))
	for _, exchange := range exchanges {
		ids = append(ids, exchange.ID)
	}
	return ids
}
