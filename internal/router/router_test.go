package router

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/upstream"
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

func TestRouterReloadReplacesCatalogAndPreservesOldOnFailure(t *testing.T) {
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
		},
	}
	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	target := rtr.Targets()[0]
	target.onFinish(RequestFeatures{}, Outcome{Success: true, StatusCode: 200, DurationMs: 1234, TTFTMs: 321}, rtr.costs, 3, time.Minute)
	beforeLatency := target.snapshot().LatencyFastMs

	err = rtr.Reload([]config.UpstreamTargetConfig{
		{
			ID:             "primary",
			Enabled:        boolPtr(true),
			Priority:       100,
			ModelDiscovery: ModelDiscoveryStaticOnly,
			StaticModels:   []string{"gpt-4.1"},
			Upstream: config.UpstreamConfig{
				BaseURL:        "https://api.openai.com/v1",
				ProviderPreset: "openai",
			},
		},
	})
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if got := rtr.Targets()[0].snapshot().LatencyFastMs; got != beforeLatency {
		t.Fatalf("LatencyFastMs after reload = %v, want inherited %v", got, beforeLatency)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-4.1","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select(gpt-4.1) error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target = %q, want primary", selection.Target.ID)
	}

	err = rtr.Reload([]config.UpstreamTargetConfig{
		{
			ID:             "broken",
			Enabled:        boolPtr(true),
			ModelDiscovery: ModelDiscoveryStaticOnly,
			Upstream: config.UpstreamConfig{
				BaseURL: "://bad-url",
			},
		},
	})
	if err == nil {
		t.Fatalf("Reload(invalid) error = nil, want error")
	}
	selection, err = rtr.Select(req)
	if err != nil {
		t.Fatalf("Select after failed reload error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target after failed reload = %q, want primary", selection.Target.ID)
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

func TestRouterChatCompletionsRequiresChatCapableAPISurface(t *testing.T) {
	chatDisabled := false
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "native-responses",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
					APIType:        "responses_native",
					Capabilities: config.UpstreamCapabilitiesConfig{
						ChatCompletions: &chatDisabled,
					},
				},
			},
			{
				ID:             "chat",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://compat.example.com/v1",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
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

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "chat" {
		t.Fatalf("selected target = %q, want chat", selection.Target.ID)
	}
	if selection.Decision == nil || len(selection.Decision.Candidates) != 2 {
		t.Fatalf("selection decision missing candidates: %+v", selection.Decision)
	}
	for _, candidate := range selection.Decision.Candidates {
		if candidate.ID == "chat" && candidate.APIType != "chat_completions" {
			t.Fatalf("chat candidate APIType = %q, want chat_completions", candidate.APIType)
		}
		if candidate.ID == "native-responses" && (candidate.SupportsPath || candidate.FilterReason != "unsupported_path") {
			t.Fatalf("native responses candidate = %+v, want unsupported_path", candidate)
		}
	}
	snapshots := rtr.Snapshots()
	if len(snapshots) != 2 {
		t.Fatalf("len(snapshots) = %d, want 2", len(snapshots))
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == "native-responses" && snapshot.APIType != "responses_native" {
			t.Fatalf("native response snapshot APIType = %q, want responses_native", snapshot.APIType)
		}
	}
}

func TestRouterResponsesEndpointAllowsNativeResponsesTarget(t *testing.T) {
	chatDisabled := false
	responsesEnabled := true
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "native-responses",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
					APIType:        "responses_native",
					Mode:           "proxy",
					Capabilities: config.UpstreamCapabilitiesConfig{
						Responses:       &responsesEnabled,
						ChatCompletions: &chatDisabled,
					},
				},
			},
			{
				ID:             "chat",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://compat.example.com/v1",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
				},
			},
		},
	}
	cfg.Router.Selection.Policy = PolicyFirstAvailable

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
	if selection.Target.ID != "native-responses" {
		t.Fatalf("selected target = %q, want native-responses", selection.Target.ID)
	}
	if selection.Target.Upstream.APIType != "responses_native" {
		t.Fatalf("selected APIType = %q, want responses_native", selection.Target.Upstream.APIType)
	}
	if selection.Decision == nil || len(selection.Decision.Candidates) != 2 {
		t.Fatalf("selection decision missing candidates: %+v", selection.Decision)
	}
	for _, candidate := range selection.Decision.Candidates {
		if candidate.ID == "native-responses" && (!candidate.SupportsPath || candidate.FilterReason != "") {
			t.Fatalf("native responses candidate = %+v, want selectable for /v1/responses", candidate)
		}
		if candidate.ID == "chat" && !candidate.SupportsPath {
			t.Fatalf("chat completions candidate = %+v, want compatible fallback for /v1/responses", candidate)
		}
	}
}

