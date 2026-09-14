package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const (
	PolicyFirstAvailable = "first_available"
	PolicyP2C            = "p2c"

	ModelDiscoveryListModels = "list_models"
	ModelDiscoveryStaticOnly = "static_only"
	ModelDiscoveryDisabled   = "disabled"

	FallbackReject = "reject"
)

const (
	HealthHealthy   = "healthy"
	HealthDegraded  = "degraded"
	HealthOpen      = "open"
	HealthProbation = "probation"
)

const probationInflightLimit int64 = 1

type costConfig struct {
	FastAlpha           float64
	SlowAlpha           float64
	Epsilon             float64
	MinCostFloor        float64
	TTFTDegradedRatio   float64
	ErrorRateDegraded   float64
	TimeoutRateDegraded float64
	ErrorRateOpen       float64
	TimeoutRateOpen     float64
}

type HealthThresholds struct {
	TTFTDegradedRatio   float64       `json:"ttft_degraded_ratio"`
	ErrorRateDegraded   float64       `json:"error_rate_degraded"`
	TimeoutRateDegraded float64       `json:"timeout_rate_degraded"`
	ErrorRateOpen       float64       `json:"error_rate_open"`
	TimeoutRateOpen     float64       `json:"timeout_rate_open"`
	FailureThreshold    int64         `json:"failure_threshold"`
	OpenWindow          time.Duration `json:"open_window"`
}

func defaultCostConfig() costConfig {
	return costConfig{
		FastAlpha:           0.30,
		SlowAlpha:           0.05,
		Epsilon:             0.02,
		MinCostFloor:        0.001,
		TTFTDegradedRatio:   1.5,
		ErrorRateDegraded:   0.15,
		TimeoutRateDegraded: 0.10,
		ErrorRateOpen:       0.35,
		TimeoutRateOpen:     0.25,
	}
}

func DefaultHealthThresholds() HealthThresholds {
	costs := defaultCostConfig()
	return HealthThresholds{
		TTFTDegradedRatio:   costs.TTFTDegradedRatio,
		ErrorRateDegraded:   costs.ErrorRateDegraded,
		TimeoutRateDegraded: costs.TimeoutRateDegraded,
		ErrorRateOpen:       costs.ErrorRateOpen,
		TimeoutRateOpen:     costs.TimeoutRateOpen,
		FailureThreshold:    3,
		OpenWindow:          15 * time.Second,
	}
}

type Router struct {
	mu               sync.RWMutex
	targets          []*Target
	modelToTargets   map[string][]*Target
	policy           string
	openWindow       time.Duration
	failureThreshold int64
	fallbackPolicy   string
	refreshInterval  time.Duration
	discoveryEnabled bool
	costs            costConfig
	store            *store.Store
	random           *rand.Rand
	sticky           *StickyBindingStore
	stopCh           chan struct{}
	stopOnce         sync.Once
	refreshMu        sync.Mutex
	refreshCond      *sync.Cond
	refreshing       bool
}

