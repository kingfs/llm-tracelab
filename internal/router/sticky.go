package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultStickyBindingTTL = time.Hour

type stickyBinding struct {
	targetID  string
	expiresAt time.Time
}

type StickyBindingStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	bindings map[string]stickyBinding
	now      func() time.Time
}

func NewStickyBindingStore(ttl time.Duration) *StickyBindingStore {
	if ttl <= 0 {
		ttl = defaultStickyBindingTTL
	}
	return &StickyBindingStore{
		ttl:      ttl,
		bindings: make(map[string]stickyBinding),
		now:      time.Now,
	}
}

func (s *StickyBindingStore) Lookup(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if s == nil || key == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.bindings[key]
	if !ok {
		return "", false
	}
	if !binding.expiresAt.IsZero() && !s.now().Before(binding.expiresAt) {
		delete(s.bindings, key)
		return "", false
	}
	return binding.targetID, true
}

func (s *StickyBindingStore) Bind(key string, targetID string) {
	key = strings.TrimSpace(key)
	targetID = strings.TrimSpace(targetID)
	if s == nil || key == "" || targetID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[key] = stickyBinding{
		targetID:  targetID,
		expiresAt: s.now().Add(s.ttl),
	}
}

func extractStickyKey(req *http.Request, body []byte) string {
	if req == nil {
		return extractStickyKeyFromBody(body)
	}
	if key := strings.TrimSpace(req.Header.Get("Session_id")); key != "" {
		return key
	}
	if key := stickyWindowPrefix(req.Header.Get("X-Codex-Window-Id")); key != "" {
		return key
	}
	if key := stickySessionFromMetadata(req.Header.Get("X-Codex-Turn-Metadata")); key != "" {
		return key
	}
	return extractStickyKeyFromBody(body)
}

func stickyWindowPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	prefix, _, ok := strings.Cut(raw, ":")
	if !ok {
		return raw
	}
	return strings.TrimSpace(prefix)
}

func stickySessionFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

func extractStickyKeyFromBody(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !json.Valid(body) {
		return ""
	}
	var payload struct {
		PreviousResponseID string `json:"previous_response_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.PreviousResponseID)
}
