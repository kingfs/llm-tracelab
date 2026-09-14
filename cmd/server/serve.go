package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/observeworker"
	"github.com/kingfs/llm-tracelab/internal/proxy"
	"github.com/kingfs/llm-tracelab/internal/reanalysis"
	"github.com/kingfs/llm-tracelab/internal/responses/functionexec"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	authMigrateDatabaseUp = auth.MigrateDatabaseUp
	authOpenDatabase      = auth.OpenDatabase
)

func newServeCommand(runtime *cliRuntime) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the proxy, recorder, monitor, and MCP management endpoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runServeWithConfig(runtime.configPath())
			})
		},
	}
}

func runServe(args []string) int {
	configPath, code := parseConfigPath("serve", args)
	if code != 0 {
		return code
	}
	return runServeWithConfig(configPath)
}

func parseConfigPath(name string, args []string) (string, int) {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	configPath := fs.StringP("config", "c", "config.yaml", "Path to configuration file")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(normalizeLegacyFlagArgs(args)); err != nil {
		return "", 2
	}
	return *configPath, 0
}

func runServeWithConfig(configPath string) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", configPath, "error", err)
		return 1
	}
	if err := applyStartupProviderProbeSuggestions(context.Background(), cfg, nil); err != nil {
		slog.Error("Startup provider probe failed", "error", err)
		return 1
	}
	if err := validateServeConfig(cfg); err != nil {
		slog.Error("Invalid serve config", "error", err)
		return 1
	}

	if cfg.DatabaseAutoMigrate() {
		if err := migrateApplicationDatabaseUp(cfg, 0); err != nil {
			slog.Error("Failed to migrate application database", "error", err)
			return 1
		}
	}

	slog.Info("Starting LLM Proxy...", "version", Version, "go_version", "1.25+")

	authStore, err := openAuthStore(cfg)
	if err != nil {
		slog.Error("Failed to initialize auth store", "error", err)
		return 1
	}
	defer authStore.Close()

	traceStore, err := store.NewWithDatabaseOptions(
		cfg.TraceOutputDir(),
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
		store.DatabaseOptions{
			AutoMigrate:           false,
			UseSessionSummaryRead: cfg.DatabaseUseSessionSummaryRead(),
		},
	)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()
	syncCtx, cancelSync := context.WithCancel(context.Background())
	var background sync.WaitGroup
	defer func() {
		cancelSync()
		background.Wait()
	}()
	parseWorker := observeworker.New(traceStore, observeworker.Options{Interval: 5 * time.Second, BatchSize: 10})
	background.Add(1)
	go func() {
		defer background.Done()
		parseWorker.Run(syncCtx)
	}()
	analysisWorker := reanalysis.NewWorker(traceStore, reanalysis.WorkerOptions{Interval: 5 * time.Second, BatchSize: 5})
	background.Add(1)
	go func() {
		defer background.Done()
		analysisWorker.Run(syncCtx)
	}()

	channelService := channel.NewService(traceStore)
	if imported, err := channelService.BootstrapFromConfig(cfg); err != nil {
		slog.Error("Failed to bootstrap channel config", "error", err)
		return 1
	} else if imported > 0 {
		slog.Info("Imported upstream config into channel store", "channels", imported)
	}
	routerCfg, source, err := routerConfigFromChannels(cfg, channelService)
	if err != nil {
		slog.Error("Failed to build router config from channels", "error", err)
		return 1
	}
	slog.Info("Resolved router config source", "source", source)
	if err := validateServeRouterConfig(cfg, routerCfg); err != nil {
		// Non-fatal by design: without an eligible chat completions upstream the
		// local Responses server simply cannot serve /v1/responses, but the
		// process must still start so operators can reach the management UI and
		// fix channel configuration. Request-time routing reports the concrete
		// per-request failure when no Responses route is available.
		slog.Warn("Local Responses server has no eligible chat completions upstream; starting anyway", "error", err)
	}

	rtr, err := router.New(routerCfg, traceStore)
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

	functionExecutorManager, err := buildResponsesFunctionExecutorManager(context.Background(), cfg, traceStore)
	if err != nil {
		slog.Error("Failed to initialize responses function executor registry", "error", err)
		return 1
	}

	if cfg.Monitor.Port != "" {
		go func() {
			mux := newManagementMuxWithFunctionExecutorManager(traceStore, rtr, cfg, functionExecutorManager, authStore)

			addr := ":" + cfg.Monitor.Port
			srv := &http.Server{
				Addr:              addr,
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       30 * time.Second,
				WriteTimeout:      2 * time.Minute,
				IdleTimeout:       2 * time.Minute,
			}
			slog.Info("Management server started", "addr", addr, "monitor_url", "http://localhost"+addr, "mcp_path", effectiveMCPPath(cfg))
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("Monitor server failed", "error", err)
			}
		}()
	}

	handler, err := proxy.NewHandlerWithAuth(cfg, traceStore, rtr, authStore, functionExecutorManager)
	if err != nil {
		slog.Error("Failed to create proxy handler", "error", err)
		return 1
	}

	startTraceStoreBackgroundSync(syncCtx, traceStore, 5*time.Minute, &background)

	addr := ":" + cfg.Server.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	slog.Info("Server listening", "addr", addr, "trace_output_dir", cfg.TraceOutputDir(), "database_driver", cfg.DatabaseDriver())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("Server failed", "error", err)
		return 1
	}
	return 0
}

