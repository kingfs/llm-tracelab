package entmigrate

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kingfs/llm-tracelab/ent/dao/migrate"

	_ "ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

const DevURLEnv = "LLM_TRACELAB_ENT_MIGRATE_DEV_URL"

type Config struct {
	Dialect string
	Dir     string
	DevURL  string
	Name    string
}

func Run(ctx context.Context, args []string) error {
	cfg, err := ParseConfig(args)
	if err != nil {
		return err
	}
	return Generate(ctx, cfg)
}

func Generate(ctx context.Context, cfg Config) error {
	dir, err := sqltool.NewGolangMigrateDir(cfg.Dir)
	if err != nil {
		return fmt.Errorf("create migrations dir: %w", err)
	}

	opts := []schema.MigrateOption{
		schema.WithDir(dir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(cfg.Dialect),
	}
	if err := migrate.NamedDiff(ctx, cfg.DevURL, cfg.Name, opts...); err != nil {
		return fmt.Errorf("generate migration diff: %w", err)
	}
	return nil
}

func ParseConfig(args []string) (Config, error) {
	cfg := Config{
		Dialect: dialect.SQLite,
		Dir:     "ent/migrations",
	}

	fs := flag.NewFlagSet("ent-migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Dialect, "dialect", cfg.Dialect, "migration dialect: sqlite or postgres")
	fs.StringVar(&cfg.Dir, "dir", cfg.Dir, "golang-migrate output directory")
	fs.StringVar(&cfg.DevURL, "dev-url", "", "Atlas dev database URL")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("%s\n%w", Usage(), err)
	}
	if fs.NArg() != 1 {
		return Config{}, fmt.Errorf("%s", Usage())
	}
	cfg.Name = fs.Arg(0)

	normalized, err := NormalizeDialect(cfg.Dialect)
	if err != nil {
		return Config{}, err
	}
	cfg.Dialect = normalized

	switch cfg.Dialect {
	case dialect.SQLite:
		if cfg.DevURL == "" {
			devPath := filepath.Join(os.TempDir(), "llm-tracelab-ent-migrate-dev.sqlite3")
			_ = os.Remove(devPath)
			cfg.DevURL = "sqlite://" + devPath + "?_fk=1"
		}
	case dialect.Postgres:
		if cfg.DevURL == "" {
			cfg.DevURL = os.Getenv(DevURLEnv)
		}
		if cfg.DevURL == "" {
			return Config{}, fmt.Errorf("postgres migration generation requires --dev-url or %s", DevURLEnv)
		}
	}
	return cfg, nil
}

func NormalizeDialect(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sqlite", dialect.SQLite:
		return dialect.SQLite, nil
	case dialect.Postgres, "postgresql":
		return dialect.Postgres, nil
	default:
		return "", fmt.Errorf("unsupported migration dialect %q; use sqlite or postgres", value)
	}
}

func Usage() string {
	return "usage: go run -mod=mod ent/migrate/main.go [--dialect sqlite|postgres] [--dir ent/migrations] [--dev-url atlas_dev_url] <migration_name>"
}
