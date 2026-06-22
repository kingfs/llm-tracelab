package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/executionevent"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamexchange"
)

type EntAuditor struct {
	client *dao.Client
}

func NewEntAuditor(client *dao.Client) *EntAuditor {
	return &EntAuditor{client: client}
}

func (a *EntAuditor) Accepted(ctx context.Context, entry RequestEntry) (string, error) {
	if a == nil || a.client == nil {
		return "", nil
	}
	id := "reqaudit_" + uuid.NewString()
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	create := a.client.RequestAudit.Create().
		SetID(id).
		SetMethod(entry.Method).
		SetPath(entry.Path).
		SetHeaderJSON(nilToEmptyMap(entry.HeaderJSON)).
		SetBodyPreview(entry.BodyPreview).
		SetBodySha256(entry.BodySha256).
		SetStatus("accepted").
		SetCreatedAt(createdAt)
	if entry.ClientRequestID != "" {
		create.SetClientRequestID(entry.ClientRequestID)
	}
	if err := create.Exec(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (a *EntAuditor) Completed(ctx context.Context, id string, result Completion) error {
	if a == nil || a.client == nil || id == "" {
		return nil
	}
	tx, err := a.client.Tx(ctx)
	if err != nil {
		return err
	}
	client := tx.Client()
	update := client.RequestAudit.UpdateOneID(id).SetStatus("completed")
	if result.ResponseID != "" {
		update.SetResponseID(result.ResponseID)
	}
	if result.ConversationID != "" {
		update.SetConversationID(result.ConversationID)
	}
	if err := update.Exec(ctx); err != nil {
		return rollback(tx, err)
	}
	if result.ResponseID != "" {
		if _, err := client.UpstreamExchange.Update().
			Where(upstreamexchange.RequestAuditIDEQ(id)).
			SetResponseID(result.ResponseID).
			Save(ctx); err != nil {
			return rollback(tx, err)
		}
		if _, err := client.ExecutionEvent.Update().
			Where(executionevent.RequestAuditIDEQ(id), executionevent.ResponseIDIsNil()).
			SetResponseID(result.ResponseID).
			Save(ctx); err != nil {
			return rollback(tx, err)
		}
	}
	return tx.Commit()
}

func (a *EntAuditor) Rejected(ctx context.Context, id string, failure Failure) error {
	if a == nil || a.client == nil || id == "" {
		return nil
	}
	status := failure.Status
	if status == "" {
		status = "failed"
	}
	update := a.client.RequestAudit.UpdateOneID(id).SetStatus(status)
	if failure.ErrorText != "" {
		update.SetErrorText(failure.ErrorText)
	}
	return update.Exec(ctx)
}

func (a *EntAuditor) RecordUpstreamExchange(ctx context.Context, entry UpstreamExchange) error {
	if a == nil || a.client == nil {
		return nil
	}
	create := a.client.UpstreamExchange.Create().SetID("upex_" + uuid.NewString())
	if entry.ResponseID != "" {
		create.SetResponseID(entry.ResponseID)
	}
	if entry.RequestAuditID != "" {
		create.SetRequestAuditID(entry.RequestAuditID)
	}
	if entry.TraceID != "" {
		create.SetTraceID(entry.TraceID)
	}
	if entry.CassettePath != "" {
		create.SetCassettePath(entry.CassettePath)
	}
	if entry.UpstreamID != "" {
		create.SetUpstreamID(entry.UpstreamID)
	}
	if entry.RouteTarget != "" {
		create.SetRouteTarget(entry.RouteTarget)
	}
	if entry.Model != "" {
		create.SetModel(entry.Model)
	}
	if entry.Endpoint != "" {
		create.SetEndpoint(entry.Endpoint)
	}
	if entry.StatusCode != 0 {
		create.SetStatusCode(entry.StatusCode)
	}
	if !entry.StartedAt.IsZero() {
		create.SetStartedAt(entry.StartedAt)
	}
	if !entry.CompletedAt.IsZero() {
		create.SetCompletedAt(entry.CompletedAt)
	}
	if entry.ErrorText != "" {
		create.SetErrorText(entry.ErrorText)
	}
	return create.Exec(ctx)
}

func (a *EntAuditor) RecordExecutionEvent(ctx context.Context, event ExecutionEvent) error {
	if a == nil || a.client == nil {
		return nil
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	requestAuditID := event.RequestAuditID
	if requestAuditID == "" {
		if id, ok := RequestAuditIDFromContext(ctx); ok {
			requestAuditID = id
		}
	}
	create := a.client.ExecutionEvent.Create().
		SetID("exev_" + uuid.NewString()).
		SetEventType(event.EventType).
		SetDetailsJSON(nilToEmptyMap(event.DetailsJSON)).
		SetOccurredAt(occurredAt)
	if event.ResponseID != "" {
		create.SetResponseID(event.ResponseID)
	}
	if requestAuditID != "" {
		create.SetRequestAuditID(requestAuditID)
	}
	if event.ConversationID != "" {
		create.SetConversationID(event.ConversationID)
	}
	if event.Phase != "" {
		create.SetPhase(event.Phase)
	}
	if event.Status != "" {
		create.SetStatus(event.Status)
	}
	if event.Message != "" {
		create.SetMessage(event.Message)
	}
	return create.Exec(ctx)
}

func nilToEmptyMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}

func rollback(tx *dao.Tx, err error) error {
	if tx == nil {
		return err
	}
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		return rollbackErr
	}
	return err
}
