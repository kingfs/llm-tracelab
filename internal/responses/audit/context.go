package audit

import "context"

type requestAuditIDContextKey struct{}

func ContextWithRequestAuditID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestAuditIDContextKey{}, id)
}

func RequestAuditIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestAuditIDContextKey{}).(string)
	return id, ok && id != ""
}
