package audit

import (
	"context"
	"net/http"
	"testing"
)

func TestRequestAuditIDContext(t *testing.T) {
	ctx := context.Background()
	if id, ok := RequestAuditIDFromContext(ctx); ok || id != "" {
		t.Fatalf("RequestAuditIDFromContext empty = %q/%v, want empty/false", id, ok)
	}

	withID := ContextWithRequestAuditID(ctx, "audit_1")
	if id, ok := RequestAuditIDFromContext(withID); !ok || id != "audit_1" {
		t.Fatalf("RequestAuditIDFromContext = %q/%v, want audit_1/true", id, ok)
	}

	unchanged := ContextWithRequestAuditID(ctx, "")
	if id, ok := RequestAuditIDFromContext(unchanged); ok || id != "" {
		t.Fatalf("RequestAuditIDFromContext empty id = %q/%v, want empty/false", id, ok)
	}
}

func TestCorrelationHeadersContext(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer client-secret")
	header.Set("Session-Id", "sess-1")
	header.Set("Thread-Id", "thread-1")
	header.Set("X-Codex-Turn-Metadata", `{"session_id":"sess-1"}`)
	header.Set("X-Client-Request-Id", "client-1")

	ctx := ContextWithCorrelationHeaders(context.Background(), header)
	got, ok := CorrelationHeadersFromContext(ctx)
	if !ok {
		t.Fatal("CorrelationHeadersFromContext ok = false, want true")
	}
	if got.Get("Session-Id") != "sess-1" || got.Get("Thread-Id") != "thread-1" || got.Get("X-Client-Request-Id") != "client-1" {
		t.Fatalf("correlation headers = %#v", got)
	}
	if got.Get("Authorization") != "" {
		t.Fatalf("Authorization was copied: %#v", got)
	}
}
