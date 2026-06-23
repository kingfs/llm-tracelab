package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/ent/dao/tracelog"
	"github.com/kingfs/llm-tracelab/internal/appdbmigrate"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	_ "modernc.org/sqlite"
)

func TestNewConfiguresSQLiteRuntimePragmas(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	var journalMode string
	if err := st.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := st.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want at least 5000", busyTimeout)
	}

	stats := st.db.Stats()
	if stats.MaxOpenConnections != 4 {
		t.Fatalf("MaxOpenConnections = %d, want 4", stats.MaxOpenConnections)
	}
}

func TestNewInitializesResponsesStateSchema(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	if st.EntClient() == nil {
		t.Fatal("EntClient() is nil")
	}

	for _, table := range []string{"responses", "response_items", "request_audits", "execution_events", "upstream_exchanges"} {
		t.Run(table, func(t *testing.T) {
			var name string
			if err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
				t.Fatalf("query sqlite_master table %q error = %v", table, err)
			}
			if name != table {
				t.Fatalf("sqlite table = %q, want %q", name, table)
			}
		})
	}
}

func TestNewInitializesAppSettingsSchema(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	var name string
	if err := st.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, "app_settings").Scan(&name); err != nil {
		t.Fatalf("query sqlite_master app_settings error = %v", err)
	}
	if name != "app_settings" {
		t.Fatalf("sqlite table = %q, want app_settings", name)
	}
}

func TestResponsesFunctionExecutorConfigSnapshotMissing(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	got, ok, err := st.LoadResponsesFunctionExecutorConfigSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LoadResponsesFunctionExecutorConfigSnapshot() error = %v", err)
	}
	if ok {
		t.Fatalf("LoadResponsesFunctionExecutorConfigSnapshot() ok = true, want false")
	}
	if got.Enabled || got.Timeout != 0 || len(got.Executors) != 0 {
		t.Fatalf("LoadResponsesFunctionExecutorConfigSnapshot() = %+v, want zero config", got)
	}
}

func TestResponsesFunctionExecutorConfigSnapshotRoundTripSQLite(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	disabled := false
	cfg := config.ResponsesFunctionExecutorConfig{
		Enabled:        true,
		Timeout:        7 * time.Second,
		MaxResultBytes: 12345,
		Redaction: config.ResponsesFunctionRedactionConfig{
			Arguments: true,
			Output:    true,
		},
		Executors: []config.ResponsesFunctionExecutorBinding{
			{
				Name:    " echo_weather ",
				Type:    "STATIC_RESPONSE",
				Enabled: &disabled,
				Output:  map[string]any{"secret": "do-not-store-static-output"},
				Process: config.ResponsesFunctionExecutorProcessConfig{
					WorkingDir:             " /tmp ",
					RequireAbsoluteCommand: true,
					AllowedCommandDirs:     []string{" /tmp "},
					RejectRoot:             true,
				},
			},
			{
				Name:         "shell_weather",
				Type:         config.ResponsesFunctionExecutorTypeExternalCommand,
				Command:      "/usr/bin/printenv DO_NOT_STORE_COMMAND",
				Args:         []string{"do-not-store-arg"},
				Env:          map[string]string{"SECRET_ENV": "do-not-store-env"},
				EnvAllowlist: []string{"DO_NOT_STORE_ALLOWLIST"},
				Process: config.ResponsesFunctionExecutorProcessConfig{
					WorkingDir:             "/tmp",
					RequireAbsoluteCommand: true,
					AllowedCommandDirs:     []string{"/tmp", " "},
					RejectRoot:             true,
				},
			},
		},
		Warnings: []string{"do-not-store-warning"},
	}

	if err := st.SaveResponsesFunctionExecutorConfigSnapshot(context.Background(), cfg); err != nil {
		t.Fatalf("SaveResponsesFunctionExecutorConfigSnapshot() error = %v", err)
	}

	got, ok, err := st.LoadResponsesFunctionExecutorConfigSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LoadResponsesFunctionExecutorConfigSnapshot() error = %v", err)
	}
	if !ok {
		t.Fatalf("LoadResponsesFunctionExecutorConfigSnapshot() ok = false, want true")
	}
	if !got.Enabled || got.Timeout != 7*time.Second || got.MaxResultBytes != 12345 {
		t.Fatalf("loaded top-level config = %+v, want saved safe fields", got)
	}
	if !got.Redaction.Arguments || !got.Redaction.Output {
		t.Fatalf("loaded redaction = %+v, want arguments/output true", got.Redaction)
	}
	if len(got.Executors) != 2 {
		t.Fatalf("loaded executors len = %d, want 2: %+v", len(got.Executors), got.Executors)
	}
	first := got.Executors[0]
	if first.Name != "echo_weather" || first.Type != config.ResponsesFunctionExecutorTypeStaticResponse || first.Enabled == nil || *first.Enabled != false {
		t.Fatalf("loaded first executor = %+v, want sanitized safe fields", first)
	}
	if first.Output != nil || first.Command != "" || len(first.Args) != 0 || len(first.Env) != 0 || len(first.EnvAllowlist) != 0 || len(first.Warnings) != 0 || first.Available {
		t.Fatalf("loaded first executor kept sensitive/runtime fields: %+v", first)
	}
	if first.Process.WorkingDir != "/tmp" || !first.Process.RequireAbsoluteCommand || len(first.Process.AllowedCommandDirs) != 1 || first.Process.AllowedCommandDirs[0] != "/tmp" || !first.Process.RejectRoot {
		t.Fatalf("loaded first process = %+v, want safe process overlay fields", first.Process)
	}
	second := got.Executors[1]
	if second.Name != "shell_weather" || second.Type != config.ResponsesFunctionExecutorTypeExternalCommand {
		t.Fatalf("loaded second executor = %+v, want name/type", second)
	}
	if second.Command != "" || len(second.Args) != 0 || len(second.Env) != 0 || len(second.EnvAllowlist) != 0 {
		t.Fatalf("loaded second executor kept executable fields: %+v", second)
	}
	if second.Process.WorkingDir != "/tmp" || !second.Process.RequireAbsoluteCommand || len(second.Process.AllowedCommandDirs) != 1 || second.Process.AllowedCommandDirs[0] != "/tmp" || !second.Process.RejectRoot {
		t.Fatalf("loaded second process = %+v, want safe process isolation fields", second.Process)
	}
}

func TestResponsesFunctionExecutorConfigSnapshotDoesNotPersistSensitiveFields(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	cfg := config.ResponsesFunctionExecutorConfig{
		Executors: []config.ResponsesFunctionExecutorBinding{
			{
				Name:         "danger",
				Type:         config.ResponsesFunctionExecutorTypeExternalCommand,
				Output:       "sensitive-static-output",
				Command:      "/bin/echo sensitive-command",
				Args:         []string{"sensitive-arg"},
				Env:          map[string]string{"SECRET": "sensitive-env"},
				EnvAllowlist: []string{"SENSITIVE_ALLOWLIST"},
				Timeout:      time.Minute,
			},
		},
		Warnings: []string{"runtime warning should not persist"},
	}
	if err := st.SaveResponsesFunctionExecutorConfigSnapshot(context.Background(), cfg); err != nil {
		t.Fatalf("SaveResponsesFunctionExecutorConfigSnapshot() error = %v", err)
	}

	var raw string
	if err := st.db.QueryRow(`SELECT value_json FROM app_settings WHERE setting_key = ?`, responsesFunctionExecutorConfigSnapshotKey).Scan(&raw); err != nil {
		t.Fatalf("query app_settings value_json error = %v", err)
	}
	for _, secret := range []string{
		"sensitive-static-output",
		"sensitive-command",
		"sensitive-arg",
		"sensitive-env",
		"SENSITIVE_ALLOWLIST",
		"runtime warning should not persist",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("persisted snapshot contains sensitive/runtime value %q in %s", secret, raw)
		}
	}
}

func TestNewInitializesSQLiteApplicationSchemaMarker(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	var version int
	var mode string
	var source string
	if err := st.db.QueryRow(`SELECT version, mode, source FROM app_schema_status WHERE namespace = 'application'`).Scan(&version, &mode, &source); err != nil {
		t.Fatalf("query app_schema_status error = %v", err)
	}
	if version != 1 || mode != "schema-init" || source != "internal/store raw DDL startup initialization" {
		t.Fatalf("app schema marker = version %d mode %q source %q", version, mode, source)
	}
}

func TestNewWithDatabaseAcceptsSQLiteFileDSN(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "llm_tracelab.sqlite3")
	st, err := NewWithDatabase(dir, "sqlite", "file:"+dbPath+"?mode=rwc", 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	defer st.Close()
	if st.dbPath != dbPath {
		t.Fatalf("dbPath = %q, want %q", st.dbPath, dbPath)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file stat error = %v", err)
	}
}

func TestNewWithDatabaseAcceptsRelativeSQLitePath(t *testing.T) {
	t.Chdir(t.TempDir())

	st, err := NewWithDatabase("logs", "sqlite", filepath.Join("docker-data", "database.sqlite3"), 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase(relative) error = %v", err)
	}
	defer st.Close()
	if st.dbPath != filepath.Join("docker-data", "database.sqlite3") {
		t.Fatalf("dbPath = %q, want relative config path", st.dbPath)
	}
	if _, err := os.Stat(filepath.Join("docker-data", "database.sqlite3")); err != nil {
		t.Fatalf("database file stat error = %v", err)
	}
}

