package routeplan

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type ClientEntrypoint string

const (
	EntrypointChatCompletions  ClientEntrypoint = "chat_completions"
	EntrypointResponses        ClientEntrypoint = "responses"
	EntrypointAnthropicMessage ClientEntrypoint = "anthropic_messages"
)

type ExecutionMode string

const (
	ExecutionModeProxyPass       ExecutionMode = "proxy_pass"
	ExecutionModeResponsesServer ExecutionMode = "responses_server"
)

type UpstreamEndpoint string

const (
	UpstreamEndpointChatCompletions  UpstreamEndpoint = "chat_completions"
	UpstreamEndpointResponses        UpstreamEndpoint = "responses"
	UpstreamEndpointAnthropicMessage UpstreamEndpoint = "anthropic_messages"
)

type ResponsesStrategy string

const (
	ResponsesStrategyAuto              ResponsesStrategy = "auto"
	ResponsesStrategyPreferNative      ResponsesStrategy = "prefer_native"
	ResponsesStrategyPreferLocalServer ResponsesStrategy = "prefer_local_server"
	ResponsesStrategyNativeOnly        ResponsesStrategy = "native_only"
	ResponsesStrategyLocalServerOnly   ResponsesStrategy = "local_server_only"
)

const (
	ReasonSelected                         = "selected"
	ReasonUnsupportedEntrypoint            = "unsupported_entrypoint"
	ReasonUnsupportedStrategy              = "unsupported_responses_strategy"
	ReasonModelNotMatched                  = "model_not_matched"
	ReasonChannelNotEnabled                = "channel_not_enabled"
	ReasonRequiresChatCompletions          = "requires_chat_completions"
	ReasonRequiresResponses                = "requires_responses"
	ReasonRequiresAnthropicMessages        = "requires_anthropic_messages"
	ReasonRequiresLocalResponsesRuntime    = "requires_local_responses_runtime"
	ReasonStrategyDisallowsNativeResponses = "strategy_disallows_native_responses"
	ReasonStrategyDisallowsChatFallback    = "strategy_disallows_chat_fallback"
	ReasonNoCandidate                      = "no_route_candidate"
)

var ErrNoRoute = errors.New("no route plan")

type Request struct {
	Entrypoint                    ClientEntrypoint
	RequestedModel                string
	ResolvedModelCandidates       []ResolvedModelCandidate
	ResponsesStrategy             ResponsesStrategy
	RequiresLocalResponsesRuntime bool
	HasTools                      bool
	Stream                        bool
}

type ResolvedModelCandidate struct {
	Model     string
	Alias     string
	ChannelID string
	Source    string
}

type UpstreamCandidate struct {
	ID                        string
	RouteTargetID             string
	ChannelID                 string
	Enabled                   bool
	Priority                  int
	Weight                    float64
	Models                    []string
	SupportsChatCompletions   bool
	SupportsResponses         bool
	SupportsAnthropicMessages bool
	SupportsToolCalling       bool
}

type RoutePlan struct {
	ClientEntrypoint        ClientEntrypoint         `json:"client_entrypoint"`
	RequestedModel          string                   `json:"requested_model,omitempty"`
	ResolvedModelCandidates []ResolvedModelCandidate `json:"resolved_model_candidates,omitempty"`
	ExecutionMode           ExecutionMode            `json:"execution_mode"`
	UpstreamEndpoint        UpstreamEndpoint         `json:"upstream_endpoint"`
	SelectedCandidateID     string                   `json:"selected_candidate_id,omitempty"`
	SelectedRouteTargetID   string                   `json:"selected_route_target_id,omitempty"`
	SelectedChannelID       string                   `json:"selected_channel_id,omitempty"`
	UpstreamModel           string                   `json:"upstream_model,omitempty"`
	Strategy                ResponsesStrategy        `json:"strategy,omitempty"`
	Reason                  string                   `json:"reason,omitempty"`
	FallbacksConsidered     []PlanCandidate          `json:"fallbacks_considered,omitempty"`
}

type PlanCandidate struct {
	CandidateID      string           `json:"candidate_id,omitempty"`
	RouteTargetID    string           `json:"route_target_id,omitempty"`
	ChannelID        string           `json:"channel_id,omitempty"`
	ExecutionMode    ExecutionMode    `json:"execution_mode,omitempty"`
	UpstreamEndpoint UpstreamEndpoint `json:"upstream_endpoint,omitempty"`
	UpstreamModel    string           `json:"upstream_model,omitempty"`
	Selectable       bool             `json:"selectable"`
	Reason           string           `json:"reason,omitempty"`
	Rank             int              `json:"rank"`
}

type Result struct {
	Plan       RoutePlan       `json:"plan"`
	Candidates []PlanCandidate `json:"candidates"`
}

type NoRouteError struct {
	Reason     string
	Candidates []PlanCandidate
}

