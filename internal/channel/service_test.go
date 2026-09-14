package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
	responsesEnabled := false
	chatCompletionsEnabled := true
	toolCallingEnabled := true
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
					ApiKey:         "test-inline-key",
					ProviderPreset: "openai",
					APIType:        "chat_completions",
					Mode:           "responses_server",
					Capabilities: config.UpstreamCapabilitiesConfig{
						Responses:       &responsesEnabled,
						ChatCompletions: &chatCompletionsEnabled,
						ToolCalling:     &toolCallingEnabled,
					},
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
	record, err := st.GetChannelConfig("openai-primary")
	if err != nil {
		t.Fatalf("GetChannelConfig() error = %v", err)
	}
	if record.Source != "bootstrap" {
		t.Fatalf("record.Source = %q, want bootstrap", record.Source)
	}
	if record.APIType != "chat_completions" || record.Mode != "responses_server" {
		t.Fatalf("record api surface = %q/%q", record.APIType, record.Mode)
	}
	if !strings.Contains(record.CapabilitiesJSON, `"chat_completions":true`) || !strings.Contains(record.CapabilitiesJSON, `"responses":false`) {
		t.Fatalf("record.CapabilitiesJSON = %s", record.CapabilitiesJSON)
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
	if target.Upstream.ApiKey != "test-inline-key" {
		t.Fatalf("target.Upstream.ApiKey = %q", target.Upstream.ApiKey)
	}
	if got := target.Upstream.Headers["X-Test"]; got != "true" {
		t.Fatalf("target header X-Test = %q", got)
	}
	if target.Upstream.APIType != "chat_completions" || target.Upstream.Mode != "responses_server" {
		t.Fatalf("target api surface = %q/%q", target.Upstream.APIType, target.Upstream.Mode)
	}
	if target.Upstream.Capabilities.ChatCompletions == nil || !*target.Upstream.Capabilities.ChatCompletions {
		t.Fatalf("target.Upstream.Capabilities.ChatCompletions = %#v", target.Upstream.Capabilities.ChatCompletions)
	}
	if target.Upstream.Capabilities.Responses == nil || *target.Upstream.Capabilities.Responses {
		t.Fatalf("target.Upstream.Capabilities.Responses = %#v", target.Upstream.Capabilities.Responses)
	}
	if len(target.StaticModels) != 2 || target.StaticModels[0] != "gpt-4.1" || target.StaticModels[1] != "gpt-5" {
		t.Fatalf("target.StaticModels = %#v", target.StaticModels)
	}
}

