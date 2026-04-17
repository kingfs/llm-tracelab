package router

import (
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestRouterSelectReturnsStructuredNoSupportingTargetError(t *testing.T) {
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

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"claude-3-7-sonnet","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	_, err = rtr.Select(req)
	if err == nil {
		t.Fatalf("Select() error = nil, want structured error")
	}
	if SelectionFailureReason(err) != SelectionFailureNoSupportingTarget {
		t.Fatalf("SelectionFailureReason() = %q, want %q", SelectionFailureReason(err), SelectionFailureNoSupportingTarget)
	}
}

func TestRouterSelectAllowsModelListRequestsWithoutCatalogMatch(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
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
				ID:             "anthropic-secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"claude-sonnet-4-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.anthropic.com",
					ProviderPreset: "anthropic",
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

	req, err := http.NewRequest(http.MethodGet, "http://proxy.local/v1/models", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "openai-primary" {
		t.Fatalf("selected target = %q, want openai-primary", selection.Target.ID)
	}
}

func TestRouterSelectAllowsAnthropicModelListEndpoint(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "anthropic-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"claude-sonnet-4-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.anthropic.com",
					ProviderPreset: "anthropic",
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

	req, err := http.NewRequest(http.MethodGet, "http://proxy.local/v1/models", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "anthropic-primary" {
		t.Fatalf("selected target = %q, want anthropic-primary", selection.Target.ID)
	}
}

func TestRouterAggregatedModelsDeduplicatesAcrossUpstreams(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1", "gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1", "claude-sonnet-4-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.anthropic.com",
					ProviderPreset: "anthropic",
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

	models := rtr.AggregatedModels()
	want := []string{"claude-sonnet-4-5", "glm-5.1", "gpt-5"}
	if len(models) != len(want) {
		t.Fatalf("len(models) = %d, want %d (%v)", len(models), len(want), models)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("models[%d] = %q, want %q (all=%v)", i, models[i], want[i], models)
		}
	}
}

func TestRouterAllowStaticFallbackRoutesUnknownModel(t *testing.T) {
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
				ID:             "static-fallback",
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
	cfg.Router.Fallback.OnMissingModel = "allow_static"

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
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target = %q, want primary", selection.Target.ID)
	}
}

func TestRouterSnapshotsExposeHealthAndModels(t *testing.T) {
	cfg := &config.Config{
		Router: config.RouterConfig{},
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5", "gpt-4.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = 50 * time.Millisecond

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	rtr.Complete(selection, Outcome{Success: false})

	snapshots := rtr.Snapshots()
	if len(snapshots) != 1 {
		t.Fatalf("len(snapshots) = %d, want 1", len(snapshots))
	}
	if snapshots[0].HealthState != HealthOpen {
		t.Fatalf("HealthState = %q, want %q", snapshots[0].HealthState, HealthOpen)
	}
	if len(snapshots[0].Models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(snapshots[0].Models))
	}
	if snapshots[0].LastRefreshStatus == "" {
		t.Fatalf("LastRefreshStatus is empty")
	}
}

func TestRouterCostAwareSelectionPrefersLowerObservedCost(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "fast",
				Enabled:        boolPtr(true),
				Priority:       50,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "slow",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://openrouter.ai/api/v1",
					ProviderPreset: "openrouter",
				},
			},
		},
	}
	cfg.Router.Selection.Epsilon = 0

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	reqFeatures := RequestFeatures{ModelName: "gpt-5", RequestBytes: 256, EstPromptTokens: 64, MaxTokens: 256}
	var fast *Target
	var slow *Target
	for _, target := range rtr.targets {
		switch target.ID {
		case "fast":
			fast = target
		case "slow":
			slow = target
		}
	}
	if fast == nil || slow == nil {
		t.Fatalf("missing test targets fast=%v slow=%v", fast, slow)
	}
	fast.onFinish(reqFeatures, Outcome{Success: true, StatusCode: 200, DurationMs: 400, TTFTMs: 80}, rtr.costs, rtr.failureThreshold, rtr.openWindow)
	slow.onFinish(reqFeatures, Outcome{Success: true, StatusCode: 200, DurationMs: 5000, TTFTMs: 1800}, rtr.costs, rtr.failureThreshold, rtr.openWindow)

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "fast" {
		t.Fatalf("selected target = %q, want fast", selection.Target.ID)
	}
}

func TestRouterCostAwareSelectionAvoidsDegradedTarget(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "stable",
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
				ID:             "flaky",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://openrouter.ai/api/v1",
					ProviderPreset: "openrouter",
				},
			},
		},
	}
	cfg.Router.Selection.Epsilon = 0

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	reqFeatures := RequestFeatures{ModelName: "gpt-5", RequestBytes: 256, EstPromptTokens: 64, MaxTokens: 256}
	var stable *Target
	var flaky *Target
	for _, target := range rtr.targets {
		switch target.ID {
		case "stable":
			stable = target
		case "flaky":
			flaky = target
		}
	}
	if stable == nil || flaky == nil {
		t.Fatalf("missing test targets stable=%v flaky=%v", stable, flaky)
	}
	stable.onFinish(reqFeatures, Outcome{Success: true, StatusCode: 200, DurationMs: 700, TTFTMs: 120}, rtr.costs, rtr.failureThreshold, rtr.openWindow)
	for i := 0; i < 3; i++ {
		flaky.onFinish(reqFeatures, Outcome{Success: false, StatusCode: 500, DurationMs: 1200, TTFTMs: 0}, rtr.costs, rtr.failureThreshold, rtr.openWindow)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "stable" {
		t.Fatalf("selected target = %q, want stable", selection.Target.ID)
	}
	snapshots := rtr.Snapshots()
	var flakySnapshot Snapshot
	for _, snapshot := range snapshots {
		if snapshot.ID == "flaky" {
			flakySnapshot = snapshot
		}
	}
	if flakySnapshot.HealthState != HealthOpen && flakySnapshot.HealthState != HealthDegraded {
		t.Fatalf("flaky health = %q, want open/degraded", flakySnapshot.HealthState)
	}
}

func TestRouterSelectReturnsStructuredAllTargetsOpenError(t *testing.T) {
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
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://openrouter.ai/api/v1",
					ProviderPreset: "openrouter",
				},
			},
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Minute
	cfg.Router.Selection.Epsilon = 0

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	reqFeatures := RequestFeatures{ModelName: "gpt-5", RequestBytes: 256, EstPromptTokens: 64, MaxTokens: 256}
	for _, target := range rtr.targets {
		target.onFinish(reqFeatures, Outcome{Success: false, StatusCode: 503, DurationMs: 1000, TTFTMs: 0}, rtr.costs, rtr.failureThreshold, rtr.openWindow)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	_, err = rtr.Select(req)
	if err == nil {
		t.Fatalf("Select() error = nil, want structured error")
	}
	if SelectionFailureReason(err) != SelectionFailureAllTargetsOpen {
		t.Fatalf("SelectionFailureReason() = %q, want %q", SelectionFailureReason(err), SelectionFailureAllTargetsOpen)
	}
}