func TestSupportsLocalResponsesServerBackendExcludesNativeResponsesOnlyTarget(t *testing.T) {
	responsesEnabled := true
	chatDisabled := false
	native, err := upstream.Resolve(config.UpstreamConfig{
		BaseURL:        "https://api.openai.com/v1",
		ProviderPreset: "openai",
		APIType:        "responses_native",
		Capabilities: config.UpstreamCapabilitiesConfig{
			Responses:       &responsesEnabled,
			ChatCompletions: &chatDisabled,
		},
	})
	if err != nil {
		t.Fatalf("Resolve(native) error = %v", err)
	}
	if !native.SupportsEndpoint("/v1/responses") {
		t.Fatal("native responses target should support /v1/responses pass-through")
	}
	if SupportsLocalResponsesServerBackend(native) {
		t.Fatal("native responses-only target must not satisfy local Responses server backend")
	}

	chat, err := upstream.Resolve(config.UpstreamConfig{
		BaseURL:        "https://compat.example.com/v1",
		ProviderPreset: "openai",
		APIType:        "chat_completions",
	})
	if err != nil {
		t.Fatalf("Resolve(chat) error = %v", err)
	}
	if !SupportsLocalResponsesServerBackend(chat) {
		t.Fatal("chat completions target should satisfy local Responses server backend")
	}
}