func (e *NoRouteError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason == "" {
		return ErrNoRoute.Error()
	}
	return fmt.Sprintf("%s: %s", ErrNoRoute, e.Reason)
}

func Plan(req Request, upstreams []UpstreamCandidate) (Result, error) {
	req = normalizeRequest(req)
	var candidates []PlanCandidate
	switch req.Entrypoint {
	case EntrypointChatCompletions:
		candidates = planForEndpoint(req, upstreams, ExecutionModeProxyPass, UpstreamEndpointChatCompletions, ReasonRequiresChatCompletions)
	case EntrypointAnthropicMessage:
		candidates = planForEndpoint(req, upstreams, ExecutionModeProxyPass, UpstreamEndpointAnthropicMessage, ReasonRequiresAnthropicMessages)
	case EntrypointResponses:
		var err error
		candidates, err = planResponses(req, upstreams)
		if err != nil {
			return Result{Candidates: candidates}, err
		}
	default:
		return Result{}, &NoRouteError{Reason: ReasonUnsupportedEntrypoint}
	}

	selected, ok := firstSelectable(candidates)
	if !ok {
		return Result{Candidates: candidates}, &NoRouteError{Reason: ReasonNoCandidate, Candidates: candidates}
	}
	return Result{Plan: routePlanFromCandidate(req, selected, candidates), Candidates: candidates}, nil
}