func TestNewWithDatabaseOptionsCanOpenWithoutMigratingSQLite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "llm_tracelab.sqlite3")
	st, err := NewWithDatabaseOptions(dir, "sqlite", dbPath, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(AutoMigrate=false) error = %v", err)
	}
	defer st.Close()

	var count int
	if err := st.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'logs'`).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master error = %v", err)
	}
	if count != 0 {
		t.Fatalf("logs table count = %d, want 0 without auto migrate", count)
	}
}

func TestNormalizeDatabaseDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		driver string
		want   string
	}{
		{name: "empty defaults sqlite", driver: "", want: "sqlite"},
		{name: "trims lowercases", driver: " SQLite ", want: "sqlite"},
		{name: "postgres", driver: "postgres", want: "postgres"},
		{name: "postgresql alias", driver: " PostgreSQL ", want: "postgres"},
		{name: "unsupported preserved", driver: "mysql", want: "mysql"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDatabaseDriver(tt.driver); got != tt.want {
				t.Fatalf("normalizeDatabaseDriver(%q) = %q, want %q", tt.driver, got, tt.want)
			}
		})
	}
}

func TestNewWithDatabaseRejectsPostgresWithoutDSN(t *testing.T) {
	t.Parallel()

	_, err := NewWithDatabase(t.TempDir(), "postgresql", "", 4, 4)
	if err == nil || !strings.Contains(err.Error(), "postgres store dsn is required") {
		t.Fatalf("NewWithDatabase(postgresql empty dsn) error = %v, want required dsn", err)
	}
}

func TestOpenStoreDatabaseAcceptsPostgresDSNWithoutConnecting(t *testing.T) {
	t.Parallel()

	db, path, entDialect, err := openStoreDatabase(t.TempDir(), "postgres", "postgres://user:pass@example.invalid/traces?sslmode=disable")
	if err != nil {
		t.Fatalf("openStoreDatabase(postgres) error = %v", err)
	}
	defer db.Close()
	if path != "postgres://user:pass@example.invalid/traces?sslmode=disable" {
		t.Fatalf("path = %q, want dsn", path)
	}
	if entDialect != "postgres" {
		t.Fatalf("entDialect = %q, want postgres", entDialect)
	}
}

func TestRebindPostgresPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "ordinary placeholder",
			query: "SELECT * FROM logs WHERE trace_id = ?",
			want:  "SELECT * FROM logs WHERE trace_id = $1",
		},
		{
			name:  "multiple placeholders",
			query: "UPDATE logs SET model = ?, status_code = ? WHERE path = ?",
			want:  "UPDATE logs SET model = $1, status_code = $2 WHERE path = $3",
		},
		{
			name:  "question mark in string literal",
			query: "SELECT '?' AS literal, path FROM logs WHERE trace_id = ?",
			want:  "SELECT '?' AS literal, path FROM logs WHERE trace_id = $1",
		},
		{
			name:  "escaped single quote in string literal",
			query: "SELECT 'can''t ?' AS literal, path FROM logs WHERE trace_id = ? AND model = ?",
			want:  "SELECT 'can''t ?' AS literal, path FROM logs WHERE trace_id = $1 AND model = $2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rebindPostgresPlaceholders(tt.query); got != tt.want {
				t.Fatalf("rebindPostgresPlaceholders() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPostgresStoreRuntimeSQLIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	recordPath := filepath.Join(dir, suffix+".http")
	if err := os.WriteFile(recordPath, []byte("# smoke\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(record) error = %v", err)
	}
	requestID := "req_" + suffix
	header := recordfile.RecordHeader{Version: "LLM_PROXY_V3"}
	header.Meta.RequestID = requestID
	header.Meta.Time = time.Now().UTC()
	header.Meta.URL = "https://api.openai.com/v1/chat/completions"
	header.Meta.Method = http.MethodPost
	header.Meta.StatusCode = http.StatusOK
	header.Layout.IsStream = true
	if err := st.UpsertLogWithGrouping(recordPath, header, GroupingInfo{}); err != nil {
		t.Fatalf("UpsertLogWithGrouping(postgres) error = %v", err)
	}
	got, err := st.GetByRequestID(requestID)
	if err != nil {
		t.Fatalf("GetByRequestID(postgres) error = %v", err)
	}
	if got.LogPath != recordPath || !got.Header.Layout.IsStream {
		t.Fatalf("postgres log round trip mismatch: path=%q stream=%v", got.LogPath, got.Header.Layout.IsStream)
	}

	runID, err := st.SaveAnalysisRun(AnalysisRunRecord{
		TraceID:         "trace_" + suffix,
		Kind:            "postgres_smoke",
		Analyzer:        "store_test",
		AnalyzerVersion: "1",
		InputRef:        "trace:" + suffix,
		OutputJSON:      "{}",
		Status:          "completed",
	})
	if err != nil {
		t.Fatalf("SaveAnalysisRun(postgres) error = %v", err)
	}
	if runID == 0 {
		t.Fatalf("SaveAnalysisRun(postgres) id = 0")
	}
	job, err := st.CreateAnalysisJob(AnalysisJobRecord{
		JobType:     "postgres_smoke",
		TargetType:  "trace",
		TargetID:    "trace_" + suffix,
		Status:      "queued",
		StepsJSON:   "[]",
		RequestJSON: "{}",
		ResultJSON:  "{}",
	})
	if err != nil {
		t.Fatalf("CreateAnalysisJob(postgres) error = %v", err)
	}
	if job.ID == 0 {
		t.Fatalf("CreateAnalysisJob(postgres) id = 0")
	}

	observationTraceID := "trace_observation_" + suffix
	if err := st.SaveObservation(observe.TraceObservation{
		TraceID:       observationTraceID,
		Provider:      "openai_compatible",
		Operation:     "chat.completions",
		Model:         "gpt-test",
		Parser:        "postgres-smoke",
		ParserVersion: "1",
		Status:        observe.ParseStatusParsed,
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{{
				ID:             "node_" + suffix,
				ProviderType:   "message",
				NormalizedType: observe.NodeMessage,
				Role:           "assistant",
				Path:           "$.choices[0].message",
				Text:           "hello postgres",
			}},
		},
	}); err != nil {
		t.Fatalf("SaveObservation(postgres) error = %v", err)
	}
	nodes, err := st.ListSemanticNodes(observationTraceID)
	if err != nil {
		t.Fatalf("ListSemanticNodes(postgres) error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Node.Text != "hello postgres" {
		t.Fatalf("postgres semantic nodes = %+v, want one saved node", nodes)
	}

	if err := st.SaveFindings(observationTraceID, []observe.Finding{{
		ID:              "finding_" + suffix,
		Category:        "postgres_smoke",
		Severity:        observe.SeverityMedium,
		Confidence:      0.75,
		Title:           "Postgres finding",
		EvidencePath:    "trace#" + observationTraceID,
		EvidenceExcerpt: "postgres finding evidence",
		Detector:        "store_test",
		DetectorVersion: "1",
	}}); err != nil {
		t.Fatalf("SaveFindings(postgres) error = %v", err)
	}
	findings, err := st.ListFindings(observationTraceID, FindingFilter{Category: "postgres_smoke"})
	if err != nil {
		t.Fatalf("ListFindings(postgres) error = %v", err)
	}
	if len(findings) != 1 || findings[0].Title != "Postgres finding" {
		t.Fatalf("postgres findings = %+v, want one saved finding", findings)
	}

	event, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: "postgres_smoke_" + suffix,
		Source:      "store_test",
		Category:    "postgres_runtime",
		Severity:    "info",
		Status:      SystemEventStatusUnread,
		Title:       "Postgres runtime smoke",
		Message:     "runtime table coverage",
		TraceID:     observationTraceID,
		DetailsJSON: json.RawMessage(`{"postgres":true}`),
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(postgres) error = %v", err)
	}
	events, err := st.ListSystemEvents(SystemEventFilter{Source: "store_test", Category: "postgres_runtime", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListSystemEvents(postgres) error = %v", err)
	}
	if events.Total == 0 || len(events.Items) == 0 || events.Items[0].ID != event.ID {
		t.Fatalf("postgres system events = %+v, want event %q", events, event.ID)
	}
}

func TestPostgresObservationReadModelsRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	now := time.Now().UTC()
	writeLog := func(requestID, status string) LogEntry {
		t.Helper()
		recordPath := filepath.Join(dir, requestID+".http")
		if err := os.WriteFile(recordPath, []byte("# readmodel\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", recordPath, err)
		}
		header := recordfile.RecordHeader{Version: "LLM_PROXY_V3"}
		header.Meta.RequestID = requestID
		header.Meta.Time = now
		header.Meta.URL = "https://api.openai.com/v1/responses"
		header.Meta.Method = http.MethodPost
		header.Meta.StatusCode = http.StatusOK
		header.Meta.Provider = "openai_compatible"
		header.Meta.Operation = "responses"
		header.Meta.Model = "gpt-postgres-readmodel"
		if err := st.UpsertLog(recordPath, header); err != nil {
			t.Fatalf("UpsertLog(%s) error = %v", status, err)
		}
		entry, err := st.GetByRequestID(requestID)
		if err != nil {
			t.Fatalf("GetByRequestID(%s) error = %v", requestID, err)
		}
		return entry
	}

	parsedEntry := writeLog("req_pg_readmodel_parsed_"+suffix, "parsed")
	unparsedEntry := writeLog("req_pg_readmodel_unparsed_"+suffix, "unparsed")
	queuedTraceID := "trace_pg_readmodel_queued_" + suffix
	runningTraceID := "trace_pg_readmodel_running_" + suffix
	failedTraceID := "trace_pg_readmodel_failed_" + suffix

	if err := st.SaveObservation(observe.TraceObservation{
		TraceID:       parsedEntry.ID,
		Provider:      "openai_compatible",
		Operation:     "responses",
		Model:         "gpt-postgres-readmodel",
		Parser:        "postgres-readmodel",
		ParserVersion: "1",
		Status:        observe.ParseStatusParsed,
	}); err != nil {
		t.Fatalf("SaveObservation(postgres) error = %v", err)
	}
	if err := st.EnqueueParseJob(queuedTraceID); err != nil {
		t.Fatalf("EnqueueParseJob(queued) error = %v", err)
	}
	queuedJobs, err := st.ListParseJobs("queued", 10000)
	if err != nil {
		t.Fatalf("ListParseJobs(queued) error = %v", err)
	}
	if !parseJobListContains(queuedJobs, queuedTraceID, "queued") {
		t.Fatalf("queued parse jobs = %+v, want trace %s", queuedJobs, queuedTraceID)
	}

	if err := st.EnqueueParseJob(runningTraceID); err != nil {
		t.Fatalf("EnqueueParseJob(running) error = %v", err)
	}
	runningJobs, err := st.ListParseJobs("queued", 10000)
	if err != nil {
		t.Fatalf("ListParseJobs(before running) error = %v", err)
	}
	runningJob, ok := findParseJob(runningJobs, runningTraceID)
	if !ok {
		t.Fatalf("queued parse jobs = %+v, want trace %s before running", runningJobs, runningTraceID)
	}
	if err := st.MarkParseJobRunning(runningJob.ID); err != nil {
		t.Fatalf("MarkParseJobRunning(postgres) error = %v", err)
	}

	if err := st.EnqueueParseJob(failedTraceID); err != nil {
		t.Fatalf("EnqueueParseJob(failed) error = %v", err)
	}
	failedJobs, err := st.ListParseJobs("queued", 10000)
	if err != nil {
		t.Fatalf("ListParseJobs(before failed) error = %v", err)
	}
	failedJob, ok := findParseJob(failedJobs, failedTraceID)
	if !ok {
		t.Fatalf("queued parse jobs = %+v, want trace %s before failure", failedJobs, failedTraceID)
	}
	if err := st.MarkParseJobFailed(failedJob.ID, "postgres readmodel parse failure"); err != nil {
		t.Fatalf("MarkParseJobFailed(postgres) error = %v", err)
	}

	metadata, err := st.LoadObservationMetadata([]string{parsedEntry.ID, queuedTraceID, unparsedEntry.ID})
	if err != nil {
		t.Fatalf("LoadObservationMetadata(postgres) error = %v", err)
	}
	if metadata[parsedEntry.ID].Parser != "postgres-readmodel" || metadata[parsedEntry.ID].Status != string(observe.ParseStatusParsed) {
		t.Fatalf("parsed metadata = %+v", metadata[parsedEntry.ID])
	}
	if metadata[queuedTraceID].Status != "queued" {
		t.Fatalf("queued metadata = %+v, want queued fallback from parse_jobs", metadata[queuedTraceID])
	}
	if metadata[unparsedEntry.ID].Status != "unparsed" {
		t.Fatalf("unparsed metadata = %+v", metadata[unparsedEntry.ID])
	}

	finding := observe.Finding{
		ID:              "finding_pg_readmodel_" + suffix,
		TraceID:         parsedEntry.ID,
		Category:        "postgres_readmodel",
		Severity:        observe.SeverityCritical,
		Confidence:      0.98,
		Title:           "Postgres readmodel finding",
		EvidencePath:    "trace#" + parsedEntry.ID,
		EvidenceExcerpt: "postgres readmodel evidence",
		Detector:        "store_test",
		DetectorVersion: "1",
	}
	if err := st.SaveFindings(parsedEntry.ID, []observe.Finding{finding}); err != nil {
		t.Fatalf("SaveFindings(postgres) error = %v", err)
	}
	allFindings, err := st.ListAllFindings(FindingFilter{Category: "postgres_readmodel", Severity: string(observe.SeverityCritical)}, 100)
	if err != nil {
		t.Fatalf("ListAllFindings(postgres) error = %v", err)
	}
	if !findingListContains(allFindings, finding.ID) {
		t.Fatalf("all findings = %+v, want finding %s", allFindings, finding.ID)
	}

	runID, err := st.SaveAnalysisRun(AnalysisRunRecord{
		TraceID:         parsedEntry.ID,
		SessionID:       "session_pg_readmodel_" + suffix,
		Kind:            "postgres_readmodel",
		Analyzer:        "store_test",
		AnalyzerVersion: "1",
		InputRef:        "trace:" + parsedEntry.ID,
		OutputJSON:      `{"postgres":true}`,
		Status:          "failed",
	})
	if err != nil {
		t.Fatalf("SaveAnalysisRun(postgres) error = %v", err)
	}
	runs, err := st.ListAnalysisRuns("session_pg_readmodel_"+suffix, parsedEntry.ID, "postgres_readmodel", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns(postgres) error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != runID || runs[0].Status != "failed" {
		t.Fatalf("analysis runs = %+v, want failed run %d", runs, runID)
	}

	dashboard, err := st.Overview(OverviewOptions{Since: now.Add(-time.Minute), Limit: 100, BucketCount: 2, BucketSize: time.Minute})
	if err != nil {
		t.Fatalf("Overview(postgres) error = %v", err)
	}
	if dashboard.Observation.Parsed < 1 || dashboard.Observation.Queued < 1 || dashboard.Observation.Running < 1 || dashboard.Observation.Failed < 1 {
		t.Fatalf("overview observation = %+v, want parsed/queued/running/failed counts", dashboard.Observation)
	}
	if !parseJobListContains(dashboard.Observation.RecentFailures, failedTraceID, "failed") {
		t.Fatalf("overview recent parse failures = %+v, want trace %s", dashboard.Observation.RecentFailures, failedTraceID)
	}
	if !parseJobListContainsError(dashboard.Observation.RecentFailures, failedTraceID, "postgres readmodel parse failure") {
		t.Fatalf("overview recent parse failures = %+v, want trace %s with persisted error text", dashboard.Observation.RecentFailures, failedTraceID)
	}
	if dashboard.Analysis.Failed < 1 || !analysisRunListContains(dashboard.Analysis.Recent, runID) {
		t.Fatalf("overview analysis = %+v, want failed run %d", dashboard.Analysis, runID)
	}
	if !findingListContains(dashboard.Attention.HighRiskFindings, finding.ID) {
		t.Fatalf("overview high risk findings = %+v, want finding %s", dashboard.Attention.HighRiskFindings, finding.ID)
	}
	if !countItemsContain(dashboard.Breakdown.FindingCategories, "postgres_readmodel") {
		t.Fatalf("overview finding categories = %+v, want postgres_readmodel category", dashboard.Breakdown.FindingCategories)
	}
}

func findParseJob(jobs []ParseJobRecord, traceID string) (ParseJobRecord, bool) {
	for _, job := range jobs {
		if job.TraceID == traceID {
			return job, true
		}
	}
	return ParseJobRecord{}, false
}

func parseJobListContains(jobs []ParseJobRecord, traceID string, status string) bool {
	for _, job := range jobs {
		if job.TraceID == traceID && job.Status == status {
			return true
		}
	}
	return false
}

func parseJobListContainsError(jobs []ParseJobRecord, traceID string, errorText string) bool {
	for _, job := range jobs {
		if job.TraceID == traceID && strings.Contains(job.LastError, errorText) {
			return true
		}
	}
	return false
}

func findingListContains(findings []observe.Finding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}

func countItemsContain(items []CountItem, label string) bool {
	for _, item := range items {
		if item.Label == label && item.Count > 0 {
			return true
		}
	}
	return false
}

func analysisRunListContains(runs []AnalysisRunRecord, id int64) bool {
	for _, run := range runs {
		if run.ID == id {
			return true
		}
	}
	return false
}

func TestPostgresEvalRunAndScoresRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	requestID := "req_eval_" + suffix
	var traceID, datasetID, evalRunID, scoreID string
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			args  []any
		}{
			{query: `DELETE FROM scores WHERE id = ? OR eval_run_id = ? OR dataset_id = ? OR trace_id = ?`, args: []any{scoreID, evalRunID, datasetID, traceID}},
			{query: `DELETE FROM eval_runs WHERE id = ? OR dataset_id = ?`, args: []any{evalRunID, datasetID}},
			{query: `DELETE FROM dataset_examples WHERE dataset_id = ? OR trace_id = ?`, args: []any{datasetID, traceID}},
			{query: `DELETE FROM datasets WHERE id = ?`, args: []any{datasetID}},
			{query: `DELETE FROM logs WHERE id = ? OR request_id = ?`, args: []any{traceID, requestID}},
		} {
			if _, err := st.db.Exec(cleanup.query, cleanup.args...); err != nil {
				t.Logf("cleanup %q error = %v", cleanup.query, err)
			}
		}
	})

	recordPath := filepath.Join(dir, suffix+".http")
	if err := os.WriteFile(recordPath, []byte("# eval postgres smoke\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(record) error = %v", err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     requestID,
			Time:          time.Now().UTC(),
			Model:         "gpt-postgres-eval",
			Provider:      "openai_compatible",
			Operation:     "responses.create",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        http.MethodPost,
			StatusCode:    http.StatusOK,
			DurationMs:    120,
			TTFTMs:        15,
			ClientIP:      "127.0.0.1",
			ContentLength: 4,
		},
		Usage: recordfile.UsageInfo{
			TotalTokens: 42,
		},
	}
	if err := st.UpsertLog(recordPath, header); err != nil {
		t.Fatalf("UpsertLog(postgres) error = %v", err)
	}
	entry, err := st.GetByRequestID(requestID)
	if err != nil {
		t.Fatalf("GetByRequestID(postgres) error = %v", err)
	}
	traceID = entry.ID

	dataset, err := st.CreateDataset("postgres-eval-"+suffix, "postgres eval migration smoke")
	if err != nil {
		t.Fatalf("CreateDataset(postgres) error = %v", err)
	}
	datasetID = dataset.ID
	added, skipped, err := st.AppendDatasetExamples(dataset.ID, []string{entry.ID}, "postgres_eval_smoke", requestID, "dsn-gated")
	if err != nil {
		t.Fatalf("AppendDatasetExamples(postgres) error = %v", err)
	}
	if added != 1 || skipped != 0 {
		t.Fatalf("AppendDatasetExamples(postgres) added/skipped = %d/%d, want 1/0", added, skipped)
	}

	examples, err := st.GetDatasetExamples(dataset.ID)
	if err != nil {
		t.Fatalf("GetDatasetExamples(postgres) error = %v", err)
	}
	if len(examples) != 1 || examples[0].TraceID != entry.ID || examples[0].Trace.Header.Meta.RequestID != requestID {
		t.Fatalf("GetDatasetExamples(postgres) = %#v, want trace %q request %q", examples, entry.ID, requestID)
	}

	run, err := st.CreateEvalRun(dataset.ID, "dataset", dataset.ID, "baseline_v4", 1)
	if err != nil {
		t.Fatalf("CreateEvalRun(postgres) error = %v", err)
	}
	evalRunID = run.ID
	score, err := st.AddScore(ScoreRecord{
		TraceID:      entry.ID,
		SessionID:    entry.SessionID,
		DatasetID:    dataset.ID,
		EvalRunID:    run.ID,
		EvaluatorKey: "postgres_eval_round_trip",
		Value:        1,
		Status:       "pass",
		Label:        "pass",
		Explanation:  "checked-in postgres migration supports eval score round trip",
	})
	if err != nil {
		t.Fatalf("AddScore(postgres) error = %v", err)
	}
	scoreID = score.ID
	if err := st.FinalizeEvalRun(run.ID, 1, 1, 0); err != nil {
		t.Fatalf("FinalizeEvalRun(postgres) error = %v", err)
	}

	gotRun, err := st.GetEvalRun(run.ID)
	if err != nil {
		t.Fatalf("GetEvalRun(postgres) error = %v", err)
	}
	if gotRun.DatasetID != dataset.ID || gotRun.EvaluatorSet != "baseline_v4" || gotRun.ScoreCount != 1 || gotRun.PassCount != 1 || gotRun.FailCount != 0 {
		t.Fatalf("GetEvalRun(postgres) = %#v, want finalized run for dataset %q", gotRun, dataset.ID)
	}

	runs, err := st.ListEvalRuns(25)
	if err != nil {
		t.Fatalf("ListEvalRuns(postgres) error = %v", err)
	}
	foundRun := false
	for _, candidate := range runs {
		if candidate.ID == run.ID {
			foundRun = true
			break
		}
	}
	if !foundRun {
		t.Fatalf("ListEvalRuns(postgres) missing run %q in %#v", run.ID, runs)
	}

	scores, err := st.ListScores(ScoreFilter{EvalRunID: run.ID}, 10)
	if err != nil {
		t.Fatalf("ListScores(eval_run postgres) error = %v", err)
	}
	if len(scores) != 1 || scores[0].ID != score.ID || scores[0].Status != "pass" {
		t.Fatalf("ListScores(eval_run postgres) = %#v, want pass score %q", scores, score.ID)
	}

	scores, err = st.ListScores(ScoreFilter{DatasetID: dataset.ID, TraceID: entry.ID}, 10)
	if err != nil {
		t.Fatalf("ListScores(dataset+trace postgres) error = %v", err)
	}
	if len(scores) != 1 || scores[0].EvaluatorKey != "postgres_eval_round_trip" {
		t.Fatalf("ListScores(dataset+trace postgres) = %#v, want postgres_eval_round_trip", scores)
	}
}

func TestPostgresExperimentRunReadModelsRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	var datasetID, baselineEvalRunID, candidateEvalRunID, experimentRunID string
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			args  []any
		}{
			{query: `DELETE FROM experiment_runs WHERE id = ? OR baseline_eval_run_id = ? OR candidate_eval_run_id = ?`, args: []any{experimentRunID, baselineEvalRunID, candidateEvalRunID}},
			{query: `DELETE FROM eval_runs WHERE id = ? OR id = ? OR dataset_id = ?`, args: []any{baselineEvalRunID, candidateEvalRunID, datasetID}},
			{query: `DELETE FROM datasets WHERE id = ?`, args: []any{datasetID}},
		} {
			if _, err := st.db.Exec(cleanup.query, cleanup.args...); err != nil {
				t.Logf("cleanup %q error = %v", cleanup.query, err)
			}
		}
	})

	dataset, err := st.CreateDataset("postgres-experiment-"+suffix, "postgres experiment read model smoke")
	if err != nil {
		t.Fatalf("CreateDataset(postgres) error = %v", err)
	}
	datasetID = dataset.ID

	baselineRun, err := st.CreateEvalRun(dataset.ID, "dataset", dataset.ID, "postgres_baseline_v1", 3)
	if err != nil {
		t.Fatalf("CreateEvalRun(baseline postgres) error = %v", err)
	}
	baselineEvalRunID = baselineRun.ID
	if err := st.FinalizeEvalRun(baselineRun.ID, 3, 2, 1); err != nil {
		t.Fatalf("FinalizeEvalRun(baseline postgres) error = %v", err)
	}

	candidateRun, err := st.CreateEvalRun(dataset.ID, "dataset", dataset.ID, "postgres_candidate_v1", 3)
	if err != nil {
		t.Fatalf("CreateEvalRun(candidate postgres) error = %v", err)
	}
	candidateEvalRunID = candidateRun.ID
	if err := st.FinalizeEvalRun(candidateRun.ID, 3, 3, 0); err != nil {
		t.Fatalf("FinalizeEvalRun(candidate postgres) error = %v", err)
	}

	experiment, err := st.CreateExperimentRun(ExperimentRunRecord{
		Name:                "postgres-baseline-vs-candidate-" + suffix,
		Description:         "postgres experiment read model round trip",
		BaselineEvalRunID:   baselineRun.ID,
		CandidateEvalRunID:  candidateRun.ID,
		BaselineScoreCount:  3,
		CandidateScoreCount: 3,
		BaselinePassRate:    66.67,
		CandidatePassRate:   100,
		PassRateDelta:       33.33,
		MatchedScoreCount:   3,
		ImprovementCount:    1,
		RegressionCount:     0,
	})
	if err != nil {
		t.Fatalf("CreateExperimentRun(postgres) error = %v", err)
	}
	experimentRunID = experiment.ID

	got, err := st.GetExperimentRun(experiment.ID)
	if err != nil {
		t.Fatalf("GetExperimentRun(postgres) error = %v", err)
	}
	if got.ID != experiment.ID || got.Name != experiment.Name || got.Description != experiment.Description {
		t.Fatalf("GetExperimentRun(postgres) = %#v, want id/name/description from created experiment %#v", got, experiment)
	}
	if got.BaselineEvalRunID != baselineRun.ID || got.CandidateEvalRunID != candidateRun.ID {
		t.Fatalf("GetExperimentRun(postgres) = %#v, want eval runs %q and %q", got, baselineRun.ID, candidateRun.ID)
	}
	if got.BaselineScoreCount != 3 || got.CandidateScoreCount != 3 || got.MatchedScoreCount != 3 {
		t.Fatalf("GetExperimentRun(postgres) = %#v, want score counts 3/3 matched 3", got)
	}
	if got.ImprovementCount != 1 || got.RegressionCount != 0 {
		t.Fatalf("GetExperimentRun(postgres) = %#v, want improvement/regression 1/0", got)
	}
	if got.BaselinePassRate != 66.67 || got.CandidatePassRate != 100 || got.PassRateDelta != 33.33 {
		t.Fatalf("GetExperimentRun(postgres) = %#v, want pass rates 66.67/100 delta 33.33", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("GetExperimentRun(postgres) CreatedAt is zero: %#v", got)
	}

	runs, err := st.ListExperimentRuns(50)
	if err != nil {
		t.Fatalf("ListExperimentRuns(postgres) error = %v", err)
	}
	var listed ExperimentRunRecord
	found := false
	for _, candidate := range runs {
		if candidate.ID == experiment.ID {
			listed = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListExperimentRuns(postgres) missing experiment %q in %#v", experiment.ID, runs)
	}
	if listed.Name != experiment.Name || listed.BaselineEvalRunID != baselineRun.ID || listed.CandidateEvalRunID != candidateRun.ID {
		t.Fatalf("ListExperimentRuns(postgres) entry = %#v, want created experiment %#v", listed, experiment)
	}
	if listed.ImprovementCount != 1 || listed.RegressionCount != 0 || listed.PassRateDelta != 33.33 {
		t.Fatalf("ListExperimentRuns(postgres) entry = %#v, want improvement/regression/delta 1/0/33.33", listed)
	}
}

func TestPostgresAnalysisJobReadModelsRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	queuedTargetID := "trace-pg-analysis-queued-" + suffix
	completedTargetID := "trace-pg-analysis-completed-" + suffix
	failedTargetID := "trace-pg-analysis-failed-" + suffix
	var queuedJobID, completedJobID, failedJobID int64
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			args  []any
		}{
			{query: `DELETE FROM system_events WHERE job_id IN (?, ?, ?) OR trace_id IN (?, ?, ?)`, args: []any{
				queuedJobID, completedJobID, failedJobID,
				queuedTargetID, completedTargetID, failedTargetID,
			}},
			{query: `DELETE FROM analysis_jobs WHERE id IN (?, ?, ?) OR target_id IN (?, ?, ?)`, args: []any{
				queuedJobID, completedJobID, failedJobID,
				queuedTargetID, completedTargetID, failedTargetID,
			}},
		} {
			if _, err := st.db.Exec(cleanup.query, cleanup.args...); err != nil {
				t.Logf("cleanup %q error = %v", cleanup.query, err)
			}
		}
	})

	queuedJob, err := st.CreateAnalysisJob(AnalysisJobRecord{
		JobType:     "postgres_trace_reanalyze",
		TargetType:  "trace",
		TargetID:    queuedTargetID,
		StepsJSON:   `["reparse_observation","scan_findings"]`,
		RequestJSON: `{"mode":"worker"}`,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisJob(queued postgres) error = %v", err)
	}
	queuedJobID = queuedJob.ID
	if queuedJob.ID == 0 || queuedJob.Status != "queued" || queuedJob.ResultJSON != "{}" {
		t.Fatalf("queued postgres job = %+v, want queued defaults and id", queuedJob)
	}

	completedJob, err := st.CreateAnalysisJob(AnalysisJobRecord{
		JobType:    "postgres_trace_reanalyze",
		TargetType: "trace",
		TargetID:   completedTargetID,
		Status:     "queued",
	})
	if err != nil {
		t.Fatalf("CreateAnalysisJob(completed postgres) error = %v", err)
	}
	completedJobID = completedJob.ID

	failedJob, err := st.CreateAnalysisJob(AnalysisJobRecord{
		JobType:    "postgres_trace_reanalyze",
		TargetType: "trace",
		TargetID:   failedTargetID,
		Status:     "queued",
	})
	if err != nil {
		t.Fatalf("CreateAnalysisJob(failed postgres) error = %v", err)
	}
	failedJobID = failedJob.ID

	workerJobs, err := st.ListAnalysisJobsForWorker(10000)
	if err != nil {
		t.Fatalf("ListAnalysisJobsForWorker(postgres) error = %v", err)
	}
	if !analysisJobListContains(workerJobs, queuedJob.ID, "queued") ||
		!analysisJobListContains(workerJobs, completedJob.ID, "queued") ||
		!analysisJobListContains(workerJobs, failedJob.ID, "queued") {
		t.Fatalf("worker jobs = %+v, want all inserted queued postgres jobs", workerJobs)
	}

	if err := st.MarkAnalysisJobRunning(completedJob.ID); err != nil {
		t.Fatalf("MarkAnalysisJobRunning(postgres) error = %v", err)
	}
	running, err := st.GetAnalysisJob(completedJob.ID)
	if err != nil {
		t.Fatalf("GetAnalysisJob(running postgres) error = %v", err)
	}
	if running.Status != "running" || running.Attempts != 1 || running.StartedAt.IsZero() {
		t.Fatalf("running postgres job = %+v, want running with one attempt and started_at", running)
	}

	if err := st.MarkAnalysisJobCompleted(completedJob.ID, `{"findings":2}`); err != nil {
		t.Fatalf("MarkAnalysisJobCompleted(postgres) error = %v", err)
	}
	completed, err := st.GetAnalysisJob(completedJob.ID)
	if err != nil {
		t.Fatalf("GetAnalysisJob(completed postgres) error = %v", err)
	}
	if completed.Status != "completed" || completed.ResultJSON != `{"findings":2}` || completed.FinishedAt.IsZero() {
		t.Fatalf("completed postgres job = %+v, want completed result and finished_at", completed)
	}

	completedJobs, err := st.ListAnalysisJobs("completed", "trace", completedTargetID, 10)
	if err != nil {
		t.Fatalf("ListAnalysisJobs(completed postgres) error = %v", err)
	}
	if len(completedJobs) != 1 || completedJobs[0].ID != completedJob.ID {
		t.Fatalf("completed jobs = %+v, want job %d", completedJobs, completedJob.ID)
	}

	if err := st.MarkAnalysisJobFailed(failedJob.ID, "postgres read model failure"); err != nil {
		t.Fatalf("MarkAnalysisJobFailed(postgres) error = %v", err)
	}
	failed, err := st.GetAnalysisJob(failedJob.ID)
	if err != nil {
		t.Fatalf("GetAnalysisJob(failed postgres) error = %v", err)
	}
	if failed.Status != "failed" || failed.LastError != "postgres read model failure" || failed.FinishedAt.IsZero() {
		t.Fatalf("failed postgres job = %+v, want failed error and finished_at", failed)
	}

	events, err := st.ListSystemEvents(SystemEventFilter{
		Source:   "analyzer",
		Category: "analysis_job_failure",
		Query:    failedTargetID,
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListSystemEvents(analysis job failure postgres) error = %v", err)
	}
	if events.Total == 0 || len(events.Items) == 0 || events.Items[0].JobID != strconv.FormatInt(failedJob.ID, 10) {
		t.Fatalf("analysis job failure events = %+v, want job %d", events, failedJob.ID)
	}
}

func analysisJobListContains(jobs []AnalysisJobRecord, id int64, status string) bool {
	for _, job := range jobs {
		if job.ID == id && job.Status == status {
			return true
		}
	}
	return false
}

func TestPostgresSystemEventReadModelRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	source := "postgres_system_event_test"
	parserFingerprint := "postgres:system_event:parser:" + suffix
	routerFingerprint := "postgres:system_event:router:" + suffix
	t.Cleanup(func() {
		if _, err := st.db.Exec(`DELETE FROM system_events WHERE fingerprint = ? OR fingerprint = ?`, parserFingerprint, routerFingerprint); err != nil {
			t.Logf("cleanup postgres system events error = %v", err)
		}
	})

	since := time.Now().UTC().Add(-time.Second)
	parserEvent, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: parserFingerprint,
		Source:      source,
		Category:    "parse_failure",
		Severity:    "error",
		Title:       "Postgres parse failure",
		Message:     "bad json in trace " + suffix,
		TraceID:     "trace-postgres-system-event-" + suffix,
		SessionID:   "session-postgres-system-event-" + suffix,
		Model:       "gpt-postgres-system-event",
		DetailsJSON: json.RawMessage(`{"postgres":true,"path":"parser"}`),
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(parser postgres) error = %v", err)
	}
	routerEvent, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: routerFingerprint,
		Source:      source,
		Category:    "routing_failure",
		Severity:    "warning",
		Title:       "Postgres routing failure",
		Message:     "all targets open",
		UpstreamID:  "upstream-postgres-system-event-" + suffix,
		Model:       "gpt-postgres-system-event",
		DetailsJSON: json.RawMessage(`{"postgres":true,"path":"router"}`),
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(router postgres) error = %v", err)
	}
	if err := st.ResolveSystemEvent(routerEvent.ID); err != nil {
		t.Fatalf("ResolveSystemEvent(postgres) error = %v", err)
	}

	page, err := st.ListSystemEvents(SystemEventFilter{
		Status:   SystemEventStatusUnread,
		Source:   source,
		Category: "parse_failure",
		Query:    "trace-postgres-system-event",
		Since:    since,
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListSystemEvents(postgres) error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != parserEvent.ID {
		t.Fatalf("ListSystemEvents(postgres) = %+v, want parser event %q", page, parserEvent.ID)
	}

	summary, err := st.SystemEventSummary(since)
	if err != nil {
		t.Fatalf("SystemEventSummary(postgres) error = %v", err)
	}
	if summary.Total < 2 || summary.Unread < 1 || summary.Error < 1 || summary.Warning != 0 {
		t.Fatalf("SystemEventSummary(postgres) = %+v, want parser unread error and resolved warning excluded from unread warnings", summary)
	}
	foundSource := false
	for _, item := range summary.BySource {
		if item.Label == source && item.Count >= 2 {
			foundSource = true
			break
		}
	}
	foundCategory := false
	for _, item := range summary.ByCategory {
		if item.Label == "parse_failure" && item.Count >= 1 {
			foundCategory = true
			break
		}
	}
	if !foundSource || !foundCategory {
		t.Fatalf("SystemEventSummary(postgres) by-source=%+v by-category=%+v, want inserted source/category", summary.BySource, summary.ByCategory)
	}

	count, err := st.MarkAllSystemEventsRead(SystemEventFilter{
		Status:   SystemEventStatusUnread,
		Source:   source,
		Category: "parse_failure",
		Since:    since,
	})
	if err != nil {
		t.Fatalf("MarkAllSystemEventsRead(postgres) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("MarkAllSystemEventsRead(postgres) count = %d, want 1", count)
	}
	afterRead, err := st.GetSystemEvent(parserEvent.ID)
	if err != nil {
		t.Fatalf("GetSystemEvent(postgres after read) error = %v", err)
	}
	if afterRead.Status != SystemEventStatusRead || afterRead.ReadAt.IsZero() {
		t.Fatalf("GetSystemEvent(postgres after read) = %+v, want read status/read_at", afterRead)
	}

	if err := st.IgnoreSystemEvent(afterRead.ID); err != nil {
		t.Fatalf("IgnoreSystemEvent(postgres) error = %v", err)
	}
	ignored, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: parserFingerprint,
		Source:      source,
		Category:    "parse_failure",
		Severity:    "critical",
		Title:       "Ignored Postgres parse failure repeated",
		Message:     "still ignored",
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(ignored postgres) error = %v", err)
	}
	if ignored.Status != SystemEventStatusIgnored || ignored.OccurrenceCount != 2 || ignored.ReadAt.IsZero() {
		t.Fatalf("UpsertSystemEvent(ignored postgres) = %+v, want ignored duplicate with read_at retained", ignored)
	}
}

func TestPostgresSessionAndOverviewRuntimeSQLRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	sessionID := "sess_pg_monitor_" + suffix
	requestIDs := []string{
		"req_pg_monitor_stream_" + suffix,
		"req_pg_monitor_batch_" + suffix,
	}
	t.Cleanup(func() {
		if _, err := st.db.Exec(`DELETE FROM logs WHERE session_id = ? OR request_id = ? OR request_id = ?`, sessionID, requestIDs[0], requestIDs[1]); err != nil {
			t.Logf("cleanup postgres monitor logs error = %v", err)
		}
	})

	writeLog := func(name string, requestID string, provider string, stream bool, statusCode int, recordedAt time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("postgres monitor smoke\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     requestID,
				Time:          recordedAt,
				Model:         "gpt-postgres-monitor",
				Provider:      provider,
				Operation:     "responses.create",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        http.MethodPost,
				StatusCode:    statusCode,
				DurationMs:    100,
				TTFTMs:        25,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
			Layout: recordfile.LayoutInfo{
				IsStream: stream,
			},
			Usage: recordfile.UsageInfo{
				TotalTokens: 11,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "postgres.runtime_sql_test",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	base := time.Date(2026, 6, 23, 8, 0, 0, 0, time.UTC)
	writeLog("postgres-session-stream.http", requestIDs[0], "openai_compatible", true, http.StatusOK, base)
	writeLog("postgres-session-batch.http", requestIDs[1], "anthropic", false, http.StatusTooManyRequests, base.Add(time.Minute))

	page, err := st.ListSessionPage(1, 10, ListFilter{Query: sessionID})
	if err != nil {
		t.Fatalf("ListSessionPage(postgres) error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].SessionID != sessionID {
		t.Fatalf("ListSessionPage(postgres) = %#v, want one session %q", page.Items, sessionID)
	}
	if page.Items[0].RequestCount != 2 || page.Items[0].StreamCount != 1 {
		t.Fatalf("session counts = requests:%d streams:%d, want 2/1", page.Items[0].RequestCount, page.Items[0].StreamCount)
	}
	if !containsString(page.Items[0].Providers, "openai_compatible") || !containsString(page.Items[0].Providers, "anthropic") {
		t.Fatalf("session providers = %#v, want openai_compatible and anthropic", page.Items[0].Providers)
	}

	session, err := st.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession(postgres) error = %v", err)
	}
	if session.RequestCount != 2 || session.StreamCount != 1 || session.FailedRequest != 1 {
		t.Fatalf("GetSession(postgres) counts = requests:%d streams:%d failed:%d, want 2/1/1", session.RequestCount, session.StreamCount, session.FailedRequest)
	}

	overview, err := st.Overview(OverviewOptions{
		Since:       base.Add(-time.Minute),
		Limit:       5,
		BucketSize:  time.Hour,
		BucketCount: 2,
	})
	if err != nil {
		t.Fatalf("Overview(postgres) error = %v", err)
	}
	if overview.Summary.StreamCount < 1 || overview.Summary.SessionCount < 1 {
		t.Fatalf("Overview(postgres) summary = %#v, want stream and session counts", overview.Summary)
	}
}

func TestPostgresUpstreamAndRoutingAnalyticsRuntimeSQLRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ReplaceAll(t.Name(), "/", "_") + "_" + time.Now().UTC().Format("20060102150405.000000000")
	upstreamID := "pg-analytics-primary-" + suffix
	model := "gpt-postgres-analytics-" + suffix
	requestIDs := []string{
		"req_pg_analytics_ok_" + suffix,
		"req_pg_analytics_rate_limited_" + suffix,
		"req_pg_analytics_other_model_" + suffix,
	}
	t.Cleanup(func() {
		if _, err := st.db.Exec(
			`DELETE FROM logs WHERE request_id = ? OR request_id = ? OR request_id = ?`,
			requestIDs[0],
			requestIDs[1],
			requestIDs[2],
		); err != nil {
			t.Logf("cleanup postgres analytics logs error = %v", err)
		}
	})

	writeLog := func(name string, requestID string, recordedAt time.Time, modelName string, statusCode int, errorText string, routingFailureReason string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("# llm-tracelab/v3\n\nPOST /v1/responses HTTP/1.1\n\nHTTP/1.1 200 OK\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      requestID,
				Time:                           recordedAt,
				Model:                          modelName,
				Provider:                       "openai_compatible",
				Operation:                      "responses.create",
				Endpoint:                       "/v1/responses",
				URL:                            "/v1/responses",
				Method:                         http.MethodPost,
				StatusCode:                     statusCode,
				DurationMs:                     100,
				TTFTMs:                         25,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  4,
				Error:                          errorText,
				SelectedUpstreamID:             upstreamID,
				SelectedUpstreamBaseURL:        "https://postgres-analytics.invalid/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
				RoutingScore:                   0.75,
				RoutingCandidateCount:          2,
				RoutingFailureReason:           routingFailureReason,
			},
			Usage: recordfile.UsageInfo{
				PromptTokens:     7,
				CompletionTokens: 5,
				TotalTokens:      12,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
	}

	base := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	writeLog("postgres-analytics-ok.http", requestIDs[0], base, model, http.StatusOK, "", "")
	writeLog("postgres-analytics-rate-limited.http", requestIDs[1], base.Add(10*time.Minute), model, http.StatusTooManyRequests, "rate limit exceeded", "all_targets_open")
	writeLog("postgres-analytics-other-model.http", requestIDs[2], base.Add(20*time.Minute), "gemini-postgres-analytics-"+suffix, http.StatusBadGateway, "selection failed", "no_supporting_target")

	analytics, err := st.ListUpstreamAnalytics(5, 5, base.Add(-time.Minute), model)
	if err != nil {
		t.Fatalf("ListUpstreamAnalytics(postgres) error = %v", err)
	}
	var upstream UpstreamAnalyticsRecord
	for _, item := range analytics {
		if item.UpstreamID == upstreamID {
			upstream = item
			break
		}
	}
	if upstream.UpstreamID == "" {
		t.Fatalf("ListUpstreamAnalytics(postgres) missing upstream %q in %#v", upstreamID, analytics)
	}
	if upstream.RequestCount != 2 || upstream.SuccessRequest != 1 || upstream.FailedRequest != 1 || upstream.TotalTokens != 12 {
		t.Fatalf("upstream analytics = %#v, want request/success/failure/tokens 2/1/1/12", upstream)
	}
	if upstream.LastModel != model || !containsString(upstream.Models, model) {
		t.Fatalf("upstream models = last:%q models:%#v, want %q", upstream.LastModel, upstream.Models, model)
	}
	if len(upstream.RecentFailures) != 1 || upstream.RecentFailures[0].Reason != "rate_limited" {
		t.Fatalf("upstream failures = %#v, want one rate_limited failure", upstream.RecentFailures)
	}

	detail, err := st.GetUpstreamDetail(upstreamID, base.Add(-time.Minute), model, 10, time.Hour, 3)
	if err != nil {
		t.Fatalf("GetUpstreamDetail(postgres) error = %v", err)
	}
	if detail.Analytics.UpstreamID != upstreamID || len(detail.Traces) != 2 {
		t.Fatalf("GetUpstreamDetail(postgres) = analytics:%#v traces:%d, want upstream %q with two traces", detail.Analytics, len(detail.Traces), upstreamID)
	}
	if len(detail.FailureReasons) != 1 || detail.FailureReasons[0].Label != "rate_limited" {
		t.Fatalf("detail failure reasons = %#v, want rate_limited", detail.FailureReasons)
	}

	routing, err := st.GetRoutingFailureAnalytics(base.Add(-time.Minute), model, 5, 5, time.Hour, 3)
	if err != nil {
		t.Fatalf("GetRoutingFailureAnalytics(postgres) error = %v", err)
	}
	if routing.Total != 1 || len(routing.Reasons) != 1 || routing.Reasons[0].Label != "all_targets_open" {
		t.Fatalf("routing analytics = %#v, want one all_targets_open failure", routing)
	}
	if len(routing.Recent) != 1 || routing.Recent[0].Reason != "all_targets_open" || routing.Recent[0].StatusCode != http.StatusTooManyRequests {
		t.Fatalf("routing recent = %#v, want one filtered 429 all_targets_open failure", routing.Recent)
	}
	timelineTotal := 0
	for _, item := range routing.Timeline {
		timelineTotal += item.Count
	}
	if timelineTotal != 1 {
		t.Fatalf("routing timeline = %#v, want total 1", routing.Timeline)
	}
}

func TestPostgresModelCatalogAnalyticsRuntimeSQLRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ToLower(strings.NewReplacer("/", "_", ".", "_").Replace(t.Name())) + "_" + time.Now().UTC().Format("20060102150405000000000")
	openAIChannelID := "pg-model-openai-" + suffix
	openRouterChannelID := "pg-model-openrouter-" + suffix
	model := "gpt-postgres-model-" + suffix
	traceModel := "gpt-postgres-trace-only-" + suffix
	requestIDs := []string{
		"req_pg_model_success_" + suffix,
		"req_pg_model_missing_usage_" + suffix,
		"req_pg_model_failed_" + suffix,
		"req_pg_model_trace_only_" + suffix,
		"req_pg_model_list_" + suffix,
	}
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			args  []any
		}{
			{query: `DELETE FROM logs WHERE request_id IN (?, ?, ?, ?, ?)`, args: []any{requestIDs[0], requestIDs[1], requestIDs[2], requestIDs[3], requestIDs[4]}},
			{query: `DELETE FROM channel_models WHERE upstream_id IN (?, ?)`, args: []any{openAIChannelID, openRouterChannelID}},
			{query: `DELETE FROM channel_configs WHERE id IN (?, ?)`, args: []any{openAIChannelID, openRouterChannelID}},
		} {
			if _, err := st.db.Exec(cleanup.query, cleanup.args...); err != nil {
				t.Logf("cleanup postgres model analytics query %q error = %v", cleanup.query, err)
			}
		}
	})

	for _, channel := range []ChannelConfigRecord{
		{ID: openAIChannelID, Name: "Postgres Model OpenAI", BaseURL: "https://postgres-model-openai.invalid/v1", ProviderPreset: "openai", HeadersJSON: "{}", Enabled: true},
		{ID: openRouterChannelID, Name: "Postgres Model OpenRouter", BaseURL: "https://postgres-model-openrouter.invalid/api/v1", ProviderPreset: "openrouter", HeadersJSON: "{}", Enabled: true},
	} {
		if _, err := st.UpsertChannelConfig(channel); err != nil {
			t.Fatalf("UpsertChannelConfig(%s postgres) error = %v", channel.ID, err)
		}
	}
	if err := st.ReplaceChannelModels(openAIChannelID, []ChannelModelRecord{{Model: model, DisplayName: "Postgres Model", Source: "manual", Enabled: true}}); err != nil {
		t.Fatalf("ReplaceChannelModels(openai postgres) error = %v", err)
	}
	if err := st.ReplaceChannelModels(openRouterChannelID, []ChannelModelRecord{{Model: model, DisplayName: "Postgres Model", Source: "manual", Enabled: false}}); err != nil {
		t.Fatalf("ReplaceChannelModels(openrouter postgres) error = %v", err)
	}

	base := time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC)
	writeModelLog(t, st, dir, "postgres-model-success.http", model, "/v1/responses", http.MethodPost, openAIChannelID, http.StatusOK, 100, base)
	writeModelLog(t, st, dir, "postgres-model-missing-usage.http", model, "/v1/responses", http.MethodPost, openAIChannelID, http.StatusOK, 0, base.Add(10*time.Minute))
	writeModelLog(t, st, dir, "postgres-model-failed.http", model, "/v1/responses", http.MethodPost, openRouterChannelID, http.StatusBadGateway, 50, base.Add(20*time.Minute))
	writeModelLog(t, st, dir, "postgres-model-trace-only.http", traceModel, "/v1/responses", http.MethodPost, openAIChannelID, http.StatusOK, 25, base.Add(30*time.Minute))
	writeModelLog(t, st, dir, "postgres-list-models.http", "list_models", "/v1/models", http.MethodGet, openAIChannelID, http.StatusOK, 0, base.Add(40*time.Minute))

	for i, requestID := range requestIDs {
		if _, err := st.db.Exec(`UPDATE logs SET request_id = ? WHERE path = ?`, requestID, filepath.Join(dir, []string{
			"postgres-model-success.http",
			"postgres-model-missing-usage.http",
			"postgres-model-failed.http",
			"postgres-model-trace-only.http",
			"postgres-list-models.http",
		}[i])); err != nil {
			t.Fatalf("update request id %q error = %v", requestID, err)
		}
	}

	items, err := st.ListModelCatalogAnalytics(base.Add(-time.Minute), time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListModelCatalogAnalytics(postgres) error = %v", err)
	}
	var catalogItem ModelCatalogAnalyticsRecord
	for _, item := range items {
		if item.Model == model {
			catalogItem = item
			break
		}
	}
	if catalogItem.Model == "" {
		t.Fatalf("ListModelCatalogAnalytics(postgres) missing model %q in %#v", model, items)
	}
	if catalogItem.ChannelCount != 2 || catalogItem.EnabledChannelCount != 1 || catalogItem.ProviderCount != 2 {
		t.Fatalf("catalog item = %+v, want channel/enabled/provider counts 2/1/2", catalogItem)
	}
	if catalogItem.Summary.RequestCount != 3 || catalogItem.Summary.SuccessRequest != 2 || catalogItem.Summary.FailedRequest != 1 || catalogItem.Summary.MissingUsage != 1 || catalogItem.Summary.TotalTokens != 150 {
		t.Fatalf("catalog summary = %+v, want request/success/failure/missing/tokens 3/2/1/1/150", catalogItem.Summary)
	}

	var traceOnlyItem ModelCatalogAnalyticsRecord
	for _, item := range items {
		if item.Model == traceModel {
			traceOnlyItem = item
			break
		}
	}
	if traceOnlyItem.Model == "" || traceOnlyItem.ChannelCount != 0 || traceOnlyItem.Summary.RequestCount != 1 || traceOnlyItem.Summary.TotalTokens != 25 {
		t.Fatalf("trace-only catalog item = %+v, want one log-derived model without channel metadata", traceOnlyItem)
	}
	for _, item := range items {
		if item.Model == "list_models" {
			t.Fatalf("ListModelCatalogAnalytics(postgres) included list_models pseudo model: %#v", items)
		}
	}

	detail, err := st.GetModelDetailAnalytics(model, base.Add(-time.Minute), time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC), time.Hour, 4)
	if err != nil {
		t.Fatalf("GetModelDetailAnalytics(postgres) error = %v", err)
	}
	if len(detail.Channels) != 2 {
		t.Fatalf("detail channels len = %d, want 2: %#v", len(detail.Channels), detail.Channels)
	}
	if len(detail.Trends) != 4 {
		t.Fatalf("detail trends len = %d, want 4: %#v", len(detail.Trends), detail.Trends)
	}
	lastTrend := detail.Trends[len(detail.Trends)-1]
	if lastTrend.RequestCount != 3 || lastTrend.FailedRequest != 1 || lastTrend.MissingUsage != 1 || lastTrend.TotalTokens != 150 {
		t.Fatalf("last trend = %+v, want request/failure/missing/tokens 3/1/1/150", lastTrend)
	}
}

func TestPostgresChannelAnalyticsRuntimeSQLRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := appdbmigrate.MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}

	dir := t.TempDir()
	st, err := NewWithDatabaseOptions(dir, "postgres", dsn, 4, 4, DatabaseOptions{AutoMigrate: false})
	if err != nil {
		t.Fatalf("NewWithDatabaseOptions(postgres) error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("Close(postgres store) error = %v", err)
		}
	})

	suffix := strings.ToLower(strings.NewReplacer("/", "_", ".", "_").Replace(t.Name())) + "_" + time.Now().UTC().Format("20060102150405000000000")
	channelID := "pg-channel-analytics-" + suffix
	model := "gpt-postgres-channel-" + suffix
	disabledModel := "gpt-postgres-channel-disabled-" + suffix
	traceOnlyModel := "gpt-postgres-channel-trace-only-" + suffix
	logNames := []string{
		"postgres-channel-success.http",
		"postgres-channel-missing-usage.http",
		"postgres-channel-failed.http",
		"postgres-channel-trace-only.http",
		"postgres-channel-list-models.http",
	}
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			args  []any
		}{
			{query: `DELETE FROM logs WHERE path IN (?, ?, ?, ?, ?)`, args: []any{
				filepath.Join(dir, logNames[0]),
				filepath.Join(dir, logNames[1]),
				filepath.Join(dir, logNames[2]),
				filepath.Join(dir, logNames[3]),
				filepath.Join(dir, logNames[4]),
			}},
			{query: `DELETE FROM channel_models WHERE upstream_id = ?`, args: []any{channelID}},
			{query: `DELETE FROM channel_configs WHERE id = ?`, args: []any{channelID}},
		} {
			if _, err := st.db.Exec(cleanup.query, cleanup.args...); err != nil {
				t.Logf("cleanup postgres channel analytics query %q error = %v", cleanup.query, err)
			}
		}
	})

	if _, err := st.UpsertChannelConfig(ChannelConfigRecord{
		ID:             channelID,
		Name:           "Postgres Channel Analytics",
		BaseURL:        "https://postgres-channel-analytics.invalid/v1",
		ProviderPreset: "openai",
		HeadersJSON:    "{}",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig(postgres) error = %v", err)
	}
	if err := st.ReplaceChannelModels(channelID, []ChannelModelRecord{
		{Model: model, DisplayName: "Postgres Channel Model", Source: "manual", Enabled: true},
		{Model: disabledModel, DisplayName: "Postgres Disabled Model", Source: "manual", Enabled: false},
		{Model: "list_models", DisplayName: "List Models", Source: "probe", Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels(postgres) error = %v", err)
	}

	base := time.Date(2026, 6, 23, 13, 0, 0, 0, time.UTC)
	writeModelLog(t, st, dir, logNames[0], model, "/v1/responses", http.MethodPost, channelID, http.StatusOK, 100, base)
	writeModelLog(t, st, dir, logNames[1], model, "/v1/responses", http.MethodPost, channelID, http.StatusOK, 0, base.Add(10*time.Minute))
	writeModelLog(t, st, dir, logNames[2], disabledModel, "/v1/responses", http.MethodPost, channelID, http.StatusBadGateway, 50, base.Add(20*time.Minute))
	writeModelLog(t, st, dir, logNames[3], traceOnlyModel, "/v1/responses", http.MethodPost, channelID, http.StatusOK, 25, base.Add(30*time.Minute))
	writeModelLog(t, st, dir, logNames[4], "list_models", "/v1/models", http.MethodGet, channelID, http.StatusOK, 0, base.Add(40*time.Minute))

	summary, err := st.GetChannelUsageSummary(channelID, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetChannelUsageSummary(postgres) error = %v", err)
	}
	if summary.RequestCount != 5 || summary.SuccessRequest != 4 || summary.FailedRequest != 1 || summary.MissingUsage != 2 || summary.TotalTokens != 175 {
		t.Fatalf("channel summary = %+v, want requests/success/failure/missing/tokens 5/4/1/2/175", summary)
	}

	trends, err := st.GetChannelUsageTrends(channelID, base.Add(-time.Minute), time.Hour, 3)
	if err != nil {
		t.Fatalf("GetChannelUsageTrends(postgres) error = %v", err)
	}
	if len(trends) != 3 {
		t.Fatalf("channel trends len = %d, want 3: %#v", len(trends), trends)
	}
	lastTrend := trends[len(trends)-1]
	if lastTrend.RequestCount != 5 || lastTrend.FailedRequest != 1 || lastTrend.MissingUsage != 2 || lastTrend.TotalTokens != 175 || lastTrend.ModelCount != 3 {
		t.Fatalf("last channel trend = %+v, want requests/failure/missing/tokens/models 5/1/2/175/3", lastTrend)
	}

	modelUsage, err := st.GetChannelModelUsage(channelID, base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("GetChannelModelUsage(postgres) error = %v", err)
	}
	byModel := map[string]ChannelModelAnalyticsRecord{}
	for _, item := range modelUsage {
		byModel[item.Model] = item
		if item.Model == "list_models" {
			t.Fatalf("GetChannelModelUsage(postgres) included list_models pseudo model: %#v", modelUsage)
		}
	}
	if byModel[model].ChannelID != channelID || !byModel[model].Enabled || byModel[model].Source != "manual" || byModel[model].Summary.RequestCount != 2 || byModel[model].Summary.MissingUsage != 1 || byModel[model].Summary.TotalTokens != 100 {
		t.Fatalf("model usage[%s] = %+v, want enabled manual summary 2/1/100", model, byModel[model])
	}
	if byModel[disabledModel].ChannelID != channelID || byModel[disabledModel].Enabled || byModel[disabledModel].Summary.FailedRequest != 1 || byModel[disabledModel].Summary.TotalTokens != 50 {
		t.Fatalf("model usage[%s] = %+v, want disabled failed summary", disabledModel, byModel[disabledModel])
	}
	if byModel[traceOnlyModel].ChannelID != channelID || byModel[traceOnlyModel].Source != "trace" || byModel[traceOnlyModel].Summary.RequestCount != 1 || byModel[traceOnlyModel].Summary.TotalTokens != 25 {
		t.Fatalf("model usage[%s] = %+v, want trace-only summary", traceOnlyModel, byModel[traceOnlyModel])
	}

	failures, err := st.GetChannelRecentFailures(channelID, base.Add(-time.Minute), 5)
	if err != nil {
		t.Fatalf("GetChannelRecentFailures(postgres) error = %v", err)
	}
	if len(failures) != 1 || failures[0].Model != disabledModel || failures[0].StatusCode != http.StatusBadGateway || failures[0].Reason != "upstream_5xx" {
		t.Fatalf("channel recent failures = %#v, want one disabled-model upstream_5xx failure", failures)
	}
}

func TestNewWithDatabaseRejectsUnsupportedDriver(t *testing.T) {
	t.Parallel()

	_, err := NewWithDatabase(t.TempDir(), "mysql", "mysql://example", 4, 4)
	if err == nil || !strings.Contains(err.Error(), `store driver "mysql" is not supported yet`) {
		t.Fatalf("NewWithDatabase(mysql) error = %v, want unsupported driver", err)
	}
}

func TestChannelConfigAndModelsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	record, err := st.UpsertChannelConfig(ChannelConfigRecord{
		ID:               "openai-primary",
		Name:             "OpenAI Primary",
		BaseURL:          "https://api.openai.com/v1",
		ProviderPreset:   "openai",
		APIType:          "chat_completions",
		Mode:             "responses_server",
		CapabilitiesJSON: `{"responses":false,"chat_completions":true,"tool_calling":true}`,
		APIKeyCiphertext: []byte("sk-secret"),
		APIKeyHint:       "sk-...cret",
		HeadersJSON:      `{"Authorization":"Bearer hidden","X-Test":"true"}`,
		Enabled:          true,
		Priority:         100,
		Weight:           1,
		CapacityHint:     1,
		ModelDiscovery:   "list_models",
	})
	if err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	if record.ID != "openai-primary" {
		t.Fatalf("record.ID = %q", record.ID)
	}
	if string(record.APIKeyCiphertext) != "sk-secret" {
		t.Fatalf("APIKeyCiphertext = %q", string(record.APIKeyCiphertext))
	}
	if record.HeadersJSON != `{"Authorization":"Bearer hidden","X-Test":"true"}` {
		t.Fatalf("HeadersJSON = %q", record.HeadersJSON)
	}
	if record.APIType != "chat_completions" || record.Mode != "responses_server" || record.CapabilitiesJSON != `{"responses":false,"chat_completions":true,"tool_calling":true}` {
		t.Fatalf("api surface = %q/%q/%s", record.APIType, record.Mode, record.CapabilitiesJSON)
	}

	var rawAPIKey []byte
	var rawHeaders string
	var rawAPIType, rawMode, rawCapabilities string
	if err := st.db.QueryRow(`SELECT api_key_ciphertext, headers_json, api_type, mode, capabilities_json FROM channel_configs WHERE id = ?`, "openai-primary").Scan(&rawAPIKey, &rawHeaders, &rawAPIType, &rawMode, &rawCapabilities); err != nil {
		t.Fatalf("query raw channel config error = %v", err)
	}
	if string(rawAPIKey) == "sk-secret" || !strings.HasPrefix(string(rawAPIKey), secretEnvelopeV1) {
		t.Fatalf("raw api_key_ciphertext = %q, want encrypted envelope", string(rawAPIKey))
	}
	if strings.Contains(rawHeaders, "Bearer hidden") || !strings.Contains(rawHeaders, secretEnvelopeV1) || !strings.Contains(rawHeaders, `"X-Test":"true"`) {
		t.Fatalf("raw headers_json = %q, want encrypted secret header and plaintext non-secret header", rawHeaders)
	}
	if rawAPIType != "chat_completions" || rawMode != "responses_server" || rawCapabilities != `{"responses":false,"chat_completions":true,"tool_calling":true}` {
		t.Fatalf("raw api surface = %q/%q/%s", rawAPIType, rawMode, rawCapabilities)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatalf("reopen New() error = %v", err)
	}
	defer reopened.Close()
	reopenedRecord, err := reopened.GetChannelConfig("openai-primary")
	if err != nil {
		t.Fatalf("reopened.GetChannelConfig() error = %v", err)
	}
	if string(reopenedRecord.APIKeyCiphertext) != "sk-secret" || reopenedRecord.HeadersJSON != record.HeadersJSON {
		t.Fatalf("reopened secrets = api_key %q headers %q", string(reopenedRecord.APIKeyCiphertext), reopenedRecord.HeadersJSON)
	}

	contextWindow := 128000
	maxOutputTokens := 4096
	compactThreshold := 7
	supportsChat := 1
	if err := st.ReplaceChannelModels("openai-primary", []ChannelModelRecord{
		{
			Model:                       "GPT-5",
			DisplayName:                 "GPT-5",
			Source:                      "manual",
			Enabled:                     true,
			SupportsChatCompletions:     &supportsChat,
			ContextWindow:               &contextWindow,
			MaxOutputTokens:             &maxOutputTokens,
			CompactHistoryItemThreshold: &compactThreshold,
			UpstreamModel:               "provider/gpt-5",
			ProfileSource:               "probe",
			ProfileAdoptionStatus:       "adopted",
		},
		{Model: "gpt-4.1", Source: "manual", Enabled: false},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}

	models, err := st.ListChannelModels("openai-primary", false)
	if err != nil {
		t.Fatalf("ListChannelModels(false) error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[1].Model != "gpt-5" ||
		models[1].ContextWindow == nil || *models[1].ContextWindow != contextWindow ||
		models[1].MaxOutputTokens == nil || *models[1].MaxOutputTokens != maxOutputTokens ||
		models[1].CompactHistoryItemThreshold == nil || *models[1].CompactHistoryItemThreshold != compactThreshold ||
		models[1].UpstreamModel != "provider/gpt-5" ||
		models[1].ProfileSource != "probe" ||
		models[1].ProfileAdoptionStatus != "adopted" {
		t.Fatalf("profile fields = %#v, want adopted profile round trip", models[1])
	}

	adoptedProfiles, err := st.ListAdoptedChannelModelProfiles()
	if err != nil {
		t.Fatalf("ListAdoptedChannelModelProfiles() error = %v", err)
	}
	if len(adoptedProfiles) != 1 || adoptedProfiles[0].Model != "gpt-5" || adoptedProfiles[0].UpstreamModel != "provider/gpt-5" {
		t.Fatalf("adoptedProfiles = %#v, want gpt-5 adopted profile", adoptedProfiles)
	}

	enabledModels, err := st.ListChannelModels("openai-primary", true)
	if err != nil {
		t.Fatalf("ListChannelModels(true) error = %v", err)
	}
	if len(enabledModels) != 1 || enabledModels[0].Model != "gpt-5" {
		t.Fatalf("enabledModels = %#v", enabledModels)
	}

	if err := st.SetChannelModelEnabled("openai-primary", "gpt-4.1", true); err != nil {
		t.Fatalf("SetChannelModelEnabled() error = %v", err)
	}
	enabledModels, err = st.ListChannelModels("openai-primary", true)
	if err != nil {
		t.Fatalf("ListChannelModels(true after enable) error = %v", err)
	}
	if len(enabledModels) != 2 {
		t.Fatalf("len(enabledModels after enable) = %d, want 2", len(enabledModels))
	}
}

func TestSecretStatusAndExportLocalSecretKey(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	status := st.SecretStatus()
	if status.Mode != "encrypted-local" {
		t.Fatalf("status.Mode = %q, want encrypted-local", status.Mode)
	}
	if !status.Exists || !status.Readable {
		t.Fatalf("status exists/readable = %v/%v, want true/true", status.Exists, status.Readable)
	}
	if status.KeyPath != filepath.Join(dir, localSecretKeyFile) {
		t.Fatalf("status.KeyPath = %q", status.KeyPath)
	}
	if status.Fingerprint == "" {
		t.Fatalf("status.Fingerprint is empty")
	}

	exported, exportStatus, err := st.ExportLocalSecretKey()
	if err != nil {
		t.Fatalf("ExportLocalSecretKey() error = %v", err)
	}
	if string(exported) == "" || !strings.HasSuffix(string(exported), "\n") {
		t.Fatalf("exported key = %q, want newline-terminated base64", string(exported))
	}
	if exportStatus.Fingerprint != status.Fingerprint {
		t.Fatalf("export fingerprint = %q, want %q", exportStatus.Fingerprint, status.Fingerprint)
	}

	if err := os.Remove(filepath.Join(dir, localSecretKeyFile)); err != nil {
		t.Fatalf("Remove(secret key) error = %v", err)
	}
	missing := st.SecretStatus()
	if missing.Exists || missing.Readable || missing.Error == "" {
		t.Fatalf("missing status = %+v, want missing key error", missing)
	}
	if _, _, err := st.ExportLocalSecretKey(); err == nil {
		t.Fatalf("ExportLocalSecretKey() error = nil, want missing key error")
	}
}

func TestRotateLocalSecretKeyReencryptsChannelSecrets(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	if _, err := st.UpsertChannelConfig(ChannelConfigRecord{
		ID:               "openai-primary",
		Name:             "OpenAI Primary",
		BaseURL:          "https://api.openai.com/v1",
		ProviderPreset:   "openai",
		APIKeyCiphertext: []byte("sk-rotate-secret"),
		APIKeyHint:       "sk-...cret",
		HeadersJSON:      `{"Authorization":"Bearer rotate","X-Test":"visible"}`,
		Enabled:          true,
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	beforeStatus := st.SecretStatus()
	var beforeAPIKey []byte
	var beforeHeaders string
	if err := st.db.QueryRow(`SELECT api_key_ciphertext, headers_json FROM channel_configs WHERE id = ?`, "openai-primary").Scan(&beforeAPIKey, &beforeHeaders); err != nil {
		t.Fatalf("query before raw channel config error = %v", err)
	}

	result, err := st.RotateLocalSecretKey()
	if err != nil {
		t.Fatalf("RotateLocalSecretKey() error = %v", err)
	}
	if result.OldFingerprint != beforeStatus.Fingerprint || result.NewFingerprint == "" || result.NewFingerprint == result.OldFingerprint {
		t.Fatalf("rotation result fingerprints = %+v, before=%q", result, beforeStatus.Fingerprint)
	}
	if result.ChannelCount != 1 || result.APIKeyCount != 1 || result.HeaderCount != 1 {
		t.Fatalf("rotation counts = %+v", result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("Stat(backupPath) error = %v", err)
	}

	var afterAPIKey []byte
	var afterHeaders string
	if err := st.db.QueryRow(`SELECT api_key_ciphertext, headers_json FROM channel_configs WHERE id = ?`, "openai-primary").Scan(&afterAPIKey, &afterHeaders); err != nil {
		t.Fatalf("query after raw channel config error = %v", err)
	}
	if string(afterAPIKey) == string(beforeAPIKey) {
		t.Fatalf("api key ciphertext did not change after rotation")
	}
	if afterHeaders == beforeHeaders {
		t.Fatalf("headers ciphertext did not change after rotation")
	}
	if strings.Contains(afterHeaders, "Bearer rotate") || !strings.Contains(afterHeaders, `"X-Test":"visible"`) {
		t.Fatalf("after headers_json = %q, want encrypted secret and plaintext non-secret", afterHeaders)
	}

	record, err := st.GetChannelConfig("openai-primary")
	if err != nil {
		t.Fatalf("GetChannelConfig() after rotate error = %v", err)
	}
	if string(record.APIKeyCiphertext) != "sk-rotate-secret" || record.HeadersJSON != `{"Authorization":"Bearer rotate","X-Test":"visible"}` {
		t.Fatalf("decrypted record after rotate = api_key %q headers %q", string(record.APIKeyCiphertext), record.HeadersJSON)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatalf("New(reopen) error = %v", err)
	}
	defer reopened.Close()
	reopenedRecord, err := reopened.GetChannelConfig("openai-primary")
	if err != nil {
		t.Fatalf("reopened.GetChannelConfig() error = %v", err)
	}
	if string(reopenedRecord.APIKeyCiphertext) != "sk-rotate-secret" || reopenedRecord.HeadersJSON != record.HeadersJSON {
		t.Fatalf("reopened record = api_key %q headers %q", string(reopenedRecord.APIKeyCiphertext), reopenedRecord.HeadersJSON)
	}
}

func TestModelCatalogAnalyticsCombinesChannelsAndLogs(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	for _, channel := range []ChannelConfigRecord{
		{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", ProviderPreset: "openai", HeadersJSON: "{}", Enabled: true},
		{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", ProviderPreset: "openrouter", HeadersJSON: "{}", Enabled: true},
	} {
		if _, err := st.UpsertChannelConfig(channel); err != nil {
			t.Fatalf("UpsertChannelConfig(%s) error = %v", channel.ID, err)
		}
	}
	if err := st.ReplaceChannelModels("openai", []ChannelModelRecord{{Model: "gpt-5", Source: "manual", Enabled: true}}); err != nil {
		t.Fatalf("ReplaceChannelModels(openai) error = %v", err)
	}
	if err := st.ReplaceChannelModels("openrouter", []ChannelModelRecord{{Model: "gpt-5", Source: "manual", Enabled: false}}); err != nil {
		t.Fatalf("ReplaceChannelModels(openrouter) error = %v", err)
	}

	writeAnalyticsLog := func(name string, upstreamID string, statusCode int, totalTokens int, recordedAt time.Time) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:          name,
				Time:               recordedAt,
				Model:              "gpt-5",
				URL:                "/v1/responses",
				Method:             "POST",
				StatusCode:         statusCode,
				DurationMs:         100,
				TTFTMs:             20,
				SelectedUpstreamID: upstreamID,
			},
			Usage: recordfile.UsageInfo{TotalTokens: totalTokens},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}
	now := time.Now().UTC()
	writeAnalyticsLog("success.http", "openai", 200, 100, now)
	writeAnalyticsLog("missing-usage.http", "openai", 200, 0, now)
	writeAnalyticsLog("failed.http", "openrouter", 500, 50, now)
	writeModelLog(t, st, dir, "list-models.http", "list_models", "/v1/models", "GET", "openai", 200, 0, now)

	items, err := st.ListModelCatalogAnalytics(now.Add(-24*time.Hour), startOfDayForTest(now))
	if err != nil {
		t.Fatalf("ListModelCatalogAnalytics() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item.Model != "gpt-5" || item.ChannelCount != 2 || item.EnabledChannelCount != 1 || item.ProviderCount != 2 {
		t.Fatalf("item = %+v", item)
	}
	if item.Summary.RequestCount != 3 || item.Summary.FailedRequest != 1 || item.Summary.MissingUsage != 1 || item.Summary.TotalTokens != 150 {
		t.Fatalf("summary = %+v", item.Summary)
	}

	detail, err := st.GetModelDetailAnalytics("gpt-5", now.Add(-24*time.Hour), startOfDayForTest(now), time.Hour, 24)
	if err != nil {
		t.Fatalf("GetModelDetailAnalytics() error = %v", err)
	}
	if len(detail.Channels) != 2 {
		t.Fatalf("len(detail.Channels) = %d, want 2", len(detail.Channels))
	}
	if len(detail.Trends) == 0 || detail.Trends[len(detail.Trends)-1].MissingUsage != 1 {
		t.Fatalf("trend missing usage not recorded: %+v", detail.Trends)
	}
	if detail.Trends[len(detail.Trends)-1].RequestCount != 3 || detail.Trends[len(detail.Trends)-1].FailedRequest != 1 {
		t.Fatalf("trend request/error counts = %+v", detail.Trends[len(detail.Trends)-1])
	}
	if len(detail.Trends) != 24 {
		t.Fatalf("len(detail.Trends) = %d, want 24", len(detail.Trends))
	}
}

func writeModelLog(t *testing.T, st *Store, dir string, name string, model string, url string, method string, upstreamID string, statusCode int, totalTokens int, recordedAt time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:          name,
			Time:               recordedAt,
			Model:              model,
			URL:                url,
			Method:             method,
			StatusCode:         statusCode,
			DurationMs:         100,
			TTFTMs:             20,
			SelectedUpstreamID: upstreamID,
		},
		Usage: recordfile.UsageInfo{TotalTokens: totalTokens},
	}
	if err := st.UpsertLog(path, header); err != nil {
		t.Fatalf("UpsertLog(%q) error = %v", name, err)
	}
}

func startOfDayForTest(now time.Time) time.Time {
	year, month, day := now.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestNewUpgradesLegacySchemaWithoutSessionColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

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
	CREATE INDEX idx_logs_model_recorded_at ON logs(model, recorded_at DESC);
	`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("db.Exec(legacySchema) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	rows, err := st.db.Query(`PRAGMA table_info(logs)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(logs) error = %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() error = %v", err)
	}

	for _, name := range []string{"session_id", "session_source", "window_id", "client_request_id"} {
		if !columns[name] {
			t.Fatalf("column %q missing after upgrade", name)
		}
	}
}

