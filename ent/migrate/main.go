//go:build ignore

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kingfs/llm-tracelab/ent/dao/migrate"

	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/mattn/go-sqlite3"
)

const devURLEnv = "LLM_TRACELAB_ENT_MIGRATE_DEV_URL"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Printf("migration generation failed: %v", err)
		os.Exit(1)
	}
}

type config struct {
	dialect string
	dir     string
	devURL  string
	name    string
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}

	if cfg.dialect == dialect.Postgres {
		return fmt.Errorf("postgres migration generation is intentionally disabled in this stage; dev-url is configured, but no SQL was generated")
	}

	dir, err := sqltool.NewGolangMigrateDir(cfg.dir)
	if err != nil {
		return fmt.Errorf("create migrations dir: %w", err)
	}

	opts := []schema.MigrateOption{
		schema.WithDir(dir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(cfg.dialect),
	}
	if err := migrate.NamedDiff(ctx, cfg.devURL, cfg.name, opts...); err != nil {
		return fmt.Errorf("generate migration diff: %w", err)
	}
	return nil
}

func parseConfig(args []string) (config, error) {
	cfg := config{
		dialect: dialect.SQLite,
		dir:     "ent/migrations",
	}

	fs := flag.NewFlagSet("ent-migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.dialect, "dialect", cfg.dialect, "migration dialect: sqlite or postgres")
	fs.StringVar(&cfg.dir, "dir", cfg.dir, "golang-migrate output directory")
	fs.StringVar(&cfg.devURL, "dev-url", "", "Atlas dev database URL")
	if err := fs.Parse(args); err != nil {
		return config{}, fmt.Errorf("%s\n%w", usage(), err)
	}
	if fs.NArg() != 1 {
		return config{}, fmt.Errorf("%s", usage())
	}
	cfg.name = fs.Arg(0)

	normalized, err := normalizeDialect(cfg.dialect)
	if err != nil {
		return config{}, err
	}
	cfg.dialect = normalized

	switch cfg.dialect {
	case dialect.SQLite:
		if cfg.devURL == "" {
			devPath := filepath.Join(os.TempDir(), "llm-tracelab-ent-migrate-dev.sqlite3")
			_ = os.Remove(devPath)
			cfg.devURL = "sqlite://" + devPath + "?_fk=1"
		}
	case dialect.Postgres:
		if cfg.devURL == "" {
			cfg.devURL = os.Getenv(devURLEnv)
		}
		if cfg.devURL == "" {
			return config{}, fmt.Errorf("postgres migration generation requires --dev-url or %s", devURLEnv)
		}
	}
	return cfg, nil
}

func normalizeDialect(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case dialect.SQLite:
		return dialect.SQLite, nil
	case dialect.Postgres, "postgresql":
		return dialect.Postgres, nil
	default:
		return "", fmt.Errorf("unsupported migration dialect %q; use sqlite or postgres", value)
	}
}

func usage() string {
	return "usage: go run -mod=mod ent/migrate/main.go [--dialect sqlite|postgres] [--dir ent/migrations] [--dev-url atlas_dev_url] <migration_name>"
}