func normalizeRequest(req Request) Request {
	req.RequestedModel = strings.TrimSpace(req.RequestedModel)
	if req.ResponsesStrategy == "" {
		req.ResponsesStrategy = ResponsesStrategyAuto
	}
	if len(req.ResolvedModelCandidates) == 0 && req.RequestedModel != "" {
		req.ResolvedModelCandidates = []ResolvedModelCandidate{{Model: req.RequestedModel, Source: "request"}}
	}
	seen := map[string]struct{}{}
	out := make([]ResolvedModelCandidate, 0, len(req.ResolvedModelCandidates))
	for _, candidate := range req.ResolvedModelCandidates {
		candidate.Model = strings.TrimSpace(candidate.Model)
		candidate.Alias = strings.TrimSpace(candidate.Alias)
		candidate.ChannelID = strings.TrimSpace(candidate.ChannelID)
		if candidate.Model == "" {
			continue
		}
		key := strings.ToLower(candidate.ChannelID + "\x00" + candidate.Model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	req.ResolvedModelCandidates = out
	return req
}

func planForEndpoint(req Request, upstreams []UpstreamCandidate, mode ExecutionMode, endpoint UpstreamEndpoint, capabilityReason string) []PlanCandidate {
	plans := make([]PlanCandidate, 0, len(upstreams))
	for _, upstream := range sortUpstreams(upstreams) {
		plan := basePlanCandidate(upstream, mode, endpoint, 0)
		if !upstream.Enabled {
			plan.Reason = ReasonChannelNotEnabled
		} else if !modelMatches(req, upstream, &plan) {
			plan.Reason = ReasonModelNotMatched
		} else if !supportsEndpoint(upstream, endpoint) {
			plan.Reason = capabilityReason
		} else if req.HasTools && !upstream.SupportsToolCalling {
			plan.Reason = "requires_tool_calling"
		} else {
			plan.Selectable = true
			plan.Reason = ReasonSelected
		}
		plans = append(plans, plan)
	}
	return plans
}

func planResponses(req Request, upstreams []UpstreamCandidate) ([]PlanCandidate, error) {
	strategy := req.ResponsesStrategy
	if strategy == "" {
		strategy = ResponsesStrategyAuto
	}
	var plans []PlanCandidate
	switch strategy {
	case ResponsesStrategyAuto, ResponsesStrategyPreferNative:
		plans = append(plans, responsesPlans(req, upstreams, ExecutionModeProxyPass, UpstreamEndpointResponses, 0)...)
		plans = append(plans, responsesPlans(req, upstreams, ExecutionModeResponsesServer, UpstreamEndpointChatCompletions, 1)...)
	case ResponsesStrategyPreferLocalServer:
		plans = append(plans, responsesPlans(req, upstreams, ExecutionModeResponsesServer, UpstreamEndpointChatCompletions, 0)...)
		plans = append(plans, responsesPlans(req, upstreams, ExecutionModeProxyPass, UpstreamEndpointResponses, 1)...)
	case ResponsesStrategyNativeOnly:
		plans = append(plans, responsesPlans(req, upstreams, ExecutionModeProxyPass, UpstreamEndpointResponses, 0)...)
		plans = markDisallowed(plans, req, upstreams, ExecutionModeResponsesServer, UpstreamEndpointChatCompletions, ReasonStrategyDisallowsChatFallback)
	case ResponsesStrategyLocalServerOnly:
		plans = append(plans, responsesPlans(req, upstreams, ExecutionModeResponsesServer, UpstreamEndpointChatCompletions, 0)...)
		plans = markDisallowed(plans, req, upstreams, ExecutionModeProxyPass, UpstreamEndpointResponses, ReasonStrategyDisallowsNativeResponses)
	default:
		return plans, &NoRouteError{Reason: ReasonUnsupportedStrategy}
	}
	return plans, nil
}

func responsesPlans(req Request, upstreams []UpstreamCandidate, mode ExecutionMode, endpoint UpstreamEndpoint, rank int) []PlanCandidate {
	plans := make([]PlanCandidate, 0, len(upstreams))
	for _, upstream := range sortUpstreams(upstreams) {
		plan := basePlanCandidate(upstream, mode, endpoint, rank)
		if !upstream.Enabled {
			plan.Reason = ReasonChannelNotEnabled
		} else if req.RequiresLocalResponsesRuntime && mode != ExecutionModeResponsesServer {
			plan.Reason = ReasonRequiresLocalResponsesRuntime
		} else if !modelMatches(req, upstream, &plan) {
			plan.Reason = ReasonModelNotMatched
		} else if !supportsEndpoint(upstream, endpoint) {
			if endpoint == UpstreamEndpointResponses {
				plan.Reason = ReasonRequiresResponses
			} else {
				plan.Reason = ReasonRequiresChatCompletions
			}
		} else if req.HasTools && !upstream.SupportsToolCalling {
			plan.Reason = "requires_tool_calling"
		} else {
			plan.Selectable = true
			plan.Reason = ReasonSelected
		}
		plans = append(plans, plan)
	}
	return plans
}

func markDisallowed(existing []PlanCandidate, req Request, upstreams []UpstreamCandidate, mode ExecutionMode, endpoint UpstreamEndpoint, reason string) []PlanCandidate {
	for _, upstream := range sortUpstreams(upstreams) {
		plan := basePlanCandidate(upstream, mode, endpoint, 99)
		_, _ = req, upstream
		plan.Reason = reason
		existing = append(existing, plan)
	}
	return existing
}

func basePlanCandidate(upstream UpstreamCandidate, mode ExecutionMode, endpoint UpstreamEndpoint, rank int) PlanCandidate {
	return PlanCandidate{
		CandidateID:      upstream.ID,
		RouteTargetID:    upstream.RouteTargetID,
		ChannelID:        upstream.ChannelID,
		ExecutionMode:    mode,
		UpstreamEndpoint: endpoint,
		Rank:             rank,
	}
}

func modelMatches(req Request, upstream UpstreamCandidate, plan *PlanCandidate) bool {
	if len(req.ResolvedModelCandidates) == 0 {
		return true
	}
	models := map[string]struct{}{}
	for _, model := range upstream.Models {
		model = strings.ToLower(strings.TrimSpace(model))
		if model != "" {
			models[model] = struct{}{}
		}
	}
	for _, candidate := range req.ResolvedModelCandidates {
		if candidate.ChannelID != "" && candidate.ChannelID != upstream.ChannelID {
			continue
		}
		model := strings.ToLower(strings.TrimSpace(candidate.Model))
		if model == "" {
			continue
		}
		if len(models) == 0 {
			if plan != nil {
				plan.UpstreamModel = candidate.Model
			}
			return true
		}
		if _, ok := models[model]; ok {
			if plan != nil {
				plan.UpstreamModel = candidate.Model
			}
			return true
		}
	}
	return false
}

func supportsEndpoint(upstream UpstreamCandidate, endpoint UpstreamEndpoint) bool {
	switch endpoint {
	case UpstreamEndpointChatCompletions:
		return upstream.SupportsChatCompletions
	case UpstreamEndpointResponses:
		return upstream.SupportsResponses
	case UpstreamEndpointAnthropicMessage:
		return upstream.SupportsAnthropicMessages
	default:
		return false
	}
}

func firstSelectable(candidates []PlanCandidate) (PlanCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.Selectable {
			return candidate, true
		}
	}
	return PlanCandidate{}, false
}

func routePlanFromCandidate(req Request, selected PlanCandidate, candidates []PlanCandidate) RoutePlan {
	return RoutePlan{
		ClientEntrypoint:        req.Entrypoint,
		RequestedModel:          req.RequestedModel,
		ResolvedModelCandidates: append([]ResolvedModelCandidate(nil), req.ResolvedModelCandidates...),
		ExecutionMode:           selected.ExecutionMode,
		UpstreamEndpoint:        selected.UpstreamEndpoint,
		SelectedCandidateID:     selected.CandidateID,
		SelectedRouteTargetID:   selected.RouteTargetID,
		SelectedChannelID:       selected.ChannelID,
		UpstreamModel:           selected.UpstreamModel,
		Strategy:                req.ResponsesStrategy,
		Reason:                  ReasonSelected,
		FallbacksConsidered:     append([]PlanCandidate(nil), candidates...),
	}
}

func sortUpstreams(upstreams []UpstreamCandidate) []UpstreamCandidate {
	out := append([]UpstreamCandidate(nil), upstreams...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}
