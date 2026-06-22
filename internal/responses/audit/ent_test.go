package audit

import (
	"context"
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
