package appdbmigrate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	entmigrations "github.com/kingfs/llm-tracelab/ent"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
)

const postgresMigrationRoot = "postgres-migrations"

func MigrateUp(driver string, dsn string, steps int) error {
	driver = normalizeDriver(driver)
	switch driver {
	case "postgres":
		return migratePostgresUp(dsn, steps)
	case "sqlite":
		return ErrSQLiteUsesStoreInit
	default:
		return fmt.Errorf("application database driver %q is not supported by versioned migrations yet", driver)
	}
}

var ErrSQLiteUsesStoreInit = errors.New("sqlite application migration still uses store schema initialization")

func migratePostgresUp(dsn string, steps int) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("postgres application database dsn is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return err
	}
	source, err := iofs.New(entmigrations.PostgresMigrations, postgresMigrationRoot)
	if err != nil {
		return fmt.Errorf("open embedded postgres migrations: %w", err)
	}
	m, err := gomigrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return err
	}
	defer closeMigrator(m)
	if steps > 0 {
		err = m.Steps(steps)
	} else {
		err = m.Up()
	}
	if errors.Is(err, gomigrate.ErrNoChange) {
		return nil
	}
	return err
}

func normalizeDriver(driver string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	switch driver {
	case "":
		return "sqlite"
	case "postgresql":
		return "postgres"
	default:
		return driver
	}
}

func closeMigrator(m *gomigrate.Migrate) {
	if m == nil {
		return
	}
	sourceErr, databaseErr := m.Close()
	_ = sourceErr
	_ = databaseErr
}
