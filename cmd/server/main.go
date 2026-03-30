package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/migrate"
	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/proxy"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
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

	slog.Info("Starting LLM Proxy...", "version", "1.0.0", "go_version", "1.23+")

	traceStore, err := store.New(cfg.Debug.OutputDir)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()

	// 2. 启动自检 (Fail Fast)
	if err := upstream.CheckConnectivity(cfg.Upstream.BaseURL, cfg.Upstream.ApiKey); err != nil {
		slog.Error("Startup self-check failed! Exiting.", "error", err)
		return 1
	}

	// --- 启动 Monitor (新增) ---
	if cfg.Monitor.Port != "" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/", monitor.ListHandler(cfg.Debug.OutputDir, traceStore))
			mux.HandleFunc("/view", monitor.DetailHandler(cfg.Debug.OutputDir))
			mux.HandleFunc("/api/detail/raw", monitor.DetailRawHandler(cfg.Debug.OutputDir))
			mux.HandleFunc("/download", monitor.DownloadHandler(cfg.Debug.OutputDir))

			addr := ":" + cfg.Monitor.Port
			slog.Info("Monitor dashboard started", "addr", addr, "url", "http://localhost"+addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				slog.Error("Monitor server failed", "error", err)
			}
		}()
	}

	// 3. 初始化 Handler
	handler, err := proxy.NewHandler(cfg, traceStore)
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
