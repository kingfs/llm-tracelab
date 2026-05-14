package channel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
)

type Store interface {
	ListChannelConfigs() ([]store.ChannelConfigRecord, error)
	GetChannelConfig(channelID string) (store.ChannelConfigRecord, error)
	UpsertChannelConfig(store.ChannelConfigRecord) (store.ChannelConfigRecord, error)
	UpdateChannelProbeStatus(channelID string, probedAt time.Time, status string, errorText string) error
	ListChannelModels(channelID string, enabledOnly bool) ([]store.ChannelModelRecord, error)
	ReplaceChannelModels(channelID string, records []store.ChannelModelRecord) error
	UpsertModelCatalog(store.ModelCatalogRecord) error
	CreateChannelProbeRun(store.ChannelProbeRunRecord) (store.ChannelProbeRunRecord, error)
	ReplaceUpstreamModels(upstreamID string, records []store.UpstreamModelRecord) error
}

type Service struct {
	store      Store
	httpClient *http.Client
}

func NewService(st Store) *Service {
	return &Service{store: st}
}

func (s *Service) WithHTTPClient(client *http.Client) *Service {
	s.httpClient = client
	return s
}

type ProbeResult struct {
	ChannelID       string
	Status          string
	Models          []string
	DiscoveredCount int
	EnabledCount    int
	Endpoint        string
	ErrorText       string
	StartedAt       time.Time
	CompletedAt     time.Time
	DurationMs      int64
}

func (s *Service) BootstrapFromConfig(cfg *config.Config) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("channel service store is required")
	}
	existing, err := s.store.ListChannelConfigs()
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil
	}

	targets := configuredUpstreams(cfg)
	imported := 0
	seenIDs := map[string]struct{}{}
	for idx, target := range targets {
		channelID := stableChannelID(target, idx)
		if _, ok := seenIDs[channelID]; ok {
			channelID = fmt.Sprintf("%s-%d", channelID, idx+1)
		}
		seenIDs[channelID] = struct{}{}

		enabled := true
		if target.Enabled != nil {
			enabled = *target.Enabled
		}
		headersJSON := "{}"
		if len(target.Upstream.Headers) > 0 {
			data, err := json.Marshal(target.Upstream.Headers)
			if err != nil {
				return imported, err
			}
			headersJSON = string(data)
		}
		_, err := s.store.UpsertChannelConfig(store.ChannelConfigRecord{
			ID:               channelID,
			Name:             defaultChannelName(channelID, target.Upstream.ProviderPreset),
			BaseURL:          target.Upstream.BaseURL,
			ProviderPreset:   target.Upstream.ProviderPreset,
			ProtocolFamily:   target.Upstream.ProtocolFamily,
			RoutingProfile:   target.Upstream.RoutingProfile,
			APIVersion:       target.Upstream.APIVersion,
			Deployment:       target.Upstream.Deployment,
			Project:          target.Upstream.Project,
			Location:         target.Upstream.Location,
			ModelResource:    target.Upstream.ModelResource,
			APIKeyCiphertext: []byte(target.Upstream.ApiKey),
			APIKeyHint:       secretHint(target.Upstream.ApiKey),
			HeadersJSON:      headersJSON,
			Enabled:          enabled,
			Priority:         target.Priority,
			Weight:           target.Weight,
			CapacityHint:     target.CapacityHint,
			ModelDiscovery:   target.ModelDiscovery,
		})
		if err != nil {
			return imported, err
		}

		models := make([]store.ChannelModelRecord, 0, len(target.StaticModels))
		for _, model := range normalizeModels(target.StaticModels) {
			models = append(models, store.ChannelModelRecord{
				ChannelID: channelID,
				Model:     model,
				Source:    "static",
				Enabled:   true,
			})
		}
		if len(models) > 0 {
			if err := s.store.ReplaceChannelModels(channelID, models); err != nil {
				return imported, err
			}
		}
		imported++
	}
	return imported, nil
}

func configuredUpstreams(cfg *config.Config) []config.UpstreamTargetConfig {
	if cfg == nil {
		return nil
	}
	if len(cfg.Upstreams) > 0 {
		return append([]config.UpstreamTargetConfig(nil), cfg.Upstreams...)
	}
	if strings.TrimSpace(cfg.Upstream.BaseURL) == "" {
		return nil
	}
	return cfg.EffectiveUpstreams()
}