func TestRouterSelectFiltersTargetsWithoutToolCallingCapability(t *testing.T) {
	toolCallingDisabled := false
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "no-tools",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://compat-no-tools.example.com/v1",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Capabilities: config.UpstreamCapabilitiesConfig{
						ToolCalling: &toolCallingDisabled,
					},
				},
			},
			{
				ID:             "tools",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://compat-tools.example.com/v1",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
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

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "tools" {
		t.Fatalf("selected target = %q, want tools", selection.Target.ID)
	}
	var sawNoTools bool
	for _, candidate := range selection.Decision.Candidates {
		if candidate.ID != "no-tools" {
			continue
		}
		sawNoTools = true
		if candidate.SupportsTools || candidate.Selectable || candidate.FilterReason != "unsupported_tools" {
			t.Fatalf("no-tools candidate = %+v, want unsupported_tools", candidate)
		}
	}
	if !sawNoTools {
		t.Fatalf("decision did not include no-tools candidate: %+v", selection.Decision)
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
	decision := SelectionDecision(err)
	if decision == nil {
		t.Fatalf("SelectionDecision() = nil, want decision trace")
	}
	if decision.ModelName != "claude-3-7-sonnet" {
		t.Fatalf("decision.ModelName = %q, want claude-3-7-sonnet", decision.ModelName)
	}
	if decision.FailureReason != SelectionFailureNoSupportingTarget {
		t.Fatalf("decision.FailureReason = %q, want %q", decision.FailureReason, SelectionFailureNoSupportingTarget)
	}
	if len(decision.Candidates) != 2 {
		t.Fatalf("len(decision.Candidates) = %d, want 2", len(decision.Candidates))
	}
	for _, candidate := range decision.Candidates {
		if candidate.Selectable {
			t.Fatalf("candidate %q selectable = true, want false", candidate.ID)
		}
		if candidate.FilterReason != "unsupported_model" {
			t.Fatalf("candidate %q FilterReason = %q, want unsupported_model", candidate.ID, candidate.FilterReason)
		}
	}
}

func TestRouterSelectionCarriesDecisionTrace(t *testing.T) {
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
	cfg.Router.Selection.Policy = PolicyFirstAvailable

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
	if selection.Decision == nil {
		t.Fatalf("selection.Decision = nil, want decision trace")
	}
	if selection.Decision.SelectedID != "primary" {
		t.Fatalf("decision.SelectedID = %q, want primary", selection.Decision.SelectedID)
	}
	if selection.Decision.AvailableCount != 2 {
		t.Fatalf("decision.AvailableCount = %d, want 2", selection.Decision.AvailableCount)
	}
	if len(selection.Decision.Candidates) != 2 {
		t.Fatalf("len(decision.Candidates) = %d, want 2", len(selection.Decision.Candidates))
	}
}

func TestRouterStickySessionReusesBoundTarget(t *testing.T) {
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
	cfg.Router.Selection.Policy = PolicyFirstAvailable

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req1 := stickyRequest(t, "same-session")
	selection, err := rtr.Select(req1)
	if err != nil {
		t.Fatalf("Select(first) error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("first target = %q, want primary", selection.Target.ID)
	}
	if selection.Decision.StickyStatus != "bind" {
		t.Fatalf("first sticky status = %q, want bind", selection.Decision.StickyStatus)
	}

	targets := rtr.Targets()
	targets[0].onFinish(RequestFeatures{ModelName: "gpt-5"}, Outcome{Success: true, StatusCode: 200, DurationMs: 5000, TTFTMs: 1800}, rtr.costs, 3, time.Minute)
	targets[1].onFinish(RequestFeatures{ModelName: "gpt-5"}, Outcome{Success: true, StatusCode: 200, DurationMs: 400, TTFTMs: 80}, rtr.costs, 3, time.Minute)
	req2 := stickyRequest(t, "same-session")
	selection, err = rtr.Select(req2)
	if err != nil {
		t.Fatalf("Select(second) error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("second target = %q, want sticky primary", selection.Target.ID)
	}
	if selection.Decision.StickyStatus != "hit" {
		t.Fatalf("second sticky status = %q, want hit", selection.Decision.StickyStatus)
	}
}

func TestRouterStickySessionBreaksWhenTargetExcluded(t *testing.T) {
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
	cfg.Router.Selection.Policy = PolicyFirstAvailable

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	body := []byte(`{"model":"gpt-5","input":"hello"}`)
	req := stickyRequest(t, "session-break")
	selection, err := rtr.SelectWithBody(req, body)
	if err != nil {
		t.Fatalf("Select(first) error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("first target = %q, want primary", selection.Target.ID)
	}

	selection, err = rtr.SelectWithExclusion(stickyRequest(t, "session-break"), body, []string{"primary"})
	if err != nil {
		t.Fatalf("SelectWithExclusion() error = %v", err)
	}
	if selection.Target.ID != "fallback" {
		t.Fatalf("excluded sticky target selection = %q, want fallback", selection.Target.ID)
	}
	if !hasStickyDecision(selection.Decision, "break", "primary") {
		t.Fatalf("sticky events = %+v, want break for primary", selection.Decision.StickyEvents)
	}
	if selection.Decision.StickyBreakID != "primary" {
		t.Fatalf("sticky break id = %q, want primary", selection.Decision.StickyBreakID)
	}

	selection, err = rtr.Select(stickyRequest(t, "session-break"))
	if err != nil {
		t.Fatalf("Select(after rebind) error = %v", err)
	}
	if selection.Target.ID != "fallback" {
		t.Fatalf("after rebind target = %q, want fallback", selection.Target.ID)
	}
	if selection.Decision.StickyStatus != "hit" {
		t.Fatalf("after rebind sticky status = %q, want hit", selection.Decision.StickyStatus)
	}
}

func TestRouterSelectWithoutStickyKeyUnchanged(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream:       config.UpstreamConfig{BaseURL: "https://api.openai.com/v1"},
			},
			{
				ID:             "fallback",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5"},
				Upstream:       config.UpstreamConfig{BaseURL: "https://openrouter.ai/api/v1"},
			},
		},
	}
	cfg.Router.Selection.Policy = PolicyFirstAvailable

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
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target = %q, want primary", selection.Target.ID)
	}
	if selection.Decision.StickyStatus != "" {
		t.Fatalf("StickyStatus = %q, want empty", selection.Decision.StickyStatus)
	}
}

