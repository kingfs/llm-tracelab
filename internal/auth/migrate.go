package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	entmigrations "github.com/kingfs/llm-tracelab/ent"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

func MigrateUp(dbPath string, steps int) error {
	m, err := newMigrator(dbPath)
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
	m, err := newMigrator(dbPath)
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

func newMigrator(dbPath string) (*gomigrate.Migrate, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
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