func TestStatsHandlesAverageTTFTAsFloat(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, statusCode int, ttftMs int64, totalTokens int) {
		t.Helper()

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}

		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
				Model:         "gpt-test",
				URL:           "/v1/chat/completions",
				Method:        "POST",
				StatusCode:    statusCode,
				DurationMs:    ttftMs,
				TTFTMs:        ttftMs,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
			Layout: recordfile.LayoutInfo{},
			Usage: recordfile.UsageInfo{
				TotalTokens: totalTokens,
			},
		}

		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
	}

	writeLog("success-a.http", 200, 800, 10)
	writeLog("success-b.http", 201, 813, 20)
	writeLog("failed.http", 500, 999, 99)

	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.TotalRequest != 3 {
		t.Fatalf("TotalRequest = %d, want 3", stats.TotalRequest)
	}
	if stats.SuccessRequest != 2 {
		t.Fatalf("SuccessRequest = %d, want 2", stats.SuccessRequest)
	}
	if stats.FailedRequest != 1 {
		t.Fatalf("FailedRequest = %d, want 1", stats.FailedRequest)
	}
	if stats.TotalTokens != 30 {
		t.Fatalf("TotalTokens = %d, want 30", stats.TotalTokens)
	}
	if stats.AvgTTFT != 807 {
		t.Fatalf("AvgTTFT = %d, want 807", stats.AvgTTFT)
	}
}

func TestSyncSkipsIncompleteHTTPFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	incompletePath := filepath.Join(dir, "in-progress.http")
	if err := os.WriteFile(incompletePath, []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", incompletePath, err)
	}

	validPath := filepath.Join(dir, "complete.http")
	validHeader := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "complete",
			Time:          time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
			Model:         "gpt-test",
			URL:           "/v1/chat/completions",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    20,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: 2,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: len64("POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\n\r\n"),
			ReqBodyLen:   len64(`{"x":1}`),
			ResHeaderLen: len64("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"),
			ResBodyLen:   len64(`{}`),
		},
	}
	prelude, err := recordfile.MarshalPrelude(validHeader, recordfile.BuildEvents(validHeader))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	validContent := string(prelude) +
		"POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\n\r\n" +
		`{"x":1}` + "\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}"
	if err := os.WriteFile(validPath, []byte(validContent), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", validPath, err)
	}

	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	entries, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].LogPath != validPath {
		t.Fatalf("entries[0].LogPath = %q, want %q", entries[0].LogPath, validPath)
	}
}

func TestExtractGroupingInfoPrefersSessionIDHeader(t *testing.T) {
	req := []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nSession_id: sess-123\r\nX-Codex-Window-Id: sess-123:0\r\nX-Client-Request-Id: req-123\r\n\r\n{}")
	info, err := extractGroupingInfoFromRequest(req)
	if err != nil {
		t.Fatalf("extractGroupingInfoFromRequest() error = %v", err)
	}
	if info.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want sess-123", info.SessionID)
	}
	if info.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", info.SessionSource)
	}
	if info.WindowID != "sess-123:0" {
		t.Fatalf("WindowID = %q, want sess-123:0", info.WindowID)
	}
	if info.ClientRequestID != "req-123" {
		t.Fatalf("ClientRequestID = %q, want req-123", info.ClientRequestID)
	}
}