func TestRouterImplicitDefaultRouteTargetPreservesTargetID(t *testing.T) {
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
					ApiKey:         "sk-primary-secret",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.Policy = PolicyFirstAvailable

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	targets := rtr.Targets()
	if len(targets) != 1 {
		t.Fatalf("len(targets) = %d, want 1", len(targets))
	}
	target := targets[0]
	if target.ID != "primary" || target.RouteTargetID != "primary:default" || target.ChannelID != "primary" || target.CredentialID != "default" {
		t.Fatalf("target identity = %+v, want old id with implicit default credential", target.snapshot())
	}

	selection, err := rtr.Select(stickyRequest(t, "implicit-default"))
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target = %q, want primary", selection.Target.ID)
	}
	if selection.Decision.SelectedID != "primary" || selection.Decision.SelectedRouteTargetID != "primary:default" || selection.Decision.SelectedChannelID != "primary" || selection.Decision.SelectedCredentialID != "default" {
		t.Fatalf("decision selection = %+v, want implicit default identity", selection.Decision)
	}
	if len(selection.Decision.Candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(selection.Decision.Candidates))
	}
	candidate := selection.Decision.Candidates[0]
	if candidate.ID != "primary" || candidate.RouteTargetID != "primary:default" || candidate.ChannelID != "primary" || candidate.CredentialID != "default" {
		t.Fatalf("candidate = %+v, want implicit default identity", candidate)
	}
}

func TestRouterExpandsExplicitCredentialsIntoRouteTargets(t *testing.T) {
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
					ApiKey:         "sk-inline-ignored",
					ProviderPreset: "openai",
				},
				Credentials: []config.CredentialConfig{
					{ID: "personal", Name: "personal key", ApiKey: "sk-personal-secret"},
					{ID: "backup", Name: "backup key", ApiKey: "sk-backup-secret"},
				},
			},
		},
	}
	cfg.Router.Selection.Policy = PolicyFirstAvailable

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	targets := rtr.Targets()
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}
	gotIDs := []string{targets[0].ID, targets[1].ID}
	wantIDs := []string{"openai-primary:backup", "openai-primary:personal"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("target IDs = %#v, want %#v", gotIDs, wantIDs)
	}
	for _, target := range targets {
		if target.ChannelID != "openai-primary" {
			t.Fatalf("target %q channel = %q, want openai-primary", target.ID, target.ChannelID)
		}
		if target.RouteTargetID != target.ID {
			t.Fatalf("target %q RouteTargetID = %q, want same as ID", target.ID, target.RouteTargetID)
		}
	}

	selection, err := rtr.Select(stickyRequest(t, "explicit-credentials"))
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "openai-primary:backup" {
		t.Fatalf("selected target = %q, want deterministic first route target", selection.Target.ID)
	}
	if selection.Decision.SelectedRouteTargetID != "openai-primary:backup" || selection.Decision.SelectedChannelID != "openai-primary" || selection.Decision.SelectedCredentialID != "backup" {
		t.Fatalf("decision selection = %+v, want backup route target fields", selection.Decision)
	}
	if len(selection.Decision.Candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(selection.Decision.Candidates))
	}
	for _, candidate := range selection.Decision.Candidates {
		if candidate.ChannelID != "openai-primary" || candidate.RouteTargetID == "" || candidate.CredentialID == "" {
			t.Fatalf("candidate missing credential route fields: %+v", candidate)
		}
	}
}

