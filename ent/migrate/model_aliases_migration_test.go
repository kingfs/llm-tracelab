package entmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresModelAliasesMigrationContainsTableAndIndexes(t *testing.T) {
	upMatches, err := filepath.Glob(filepath.Join("..", "postgres-migrations", "*_add_model_aliases.up.sql"))
	if err != nil {
		t.Fatalf("glob up migration: %v", err)
	}
	if len(upMatches) != 1 {
		t.Fatalf("model_aliases up migrations = %v, want exactly one", upMatches)
	}
	downMatches, err := filepath.Glob(filepath.Join("..", "postgres-migrations", "*_add_model_aliases.down.sql"))
	if err != nil {
		t.Fatalf("glob down migration: %v", err)
	}
	if len(downMatches) != 1 {
		t.Fatalf("model_aliases down migrations = %v, want exactly one", downMatches)
	}

	data, err := os.ReadFile(upMatches[0])
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	sql := string(data)
	for _, want := range []string{
		`CREATE TABLE IF NOT EXISTS "model_aliases"`,
		`"alias" character varying NOT NULL`,
		`"target_model" character varying NOT NULL`,
		`"channel_id" character varying NOT NULL DEFAULT ''`,
		`"enabled" boolean NOT NULL DEFAULT true`,
		`CREATE INDEX IF NOT EXISTS "idx_model_aliases_alias"`,
		`CREATE INDEX IF NOT EXISTS "idx_model_aliases_target"`,
		`CREATE INDEX IF NOT EXISTS "idx_model_aliases_channel"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration %s missing %q", upMatches[0], want)
		}
	}
}
