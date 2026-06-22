package audit

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestEntAuditorCompletedPreservesExplicitExecutionEventResponseID(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	auditor := NewEntAuditor(client)
	base := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)

	mustCreateRequestAudit(t, client, requestAuditSeed{
		id:        "audit_completed",
		method:    "POST",
		path:      "/v1/responses",
		status:    "accepted",
		createdAt: base,
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "event_missing_response",
		requestAuditID: "audit_completed",
		eventType:      "response.model_call",
		status:         "completed",
		occurredAt:     base.Add(time.Second),
	})
	mustCreateExecutionEvent(t, client, executionEventSeed{
		id:             "event_explicit_response",
		requestAuditID: "audit_completed",
		responseID:     "resp_compact",
		eventType:      "response.compact",
		status:         "auto_triggered",
		occurredAt:     base.Add(2 * time.Second),
	})

	if err := auditor.Completed(ctx, "audit_completed", Completion{ResponseID: "resp_final"}); err != nil {
		t.Fatalf("Completed() error = %v", err)
	}

	missingResponse, err := client.ExecutionEvent.Get(ctx, "event_missing_response")
	if err != nil {
		t.Fatalf("get event_missing_response: %v", err)
	}
	if missingResponse.ResponseID != "resp_final" {
		t.Fatalf("missing response event response_id = %q, want resp_final", missingResponse.ResponseID)
	}
	explicitResponse, err := client.ExecutionEvent.Get(ctx, "event_explicit_response")
	if err != nil {
		t.Fatalf("get event_explicit_response: %v", err)
	}
	if explicitResponse.ResponseID != "resp_compact" {
		t.Fatalf("explicit response event response_id = %q, want resp_compact", explicitResponse.ResponseID)
	}
}

func TestEntAuditorRecordAndQueryToolCallAudit(t *testing.T) {
	ctx := context.Background()
	client := openAuditTestClient(t)
	auditor := NewEntAuditor(client)
	base := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)

	mustCreateRequestAudit(t, client, requestAuditSeed{
		id:        "audit_tool_call",
		method:    "POST",
		path:      "/v1/responses",
		status:    "accepted",
		createdAt: base,
	})

	id, err := auditor.RecordToolCallAudit(ContextWithRequestAuditID(ctx, "audit_tool_call"), ToolCallAudit{
		CallID:       "call_lookup",
		ToolType:     "function",
		ToolName:     "lookup",
		Executor:     "function_executor:lookup",
		Status:       "completed",
		InputJSON:    map[string]any{"q": "codex"},
		OutputJSON:   map[string]any{"ok": true},
		MetadataJSON: map[string]any{"iteration": "1"},
		StartedAt:    base.Add(time.Second),
		CompletedAt:  base.Add(2 * time.Second),
		CreatedAt:    base.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("RecordToolCallAudit() error = %v", err)
	}
	if id == "" {
		t.Fatalf("RecordToolCallAudit() id is empty")
	}

	records, err := NewQueryService(client).ListToolCallAudits(ctx, ListToolCallAuditsParams{
		RequestAuditID: "audit_tool_call",
		Status:         "completed",
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListToolCallAudits() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("ListToolCallAudits() len = %d, want 1: %#v", len(records), records)
	}
	record := records[0]
	if record.ID != id || record.RequestAuditID != "audit_tool_call" {
		t.Fatalf("ToolCallAudit identity = %+v, want id/%s request audit_tool_call", record, id)
	}
	if record.CallID != "call_lookup" || record.ToolType != "function" || record.ToolName != "lookup" || record.Executor != "function_executor:lookup" || record.Phase != "tool_call" || record.Status != "completed" {
		t.Fatalf("ToolCallAudit fields = %+v, want lookup completed tool_call", record)
	}
	if !reflect.DeepEqual(record.InputJSON, map[string]any{"q": "codex"}) {
		t.Fatalf("InputJSON = %#v, want q", record.InputJSON)
	}
	if !reflect.DeepEqual(record.OutputJSON, map[string]any{"ok": true}) {
		t.Fatalf("OutputJSON = %#v, want ok", record.OutputJSON)
	}
	if !reflect.DeepEqual(record.MetadataJSON, map[string]any{"iteration": "1"}) {
		t.Fatalf("MetadataJSON = %#v, want iteration", record.MetadataJSON)
	}
	if !record.StartedAt.Equal(base.Add(time.Second)) || !record.CompletedAt.Equal(base.Add(2*time.Second)) || !record.CreatedAt.Equal(base.Add(3*time.Second)) {
		t.Fatalf("timestamps = %s/%s/%s, want seeded times", record.StartedAt, record.CompletedAt, record.CreatedAt)
	}
}