func TestRouterStickyBindsConcreteCredentialRouteTarget(t *testing.T) {
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
				Credentials: []config.CredentialConfig{
					{ID: "a", ApiKey: "sk-a-secret"},
					{ID: "b", ApiKey: "sk-b-secret"},
				},
			},
		},
	}
	cfg.Router.Selection.Policy = PolicyFirstAvailable

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	body := []byte(`{"model":"gpt-5","input":"hello"}`)
	first, err := rtr.SelectWithBody(stickyRequest(t, "credential-sticky"), body)
	if err != nil {
		t.Fatalf("Select(first) error = %v", err)
	}
	if first.Target.ID != "openai-primary:a" || first.Decision.StickyStatus != "bind" {
		t.Fatalf("first selection target/status = %q/%q, want openai-primary:a bind", first.Target.ID, first.Decision.StickyStatus)
	}
	if first.Decision.StickyTargetID != "openai-primary:a" || first.Decision.SelectedRouteTargetID != "openai-primary:a" {
		t.Fatalf("first decision = %+v, want concrete route target binding", first.Decision)
	}

	second, err := rtr.SelectWithBody(stickyRequest(t, "credential-sticky"), body)
	if err != nil {
		t.Fatalf("Select(second) error = %v", err)
	}
	if second.Target.ID != "openai-primary:a" || second.Decision.StickyStatus != "hit" {
		t.Fatalf("second selection target/status = %q/%q, want openai-primary:a hit", second.Target.ID, second.Decision.StickyStatus)
	}
	if !hasStickyRouteDecision(second.Decision, "hit", "openai-primary:a", "") {
		t.Fatalf("sticky events = %+v, want hit for concrete route target", second.Decision.StickyEvents)
	}

	rebound, err := rtr.SelectWithExclusion(stickyRequest(t, "credential-sticky"), body, []string{"openai-primary:a"})
	if err != nil {
		t.Fatalf("SelectWithExclusion() error = %v", err)
	}
	if rebound.Target.ID != "openai-primary:b" {
		t.Fatalf("rebound target = %q, want openai-primary:b", rebound.Target.ID)
	}
	if !hasStickyRouteDecision(rebound.Decision, "break", "openai-primary:a", "openai-primary:a") {
		t.Fatalf("sticky events = %+v, want break for openai-primary:a", rebound.Decision.StickyEvents)
	}
	if !hasStickyRouteDecision(rebound.Decision, "bind", "openai-primary:b", "openai-primary:a") {
		t.Fatalf("sticky events = %+v, want rebind to openai-primary:b", rebound.Decision.StickyEvents)
	}

	third, err := rtr.SelectWithBody(stickyRequest(t, "credential-sticky"), body)
	if err != nil {
		t.Fatalf("Select(third) error = %v", err)
	}
	if third.Target.ID != "openai-primary:b" || third.Decision.StickyStatus != "hit" {
		t.Fatalf("third selection target/status = %q/%q, want openai-primary:b hit", third.Target.ID, third.Decision.StickyStatus)
	}
}

func stickyRequest(t *testing.T, sessionID string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Session_id", sessionID)
	return req
}

func hasStickyDecision(decision *DecisionTrace, status string, breakID string) bool {
	if decision == nil {
		return false
	}
	for _, event := range decision.StickyEvents {
		if event.Status == status && event.BreakID == breakID {
			return true
		}
	}
	return false
}

