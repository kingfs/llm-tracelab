package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

const (
	ToolTypeWebSearch          = "web_search"
	ToolTypeMCP                = "mcp"
	ToolTypeFileSearch         = "file_search"
	ToolTypeCodeInterpreter    = "code_interpreter"
	ToolTypeComputerUsePreview = "computer_use_preview"
)

type Status string

const (
	StatusCompleted Status = "completed"
)

var ErrNilExecutor = errors.New("hosted tool executor is nil")

type ToolContext struct {
	ResponseID string          `json:"response_id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Tools      []protocol.Tool `json:"tools,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Tool      protocol.Tool   `json:"tool"`
}

type ToolResult struct {
	Output  any         `json:"output,omitempty"`
	Status  Status      `json:"status,omitempty"`
	Summary SafeSummary `json:"summary,omitempty"`
}

type SafeSummary struct {
	Arguments RedactedValue  `json:"arguments,omitempty"`
	Output    RedactedValue  `json:"output,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

type RedactedValue struct {
	Value    any  `json:"value,omitempty"`
	Redacted bool `json:"redacted,omitempty"`
}

type Executor interface {
	Type() string
	Enabled(ToolContext) bool
	ExecuteHostedTool(context.Context, ToolContext, ToolCall) (ToolResult, error)
}

type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

func NewRegistry(executors ...Executor) (*Registry, error) {
	r := &Registry{}
	for _, executor := range executors {
		if err := r.Register(executor); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(executor Executor) error {
	if r == nil {
		return nil
	}
	if isNilExecutor(executor) {
		return ErrNilExecutor
	}
	toolType := normalizeToolType(executor.Type())
	if toolType == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executors == nil {
		r.executors = map[string]Executor{}
	}
	r.executors[toolType] = executor
	return nil
}

func (r *Registry) Resolve(toolType string) (Executor, bool) {
	if r == nil {
		return nil, false
	}
	toolType = normalizeToolType(toolType)
	if toolType == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, ok := r.executors[toolType]
	return executor, ok
}

func (r *Registry) List(ctx ToolContext) []Executor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	executors := make([]Executor, 0, len(r.executors))
	for _, executor := range r.executors {
		executors = append(executors, executor)
	}
	r.mu.RUnlock()

	out := make([]Executor, 0, len(executors))
	for _, executor := range executors {
		if isNilExecutor(executor) || !executor.Enabled(ctx) {
			continue
		}
		out = append(out, executor)
	}
	sort.Slice(out, func(i, j int) bool {
		return normalizeToolType(out[i].Type()) < normalizeToolType(out[j].Type())
	})
	return out
}

func normalizeToolType(toolType string) string {
	return strings.TrimSpace(toolType)
}

func isNilExecutor(executor Executor) bool {
	if executor == nil {
		return true
	}
	value := reflect.ValueOf(executor)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
