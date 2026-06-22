package functionexec

import (
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
)

func TestManagerApplyKeepsStableRegistryPointer(t *testing.T) {
	manager, err := NewManager(config.ResponsesFunctionExecutorConfig{
		Enabled: true,
		Executors: []config.ResponsesFunctionExecutorBinding{
			{
				Name:   "lookup",
				Type:   config.ResponsesFunctionExecutorTypeStaticResponse,
				Output: "old",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	registry := manager.Registry()
	if got := registry.Snapshot(); len(got) != 1 {
		t.Fatalf("initial registry snapshot len = %d, want 1", len(got))
	}

	if err := manager.Apply(config.ResponsesFunctionExecutorConfig{
		Enabled: true,
		Executors: []config.ResponsesFunctionExecutorBinding{
			{
				Name:   "summarize",
				Type:   config.ResponsesFunctionExecutorTypeStaticResponse,
				Output: "new",
			},
		},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if manager.Registry() != registry {
		t.Fatalf("Registry() pointer changed after Apply")
	}
	snapshot := registry.Snapshot()
	if _, ok := snapshot["lookup"]; ok {
		t.Fatalf("registry snapshot still contains old executor: %+v", snapshot)
	}
	if _, ok := snapshot["summarize"]; !ok || len(snapshot) != 1 {
		t.Fatalf("registry snapshot = %+v, want only summarize", snapshot)
	}
}