func hasStickyRouteDecision(decision *DecisionTrace, status string, routeTargetID string, breakID string) bool {
	if decision == nil {
		return false
	}
	for _, event := range decision.StickyEvents {
		if event.Status == status && event.RouteTargetID == routeTargetID && event.BreakID == breakID {
			return true
		}
	}
	return false
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

func TestRouterDoesNotRouteAnthropicMessagesToOpenAICompatibleTarget(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1", "deepseek-chat"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "anthropic-primary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1"},
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

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/messages?beta=true", strings.NewReader(`{"model":"glm-5.1","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "anthropic-primary" {
		t.Fatalf("selected target = %q, want anthropic-primary", selection.Target.ID)
	}
}

func TestRouterExtractsModelAndRoutesAnthropicCountTokens(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "anthropic-primary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"glm-5.1"},
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

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/messages/count_tokens?beta=true", strings.NewReader(`{"model":"glm-5.1","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "anthropic-primary" {
		t.Fatalf("selected target = %q, want anthropic-primary", selection.Target.ID)
	}
	if selection.Decision == nil || selection.Decision.ModelName != "glm-5.1" {
		t.Fatalf("decision model = %#v, want glm-5.1", selection.Decision)
	}
}

func TestRouterExtractsModelAndRoutesOpenAITokenize(t *testing.T) {
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
				ID:             "vllm-primary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"qwen3.6-35b-a3b"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "http://vllm.local:8000/v1",
					ProviderPreset: "vllm",
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

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/tokenize", strings.NewReader(`{"model":"qwen3.6-35b-a3b","prompt":"hello","add_special_tokens":false}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Target.ID != "vllm-primary" {
		t.Fatalf("selected target = %q, want vllm-primary", selection.Target.ID)
	}
	if selection.Decision == nil || selection.Decision.ModelName != "qwen3.6-35b-a3b" {
		t.Fatalf("decision model = %#v, want qwen3.6-35b-a3b", selection.Decision)
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
	cfg.Router.Selection.Policy = PolicyFirstAvailable

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

func TestRouterClientCancelDoesNotOpenTarget(t *testing.T) {
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
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Minute

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
	rtr.Complete(selection, Outcome{Success: false, ClientCanceled: true, StatusCode: http.StatusBadGateway, DurationMs: 100, Stream: true})

	snapshots := rtr.Snapshots()
	if snapshots[0].HealthState == HealthOpen {
		t.Fatalf("HealthState = %q, want non-open after client cancellation", snapshots[0].HealthState)
	}

	req2, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello again"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	if _, err := rtr.Select(req2); err != nil {
		t.Fatalf("Select() after client cancellation error = %v", err)
	}
}

func TestRouterClientRequestErrorDoesNotOpenTarget(t *testing.T) {
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
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Minute

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
	rtr.Complete(selection, Outcome{Success: false, StatusCode: http.StatusBadRequest, DurationMs: 100, TTFTMs: 30})

	snapshots := rtr.Snapshots()
	if snapshots[0].HealthState == HealthOpen {
		t.Fatalf("HealthState = %q, want non-open after 400 response", snapshots[0].HealthState)
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
		flaky.onFinish(reqFeatures, Outcome{Success: false, StatusCode: 0, DurationMs: 1200, TTFTMs: 0}, rtr.costs, rtr.failureThreshold, rtr.openWindow)
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

func TestRouterSelectWithExclusion(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.primary.example/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "secondary",
				Enabled:        boolPtr(true),
				Priority:       90,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.secondary.example/v1",
					ProviderPreset: "openai",
				},
			},
			{
				ID:             "tertiary",
				Enabled:        boolPtr(true),
				Priority:       80,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.tertiary.example/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	// Use first_available so priority ordering is deterministic.
	cfg.Router.Selection.Policy = PolicyFirstAvailable

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	body := []byte(`{"model":"gpt-5.5","input":"hello"}`)

	// First selection: should be highest-priority target.
	req1, _ := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil)
	req1.Header.Set("Content-Type", "application/json")
	sel1, err := rtr.SelectWithBody(req1, body)
	if err != nil {
		t.Fatalf("first SelectWithBody() error = %v", err)
	}
	if sel1.Target.ID != "primary" {
		t.Fatalf("first selection = %q, want primary", sel1.Target.ID)
	}
	rtr.Complete(sel1, Outcome{Success: false, StatusCode: 404}) // simulate model-not-found

	// Second selection: exclude primary, should get secondary.
	req2, _ := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil)
	req2.Header.Set("Content-Type", "application/json")
	sel2, err := rtr.SelectWithExclusion(req2, body, []string{"primary"})
	if err != nil {
		t.Fatalf("SelectWithExclusion(exclude primary) error = %v", err)
	}
	if sel2.Target.ID != "secondary" {
		t.Fatalf("second selection after exclusion = %q, want secondary", sel2.Target.ID)
	}

	// Third selection: exclude primary + secondary, should get tertiary.
	req3, _ := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil)
	req3.Header.Set("Content-Type", "application/json")
	sel3, err := rtr.SelectWithExclusion(req3, body, []string{"primary", "secondary"})
	if err != nil {
		t.Fatalf("SelectWithExclusion(exclude primary+secondary) error = %v", err)
	}
	if sel3.Target.ID != "tertiary" {
		t.Fatalf("third selection after exclusion = %q, want tertiary", sel3.Target.ID)
	}

	// Exclude all targets: should fail.
	req4, _ := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil)
	req4.Header.Set("Content-Type", "application/json")
	_, err = rtr.SelectWithExclusion(req4, body, []string{"primary", "secondary", "tertiary"})
	if err == nil {
		t.Fatalf("SelectWithExclusion(all excluded) error = nil, want error")
	}

	// Empty exclusion list should behave like SelectWithBody.
	req5, _ := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil)
	req5.Header.Set("Content-Type", "application/json")
	sel5, err := rtr.SelectWithExclusion(req5, body, nil)
	if err != nil {
		t.Fatalf("SelectWithExclusion(nil) error = %v", err)
	}
	if sel5.Target.ID != "primary" {
		t.Fatalf("SelectWithExclusion(nil) = %q, want primary (same as SelectWithBody)", sel5.Target.ID)
	}
}

func TestRouterRefreshNowRebuildsCatalog(t *testing.T) {
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
		},
	}

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	target := rtr.Targets()[0]
	target.StaticModels = []string{"gpt-5.5"}

	if _, err := rtr.RefreshNow(); err != nil {
		t.Fatalf("RefreshNow() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() after RefreshNow error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target = %q, want primary", selection.Target.ID)
	}
}

