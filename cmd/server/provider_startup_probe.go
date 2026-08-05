package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/providerprobe"
	"github.com/kingfs/llm-tracelab/internal/upstream"
)

func applyStartupProviderProbeSuggestions(ctx context.Context, cfg *config.Config, client *http.Client) error {
	if cfg == nil || !cfg.ProviderProbeStartupFillEnabled() {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.ProviderProbeTimeout()}
	}
	if len(cfg.Upstreams) == 0 {
		applyStartupProviderProbeToTarget(ctx, &config.UpstreamTargetConfig{
			ID:       "default",
			Upstream: cfg.Upstream,
		}, &cfg.Upstream, client)
		return nil
	}
	for i := range cfg.Upstreams {
		target := &cfg.Upstreams[i]
		if target.Enabled != nil && !*target.Enabled {
			continue
		}
		applyStartupProviderProbeToTarget(ctx, target, &target.Upstream, client)
	}
	return nil
}

func applyStartupProviderProbeToTarget(ctx context.Context, target *config.UpstreamTargetConfig, upstreamCfg *config.UpstreamConfig, client *http.Client) {
	if target == nil || upstreamCfg == nil || strings.TrimSpace(upstreamCfg.BaseURL) == "" {
		return
	}
	probeTarget := *target
	probeTarget.Upstream = *upstreamCfg
	report, err := providerprobe.Probe(ctx, providerProbeTargetFromConfig(probeTarget), client)
	if err != nil {
		slog.Warn("Startup provider probe failed; continuing with explicit configuration",
			"provider_id", strings.TrimSpace(probeTarget.ID),
			"error", err,
		)
		return
	}
	applyStartupProviderProbeReport(upstreamCfg, strings.TrimSpace(probeTarget.ID), report)
}

func applyStartupProviderProbeReport(upstreamCfg *config.UpstreamConfig, providerID string, report providerprobe.Report) {
	if upstreamCfg == nil || report.Status != providerprobe.StatusDetected {
		return
	}
	if strings.TrimSpace(upstreamCfg.ProtocolFamily) == "" {
		upstreamCfg.ProtocolFamily = report.SuggestedProtocolFamily
	} else if report.SuggestedProtocolFamily != "" && normalizeProbeConfigValue(upstreamCfg.ProtocolFamily) != report.SuggestedProtocolFamily {
		slog.Warn("Startup provider probe suggestion differs from explicit protocol_family",
			"provider_id", providerID,
			"configured_protocol_family", upstreamCfg.ProtocolFamily,
			"suggested_protocol_family", report.SuggestedProtocolFamily,
		)
	}
	if strings.TrimSpace(upstreamCfg.APIType) == "" {
		upstreamCfg.APIType = report.SuggestedAPIType
	} else if report.SuggestedAPIType != "" && normalizeProbeConfigValue(upstreamCfg.APIType) != report.SuggestedAPIType {
		slog.Warn("Startup provider probe suggestion differs from explicit api_type",
			"provider_id", providerID,
			"configured_api_type", upstreamCfg.APIType,
			"suggested_api_type", report.SuggestedAPIType,
		)
	}
	applyStartupProviderProbeCapabilities(&upstreamCfg.Capabilities, providerID, report.Capabilities)
}

func applyStartupProviderProbeCapabilities(capabilities *config.UpstreamCapabilitiesConfig, providerID string, suggested []string) {
	for _, capability := range suggested {
		if upstream.SetCapabilityIfUnset(capabilities, capability, true) {
			continue
		}
		if explicitlyDisabledCapability(capabilities, capability) {
			slog.Warn("Startup provider probe found capability disabled by explicit config",
				"provider_id", providerID,
				"capability", capability,
			)
		}
	}
}

func explicitlyDisabledCapability(capabilities *config.UpstreamCapabilitiesConfig, capability string) bool {
	if capabilities == nil {
		return false
	}
	resolved := upstream.ResolvedUpstream{Capabilities: *capabilities}
	enabled, configured := resolved.Capability(capability)
	return configured && !enabled
}

func normalizeProbeConfigValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
