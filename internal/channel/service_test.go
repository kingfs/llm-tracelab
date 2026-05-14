package channel

import (
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
)

func TestBootstrapFromConfigImportsYAMLUpstreamsOnce(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	enabled := true
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        &enabled,
				Priority:       100,
				Weight:         1,
				CapacityHint:   2,
				ModelDiscovery: "static_only",
				StaticModels:   []string{"gpt-5", "GPT-5", "gpt-4.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ApiKey:         "sk-test-secret",
					ProviderPreset: "openai",
					Headers: map[string]string{
						"X-Test": "true",
					},
				},
			},
		},
	}

	svc := NewService(st)
	imported, err := svc.BootstrapFromConfig(cfg)
	if err != nil {
		t.Fatalf("BootstrapFromConfig() error = %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}
	imported, err = svc.BootstrapFromConfig(cfg)
	if err != nil {
		t.Fatalf("second BootstrapFromConfig() error = %v", err)
	}
	if imported != 0 {
		t.Fatalf("second imported = %d, want 0", imported)
	}

	targets, err := svc.RuntimeTargets()
	if err != nil {
		t.Fatalf("RuntimeTargets() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("len(targets) = %d, want 1", len(targets))
	}
	target := targets[0]
	if target.ID != "openai-primary" {
		t.Fatalf("target.ID = %q", target.ID)
	}
	if target.Upstream.ApiKey != "sk-test-secret" {
		t.Fatalf("target.Upstream.ApiKey = %q", target.Upstream.ApiKey)
	}
	if got := target.Upstream.Headers["X-Test"]; got != "true" {
		t.Fatalf("target header X-Test = %q", got)
	}
	if len(target.StaticModels) != 2 || target.StaticModels[0] != "gpt-4.1" || target.StaticModels[1] != "gpt-5" {
		t.Fatalf("target.StaticModels = %#v", target.StaticModels)
	}
}

func TestRuntimeTargetsSkipsDisabledChannelsAndModels(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "enabled",
		Name:           "Enabled",
		BaseURL:        "https://example.com/v1",
		ProviderPreset: "openai",
		HeadersJSON:    "{}",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig(enabled) error = %v", err)
	}
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "disabled",
		Name:           "Disabled",
		BaseURL:        "https://disabled.example.com/v1",
		ProviderPreset: "openai",
		HeadersJSON:    "{}",
		Enabled:        false,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig(disabled) error = %v", err)
	}
	if err := st.ReplaceChannelModels("enabled", []store.ChannelModelRecord{
		{Model: "gpt-5", Source: "manual", Enabled: true},
		{Model: "gpt-4.1", Source: "manual", Enabled: false},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}

	targets, err := NewService(st).RuntimeTargets()
	if err != nil {
		t.Fatalf("RuntimeTargets() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("len(targets) = %d, want 1", len(targets))
	}
	if targets[0].ID != "enabled" {
		t.Fatalf("target.ID = %q", targets[0].ID)
	}
	if len(targets[0].StaticModels) != 1 || targets[0].StaticModels[0] != "gpt-5" {
		t.Fatalf("StaticModels = %#v", targets[0].StaticModels)
	}
}
