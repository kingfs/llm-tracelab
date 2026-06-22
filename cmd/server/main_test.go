package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
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

func TestRootCommandHelpWorksWithConfigShortcut(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", "config.yaml", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := out.String()
	for _, want := range []string{
		"Local-first LLM API record/replay proxy",
		"Available Commands:",
		"-c, --config string",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output = %q, want contain %q", output, want)
		}
	}
}

func TestCLIRuntimeReadsConfigFromEnv(t *testing.T) {
	t.Setenv("LLM_TRACELAB_CONFIG", "env-config.yaml")

	runtime := newCLIRuntime()
	if got := runtime.configPath(); got != "env-config.yaml" {
		t.Fatalf("configPath() = %q, want env-config.yaml", got)
	}
}

func TestRouterConfigFromChannelsUsesDatabaseTargets(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "db-channel",
		Name:           "DB Channel",
		BaseURL:        "https://db.example.com/v1",
		ProviderPreset: "openai",
		HeadersJSON:    "{}",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	if err := st.ReplaceChannelModels("db-channel", []store.ChannelModelRecord{
		{Model: "gpt-5", Source: "manual", Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = "https://yaml.example.com/v1"
	cfg.Upstream.ProviderPreset = "openai"

	routerCfg, source, err := routerConfigFromChannels(cfg, channel.NewService(st))
	if err != nil {
		t.Fatalf("routerConfigFromChannels() error = %v", err)
	}
	if source != "database" {
		t.Fatalf("source = %q, want database", source)
	}
	if len(routerCfg.Upstreams) != 1 || routerCfg.Upstreams[0].ID != "db-channel" {
		t.Fatalf("routerCfg.Upstreams = %#v", routerCfg.Upstreams)
	}
	if routerCfg.Upstream.BaseURL != "" {
		t.Fatalf("routerCfg.Upstream.BaseURL = %q, want empty", routerCfg.Upstream.BaseURL)
	}
}

func TestRouterConfigFromChannelsFallsBackToYAML(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = "https://yaml.example.com/v1"
	cfg.Upstream.ProviderPreset = "openai"

	routerCfg, source, err := routerConfigFromChannels(cfg, channel.NewService(st))
	if err != nil {
		t.Fatalf("routerConfigFromChannels() error = %v", err)
	}
	if source != "yaml" {
		t.Fatalf("source = %q, want yaml", source)
	}
	if routerCfg != cfg {
		t.Fatalf("routerConfigFromChannels should return original cfg on YAML fallback")
	}
}

func TestResponsesServerConfigFromServeConfigDefaultsDisabled(t *testing.T) {
	got := responsesServerConfigFromServeConfig(&config.Config{})

	if got.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if got.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q, want empty", got.DefaultModel)
	}
	if got.ForceStore {
		t.Fatalf("ForceStore = true, want false")
	}
	if got.MaxRequestBodyBytes != 16<<20 {
		t.Fatalf("MaxRequestBodyBytes = %d, want %d", got.MaxRequestBodyBytes, 16<<20)
	}
	if got.Path != "/v1/responses" {
		t.Fatalf("Path = %q, want /v1/responses", got.Path)
	}
	if got.FunctionExecutors.Enabled {
		t.Fatalf("FunctionExecutors.Enabled = true, want false")
	}
	if got.FunctionExecutors.MaxResultBytes != 64<<10 {
		t.Fatalf("FunctionExecutors.MaxResultBytes = %d, want %d", got.FunctionExecutors.MaxResultBytes, 64<<10)
	}
}

func TestResponsesServerConfigFromServeConfigCopiesEnabledValues(t *testing.T) {
	cfg := &config.Config{}
	cfg.ResponsesServer.Enabled = true
	cfg.ResponsesServer.DefaultModel = "qwen3"
	cfg.ResponsesServer.ForceStore = true
	cfg.ResponsesServer.MaxRequestBodyBytes = 1024
	cfg.ResponsesServer.Path = "/custom/responses"
	cfg.ResponsesServer.FunctionExecutors.Enabled = true
	cfg.ResponsesServer.FunctionExecutors.MaxResultBytes = 128

	got := responsesServerConfigFromServeConfig(cfg)

	if !got.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if got.DefaultModel != "qwen3" {
		t.Fatalf("DefaultModel = %q, want qwen3", got.DefaultModel)
	}
	if !got.ForceStore {
		t.Fatalf("ForceStore = false, want true")
	}
	if got.MaxRequestBodyBytes != 1024 {
		t.Fatalf("MaxRequestBodyBytes = %d, want 1024", got.MaxRequestBodyBytes)
	}
	if got.Path != "/custom/responses" {
		t.Fatalf("Path = %q, want /custom/responses", got.Path)
	}
	if !got.FunctionExecutors.Enabled || got.FunctionExecutors.MaxResultBytes != 128 {
		t.Fatalf("FunctionExecutors = %+v, want enabled max 128", got.FunctionExecutors)
	}
}

func TestRouterConfigFromChannelsKeepsYAMLWhenExplicitCredentialsExist(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "db-channel",
		Name:           "DB Channel",
		BaseURL:        "https://db.example.com/v1",
		ProviderPreset: "openai",
		HeadersJSON:    "{}",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}

	cfg := &config.Config{}
	cfg.Upstreams = []config.UpstreamTargetConfig{
		{
			ID: "yaml-channel",
			Upstream: config.UpstreamConfig{
				BaseURL:        "https://yaml.example.com/v1",
				ApiKey:         "$env:OPENAI_TEST_KEY",
				ProviderPreset: "openai",
			},
			Credentials: []config.CredentialConfig{
				{
					ID:     "primary",
					ApiKey: "$env:OPENAI_TEST_KEY",
				},
			},
		},
	}

	routerCfg, source, err := routerConfigFromChannels(cfg, channel.NewService(st))
	if err != nil {
		t.Fatalf("routerConfigFromChannels() error = %v", err)
	}
	if source != "yaml" {
		t.Fatalf("source = %q, want yaml", source)
	}
	if routerCfg != cfg {
		t.Fatalf("routerConfigFromChannels should return original cfg when YAML credentials are explicit")
	}
	if got := routerCfg.Upstreams[0].EffectiveCredentials()[0].ID; got != "primary" {
		t.Fatalf("credential id = %q, want primary", got)
	}
}

func TestRootCommandRegistersBaseCommands(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	for _, want := range []string{"serve", "migrate", "db", "db secret", "db secret status", "db secret export", "db secret rotate", "auth", "analyze", "analyze repair-usage", "analyze reanalyze", "version", "schema", "completion"} {
		parts := strings.Fields(want)
		found, _, err := cmd.Find(parts)
		if err != nil || found.CommandPath() != cliName+" "+want {
			t.Fatalf("root command missing %q: found=%v err=%v", want, found, err)
		}
	}
}

func TestVersionCommandSupportsJSONEnvelope(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"version", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "version" || envelope.Result.Name != cliName || envelope.Result.Version == "" {
		t.Fatalf("version envelope = %+v", envelope)
	}
}

func TestSchemaCommandSupportsJSONEnvelopeForCommandPath(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"schema", "auth", "create-token", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Contracts struct {
				Formats []string `json:"formats"`
				Stdout  string   `json:"stdout"`
				Stderr  string   `json:"stderr"`
			} `json:"contracts"`
			Commands []struct {
				Path  string `json:"path"`
				Flags []struct {
					Name string `json:"name"`
				} `json:"flags"`
			} `json:"commands"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || len(envelope.Result.Commands) != 1 {
		t.Fatalf("schema envelope = %+v", envelope)
	}
	if envelope.Result.Commands[0].Path != "llm-tracelab auth create-token" {
		t.Fatalf("schema path = %q", envelope.Result.Commands[0].Path)
	}
	if len(envelope.Result.Contracts.Formats) == 0 || envelope.Result.Contracts.Stdout == "" || envelope.Result.Contracts.Stderr == "" {
		t.Fatalf("schema contracts missing machine contract: %+v", envelope.Result.Contracts)
	}
	var foundDryRun bool
	for _, flag := range envelope.Result.Commands[0].Flags {
		if flag.Name == "dry-run" {
			foundDryRun = true
		}
	}
	if !foundDryRun {
		t.Fatalf("schema flags = %+v, want dry-run", envelope.Result.Commands[0].Flags)
	}
}

func TestAuthCreateTokenDryRunJSONDoesNotRequireDatabase(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"auth", "create-token", "--dry-run", "--format", "json", "--username", "admin", "--name", "agent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			DryRun  bool   `json:"dry_run"`
			Mutated bool   `json:"mutated"`
			Name    string `json:"name"`
			Token   string `json:"token"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || !envelope.Result.DryRun || envelope.Result.Mutated || envelope.Result.Name != "agent" || envelope.Result.Token != "" {
		t.Fatalf("dry-run envelope = %+v", envelope)
	}
}

func TestDBMigrateUpDryRunJSONUsesApplicationNamespace(t *testing.T) {
	t.Parallel()

	configPath := writePostgresDBMigrateConfig(t)
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "db", "migrate", "up", "--dry-run", "--step", "2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			DryRun    bool   `json:"dry_run"`
			Mutated   bool   `json:"mutated"`
			Driver    string `json:"driver"`
			DSN       string `json:"dsn"`
			Direction string `json:"direction"`
			Steps     int    `json:"steps"`
			All       bool   `json:"all"`
			Mode      string `json:"migration_mode"`
			Source    string `json:"migration_source"`
			Path      string `json:"migration_source_path"`
			Namespace string `json:"database_namespace"`
			Versioned bool   `json:"schema_versioned"`
			Auth      string `json:"auth_migration_scope"`
			Rollback  bool   `json:"rollback_supported"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "db.migrate.up" {
		t.Fatalf("envelope command = %+v, want db.migrate.up", envelope)
	}
	if !envelope.Result.DryRun || envelope.Result.Mutated || envelope.Result.Driver != "postgres" || envelope.Result.Direction != "up" || envelope.Result.Steps != 2 || envelope.Result.All {
		t.Fatalf("dry-run result = %+v", envelope.Result)
	}
	if envelope.Result.Mode != "versioned-sql" {
		t.Fatalf("migration_mode = %q, want versioned-sql", envelope.Result.Mode)
	}
	if envelope.Result.Source != "postgres-checked-in-sql" || envelope.Result.Path != "ent/postgres-migrations" || envelope.Result.Namespace != "application" || !envelope.Result.Versioned || envelope.Result.Auth != "excluded" || envelope.Result.Rollback {
		t.Fatalf("migration report = %+v", envelope.Result)
	}
	if strings.Contains(envelope.Result.DSN, "secret") || strings.Contains(envelope.Command, "auth") {
		t.Fatalf("db migrate dry-run leaked auth namespace or secret: command=%q dsn=%q", envelope.Command, envelope.Result.DSN)
	}
}

func TestDBMigrateStatusJSONReportsPostgresApplicationSource(t *testing.T) {
	t.Parallel()

	configPath := writePostgresDBMigrateConfig(t)
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "db", "migrate", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			DryRun      bool   `json:"dry_run"`
			Mutated     bool   `json:"mutated"`
			Driver      string `json:"driver"`
			DSN         string `json:"dsn"`
			Direction   string `json:"direction"`
			Namespace   string `json:"database_namespace"`
			Mode        string `json:"migration_mode"`
			Source      string `json:"migration_source"`
			Path        string `json:"migration_source_path"`
			Versioned   bool   `json:"schema_versioned"`
			StatusCheck string `json:"status_check"`
			Rollback    bool   `json:"rollback_supported"`
			AuthScope   string `json:"auth_migration_scope"`
			AuthCommand string `json:"auth_migration_command"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "db.migrate.status" {
		t.Fatalf("envelope command = %+v, want db.migrate.status", envelope)
	}
	if envelope.Result.DryRun || envelope.Result.Mutated || envelope.Result.Driver != "postgres" || envelope.Result.Direction != "status" {
		t.Fatalf("status result = %+v", envelope.Result)
	}
	if envelope.Result.Namespace != "application" || envelope.Result.Mode != "versioned-sql" || envelope.Result.Source != "postgres-checked-in-sql" || envelope.Result.Path != "ent/postgres-migrations" || !envelope.Result.Versioned {
		t.Fatalf("postgres status source = %+v", envelope.Result)
	}
	if envelope.Result.StatusCheck != "configuration-only" || envelope.Result.Rollback || envelope.Result.AuthScope != "excluded" || envelope.Result.AuthCommand != "auth migrate" {
		t.Fatalf("postgres status scope = %+v", envelope.Result)
	}
	if strings.Contains(envelope.Result.DSN, "secret") || strings.Contains(envelope.Command, "auth") {
		t.Fatalf("db migrate status leaked auth namespace or secret: command=%q dsn=%q", envelope.Command, envelope.Result.DSN)
	}
}

func TestDBMigrateStatusJSONReportsSQLiteFallbackSource(t *testing.T) {
	t.Parallel()

	configPath := writeSQLiteDBMigrateConfig(t)
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "db", "migrate", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			Driver    string `json:"driver"`
			Mode      string `json:"migration_mode"`
			Source    string `json:"migration_source"`
			Path      string `json:"migration_source_path"`
			Versioned bool   `json:"schema_versioned"`
			Auth      string `json:"auth_migration_scope"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "db.migrate.status" {
		t.Fatalf("envelope command = %+v, want db.migrate.status", envelope)
	}
	if envelope.Result.Driver != "sqlite" || envelope.Result.Mode != "schema-init" || envelope.Result.Source != "sqlite-startup-schema-fallback" || envelope.Result.Path != "internal/store raw DDL startup initialization" || envelope.Result.Versioned || envelope.Result.Auth != "excluded" {
		t.Fatalf("sqlite status source = %+v", envelope.Result)
	}
}

func TestDBMigrateStatusCheckDBJSONReportsSQLiteFallback(t *testing.T) {
	t.Parallel()

	configPath := writeSQLiteDBMigrateConfig(t)
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "db", "migrate", "status", "--check-db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}

	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			StatusCheck             string `json:"status_check"`
			DatabaseStatusAvailable bool   `json:"database_status_available"`
			DatabaseStatusVersioned bool   `json:"database_status_versioned"`
			DatabaseStatusDriver    string `json:"database_status_driver"`
			DatabaseStatusMessage   string `json:"database_status_message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK {
		t.Fatalf("envelope ok = false: %+v", envelope)
	}
	if envelope.Result.StatusCheck != "database" || envelope.Result.DatabaseStatusAvailable || envelope.Result.DatabaseStatusVersioned || envelope.Result.DatabaseStatusDriver != "sqlite" {
		t.Fatalf("sqlite database status = %+v", envelope.Result)
	}
	if !strings.Contains(envelope.Result.DatabaseStatusMessage, "sqlite application migration still uses store schema initialization") {
		t.Fatalf("sqlite database status message = %q", envelope.Result.DatabaseStatusMessage)
	}
}

