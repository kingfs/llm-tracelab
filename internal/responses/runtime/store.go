package runtime

import (
	"context"
	"sync"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

type Store interface {
	Put(ctx context.Context, response protocol.Response, request protocol.CreateResponseRequest, inputItems []protocol.InputItem, outputItems []protocol.OutputItem) error
	Get(ctx context.Context, id string) (protocol.Response, bool, error)
	UpdateStatus(ctx context.Context, id string, status string) (protocol.Response, bool, error)
	UpdateStatusMetadata(ctx context.Context, id string, status string, metadata map[string]any) (protocol.Response, bool, error)
	InputItems(ctx context.Context, id string) ([]protocol.InputItem, bool, error)
	ContinuationItems(ctx context.Context, id string) ([]LedgerItem, bool, error)
}

type LedgerItem struct {
	Input  *protocol.InputItem
	Output *protocol.OutputItem
}

type MemoryStore struct {
	mu            sync.RWMutex
	responses     map[string]protocol.Response
	requests      map[string]protocol.CreateResponseRequest
	inputs        map[string][]protocol.InputItem
	outputs       map[string][]protocol.OutputItem
	conversations map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		responses:     map[string]protocol.Response{},
		requests:      map[string]protocol.CreateResponseRequest{},
		inputs:        map[string][]protocol.InputItem{},
		outputs:       map[string][]protocol.OutputItem{},
		conversations: map[string]string{},
	}
}

func (s *MemoryStore) Put(ctx context.Context, response protocol.Response, request protocol.CreateResponseRequest, inputItems []protocol.InputItem, outputItems []protocol.OutputItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[response.ID] = cloneResponseValue(response)
	s.requests[response.ID] = request
	s.inputs[response.ID] = cloneInputItems(inputItems)
	s.outputs[response.ID] = cloneOutputItems(outputItems)
	if conversationID := codexConversationID(response.Metadata); conversationID != "" {
		s.conversations[conversationID] = response.ID
	}
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id string) (protocol.Response, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp, ok := s.responses[id]
	return cloneResponseValue(resp), ok, nil
}

func (s *MemoryStore) UpdateStatus(ctx context.Context, id string, status string) (protocol.Response, bool, error) {
	return s.UpdateStatusMetadata(ctx, id, status, nil)
}

func (s *MemoryStore) UpdateStatusMetadata(ctx context.Context, id string, status string, metadata map[string]any) (protocol.Response, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, ok := s.responses[id]
	if !ok {
		return protocol.Response{}, false, nil
	}
	resp.Status = status
	if metadata != nil {
		resp.Metadata = mergeMetadata(resp.Metadata, metadata)
	}
	s.responses[id] = cloneResponseValue(resp)
	return cloneResponseValue(resp), true, nil
}

func (s *MemoryStore) InputItems(ctx context.Context, id string) ([]protocol.InputItem, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.responses[id]; !ok {
		return nil, false, nil
	}
	return cloneInputItems(s.inputs[id]), true, nil
}

func (s *MemoryStore) ContinuationItems(ctx context.Context, id string) ([]LedgerItem, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.responses[id]; !ok {
		return nil, false, nil
	}
	ids := make([]string, 0, 8)
	for current := id; current != ""; {
		resp, ok := s.responses[current]
		if !ok {
			return nil, false, nil
		}
		ids = append(ids, current)
		if responseHasCompactRequest(s.inputs[current]) || responseIsCodexCompactCandidate(resp.Metadata) {
			break
		}
		current = resp.PreviousResponseID
	}
	items := make([]LedgerItem, 0, len(ids)*2)
	for i := len(ids) - 1; i >= 0; i-- {
		responseID := ids[i]
		for _, item := range s.inputs[responseID] {
			copied := cloneInputItem(item)
			items = append(items, LedgerItem{Input: &copied})
		}
		for _, item := range s.outputs[responseID] {
			copied := cloneOutputItem(item)
			items = append(items, LedgerItem{Output: &copied})
		}
	}
	return items, true, nil
}

func (s *MemoryStore) LatestResponseIDByConversation(ctx context.Context, conversationID string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.conversations[conversationID]
	return id, ok, nil
}

func responseHasCompactRequest(inputs []protocol.InputItem) bool {
	for _, input := range inputs {
		if input.Type == "compact_request" {
			return true
		}
	}
	return false
}

func responseIsCodexCompactCandidate(metadata map[string]any) bool {
	if boolFromMap(metadata, "codex_compact_candidate") {
		return true
	}
	gateway, ok := stringAnyMap(metadata["_gateway"])
	if !ok {
		return false
	}
	if boolFromMap(gateway, "codex_compact_candidate") {
		return true
	}
	classification, ok := stringAnyMap(gateway["request_classification"])
	if !ok {
		return false
	}
	if class, _ := classification["class"].(string); class == "codex_compact_candidate" {
		return true
	}
	return boolFromMap(classification, "codex_compact_candidate")
}

func codexConversationID(metadata map[string]any) string {
	codex := map[string]any{}
	if topLevel, ok := stringAnyMap(metadata["codex"]); ok {
		mergeInto(codex, topLevel)
	}
	if gateway, ok := stringAnyMap(metadata["_gateway"]); ok {
		if gatewayCodex, ok := stringAnyMap(gateway["codex"]); ok {
			mergeInto(codex, gatewayCodex)
		}
	}
	value, _ := codex["thread_id"].(string)
	return value
}

func boolFromMap(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key].(bool)
	return ok && value
}

func mergeMetadata(existing map[string]any, updates map[string]any) map[string]any {
	merged := map[string]any{}
	mergeInto(merged, existing)
	mergeInto(merged, updates)
	return merged
}

func mergeInto(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if dstNested, ok := stringAnyMap(dst[key]); ok {
			if srcNested, ok := stringAnyMap(value); ok {
				mergedNested := map[string]any{}
				mergeInto(mergedNested, dstNested)
				mergeInto(mergedNested, srcNested)
				dst[key] = mergedNested
				continue
			}
		}
		dst[key] = value
	}
}

func stringAnyMap(value any) (map[string]any, bool) {
	mapped, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return mapped, true
}