func TestExtractGroupingInfoFallsBackToCodexMetadata(t *testing.T) {
	req := []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nX-Codex-Turn-Metadata: {\"session_id\":\"sess-meta\"}\r\n\r\n{}")
	info, err := extractGroupingInfoFromRequest(req)
	if err != nil {
		t.Fatalf("extractGroupingInfoFromRequest() error = %v", err)
	}
	if info.SessionID != "sess-meta" {
		t.Fatalf("SessionID = %q, want sess-meta", info.SessionID)
	}
	if info.SessionSource != "header.x_codex_turn_metadata.session_id" {
		t.Fatalf("SessionSource = %q", info.SessionSource)
	}
}

func TestExtractGroupingInfoRecognizesClaudeCodeSessionHeader(t *testing.T) {
	req := []byte("POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nX-Claude-Code-Session-Id: sess-claude-code\r\nX-Codex-Turn-Metadata: {\"session_id\":\"sess-meta\"}\r\n\r\n{}")
	info, err := extractGroupingInfoFromRequest(req)
	if err != nil {
		t.Fatalf("extractGroupingInfoFromRequest() error = %v", err)
	}
	if info.SessionID != "sess-claude-code" {
		t.Fatalf("SessionID = %q, want sess-claude-code", info.SessionID)
	}
	if info.SessionSource != "header.x_claude_code_session_id" {
		t.Fatalf("SessionSource = %q, want header.x_claude_code_session_id", info.SessionSource)
	}
}

