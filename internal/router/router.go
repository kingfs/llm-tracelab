package router

import (
	"bytes"
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

type Router struct {
	mu               sync.RWMutex
	targets          []*Target
	modelToTargets   map[string][]*Target
	policy           string
	openWindow       time.Duration
	failureThreshold int64
	fallbackPolicy   string
	store            *store.Store
	random           *rand.Rand
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
	consecutiveFailures int64
	openUntil           time.Time
	models              map[string]struct{}
}

type Selection struct {
	Target         *Target
	Score          float64
	CandidateCount int
	Candidates     []string
}

type Outcome struct {
	Success bool
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
		store:            st,
		random:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	if r.openWindow <= 0 {
		r.openWindow = 15 * time.Second
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
	var usable int
	for _, target := range r.targets {
		models, status, refreshErr := r.refreshTarget(target)
		if refreshErr == nil || len(models) > 0 || target.allowUnknownModels {
			usable++
		}
		if len(models) > 0 {
			target.setModels(models)
		}
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
				LastRefreshAt:     time.Now().UTC(),
				LastRefreshStatus: status,
			}
			if refreshErr != nil {
				record.LastRefreshError = refreshErr.Error()
			}
			if err := r.store.UpsertUpstreamTarget(record); err != nil {
				return err
			}
			modelRecords := make([]store.UpstreamModelRecord, 0, len(models))
			for _, model := range models {
				modelRecords = append(modelRecords, store.UpstreamModelRecord{
					UpstreamID: target.ID,
					Model:      model,
					Source:     "catalog",
					SeenAt:     time.Now().UTC(),
				})
			}
			if err := r.store.ReplaceUpstreamModels(target.ID, modelRecords); err != nil {
				return err
			}
		}
	}
	r.rebuildCatalog()
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

func (r *Router) Select(req *http.Request) (*Selection, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	body, err := readAndRestoreBody(req)
	if err != nil {
		return nil, err
	}
	model := requestModel(req.URL.Path, body)

	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := r.candidatesForRequest(req.URL.Path, model)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no upstream target supports model %q for endpoint %q", model, llm.NormalizeEndpoint(req.URL.Path))
	}
	available := make([]*Target, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.isOpen(time.Now()) {
			continue
		}
		available = append(available, candidate)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("all upstream targets are temporarily unavailable for model %q", model)
	}

	selected, score := r.pick(available)
	selected.onStart()

	candidateIDs := make([]string, 0, len(available))
	for _, candidate := range available {
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	return &Selection{
		Target:         selected,
		Score:          score,
		CandidateCount: len(available),
		Candidates:     candidateIDs,
	}, nil
}

func (r *Router) Complete(selection *Selection, outcome Outcome) {
	if selection == nil || selection.Target == nil {
		return
	}
	selection.Target.onFinish(outcome.Success, r.failureThreshold, r.openWindow)
}

func (r *Router) pick(candidates []*Target) (*Target, float64) {
	if len(candidates) == 1 || r.policy == PolicyFirstAvailable {
		best := candidates[0]
		bestScore := scoreTarget(best)
		for _, candidate := range candidates[1:] {
			if compareCandidate(candidate, best) < 0 {
				best = candidate
				bestScore = scoreTarget(candidate)
			}
		}
		return best, bestScore
	}

	aIdx := r.random.Intn(len(candidates))
	bIdx := r.random.Intn(len(candidates) - 1)
	if bIdx >= aIdx {
		bIdx++
	}
	a := candidates[aIdx]
	b := candidates[bIdx]
	scoreA := scoreTarget(a)
	scoreB := scoreTarget(b)
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

func (r *Router) rebuildCatalog() {
	catalog := make(map[string][]*Target)
	for _, target := range r.targets {
		for model := range target.models {
			catalog[model] = append(catalog[model], target)
		}
	}
	for _, targets := range catalog {
		slices.SortFunc(targets, func(a, b *Target) int {
			return compareCandidate(a, b)
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

func (t *Target) isOpen(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.openUntil.IsZero() && now.Before(t.openUntil)
}

func (t *Target) onStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight++
}

func (t *Target) onFinish(success bool, failureThreshold int64, openWindow time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight > 0 {
		t.inflight--
	}
	if success {
		t.consecutiveFailures = 0
		return
	}
	t.consecutiveFailures++
	if t.consecutiveFailures >= failureThreshold {
		t.openUntil = time.Now().Add(openWindow)
		t.consecutiveFailures = 0
	}
}

func scoreTarget(target *Target) float64 {
	target.mu.Lock()
	defer target.mu.Unlock()
	capacity := math.Max(1, target.Weight*target.CapacityHint)
	return float64(target.inflight+1) / capacity
}

func compareCandidate(a, b *Target) int {
	scoreA := scoreTarget(a)
	scoreB := scoreTarget(b)
	return compareScore(a, scoreA, b, scoreB)
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
