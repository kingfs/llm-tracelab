package main

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

func TestLogResolvedUpstreamConfigIncludesRoutingDiagnostics(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	logResolvedUpstreamConfig(
		upstream.ResolvedUpstream{
			BaseURL:        "https://generativelanguage.googleapis.com",
			ProviderPreset: "google_genai",
			ProtocolFamily: upstream.ProtocolFamilyGoogleGenAI,
			RoutingProfile: upstream.RoutingProfileGoogleAIStudio,
		},
		upstream.StartupDiagnostics{
			ConnectivityEndpoint: "/v1beta/models",
			ConnectivityURL:      "https://generativelanguage.googleapis.com/v1beta/models",
			ModelRoutingHint:     "model is selected in the request path",
		},
	)

	output := buf.String()
	for _, want := range []string{
		"Resolved upstream config",
		"provider_preset=google_genai",
		"protocol_family=google_genai",
		"routing_profile=google_ai_studio",
		"connectivity_endpoint=/v1beta/models",
		"connectivity_url=https://generativelanguage.googleapis.com/v1beta/models",
		"model_routing_hint=\"model is selected in the request path\"",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output = %q, want contain %q", output, want)
		}
	}
}

func TestNormalizeRootArgsPreservesLegacyServeShortcut(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "empty defaults to serve", args: nil, want: []string{"serve"}},
		{name: "short config flag defaults to serve", args: []string{"-c", "config.yaml"}, want: []string{"serve", "-c", "config.yaml"}},
		{name: "long config flag defaults to serve", args: []string{"--config", "config.yaml"}, want: []string{"serve", "--config", "config.yaml"}},
		{name: "explicit command is unchanged", args: []string{"migrate", "-c", "config.yaml"}, want: []string{"migrate", "-c", "config.yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeRootArgs(tc.args)
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Fatalf("normalizeRootArgs(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestRootCommandRegistersBaseCommands(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	for _, want := range []string{"serve", "migrate", "db", "auth", "version", "completion"} {
		if found, _, err := cmd.Find([]string{want}); err != nil || found.Name() != want {
			t.Fatalf("root command missing %q: found=%v err=%v", want, found, err)
		}
	}
}

func TestRunServeLogsActionableInvalidUpstreamConfig(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
upstream:
  base_url: "https://api.anthropic.com"
  provider_preset: "anthropic"
  protocol_family: "google_genai"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	code := runServe([]string{"-c", configPath})
	if code != 1 {
		t.Fatalf("runServe() = %d, want 1", code)
	}

	output := buf.String()
	for _, want := range []string{
		"Invalid upstream config",
		`upstream.provider_preset=`,
		`anthropic_messages`,
		`google_genai`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output = %q, want contain %q", output, want)
		}
	}
}

func TestRunMigrateLogsSummary(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
upstream:
  base_url: "https://api.openai.com/v1"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	code := runMigrate([]string{"-c", configPath, "-rewrite-v2=false", "-rebuild-index=false"})
	if code != 0 {
		t.Fatalf("runMigrate() = %d, want 0", code)
	}

	output := buf.String()
	for _, want := range []string{
		"Migration finished",
		"output_dir=" + dir,
		"scanned_files=0",
		"converted_files=0",
		"skipped_v3_files=0",
		"indexed_rows=0",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output = %q, want contain %q", output, want)
		}
	}
}

func TestRunAuthInitUserAndCreateToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
auth:
  database_path: "` + filepath.Join(dir, "control.sqlite3") + `"