func (r *Router) HealthThresholds() HealthThresholds {
	if r == nil {
		return DefaultHealthThresholds()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return HealthThresholds{
		TTFTDegradedRatio:   r.costs.TTFTDegradedRatio,
		ErrorRateDegraded:   r.costs.ErrorRateDegraded,
		TimeoutRateDegraded: r.costs.TimeoutRateDegraded,
		ErrorRateOpen:       r.costs.ErrorRateOpen,
		TimeoutRateOpen:     r.costs.TimeoutRateOpen,
		FailureThreshold:    r.failureThreshold,
		OpenWindow:          r.openWindow,
	}
}

type Target struct {
	ID                   string
	RouteTargetID        string
	ChannelID            string
	CredentialID         string
	CredentialHint       string
	Enabled              bool
	Priority             int
	Weight               float64
	CapacityHint         float64
	ModelDiscovery       string
	StaticModels         []string
	ModelAliases         map[string]string
	configuredModelsOnly bool
	Upstream             upstream.ResolvedUpstream

	allowUnknownModels bool

	mu                  sync.Mutex
	inflight            int64
	inflightStreaming   int64
	inflightNonStream   int64
	consecutiveFailures int64
	openUntil           time.Time
	models              map[string]struct{}
	lastRefreshAt       time.Time
	lastRefreshStatus   string
	lastRefreshError    string
	ttftFastMs          float64
	ttftSlowMs          float64
	reqLatencyFastMs    float64
	reqLatencySlowMs    float64
	errorRate           float64
	timeoutRate         float64
	cancelRate          float64
	healthState         string
	modelHealth         map[string]*modelHealthState
}

type modelHealthState struct {
	consecutiveFailures int64
	openUntil           time.Time
	healthState         string
	errorRate           float64
}

type Snapshot struct {
	ID                string    `json:"id"`
	RouteTargetID     string    `json:"route_target_id,omitempty"`
	ChannelID         string    `json:"channel_id,omitempty"`
	CredentialID      string    `json:"credential_id,omitempty"`
	CredentialHint    string    `json:"credential_hint,omitempty"`
	Enabled           bool      `json:"enabled"`
	Priority          int       `json:"priority"`
	Weight            float64   `json:"weight"`
	CapacityHint      float64   `json:"capacity_hint"`
	ModelDiscovery    string    `json:"model_discovery"`
	BaseURL           string    `json:"base_url"`
	ProviderPreset    string    `json:"provider_preset"`
	APIType           string    `json:"api_type"`
	Mode              string    `json:"mode,omitempty"`
	ProtocolFamily    string    `json:"protocol_family"`
	RoutingProfile    string    `json:"routing_profile"`
	HealthState       string    `json:"health_state"`
	Inflight          int64     `json:"inflight"`
	InflightStreaming int64     `json:"inflight_streaming"`
	InflightNonStream int64     `json:"inflight_non_stream"`
	TTFTFastMs        float64   `json:"ttft_fast_ms"`
	TTFTSlowMs        float64   `json:"ttft_slow_ms"`
	LatencyFastMs     float64   `json:"latency_fast_ms"`
	LatencySlowMs     float64   `json:"latency_slow_ms"`
	ErrorRate         float64   `json:"error_rate"`
	TimeoutRate       float64   `json:"timeout_rate"`
	CancelRate        float64   `json:"cancel_rate"`
	LastRefreshAt     time.Time `json:"last_refresh_at"`
	LastRefreshStatus string    `json:"last_refresh_status"`
	LastRefreshError  string    `json:"last_refresh_error,omitempty"`
	OpenUntil         time.Time `json:"open_until,omitempty"`
	Models            []string  `json:"models"`
}

type Selection struct {
	Target         *Target
	Score          float64
	CandidateCount int
	Candidates     []string
	Request        RequestFeatures
	Decision       *DecisionTrace
	Credential     CredentialDecisionInfo
}

type SelectionError struct {
	Reason   string
	Message  string
	Decision *DecisionTrace
}

func (e *SelectionError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Reason
}

const (
	SelectionFailureNilRequest         = "nil_request"
	SelectionFailureNoSupportingTarget = "no_supporting_target"
	SelectionFailureAllTargetsOpen     = "all_targets_open"
	SelectionFailureAllTargetsExcluded = "all_targets_excluded"
	SelectionFailureUnknown            = "unknown"
)

const LocalResponsesServerBackendRequiredError = "local Responses execution mode requires at least one enabled OpenAI-compatible chat completions-compatible upstream"

func LocalResponsesServerBackendRequired() error {
	return errors.New(LocalResponsesServerBackendRequiredError)
}

func SelectionFailureReason(err error) string {
	var selectionErr *SelectionError
	if errors.As(err, &selectionErr) && strings.TrimSpace(selectionErr.Reason) != "" {
		return selectionErr.Reason
	}
	return SelectionFailureUnknown
}

func SelectionDecision(err error) *DecisionTrace {
	var selectionErr *SelectionError
	if errors.As(err, &selectionErr) {
		return selectionErr.Decision
	}
	return nil
}

type RequestFeatures struct {
	ModelName           string
	RequestBytes        int64
	EstPromptTokens     float64
	MaxTokens           float64
	Stream              bool
	HasTools            bool
	HasStructuredOutput bool
}

type DecisionTrace struct {
	ModelName              string              `json:"model_name,omitempty"`
	Endpoint               string              `json:"endpoint,omitempty"`
	Policy                 string              `json:"policy,omitempty"`
	FallbackPolicy         string              `json:"fallback_policy,omitempty"`
	ExcludedIDs            []string            `json:"excluded_ids,omitempty"`
	Candidates             []CandidateDecision `json:"candidates,omitempty"`
	AvailableCount         int                 `json:"available_count"`
	SelectedID             string              `json:"selected_id,omitempty"`
	SelectedRouteTargetID  string              `json:"route_target_id,omitempty"`
	SelectedChannelID      string              `json:"channel_id,omitempty"`
	SelectedCredentialID   string              `json:"credential_id,omitempty"`
	SelectedCredentialHint string              `json:"credential_hint,omitempty"`
	SelectedScore          float64             `json:"selected_score,omitempty"`
	FailureReason          string              `json:"failure_reason,omitempty"`
	StickyKey              string              `json:"sticky_key,omitempty"`
	StickyStatus           string              `json:"sticky_status,omitempty"`
	StickyTargetID         string              `json:"sticky_target_id,omitempty"`
	StickyBreakID          string              `json:"sticky_break_id,omitempty"`
	StickyEvents           []StickyDecision    `json:"sticky_events,omitempty"`
}

type CandidateDecision struct {
	ID                     string  `json:"id"`
	RouteTargetID          string  `json:"route_target_id,omitempty"`
	ChannelID              string  `json:"channel_id,omitempty"`
	CredentialID           string  `json:"credential_id,omitempty"`
	CredentialHint         string  `json:"credential_hint,omitempty"`
	CredentialHealthState  string  `json:"credential_health_state,omitempty"`
	CredentialSelectable   *bool   `json:"credential_selectable,omitempty"`
	CredentialFilterReason string  `json:"credential_filter_reason,omitempty"`
	ProviderPreset         string  `json:"provider_preset,omitempty"`
	APIType                string  `json:"api_type,omitempty"`
	Mode                   string  `json:"mode,omitempty"`
	BaseURL                string  `json:"base_url,omitempty"`
	Priority               int     `json:"priority"`
	Weight                 float64 `json:"weight"`
	HealthState            string  `json:"health_state,omitempty"`
	SupportsPath           bool    `json:"supports_path"`
	SupportsModel          bool    `json:"supports_model"`
	SupportsTools          bool    `json:"supports_tools"`
	Excluded               bool    `json:"excluded,omitempty"`
	Selectable             bool    `json:"selectable"`
	FilterReason           string  `json:"filter_reason,omitempty"`
}

type StickyDecision struct {
	Status         string `json:"status,omitempty"`
	Key            string `json:"key,omitempty"`
	TargetID       string `json:"target_id,omitempty"`
	BreakID        string `json:"break_id,omitempty"`
	RouteTargetID  string `json:"route_target_id,omitempty"`
	ChannelID      string `json:"channel_id,omitempty"`
	CredentialID   string `json:"credential_id,omitempty"`
	CredentialHint string `json:"credential_hint,omitempty"`
}

type CredentialDecisionInfo struct {
	RouteTargetID  string
	ChannelID      string
	CredentialID   string
	CredentialHint string
}

type Outcome struct {
	Success        bool
	ClientCanceled bool
	StatusCode     int
	DurationMs     float64
	TTFTMs         float64
	Stream         bool
}

func New(cfg *config.Config, st *store.Store) (*Router, error) {
	targetCfgs := cfg.EffectiveUpstreams()
	if len(cfg.Upstreams) > 0 && strings.TrimSpace(cfg.Upstream.BaseURL) != "" {
		return nil, fmt.Errorf("config cannot define both upstream and upstreams")
	}

	r := &Router{
		modelToTargets:   make(map[string][]*Target),
		policy:           normalizePolicy(cfg.Router.Selection.Policy),
		openWindow:       cfg.Router.Selection.OpenWindow,
		failureThreshold: cfg.Router.Selection.FailureThreshold,
		fallbackPolicy:   normalizeFallback(cfg.Router.Fallback.OnMissingModel),
		refreshInterval:  cfg.Router.ModelDiscovery.RefreshInterval,
		discoveryEnabled: cfg.Router.ModelDiscovery.Enabled == nil || *cfg.Router.ModelDiscovery.Enabled,
		costs:            defaultCostConfig(),
		store:            st,
		random:           rand.New(rand.NewSource(time.Now().UnixNano())),
		sticky:           NewStickyBindingStore(defaultStickyBindingTTL),
		stopCh:           make(chan struct{}),
	}
	r.refreshCond = sync.NewCond(&r.refreshMu)
	if cfg.Router.Selection.Epsilon > 0 {
		r.costs.Epsilon = cfg.Router.Selection.Epsilon
	}
	if r.openWindow <= 0 {
		r.openWindow = 15 * time.Second
	}
	if r.refreshInterval <= 0 {
		r.refreshInterval = 10 * time.Minute
	}
	if r.failureThreshold <= 0 {
		r.failureThreshold = 3
	}
	if len(targetCfgs) == 0 {
		return r, nil
	}

	targets, err := buildTargets(targetCfgs)
	if err != nil {
		return nil, err
	}
	r.targets = targets
	return r, nil
}

func buildTargets(targetCfgs []config.UpstreamTargetConfig) ([]*Target, error) {
	if len(targetCfgs) == 0 {
		return nil, nil
	}
	seenIDs := map[string]struct{}{}
	targets := make([]*Target, 0, len(targetCfgs))
	for idx, targetCfg := range targetCfgs {
		enabled := true
		if targetCfg.Enabled != nil {
			enabled = *targetCfg.Enabled
		}
		if !enabled {
			continue
		}

		resolved, err := upstream.Resolve(targetCfg.Upstream)
		if err != nil {
			return nil, fmt.Errorf("resolve upstream target %q: %w", targetID(targetCfg, idx), err)
		}

		channelID := targetID(targetCfg, idx)
		credentials := explicitCredentials(targetCfg.Credentials)
		if len(credentials) == 0 {
			target := newTargetFromConfig(targetCfg, resolved, channelID, routeTargetID(channelID, "default"), channelID, "default", credentialHintFromUpstream(resolved), len(targetCfgs) == 1)
			if err := appendTarget(&targets, seenIDs, target); err != nil {
				return nil, err
			}
			continue
		}
		for credIdx, credential := range credentials {
			credentialID := credentialID(credential, credIdx)
			resolvedForCredential := resolved
			if strings.TrimSpace(credential.ApiKey) != "" {
				resolvedForCredential.APIKey = credential.ApiKey
			}
			routeTargetID := routeTargetID(channelID, credentialID)
			target := newTargetFromConfig(targetCfg, resolvedForCredential, routeTargetID, routeTargetID, channelID, credentialID, credentialHint(credential, resolvedForCredential), len(targetCfgs) == 1)
			if err := appendTarget(&targets, seenIDs, target); err != nil {
				return nil, err
			}
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no enabled upstream targets configured")
	}
	sortTargets(targets)
	return targets, nil
}

func newTargetFromConfig(targetCfg config.UpstreamTargetConfig, resolved upstream.ResolvedUpstream, id string, routeTargetID string, channelID string, credentialID string, credentialHint string, singleConfiguredTarget bool) *Target {
	return &Target{
		ID:                   id,
		RouteTargetID:        routeTargetID,
		ChannelID:            channelID,
		CredentialID:         credentialID,
		CredentialHint:       credentialHint,
		Enabled:              true,
		Priority:             targetCfg.Priority,
		Weight:               defaultFloat(targetCfg.Weight, 1),
		CapacityHint:         defaultFloat(targetCfg.CapacityHint, 1),
		ModelDiscovery:       normalizeDiscoveryMode(targetCfg.ModelDiscovery),
		StaticModels:         normalizeModels(targetCfg.StaticModels),
		ModelAliases:         normalizeModelAliases(targetCfg.ModelAliases),
		configuredModelsOnly: targetCfg.ConfiguredModelsOnly,
		Upstream:             resolved,
		allowUnknownModels:   allowUnknownModels(targetCfg, singleConfiguredTarget),
		models:               map[string]struct{}{},
		ttftFastMs:           500,
		ttftSlowMs:           500,
		reqLatencyFastMs:     800,
		reqLatencySlowMs:     800,
		healthState:          HealthHealthy,
		modelHealth:          map[string]*modelHealthState{},
	}
}

func (t *Target) ResolveModelAlias(model string) string {
	if t == nil || len(t.ModelAliases) == 0 {
		return ""
	}
	return t.ModelAliases[strings.ToLower(strings.TrimSpace(model))]
}

func appendTarget(targets *[]*Target, seenIDs map[string]struct{}, target *Target) error {
	if target == nil {
		return nil
	}
	if _, exists := seenIDs[target.ID]; exists {
		return fmt.Errorf("duplicate upstream target id %q", target.ID)
	}
	seenIDs[target.ID] = struct{}{}
	*targets = append(*targets, target)
	return nil
}

func sortTargets(targets []*Target) {
	slices.SortFunc(targets, func(a, b *Target) int {
		if a.Priority != b.Priority {
			return b.Priority - a.Priority
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func (r *Router) Initialize() error {
	if r == nil {
		return nil
	}
	if len(r.Targets()) == 0 {
		return nil
	}
	usable, err := r.refreshAll()
	if err != nil {
		return err
	}
	if usable == 0 {
		return fmt.Errorf("no usable upstream targets after startup discovery")
	}
	r.mu.Lock()
	r.rebuildCatalog()
	r.mu.Unlock()
	return nil
}

func (r *Router) Reload(targetCfgs []config.UpstreamTargetConfig) error {
	if r == nil {
		return fmt.Errorf("router is nil")
	}
	nextTargets, err := buildTargets(targetCfgs)
	if err != nil {
		return err
	}

	r.mu.RLock()
	existing := make(map[string]*Target, len(r.targets))
	for _, target := range r.targets {
		existing[target.ID] = target
	}
	r.mu.RUnlock()
	for _, target := range nextTargets {
		if old := existing[target.ID]; old != nil {
			target.inheritRuntimeState(old)
		}
	}

	if len(nextTargets) > 0 {
		if _, err := r.refreshTargets(nextTargets); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.targets = nextTargets
	r.rebuildCatalog()
	r.mu.Unlock()
	return nil
}

func (r *Router) Targets() []*Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*Target(nil), r.targets...)
}

func (r *Router) HasLocalResponsesServerBackend() bool {
	if r == nil {
		return false
	}
	for _, target := range r.Targets() {
		if target == nil || !target.Enabled {
			continue
		}
		if SupportsLocalResponsesServerBackend(target.Upstream) {
			return true
		}
	}
	return false
}

func ValidateLocalResponsesServerBackendConfig(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.Upstreams) > 0 && strings.TrimSpace(cfg.Upstream.BaseURL) != "" {
		return fmt.Errorf("config cannot define both upstream and upstreams")
	}
	targets := cfg.EffectiveUpstreams()
	if len(targets) == 0 {
		return nil
	}
	for idx, targetCfg := range targets {
		enabled := true
		if targetCfg.Enabled != nil {
			enabled = *targetCfg.Enabled
		}
		if !enabled {
			continue
		}
		resolved, err := upstream.Resolve(targetCfg.Upstream)
		if err != nil {
			return fmt.Errorf("resolve upstream target %q: %w", targetID(targetCfg, idx), err)
		}
		if SupportsLocalResponsesServerBackend(resolved) {
			return nil
		}
	}
	return LocalResponsesServerBackendRequired()
}

func SupportsLocalResponsesServerBackend(resolved upstream.ResolvedUpstream) bool {
	if resolved.ProtocolFamily != upstream.ProtocolFamilyOpenAICompatible {
		return false
	}
	return resolved.SupportsChatCompletionsAPI()
}

func (r *Router) Policy() string {
	if r == nil {
		return ""
	}
	return r.policy
}

func (r *Router) StartBackgroundRefresh() {
	if r == nil || !r.discoveryEnabled || r.refreshInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(r.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := r.refreshAll(); err != nil {
					continue
				}
				r.mu.Lock()
				r.rebuildCatalog()
				r.mu.Unlock()
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *Router) RefreshNow() (int, error) {
	if r == nil {
		return 0, nil
	}
	r.refreshMu.Lock()
	if r.refreshing {
		for r.refreshing {
			r.refreshCond.Wait()
		}
		r.refreshMu.Unlock()
		return 0, nil
	}
	r.refreshing = true
	r.refreshMu.Unlock()
	defer func() {
		r.refreshMu.Lock()
		r.refreshing = false
		r.refreshCond.Broadcast()
		r.refreshMu.Unlock()
	}()

	usable, err := r.refreshAll()
	if err != nil {
		return usable, err
	}
	r.mu.Lock()
	r.rebuildCatalog()
	r.mu.Unlock()
	return usable, nil
}

func (r *Router) Close() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
}

func (r *Router) Snapshots() []Snapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	targets := append([]*Target(nil), r.targets...)
	r.mu.RUnlock()

	out := make([]Snapshot, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.snapshot())
	}
	slices.SortFunc(out, func(a, b Snapshot) int {
		if a.Priority != b.Priority {
			return b.Priority - a.Priority
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (r *Router) AggregatedModels() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	targets := append([]*Target(nil), r.targets...)
	r.mu.RUnlock()

	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, target := range targets {
		if target.configuredModelsOnly || len(target.StaticModels) > 0 {
			for _, model := range normalizeModels(target.StaticModels) {
				if model == "" {
					continue
				}
				if _, ok := seen[model]; ok {
					continue
				}
				seen[model] = struct{}{}
				out = append(out, model)
			}
			continue
		}
		target.mu.Lock()
		for model := range target.models {
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			out = append(out, model)
		}
		target.mu.Unlock()
	}
	slices.Sort(out)
	return out
}

func (r *Router) Select(req *http.Request) (*Selection, error) {
	if req == nil {
		return nil, &SelectionError{
			Reason:  SelectionFailureNilRequest,
			Message: "nil request",
		}
	}
	body, err := readAndRestoreBody(req)
	if err != nil {
		return nil, err
	}
	return r.SelectWithBody(req, body)
}

// SelectWithExclusion works like SelectWithBody but excludes the given target IDs
// from consideration. This is used by the proxy handler to retry after an upstream
// failure with the next available candidate.
func (r *Router) SelectWithExclusion(req *http.Request, body []byte, excludeIDs []string) (*Selection, error) {
	if req == nil {
		return nil, &SelectionError{
			Reason:  SelectionFailureNilRequest,
			Message: "nil request",
		}
	}
	if len(excludeIDs) == 0 {
		return r.SelectWithBody(req, body)
	}
	return r.selectTargets(req, body, excludeIDs)
}

func (r *Router) SelectWithBody(req *http.Request, body []byte) (*Selection, error) {
	if req == nil {
		return nil, &SelectionError{
			Reason:  SelectionFailureNilRequest,
			Message: "nil request",
		}
	}
	return r.selectTargets(req, body, nil)
}

func (r *Router) HasSelectableCandidateWithBody(req *http.Request, body []byte) bool {
	if r == nil || req == nil {
		return false
	}
	rawPath := req.URL.Path
	features := extractRequestFeatures(rawPath, body)
	model := features.ModelName
	candidates := r.candidatesForRequest(rawPath, model, features)
	now := time.Now()
	for _, candidate := range candidates {
		if candidate.canSelect(now, model) {
			return true
		}
	}
	return false
}

func (r *Router) HasSelectableNativeResponsesCandidateWithBody(req *http.Request, body []byte) bool {
	if r == nil || req == nil {
		return false
	}
	rawPath := req.URL.Path
	features := extractRequestFeatures(rawPath, body)
	model := features.ModelName
	candidates := r.candidatesForRequest(rawPath, model, features)
	now := time.Now()
	for _, candidate := range candidates {
		if candidate.Upstream.SupportsResponsesAPIForModel(model) && candidate.canSelect(now, model) {
			return true
		}
	}
	return false
}

func (r *Router) HasNativeResponsesTargetWithBody(req *http.Request, body []byte) bool {
	if r == nil || req == nil {
		return false
	}
	rawPath := req.URL.Path
	features := extractRequestFeatures(rawPath, body)
	if llm.NormalizeEndpoint(rawPath) != "/v1/responses" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, target := range r.targets {
		if target == nil || !target.Upstream.SupportsResponsesAPIForModel(features.ModelName) {
			continue
		}
		if supportsPath(target, rawPath, features) && supportsRequestFeatures(target, features) {
			return true
		}
	}
	return false
}

// selectTargets is the shared selection core used by SelectWithBody and SelectWithExclusion.
func (r *Router) selectTargets(req *http.Request, body []byte, excludeIDs []string) (*Selection, error) {
	rawPath := req.URL.Path
	features := extractRequestFeatures(rawPath, body)
	model := features.ModelName
	stickyKey := extractStickyKey(req, body)
	stickyTargetID, hasStickyBinding := r.sticky.Lookup(stickyKey)

	r.mu.RLock()
	defer r.mu.RUnlock()

	decision := r.buildDecisionTrace(rawPath, model, excludeIDs, features)
	candidates := r.candidatesForRequest(rawPath, model, features)
	if len(candidates) == 0 {
		return nil, &SelectionError{
			Reason:   SelectionFailureNoSupportingTarget,
			Message:  fmt.Sprintf("no upstream target supports model %q for endpoint %q", model, llm.NormalizeEndpoint(rawPath)),
			Decision: decision.withFailure(SelectionFailureNoSupportingTarget),
		}
	}

	excludeSet := make(map[string]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = struct{}{}
	}

	available := make([]*Target, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.canSelect(time.Now(), model) {
			continue
		}
		if _, excluded := excludeSet[candidate.ID]; excluded {
			continue
		}
		available = append(available, candidate)
	}
	decision.markAvailable(available)
	if len(available) == 0 {
		reason := SelectionFailureAllTargetsOpen
		msg := fmt.Sprintf("all upstream targets are temporarily unavailable for model %q", model)
		if len(excludeIDs) > 0 {
			reason = SelectionFailureAllTargetsExcluded
			msg = fmt.Sprintf("all upstream targets for model %q have been exhausted", model)
		}
		return nil, &SelectionError{
			Reason:   reason,
			Message:  msg,
			Decision: decision.withFailure(reason),
		}
	}

	var selected *Target
	var score float64
	if hasStickyBinding {
		if stickyTarget := findTargetByRouteTargetID(available, stickyTargetID); stickyTarget != nil {
			selected = stickyTarget
			score = r.expectedCost(selected, features)
			decision.withStickyTarget("hit", stickyKey, stickyTarget, stickyTarget.ID, "")
		} else {
			stickyTarget := findTargetByRouteTargetID(candidates, stickyTargetID)
			decision.withStickyTarget("break", stickyKey, stickyTarget, targetAlias(stickyTarget, stickyTargetID), targetAlias(stickyTarget, stickyTargetID))
		}
	} else if stickyKey != "" {
		decision.withSticky("miss", stickyKey, "", "")
	}
	if selected == nil {
		selected, score = r.pick(available, features)
		if stickyKey != "" {
			r.sticky.Bind(stickyKey, selected.RouteTargetID)
			if hasStickyBinding {
				previousTarget := findTargetByRouteTargetID(candidates, stickyTargetID)
				decision.withStickyTarget("bind", stickyKey, selected, selected.ID, targetAlias(previousTarget, stickyTargetID))
			} else {
				decision.withStickyTarget("bind", stickyKey, selected, selected.ID, "")
			}
		}
	}
	selected.onStart(features)

	candidateIDs := make([]string, 0, len(available))
	for _, candidate := range available {
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	return &Selection{
		Target:         selected,
		Score:          score,
		CandidateCount: len(available),
		Candidates:     candidateIDs,
		Request:        features,
		Decision:       decision.withSelectedTarget(selected, score),
		Credential:     credentialDecisionFromTarget(selected),
	}, nil
}

func findTargetByRouteTargetID(targets []*Target, routeTargetID string) *Target {
	for _, target := range targets {
		if target != nil && target.RouteTargetID == routeTargetID {
			return target
		}
	}
	return nil
}

func targetAlias(target *Target, fallback string) string {
	if target != nil && target.ID != "" {
		return target.ID
	}
	return fallback
}

func (r *Router) buildDecisionTrace(rawPath string, model string, excludeIDs []string, features RequestFeatures) *DecisionTrace {
	decision := &DecisionTrace{
		ModelName:      model,
		Endpoint:       llm.NormalizeEndpoint(rawPath),
		Policy:         r.policy,
		FallbackPolicy: r.fallbackPolicy,
		ExcludedIDs:    append([]string(nil), excludeIDs...),
		Candidates:     make([]CandidateDecision, 0, len(r.targets)),
	}
	excludeSet := make(map[string]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = struct{}{}
	}
	now := time.Now()
	for _, target := range r.targets {
		candidate := target.candidateDecision(rawPath, model, now, features)
		if _, excluded := excludeSet[target.ID]; excluded {
			candidate.Excluded = true
			candidate.Selectable = false
			candidate.FilterReason = "excluded"
		}
		decision.Candidates = append(decision.Candidates, candidate)
	}
	return decision
}

func (d *DecisionTrace) markAvailable(available []*Target) {
	if d == nil {
		return
	}
	availableSet := make(map[string]struct{}, len(available))
	for _, target := range available {
		if target != nil {
			availableSet[target.ID] = struct{}{}
		}
	}
	d.AvailableCount = len(availableSet)
	for i := range d.Candidates {
		if _, ok := availableSet[d.Candidates[i].ID]; ok {
			d.Candidates[i].Selectable = true
			d.Candidates[i].FilterReason = ""
		}
	}
}

func (d *DecisionTrace) withSelection(selectedID string, score float64) *DecisionTrace {
	if d == nil {
		return nil
	}
	d.SelectedID = selectedID
	d.SelectedRouteTargetID = selectedID
	d.SelectedScore = score
	return d
}

func (d *DecisionTrace) withSelectedTarget(target *Target, score float64) *DecisionTrace {
	if d == nil {
		return nil
	}
	if target == nil {
		return d.withSelection("", score)
	}
	d.SelectedID = target.ID
	d.SelectedRouteTargetID = target.RouteTargetID
	d.SelectedChannelID = target.ChannelID
	d.SelectedCredentialID = target.CredentialID
	d.SelectedCredentialHint = target.CredentialHint
	d.SelectedScore = score
	return d
}

func (d *DecisionTrace) withFailure(reason string) *DecisionTrace {
	if d == nil {
		return nil
	}
	d.FailureReason = reason
	return d
}

func (d *DecisionTrace) withSticky(status string, key string, targetID string, breakID string) *DecisionTrace {
	if d == nil {
		return nil
	}
	return d.withStickyTarget(status, key, nil, targetID, breakID)
}

func (d *DecisionTrace) withStickyTarget(status string, key string, target *Target, targetID string, breakID string) *DecisionTrace {
	if d == nil {
		return nil
	}
	d.StickyStatus = status
	d.StickyKey = key
	d.StickyTargetID = targetID
	d.StickyBreakID = breakID
	event := StickyDecision{
		Status:   status,
		Key:      key,
		TargetID: targetID,
		BreakID:  breakID,
	}
	if target != nil {
		event.RouteTargetID = target.RouteTargetID
		event.ChannelID = target.ChannelID
		event.CredentialID = target.CredentialID
		event.CredentialHint = target.CredentialHint
	}
	d.StickyEvents = append(d.StickyEvents, event)
	return d
}

func (r *Router) Complete(selection *Selection, outcome Outcome) {
	if selection == nil || selection.Target == nil {
		return
	}
	selection.Target.onFinish(selection.Request, outcome, r.costs, r.failureThreshold, r.openWindow)
}

func (r *Router) Release(selection *Selection) {
	if selection == nil || selection.Target == nil {
		return
	}
	selection.Target.onRelease(selection.Request)
}

func (r *Router) pick(candidates []*Target, req RequestFeatures) (*Target, float64) {
	if len(candidates) == 1 || r.policy == PolicyFirstAvailable {
		best := candidates[0]
		bestScore := r.expectedCost(best, req)
		for _, candidate := range candidates[1:] {
			candidateScore := r.expectedCost(candidate, req)
			if compareScore(candidate, candidateScore, best, bestScore) < 0 {
				best = candidate
				bestScore = candidateScore
			}
		}
		return best, bestScore
	}

	return r.pickCostAware(candidates, req)
}

func (r *Router) pickCostAware(candidates []*Target, req RequestFeatures) (*Target, float64) {
	if len(candidates) == 1 {
		return candidates[0], r.expectedCost(candidates[0], req)
	}
	if r.random.Float64() < r.costs.Epsilon {
		idx := r.random.Intn(len(candidates))
		return candidates[idx], r.expectedCost(candidates[idx], req)
	}

	aIdx := r.random.Intn(len(candidates))
	bIdx := r.random.Intn(len(candidates) - 1)
	if bIdx >= aIdx {
		bIdx++
	}
	a := candidates[aIdx]
	b := candidates[bIdx]
	scoreA := r.expectedCost(a, req)
	scoreB := r.expectedCost(b, req)
	if compareScore(a, scoreA, b, scoreB) <= 0 {
		return a, scoreA
	}
	return b, scoreB
}

func (r *Router) candidatesForRequest(rawPath string, model string, features RequestFeatures) []*Target {
	if model == ModelDiscoveryListModels {
		candidates := make([]*Target, 0, len(r.targets))
		for _, target := range r.targets {
			if supportsPath(target, rawPath, features) && supportsRequestFeatures(target, features) {
				candidates = append(candidates, target)
			}
		}
		return candidates
	}

	var candidates []*Target
	if model != "" {
		for _, target := range r.modelToTargets[strings.ToLower(model)] {
			if supportsPath(target, rawPath, features) && supportsRequestFeatures(target, features) {
				candidates = append(candidates, target)
			}
		}
	}
	if len(candidates) > 0 {
		return candidates
	}

	var fallback []*Target
	for _, target := range r.targets {
		if !supportsPath(target, rawPath, features) {
			continue
		}
		if !supportsRequestFeatures(target, features) {
			continue
		}
		if target.allowUnknownModels || model == "" || r.fallbackPolicy != FallbackReject {
			fallback = append(fallback, target)
		}
	}
	return fallback
}

func (r *Router) refreshTarget(target *Target) ([]string, string, error) {
	modelSet := map[string]struct{}{}
	for _, model := range target.StaticModels {
		modelSet[strings.ToLower(model)] = struct{}{}
	}
	if inferred := inferConfiguredModel(target.Upstream); inferred != "" {
		modelSet[strings.ToLower(inferred)] = struct{}{}
	}

	status := "static"
	var discoverErr error
	if target.ModelDiscovery != ModelDiscoveryDisabled && target.ModelDiscovery != ModelDiscoveryStaticOnly {
		discovered, err := upstream.DiscoverModelsResolved(target.Upstream, nil)
		if err != nil {
			discoverErr = err
			status = "error"
		} else {
			status = "ready"
			for _, model := range discovered {
				modelSet[strings.ToLower(strings.TrimSpace(model))] = struct{}{}
			}
		}
	}

	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		if model != "" {
			models = append(models, model)
		}
	}
	slices.Sort(models)
	if len(models) == 0 && discoverErr == nil {
		status = "empty"
	}
	return models, status, discoverErr
}

func (r *Router) refreshAll() (int, error) {
	r.mu.RLock()
	targets := append([]*Target(nil), r.targets...)
	r.mu.RUnlock()
	return r.refreshTargets(targets)
}

func (r *Router) refreshTargets(targets []*Target) (int, error) {
	var usable int
	for _, target := range targets {
		models, status, refreshErr := r.refreshTarget(target)
		if refreshErr == nil || len(models) > 0 || target.allowUnknownModels {
			usable++
		}
		target.setRefreshResult(models, status, refreshErr, r.failureThreshold, r.openWindow, r.costs)
		if r.store != nil {
			record := store.UpstreamTargetRecord{
				ID:                target.ID,
				BaseURL:           target.Upstream.BaseURL,
				ProviderPreset:    target.Upstream.ProviderPreset,
				ProtocolFamily:    target.Upstream.ProtocolFamily,
				RoutingProfile:    target.Upstream.RoutingProfile,
				Enabled:           target.Enabled,
				Priority:          target.Priority,
				Weight:            target.Weight,
				CapacityHint:      target.CapacityHint,
				LastRefreshAt:     target.snapshot().LastRefreshAt,
				LastRefreshStatus: status,
			}
			if refreshErr != nil {
				record.LastRefreshError = refreshErr.Error()
			}
			if err := r.store.UpsertUpstreamTarget(record); err != nil {
				return 0, err
			}
			modelRecords := make([]store.UpstreamModelRecord, 0, len(models))
			seenAt := time.Now().UTC()
			for _, model := range models {
				modelRecords = append(modelRecords, store.UpstreamModelRecord{
					UpstreamID: target.ID,
					Model:      model,
					Source:     "catalog",
					SeenAt:     seenAt,
				})
			}
			if err := r.store.ReplaceUpstreamModels(target.ID, modelRecords); err != nil {
				return 0, err
			}
		}
	}
	return usable, nil
}

func (r *Router) rebuildCatalog() {
	catalog := make(map[string][]*Target)
	for _, target := range r.targets {
		for model := range target.models {
			catalog[model] = append(catalog[model], target)
		}
	}
	for _, targets := range catalog {
		slices.SortFunc(targets, func(a, b *Target) int {
			return compareScore(a, r.expectedCost(a, RequestFeatures{}), b, r.expectedCost(b, RequestFeatures{}))
		})
	}
	r.modelToTargets = catalog
}

func (t *Target) setRefreshResult(models []string, status string, refreshErr error, failureThreshold int64, openWindow time.Duration, costs costConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.models = make(map[string]struct{}, len(models))
	for _, model := range models {
		if model != "" {
			t.models[strings.ToLower(model)] = struct{}{}
		}
	}
	t.lastRefreshAt = time.Now().UTC()
	t.lastRefreshStatus = status
	if refreshErr != nil {
		t.lastRefreshError = refreshErr.Error()
	} else {
		t.lastRefreshError = ""
	}
	t.applyRefreshHealthLocked(models, status, refreshErr, failureThreshold, openWindow, costs)
}

func (t *Target) applyRefreshHealthLocked(models []string, status string, refreshErr error, failureThreshold int64, openWindow time.Duration, costs costConfig) {
	if status != "ready" && status != "empty" && status != "static" && status != "error" {
		return
	}
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	if openWindow <= 0 {
		openWindow = 15 * time.Second
	}
	if refreshErr != nil {
		t.consecutiveFailures++
		if t.errorRate > 0 {
			t.errorRate = ewma(t.errorRate, 1, costs.FastAlpha)
		}
		if t.consecutiveFailures >= failureThreshold {
			t.healthState = HealthOpen
			t.openUntil = time.Now().Add(openWindow)
			t.consecutiveFailures = 0
			return
		}
		if t.healthState != HealthOpen {
			t.healthState = HealthDegraded
		}
		return
	}
	t.errorRate = ewma(t.errorRate, 0, costs.FastAlpha)
	t.timeoutRate = ewma(t.timeoutRate, 0, costs.FastAlpha)
	t.consecutiveFailures = 0
	if t.healthState == HealthOpen || t.healthState == HealthDegraded || t.healthState == HealthProbation {
		t.healthState = HealthProbation
		t.openUntil = time.Time{}
	}
	for _, model := range models {
		state := t.modelHealth[strings.ToLower(strings.TrimSpace(model))]
		if state == nil {
			continue
		}
		if state.healthState == HealthOpen || state.healthState == HealthDegraded || state.healthState == HealthProbation {
			state.healthState = HealthProbation
			state.openUntil = time.Time{}
			state.consecutiveFailures = 0
			state.errorRate = ewma(state.errorRate, 0, costs.FastAlpha)
		}
	}
}

func (t *Target) inheritRuntimeState(old *Target) {
	if t == nil || old == nil {
		return
	}
	old.mu.Lock()
	defer old.mu.Unlock()
	t.consecutiveFailures = old.consecutiveFailures
	t.openUntil = old.openUntil
	t.lastRefreshAt = old.lastRefreshAt
	t.lastRefreshStatus = old.lastRefreshStatus
	t.lastRefreshError = old.lastRefreshError
	t.ttftFastMs = old.ttftFastMs
	t.ttftSlowMs = old.ttftSlowMs
	t.reqLatencyFastMs = old.reqLatencyFastMs
	t.reqLatencySlowMs = old.reqLatencySlowMs
	t.errorRate = old.errorRate
	t.timeoutRate = old.timeoutRate
	t.cancelRate = old.cancelRate
	t.healthState = old.healthState
}

func (t *Target) snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	models := make([]string, 0, len(t.models))
	for model := range t.models {
		models = append(models, model)
	}
	slices.Sort(models)
	health := t.healthState
	if health == "" {
		health = HealthHealthy
	}
	return Snapshot{
		ID:                t.ID,
		RouteTargetID:     t.RouteTargetID,
		ChannelID:         t.ChannelID,
		CredentialID:      t.CredentialID,
		CredentialHint:    t.CredentialHint,
		Enabled:           t.Enabled,
		Priority:          t.Priority,
		Weight:            t.Weight,
		CapacityHint:      t.CapacityHint,
		ModelDiscovery:    t.ModelDiscovery,
		BaseURL:           t.Upstream.BaseURL,
		ProviderPreset:    t.Upstream.ProviderPreset,
		APIType:           t.Upstream.APIType,
		Mode:              t.Upstream.Mode,
		ProtocolFamily:    t.Upstream.ProtocolFamily,
		RoutingProfile:    t.Upstream.RoutingProfile,
		HealthState:       health,
		Inflight:          t.inflight,
		InflightStreaming: t.inflightStreaming,
		InflightNonStream: t.inflightNonStream,
		TTFTFastMs:        t.ttftFastMs,
		TTFTSlowMs:        t.ttftSlowMs,
		LatencyFastMs:     t.reqLatencyFastMs,
		LatencySlowMs:     t.reqLatencySlowMs,
		ErrorRate:         t.errorRate,
		TimeoutRate:       t.timeoutRate,
		CancelRate:        t.cancelRate,
		LastRefreshAt:     t.lastRefreshAt,
		LastRefreshStatus: t.lastRefreshStatus,
		LastRefreshError:  t.lastRefreshError,
		OpenUntil:         t.openUntil,
		Models:            models,
	}
}

func (t *Target) canSelect(now time.Time, model string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.healthState == HealthOpen && !t.openUntil.IsZero() && now.After(t.openUntil) {
		t.healthState = HealthProbation
	}
	if t.healthState == HealthOpen && !t.openUntil.IsZero() && now.Before(t.openUntil) {
		return false
	}
	if t.healthState == HealthProbation && t.inflight >= probationInflightLimit {
		return false
	}
	modelKey := strings.ToLower(strings.TrimSpace(model))
	if modelKey != "" {
		state := t.modelHealth[modelKey]
		if state != nil {
			if state.healthState == HealthOpen && !state.openUntil.IsZero() && now.After(state.openUntil) {
				state.healthState = HealthProbation
			}
			if state.healthState == HealthOpen && !state.openUntil.IsZero() && now.Before(state.openUntil) {
				return false
			}
			if state.healthState == HealthProbation && t.inflight >= probationInflightLimit {
				return false
			}
		}
	}
	return true
}

func (t *Target) candidateDecision(rawPath string, model string, now time.Time, features RequestFeatures) CandidateDecision {
	t.mu.Lock()
	defer t.mu.Unlock()

	decision := CandidateDecision{
		ID:             t.ID,
		RouteTargetID:  t.RouteTargetID,
		ChannelID:      t.ChannelID,
		CredentialID:   t.CredentialID,
		CredentialHint: t.CredentialHint,
		ProviderPreset: t.Upstream.ProviderPreset,
		APIType:        t.Upstream.APIType,
		Mode:           t.Upstream.Mode,
		BaseURL:        t.Upstream.BaseURL,
		Priority:       t.Priority,
		Weight:         t.Weight,
		HealthState:    t.healthState,
		SupportsPath:   supportsPath(t, rawPath, features),
		SupportsModel:  t.supportsModelLocked(model),
		SupportsTools:  supportsRequestFeatures(t, features),
		Selectable:     true,
	}
	if !decision.SupportsPath {
		decision.Selectable = false
		decision.FilterReason = "unsupported_path"
		return decision
	}
	if !decision.SupportsModel {
		decision.Selectable = false
		decision.FilterReason = "unsupported_model"
		return decision
	}
	if !decision.SupportsTools {
		decision.Selectable = false
		decision.FilterReason = "unsupported_tools"
		return decision
	}
	if t.healthState == HealthOpen && !t.openUntil.IsZero() && now.Before(t.openUntil) {
		decision.Selectable = false
		decision.FilterReason = "target_open"
		return decision
	}
	if t.healthState == HealthProbation && t.inflight >= probationInflightLimit {
		decision.Selectable = false
		decision.FilterReason = "target_probation_full"
		return decision
	}
	modelKey := strings.ToLower(strings.TrimSpace(model))
	if modelKey != "" {
		if state := t.modelHealth[modelKey]; state != nil {
			if state.healthState == HealthOpen && !state.openUntil.IsZero() && now.Before(state.openUntil) {
				decision.Selectable = false
				decision.FilterReason = "model_open"
				return decision
			}
			if state.healthState == HealthProbation && t.inflight >= probationInflightLimit {
				decision.Selectable = false
				decision.FilterReason = "model_probation_full"
				return decision
			}
		}
	}
	return decision
}

func credentialDecisionFromTarget(target *Target) CredentialDecisionInfo {
	if target == nil {
		return CredentialDecisionInfo{}
	}
	return CredentialDecisionInfo{
		RouteTargetID:  target.RouteTargetID,
		ChannelID:      target.ChannelID,
		CredentialID:   target.CredentialID,
		CredentialHint: target.CredentialHint,
	}
}

func (t *Target) supportsModelLocked(model string) bool {
	modelKey := strings.ToLower(strings.TrimSpace(model))
	if modelKey == "" || modelKey == ModelDiscoveryListModels {
		return true
	}
	if _, ok := t.models[modelKey]; ok {
		return true
	}
	return t.allowUnknownModels
}

func (t *Target) onStart(req RequestFeatures) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight++
	if req.Stream {
		t.inflightStreaming++
	} else {
		t.inflightNonStream++
	}
}

func (t *Target) onRelease(req RequestFeatures) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight > 0 {
		t.inflight--
	}
	if req.Stream {
		if t.inflightStreaming > 0 {
			t.inflightStreaming--
		}
	} else if t.inflightNonStream > 0 {
		t.inflightNonStream--
	}
}

func (t *Target) onFinish(req RequestFeatures, outcome Outcome, costs costConfig, failureThreshold int64, openWindow time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.releaseLocked(req)

	if outcome.DurationMs > 0 {
		t.reqLatencyFastMs = ewma(t.reqLatencyFastMs, outcome.DurationMs, costs.FastAlpha)
		t.reqLatencySlowMs = ewma(t.reqLatencySlowMs, outcome.DurationMs, costs.SlowAlpha)
	}
	if outcome.TTFTMs > 0 {
		t.ttftFastMs = ewma(t.ttftFastMs, outcome.TTFTMs, costs.FastAlpha)
		t.ttftSlowMs = ewma(t.ttftSlowMs, outcome.TTFTMs, costs.SlowAlpha)
	}

	if outcome.ClientCanceled {
		t.cancelRate = ewma(t.cancelRate, 1, costs.FastAlpha)
		return
	}
	t.cancelRate = ewma(t.cancelRate, 0, costs.FastAlpha)

	healthFailure := countsAsUpstreamHealthFailure(outcome)
	modelScoped := shouldUpdateModelHealth(req, outcome)
	if modelScoped {
		t.updateModelHealthLocked(req.ModelName, outcome, costs, failureThreshold, openWindow)
	}
	if outcome.Success {
		t.errorRate = ewma(t.errorRate, 0, costs.FastAlpha)
		t.timeoutRate = ewma(t.timeoutRate, 0, costs.FastAlpha)
		t.consecutiveFailures = 0
	} else if healthFailure && !modelScoped {
		t.errorRate = ewma(t.errorRate, 1, costs.FastAlpha)
		t.consecutiveFailures++
		if countsAsUpstreamTimeout(outcome) {
			t.timeoutRate = ewma(t.timeoutRate, 1, costs.FastAlpha)
		} else {
			t.timeoutRate = ewma(t.timeoutRate, 0, costs.FastAlpha)
		}
	}

	ttftRatio := ratio(t.ttftFastMs, t.ttftSlowMs)
	switch {
	case t.healthState == HealthProbation && outcome.Success:
		t.healthState = HealthHealthy
		t.openUntil = time.Time{}
		t.consecutiveFailures = 0
	case t.consecutiveFailures >= failureThreshold || t.errorRate >= costs.ErrorRateOpen || t.timeoutRate >= costs.TimeoutRateOpen:
		t.healthState = HealthOpen
		t.openUntil = time.Now().Add(openWindow)
		t.consecutiveFailures = 0
	case t.errorRate >= costs.ErrorRateDegraded || t.timeoutRate >= costs.TimeoutRateDegraded || ttftRatio >= costs.TTFTDegradedRatio:
		if t.healthState != HealthProbation {
			t.healthState = HealthDegraded
		}
	default:
		if t.healthState != HealthOpen {
			t.healthState = HealthHealthy
		}
	}
}

func (t *Target) releaseLocked(req RequestFeatures) {
	if t.inflight > 0 {
		t.inflight--
	}
	if req.Stream {
		if t.inflightStreaming > 0 {
			t.inflightStreaming--
		}
	} else if t.inflightNonStream > 0 {
		t.inflightNonStream--
	}
}

func shouldUpdateModelHealth(req RequestFeatures, outcome Outcome) bool {
	if strings.TrimSpace(req.ModelName) == "" || outcome.ClientCanceled {
		return false
	}
	if outcome.Success {
		return true
	}
	if outcome.StatusCode == 0 {
		return false
	}
	return countsAsUpstreamHealthFailure(outcome)
}

func (t *Target) updateModelHealthLocked(model string, outcome Outcome, costs costConfig, failureThreshold int64, openWindow time.Duration) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return
	}
	if t.modelHealth == nil {
		t.modelHealth = map[string]*modelHealthState{}
	}
	state := t.modelHealth[model]
	if state == nil {
		state = &modelHealthState{healthState: HealthHealthy}
		t.modelHealth[model] = state
	}
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	if openWindow <= 0 {
		openWindow = 15 * time.Second
	}
	if outcome.Success {
		state.errorRate = ewma(state.errorRate, 0, costs.FastAlpha)
		state.consecutiveFailures = 0
		if state.healthState == HealthOpen || state.healthState == HealthProbation || state.healthState == HealthDegraded {
			state.healthState = HealthHealthy
			state.openUntil = time.Time{}
		}
		return
	}
	state.errorRate = ewma(state.errorRate, 1, costs.FastAlpha)
	state.consecutiveFailures++
	switch {
	case state.healthState == HealthProbation:
		state.healthState = HealthOpen
		state.openUntil = time.Now().Add(openWindow)
		state.consecutiveFailures = 0
	case state.consecutiveFailures >= failureThreshold || state.errorRate >= costs.ErrorRateOpen:
		state.healthState = HealthOpen
		state.openUntil = time.Now().Add(openWindow)
		state.consecutiveFailures = 0
	case state.errorRate >= costs.ErrorRateDegraded:
		state.healthState = HealthDegraded
	}
}