func TestNewBackfillsGroupingForLegacyNoneRows(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "codex.http")
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-codex",
			Time:          time.Date(2026, 4, 21, 9, 2, 55, 0, time.UTC),
			Model:         "gpt-5.4",
			Provider:      "openai_compatible",
			Operation:     "responses.create",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    100,
			TTFTMs:        20,
			ClientIP:      "127.0.0.1",
			ContentLength: 2,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: len64("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nSession_id: sess-fixed\r\nX-Client-Request-Id: req-fixed\r\nX-Codex-Window-Id: sess-fixed:2\r\n\r\n"),
			ReqBodyLen:   len64(`{}`),
			ResHeaderLen: len64("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"),
			ResBodyLen:   len64(`{}`),
			IsStream:     true,
		},
	}
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	content := string(prelude) +
		"POST /v1/responses HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Session_id: sess-fixed\r\n" +
		"X-Client-Request-Id: req-fixed\r\n" +
		"X-Codex-Window-Id: sess-fixed:2\r\n" +
		"\r\n" +
		"{}\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", logPath, err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", logPath, err)
	}

	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacyInsert := `
	CREATE TABLE IF NOT EXISTS logs (
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
		is_stream INTEGER NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		session_source TEXT NOT NULL DEFAULT '',
		window_id TEXT NOT NULL DEFAULT '',
		client_request_id TEXT NOT NULL DEFAULT ''
	);`
	if _, err := db.Exec(legacyInsert); err != nil {
		t.Fatalf("db.Exec(schema) error = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO logs (
			path, trace_id, mod_time_ns, file_size, version, request_id, recorded_at, model, provider, operation, endpoint, url, method,
			status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		logPath,
		"trace-legacy",
		info.ModTime().UnixNano(),
		info.Size(),
		header.Version,
		header.Meta.RequestID,
		header.Meta.Time.UTC().Format(timeLayout),
		header.Meta.Model,
		header.Meta.Provider,
		header.Meta.Operation,
		header.Meta.Endpoint,
		header.Meta.URL,
		header.Meta.Method,
		header.Meta.StatusCode,
		header.Meta.DurationMs,
		header.Meta.TTFTMs,
		header.Meta.ClientIP,
		header.Meta.ContentLength,
		"",
		0,
		0,
		0,
		0,
		header.Layout.ReqHeaderLen,
		header.Layout.ReqBodyLen,
		header.Layout.ResHeaderLen,
		header.Layout.ResBodyLen,
		boolToInt(header.Layout.IsStream),
		"",
		"none",
		"",
		"",
	); err != nil {
		t.Fatalf("db.Exec(insert) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	entry, err := st.GetByID("trace-legacy")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if entry.SessionID != "sess-fixed" {
		t.Fatalf("SessionID = %q, want sess-fixed", entry.SessionID)
	}
	if entry.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", entry.SessionSource)
	}
	if entry.WindowID != "sess-fixed:2" {
		t.Fatalf("WindowID = %q, want sess-fixed:2", entry.WindowID)
	}
	if entry.ClientRequestID != "req-fixed" {
		t.Fatalf("ClientRequestID = %q, want req-fixed", entry.ClientRequestID)
	}

	var recordedAtType string
	rows, err := st.db.Query(`PRAGMA table_info(logs)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(logs) error = %v", err)
	}
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		if name == "recorded_at" {
			recordedAtType = typ
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close() error = %v", err)
	}
	if !strings.EqualFold(recordedAtType, "datetime") {
		t.Fatalf("recorded_at type = %q, want datetime", recordedAtType)
	}
	row, err := st.client.TraceLog.Query().Where(tracelog.TraceIDEQ("trace-legacy")).Only(context.Background())
	if err != nil {
		t.Fatalf("ent TraceLog.Query() error = %v", err)
	}
	if !row.RecordedAt.Equal(header.Meta.Time.UTC()) {
		t.Fatalf("ent RecordedAt = %s, want %s", row.RecordedAt, header.Meta.Time.UTC())
	}
}

func TestDatasetRoundTripAndDedupAppend(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, requestID string) string {
		t.Helper()

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     requestID,
				Time:          time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
				Model:         "gpt-5.1-codex",
				Provider:      "openai_compatible",
				Operation:     "responses.create",
				Endpoint:      "v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    200,
				DurationMs:    100,
				TTFTMs:        10,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
		entry, err := st.GetByRequestID(requestID)
		if err != nil {
			t.Fatalf("GetByRequestID(%q) error = %v", requestID, err)
		}
		return entry.ID
	}

	traceA := writeLog("a.http", "req-a")
	traceB := writeLog("b.http", "req-b")

	dataset, err := st.CreateDataset("smoke", "dataset desc")
	if err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}
	added, skipped, err := st.AppendDatasetExamples(dataset.ID, []string{traceA, traceB, traceA}, "trace_list", "", "note")
	if err != nil {
		t.Fatalf("AppendDatasetExamples() error = %v", err)
	}
	if added != 2 || skipped != 0 {
		t.Fatalf("AppendDatasetExamples() added/skipped = %d/%d, want 2/0", added, skipped)
	}
	added, skipped, err = st.AppendDatasetExamples(dataset.ID, []string{traceB}, "trace_list", "", "")
	if err != nil {
		t.Fatalf("AppendDatasetExamples() second error = %v", err)
	}
	if added != 0 || skipped != 1 {
		t.Fatalf("second append added/skipped = %d/%d, want 0/1", added, skipped)
	}

	got, err := st.GetDataset(dataset.ID)
	if err != nil {
		t.Fatalf("GetDataset() error = %v", err)
	}
	if got.ExampleCount != 2 {
		t.Fatalf("GetDataset().ExampleCount = %d, want 2", got.ExampleCount)
	}

	items, err := st.GetDatasetExamples(dataset.ID)
	if err != nil {
		t.Fatalf("GetDatasetExamples() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(GetDatasetExamples()) = %d, want 2", len(items))
	}
	if items[0].Position != 1 || items[1].Position != 2 {
		t.Fatalf("positions = %d,%d, want 1,2", items[0].Position, items[1].Position)
	}
	if items[0].TraceID != traceA || items[1].TraceID != traceB {
		t.Fatalf("trace order = %q,%q, want %q,%q", items[0].TraceID, items[1].TraceID, traceA, traceB)
	}

	list, err := st.ListDatasets()
	if err != nil {
		t.Fatalf("ListDatasets() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != dataset.ID {
		t.Fatalf("ListDatasets() = %#v, want one dataset %q", list, dataset.ID)
	}
}

func TestEvalRunAndScoresRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	path := filepath.Join(dir, "trace.http")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-score",
			Time:          time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1-codex",
			Provider:      "openai_compatible",
			Operation:     "responses.create",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    100,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: 4,
		},
	}
	if err := st.UpsertLog(path, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}
	entry, err := st.GetByRequestID("req-score")
	if err != nil {
		t.Fatalf("GetByRequestID() error = %v", err)
	}

	dataset, err := st.CreateDataset("eval-dataset", "")
	if err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}
	run, err := st.CreateEvalRun(dataset.ID, "dataset", dataset.ID, "baseline_v1", 1)
	if err != nil {
		t.Fatalf("CreateEvalRun() error = %v", err)
	}
	score, err := st.AddScore(ScoreRecord{
		TraceID:      entry.ID,
		SessionID:    entry.SessionID,
		DatasetID:    dataset.ID,
		EvalRunID:    run.ID,
		EvaluatorKey: "http_status_2xx",
		Value:        1,
		Status:       "pass",
		Label:        "pass",
		Explanation:  "ok",
	})
	if err != nil {
		t.Fatalf("AddScore() error = %v", err)
	}
	if err := st.FinalizeEvalRun(run.ID, 1, 1, 0); err != nil {
		t.Fatalf("FinalizeEvalRun() error = %v", err)
	}

	gotRun, err := st.GetEvalRun(run.ID)
	if err != nil {
		t.Fatalf("GetEvalRun() error = %v", err)
	}
	if gotRun.ScoreCount != 1 || gotRun.PassCount != 1 || gotRun.FailCount != 0 {
		t.Fatalf("GetEvalRun() = %#v, want score/pass/fail = 1/1/0", gotRun)
	}

	runs, err := st.ListEvalRuns(10)
	if err != nil {
		t.Fatalf("ListEvalRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("ListEvalRuns() = %#v, want run %q", runs, run.ID)
	}

	scores, err := st.ListScores(ScoreFilter{EvalRunID: run.ID}, 10)
	if err != nil {
		t.Fatalf("ListScores(eval_run) error = %v", err)
	}
	if len(scores) != 1 || scores[0].ID != score.ID {
		t.Fatalf("ListScores(eval_run) = %#v, want score %q", scores, score.ID)
	}

	scores, err = st.ListScores(ScoreFilter{DatasetID: dataset.ID, TraceID: entry.ID}, 10)
	if err != nil {
		t.Fatalf("ListScores(dataset+trace) error = %v", err)
	}
	if len(scores) != 1 || scores[0].EvaluatorKey != "http_status_2xx" {
		t.Fatalf("ListScores(dataset+trace) = %#v, want http_status_2xx", scores)
	}
}

func TestExperimentRunRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	baselineRun, err := st.CreateEvalRun("", "trace_list", "", "baseline_v1", 2)
	if err != nil {
		t.Fatalf("CreateEvalRun(baseline) error = %v", err)
	}
	candidateRun, err := st.CreateEvalRun("", "trace_list", "", "baseline_v1", 2)
	if err != nil {
		t.Fatalf("CreateEvalRun(candidate) error = %v", err)
	}

	experiment, err := st.CreateExperimentRun(ExperimentRunRecord{
		Name:                "baseline-vs-candidate",
		Description:         "first experiment",
		BaselineEvalRunID:   baselineRun.ID,
		CandidateEvalRunID:  candidateRun.ID,
		BaselineScoreCount:  6,
		CandidateScoreCount: 6,
		BaselinePassRate:    50,
		CandidatePassRate:   66.67,
		PassRateDelta:       16.67,
		MatchedScoreCount:   6,
		ImprovementCount:    2,
		RegressionCount:     1,
	})
	if err != nil {
		t.Fatalf("CreateExperimentRun() error = %v", err)
	}

	got, err := st.GetExperimentRun(experiment.ID)
	if err != nil {
		t.Fatalf("GetExperimentRun() error = %v", err)
	}
	if got.BaselineEvalRunID != baselineRun.ID || got.CandidateEvalRunID != candidateRun.ID {
		t.Fatalf("GetExperimentRun() = %#v, want eval runs %q and %q", got, baselineRun.ID, candidateRun.ID)
	}
	if got.ImprovementCount != 2 || got.RegressionCount != 1 || got.MatchedScoreCount != 6 {
		t.Fatalf("GetExperimentRun() = %#v, want counts 2/1/6", got)
	}

	runs, err := st.ListExperimentRuns(10)
	if err != nil {
		t.Fatalf("ListExperimentRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != experiment.ID {
		t.Fatalf("ListExperimentRuns() = %#v, want experiment %q", runs, experiment.ID)
	}
}

func TestListSessionPageAggregatesBySession(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, statusCode int, ttftMs int64, totalTokens int, sessionID string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          recordedAt,
				Model:         "gpt-test",
				Provider:      "openai_compatible",
				Operation:     "responses",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    statusCode,
				DurationMs:    ttftMs + 200,
				TTFTMs:        ttftMs,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
			Layout: recordfile.LayoutInfo{
				IsStream: true,
			},
			Usage: recordfile.UsageInfo{
				TotalTokens: totalTokens,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "header.session_id",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	base := time.Date(2026, 4, 16, 8, 0, 0, 0, time.UTC)
	writeLog("a.http", base, 200, 120, 10, "sess-a")
	writeLog("b.http", base.Add(2*time.Minute), 500, 200, 99, "sess-a")
	writeLog("c.http", base.Add(3*time.Minute), 200, 150, 30, "sess-b")

	result, err := st.ListSessionPage(1, 50, ListFilter{})
	if err != nil {
		t.Fatalf("ListSessionPage() error = %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(result.Items) = %d, want 2", len(result.Items))
	}
	if result.Items[0].SessionID != "sess-b" {
		t.Fatalf("first SessionID = %q, want sess-b", result.Items[0].SessionID)
	}
	if result.Items[1].SessionID != "sess-a" {
		t.Fatalf("second SessionID = %q, want sess-a", result.Items[1].SessionID)
	}
	if result.Items[1].RequestCount != 2 {
		t.Fatalf("RequestCount = %d, want 2", result.Items[1].RequestCount)
	}
	if result.Items[1].SuccessRequest != 1 || result.Items[1].FailedRequest != 1 {
		t.Fatalf("success/failed = %d/%d, want 1/1", result.Items[1].SuccessRequest, result.Items[1].FailedRequest)
	}
	if result.Items[1].TotalTokens != 10 {
		t.Fatalf("TotalTokens = %d, want 10", result.Items[1].TotalTokens)
	}
}

func TestListPageAppliesFilters(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, model string, provider string, sessionID string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 4, 16, 9, 0, 0, 0, time.UTC),
				Model:         model,
				Provider:      provider,
				Operation:     "responses",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    200,
				DurationMs:    30,
				TTFTMs:        10,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "header.session_id",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	writeLog("alpha.http", "gpt-alpha", "openai_compatible", "sess-alpha")
	writeLog("beta.http", "gemini-pro", "google_genai", "sess-beta")

	result, err := st.ListPage(1, 50, ListFilter{Provider: "google_genai", Query: "sess-beta"})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(result.Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].Header.Meta.Model != "gemini-pro" {
		t.Fatalf("model = %q, want gemini-pro", result.Items[0].Header.Meta.Model)
	}
}

func TestListPageAppliesRoutingDecisionFilters(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, statusCode int, durationMs int64, ttftMs int64, totalTokens int, upstreamID string, errorText string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:          name,
				Time:               time.Date(2026, 4, 16, 9, 0, 0, 0, time.UTC),
				Model:              "gpt-5",
				Provider:           "openai_compatible",
				Operation:          "responses",
				Endpoint:           "/v1/responses",
				URL:                "/v1/responses",
				Method:             "POST",
				StatusCode:         statusCode,
				DurationMs:         durationMs,
				TTFTMs:             ttftMs,
				SelectedUpstreamID: upstreamID,
				Error:              errorText,
			},
			Usage: recordfile.UsageInfo{TotalTokens: totalTokens},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	writeLog("ok.http", 200, 240, 40, 120, "openai-primary", "")
	writeLog("slow-error.http", 429, 1500, 320, 900, "openai-secondary", "rate limited")

	result, err := st.ListPage(1, 50, ListFilter{
		SelectedUpstream: "secondary",
		Status:           "error",
		MinDurationMs:    1000,
		MinTTFTMs:        300,
		MinTokens:        800,
		MaxTokens:        1000,
	})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(result.Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].Header.Meta.RequestID != "slow-error.http" {
		t.Fatalf("request id = %q, want slow-error.http", result.Items[0].Header.Meta.RequestID)
	}

	result, err = st.ListPage(1, 50, ListFilter{Status: "success", MaxDurationMs: 300, MaxTTFTMs: 50, MaxTokens: 200})
	if err != nil {
		t.Fatalf("ListPage(success) error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Header.Meta.RequestID != "ok.http" {
		t.Fatalf("success result = %+v", result.Items)
	}
}

func TestListPageAppliesObservationStatusFilter(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:  name,
				Time:       time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC),
				Model:      "gpt-5",
				Provider:   "openai_compatible",
				Operation:  "responses",
				Endpoint:   "/v1/responses",
				URL:        "/v1/responses",
				Method:     "POST",
				StatusCode: 200,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
		entry, err := st.GetByRequestID(name)
		if err != nil {
			t.Fatalf("GetByRequestID(%q) error = %v", name, err)
		}
		return entry.ID
	}

	parsedID := writeLog("parsed.http")
	unparsedID := writeLog("unparsed.http")
	if err := st.SaveObservation(observe.TraceObservation{
		TraceID:       parsedID,
		Provider:      "openai_compatible",
		Operation:     "responses",
		Model:         "gpt-5",
		Parser:        "openai",
		ParserVersion: "0.1.0",
		Status:        observe.ParseStatusParsed,
	}); err != nil {
		t.Fatalf("SaveObservation() error = %v", err)
	}

	result, err := st.ListPage(1, 50, ListFilter{ObservationStatus: "parsed"})
	if err != nil {
		t.Fatalf("ListPage(parsed) error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != parsedID || result.Items[0].Observation.Status != "parsed" {
		t.Fatalf("parsed result = %+v", result.Items)
	}

	result, err = st.ListPage(1, 50, ListFilter{ObservationStatus: "unparsed"})
	if err != nil {
		t.Fatalf("ListPage(unparsed) error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != unparsedID || result.Items[0].Observation.Status != "unparsed" {
		t.Fatalf("unparsed result = %+v", result.Items)
	}
}

func TestListSessionPageAppliesFilters(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, model string, provider string, sessionID string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC),
				Model:         model,
				Provider:      provider,
				Operation:     "responses",
				Endpoint:      "/v1/responses",
				URL:           "/v1/responses",
				Method:        "POST",
				StatusCode:    200,
				DurationMs:    30,
				TTFTMs:        10,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{
			SessionID:     sessionID,
			SessionSource: "header.session_id",
		}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	writeLog("codex.http", "gpt-5-codex", "openai_compatible", "sess-codex")
	writeLog("google.http", "gemini-pro", "google_genai", "sess-google")

	result, err := st.ListSessionPage(1, 50, ListFilter{Model: "codex"})
	if err != nil {
		t.Fatalf("ListSessionPage() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(result.Items) = %d, want 1", len(result.Items))
	}
	if result.Items[0].SessionID != "sess-codex" {
		t.Fatalf("SessionID = %q, want sess-codex", result.Items[0].SessionID)
	}
}

func len64(v string) int64 {
	return int64(len(v))
}

func TestUpstreamCatalogPersistenceAndRoutingMetadata(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	if err := st.UpsertUpstreamTarget(UpstreamTargetRecord{
		ID:                "openai-primary",
		BaseURL:           "https://api.openai.com/v1",
		ProviderPreset:    "openai",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          100,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     time.Date(2026, 4, 17, 7, 0, 0, 0, time.UTC),
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}
	if err := st.ReplaceUpstreamModels("openai-primary", []UpstreamModelRecord{
		{UpstreamID: "openai-primary", Model: "gpt-5", Source: "catalog", SeenAt: time.Date(2026, 4, 17, 7, 0, 0, 0, time.UTC)},
		{UpstreamID: "openai-primary", Model: "gpt-4.1", Source: "catalog", SeenAt: time.Date(2026, 4, 17, 7, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatalf("ReplaceUpstreamModels() error = %v", err)
	}

	logPath := filepath.Join(dir, "trace.http")
	if err := os.WriteFile(logPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:                      "req-1",
			Time:                           time.Date(2026, 4, 17, 7, 1, 0, 0, time.UTC),
			Model:                          "gpt-5",
			Provider:                       "openai_compatible",
			Operation:                      "responses",
			Endpoint:                       "/v1/responses",
			URL:                            "/v1/responses",
			Method:                         "POST",
			StatusCode:                     200,
			DurationMs:                     42,
			TTFTMs:                         11,
			ClientIP:                       "127.0.0.1",
			ContentLength:                  12,
			SelectedUpstreamID:             "openai-primary",
			SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
			SelectedUpstreamProviderPreset: "openai",
			RoutingPolicy:                  "p2c",
			RoutingScore:                   1.25,
			RoutingCandidateCount:          2,
		},
	}
	if err := st.UpsertLog(logPath, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}

	entry, err := st.GetByID(mustTraceID(t, st, logPath))
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if entry.Header.Meta.SelectedUpstreamID != "openai-primary" {
		t.Fatalf("SelectedUpstreamID = %q, want openai-primary", entry.Header.Meta.SelectedUpstreamID)
	}
	if entry.Header.Meta.RoutingPolicy != "p2c" {
		t.Fatalf("RoutingPolicy = %q, want p2c", entry.Header.Meta.RoutingPolicy)
	}
	if entry.Header.Meta.RoutingCandidateCount != 2 {
		t.Fatalf("RoutingCandidateCount = %d, want 2", entry.Header.Meta.RoutingCandidateCount)
	}

	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE upstream_id = ?`, "openai-primary").Scan(&count); err != nil {
		t.Fatalf("QueryRow() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("upstream_models count = %d, want 2", count)
	}
}

func TestListUpstreamAnalyticsAggregatesLogs(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, statusCode int, model string, tokens int, ttft int64, errText string) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      name,
				Time:                           time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC).Add(time.Duration(len(name)) * time.Minute),
				Model:                          model,
				Provider:                       "openai_compatible",
				Operation:                      "responses",
				Endpoint:                       "/v1/responses",
				URL:                            "/v1/responses",
				Method:                         "POST",
				StatusCode:                     statusCode,
				DurationMs:                     40,
				TTFTMs:                         ttft,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  8,
				Error:                          errText,
				SelectedUpstreamID:             "openai-primary",
				SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
				RoutingCandidateCount:          2,
			},
			Usage: recordfile.UsageInfo{
				PromptTokens:     tokens / 2,
				CompletionTokens: tokens / 2,
				TotalTokens:      tokens,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	writeLog("ok-a.http", 200, "gpt-5", 20, 100, "")
	writeLog("ok-b.http", 200, "gpt-5", 30, 120, "")
	writeLog("fail.http", 503, "gpt-4.1", 0, 0, "upstream overloaded")

	analytics, err := st.ListUpstreamAnalytics(5, 3, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListUpstreamAnalytics() error = %v", err)
	}
	if len(analytics) != 1 {
		t.Fatalf("len(analytics) = %d, want 1", len(analytics))
	}
	got := analytics[0]
	if got.UpstreamID != "openai-primary" {
		t.Fatalf("UpstreamID = %q, want openai-primary", got.UpstreamID)
	}
	if got.RequestCount != 3 || got.SuccessRequest != 2 || got.FailedRequest != 1 {
		t.Fatalf("counts = %+v", got)
	}
	if got.TotalTokens != 50 {
		t.Fatalf("TotalTokens = %d, want 50", got.TotalTokens)
	}
	if got.AvgTTFT != 110 {
		t.Fatalf("AvgTTFT = %d, want 110", got.AvgTTFT)
	}
	if got.LastModel == "" || len(got.Models) == 0 {
		t.Fatalf("model coverage missing: %+v", got)
	}
	if len(got.RecentErrors) != 1 {
		t.Fatalf("RecentErrors = %#v, want 1 error", got.RecentErrors)
	}
	if len(got.RecentFailures) != 1 || got.RecentFailures[0].TraceID == "" {
		t.Fatalf("RecentFailures = %#v, want 1 traced failure", got.RecentFailures)
	}
}

