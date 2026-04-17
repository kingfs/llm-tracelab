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
	stopCh           chan struct{}
	stopOnce         sync.Once
}

type Target struct {
	ID             string
	Enabled        bool
	Priority       int
	Weight         float64
	CapacityHint   float64
	ModelDiscovery string
	StaticModels   []string
	Upstream       upstream.ResolvedUpstream

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
}

type Snapshot struct {
	ID                string    `json:"id"`
	Enabled           bool      `json:"enabled"`
	Priority          int       `json:"priority"`
	Weight            float64   `json:"weight"`
	CapacityHint      float64   `json:"capacity_hint"`
	ModelDiscovery    string    `json:"model_discovery"`
	BaseURL           string    `json:"base_url"`
	ProviderPreset    string    `json:"provider_preset"`
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
}

type SelectionError struct {
	Reason  string
	Message string
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
	SelectionFailureUnknown            = "unknown"
)

func SelectionFailureReason(err error) string {
	var selectionErr *SelectionError
	if errors.As(err, &selectionErr) && strings.TrimSpace(selectionErr.Reason) != "" {
		return selectionErr.Reason
	}
	return SelectionFailureUnknown
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
	if len(targetCfgs) == 0 {
		return nil, fmt.Errorf("no upstream targets configured")
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
		stopCh:           make(chan struct{}),
	}
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

	seenIDs := map[string]struct{}{}
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

		target := &Target{
			ID:                 targetID(targetCfg, idx),
			Enabled:            enabled,
			Priority:           targetCfg.Priority,
			Weight:             defaultFloat(targetCfg.Weight, 1),
			CapacityHint:       defaultFloat(targetCfg.CapacityHint, 1),
			ModelDiscovery:     normalizeDiscoveryMode(targetCfg.ModelDiscovery),
			StaticModels:       normalizeModels(targetCfg.StaticModels),
			Upstream:           resolved,
			allowUnknownModels: len(targetCfgs) == 1,
			models:             map[string]struct{}{},
			ttftFastMs:         500,
			ttftSlowMs:         500,
			reqLatencyFastMs:   800,
			reqLatencySlowMs:   800,
			healthState:        HealthHealthy,
		}
		if _, exists := seenIDs[target.ID]; exists {
			return nil, fmt.Errorf("duplicate upstream target id %q", target.ID)
		}
		seenIDs[target.ID] = struct{}{}
		r.targets = append(r.targets, target)
	}
	if len(r.targets) == 0 {
		return nil, fmt.Errorf("no enabled upstream targets configured")
	}
	slices.SortFunc(r.targets, func(a, b *Target) int {
		if a.Priority != b.Priority {
			return b.Priority - a.Priority
		}
		return strings.Compare(a.ID, b.ID)
	})
	return r, nil
}

func (r *Router) Initialize() error {
	usable, err := r.refreshAll()
	if err != nil {
		return err
	}
	if usable == 0 {
		return fmt.Errorf("no usable upstream targets after startup discovery")
	}
	return nil
}

func (r *Router) Targets() []*Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*Target(nil), r.targets...)
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
			case <-r.stopCh:
				return
			}
		}
	}()
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
	features := extractRequestFeatures(req.URL.Path, body)
	model := features.ModelName

	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := r.candidatesForRequest(req.URL.Path, model)
	if len(candidates) == 0 {
		return nil, &SelectionError{
			Reason:  SelectionFailureNoSupportingTarget,
			Message: fmt.Sprintf("no upstream target supports model %q for endpoint %q", model, llm.NormalizeEndpoint(req.URL.Path)),
		}
	}
	available := make([]*Target, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.isOpen(time.Now()) {
			continue
		}
		available = append(available, candidate)
	}
	if len(available) == 0 {
		return nil, &SelectionError{
			Reason:  SelectionFailureAllTargetsOpen,
			Message: fmt.Sprintf("all upstream targets are temporarily unavailable for model %q", model),
		}
	}

	selected, score := r.pick(available, features)
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
	}, nil
}

