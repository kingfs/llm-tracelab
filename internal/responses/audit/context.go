package audit

import (
	"context"
	"net/http"
)

type requestAuditIDContextKey struct{}
type correlationHeadersContextKey struct{}

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

func ContextWithCorrelationHeaders(ctx context.Context, header http.Header) context.Context {
	correlation := CorrelationHeaders(header)
	if len(correlation) == 0 {
		return ctx
	}
	return context.WithValue(ctx, correlationHeadersContextKey{}, correlation)
}

func CorrelationHeadersFromContext(ctx context.Context) (http.Header, bool) {
	header, ok := ctx.Value(correlationHeadersContextKey{}).(http.Header)
	if !ok || len(header) == 0 {
		return nil, false
	}
	return cloneHeader(header), true
}
