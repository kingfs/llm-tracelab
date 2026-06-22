package appdbmigrate

import (
	"database/sql"
	"errors"
	"os"
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

func TestMigrateUpPostgresRequiresDSN(t *testing.T) {
	err := MigrateUp("postgresql", "", 0)
	if err == nil {
		t.Fatalf("MigrateUp(postgresql empty dsn) error = nil")
	}
	if !strings.Contains(err.Error(), "postgres application database dsn is required") {
		t.Fatalf("MigrateUp(postgresql empty dsn) error = %q", err.Error())
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