func TestRouterRefreshNowRecoversOpenTargetToProbation(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Hour

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	selection, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	rtr.Complete(selection, Outcome{Success: false, StatusCode: 0})
	if got := rtr.Snapshots()[0].HealthState; got != HealthOpen {
		t.Fatalf("HealthState after failure = %q, want %q", got, HealthOpen)
	}

	if _, err := rtr.RefreshNow(); err != nil {
		t.Fatalf("RefreshNow() error = %v", err)
	}
	if got := rtr.Snapshots()[0].HealthState; got != HealthProbation {
		t.Fatalf("HealthState after successful refresh = %q, want %q", got, HealthProbation)
	}
	if _, err := rtr.Select(req); err != nil {
		t.Fatalf("Select() after successful refresh error = %v", err)
	}
}

func TestRouterProbationTargetAllowsOnlyOneProbe(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Hour

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	first, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	rtr.Complete(first, Outcome{Success: false, StatusCode: 0})
	if _, err := rtr.RefreshNow(); err != nil {
		t.Fatalf("RefreshNow() error = %v", err)
	}

	probe, err := rtr.Select(req)
	if err != nil {
		t.Fatalf("Select() probation probe error = %v", err)
	}

	_, err = rtr.Select(req)
	if err == nil {
		t.Fatalf("Select() concurrent probation request error = nil, want all targets open")
	}
	if SelectionFailureReason(err) != SelectionFailureAllTargetsOpen {
		t.Fatalf("SelectionFailureReason() = %q, want %q", SelectionFailureReason(err), SelectionFailureAllTargetsOpen)
	}

	rtr.Complete(probe, Outcome{Success: true, StatusCode: http.StatusOK})
	if _, err := rtr.Select(req); err != nil {
		t.Fatalf("Select() after successful probation probe error = %v", err)
	}
}

func TestRouterModelScopedFailureDoesNotOpenWholeTarget(t *testing.T) {
	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5.5", "gpt-5.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	cfg.Router.Selection.FailureThreshold = 1
	cfg.Router.Selection.OpenWindow = time.Hour

	rtr, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	failingReq, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	failingReq.Header.Set("Content-Type", "application/json")
	selection, err := rtr.Select(failingReq)
	if err != nil {
		t.Fatalf("Select(gpt-5.5) error = %v", err)
	}
	rtr.Complete(selection, Outcome{Success: false, StatusCode: http.StatusServiceUnavailable})

	_, err = rtr.Select(failingReq)
	if err == nil {
		t.Fatalf("Select(gpt-5.5) after model failure error = nil, want unavailable")
	}
	if SelectionFailureReason(err) != SelectionFailureAllTargetsOpen {
		t.Fatalf("SelectionFailureReason(gpt-5.5) = %q, want %q", SelectionFailureReason(err), SelectionFailureAllTargetsOpen)
	}

	healthyReq, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5.1","input":"hello"}`))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	healthyReq.Header.Set("Content-Type", "application/json")
	selection, err = rtr.Select(healthyReq)
	if err != nil {
		t.Fatalf("Select(gpt-5.1) after gpt-5.5 model failure error = %v", err)
	}
	if selection.Target.ID != "primary" {
		t.Fatalf("selected target = %q, want primary", selection.Target.ID)
	}
}

func TestTargetRefreshFailureCanOpenHealthState(t *testing.T) {
	target := &Target{
		ID:          "primary",
		models:      map[string]struct{}{},
		healthState: HealthHealthy,
	}
	costs := defaultCostConfig()

	target.setRefreshResult(nil, "error", errors.New("temporary discovery failure"), 2, time.Minute, costs)
	if got := target.snapshot().HealthState; got != HealthDegraded {
		t.Fatalf("HealthState after first refresh failure = %q, want %q", got, HealthDegraded)
	}

	target.setRefreshResult(nil, "error", errors.New("temporary discovery failure"), 2, time.Minute, costs)
	snapshot := target.snapshot()
	if snapshot.HealthState != HealthOpen {
		t.Fatalf("HealthState after second refresh failure = %q, want %q", snapshot.HealthState, HealthOpen)
	}
	if snapshot.OpenUntil.IsZero() {
		t.Fatalf("OpenUntil is zero after refresh failures opened target")
	}
}
