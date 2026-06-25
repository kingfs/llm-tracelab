package entmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresLogExchangeFieldsMigrationContainsColumnsAndIndexes(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "postgres-migrations", "*_add_log_exchange_fields.up.sql"))
	if err != nil {
		t.Fatalf("glob migration: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("log exchange field migrations = %v, want exactly one", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)
	for _, want := range []string{
		`ALTER TABLE "logs"`,
		`ADD COLUMN "exchange_id" character varying NOT NULL DEFAULT ''`,
		`ADD COLUMN "exchange_kind" character varying NOT NULL DEFAULT ''`,
		`ADD COLUMN "exchange_role" character varying NOT NULL DEFAULT ''`,
		`ADD COLUMN "parent_exchange_id" character varying NOT NULL DEFAULT ''`,
		`ADD COLUMN "sequence_index" bigint NOT NULL DEFAULT 0`,
		`CREATE INDEX "tracelog_exchange_kind_recorded_at"`,
		`CREATE INDEX "tracelog_parent_exchange_id"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration %s missing %q", matches[0], want)
		}
	}
}