func TestDBMigrateDownDryRunJSONCanPreviewUnsupportedRollback(t *testing.T) {
	t.Parallel()

	configPath := writePostgresDBMigrateConfig(t)
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "db", "migrate", "down", "--dry-run", "--step", "1", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			DryRun    bool   `json:"dry_run"`
			Mutated   bool   `json:"mutated"`
			Driver    string `json:"driver"`
			Direction string `json:"direction"`
			Steps     int    `json:"steps"`
			All       bool   `json:"all"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "db.migrate.down" {
		t.Fatalf("envelope command = %+v, want db.migrate.down", envelope)
	}
	if !envelope.Result.DryRun || envelope.Result.Mutated || envelope.Result.Driver != "postgres" || envelope.Result.Direction != "down" || envelope.Result.Steps != 1 || !envelope.Result.All {
		t.Fatalf("dry-run result = %+v", envelope.Result)
	}
}

func TestDBMigrateDownWithoutDryRunIsUnsupported(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if code := runAppDBMigrateWithOptions(appDBMigrateOptions{
		configPath: writePostgresDBMigrateConfig(t),
		direction:  "down",
		format:     "json",
		stdout:     &out,
	}); code != 2 {
		t.Fatalf("runAppDBMigrateWithOptions() = %d, want 2, output=%s", code, out.String())
	}
}

func TestAuthMigrateDryRunJSONKeepsAuthNamespace(t *testing.T) {
	t.Parallel()

	configPath := writePostgresDBMigrateConfig(t)
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "auth", "migrate", "up", "--dry-run", "--step", "1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}

	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			DryRun    bool   `json:"dry_run"`
			Mutated   bool   `json:"mutated"`
			Driver    string `json:"driver"`
			Direction string `json:"direction"`
			Steps     int    `json:"steps"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "auth.migrate.up" {
		t.Fatalf("envelope command = %+v, want auth.migrate.up", envelope)
	}
	if !envelope.Result.DryRun || envelope.Result.Mutated || envelope.Result.Driver != "postgres" || envelope.Result.Direction != "up" || envelope.Result.Steps != 1 {
		t.Fatalf("auth dry-run result = %+v", envelope.Result)
	}
}

