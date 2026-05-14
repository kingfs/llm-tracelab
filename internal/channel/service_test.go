package channel

import (
	"net/http"
	"net/http/httptest"
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

func TestProbeDiscoversModelsAndUpdatesCatalogs(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("request path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-probe" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-4.1"}]}`))
	}))
	defer upstreamServer.Close()

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:               "probe-channel",
		Name:             "Probe Channel",
		BaseURL:          upstreamServer.URL + "/v1",
		ProviderPreset:   "openai",
		APIKeyCiphertext: []byte("sk-probe"),
		HeadersJSON:      "{}",
		Enabled:          true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}

	result, err := NewService(st).Probe("probe-channel")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("Status = %q", result.Status)
	}
	if len(result.Models) != 2 || result.Models[0] != "gpt-4.1" || result.Models[1] != "gpt-5" {
		t.Fatalf("Models = %#v", result.Models)
	}

	models, err := st.ListChannelModels("probe-channel", true)
	if err != nil {
		t.Fatalf("ListChannelModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(channel models) = %d, want 2", len(models))
	}
	upstreamModels, err := st.ListUpstreamModels()
	if err != nil {
		t.Fatalf("ListUpstreamModels() error = %v", err)
	}
	if len(upstreamModels) != 2 {
		t.Fatalf("len(upstream models) = %d, want 2", len(upstreamModels))
	}
	runs, err := st.ListChannelProbeRuns("probe-channel", 10)
	if err != nil {
		t.Fatalf("ListChannelProbeRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "success" || runs[0].DiscoveredCount != 2 {
		t.Fatalf("probe runs = %#v", runs)
	}
}

func TestProbeFailureRecordsRunAndKeepsExistingModels(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no models", http.StatusInternalServerError)
	}))
	defer upstreamServer.Close()

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "probe-channel",
		Name:           "Probe Channel",
		BaseURL:        upstreamServer.URL + "/v1",
		ProviderPreset: "openai",
		HeadersJSON:    "{}",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	if err := st.ReplaceChannelModels("probe-channel", []store.ChannelModelRecord{
		{Model: "existing-model", Source: "manual", Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}

	result, err := NewService(st).Probe("probe-channel")
	if err == nil {
		t.Fatalf("Probe() error = nil, want failure")
	}
	if result.Status != "failed" {
		t.Fatalf("Status = %q, want failed", result.Status)
	}

	models, err := st.ListChannelModels("probe-channel", true)
	if err != nil {
		t.Fatalf("ListChannelModels() error = %v", err)
	}
	if len(models) != 1 || models[0].Model != "existing-model" {
		t.Fatalf("models after failed probe = %#v", models)
	}
	runs, err := st.ListChannelProbeRuns("probe-channel", 10)
	if err != nil {
		t.Fatalf("ListChannelProbeRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorText == "" {
		t.Fatalf("probe runs = %#v", runs)
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