func countsAsUpstreamHealthFailure(outcome Outcome) bool {
	if outcome.Success || outcome.ClientCanceled {
		return false
	}
	if outcome.StatusCode == 0 {
		return true
	}
	if outcome.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if outcome.StatusCode == http.StatusRequestTimeout ||
		outcome.StatusCode == http.StatusBadGateway ||
		outcome.StatusCode == http.StatusServiceUnavailable ||
		outcome.StatusCode == http.StatusGatewayTimeout {
		return true
	}
	return outcome.StatusCode >= 500
}

func countsAsUpstreamTimeout(outcome Outcome) bool {
	return outcome.StatusCode == http.StatusRequestTimeout ||
		outcome.StatusCode == http.StatusGatewayTimeout ||
		(outcome.Stream && outcome.TTFTMs <= 0)
}

func (r *Router) expectedCost(target *Target, req RequestFeatures) float64 {
	target.mu.Lock()
	defer target.mu.Unlock()
	prefillCost := estimatePrefillCost(req)
	decodeCost := estimateDecodeCost(req)
	queuePressure := 0.35*norm(float64(target.inflight), 8) +
		0.35*norm(target.ttftFastMs, 1200) +
		0.20*maxFloat(0, norm(target.ttftFastMs, 1200)-norm(target.ttftSlowMs, 1200))
	decodePressure := 0.40*norm(float64(target.inflightStreaming), 6) +
		0.35*norm(target.reqLatencyFastMs, 4000) +
		0.25*maxFloat(0, norm(target.reqLatencyFastMs, 4000)-norm(target.reqLatencySlowMs, 4000))
	healthPenalty := 0.45*target.errorRate + 0.35*target.timeoutRate + 0.10*norm(float64(target.consecutiveFailures), 4)
	switch target.healthState {
	case HealthDegraded:
		healthPenalty += 0.25
	case HealthProbation:
		healthPenalty += 0.15
	case HealthOpen:
		healthPenalty += 10
	}
	occupancy := 0.45*norm(float64(target.inflight), 8) + 0.55*norm(float64(target.inflightStreaming), 6)
	capacity := math.Max(1, target.Weight*target.CapacityHint)
	cost := (queuePressure*prefillCost + decodePressure*decodeCost + occupancy + healthPenalty) / capacity
	if cost < r.costs.MinCostFloor {
		return r.costs.MinCostFloor
	}
	return cost
}