func TestOpenAuthStoreAutoMigrateSQLiteCreatesAuthSchemaAndInitUser(t *testing.T) {
	dir := t.TempDir()
	autoMigrate := true
	cfg := &config.Config{}
	cfg.Trace.OutputDir = dir
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "control.sqlite3")
	cfg.Database.AutoMigrate = &autoMigrate

	st, err := openAuthStore(cfg)
	if err != nil {
		t.Fatalf("openAuthStore() error = %v", err)
	}
	defer st.Close()

	if _, err := st.CreateUser(context.Background(), "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := st.VerifyPassword(context.Background(), "admin", "correct horse battery staple"); err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
}

func TestOpenAuthStoreAutoMigratePostgresUsesVersionedMigrator(t *testing.T) {
	origMigrate := authMigrateDatabaseUp
	origOpen := authOpenDatabase
	t.Cleanup(func() {
		authMigrateDatabaseUp = origMigrate
		authOpenDatabase = origOpen
	})

	var migrated bool
	var migratedDriver string
	var migratedDSN string
	var migratedSteps int
	var openedDriver string
	var openedDSN string
	var openedMaxOpen int
	var openedMaxIdle int
	authMigrateDatabaseUp = func(driver string, dsn string, steps int) error {
		migrated = true
		migratedDriver = driver
		migratedDSN = dsn
		migratedSteps = steps
		return nil
	}
	authOpenDatabase = func(driver string, dsn string, maxOpenConns int, maxIdleConns int) (*auth.Store, error) {
		openedDriver = driver
		openedDSN = dsn
		openedMaxOpen = maxOpenConns
		openedMaxIdle = maxIdleConns
		return &auth.Store{}, nil
	}

	autoMigrate := true
	cfg := &config.Config{}
	cfg.Database.Driver = "postgresql"
	cfg.Database.DSN = "postgres://user:pass@example.invalid/llm_tracelab?sslmode=disable"
	cfg.Database.MaxOpenConns = 7
	cfg.Database.MaxIdleConns = 3
	cfg.Database.AutoMigrate = &autoMigrate

	st, err := openAuthStore(cfg)
	if err != nil {
		t.Fatalf("openAuthStore() error = %v", err)
	}
	defer st.Close()

	if !migrated {
		t.Fatalf("Postgres auto schema did not call versioned migrator")
	}
	if migratedDriver != "postgres" || migratedDSN != cfg.Database.DSN || migratedSteps != 0 {
		t.Fatalf("MigrateDatabaseUp args = driver=%q dsn=%q steps=%d", migratedDriver, migratedDSN, migratedSteps)
	}
	if openedDriver != "postgres" || openedDSN != cfg.Database.DSN || openedMaxOpen != 7 || openedMaxIdle != 3 {
		t.Fatalf("OpenDatabase args = driver=%q dsn=%q maxOpen=%d maxIdle=%d", openedDriver, openedDSN, openedMaxOpen, openedMaxIdle)
	}
}