func (s *Service) RuntimeTargets() ([]config.UpstreamTargetConfig, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("channel service store is required")
	}
	channels, err := s.store.ListChannelConfigs()
	if err != nil {
		return nil, err
	}
	targets := make([]config.UpstreamTargetConfig, 0, len(channels))
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		models, err := s.store.ListChannelModels(channel.ID, true)
		if err != nil {
			return nil, err
		}
		headers := map[string]string{}
		if strings.TrimSpace(channel.HeadersJSON) != "" {
			if err := json.Unmarshal([]byte(channel.HeadersJSON), &headers); err != nil {
				return nil, fmt.Errorf("decode headers for channel %q: %w", channel.ID, err)
			}
		}
		enabled := true
		target := config.UpstreamTargetConfig{
			ID:             channel.ID,
			Enabled:        &enabled,
			Priority:       channel.Priority,
			Weight:         channel.Weight,
			CapacityHint:   channel.CapacityHint,
			ModelDiscovery: channel.ModelDiscovery,
			StaticModels:   channelModelNames(models),
			Upstream: config.UpstreamConfig{
				BaseURL:        channel.BaseURL,
				ApiKey:         string(channel.APIKeyCiphertext),
				ProviderPreset: channel.ProviderPreset,
				ProtocolFamily: channel.ProtocolFamily,
				RoutingProfile: channel.RoutingProfile,
				APIVersion:     channel.APIVersion,
				Deployment:     channel.Deployment,
				Project:        channel.Project,
				Location:       channel.Location,
				ModelResource:  channel.ModelResource,
				Headers:        headers,
			},
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func (s *Service) Probe(channelID string) (ProbeResult, error) {
	if s == nil || s.store == nil {
		return ProbeResult{}, fmt.Errorf("channel service store is required")
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ProbeResult{}, fmt.Errorf("channel id is required")
	}
	startedAt := time.Now().UTC()
	result := ProbeResult{ChannelID: channelID, StartedAt: startedAt}

	channel, err := s.store.GetChannelConfig(channelID)
	if err != nil {
		return result, err
	}
	resolved, err := upstream.Resolve(upstreamConfigFromChannel(channel))
	if err != nil {
		return s.finishProbe(result, resolved, nil, err)
	}
	models, err := upstream.DiscoverModelsResolved(resolved, s.httpClient)
	return s.finishProbe(result, resolved, models, err)
}

func (s *Service) finishProbe(result ProbeResult, resolved upstream.ResolvedUpstream, models []string, probeErr error) (ProbeResult, error) {
	completedAt := time.Now().UTC()
	result.CompletedAt = completedAt
	result.DurationMs = completedAt.Sub(result.StartedAt).Milliseconds()
	if diagnostics, err := resolved.StartupDiagnostics(); err == nil {
		result.Endpoint = diagnostics.ConnectivityEndpoint
	}
	result.Models = normalizeModels(models)
	result.DiscoveredCount = len(result.Models)
	result.EnabledCount = len(result.Models)
	result.Status = "success"
	if probeErr != nil {
		result.Status = "failed"
		result.ErrorText = probeErr.Error()
	}

	channelModels := make([]store.ChannelModelRecord, 0, len(result.Models))
	upstreamModels := make([]store.UpstreamModelRecord, 0, len(result.Models))
	for _, model := range result.Models {
		channelModels = append(channelModels, store.ChannelModelRecord{
			ChannelID:   result.ChannelID,
			Model:       model,
			DisplayName: model,
			Source:      "discovered",
			Enabled:     true,
			FirstSeenAt: completedAt,
			LastSeenAt:  completedAt,
			LastProbeAt: completedAt,
		})
		upstreamModels = append(upstreamModels, store.UpstreamModelRecord{
			UpstreamID: result.ChannelID,
			Model:      model,
			Source:     "discovered",
			SeenAt:     completedAt,
		})
		if err := s.store.UpsertModelCatalog(store.ModelCatalogRecord{
			Model:       model,
			DisplayName: model,
			FirstSeenAt: completedAt,
			LastSeenAt:  completedAt,
		}); err != nil {
			return result, err
		}
	}
	if probeErr == nil {
		if err := s.store.ReplaceChannelModels(result.ChannelID, channelModels); err != nil {
			return result, err
		}
		if err := s.store.ReplaceUpstreamModels(result.ChannelID, upstreamModels); err != nil {
			return result, err
		}
	}
	if err := s.store.UpdateChannelProbeStatus(result.ChannelID, completedAt, result.Status, result.ErrorText); err != nil {
		return result, err
	}
	if _, err := s.store.CreateChannelProbeRun(store.ChannelProbeRunRecord{
		ChannelID:       result.ChannelID,
		Status:          result.Status,
		StartedAt:       result.StartedAt,
		CompletedAt:     result.CompletedAt,
		DurationMs:      result.DurationMs,
		DiscoveredCount: result.DiscoveredCount,
		EnabledCount:    result.EnabledCount,
		Endpoint:        result.Endpoint,
		ErrorText:       result.ErrorText,
	}); err != nil {
		return result, err
	}
	return result, probeErr
}

func upstreamConfigFromChannel(channel store.ChannelConfigRecord) config.UpstreamConfig {
	headers := map[string]string{}
	if strings.TrimSpace(channel.HeadersJSON) != "" {
		_ = json.Unmarshal([]byte(channel.HeadersJSON), &headers)
	}
	return config.UpstreamConfig{
		BaseURL:        channel.BaseURL,
		ApiKey:         string(channel.APIKeyCiphertext),
		ProviderPreset: channel.ProviderPreset,
		ProtocolFamily: channel.ProtocolFamily,
		RoutingProfile: channel.RoutingProfile,
		APIVersion:     channel.APIVersion,
		Deployment:     channel.Deployment,
		Project:        channel.Project,
		Location:       channel.Location,
		ModelResource:  channel.ModelResource,
		Headers:        headers,
	}
}

func stableChannelID(target config.UpstreamTargetConfig, idx int) string {
	if id := strings.TrimSpace(target.ID); id != "" {
		return slugify(id)
	}
	if idx == 0 && strings.TrimSpace(target.Upstream.BaseURL) != "" {
		return "default"
	}
	parts := []string{target.Upstream.ProviderPreset}
	if parsed, err := url.Parse(target.Upstream.BaseURL); err == nil && parsed.Host != "" {
		parts = append(parts, parsed.Host)
	}
	id := slugify(strings.Join(parts, "-"))
	if id == "" {
		return fmt.Sprintf("upstream-%d", idx+1)
	}
	return id
}

func defaultChannelName(id string, providerPreset string) string {
	if providerPreset = strings.TrimSpace(providerPreset); providerPreset != "" {
		return providerPreset
	}
	return id
}

func secretHint(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return secret
	}
	return secret[:3] + "..." + secret[len(secret)-4:]
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugRe.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	return value
}

func normalizeModels(models []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(models))
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

func channelModelNames(models []store.ChannelModelRecord) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, model.Model)
	}
	return normalizeModels(out)
}
