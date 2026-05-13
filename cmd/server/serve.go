package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/observeworker"
	"github.com/kingfs/llm-tracelab/internal/proxy"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	if err := validateServeConfig(cfg); err != nil {
		slog.Error("Invalid serve config", "error", err)
		return 1
	}

	slog.Info("Starting LLM Proxy...", "version", Version, "go_version", "1.25+")

	authStore, err := openAuthStore(cfg)
	if err != nil {
		slog.Error("Failed to initialize auth store", "error", err)
		return 1
	}
	defer authStore.Close()

	traceStore, err := store.NewWithDatabase(
		cfg.TraceOutputDir(),
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
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
	startTraceStoreBackgroundSync(syncCtx, traceStore, 5*time.Minute, &background)
	parseWorker := observeworker.New(traceStore, observeworker.Options{Interval: 5 * time.Second, BatchSize: 10})
	background.Add(1)
	go func() {
		defer background.Done()
		parseWorker.Run(syncCtx)
	}()

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
			mux := newManagementMux(traceStore, rtr, cfg, authStore)

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

	handler, err := proxy.NewHandlerWithAuth(cfg, traceStore, rtr, authStore)
	if err != nil {
		slog.Error("Failed to create proxy handler", "error", err)
		return 1
	}

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
	if cfg.DatabaseAutoMigrate() {
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), 0); err != nil {
			return nil, fmt.Errorf("migrate database: %w", err)
		}
	}
	st, err := auth.OpenDatabase(
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
	if err != nil {
		return nil, err
	}
	return st, nil
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