func buildResponsesFunctionExecutorManager(ctx context.Context, cfg *config.Config, traceStore *store.Store) (*functionexec.Manager, error) {
	base := cfg.ResponsesFunctionExecutorsConfig()
	if traceStore != nil {
		snapshot, ok, err := traceStore.LoadResponsesFunctionExecutorConfigSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			base = functionexec.ApplySafeOverlay(base, snapshot)
			slog.Info("Loaded persisted responses function executor overlay")
		}
	}
	return functionexec.NewManager(base)
}

func startTraceStoreBackgroundSync(ctx context.Context, traceStore *store.Store, interval time.Duration, wg *sync.WaitGroup) {
	if traceStore == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		run := func(reason string) {
			start := time.Now()
			if err := traceStore.Sync(); err != nil {
				slog.Warn("Trace index background sync failed", "reason", reason, "error", err)
				return
			}
			slog.Info("Trace index background sync finished", "reason", reason, "duration", time.Since(start).String())
		}

		run("startup")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run("periodic")
			}
		}
	}()
}

func openAuthStore(cfg *config.Config) (*auth.Store, error) {
	return openAuthStoreWithAutoSchema(cfg)
}

func openAuthStoreWithAutoSchema(cfg *config.Config) (*auth.Store, error) {
	driver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	switch driver {
	case "sqlite":
		if cfg.DatabaseAutoMigrate() {
			if err := authMigrateDatabaseUp(driver, cfg.DatabaseDSN(), 0); err != nil {
				return nil, fmt.Errorf("migrate database: %w", err)
			}
		}
	case "postgres":
		// Postgres auth tables are owned by the application migration set, which
		// serve applies before opening the auth store.
	default:
		return nil, fmt.Errorf("auth store driver %q is not supported yet", driver)
	}

	st, err := authOpenDatabase(
		driver,
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func normalizeAuthStoreDriver(driver string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	switch driver {
	case "":
		return "sqlite"
	case "postgresql":
		return "postgres"
	default:
		return driver
	}
}

func validateServeConfig(cfg *config.Config) error {
	switch driver := cfg.DatabaseDriver(); driver {
	case "sqlite", "postgres", "postgresql":
	default:
		return fmt.Errorf("database.driver %q is not supported yet; use sqlite or postgres", driver)
	}
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

// validateServeRouterConfig reports router-level configuration problems that
// make the local Responses server unusable. It is a preflight diagnostic, not a
// startup gate: callers log the returned error as a warning so the process still
// boots and the management UI stays reachable for reconfiguration.
func validateServeRouterConfig(cfg *config.Config, routerCfg *config.Config) error {
	if cfg != nil {
		if err := router.ValidateLocalResponsesServerBackendConfig(routerCfg); err != nil {
			return err
		}
	}
	return nil
}

func routerConfigFromChannels(cfg *config.Config, channelService *channel.Service) (*config.Config, string, error) {
	if configHasExplicitCredentials(cfg) {
		return cfg, "yaml", nil
	}
	targets, err := channelService.RuntimeTargets()
	if err != nil {
		return nil, "", err
	}
	if len(targets) == 0 {
		return cfg, "yaml", nil
	}
	routerCfg := *cfg
	routerCfg.Upstream = config.UpstreamConfig{}
	routerCfg.Upstreams = targets
	return &routerCfg, "database", nil
}

func configHasExplicitCredentials(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, target := range cfg.EffectiveUpstreams() {
		if target.HasExplicitCredentials() {
			return true
		}
	}
	return false
}
