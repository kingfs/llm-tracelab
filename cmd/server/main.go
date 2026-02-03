package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/proxy"
	"github.com/kingfs/llm-tracelab/internal/upstream"
)

func main() {
	configPath := flag.String("c", "config.yaml", "Path to configuration file")
	flag.Parse()

	// 1. 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", *configPath, "error", err)
		os.Exit(1)
	}

	// 设置全局 logger 样式
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("Starting LLM Proxy...", "version", "1.0.0", "go_version", "1.23+")

	// 2. 启动自检 (Fail Fast)：replay 模式跳过上游连通性检查
	mode := cfg.Upstream.Mode
	if mode == "" {
		mode = "proxy"
	}
	if mode == "replay" {
		if cfg.Upstream.ReplayDir == "" {
			slog.Error("Replay mode requires replay_dir to be set. Exiting.")
			os.Exit(1)
		}
		if st, err := os.Stat(cfg.Upstream.ReplayDir); err != nil || !st.IsDir() {
			slog.Error("replay_dir is not a valid directory", "replay_dir", cfg.Upstream.ReplayDir, "error", err)
			os.Exit(1)
		}
		slog.Info("Replay mode enabled, skipping upstream connectivity check", "replay_dir", cfg.Upstream.ReplayDir)
	} else {
		if err := upstream.CheckConnectivity(cfg.Upstream.BaseURL, cfg.Upstream.ApiKey); err != nil {
			slog.Error("Startup self-check failed! Exiting.", "error", err)
			os.Exit(1)
		}
	}

	// --- 启动 Monitor (新增) ---
	if cfg.Monitor.Port != "" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/", monitor.ListHandler(cfg.Debug.OutputDir))
			mux.HandleFunc("/view", monitor.DetailHandler(cfg.Debug.OutputDir))
			mux.HandleFunc("/download", monitor.DownloadHandler(cfg.Debug.OutputDir))

			addr := ":" + cfg.Monitor.Port
			slog.Info("Monitor dashboard started", "addr", addr, "url", "http://localhost"+addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				slog.Error("Monitor server failed", "error", err)
			}
		}()
	}

	// 3. 初始化 Handler
	handler, err := proxy.NewHandler(cfg)
	if err != nil {
		slog.Error("Failed to create proxy handler", "error", err)
		os.Exit(1)
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
	}
}
