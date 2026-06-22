package audit

import (
	"context"
	stdsql "database/sql"
	"path/filepath"
	"reflect"
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
	eventType      string
	phase          string
	status         string
	message        string
	occurredAt     time.Time
}

func mustCreateExecutionEvent(t *testing.T, client *dao.Client, seed executionEventSeed) {
	t.Helper()
	create := client.ExecutionEvent.Create().
		SetID(seed.id).
		SetEventType(seed.eventType).
		SetDetailsJSON(map[string]any{"seed": seed.id}).
		SetOccurredAt(seed.occurredAt)
	if seed.responseID != "" {
		create.SetResponseID(seed.responseID)
	}
	if seed.requestAuditID != "" {
		create.SetRequestAuditID(seed.requestAuditID)
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
