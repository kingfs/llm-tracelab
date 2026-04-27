package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	entmigrations "github.com/kingfs/llm-tracelab/ent"
	"github.com/kingfs/llm-tracelab/internal/config"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

func MigrateUp(dbPath string, steps int) error {
	return MigrateDatabaseUp("sqlite", dbPath, steps)
}

func MigrateDatabaseUp(driver string, dsn string, steps int) error {
	m, err := newMigrator(driver, dsn)
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

func MigrateDown(dbPath string, steps int, all bool) error {
	return MigrateDatabaseDown("sqlite", dbPath, steps, all)
}

func MigrateDatabaseDown(driver string, dsn string, steps int, all bool) error {
	m, err := newMigrator(driver, dsn)
	if err != nil {
		return err
	}
	defer closeMigrator(m)
	switch {
	case all:
		err = m.Down()
	case steps > 0:
		err = m.Steps(-steps)
	default:
		err = m.Steps(-1)
	}
	if errors.Is(err, gomigrate.ErrNoChange) {
		return nil
	}
	return err
}

func newMigrator(driverName string, dsn string) (*gomigrate.Migrate, error) {
	driverName = normalizeDriver(driverName)
	if driverName != "sqlite" {
		return nil, fmt.Errorf("database driver %q is not supported by embedded migrations yet", driverName)
	}
	path := config.SQLitePathFromDSN(dsn)
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	source, err := iofs.New(entmigrations.Migrations, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	m, err := gomigrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return m, nil
}

func closeMigrator(m *gomigrate.Migrate) {
	if m == nil {
		return
	}
	sourceErr, databaseErr := m.Close()
	_ = sourceErr
	_ = databaseErr
}