func TestBootstrapFromConfigSkipsExplicitCredentialsWithoutStorageProjection(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID: "openai-primary",
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ApiKey:         "test-inline-key",
					ProviderPreset: "openai",
				},
				Credentials: []config.CredentialConfig{
					{
						ID:     "primary",
						ApiKey: "test-explicit-key",
					},
				},
			},
		},
	}

	imported, err := NewService(st).BootstrapFromConfig(cfg)
	if err != nil {
		t.Fatalf("BootstrapFromConfig() error = %v", err)
	}
	if imported != 0 {
		t.Fatalf("imported = %d, want 0", imported)
	}
	channels, err := st.ListChannelConfigs()
	if err != nil {
		t.Fatalf("ListChannelConfigs() error = %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("len(channels) = %d, want 0", len(channels))
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

	enable := true
	result, err := NewService(st).ProbeWithOptions("probe-channel", ProbeOptions{EnableDiscovered: &enable})
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

func TestProbeWithOptionsDetectsProviderSurface(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
		case "/v1/chat/completions":
			http.Error(w, "missing model", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
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

	result, err := NewService(st).ProbeWithOptions("probe-channel", ProbeOptions{DetectProvider: true})
	if err != nil {
		t.Fatalf("ProbeWithOptions() error = %v", err)
	}
	if result.ProviderReport.Status != "detected" {
		t.Fatalf("ProviderReport.Status = %q, want detected", result.ProviderReport.Status)
	}
	if result.ProviderReport.SuggestedAPIType != "chat_completions" || result.ProviderReport.SuggestedProtocolFamily != "openai_compatible" {
		t.Fatalf("provider suggestion = %q/%q", result.ProviderReport.SuggestedAPIType, result.ProviderReport.SuggestedProtocolFamily)
	}
	if !slices.Contains(result.ProviderReport.Capabilities, "chat_completions") || !slices.Contains(result.ProviderReport.Capabilities, "models") {
		t.Fatalf("ProviderReport.Capabilities = %#v", result.ProviderReport.Capabilities)
	}
	runs, err := st.ListChannelProbeRuns("probe-channel", 10)
	if err != nil {
		t.Fatalf("ListChannelProbeRuns() error = %v", err)
	}
	if len(runs) != 1 || !strings.Contains(runs[0].RequestMetaJSON, `"provider_probe"`) {
		t.Fatalf("probe run meta = %#v", runs)
	}
}

func TestProviderProbeReportDetectsChannelsReadOnly(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
		case "/v1/chat/completions":
			http.Error(w, "missing model", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
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
		APIType:          "responses",
		ProtocolFamily:   "openai_compatible",
		APIKeyCiphertext: []byte("sk-probe"),
		HeadersJSON:      "{}",
		Enabled:          true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}

	report, err := NewService(st).ProviderProbeReport(context.Background(), ProviderProbeReportOptions{})
	if err != nil {
		t.Fatalf("ProviderProbeReport() error = %v", err)
	}
	if len(report.Reports) != 1 {
		t.Fatalf("len(report.Reports) = %d, want 1", len(report.Reports))
	}
	got := report.Reports[0]
	if got.TargetSource != "channel" || got.ProviderID != "probe-channel" || got.Status != "detected" {
		t.Fatalf("report identity/status = %+v", got)
	}
	if got.SuggestedAPIType != "chat_completions" || got.SuggestedProtocolFamily != "openai_compatible" {
		t.Fatalf("suggestion = %q/%q", got.SuggestedAPIType, got.SuggestedProtocolFamily)
	}
	if !slices.Contains(got.Warnings, `specified api_type "responses" differs from probed suggestion "chat_completions"`) {
		t.Fatalf("Warnings = %#v, want api_type mismatch", got.Warnings)
	}
	runs, err := st.ListChannelProbeRuns("probe-channel", 10)
	if err != nil {
		t.Fatalf("ListChannelProbeRuns() error = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("ProviderProbeReport wrote probe runs: %#v", runs)
	}
	models, err := st.ListChannelModels("probe-channel", false)
	if err != nil {
		t.Fatalf("ListChannelModels() error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("ProviderProbeReport wrote channel models: %#v", models)
	}
}

func TestApplyProviderProbeReportFillsOnlyMissingNonSensitiveSuggestions(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
		case "/v1/chat/completions":
			http.Error(w, "missing model", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstreamServer.Close()

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	disabled := false
	capabilitiesJSON, err := json.Marshal(config.UpstreamCapabilitiesConfig{ChatCompletions: &disabled})
	if err != nil {
		t.Fatalf("json.Marshal(capabilities) error = %v", err)
	}
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:               "probe-channel",
		Name:             "Probe Channel",
		BaseURL:          upstreamServer.URL + "/v1",
		APIKeyCiphertext: []byte("sk-probe"),
		APIKeyHint:       "sk...robe",
		HeadersJSON:      `{"Authorization":"Bearer secret","X-Test":"visible"}`,
		CapabilitiesJSON: string(capabilitiesJSON),
		Enabled:          true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}

	result, err := NewService(st).ApplyProviderProbeReport(context.Background(), ProviderProbeReportOptions{})
	if err != nil {
		t.Fatalf("ApplyProviderProbeReport() error = %v", err)
	}
	if len(result.Applied) != 1 || !result.Applied[0].Applied {
		t.Fatalf("Applied = %#v", result.Applied)
	}
	if !slices.Contains(result.Applied[0].AppliedFields, "api_type") ||
		!slices.Contains(result.Applied[0].AppliedFields, "protocol_family") ||
		!slices.Contains(result.Applied[0].AppliedFields, "capabilities.models") {
		t.Fatalf("AppliedFields = %#v", result.Applied[0].AppliedFields)
	}
	if slices.Contains(result.Applied[0].AppliedFields, "capabilities.chat_completions") {
		t.Fatalf("explicit false capability was applied: %#v", result.Applied[0].AppliedFields)
	}
	record, err := st.GetChannelConfig("probe-channel")
	if err != nil {
		t.Fatalf("GetChannelConfig() error = %v", err)
	}
	if record.APIType != "chat_completions" || record.ProtocolFamily != "openai_compatible" {
		t.Fatalf("record suggestions = %q/%q", record.APIType, record.ProtocolFamily)
	}
	if string(record.APIKeyCiphertext) != "sk-probe" || !strings.Contains(record.HeadersJSON, "Bearer secret") {
		t.Fatalf("secret fields were not preserved: api_key=%q headers=%s", string(record.APIKeyCiphertext), record.HeadersJSON)
	}
	var capabilities config.UpstreamCapabilitiesConfig
	if err := json.Unmarshal([]byte(record.CapabilitiesJSON), &capabilities); err != nil {
		t.Fatalf("json.Unmarshal(capabilities) error = %v", err)
	}
	if capabilities.ChatCompletions == nil || *capabilities.ChatCompletions {
		t.Fatalf("explicit chat_completions capability was overwritten: %#v", capabilities.ChatCompletions)
	}
	if capabilities.Models == nil || !*capabilities.Models {
		t.Fatalf("models capability was not applied: %#v", capabilities.Models)
	}
	runs, err := st.ListChannelProbeRuns("probe-channel", 10)
	if err != nil {
		t.Fatalf("ListChannelProbeRuns() error = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("ApplyProviderProbeReport wrote probe runs: %#v", runs)
	}
}

func TestApplyProviderProbeReportSkipsNonDetectedAndSupportsChannelSelection(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
		case "/v1/chat/completions":
			http.Error(w, "missing model", http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstreamServer.Close()
	unknownServer := httptest.NewServer(http.NotFoundHandler())
	defer unknownServer.Close()

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	for _, record := range []store.ChannelConfigRecord{
		{ID: "selected-channel", Name: "Selected Channel", BaseURL: upstreamServer.URL + "/v1", HeadersJSON: "{}", Enabled: true},
		{ID: "other-channel", Name: "Other Channel", BaseURL: upstreamServer.URL + "/v1", HeadersJSON: "{}", Enabled: true},
		{ID: "unknown-channel", Name: "Unknown Channel", BaseURL: unknownServer.URL + "/v1", HeadersJSON: "{}", Enabled: true},
	} {
		if _, err := st.UpsertChannelConfig(record); err != nil {
			t.Fatalf("UpsertChannelConfig(%s) error = %v", record.ID, err)
		}
	}

	selected, err := NewService(st).ApplyProviderProbeReport(context.Background(), ProviderProbeReportOptions{ChannelID: "selected-channel"})
	if err != nil {
		t.Fatalf("selected ApplyProviderProbeReport() error = %v", err)
	}
	if len(selected.Applied) != 1 || selected.Applied[0].ChannelID != "selected-channel" || !selected.Applied[0].Applied {
		t.Fatalf("selected Applied = %#v", selected.Applied)
	}
	other, err := st.GetChannelConfig("other-channel")
	if err != nil {
		t.Fatalf("GetChannelConfig(other-channel) error = %v", err)
	}
	if other.APIType != "" || other.ProtocolFamily != "" {
		t.Fatalf("unselected channel was updated: %q/%q", other.APIType, other.ProtocolFamily)
	}

	unknown, err := NewService(st).ApplyProviderProbeReport(context.Background(), ProviderProbeReportOptions{ChannelID: "unknown-channel"})
	if err != nil {
		t.Fatalf("unknown ApplyProviderProbeReport() error = %v", err)
	}
	if len(unknown.Applied) != 1 || unknown.Applied[0].Applied || unknown.Applied[0].SkippedReason == "" {
		t.Fatalf("unknown Applied = %#v", unknown.Applied)
	}
	record, err := st.GetChannelConfig("unknown-channel")
	if err != nil {
		t.Fatalf("GetChannelConfig(unknown-channel) error = %v", err)
	}
	if record.APIType != "" || record.ProtocolFamily != "" || record.CapabilitiesJSON != "{}" {
		t.Fatalf("unknown channel was updated: %+v", record)
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
	if !strings.Contains(runs[0].RequestMetaJSON, `"failure_reason":"upstream_error"`) {
		t.Fatalf("RequestMetaJSON = %s, want upstream_error classification", runs[0].RequestMetaJSON)
	}
}

func TestProbeWithOptionsDiscoversDisabledModelsWithoutReplacingManual(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("request path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new"},{"id":"gpt-manual"}]}`))
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
	if _, err := st.UpsertChannelModel("probe-channel", store.ChannelModelRecord{
		Model:       "gpt-manual",
		DisplayName: "Manual Model",
		Source:      "manual",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("UpsertChannelModel(manual) error = %v", err)
	}

	enable := false
	result, err := NewService(st).ProbeWithOptions("probe-channel", ProbeOptions{EnableDiscovered: &enable})
	if err != nil {
		t.Fatalf("ProbeWithOptions() error = %v", err)
	}
	if result.EnabledCount != 1 {
		t.Fatalf("EnabledCount = %d, want 1", result.EnabledCount)
	}
	models, err := st.ListChannelModels("probe-channel", false)
	if err != nil {
		t.Fatalf("ListChannelModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2: %#v", len(models), models)
	}
	byModel := map[string]store.ChannelModelRecord{}
	for _, model := range models {
		byModel[model.Model] = model
	}
	if byModel["gpt-manual"].Source != "manual" || !byModel["gpt-manual"].Enabled || byModel["gpt-manual"].DisplayName != "Manual Model" {
		t.Fatalf("manual model was not preserved: %+v", byModel["gpt-manual"])
	}
	if byModel["gpt-new"].Source != "discovered" || byModel["gpt-new"].Enabled {
		t.Fatalf("new discovered model = %+v, want disabled discovered", byModel["gpt-new"])
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
	if !targets[0].ConfiguredModelsOnly {
		t.Fatalf("ConfiguredModelsOnly = false, want true for channel runtime target")
	}
}

func TestRuntimeTargetsProjectsEnabledModelAliases(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	for _, channelID := range []string{"primary", "secondary"} {
		if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
			ID:             channelID,
			Name:           channelID,
			BaseURL:        "https://" + channelID + ".example.com/v1",
			ProviderPreset: "openai",
			HeadersJSON:    "{}",
			Enabled:        true,
		}); err != nil {
			t.Fatalf("UpsertChannelConfig(%s) error = %v", channelID, err)
		}
		if err := st.ReplaceChannelModels(channelID, []store.ChannelModelRecord{
			{Model: "gpt-5.5", Source: "manual", Enabled: true},
		}); err != nil {
			t.Fatalf("ReplaceChannelModels(%s) error = %v", channelID, err)
		}
	}

	if _, err := st.UpsertModelAlias(store.ModelAliasRecord{Alias: "abc", TargetModel: "gpt-5.5", Enabled: true}); err != nil {
		t.Fatalf("UpsertModelAlias(global) error = %v", err)
	}
	if _, err := st.UpsertModelAlias(store.ModelAliasRecord{Alias: "disabled", TargetModel: "gpt-5.5", Enabled: false}); err != nil {
		t.Fatalf("UpsertModelAlias(disabled) error = %v", err)
	}

	targets, err := NewService(st).RuntimeTargets()
	if err != nil {
		t.Fatalf("RuntimeTargets() error = %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2", len(targets))
	}
	for _, target := range targets {
		if !slices.Equal(target.StaticModels, []string{"abc", "gpt-5.5"}) {
			t.Fatalf("target %s StaticModels = %#v", target.ID, target.StaticModels)
		}
	}
}

func TestRuntimeTargetsProjectsChannelScopedModelAliases(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	for _, channelID := range []string{"primary", "secondary"} {
		if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
			ID:             channelID,
			Name:           channelID,
			BaseURL:        "https://" + channelID + ".example.com/v1",
			ProviderPreset: "openai",
			HeadersJSON:    "{}",
			Enabled:        true,
		}); err != nil {
			t.Fatalf("UpsertChannelConfig(%s) error = %v", channelID, err)
		}
		if err := st.ReplaceChannelModels(channelID, []store.ChannelModelRecord{
			{Model: "gpt-5.5", Source: "manual", Enabled: true},
		}); err != nil {
			t.Fatalf("ReplaceChannelModels(%s) error = %v", channelID, err)
		}
	}

	if _, err := st.UpsertModelAlias(store.ModelAliasRecord{Alias: "coder", TargetModel: "gpt-5.5", ChannelID: "primary", Enabled: true}); err != nil {
		t.Fatalf("UpsertModelAlias(scoped) error = %v", err)
	}

	targets, err := NewService(st).RuntimeTargets()
	if err != nil {
		t.Fatalf("RuntimeTargets() error = %v", err)
	}
	modelsByTarget := map[string][]string{}
	for _, target := range targets {
		modelsByTarget[target.ID] = target.StaticModels
	}
	if !slices.Equal(modelsByTarget["primary"], []string{"coder", "gpt-5.5"}) {
		t.Fatalf("primary StaticModels = %#v", modelsByTarget["primary"])
	}
	if !slices.Equal(modelsByTarget["secondary"], []string{"gpt-5.5"}) {
		t.Fatalf("secondary StaticModels = %#v", modelsByTarget["secondary"])
	}
}

func TestProbeDefaultsToDisabledAndPreservesManualChoices(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"new"},{"id":"enabled"},{"id":"disabled"}]}`))
	}))
	defer upstreamServer.Close()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{ID: "p", Name: "P", Enabled: true, BaseURL: upstreamServer.URL + "/v1", ProviderPreset: "openai"}); err != nil {
		t.Fatal(err)
	}
	contextWindow := 128000
	for _, model := range []store.ChannelModelRecord{{Model: "enabled", Enabled: true, Source: "manual", ContextWindow: &contextWindow, ProfileAdoptionStatus: "adopted"}, {Model: "disabled", Enabled: false, Source: "discovered"}} {
		if _, err := st.UpsertChannelModel("p", model); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewService(st).Probe("p"); err != nil {
		t.Fatal(err)
	}
	models, err := st.ListChannelModels("p", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models=%v", models)
	}
	for _, model := range models {
		if model.Model == "enabled" && (model.ContextWindow == nil || *model.ContextWindow != contextWindow || model.ProfileAdoptionStatus != "adopted") {
			t.Fatalf("discovery erased model profile: %+v", model)
		}
		if model.Enabled != (model.Model == "enabled") {
			t.Fatalf("discovery changed authorization: %+v", model)
		}
	}
}