func TestGetUpstreamDetailReturnsBreakdownAndRecentTraces(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, endpoint string, model string, statusCode int, tokens int, errText string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      name,
				Time:                           recordedAt,
				Model:                          model,
				Provider:                       "openai_compatible",
				Operation:                      "responses",
				Endpoint:                       endpoint,
				URL:                            endpoint,
				Method:                         "POST",
				StatusCode:                     statusCode,
				DurationMs:                     40,
				TTFTMs:                         90,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  6,
				Error:                          errText,
				SelectedUpstreamID:             "openai-primary",
				SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
			},
			Usage: recordfile.UsageInfo{
				PromptTokens:     tokens / 2,
				CompletionTokens: tokens / 2,
				TotalTokens:      tokens,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	base := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	writeLog("match-a.http", base.Add(1*time.Minute), "/v1/responses", "gpt-5", 200, 20, "")
	writeLog("match-b.http", base.Add(2*time.Minute), "/v1/chat/completions", "gpt-5", 503, 0, "upstream overloaded")
	writeLog("other-model.http", base.Add(3*time.Minute), "/v1/responses", "gemini-2.5-flash", 200, 10, "")

	detail, err := st.GetUpstreamDetail("openai-primary", time.Time{}, "gpt-5", 10, time.Minute, 4)
	if err != nil {
		t.Fatalf("GetUpstreamDetail() error = %v", err)
	}
	if detail.Analytics.UpstreamID != "openai-primary" {
		t.Fatalf("UpstreamID = %q, want openai-primary", detail.Analytics.UpstreamID)
	}
	if len(detail.Traces) != 2 {
		t.Fatalf("len(Traces) = %d, want 2", len(detail.Traces))
	}
	if detail.Traces[0].Header.Meta.RequestID != "match-b.http" {
		t.Fatalf("most recent trace = %q, want match-b.http", detail.Traces[0].Header.Meta.RequestID)
	}
	if len(detail.Models) != 1 || detail.Models[0].Label != "gpt-5" || detail.Models[0].Count != 2 {
		t.Fatalf("Models = %#v, want gpt-5 x 2", detail.Models)
	}
	if len(detail.Endpoints) != 2 {
		t.Fatalf("Endpoints = %#v, want 2 items", detail.Endpoints)
	}
	if len(detail.FailureReasons) != 1 || detail.FailureReasons[0].Label != "upstream_overloaded" || detail.FailureReasons[0].Count != 1 {
		t.Fatalf("FailureReasons = %#v, want upstream_overloaded x 1", detail.FailureReasons)
	}
	if len(detail.Analytics.RecentFailures) != 1 || detail.Analytics.RecentFailures[0].Model != "gpt-5" {
		t.Fatalf("RecentFailures = %#v, want one gpt-5 failure", detail.Analytics.RecentFailures)
	}
	if detail.Analytics.RecentFailures[0].Reason != "upstream_overloaded" {
		t.Fatalf("RecentFailure reason = %q, want upstream_overloaded", detail.Analytics.RecentFailures[0].Reason)
	}
	if len(detail.Timeline) != 4 {
		t.Fatalf("len(Timeline) = %d, want 4", len(detail.Timeline))
	}
	totalTimeline := 0
	for _, item := range detail.Timeline {
		totalTimeline += item.Count
	}
	if totalTimeline != 1 {
		t.Fatalf("timeline total = %d, want 1", totalTimeline)
	}
}

func TestGetRoutingFailureAnalyticsAggregatesReasonsAndRecent(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, model string, reason string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:            name,
				Time:                 recordedAt,
				Model:                model,
				Provider:             "openai_compatible",
				Operation:            "responses",
				Endpoint:             "/v1/responses",
				URL:                  "/v1/responses",
				Method:               "POST",
				StatusCode:           502,
				DurationMs:           12,
				TTFTMs:               0,
				ClientIP:             "127.0.0.1",
				ContentLength:        8,
				Error:                "selection failed",
				RoutingPolicy:        "p2c",
				RoutingFailureReason: reason,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	base := time.Date(2026, 4, 18, 11, 0, 0, 0, time.UTC)
	writeLog("reason-a.http", base.Add(1*time.Minute), "gpt-5", "no_supporting_target")
	writeLog("reason-b.http", base.Add(2*time.Minute), "gpt-5", "no_supporting_target")
	writeLog("reason-c.http", base.Add(3*time.Minute), "gpt-5", "all_targets_open")
	writeLog("other-model.http", base.Add(4*time.Minute), "gemini-2.5-flash", "no_supporting_target")

	analytics, err := st.GetRoutingFailureAnalytics(time.Time{}, "gpt-5", 5, 5, time.Hour, 6)
	if err != nil {
		t.Fatalf("GetRoutingFailureAnalytics() error = %v", err)
	}
	if analytics.Total != 3 {
		t.Fatalf("Total = %d, want 3", analytics.Total)
	}
	if len(analytics.Reasons) != 2 {
		t.Fatalf("Reasons = %#v, want 2 items", analytics.Reasons)
	}
	if analytics.Reasons[0].Label != "no_supporting_target" || analytics.Reasons[0].Count != 2 {
		t.Fatalf("top reason = %#v, want no_supporting_target x2", analytics.Reasons[0])
	}
	if len(analytics.Recent) != 3 {
		t.Fatalf("Recent = %#v, want 3 items", analytics.Recent)
	}
	if analytics.Recent[0].Reason != "all_targets_open" {
		t.Fatalf("most recent reason = %q, want all_targets_open", analytics.Recent[0].Reason)
	}
	if len(analytics.Timeline) != 6 {
		t.Fatalf("Timeline = %#v, want 6 buckets", analytics.Timeline)
	}
	totalTimeline := 0
	for _, item := range analytics.Timeline {
		totalTimeline += item.Count
	}
	if totalTimeline != 3 {
		t.Fatalf("timeline total = %d, want 3", totalTimeline)
	}
}

func TestSaveObservationPersistsSummaryAndSemanticNodes(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	root := observe.SemanticNode{
		ID:             "node-root",
		ProviderType:   "message",
		NormalizedType: observe.NodeMessage,
		Path:           "$.output[0]",
		Index:          0,
		Children: []observe.SemanticNode{{
			ID:             "node-child",
			ProviderType:   "output_text",
			NormalizedType: observe.NodeText,
			Path:           "$.output[0].content[0]",
			Index:          0,
			Text:           "hello",
		}},
	}
	obs := observe.TraceObservation{
		TraceID:       "trace-observe",
		Provider:      "openai_compatible",
		Operation:     "responses",
		Model:         "gpt-5.1",
		Parser:        "openai",
		ParserVersion: "0.1.0",
		Status:        observe.ParseStatusParsed,
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{root},
		},
		Warnings: []observe.ParseWarning{{Code: "fixture", Message: "test warning"}},
	}
	if err := st.SaveObservation(obs); err != nil {
		t.Fatalf("SaveObservation() error = %v", err)
	}

	summary, err := st.GetObservationSummary("trace-observe")
	if err != nil {
		t.Fatalf("GetObservationSummary() error = %v", err)
	}
	if summary.Parser != "openai" || summary.Status != string(observe.ParseStatusParsed) {
		t.Fatalf("summary = %+v", summary)
	}

	nodes, err := st.ListSemanticNodes("trace-observe")
	if err != nil {
		t.Fatalf("ListSemanticNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	tree := observe.RebuildNodeTree(nodes)
	if len(tree) != 1 || len(tree[0].Children) != 1 || tree[0].Children[0].Text != "hello" {
		t.Fatalf("tree = %+v", tree)
	}
}

func TestSaveObservationDeduplicatesSemanticNodes(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	toolCall := observe.SemanticNode{
		ID:             "node-tool-call",
		ProviderType:   "tool_call",
		NormalizedType: observe.NodeToolCall,
		Path:           "$.stream.tool_calls[0]",
		Index:          0,
		Text:           "lookup",
	}
	obs := observe.TraceObservation{
		TraceID:       "trace-observe-duplicate",
		Provider:      "openai_compatible",
		Operation:     "chat.completions",
		Model:         "gpt-5.1",
		Parser:        "openai",
		ParserVersion: "0.1.0",
		Status:        observe.ParseStatusParsed,
		Response: observe.ObservationResponse{
			Nodes: []observe.SemanticNode{toolCall},
		},
		Stream: observe.ObservationStream{
			AccumulatedToolCalls: []observe.SemanticNode{toolCall},
		},
	}
	if err := st.SaveObservation(obs); err != nil {
		t.Fatalf("SaveObservation() error = %v", err)
	}
	if err := st.SaveObservation(obs); err != nil {
		t.Fatalf("second SaveObservation() error = %v", err)
	}

	nodes, err := st.ListSemanticNodes("trace-observe-duplicate")
	if err != nil {
		t.Fatalf("ListSemanticNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	if nodes[0].Node.ID != "node-tool-call" {
		t.Fatalf("node id = %q, want node-tool-call", nodes[0].Node.ID)
	}
}

func TestSystemEventUpsertDeduplicatesAndReopens(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	first, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: "parser:save_observation:semantic_nodes_unique_constraint",
		Source:      "parser",
		Category:    "parse_failure",
		Severity:    "error",
		Title:       "Observation parse job failed",
		Message:     "constraint failed",
		TraceID:     "trace-system-event",
		JobID:       "36",
		DetailsJSON: json.RawMessage(`{"parser":"openai"}`),
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent() error = %v", err)
	}
	if first.ID == "" || first.Status != SystemEventStatusUnread || first.OccurrenceCount != 1 {
		t.Fatalf("first event = %+v", first)
	}
	if err := st.MarkSystemEventRead(first.ID); err != nil {
		t.Fatalf("MarkSystemEventRead() error = %v", err)
	}
	read, err := st.GetSystemEvent(first.ID)
	if err != nil {
		t.Fatalf("GetSystemEvent(read) error = %v", err)
	}
	if read.Status != SystemEventStatusRead || read.ReadAt.IsZero() {
		t.Fatalf("read event = %+v", read)
	}

	second, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: first.Fingerprint,
		Source:      "parser",
		Category:    "parse_failure",
		Severity:    "critical",
		Title:       "Observation parse job failed again",
		Message:     "constraint failed again",
		TraceID:     "trace-system-event-2",
		JobID:       "37",
	})
	if err != nil {
		t.Fatalf("second UpsertSystemEvent() error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second id = %q, want %q", second.ID, first.ID)
	}
	if second.Status != SystemEventStatusUnread || second.OccurrenceCount != 2 || !second.ReadAt.IsZero() {
		t.Fatalf("second event = %+v", second)
	}
	if second.Severity != "critical" || second.TraceID != "trace-system-event-2" {
		t.Fatalf("updated event = %+v", second)
	}
}

