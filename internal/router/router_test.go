package router

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
)

func boolPtr(v bool) *bool { return &v }

func TestRouterSelectUsesModelCatalog(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "fallback",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-4.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://openrouter.ai/api/v1",
					ProviderPreset: "openrouter",
				},
			},
		},
	}

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-4.1","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "fallback" {
		t.Fatalf("selected target = %q, want fallback", selection.Target.ID)
	}
}

func TestRouterSingleTargetAllowsUnknownModels(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "default",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"unknown-future-model","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "default" {
		t.Fatalf("selected target = %q, want default", selection.Target.ID)
	}
}
