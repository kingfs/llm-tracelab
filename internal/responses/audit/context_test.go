package audit

import (
	"context"
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