func TestSystemEventsListSummaryAndStatusActions(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	parserEvent, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: "parser:trace-a:bad-json",
		Source:      "parser",
		Category:    "parse_failure",
		Severity:    "error",
		Title:       "Parse failed",
		Message:     "bad json",
		TraceID:     "trace-a",
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(parser) error = %v", err)
	}
	routerEvent, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: "router:gpt-5.5:all_targets_open",
		Source:      "router",
		Category:    "routing_failure",
		Severity:    "warning",
		Title:       "Routing failed",
		Message:     "all targets open",
		Model:       "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(router) error = %v", err)
	}
	if err := st.ResolveSystemEvent(routerEvent.ID); err != nil {
		t.Fatalf("ResolveSystemEvent() error = %v", err)
	}

	page, err := st.ListSystemEvents(SystemEventFilter{Status: SystemEventStatusUnread, Query: "trace-a", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListSystemEvents() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != parserEvent.ID {
		t.Fatalf("page = %+v", page)
	}
	summary, err := st.SystemEventSummary(time.Time{})
	if err != nil {
		t.Fatalf("SystemEventSummary() error = %v", err)
	}
	if summary.Total != 2 || summary.Unread != 1 || summary.Error != 1 || summary.Warning != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.BySource) != 2 {
		t.Fatalf("summary by source = %+v, want two sources", summary.BySource)
	}

	count, err := st.MarkAllSystemEventsRead(SystemEventFilter{Status: SystemEventStatusUnread})
	if err != nil {
		t.Fatalf("MarkAllSystemEventsRead() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("marked count = %d, want 1", count)
	}
	afterRead, err := st.GetSystemEvent(parserEvent.ID)
	if err != nil {
		t.Fatalf("GetSystemEvent(after read) error = %v", err)
	}
	if afterRead.Status != SystemEventStatusRead || afterRead.ReadAt.IsZero() {
		t.Fatalf("after read = %+v", afterRead)
	}

	if err := st.IgnoreSystemEvent(afterRead.ID); err != nil {
		t.Fatalf("IgnoreSystemEvent() error = %v", err)
	}
	ignored, err := st.UpsertSystemEvent(SystemEvent{
		Fingerprint: afterRead.Fingerprint,
		Source:      "parser",
		Category:    "parse_failure",
		Severity:    "critical",
		Title:       "Ignored parse failure repeated",
		Message:     "still ignored",
	})
	if err != nil {
		t.Fatalf("UpsertSystemEvent(ignored) error = %v", err)
	}
	if ignored.Status != SystemEventStatusIgnored || ignored.OccurrenceCount != 2 {
		t.Fatalf("ignored repeated event = %+v", ignored)
	}
}

func TestMarkParseJobFailedCreatesSystemEvent(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	if err := st.EnqueueParseJob("trace-parse-failed"); err != nil {
		t.Fatalf("EnqueueParseJob() error = %v", err)
	}
	jobs, err := st.ListParseJobs("queued", 10)
	if err != nil {
		t.Fatalf("ListParseJobs() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want one queued job", jobs)
	}

	const parseError = "constraint failed: UNIQUE constraint failed: semantic_nodes.trace_id, semantic_nodes.node_id (2067)"
	if err := st.MarkParseJobFailed(jobs[0].ID, parseError); err != nil {
		t.Fatalf("MarkParseJobFailed() error = %v", err)
	}

	page, err := st.ListSystemEvents(SystemEventFilter{
		Status:   SystemEventStatusUnread,
		Source:   "parser",
		Category: "parse_failure",
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListSystemEvents() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %+v, want one parser event", page)
	}
	event := page.Items[0]
	if event.TraceID != "trace-parse-failed" || event.JobID != "1" || event.Severity != "error" {
		t.Fatalf("event = %+v", event)
	}
	if !strings.Contains(event.Message, "semantic_nodes.trace_id") {
		t.Fatalf("event message = %q, want parse error", event.Message)
	}
}

func TestSaveAnalysisRunFailureCreatesSystemEvent(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	id, err := st.SaveAnalysisRun(AnalysisRunRecord{
		SessionID:       "sess-analysis-failed",
		TraceID:         "trace-analysis-failed",
		Kind:            "trace_audit",
		Analyzer:        "deterministic",
		AnalyzerVersion: "0.1.0",
		InputRef:        "trace:trace-analysis-failed",
		OutputJSON:      `{"error":"detector crashed"}`,
		Status:          "failed",
		Model:           "gpt-test",
	})
	if err != nil {
		t.Fatalf("SaveAnalysisRun() error = %v", err)
	}
	if id == 0 {
		t.Fatalf("analysis run id = 0")
	}

	page, err := st.ListSystemEvents(SystemEventFilter{
		Status:   SystemEventStatusUnread,
		Source:   "analyzer",
		Category: "analysis_failure",
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListSystemEvents() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %+v, want one analyzer event", page)
	}
	event := page.Items[0]
	if event.TraceID != "trace-analysis-failed" || event.SessionID != "sess-analysis-failed" || event.JobID != "1" {
		t.Fatalf("event = %+v", event)
	}
	if event.Model != "gpt-test" || event.Severity != "error" {
		t.Fatalf("event = %+v", event)
	}
}

func TestUpsertLogWithGroupingCreatesRoutingFailureSystemEvents(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeRoutingFailure := func(name string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:             name,
				Time:                  time.Date(2026, 5, 15, 2, 0, 0, 0, time.UTC),
				Model:                 "gpt-5",
				Provider:              "openai_compatible",
				Operation:             "responses",
				Endpoint:              "/v1/responses",
				URL:                   "/v1/responses",
				Method:                "POST",
				StatusCode:            503,
				Error:                 "no available channel target",
				RoutingPolicy:         "priority",
				RoutingCandidateCount: 3,
				RoutingFailureReason:  "all_targets_open",
			},
		}
		if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{SessionID: "sess-routing-failed"}); err != nil {
			t.Fatalf("UpsertLogWithGrouping(%q) error = %v", path, err)
		}
	}

	writeRoutingFailure("routing-a.http")
	writeRoutingFailure("routing-b.http")

	page, err := st.ListSystemEvents(SystemEventFilter{
		Status:   SystemEventStatusUnread,
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListSystemEvents() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Source != "router" {
		t.Fatalf("page = %+v, want one grouped router event and no duplicate transport event", page)
	}
	event := page.Items[0]
	if event.OccurrenceCount != 2 || event.Model != "gpt-5" || event.SessionID != "sess-routing-failed" {
		t.Fatalf("event = %+v", event)
	}
	if event.Fingerprint != "router:gpt_5:all_targets_open" {
		t.Fatalf("fingerprint = %q", event.Fingerprint)
	}
}

func TestUpsertLogWithGroupingCreatesTransportSystemEvent(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	path := filepath.Join(dir, "broken-pipe.http")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:          "broken-pipe",
			Time:               time.Date(2026, 5, 15, 2, 5, 0, 0, time.UTC),
			Model:              "gpt-5",
			Provider:           "openai_compatible",
			Operation:          "responses",
			Endpoint:           "/v1/responses",
			URL:                "/v1/responses",
			Method:             "POST",
			StatusCode:         200,
			Error:              "failed to copy response body: write tcp 127.0.0.1:8080->127.0.0.1:54211: write: broken pipe",
			SelectedUpstreamID: "openai-primary",
		},
		Layout: recordfile.LayoutInfo{IsStream: true},
	}
	if err := st.UpsertLogWithGrouping(path, header, GroupingInfo{SessionID: "sess-transport"}); err != nil {
		t.Fatalf("UpsertLogWithGrouping() error = %v", err)
	}

	page, err := st.ListSystemEvents(SystemEventFilter{
		Status:   SystemEventStatusUnread,
		Source:   "upstream",
		Category: "transport_error",
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListSystemEvents() error = %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %+v, want one transport event", page)
	}
	event := page.Items[0]
	if event.Severity != "warning" || event.UpstreamID != "openai-primary" || event.SessionID != "sess-transport" {
		t.Fatalf("event = %+v", event)
	}
	if event.Fingerprint != "upstream:openai_primary:v1_responses:client_disconnect" {
		t.Fatalf("fingerprint = %q", event.Fingerprint)
	}
}

func TestSaveFindingsRebuildsTraceFindings(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	longEvidence := strings.Repeat("x", 600)
	first := observe.Finding{
		ID:              "finding-1",
		TraceID:         "trace-findings",
		Category:        "credential_leak",
		Severity:        observe.SeverityHigh,
		Confidence:      0.95,
		Title:           "Credential exposure",
		EvidencePath:    "trace#trace-findings#node#node-secret",
		EvidenceExcerpt: longEvidence,
		NodeID:          "node-secret",
		Detector:        "credential",
		DetectorVersion: "0.1.0",
	}
	if err := st.SaveFindings("trace-findings", []observe.Finding{first}); err != nil {
		t.Fatalf("SaveFindings() error = %v", err)
	}
	findings, err := st.ListFindings("trace-findings", FindingFilter{Severity: string(observe.SeverityHigh)})
	if err != nil {
		t.Fatalf("ListFindings() error = %v", err)
	}
	if len(findings) != 1 || findings[0].ID != "finding-1" {
		t.Fatalf("findings = %+v", findings)
	}
	if len(findings[0].EvidenceExcerpt) != 500 {
		t.Fatalf("evidence excerpt length = %d, want 500", len(findings[0].EvidenceExcerpt))
	}

	second := first
	second.ID = "finding-2"
	second.Category = "tool_result_error"
	second.Severity = observe.SeverityMedium
	if err := st.SaveFindings("trace-findings", []observe.Finding{second}); err != nil {
		t.Fatalf("SaveFindings(rebuild) error = %v", err)
	}
	findings, err = st.ListFindings("trace-findings", FindingFilter{})
	if err != nil {
		t.Fatalf("ListFindings(rebuild) error = %v", err)
	}
	if len(findings) != 1 || findings[0].ID != "finding-2" {
		t.Fatalf("findings after rebuild = %+v", findings)
	}
	allFindings, err := st.ListAllFindings(FindingFilter{Category: "tool_result_error"}, 10)
	if err != nil {
		t.Fatalf("ListAllFindings() error = %v", err)
	}
	if len(allFindings) != 1 || allFindings[0].TraceID != "trace-findings" {
		t.Fatalf("all findings = %+v", allFindings)
	}
}

func TestSaveAndListAnalysisRuns(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	id, err := st.SaveAnalysisRun(AnalysisRunRecord{
		SessionID:       "sess-analysis",
		Kind:            "session_summary",
		Analyzer:        "session_summary",
		AnalyzerVersion: "0.1.0",
		InputRef:        "session:sess-analysis",
		OutputJSON:      `{"session_id":"sess-analysis"}`,
		Status:          "completed",
	})
	if err != nil {
		t.Fatalf("SaveAnalysisRun() error = %v", err)
	}
	if id == 0 {
		t.Fatalf("analysis run id = 0")
	}
	runs, err := st.ListAnalysisRuns("sess-analysis", "", "session_summary", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != id || runs[0].OutputJSON == "" {
		t.Fatalf("runs = %+v, want id %d", runs, id)
	}
	allRuns, err := st.ListAnalysisRuns("", "", "session_summary", 10)
	if err != nil {
		t.Fatalf("ListAnalysisRuns(all) error = %v", err)
	}
	if len(allRuns) != 1 || allRuns[0].ID != id {
		t.Fatalf("all runs = %+v, want id %d", allRuns, id)
	}
}

func TestAnalysisJobLifecycle(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	job, err := st.CreateAnalysisJob(AnalysisJobRecord{
		JobType:     "trace_reanalyze",
		TargetType:  "trace",
		TargetID:    "trace-job",
		StepsJSON:   `["reparse_observation","scan_findings"]`,
		RequestJSON: `{"mode":"async"}`,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisJob() error = %v", err)
	}
	if job.ID == 0 || job.Status != "queued" || job.ResultJSON != "{}" || job.RequestJSON != `{"mode":"async"}` {
		t.Fatalf("job = %+v, want queued with id and empty result", job)
	}

	if err := st.MarkAnalysisJobRunning(job.ID); err != nil {
		t.Fatalf("MarkAnalysisJobRunning() error = %v", err)
	}
	running, err := st.GetAnalysisJob(job.ID)
	if err != nil {
		t.Fatalf("GetAnalysisJob(running) error = %v", err)
	}
	if running.Status != "running" || running.Attempts != 1 || running.StartedAt.IsZero() {
		t.Fatalf("running job = %+v", running)
	}

	if err := st.MarkAnalysisJobCompleted(job.ID, `{"findings":1}`); err != nil {
		t.Fatalf("MarkAnalysisJobCompleted() error = %v", err)
	}
	completed, err := st.GetAnalysisJob(job.ID)
	if err != nil {
		t.Fatalf("GetAnalysisJob(completed) error = %v", err)
	}
	if completed.Status != "completed" || completed.ResultJSON != `{"findings":1}` || completed.FinishedAt.IsZero() {
		t.Fatalf("completed job = %+v", completed)
	}

	jobs, err := st.ListAnalysisJobs("completed", "trace", "trace-job", 10)
	if err != nil {
		t.Fatalf("ListAnalysisJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("jobs = %+v, want completed job %d", jobs, job.ID)
	}
}

func TestListTraceIDsSupportsBatchFilters(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:  "req-batch-missing",
			Time:       time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
			Model:      "gpt-5.1",
			Provider:   "openai_compatible",
			Operation:  "responses",
			Endpoint:   "/v1/responses",
			URL:        "/v1/responses",
			Method:     "POST",
			StatusCode: 200,
		},
	}
	path := filepath.Join(t.TempDir(), "batch-missing.http")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := st.UpsertLog(path, header); err != nil {
		t.Fatalf("UpsertLog(missing) error = %v", err)
	}
	header.Meta.RequestID = "req-batch-usage"
	header.Usage = recordfile.UsageInfo{TotalTokens: 3}
	path = filepath.Join(t.TempDir(), "batch-usage.http")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := st.UpsertLog(path, header); err != nil {
		t.Fatalf("UpsertLog(usage) error = %v", err)
	}

	ids, err := st.ListTraceIDs(ListFilter{Endpoint: "responses", MissingUsage: true}, 10)
	if err != nil {
		t.Fatalf("ListTraceIDs() error = %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("ids = %+v, want one missing-usage trace", ids)
	}
}

func TestListTraceIDsAppliesObservationStatusFilter(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:  "req-batch-unparsed",
			Time:       time.Date(2026, 5, 19, 9, 10, 0, 0, time.UTC),
			Model:      "gpt-5",
			Provider:   "openai_compatible",
			Operation:  "responses",
			Endpoint:   "/v1/responses",
			URL:        "/v1/responses",
			Method:     "POST",
			StatusCode: 200,
		},
	}
	unparsedPath := filepath.Join(t.TempDir(), "batch-unparsed.http")
	if err := os.WriteFile(unparsedPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := st.UpsertLog(unparsedPath, header); err != nil {
		t.Fatalf("UpsertLog(unparsed) error = %v", err)
	}
	unparsedEntry, err := st.GetByRequestID("req-batch-unparsed")
	if err != nil {
		t.Fatalf("GetByRequestID(unparsed) error = %v", err)
	}

	header.Meta.RequestID = "req-batch-parsed"
	parsedPath := filepath.Join(t.TempDir(), "batch-parsed.http")
	if err := os.WriteFile(parsedPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := st.UpsertLog(parsedPath, header); err != nil {
		t.Fatalf("UpsertLog(parsed) error = %v", err)
	}
	parsedEntry, err := st.GetByRequestID("req-batch-parsed")
	if err != nil {
		t.Fatalf("GetByRequestID(parsed) error = %v", err)
	}
	if err := st.SaveObservation(observe.TraceObservation{
		TraceID:       parsedEntry.ID,
		Parser:        "openai",
		ParserVersion: "0.1.0",
		Status:        observe.ParseStatusParsed,
	}); err != nil {
		t.Fatalf("SaveObservation() error = %v", err)
	}

	ids, err := st.ListTraceIDs(ListFilter{ObservationStatus: "unparsed"}, 10)
	if err != nil {
		t.Fatalf("ListTraceIDs() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != unparsedEntry.ID {
		t.Fatalf("ids = %+v, want unparsed trace %s", ids, unparsedEntry.ID)
	}
}

func TestMarkAnalysisJobFailedCreatesSystemEvent(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	job, err := st.CreateAnalysisJob(AnalysisJobRecord{
		JobType:    "trace_reanalyze",
		TargetType: "trace",
		TargetID:   "trace-failed-job",
		StepsJSON:  `["reparse_observation"]`,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisJob() error = %v", err)
	}
	if err := st.MarkAnalysisJobFailed(job.ID, "parser failed"); err != nil {
		t.Fatalf("MarkAnalysisJobFailed() error = %v", err)
	}

	events, err := st.ListSystemEvents(SystemEventFilter{Source: "analyzer", Status: "all", PageSize: 10})
	if err != nil {
		t.Fatalf("ListSystemEvents() error = %v", err)
	}
	if len(events.Items) != 1 || events.Items[0].Category != "analysis_job_failure" || events.Items[0].TraceID != "trace-failed-job" {
		t.Fatalf("events = %+v", events)
	}
}

func TestClassifyUpstreamFailureSeparatesRetryQueueSaturation(t *testing.T) {
	got := classifyUpstreamFailure(http.StatusServiceUnavailable, "Proxy overloaded: upstream retry wait queue saturated")
	if got != "retry_queue_saturated" {
		t.Fatalf("classifyUpstreamFailure() = %q, want retry_queue_saturated", got)
	}
}

func mustTraceID(t *testing.T, st *Store, path string) string {
	t.Helper()
	var traceID string
	if err := st.db.QueryRow(`SELECT trace_id FROM logs WHERE path = ?`, path).Scan(&traceID); err != nil {
		t.Fatalf("trace id query error = %v", err)
	}
	return traceID
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