func (r *Router) Complete(selection *Selection, outcome Outcome) {
	if selection == nil || selection.Target == nil {
		return
	}
	selection.Target.onFinish(selection.Request, outcome, r.costs, r.failureThreshold, r.openWindow)
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

func (r *Router) candidatesForRequest(rawPath string, model string) []*Target {
	var candidates []*Target
	if model != "" {
		for _, target := range r.modelToTargets[strings.ToLower(model)] {
			if supportsPath(target, rawPath) {
				candidates = append(candidates, target)
			}
		}
	}
	if len(candidates) > 0 {
		return candidates
	}

	var fallback []*Target
	for _, target := range r.targets {
		if !supportsPath(target, rawPath) {
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
	var usable int
	for _, target := range r.targets {
		models, status, refreshErr := r.refreshTarget(target)
		if refreshErr == nil || len(models) > 0 || target.allowUnknownModels {
			usable++
		}
		target.setRefreshResult(models, status, refreshErr)
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
	r.mu.Lock()
	r.rebuildCatalog()
	r.mu.Unlock()
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

func (t *Target) setModels(models []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.models = make(map[string]struct{}, len(models))
	for _, model := range models {
		if model != "" {
			t.models[strings.ToLower(model)] = struct{}{}
		}
	}
}

func (t *Target) setRefreshResult(models []string, status string, refreshErr error) {
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
		Enabled:           t.Enabled,
		Priority:          t.Priority,
		Weight:            t.Weight,
		CapacityHint:      t.CapacityHint,
		ModelDiscovery:    t.ModelDiscovery,
		BaseURL:           t.Upstream.BaseURL,
		ProviderPreset:    t.Upstream.ProviderPreset,
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

func (t *Target) isOpen(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.healthState == HealthOpen && !t.openUntil.IsZero() && now.After(t.openUntil) {
		t.healthState = HealthProbation
	}
	return t.healthState == HealthOpen && !t.openUntil.IsZero() && now.Before(t.openUntil)
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

func (t *Target) onFinish(req RequestFeatures, outcome Outcome, costs costConfig, failureThreshold int64, openWindow time.Duration) {
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

	if outcome.DurationMs > 0 {
		t.reqLatencyFastMs = ewma(t.reqLatencyFastMs, outcome.DurationMs, costs.FastAlpha)
		t.reqLatencySlowMs = ewma(t.reqLatencySlowMs, outcome.DurationMs, costs.SlowAlpha)
	}
	if outcome.TTFTMs > 0 {
		t.ttftFastMs = ewma(t.ttftFastMs, outcome.TTFTMs, costs.FastAlpha)
		t.ttftSlowMs = ewma(t.ttftSlowMs, outcome.TTFTMs, costs.SlowAlpha)
	}

	if outcome.Success {
		t.errorRate = ewma(t.errorRate, 0, costs.FastAlpha)
		t.timeoutRate = ewma(t.timeoutRate, 0, costs.FastAlpha)
		t.consecutiveFailures = 0
	} else {
		t.errorRate = ewma(t.errorRate, 1, costs.FastAlpha)
		t.consecutiveFailures++
		if outcome.StatusCode == http.StatusRequestTimeout || outcome.StatusCode == http.StatusGatewayTimeout || (outcome.Stream && outcome.TTFTMs <= 0) {
			t.timeoutRate = ewma(t.timeoutRate, 1, costs.FastAlpha)
		} else {
			t.timeoutRate = ewma(t.timeoutRate, 0, costs.FastAlpha)
		}
	}
	if outcome.ClientCanceled {
		t.cancelRate = ewma(t.cancelRate, 1, costs.FastAlpha)
	} else {
		t.cancelRate = ewma(t.cancelRate, 0, costs.FastAlpha)
	}

	ttftRatio := ratio(t.ttftFastMs, t.ttftSlowMs)
	switch {
	case t.consecutiveFailures >= failureThreshold || t.errorRate >= costs.ErrorRateOpen || t.timeoutRate >= costs.TimeoutRateOpen:
		t.healthState = HealthOpen
		t.openUntil = time.Now().Add(openWindow)
		t.consecutiveFailures = 0
	case t.healthState == HealthProbation && outcome.Success:
		t.healthState = HealthHealthy
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

func supportsPath(target *Target, rawPath string) bool {
	_, err := llm.AdapterForPath(rawPath, target.Upstream.BaseURL)
	return err == nil
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

func targetID(cfg config.UpstreamTargetConfig, idx int) string {
	if id := strings.TrimSpace(cfg.ID); id != "" {
		return id
	}
	return fmt.Sprintf("upstream-%d", idx+1)
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

func defaultFloat(v float64, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	return v
}

func ewma(old, sample, alpha float64) float64 {
	if sample <= 0 {
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
