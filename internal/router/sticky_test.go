package router

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestExtractStickyKeyPrecedence(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"previous_response_id":"resp_body"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Session_id", "session_header")
	req.Header.Set("X-Codex-Window-Id", "window:turn")
	req.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"metadata_session"}`)

	if got := extractStickyKey(req, []byte(`{"previous_response_id":"resp_body"}`)); got != "session_header" {
		t.Fatalf("extractStickyKey() = %q, want session_header", got)
	}
}

func TestExtractStickyKeyFallbacks(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("X-Codex-Window-Id", "window-123:turn-456")
	if got := extractStickyKey(req, nil); got != "window-123" {
		t.Fatalf("extractStickyKey(window) = %q, want window-123", got)
	}

	req.Header.Del("X-Codex-Window-Id")
	req.Header.Set("X-Codex-Turn-Metadata", `{"session_id":"metadata-session"}`)
	if got := extractStickyKey(req, nil); got != "metadata-session" {
		t.Fatalf("extractStickyKey(metadata) = %q, want metadata-session", got)
	}

	req.Header.Del("X-Codex-Turn-Metadata")
	if got := extractStickyKey(req, []byte(`{"model":"gpt-5","previous_response_id":"resp_123"}`)); got != "resp_123" {
		t.Fatalf("extractStickyKey(body) = %q, want resp_123", got)
	}
}

func TestStickyBindingStoreExpires(t *testing.T) {
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	store := NewStickyBindingStore(time.Minute)
	store.now = func() time.Time { return now }
	store.Bind("session", "primary")
	if got, ok := store.Lookup("session"); !ok || got != "primary" {
		t.Fatalf("Lookup() = %q, %v; want primary, true", got, ok)
	}

	now = now.Add(time.Minute)
	if got, ok := store.Lookup("session"); ok || got != "" {
		t.Fatalf("Lookup(expired) = %q, %v; want empty, false", got, ok)
	}
}