func TestTopLevelMigrateAutoMigrateUsesApplicationDatabase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
trace:
  output_dir: "` + dir + `"
database:
  driver: sqlite
  dsn: "` + dbPath + `"
  auto_migrate: true
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out bytes.Buffer
	if code := runMigrateWithOptions(migrateOptions{
		configPath:   configPath,
		rewriteV2:    false,
		rebuildIndex: false,
		format:       "json",
		stdout:       &out,
	}); code != 0 {
		t.Fatalf("runMigrateWithOptions() = %d, want 0, output=%s", code, out.String())
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations marker error = %v", err)
	}
	if count != 0 {
		t.Fatalf("top-level migrate created auth schema_migrations table; want application database init only")
	}
}

func TestOpenApplicationDatabaseAutoMigrateFalseDoesNotCreateSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	autoMigrate := false
	cfg := &config.Config{}
	cfg.Trace.OutputDir = dir
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = dbPath
	cfg.Database.AutoMigrate = &autoMigrate

	st, err := openApplicationDatabase(cfg)
	if err != nil {
		t.Fatalf("openApplicationDatabase() error = %v", err)
	}
	defer st.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'logs'`).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master error = %v", err)
	}
	if count != 0 {
		t.Fatalf("logs table count = %d, want 0 with auto_migrate=false", count)
	}
}

func writePostgresDBMigrateConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
trace:
  output_dir: "` + dir + `"
database:
  driver: postgres
  dsn: "postgres://user:secret@127.0.0.1:15432/llm_tracelab?sslmode=disable"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return configPath
}

func writeSQLiteDBMigrateConfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
trace:
  output_dir: "` + dir + `"
database:
  driver: sqlite
  dsn: "` + filepath.Join(dir, "trace_index.sqlite3") + `"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return configPath
}

func TestDBSecretStatusAndExportCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
trace:
  output_dir: "` + dir + `"
database:
  driver: sqlite
  dsn: "` + filepath.Join(dir, "trace_index.sqlite3") + `"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var out bytes.Buffer
	if code := runDBSecretStatusWithOptions(dbSecretOptions{configPath: configPath, format: "json", stdout: &out}); code != 0 {
		t.Fatalf("runDBSecretStatusWithOptions() = %d, output=%s", code, out.String())
	}
	var statusEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Mode        string `json:"mode"`
			KeyPath     string `json:"key_path"`
			Exists      bool   `json:"exists"`
			Readable    bool   `json:"readable"`
			Fingerprint string `json:"fingerprint"`
			Error       string `json:"error"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &statusEnvelope); err != nil {
		t.Fatalf("json.Unmarshal(status) error = %v, output=%q", err, out.String())
	}
	if !statusEnvelope.OK || statusEnvelope.Result.Mode != "encrypted-local" || !statusEnvelope.Result.Exists || !statusEnvelope.Result.Readable || statusEnvelope.Result.Fingerprint == "" || statusEnvelope.Result.Error != "" {
		t.Fatalf("status envelope = %+v", statusEnvelope)
	}
	if strings.Contains(out.String(), "key\":\"") {
		t.Fatalf("status output leaked exported key: %s", out.String())
	}

	exportPath := filepath.Join(dir, "secret-backup.key")
	out.Reset()
	if code := runDBSecretExportWithOptions(dbSecretOptions{configPath: configPath, format: "json", stdout: &out, outPath: exportPath}); code != 0 {
		t.Fatalf("runDBSecretExportWithOptions(file) = %d, output=%s", code, out.String())
	}
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatalf("Stat(exportPath) error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %v, want 0600", info.Mode().Perm())
	}
	exported, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("ReadFile(exportPath) error = %v", err)
	}
	if len(strings.TrimSpace(string(exported))) == 0 {
		t.Fatalf("exported key is empty")
	}
	if strings.Contains(out.String(), strings.TrimSpace(string(exported))) {
		t.Fatalf("file export result leaked key: %s", out.String())
	}

	out.Reset()
	if code := runDBSecretExportWithOptions(dbSecretOptions{configPath: configPath, format: "json", stdout: &out}); code != 0 {
		t.Fatalf("runDBSecretExportWithOptions(stdout json) = %d, output=%s", code, out.String())
	}
	var exportEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Key string `json:"key"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &exportEnvelope); err != nil {
		t.Fatalf("json.Unmarshal(export) error = %v, output=%q", err, out.String())
	}
	if !exportEnvelope.OK || exportEnvelope.Result.Key != strings.TrimSpace(string(exported)) {
		t.Fatalf("export envelope = %+v, want exported key", exportEnvelope)
	}
}

func TestDBSecretRotateCommand(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
trace:
  output_dir: "` + dir + `"
database:
  driver: sqlite
  dsn: "` + filepath.Join(dir, "trace_index.sqlite3") + `"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	st, err := store.NewWithDatabase(dir, "sqlite", filepath.Join(dir, "trace_index.sqlite3"), 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:               "openai-primary",
		Name:             "OpenAI Primary",
		BaseURL:          "https://api.openai.com/v1",
		ProviderPreset:   "openai",
		APIKeyCiphertext: []byte("sk-cli-rotate"),
		HeadersJSON:      `{"Authorization":"Bearer cli","X-Test":"visible"}`,
		Enabled:          true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	before := st.SecretStatus()
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var out bytes.Buffer
	if code := runDBSecretRotateWithOptions(dbSecretOptions{configPath: configPath, format: "text", stdout: &out}); code != 2 {
		t.Fatalf("runDBSecretRotateWithOptions(no yes) = %d, output=%s", code, out.String())
	}

	out.Reset()
	if code := runDBSecretRotateWithOptions(dbSecretOptions{configPath: configPath, format: "json", stdout: &out, yes: true}); code != 0 {
		t.Fatalf("runDBSecretRotateWithOptions() = %d, output=%s", code, out.String())
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			OldFingerprint string `json:"old_fingerprint"`
			NewFingerprint string `json:"new_fingerprint"`
			BackupPath     string `json:"backup_path"`
			ChannelCount   int    `json:"channel_count"`
			APIKeyCount    int    `json:"api_key_count"`
			HeaderCount    int    `json:"header_count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal(rotate) error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Result.OldFingerprint != before.Fingerprint || envelope.Result.NewFingerprint == "" || envelope.Result.NewFingerprint == envelope.Result.OldFingerprint {
		t.Fatalf("rotate envelope = %+v, before=%+v", envelope, before)
	}
	if envelope.Result.ChannelCount != 1 || envelope.Result.APIKeyCount != 1 || envelope.Result.HeaderCount != 1 || envelope.Result.BackupPath == "" {
		t.Fatalf("rotate counts = %+v", envelope.Result)
	}
	if _, err := os.Stat(envelope.Result.BackupPath); err != nil {
		t.Fatalf("Stat(backupPath) error = %v", err)
	}

	reopened, err := store.NewWithDatabase(dir, "sqlite", filepath.Join(dir, "trace_index.sqlite3"), 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(reopen) error = %v", err)
	}
	defer reopened.Close()
	record, err := reopened.GetChannelConfig("openai-primary")
	if err != nil {
		t.Fatalf("GetChannelConfig() error = %v", err)
	}
	if string(record.APIKeyCiphertext) != "sk-cli-rotate" || record.HeadersJSON != `{"Authorization":"Bearer cli","X-Test":"visible"}` {
		t.Fatalf("record after rotate = api_key %q headers %q", string(record.APIKeyCiphertext), record.HeadersJSON)
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

func TestProviderProbeJSONReportsSuggestedAPISurface(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model is required"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/responses":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"input is required"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstreamServer.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
upstreams:
  - id: "openai-local"
    upstream:
      base_url: "` + upstreamServer.URL + `/api/v1"
      api_key: "secret-probe-key"
      api_type: "chat_completions"
      protocol_family: "openai_compatible"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "provider", "probe", "--id", "openai-local", "--timeout", "2s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}
	if strings.Contains(out.String(), "secret-probe-key") {
		t.Fatalf("provider probe output leaked api key: %s", out.String())
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			Reports []struct {
				ProviderID              string   `json:"provider_id"`
				Status                  string   `json:"status"`
				SuggestedAPIType        string   `json:"suggested_api_type"`
				SuggestedProtocolFamily string   `json:"suggested_protocol_family"`
				Capabilities            []string `json:"capabilities"`
				Warnings                []string `json:"warnings"`
			} `json:"reports"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "provider.probe" || len(envelope.Result.Reports) != 1 {
		t.Fatalf("probe envelope = %+v", envelope)
	}
	report := envelope.Result.Reports[0]
	if report.ProviderID != "openai-local" || report.Status != "detected" {
		t.Fatalf("probe report identity/status = %+v", report)
	}
	if report.SuggestedAPIType != "responses" || report.SuggestedProtocolFamily != "openai_compatible" {
		t.Fatalf("probe suggestion = %+v", report)
	}
	if !hasString(report.Capabilities, "responses") || !hasString(report.Capabilities, "chat_completions") || !hasString(report.Capabilities, "models") {
		t.Fatalf("probe capabilities = %+v", report.Capabilities)
	}
	if !containsStringFragment(report.Warnings, "specified api_type") {
		t.Fatalf("probe warnings = %+v, want api_type mismatch warning", report.Warnings)
	}
}

func TestProviderProbeReportJSONReportsConfiguredUpstreams(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model is required"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstreamServer.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configBody := `
upstreams:
  - id: "openai-report"
    upstream:
      base_url: "` + upstreamServer.URL + `/v1"
      api_key: "secret-report-key"
      api_type: "responses"
      protocol_family: "openai_compatible"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "provider", "probe-report", "--timeout", "2s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%s", err, out.String())
	}
	if strings.Contains(out.String(), "secret-report-key") {
		t.Fatalf("provider probe-report output leaked api key: %s", out.String())
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Result  struct {
			Reports []struct {
				TargetSource            string   `json:"target_source"`
				ProviderID              string   `json:"provider_id"`
				Status                  string   `json:"status"`
				SuggestedAPIType        string   `json:"suggested_api_type"`
				SuggestedProtocolFamily string   `json:"suggested_protocol_family"`
				Capabilities            []string `json:"capabilities"`
				Warnings                []string `json:"warnings"`
			} `json:"reports"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Command != "provider.probe_report" || len(envelope.Result.Reports) != 1 {
		t.Fatalf("probe-report envelope = %+v", envelope)
	}
	report := envelope.Result.Reports[0]
	if report.TargetSource != "upstream" || report.ProviderID != "openai-report" || report.Status != "detected" {
		t.Fatalf("probe-report identity/status = %+v", report)
	}
	if report.SuggestedAPIType != "chat_completions" || report.SuggestedProtocolFamily != "openai_compatible" {
		t.Fatalf("probe-report suggestion = %+v", report)
	}
	if !hasString(report.Capabilities, "chat_completions") || !hasString(report.Capabilities, "models") {
		t.Fatalf("probe-report capabilities = %+v", report.Capabilities)
	}
	if !containsStringFragment(report.Warnings, "specified api_type") {
		t.Fatalf("probe-report warnings = %+v, want api_type mismatch warning", report.Warnings)
	}
}

func TestStartupProviderProbeDefaultDisabledDoesNotProbe(t *testing.T) {
	var calls int
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.Upstreams = []config.UpstreamTargetConfig{
		{
			ID: "local",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamServer.URL + "/api/v1",
			},
		},
	}

	if err := applyStartupProviderProbeSuggestions(context.Background(), cfg, upstreamServer.Client()); err != nil {
		t.Fatalf("applyStartupProviderProbeSuggestions() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("startup probe made %d HTTP calls with default config, want 0", calls)
	}
	if cfg.Upstreams[0].Upstream.APIType != "" || cfg.Upstreams[0].Upstream.ProtocolFamily != "" {
		t.Fatalf("upstream config was modified while disabled: %+v", cfg.Upstreams[0].Upstream)
	}
}

func TestStartupProviderProbeFillsMissingAPISurface(t *testing.T) {
	upstreamServer := newOpenAIProbeTestServer(t)
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.ProviderProbe.StartupFill = true
	cfg.Upstreams = []config.UpstreamTargetConfig{
		{
			ID: "local",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamServer.URL + "/api/v1",
			},
		},
	}

	if err := applyStartupProviderProbeSuggestions(context.Background(), cfg, upstreamServer.Client()); err != nil {
		t.Fatalf("applyStartupProviderProbeSuggestions() error = %v", err)
	}
	got := cfg.Upstreams[0].Upstream
	if got.ProtocolFamily != upstream.ProtocolFamilyOpenAICompatible {
		t.Fatalf("ProtocolFamily = %q, want %q", got.ProtocolFamily, upstream.ProtocolFamilyOpenAICompatible)
	}
	if got.APIType != upstream.APITypeResponses {
		t.Fatalf("APIType = %q, want %q", got.APIType, upstream.APITypeResponses)
	}
	if got.Capabilities.Responses == nil || !*got.Capabilities.Responses {
		t.Fatalf("capabilities.responses = %v, want true", got.Capabilities.Responses)
	}
	if got.Capabilities.ChatCompletions == nil || !*got.Capabilities.ChatCompletions {
		t.Fatalf("capabilities.chat_completions = %v, want true", got.Capabilities.ChatCompletions)
	}
	if got.Capabilities.Models == nil || !*got.Capabilities.Models {
		t.Fatalf("capabilities.models = %v, want true", got.Capabilities.Models)
	}
}

func TestStartupProviderProbeFailureDoesNotBlockStartup(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstreamServer.Close()

	cfg := &config.Config{}
	cfg.ProviderProbe.StartupFill = true
	cfg.Upstreams = []config.UpstreamTargetConfig{
		{
			ID: "local",
			Upstream: config.UpstreamConfig{
				BaseURL: upstreamServer.URL + "/api/v1",
			},
		},
	}

	if err := applyStartupProviderProbeSuggestions(context.Background(), cfg, upstreamServer.Client()); err != nil {
		t.Fatalf("applyStartupProviderProbeSuggestions() error = %v, want nil best-effort startup", err)
	}
	got := cfg.Upstreams[0].Upstream
	if got.APIType != "" || got.ProtocolFamily != "" || got.Capabilities.Responses != nil || got.Capabilities.ChatCompletions != nil {
		t.Fatalf("failed startup probe modified config: %+v", got)
	}
}

func TestStartupProviderProbeDoesNotOverrideExplicitAPISurface(t *testing.T) {
	upstreamServer := newOpenAIProbeTestServer(t)
	defer upstreamServer.Close()

	responsesDisabled := false
	cfg := &config.Config{}
	cfg.ProviderProbe.StartupFill = true
	cfg.Upstreams = []config.UpstreamTargetConfig{
		{
			ID: "local",
			Upstream: config.UpstreamConfig{
				BaseURL:        upstreamServer.URL + "/api/v1",
				APIType:        upstream.APITypeChatCompletions,
				ProtocolFamily: upstream.ProtocolFamilyAnthropicMessages,
				Capabilities: config.UpstreamCapabilitiesConfig{
					Responses: &responsesDisabled,
				},
			},
		},
	}

	if err := applyStartupProviderProbeSuggestions(context.Background(), cfg, upstreamServer.Client()); err != nil {
		t.Fatalf("applyStartupProviderProbeSuggestions() error = %v", err)
	}
	got := cfg.Upstreams[0].Upstream
	if got.ProtocolFamily != upstream.ProtocolFamilyAnthropicMessages {
		t.Fatalf("ProtocolFamily = %q, want explicit %q", got.ProtocolFamily, upstream.ProtocolFamilyAnthropicMessages)
	}
	if got.APIType != upstream.APITypeChatCompletions {
		t.Fatalf("APIType = %q, want explicit %q", got.APIType, upstream.APITypeChatCompletions)
	}
	if got.Capabilities.Responses == nil || *got.Capabilities.Responses {
		t.Fatalf("capabilities.responses = %v, want explicit false", got.Capabilities.Responses)
	}
	if got.Capabilities.Models == nil || !*got.Capabilities.Models {
		t.Fatalf("capabilities.models = %v, want missing field filled true", got.Capabilities.Models)
	}
}

func newOpenAIProbeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model is required"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/responses":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"input is required"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsStringFragment(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
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

func TestRunAnalyzeReparsePersistsObservation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "llm_tracelab.sqlite3")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
database:
  dsn: "file:` + dbPath + `?mode=rwc"
upstream:
  base_url: "https://api.openai.com/v1"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	reqHead := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n"
	reqBody := `{"model":"gpt-5.1","input":"hello with sk-test_abcdefghijklmnopqrstuvwxyz"}`
	resHead := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	resBody := `{"id":"resp_1","object":"response","created_at":1741476777,"status":"completed","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-reparse",
			Time:          time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1",
			Provider:      "openai_compatible",
			Operation:     "responses",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    20,
			TTFTMs:        5,
			ClientIP:      "127.0.0.1",
			ContentLength: int64(len(reqBody)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHead)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHead)),
			ResBodyLen:   int64(len(resBody)),
		},
	}
	reqHeadWithSession := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nSession_id: sess-analysis-cli\r\n\r\n"
	header.Layout.ReqHeaderLen = int64(len(reqHeadWithSession))
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude(session) error = %v", err)
	}
	logPath := filepath.Join(dir, "trace.http")
	if err := os.WriteFile(logPath, []byte(string(prelude)+reqHeadWithSession+reqBody+"\n"+resHead+resBody), 0o644); err != nil {
		t.Fatalf("WriteFile(trace) error = %v", err)
	}

	st, err := store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	if err := st.UpsertLogWithGrouping(logPath, header, store.GroupingInfo{SessionID: "sess-analysis-cli", SessionSource: "header.session_id"}); err != nil {
		t.Fatalf("UpsertLogWithGrouping() error = %v", err)
	}
	traceID := mustTraceIDFromStore(t, st, logPath)
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var out bytes.Buffer
	code := runAnalyzeReparse(analyzeReparseOptions{
		configPath: configPath,
		traceID:    traceID,
		format:     "json",
		stdout:     &out,
	})
	if code != 0 {
		t.Fatalf("runAnalyzeReparse() = %d, want 0", code)
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			TraceID string `json:"trace_id"`
			Parser  string `json:"parser"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Result.TraceID != traceID || envelope.Result.Parser != "openai" {
		t.Fatalf("envelope = %+v, want trace id %q", envelope, traceID)
	}

	st, err = store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(reopen) error = %v", err)
	}
	defer st.Close()
	summary, err := st.GetObservationSummary(traceID)
	if err != nil {
		t.Fatalf("GetObservationSummary() error = %v", err)
	}
	if summary.Parser != "openai" || summary.Status != "parsed" {
		t.Fatalf("summary = %+v", summary)
	}
	nodes, err := st.ListSemanticNodes(traceID)
	if err != nil {
		t.Fatalf("ListSemanticNodes() error = %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("semantic nodes empty")
	}

	out.Reset()
	code = runAnalyzeScan(analyzeScanOptions{
		configPath: configPath,
		traceID:    traceID,
		format:     "json",
		stdout:     &out,
	})
	if code != 0 {
		t.Fatalf("runAnalyzeScan() = %d, want 0", code)
	}
	findings, err := st.ListFindings(traceID, store.FindingFilter{Category: "credential_leak"})
	if err != nil {
		t.Fatalf("ListFindings() error = %v", err)
	}
	if len(findings) != 1 || findings[0].EvidencePath == "" || findings[0].NodeID == "" {
		t.Fatalf("findings = %+v", findings)
	}

	out.Reset()
	code = runAnalyzeRepairUsage(analyzeRepairUsageOptions{
		configPath: configPath,
		traceID:    traceID,
		format:     "json",
		stdout:     &out,
	})
	if code != 0 {
		t.Fatalf("runAnalyzeRepairUsage() = %d, want 0", code)
	}
	var repairEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			TraceID     string `json:"trace_id"`
			TotalTokens int    `json:"total_tokens"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &repairEnvelope); err != nil {
		t.Fatalf("json.Unmarshal(repair) error = %v, output=%q", err, out.String())
	}
	if !repairEnvelope.OK || repairEnvelope.Result.TraceID != traceID || repairEnvelope.Result.TotalTokens != 2 {
		t.Fatalf("repair envelope = %+v, want total tokens 2", repairEnvelope)
	}

	out.Reset()
	code = runAnalyzeReanalyze(analyzeReanalyzeOptions{
		configPath: configPath,
		traceID:    traceID,
		reparse:    true,
		scan:       true,
		format:     "json",
		stdout:     &out,
	})
	if code != 0 {
		t.Fatalf("runAnalyzeReanalyze(trace) = %d, want 0", code)
	}
	var reanalyzeEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			TraceID string `json:"trace_id"`
			JobType string `json:"job_type"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &reanalyzeEnvelope); err != nil {
		t.Fatalf("json.Unmarshal(reanalyze trace) error = %v, output=%q", err, out.String())
	}
	if !reanalyzeEnvelope.OK || reanalyzeEnvelope.Result.TraceID != traceID || reanalyzeEnvelope.Result.JobType != "trace_reanalyze" {
		t.Fatalf("reanalyze trace envelope = %+v", reanalyzeEnvelope)
	}

	out.Reset()
	code = runAnalyzeSession(analyzeSessionOptions{
		configPath: configPath,
		sessionID:  "sess-analysis-cli",
		format:     "json",
		stdout:     &out,
	})
	if code != 0 {
		t.Fatalf("runAnalyzeSession() = %d, want 0", code)
	}
	runs, err := st.ListAnalysisRuns("sess-analysis-cli", "", "session_summary", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns() error = %v", err)
	}
	if len(runs) != 1 || !strings.Contains(runs[0].OutputJSON, `"trace_refs"`) {
		t.Fatalf("analysis runs = %+v", runs)
	}

	out.Reset()
	code = runAnalyzeReanalyze(analyzeReanalyzeOptions{
		configPath: configPath,
		sessionID:  "sess-analysis-cli",
		reparse:    true,
		scan:       true,
		format:     "json",
		stdout:     &out,
	})
	if code != 0 {
		t.Fatalf("runAnalyzeReanalyze(session) = %d, want 0", code)
	}
}

func TestAnalyzeCommandsEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "llm_tracelab.sqlite3")
	configBody := []byte(strings.TrimSpace(`
