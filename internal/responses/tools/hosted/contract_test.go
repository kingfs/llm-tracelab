package hosted

import (
	"context"
	"errors"
	"testing"
)

type fakeExecutor struct {
	toolType string
	enabled  bool
	output   any
}

func (e *fakeExecutor) Type() string {
	return e.toolType
}

func (e *fakeExecutor) Enabled(ToolContext) bool {
	return e.enabled
}

func (e *fakeExecutor) ExecuteHostedTool(context.Context, ToolContext, ToolCall) (ToolResult, error) {
	return ToolResult{Output: e.output}, nil
}

func TestRegistryRegisterResolve(t *testing.T) {
	executor := &fakeExecutor{toolType: ToolTypeWebSearch, enabled: true, output: "ok"}
	registry, err := NewRegistry(executor)
	if err != nil {
		t.Fatalf("NewRegistry error = %v", err)
	}

	got, ok := registry.Resolve(ToolTypeWebSearch)
	if !ok {
		t.Fatalf("Resolve(%q) ok = false, want true", ToolTypeWebSearch)
	}
	if got != executor {
		t.Fatalf("Resolve(%q) executor = %p, want %p", ToolTypeWebSearch, got, executor)
	}
}

func TestRegistryListExcludesDisabledExecutor(t *testing.T) {
	enabled := &fakeExecutor{toolType: ToolTypeWebSearch, enabled: true}
	disabled := &fakeExecutor{toolType: ToolTypeMCP, enabled: false}
	registry, err := NewRegistry(disabled, enabled)
	if err != nil {
		t.Fatalf("NewRegistry error = %v", err)
	}

	got := registry.List(ToolContext{})
	if len(got) != 1 {
		t.Fatalf("List length = %d, want 1", len(got))
	}
	if got[0] != enabled {
		t.Fatalf("List[0] = %p, want enabled executor %p", got[0], enabled)
	}
}

func TestRegistryDuplicateToolTypeOverwrites(t *testing.T) {
	first := &fakeExecutor{toolType: ToolTypeWebSearch, enabled: true, output: "first"}
	second := &fakeExecutor{toolType: ToolTypeWebSearch, enabled: true, output: "second"}
	registry, err := NewRegistry(first)
	if err != nil {
		t.Fatalf("NewRegistry error = %v", err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatalf("Register duplicate error = %v", err)
	}

	got, ok := registry.Resolve(ToolTypeWebSearch)
	if !ok {
		t.Fatalf("Resolve(%q) ok = false, want true", ToolTypeWebSearch)
	}
	if got != second {
		t.Fatalf("duplicate Resolve executor = %p, want later executor %p", got, second)
	}
}

func TestRegistryNilExecutor(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry error = %v", err)
	}

	if err := registry.Register(nil); !errors.Is(err, ErrNilExecutor) {
		t.Fatalf("Register(nil) error = %v, want ErrNilExecutor", err)
	}

	var typedNil *fakeExecutor
	if err := registry.Register(typedNil); !errors.Is(err, ErrNilExecutor) {
		t.Fatalf("Register(typed nil) error = %v, want ErrNilExecutor", err)
	}
}
