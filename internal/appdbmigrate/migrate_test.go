package appdbmigrate

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

func TestMigrateUpSQLiteUsesStoreInit(t *testing.T) {
	err := MigrateUp("sqlite", "ignored.sqlite3", 0)
	if !errors.Is(err, ErrSQLiteUsesStoreInit) {
		t.Fatalf("MigrateUp(sqlite) error = %v, want ErrSQLiteUsesStoreInit", err)
	}
}

func TestMigrateDownSQLiteUsesStoreInit(t *testing.T) {
	err := MigrateDown("sqlite", "ignored.sqlite3", 1, false)
	if !errors.Is(err, ErrSQLiteUsesStoreInit) {
		t.Fatalf("MigrateDown(sqlite) error = %v, want ErrSQLiteUsesStoreInit", err)
	}
}

func TestCheckStatusSQLiteReportsSchemaInitFallback(t *testing.T) {
	status, err := CheckStatus("sqlite", "ignored.sqlite3")
	if err != nil {
		t.Fatalf("CheckStatus(sqlite) error = %v", err)
	}
	if status.Driver != "sqlite" || status.Versioned || status.Available {
		t.Fatalf("CheckStatus(sqlite) = %+v, want non-versioned unavailable fallback", status)
	}
	if !strings.Contains(status.Message, "database file does not exist") || !strings.Contains(status.Message, ErrSQLiteUsesStoreInit.Error()) {
		t.Fatalf("CheckStatus(sqlite) message = %q, want fallback explanation", status.Message)
	}
}