server:
  port: "8080"
monitor:
  port: ""
database:
  dsn: "file:` + dbPath + `?mode=rwc"
upstream:
  base_url: "https://api.openai.com/v1"
debug:
  output_dir: "` + dir + `"
  mask_key: false
`))
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	reqHead := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n"
	reqBody := `{"model":"gpt-5.1","input":"hello with sk-test_abcdefghijklmnopqrstuvwxyz"}`
	resHead := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	resBody := `{"id":"resp_cmd","object":"response","status":"completed","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello from cli"}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}`
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-reparse-command",
			Time:          time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1",
			Provider:      "openai_compatible",
			Operation:     "responses",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    20,
			TTFTMs:        5,
			ClientIP:      "127.0.0.1",
			ContentLength: int64(len(reqBody)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHead)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHead)),
			ResBodyLen:   int64(len(resBody)),
		},
	}
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	logPath := filepath.Join(dir, "trace-command.http")
	if err := os.WriteFile(logPath, []byte(string(prelude)+reqHead+reqBody+"\n"+resHead+resBody), 0o644); err != nil {
		t.Fatalf("WriteFile(trace) error = %v", err)
	}

	st, err := store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	if err := st.UpsertLogWithGrouping(logPath, header, store.GroupingInfo{SessionID: "sess-command-e2e", SessionSource: "test"}); err != nil {
		t.Fatalf("UpsertLogWithGrouping() error = %v", err)
	}
	traceID := mustTraceIDFromStore(t, st, logPath)
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "analyze", "reparse", "--trace-id", traceID})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, output=%q", err, out.String())
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			TraceID string `json:"trace_id"`
			Parser  string `json:"parser"`
			Status  string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, out.String())
	}
	if !envelope.OK || envelope.Result.TraceID != traceID || envelope.Result.Parser != "openai" || envelope.Result.Status != "parsed" {
		t.Fatalf("envelope = %+v", envelope)
	}

	st, err = store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(reopen) error = %v", err)
	}
	nodes, err := st.ListSemanticNodes(traceID)
	if err != nil {
		t.Fatalf("ListSemanticNodes() error = %v", err)
	}
	var foundText bool
	for _, node := range nodes {
		if string(node.Node.NormalizedType) == "text" && node.Node.Text == "hello from cli" {
			foundText = true
			break
		}
	}
	if !foundText {
		t.Fatalf("semantic text node missing in %+v", nodes)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close(reopen) error = %v", err)
	}

	cmd = newRootCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "analyze", "scan", "--trace-id", traceID})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan Execute() error = %v, output=%q", err, out.String())
	}
	var scanEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			TraceID  string `json:"trace_id"`
			Findings int    `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &scanEnvelope); err != nil {
		t.Fatalf("json.Unmarshal(scan) error = %v, output=%q", err, out.String())
	}
	if !scanEnvelope.OK || scanEnvelope.Result.TraceID != traceID || scanEnvelope.Result.Findings == 0 {
		t.Fatalf("scan envelope = %+v", scanEnvelope)
	}

	st, err = store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(scan reopen) error = %v", err)
	}
	findings, err := st.ListFindings(traceID, store.FindingFilter{Category: "credential_leak"})
	if err != nil {
		t.Fatalf("ListFindings() error = %v", err)
	}
	if len(findings) != 1 || findings[0].NodeID == "" {
		t.Fatalf("findings = %+v", findings)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close(scan reopen) error = %v", err)
	}

	cmd = newRootCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"-c", configPath, "--format", "json", "analyze", "session", "--session-id", "sess-command-e2e"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("session Execute() error = %v, output=%q", err, out.String())
	}
	var sessionEnvelope struct {
		OK     bool `json:"ok"`
		Result struct {
			SessionID   string `json:"session_id"`
			TraceCount  int    `json:"trace_count"`
			FindingRefs int    `json:"finding_refs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &sessionEnvelope); err != nil {
		t.Fatalf("json.Unmarshal(session) error = %v, output=%q", err, out.String())
	}
	if !sessionEnvelope.OK || sessionEnvelope.Result.SessionID != "sess-command-e2e" || sessionEnvelope.Result.TraceCount != 1 || sessionEnvelope.Result.FindingRefs == 0 {
		t.Fatalf("session envelope = %+v", sessionEnvelope)
	}

	st, err = store.NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(session reopen) error = %v", err)
	}
	defer st.Close()
	runs, err := st.ListAnalysisRuns("sess-command-e2e", "", "session_summary", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns() error = %v", err)
	}
	if len(runs) != 1 || !strings.Contains(runs[0].OutputJSON, `"finding_refs"`) {
		t.Fatalf("analysis runs = %+v", runs)
	}
}

func mustTraceIDFromStore(t *testing.T, st *store.Store, path string) string {
	t.Helper()
	entry, err := st.ListRecent(1)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(entry) != 1 || entry[0].LogPath != path {
		t.Fatalf("recent entries = %+v, want path %q", entry, path)
	}
	return entry[0].ID
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
	if len(tools.Tools) != 20 {
		t.Fatalf("len(tools.Tools) = %d, want 20", len(tools.Tools))
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
	if len(tools.Tools) != 20 {
		t.Fatalf("len(tools.Tools) = %d, want 20", len(tools.Tools))
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
