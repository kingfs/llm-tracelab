package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/mcpserver"
	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newManagementMux(traceStore *store.Store, rtr *router.Router, cfg *config.Config, authStore ...*auth.Store) *http.ServeMux {
	mux := http.NewServeMux()
	var authStorePtr *auth.Store
	var verifier auth.TokenVerifier
	if len(authStore) > 0 {
		authStorePtr = authStore[0]
	}
	if authStorePtr != nil {
		verifier = authStorePtr
	}
	if cfg.MCP.Enabled {
		server := mcpserver.New(traceStore, mcpserver.Options{Router: rtr})
		mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		mux.Handle(normalizeMCPPathMust(cfg.MCP.Path), auth.Middleware(mcpHandler, "llm-tracelab-mcp", verifier))
	}
	monitor.RegisterRoutes(mux, traceStore, monitor.RouteOptions{
		Router:       rtr,
		AuthVerifier: verifier,
		AuthStore:    authStorePtr,
		SessionTTL:   cfg.AuthSessionTTL(),
	})
	return mux
}

func effectiveMCPPath(cfg *config.Config) string {
	if !cfg.MCP.Enabled {
		return ""
	}
	return normalizeMCPPathMust(cfg.MCP.Path)
}

func normalizeMCPPathMust(path string) string {
	normalized, err := normalizeMCPPath(path)
	if err != nil {
		panic(err)
	}
	return normalized
}

func normalizeMCPPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/mcp", nil
	}
	if path == "/" {
		return "", fmt.Errorf("mcp.path must not be /")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "", fmt.Errorf("mcp.path must not be empty")
	}
	return path, nil
}

func logResolvedTargets(rtr *router.Router) {
	for _, target := range rtr.Targets() {
		diagnostics, err := target.Upstream.StartupDiagnostics()
		if err != nil {
			slog.Warn("Failed to build upstream startup diagnostics", "upstream_id", target.ID, "error", err)
			continue
		}
		slog.Info(
			"Resolved upstream target",
			"upstream_id", target.ID,
			"base_url", target.Upstream.BaseURL,
			"provider_preset", target.Upstream.ProviderPreset,
			"protocol_family", target.Upstream.ProtocolFamily,
			"routing_profile", target.Upstream.RoutingProfile,
			"api_version", target.Upstream.APIVersion,
			"deployment", target.Upstream.Deployment,
			"connectivity_endpoint", diagnostics.ConnectivityEndpoint,
			"connectivity_url", diagnostics.ConnectivityURL,
			"model_routing_hint", diagnostics.ModelRoutingHint,
		)
	}
}

func logResolvedUpstreamConfig(resolvedUpstream upstream.ResolvedUpstream, diagnostics upstream.StartupDiagnostics) {
	slog.Info(
		"Resolved upstream config",
		"base_url", resolvedUpstream.BaseURL,
		"provider_preset", resolvedUpstream.ProviderPreset,
		"protocol_family", resolvedUpstream.ProtocolFamily,
		"routing_profile", resolvedUpstream.RoutingProfile,
		"api_version", resolvedUpstream.APIVersion,
		"deployment", resolvedUpstream.Deployment,
		"connectivity_endpoint", diagnostics.ConnectivityEndpoint,
		"connectivity_url", diagnostics.ConnectivityURL,
		"model_routing_hint", diagnostics.ModelRoutingHint,
	)
}
