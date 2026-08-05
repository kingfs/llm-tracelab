package functionexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
	responsesruntime "github.com/kingfs/llm-tracelab/internal/responses/runtime"
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

func TestRegistrationsPassExternalCommandSandboxPolicy(t *testing.T) {
	commandDir := t.TempDir()
	commandPath := filepath.Join(commandDir, "lookup")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write command fixture: %v", err)
	}
	registrations, err := Registrations(config.ResponsesFunctionExecutorConfig{
		Enabled: true,
		Executors: []config.ResponsesFunctionExecutorBinding{
			{
				Name:    "lookup",
				Type:    config.ResponsesFunctionExecutorTypeExternalCommand,
				Command: commandPath,
				Process: config.ResponsesFunctionExecutorProcessConfig{
					AllowedCommandDirs: []string{commandDir},
					RejectRoot:         true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Registrations() error = %v", err)
	}
	registration := registrations["lookup"]
	if registration.Executor == nil {
		t.Fatalf("registration lookup executor is nil")
	}
	executor, ok := registration.Executor.(responsesruntime.ExternalCommandFunctionToolExecutor)
	if !ok {
		t.Fatalf("registration executor type = %T, want ExternalCommandFunctionToolExecutor", registration.Executor)
	}
	if len(executor.AllowedCommandDirs) != 1 || executor.AllowedCommandDirs[0] != commandDir || !executor.RejectRoot {
		t.Fatalf("executor sandbox fields = dirs %+v reject_root %v, want configured values", executor.AllowedCommandDirs, executor.RejectRoot)
	}
}
