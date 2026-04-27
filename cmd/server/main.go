package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/mcpserver"
	"github.com/kingfs/llm-tracelab/internal/migrate"
	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/proxy"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
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
	case "auth":
		return runAuth(args[1:])
	case "db":
		return runDB(args[1:])
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

	// 3. 初始化 Handler
	handler, err := proxy.NewHandlerWithAuth(cfg, traceStore, rtr, authStore)
	if err != nil {
		slog.Error("Failed to create proxy handler", "error", err)
		return 1
	}

	// 4. 启动 Server
	addr := ":" + cfg.Server.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // 针对 LLM 长时间推理
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
	if cfg.DatabaseAutoMigrate() {
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), 0); err != nil {
			slog.Error("Database migration failed", "error", err)
			return 1
		}
	}

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

	result, err := migrate.Run(traceStore, migrate.Options{
		OutputDir: cfg.TraceOutputDir(),
		RewriteV2: *rewriteV2,
		RebuildDB: *rebuildIndex,
	})
	if err != nil {
		slog.Error("Migration failed", "error", err)
		return 1
	}

	slog.Info(
		"Migration finished",
		"output_dir", cfg.TraceOutputDir(),
		"scanned_files", result.ScannedFiles,
		"converted_files", result.ConvertedFiles,
		"skipped_v3_files", result.SkippedV3Files,
		"indexed_rows", result.RebuiltIndexRows,
	)
	return 0
}

func runAuth(args []string) int {
	cmd := newAuthCommand()
	cmd.SetArgs(args)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		var exit cliExitError
		if errors.As(err, &exit) {
			return exit.code
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runDB(args []string) int {
	if len(args) == 0 || args[0] != "migrate" {
		fmt.Fprintln(os.Stderr, "db requires migrate up or migrate down")
		return 2
	}
	return runAuthMigrate(args[1:])
}

type cliExitError struct {
	code int
}

func (e cliExitError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "auth",
		Short:         "Manage users, tokens, and auth database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			printUsage(os.Stderr)
			return cliExitError{code: 2}
		},
	}
	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Manage auth database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "auth migrate requires up or down")
			return cliExitError{code: 2}
		},
	}
	migrateCmd.AddCommand(authCommandAdapter("up", func(args []string) int {
		return runAuthMigrate(append([]string{"up"}, args...))
	}))
	migrateCmd.AddCommand(authCommandAdapter("down", func(args []string) int {
		return runAuthMigrate(append([]string{"down"}, args...))
	}))
	cmd.AddCommand(migrateCmd)
	cmd.AddCommand(authCommandAdapter("init-user", runAuthInitUser))
	cmd.AddCommand(authCommandAdapter("reset-password", runAuthResetPassword))
	cmd.AddCommand(authCommandAdapter("create-token", runAuthCreateToken))
	return cmd
}

func authCommandAdapter(use string, run func([]string) int) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := run(args); code != 0 {
				return cliExitError{code: code}
			}
			return nil
		},
	}
}

func runAuthMigrate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth migrate requires up or down")
		return 2
	}
	direction := args[0]
	fs := flag.NewFlagSet("auth migrate "+direction, flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	steps := fs.Int("step", 0, "Apply only N migration steps")
	all := fs.Bool("all", false, "Roll back all migrations")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", *configPath, "error", err)
		return 1
	}
	switch direction {
	case "up":
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), *steps); err != nil {
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	case "down":
		if err := auth.MigrateDatabaseDown(cfg.DatabaseDriver(), cfg.DatabaseDSN(), *steps, *all); err != nil {
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown auth migrate direction %q\n", direction)
		return 2
	}
	slog.Info("Database migration finished", "driver", cfg.DatabaseDriver(), "dsn", config.RedactDSN(cfg.DatabaseDSN()), "direction", direction)
	return 0
}

func runAuthInitUser(args []string) int {
	fs := flag.NewFlagSet("auth init-user", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	username := fs.String("username", "admin", "Username")
	password := fs.String("password", "", "Password")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*password) == "" {
		fmt.Fprintln(os.Stderr, "--password is required")
		return 2
	}
	cfg, st, code := openAuthStoreForCommand(*configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	if _, err := st.CreateUser(context.Background(), *username, *password); err != nil {
		slog.Error("Create user failed", "error", err)
		return 1
	}
	slog.Info("User created", "driver", cfg.DatabaseDriver(), "username", *username)
	return 0
}

func runAuthResetPassword(args []string) int {
	fs := flag.NewFlagSet("auth reset-password", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	username := fs.String("username", "admin", "Username")
	password := fs.String("password", "", "New password")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*password) == "" {
		fmt.Fprintln(os.Stderr, "--password is required")
		return 2
	}
	cfg, st, code := openAuthStoreForCommand(*configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	if err := st.ResetPassword(context.Background(), *username, *password); err != nil {
		slog.Error("Reset password failed", "error", err)
		return 1
	}
	slog.Info("Password reset", "driver", cfg.DatabaseDriver(), "username", *username)
	return 0
}

func runAuthCreateToken(args []string) int {
	fs := flag.NewFlagSet("auth create-token", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	username := fs.String("username", "admin", "Username")
	name := fs.String("name", "cli", "Token name")
	scope := fs.String("scope", auth.DefaultTokenScope, "Token scope")
	ttl := fs.Duration("ttl", 0, "Token TTL, 0 means no expiration")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, st, code := openAuthStoreForCommand(*configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	token, err := st.CreateToken(context.Background(), *username, *name, *scope, *ttl)
	if err != nil {
		slog.Error("Create token failed", "error", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, token.Token)
	_ = cfg
	return 0
}

func openAuthStoreForCommand(configPath string) (*config.Config, *auth.Store, int) {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", configPath, "error", err)
		return nil, nil, 1
	}
	if cfg.DatabaseAutoMigrate() {
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), 0); err != nil {
			slog.Error("Database migration failed", "error", err)
			return nil, nil, 1
		}
	}
	st, err := auth.OpenDatabase(
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
	if err != nil {
		slog.Error("Open auth store failed", "error", err)
		return nil, nil, 1
	}
	return cfg, st, 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  llm-tracelab serve -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab migrate -c config.yaml [-rewrite-v2=true] [-rebuild-index=true]")
	fmt.Fprintln(w, "  llm-tracelab db migrate up|down -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab auth migrate up|down -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab auth init-user -c config.yaml --username admin --password 'change-me'")
	fmt.Fprintln(w, "  llm-tracelab auth reset-password -c config.yaml --username admin --password 'new-password'")
	fmt.Fprintln(w, "  llm-tracelab auth create-token -c config.yaml --username admin --name cli")
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