func estimatePrefillCost(req RequestFeatures) float64 {
	cost := 0.70*norm(req.EstPromptTokens, 512) + 0.30*norm(float64(req.RequestBytes), 4096)
	if req.HasTools {
		cost += 0.2
	}
	return maxFloat(cost, 0.05)
}

func estimateDecodeCost(req RequestFeatures) float64 {
	streamFlag := 0.0
	if req.Stream {
		streamFlag = 1.0
	}
	structuredPenalty := 0.0
	if req.HasStructuredOutput {
		structuredPenalty = 0.2
	}
	cost := 0.70*norm(req.MaxTokens, 512) + 0.20*streamFlag + 0.10*structuredPenalty
	return maxFloat(cost, 0.05)
}

func compareScore(a *Target, scoreA float64, b *Target, scoreB float64) int {
	if scoreA < scoreB {
		return -1
	}
	if scoreA > scoreB {
		return 1
	}
	if a.Priority > b.Priority {
		return -1
	}
	if a.Priority < b.Priority {
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}

// supportsPath reports whether the target can serve rawPath for the model named
// in features. The API-surface check is per-model so that a model which only
// declares one protocol surface is not selected for the other one.
func supportsPath(target *Target, rawPath string, features RequestFeatures) bool {
	if target == nil {
		return false
	}
	semantics := llm.ClassifyPath(rawPath, "")
	if !supportsProtocolFamily(target.Upstream.ProtocolFamily, semantics.Provider, semantics.Endpoint) {
		return false
	}
	if !supportsAPISurface(target.Upstream, semantics.Endpoint, features.ModelName) {
		return false
	}
	_, err := llm.AdapterFor(semantics.Provider, semantics.Endpoint)
	return err == nil
}

func supportsAPISurface(resolved upstream.ResolvedUpstream, endpoint string, model string) bool {
	return resolved.SupportsEndpointForModel(endpoint, model)
}

func supportsRequestFeatures(target *Target, features RequestFeatures) bool {
	if target == nil {
		return false
	}
	if features.HasTools && !target.Upstream.SupportsToolCallingForModel(features.ModelName) {
		return false
	}
	return true
}

func supportsProtocolFamily(protocolFamily string, provider string, endpoint string) bool {
	switch protocolFamily {
	case upstream.ProtocolFamilyAnthropicMessages:
		return provider == llm.ProviderAnthropic || endpoint == "/v1/models"
	case upstream.ProtocolFamilyGoogleGenAI:
		return provider == llm.ProviderGoogleGenAI
	case upstream.ProtocolFamilyVertexNative:
		return provider == llm.ProviderVertexNative
	case upstream.ProtocolFamilyOpenAICompatible, "":
		return llm.IsOpenAICompatibleProvider(provider)
	default:
		return false
	}
}

func requestModel(rawPath string, body []byte) string {
	if parsed, err := llm.ParseRequestForPath(rawPath, "", body); err == nil && strings.TrimSpace(parsed.Model) != "" {
		return strings.TrimSpace(parsed.Model)
	}
	if inferred := llm.ModelFromPath(rawPath); inferred != "" {
		return inferred
	}
	if llm.NormalizeEndpoint(rawPath) == "/v1/models" {
		return "list_models"
	}
	return ""
}

func extractRequestFeatures(rawPath string, body []byte) RequestFeatures {
	features := RequestFeatures{
		ModelName:       requestModel(rawPath, body),
		RequestBytes:    int64(len(body)),
		EstPromptTokens: maxFloat(float64(len(body))/4.0, 1),
		MaxTokens:       256,
	}
	if len(body) == 0 {
		return features
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return features
	}
	if stream, ok := payload["stream"].(bool); ok {
		features.Stream = stream
	}
	if tools, ok := payload["tools"].([]any); ok && len(tools) > 0 {
		features.HasTools = true
	}
	if _, ok := payload["response_format"]; ok {
		features.HasStructuredOutput = true
	}
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if value, ok := parseNumber(payload[key]); ok && value > 0 {
			features.MaxTokens = value
			break
		}
	}
	return features
}

func parseNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func inferConfiguredModel(resolved upstream.ResolvedUpstream) string {
	model := llm.ModelFromPath("/" + strings.Trim(resolved.ModelResource, "/"))
	if model != "" {
		return model
	}
	return ""
}

func readAndRestoreBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}

func normalizeModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	slices.Sort(out)
	return out
}

func normalizeModelAliases(aliases map[string]string) map[string]string {
	if len(aliases) == 0 {
		return nil
	}
	out := map[string]string{}
	for alias, targetModel := range aliases {
		alias = strings.ToLower(strings.TrimSpace(alias))
		targetModel = strings.ToLower(strings.TrimSpace(targetModel))
		if alias == "" || targetModel == "" {
			continue
		}
		out[alias] = targetModel
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func targetID(cfg config.UpstreamTargetConfig, idx int) string {
	if id := strings.TrimSpace(cfg.ID); id != "" {
		return id
	}
	return fmt.Sprintf("upstream-%d", idx+1)
}

func explicitCredentials(credentials []config.CredentialConfig) []config.CredentialConfig {
	out := make([]config.CredentialConfig, 0, len(credentials))
	for _, credential := range credentials {
		if strings.TrimSpace(credential.ID) == "" && strings.TrimSpace(credential.Name) == "" && strings.TrimSpace(credential.ApiKey) == "" {
			continue
		}
		out = append(out, credential)
	}
	return out
}

func credentialID(credential config.CredentialConfig, idx int) string {
	if id := strings.TrimSpace(credential.ID); id != "" {
		return id
	}
	if name := strings.TrimSpace(credential.Name); name != "" {
		return slugID(name)
	}
	return fmt.Sprintf("credential-%d", idx+1)
}

func routeTargetID(channelID string, credentialID string) string {
	channelID = strings.TrimSpace(channelID)
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		credentialID = "default"
	}
	return channelID + ":" + credentialID
}

func credentialHint(credential config.CredentialConfig, resolved upstream.ResolvedUpstream) string {
	if id := strings.TrimSpace(credential.ID); id != "" {
		return id
	}
	if name := strings.TrimSpace(credential.Name); name != "" {
		return name
	}
	return credentialHintFromUpstream(resolved)
}

func credentialHintFromUpstream(resolved upstream.ResolvedUpstream) string {
	return "default"
}

func slugID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizePolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", PolicyP2C:
		return PolicyP2C
	case PolicyFirstAvailable:
		return PolicyFirstAvailable
	default:
		return PolicyP2C
	}
}

func normalizeFallback(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", FallbackReject:
		return FallbackReject
	default:
		return strings.ToLower(strings.TrimSpace(policy))
	}
}

func normalizeDiscoveryMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModelDiscoveryListModels:
		return ModelDiscoveryListModels
	case ModelDiscoveryStaticOnly:
		return ModelDiscoveryStaticOnly
	case ModelDiscoveryDisabled:
		return ModelDiscoveryDisabled
	default:
		return ModelDiscoveryListModels
	}
}

func allowUnknownModels(cfg config.UpstreamTargetConfig, fallback bool) bool {
	if cfg.AllowUnknownModels != nil {
		return *cfg.AllowUnknownModels
	}
	return fallback
}

func defaultFloat(v float64, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	return v
}

func ewma(old, sample, alpha float64) float64 {
	if sample < 0 {
		return old
	}
	if old <= 0 {
		return sample
	}
	return alpha*sample + (1-alpha)*old
}

func norm(v, scale float64) float64 {
	if scale <= 0 {
		return v
	}
	if v <= 0 {
		return 0
	}
	return v / scale
}

func ratio(a, b float64) float64 {
	if b <= 0 {
		return 1
	}
	return a / b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