func TestCheckStatusSQLiteReportsApplicationSchemaMarker(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(sqlite) error = %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE logs (path TEXT PRIMARY KEY)`,
		`CREATE TABLE responses (id TEXT PRIMARY KEY)`,
		`CREATE TABLE response_items (id TEXT PRIMARY KEY)`,
		`CREATE TABLE request_audits (id TEXT PRIMARY KEY)`,
		`CREATE TABLE execution_events (id TEXT PRIMARY KEY)`,
		`CREATE TABLE upstream_exchanges (id TEXT PRIMARY KEY)`,
		`CREATE TABLE tool_call_audits (id TEXT PRIMARY KEY)`,
		`CREATE TABLE app_schema_status (
			namespace TEXT PRIMARY KEY,
			version INTEGER NOT NULL,
			mode TEXT NOT NULL,
			source TEXT NOT NULL,
			updated_at datetime NOT NULL
		)`,
		`INSERT INTO app_schema_status (namespace, version, mode, source, updated_at)
		 VALUES ('application', 1, 'schema-init', 'internal/store raw DDL startup initialization', CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("db.Exec(%q) error = %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	status, err := CheckStatus("sqlite", dbPath)
	if err != nil {
		t.Fatalf("CheckStatus(sqlite) error = %v", err)
	}
	if status.Driver != "sqlite" || status.Versioned || !status.Available || !status.RequiredTablesPresent {
		t.Fatalf("CheckStatus(sqlite) = %+v, want readable non-versioned application schema", status)
	}
	if status.SchemaMarker != "app_schema_status" || status.SchemaMarkerVersion != 1 || len(status.MissingTables) != 0 {
		t.Fatalf("sqlite marker status = %+v", status)
	}
	if !strings.Contains(status.Message, "marker version 1") || !strings.Contains(status.Message, ErrSQLiteUsesStoreInit.Error()) {
		t.Fatalf("CheckStatus(sqlite) message = %q, want marker and fallback explanation", status.Message)
	}
}

func TestCheckStatusSQLiteReportsLegacySchemaWithoutMarker(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(sqlite) error = %v", err)
	}
	for _, table := range sqliteApplicationRequiredTables {
		if _, err := db.Exec(`CREATE TABLE ` + table + ` (id TEXT PRIMARY KEY)`); err != nil {
			t.Fatalf("create table %q error = %v", table, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	status, err := CheckStatus("sqlite", dbPath)
	if err != nil {
		t.Fatalf("CheckStatus(sqlite) error = %v", err)
	}
	if !status.Available || !status.RequiredTablesPresent || status.SchemaMarker != "" || status.SchemaMarkerVersion != 0 {
		t.Fatalf("legacy sqlite status = %+v, want available schema without marker", status)
	}
	if !strings.Contains(status.Message, "marker missing for legacy database") {
		t.Fatalf("legacy sqlite message = %q", status.Message)
	}
}

func TestCheckStatusPostgresRequiresDSN(t *testing.T) {
	_, err := CheckStatus("postgres", "")
	if err == nil {
		t.Fatalf("CheckStatus(postgres empty dsn) error = nil")
	}
	if !strings.Contains(err.Error(), "postgres application database dsn is required") {
		t.Fatalf("CheckStatus(postgres empty dsn) error = %q", err.Error())
	}
}

func TestMigrateUpPostgresRequiresDSN(t *testing.T) {
	err := MigrateUp("postgresql", "", 0)
	if err == nil {
		t.Fatalf("MigrateUp(postgresql empty dsn) error = nil")
	}
	if !strings.Contains(err.Error(), "postgres application database dsn is required") {
		t.Fatalf("MigrateUp(postgresql empty dsn) error = %q", err.Error())
	}
}

func TestMigrateDownPostgresRequiresDSN(t *testing.T) {
	err := MigrateDown("postgresql", "", 1, false)
	if err == nil {
		t.Fatalf("MigrateDown(postgresql empty dsn) error = nil")
	}
	if !strings.Contains(err.Error(), "postgres application database dsn is required") {
		t.Fatalf("MigrateDown(postgresql empty dsn) error = %q", err.Error())
	}
}

func TestMigrateUpRejectsUnsupportedDriver(t *testing.T) {
	err := MigrateUp("mysql", "mysql://example", 0)
	if err == nil {
		t.Fatalf("MigrateUp(mysql) error = nil")
	}
	if !strings.Contains(err.Error(), `application database driver "mysql" is not supported`) {
		t.Fatalf("MigrateUp(mysql) error = %q", err.Error())
	}
}

func TestMigrateDownRejectsUnsupportedDriver(t *testing.T) {
	err := MigrateDown("mysql", "mysql://example", 1, false)
	if err == nil {
		t.Fatalf("MigrateDown(mysql) error = nil")
	}
	if !strings.Contains(err.Error(), `application database driver "mysql" is not supported`) {
		t.Fatalf("MigrateDown(mysql) error = %q", err.Error())
	}
}

func TestMigrateUpPostgresIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}

	if err := MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres) error = %v", err)
	}
	if err := MigrateUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateUp(postgres idempotent) error = %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open(postgres) error = %v", err)
	}
	defer db.Close()

	for _, table := range []string{
		"schema_migrations",
		"analysis_jobs",
		"analysis_runs",
		"channel_configs",
		"channel_models",
		"channel_probe_runs",
		"dataset_examples",
		"datasets",
		"eval_runs",
		"execution_events",
		"experiment_runs",
		"logs",
		"model_catalog",
		"parse_jobs",
		"parser_versions",
		"request_audits",
		"response_items",
		"responses",
		"scores",
		"semantic_nodes",
		"system_events",
		"tool_call_audits",
		"trace_findings",
		"trace_observations",
		"upstream_exchanges",
		"upstream_models",
		"upstream_targets",
	} {
		t.Run(table, func(t *testing.T) {
			var exists bool
			if err := db.QueryRow(`SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)`, table).Scan(&exists); err != nil {
				t.Fatalf("query table %q error = %v", table, err)
			}
			if !exists {
				t.Fatalf("table %q does not exist", table)
			}
		})
	}

	var version uint
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty); err != nil {
		t.Fatalf("query schema_migrations error = %v", err)
	}
	if version == 0 {
		t.Fatalf("schema_migrations version = 0, want applied version")
	}
	if dirty {
		t.Fatalf("schema_migrations dirty = true")
	}
}