upstream:
  base_url: "https://api.openai.com/v1"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if code := run([]string{"auth", "init-user", "-c", configPath, "--username", "admin", "--password", "change-me-123"}); code != 0 {
		t.Fatalf("auth init-user code = %d, want 0", code)
	}
	if code := run([]string{"auth", "create-token", "-c", configPath, "--username", "admin", "--name", "test"}); code != 0 {
		t.Fatalf("auth create-token code = %d, want 0", code)
	}

	st, err := auth.Open(filepath.Join(dir, "control.sqlite3"))
	if err != nil {
		t.Fatalf("auth.Open() error = %v", err)
	}
	defer st.Close()
	if _, err := st.Login(context.Background(), "admin", "change-me-123", 0); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestUnifiedDatabaseAdoptsLegacyTraceIndexWithFileDSN(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy_trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacySchema := `
	CREATE TABLE logs (
		path TEXT PRIMARY KEY,
		trace_id TEXT NOT NULL DEFAULT '',
		mod_time_ns INTEGER NOT NULL,
		file_size INTEGER NOT NULL,
		version TEXT NOT NULL,
		request_id TEXT NOT NULL,
		recorded_at TEXT NOT NULL,
		model TEXT NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		operation TEXT NOT NULL DEFAULT '',
		endpoint TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL,
		method TEXT NOT NULL,
		status_code INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL,
		ttft_ms INTEGER NOT NULL,
		client_ip TEXT NOT NULL,
		content_length INTEGER NOT NULL,
		error_text TEXT NOT NULL,
		prompt_tokens INTEGER NOT NULL,
		completion_tokens INTEGER NOT NULL,
		total_tokens INTEGER NOT NULL,
		cached_tokens INTEGER NOT NULL,
		req_header_len INTEGER NOT NULL,
		req_body_len INTEGER NOT NULL,
		res_header_len INTEGER NOT NULL,
		res_body_len INTEGER NOT NULL,
		is_stream INTEGER NOT NULL
	);
	CREATE INDEX idx_logs_recorded_at ON logs(recorded_at DESC);
	`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("db.Exec(legacySchema) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	configPath := filepath.Join(dir, "config.yaml")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
database:
  driver: "sqlite"
  dsn: "file:` + dbPath + `?mode=rwc"
  auto_migrate: true
trace:
  output_dir: "` + dir + `"
upstream:
  base_url: "https://api.openai.com/v1"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if code := run([]string{"auth", "init-user", "-c", configPath, "--username", "admin", "--password", "change-me-123"}); code != 0 {
		t.Fatalf("auth init-user code = %d, want 0", code)
	}
	if code := runMigrate([]string{"-c", configPath, "-rewrite-v2=false", "-rebuild-index=false"}); code != 0 {
		t.Fatalf("runMigrate() = %d, want 0", code)
	}

	authStore, err := auth.OpenDatabase("sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("auth.OpenDatabase() error = %v", err)
	}
	defer authStore.Close()
	if _, err := authStore.Login(context.Background(), "admin", "change-me-123", 0); err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	traceStore, err := store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("store.NewWithDatabase() error = %v", err)
	}
	defer traceStore.Close()
	if _, err := traceStore.Stats(); err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
}

func TestRunServeRejectsMCPWithoutMonitorPort(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
mcp:
  enabled: true
upstream:
  base_url: "https://api.openai.com/v1"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	code := runServe([]string{"-c", configPath})
	if code != 1 {
		t.Fatalf("runServe() = %d, want 1", code)
	}
	if output := buf.String(); !strings.Contains(output, "monitor.port is required when mcp.enabled=true") {
		t.Fatalf("log output = %q, want monitor.port requirement", output)
	}
}

func TestNewManagementMuxServesStreamableMCP(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{}
	cfg.Monitor.Port = "8081"
	cfg.MCP.Enabled = true
	cfg.MCP.Path = "/mcp"

	httpServer := httptest.NewServer(newManagementMux(st, nil, cfg))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("session.ListTools() error = %v", err)
	}
	if len(tools.Tools) != 6 {
		t.Fatalf("len(tools.Tools) = %d, want 6", len(tools.Tools))
	}
}

func TestNewManagementMuxRejectsUnauthorizedMCP(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{}
	cfg.Monitor.Port = "8081"
	cfg.MCP.Enabled = true
	cfg.MCP.Path = "/mcp"
	authStore := newTestAuthStore(t)
	defer authStore.Close()

	httpServer := httptest.NewServer(newManagementMux(st, nil, cfg, authStore))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	_, err = client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
	}, nil)
	if err == nil {
		t.Fatalf("client.Connect() error = nil, want unauthorized error")
	}
	if !strings.Contains(err.Error(), "401") && !strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
		t.Fatalf("client.Connect() error = %v, want unauthorized", err)
	}
}

func TestNewManagementMuxServesAuthorizedStreamableMCP(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{}
	cfg.Monitor.Port = "8081"
	cfg.MCP.Enabled = true
	cfg.MCP.Path = "/mcp"
	authStore := newTestAuthStore(t)
	defer authStore.Close()
	token, err := authStore.CreateToken(context.Background(), "admin", "mcp", auth.DefaultTokenScope, time.Hour)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	httpServer := httptest.NewServer(newManagementMux(st, nil, cfg, authStore))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: authTransport{Token: token.Token}},
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("session.ListTools() error = %v", err)
	}
	if len(tools.Tools) != 6 {
		t.Fatalf("len(tools.Tools) = %d, want 6", len(tools.Tools))
	}
}

func newTestAuthStore(t *testing.T) *auth.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "auth.sqlite3")
	if err := auth.MigrateUp(dbPath, 0); err != nil {
		t.Fatalf("auth.MigrateUp() error = %v", err)
	}
	authStore, err := auth.Open(dbPath)
	if err != nil {
		t.Fatalf("auth.Open() error = %v", err)
	}
	if _, err := authStore.CreateUser(context.Background(), "admin", "change-me-123"); err != nil {
		_ = authStore.Close()
		t.Fatalf("CreateUser() error = %v", err)
	}
	return authStore
}

type authTransport struct {
	Token string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.Token)
	return http.DefaultTransport.RoundTrip(clone)
}
