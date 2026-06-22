package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/kingfs/llm-tracelab/ent/dao"
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
	update := a.client.RequestAudit.UpdateOneID(id).SetStatus("completed")
	if result.ResponseID != "" {
		update.SetResponseID(result.ResponseID)
	}
	if result.ConversationID != "" {
		update.SetConversationID(result.ConversationID)
	}
	return update.Exec(ctx)
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

func nilToEmptyMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}
