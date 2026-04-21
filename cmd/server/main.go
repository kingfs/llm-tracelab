package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/mcpserver"
	"github.com/kingfs/llm-tracelab/internal/migrate"
	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/proxy"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if len(args) == 0 {
		return runServe(args)
	}
	if args[0] == "serve" {
		return runServe(args[1:])
	}
	if args[0] == "-c" || args[0] == "--help" || args[0] == "-h" {
		return runServe(args)
	}

	switch args[0] {
	case "migrate":
		return runMigrate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", *configPath, "error", err)
		return 1
	}
	if err := validateServeConfig(cfg); err != nil {
		slog.Error("Invalid serve config", "error", err)
		return 1
	}

	slog.Info("Starting LLM Proxy...", "version", "1.0.0", "go_version", "1.23+")

	traceStore, err := store.New(cfg.Debug.OutputDir)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()

	rtr, err := router.New(cfg, traceStore)
	if err != nil {
		slog.Error("Invalid upstream config", "error", err)
		return 1
	}
	if err := rtr.Initialize(); err != nil {
		slog.Error("Failed to initialize upstream router", "error", err)
		return 1
	}
	defer rtr.Close()
	rtr.StartBackgroundRefresh()
	logResolvedTargets(rtr)

	if cfg.Monitor.Port != "" {
		go func() {
			mux := newManagementMux(traceStore, rtr, cfg)

			addr := ":" + cfg.Monitor.Port
			slog.Info("Management server started", "addr", addr, "monitor_url", "http://localhost"+addr, "mcp_path", effectiveMCPPath(cfg))
			if err := http.ListenAndServe(addr, mux); err != nil {
				slog.Error("Monitor server failed", "error", err)
			}
		}()
	}

	// 3. 初始化 Handler
	handler, err := proxy.NewHandler(cfg, traceStore, rtr)
	if err != nil {
		slog.Error("Failed to create proxy handler", "error", err)
		return 1
	}

	// 4. 启动 Server
	addr := ":" + cfg.Server.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute, // 针对 LLM 长时间推理
		WriteTimeout: 5 * time.Minute,
	}

	slog.Info("Server listening", "addr", addr, "output_dir", cfg.Debug.OutputDir)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("Server failed", "error", err)
		return 1
	}
	return 0
}

func runMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	rewriteV2 := fs.Bool("rewrite-v2", true, "Rewrite legacy V2 cassette files to V3")
	rebuildIndex := fs.Bool("rebuild-index", true, "Rebuild SQLite metadata index from cassette files")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", *configPath, "error", err)
		return 1
	}

	traceStore, err := store.New(cfg.Debug.OutputDir)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()

	result, err := migrate.Run(traceStore, migrate.Options{
		OutputDir: cfg.Debug.OutputDir,
		RewriteV2: *rewriteV2,
		RebuildDB: *rebuildIndex,
	})
	if err != nil {
		slog.Error("Migration failed", "error", err)
		return 1
	}

	slog.Info(
		"Migration finished",
		"output_dir", cfg.Debug.OutputDir,
		"scanned_files", result.ScannedFiles,
		"converted_files", result.ConvertedFiles,
		"skipped_v3_files", result.SkippedV3Files,
		"indexed_rows", result.RebuiltIndexRows,
	)
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  llm-tracelab serve -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab migrate -c config.yaml [-rewrite-v2=true] [-rebuild-index=true]")
}

func validateServeConfig(cfg *config.Config) error {
	if cfg.MCP.Enabled && cfg.Monitor.Port == "" {
		return fmt.Errorf("monitor.port is required when mcp.enabled=true")
	}
	if cfg.MCP.Enabled {
		if _, err := normalizeMCPPath(cfg.MCP.Path); err != nil {
			return err
		}
	}
	return nil
}

func newManagementMux(traceStore *store.Store, rtr *router.Router, cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	if cfg.MCP.Enabled {
		server := mcpserver.New(traceStore, mcpserver.Options{Router: rtr})
		mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		mux.Handle(normalizeMCPPathMust(cfg.MCP.Path), withMCPAuth(mcpHandler, cfg.MCP.AuthToken))
	}
	monitor.RegisterRoutes(mux, traceStore, monitor.RouteOptions{Router: rtr})
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

func withMCPAuth(next http.Handler, authToken string) http.Handler {
	expected := strings.TrimSpace(authToken)
	if expected == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if provided, ok := extractBearerToken(r.Header.Get("Authorization")); !ok || provided != expected {
			w.Header().Set("WWW-Authenticate", `Bearer realm="llm-tracelab-mcp"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractBearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		token := strings.TrimSpace(header[len("bearer "):])
		return token, token != ""
	}
	return header, true
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
