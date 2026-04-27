//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kingfs/llm-tracelab/ent/dao/migrate"

	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Printf("migration generation failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run -mod=mod ent/migrate/main.go <migration_name>")
	}
	migrationName := os.Args[1]
	devPath := filepath.Join(os.TempDir(), "llm-tracelab-ent-migrate-dev.sqlite3")
	_ = os.Remove(devPath)

	dir, err := sqltool.NewGolangMigrateDir("ent/migrations")
	if err != nil {
		return fmt.Errorf("create migrations dir: %w", err)
	}

	opts := []schema.MigrateOption{
		schema.WithDir(dir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(dialect.SQLite),
	}
	if err := migrate.NamedDiff(ctx, "sqlite://"+devPath+"?_fk=1", migrationName, opts...); err != nil {
		return fmt.Errorf("generate migration diff: %w", err)
	}
	return nil
}
