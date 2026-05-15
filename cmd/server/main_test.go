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
	if len(tools.Tools) != 17 {
		t.Fatalf("len(tools.Tools) = %d, want 17", len(tools.Tools))
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
	if len(tools.Tools) != 17 {
		t.Fatalf("len(tools.Tools) = %d, want 17", len(tools.Tools))
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
