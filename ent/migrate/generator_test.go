package entmigrate

import (
	"strings"
	"testing"

	"entgo.io/ent/dialect"
)

func TestParseConfigDefaultsSQLite(t *testing.T) {
	t.Setenv(DevURLEnv, "")

	cfg, err := ParseConfig([]string{"add_responses_tables"})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if cfg.Dialect != dialect.SQLite {
		t.Fatalf("Dialect = %q, want sqlite", cfg.Dialect)
	}
	if cfg.Dir != "ent/migrations" {
		t.Fatalf("Dir = %q, want ent/migrations", cfg.Dir)
	}
	if cfg.Name != "add_responses_tables" {
		t.Fatalf("Name = %q, want add_responses_tables", cfg.Name)
	}
	if !strings.HasPrefix(cfg.DevURL, "sqlite://") || !strings.Contains(cfg.DevURL, "llm-tracelab-ent-migrate-dev.sqlite3") {
		t.Fatalf("DevURL = %q, want temp sqlite dev url", cfg.DevURL)
	}
}

func TestParseConfigPostgresUsesExplicitDevURL(t *testing.T) {
	t.Setenv(DevURLEnv, "")

	cfg, err := ParseConfig([]string{
		"--dialect", "postgresql",
		"--dir", "ent/postgres-migrations",
		"--dev-url", "postgres://user:pass@localhost:5432/dev?sslmode=disable",
		"init_postgres",
	})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if cfg.Dialect != dialect.Postgres {
		t.Fatalf("Dialect = %q, want postgres", cfg.Dialect)
	}
	if cfg.Dir != "ent/postgres-migrations" {
		t.Fatalf("Dir = %q, want ent/postgres-migrations", cfg.Dir)
	}
	if cfg.DevURL != "postgres://user:pass@localhost:5432/dev?sslmode=disable" {
		t.Fatalf("DevURL = %q", cfg.DevURL)
	}
}

func TestParseConfigPostgresUsesEnvDevURL(t *testing.T) {
	t.Setenv(DevURLEnv, "postgres://env-user:pass@localhost:5432/dev?sslmode=disable")

	cfg, err := ParseConfig([]string{"--dialect", "postgres", "init_postgres"})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	if cfg.DevURL != "postgres://env-user:pass@localhost:5432/dev?sslmode=disable" {
		t.Fatalf("DevURL = %q, want env dev url", cfg.DevURL)
	}
}

func TestParseConfigPostgresRequiresDevURL(t *testing.T) {
	t.Setenv(DevURLEnv, "")

	_, err := ParseConfig([]string{"--dialect", "postgres", "init_postgres"})
	if err == nil {
		t.Fatalf("ParseConfig() error = nil, want dev-url error")
	}
	if !strings.Contains(err.Error(), "postgres migration generation requires --dev-url") {
		t.Fatalf("ParseConfig() error = %q", err.Error())
	}
}

func TestNormalizeDialectRejectsUnknown(t *testing.T) {
	_, err := NormalizeDialect("mysql")
	if err == nil {
		t.Fatalf("NormalizeDialect() error = nil, want unsupported dialect")
	}
	if !strings.Contains(err.Error(), `unsupported migration dialect "mysql"`) {
		t.Fatalf("NormalizeDialect() error = %q", err.Error())
	}
}
