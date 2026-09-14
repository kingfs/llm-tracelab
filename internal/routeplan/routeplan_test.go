package routeplan

import (
	"errors"
	"testing"
)

func TestPlanChatCompletionsProxyPass(t *testing.T) {
	result, err := Plan(Request{
		Entrypoint:              EntrypointChatCompletions,
		RequestedModel:          "abc",
		ResolvedModelCandidates: []ResolvedModelCandidate{{Model: "gpt-5.5", Alias: "abc"}},
	}, []UpstreamCandidate{
		{ID: "native-responses", ChannelID: "native", Enabled: true, Models: []string{"gpt-5.5"}, SupportsResponses: true},
		{ID: "chat", ChannelID: "chat", Enabled: true, Models: []string{"gpt-5.5"}, SupportsChatCompletions: true, SupportsToolCalling: true},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Plan.ExecutionMode != ExecutionModeProxyPass || result.Plan.UpstreamEndpoint != UpstreamEndpointChatCompletions {
		t.Fatalf("plan = %#v, want chat proxy pass", result.Plan)
	}
	if result.Plan.SelectedCandidateID != "chat" || result.Plan.UpstreamModel != "gpt-5.5" {
		t.Fatalf("selected = %q model %q, want chat gpt-5.5", result.Plan.SelectedCandidateID, result.Plan.UpstreamModel)
	}
}

func TestPlanAnthropicMessagesProxyPass(t *testing.T) {
	result, err := Plan(Request{Entrypoint: EntrypointAnthropicMessage, RequestedModel: "claude-sonnet"}, []UpstreamCandidate{
		{ID: "anthropic", ChannelID: "anthropic", Enabled: true, Models: []string{"claude-sonnet"}, SupportsAnthropicMessages: true},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Plan.ExecutionMode != ExecutionModeProxyPass || result.Plan.UpstreamEndpoint != UpstreamEndpointAnthropicMessage {
		t.Fatalf("plan = %#v, want anthropic proxy pass", result.Plan)
	}
}

func TestPlanResponsesStrategies(t *testing.T) {
	upstreams := []UpstreamCandidate{
		{ID: "chat", ChannelID: "chat", Enabled: true, Models: []string{"gpt-5.5"}, SupportsChatCompletions: true, SupportsToolCalling: true},
		{ID: "native", ChannelID: "native", Enabled: true, Models: []string{"gpt-5.5"}, SupportsResponses: true, SupportsToolCalling: true},
	}
	tests := []struct {
		name     string
		strategy ResponsesStrategy
		wantMode ExecutionMode
		wantEP   UpstreamEndpoint
		wantID   string
	}{
		{name: "auto prefers native", strategy: ResponsesStrategyAuto, wantMode: ExecutionModeProxyPass, wantEP: UpstreamEndpointResponses, wantID: "native"},
		{name: "prefer native", strategy: ResponsesStrategyPreferNative, wantMode: ExecutionModeProxyPass, wantEP: UpstreamEndpointResponses, wantID: "native"},
		{name: "prefer local", strategy: ResponsesStrategyPreferLocalServer, wantMode: ExecutionModeResponsesServer, wantEP: UpstreamEndpointChatCompletions, wantID: "chat"},
		{name: "native only", strategy: ResponsesStrategyNativeOnly, wantMode: ExecutionModeProxyPass, wantEP: UpstreamEndpointResponses, wantID: "native"},
		{name: "local only", strategy: ResponsesStrategyLocalServerOnly, wantMode: ExecutionModeResponsesServer, wantEP: UpstreamEndpointChatCompletions, wantID: "chat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Plan(Request{Entrypoint: EntrypointResponses, RequestedModel: "gpt-5.5", ResponsesStrategy: tt.strategy}, upstreams)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			if result.Plan.ExecutionMode != tt.wantMode || result.Plan.UpstreamEndpoint != tt.wantEP || result.Plan.SelectedCandidateID != tt.wantID {
				t.Fatalf("plan = %#v, want mode %s endpoint %s id %s", result.Plan, tt.wantMode, tt.wantEP, tt.wantID)
			}
		})
	}
}

func TestPlanResponsesAutoFallsBackToChat(t *testing.T) {
	result, err := Plan(Request{Entrypoint: EntrypointResponses, RequestedModel: "deepseek"}, []UpstreamCandidate{
		{ID: "chat", ChannelID: "deepseek", Enabled: true, Models: []string{"deepseek"}, SupportsChatCompletions: true, SupportsToolCalling: true},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Plan.ExecutionMode != ExecutionModeResponsesServer || result.Plan.UpstreamEndpoint != UpstreamEndpointChatCompletions {
		t.Fatalf("plan = %#v, want responses server chat fallback", result.Plan)
	}
}

func TestPlanResponsesStrategyDisallowsFallback(t *testing.T) {
	_, err := Plan(Request{Entrypoint: EntrypointResponses, RequestedModel: "deepseek", ResponsesStrategy: ResponsesStrategyNativeOnly}, []UpstreamCandidate{
		{ID: "chat", ChannelID: "deepseek", Enabled: true, Models: []string{"deepseek"}, SupportsChatCompletions: true},
	})
	assertNoRouteReason(t, err, ReasonNoCandidate)
}

func TestPlanResponsesRequiresLocalRuntime(t *testing.T) {
	result, err := Plan(Request{Entrypoint: EntrypointResponses, RequestedModel: "gpt-5.5", RequiresLocalResponsesRuntime: true}, []UpstreamCandidate{
		{ID: "native", ChannelID: "native", Enabled: true, Models: []string{"gpt-5.5"}, SupportsResponses: true},
		{ID: "chat", ChannelID: "chat", Enabled: true, Models: []string{"gpt-5.5"}, SupportsChatCompletions: true},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Plan.SelectedCandidateID != "chat" || result.Plan.ExecutionMode != ExecutionModeResponsesServer {
		t.Fatalf("plan = %#v, want chat local runtime", result.Plan)
	}
	if got := result.Candidates[0].Reason; got != ReasonRequiresLocalResponsesRuntime {
		t.Fatalf("native reason = %q, want %q", got, ReasonRequiresLocalResponsesRuntime)
	}
}

func TestPlanChannelScopedAlias(t *testing.T) {
	result, err := Plan(Request{
		Entrypoint:              EntrypointChatCompletions,
		RequestedModel:          "abc",
		ResolvedModelCandidates: []ResolvedModelCandidate{{Model: "gpt-5.5", Alias: "abc", ChannelID: "allowed"}},
	}, []UpstreamCandidate{
		{ID: "blocked", ChannelID: "blocked", Enabled: true, Models: []string{"gpt-5.5"}, SupportsChatCompletions: true},
		{ID: "allowed", ChannelID: "allowed", Enabled: true, Models: []string{"gpt-5.5"}, SupportsChatCompletions: true},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Plan.SelectedChannelID != "allowed" {
		t.Fatalf("selected channel = %q, want allowed", result.Plan.SelectedChannelID)
	}
}

func TestPlanNoRouteDiagnostics(t *testing.T) {
	_, err := Plan(Request{Entrypoint: EntrypointChatCompletions, RequestedModel: "gpt-5.5", HasTools: true}, []UpstreamCandidate{
		{ID: "chat", ChannelID: "chat", Enabled: true, Models: []string{"gpt-5.5"}, SupportsChatCompletions: true, SupportsToolCalling: false},
	})
	assertNoRouteReason(t, err, ReasonNoCandidate)
	var noRoute *NoRouteError
	if !errors.As(err, &noRoute) || len(noRoute.Candidates) != 1 || noRoute.Candidates[0].Reason != "requires_tool_calling" {
		t.Fatalf("err = %#v, want tool calling candidate diagnostic", err)
	}
}

func assertNoRouteReason(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatal("Plan() error = nil, want no route")
	}
	var noRoute *NoRouteError
	if !errors.As(err, &noRoute) {
		t.Fatalf("error = %T %v, want NoRouteError", err, err)
	}
	if noRoute.Reason != reason {
		t.Fatalf("NoRouteError reason = %q, want %q", noRoute.Reason, reason)
	}
}

func TestPlanUsesPerModelCapabilities(t *testing.T) {
	yes := true
	no := false
	upstreams := []UpstreamCandidate{
		{
			ID: "mixed", ChannelID: "mixed", Enabled: true,
			Models:                  []string{"model-native", "model-chat"},
			SupportsResponses:       true,
			SupportsChatCompletions: false,
			ModelCapabilities: map[string]ModelCapabilities{
				"model-native": {SupportsResponses: &yes, SupportsChatCompletions: &no},
				"model-chat":   {SupportsResponses: &no, SupportsChatCompletions: &yes},
			},
		},
	}
	tests := []struct {
		name     string
		model    string
		wantMode ExecutionMode
		wantEP   UpstreamEndpoint
	}{
		{name: "native model", model: "model-native", wantMode: ExecutionModeProxyPass, wantEP: UpstreamEndpointResponses},
		{name: "chat-only model", model: "model-chat", wantMode: ExecutionModeResponsesServer, wantEP: UpstreamEndpointChatCompletions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Plan(Request{
				Entrypoint:              EntrypointResponses,
				RequestedModel:          tt.model,
				ResolvedModelCandidates: []ResolvedModelCandidate{{Model: tt.model}},
			}, upstreams)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			if result.Plan.ExecutionMode != tt.wantMode || result.Plan.UpstreamEndpoint != tt.wantEP {
				t.Fatalf("plan = %#v, want mode %s endpoint %s", result.Plan, tt.wantMode, tt.wantEP)
			}
		})
	}

	// An unlisted model keeps the channel-level flags.
	result, err := Plan(Request{
		Entrypoint:              EntrypointResponses,
		RequestedModel:          "model-other",
		ResolvedModelCandidates: []ResolvedModelCandidate{{Model: "model-other"}},
	}, []UpstreamCandidate{
		{
			ID: "mixed", ChannelID: "mixed", Enabled: true,
			Models:                  []string{"model-other"},
			SupportsResponses:       true,
			SupportsChatCompletions: false,
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Plan.ExecutionMode != ExecutionModeProxyPass {
		t.Fatalf("plan = %#v, want channel-level native proxy pass", result.Plan)
	}
}
