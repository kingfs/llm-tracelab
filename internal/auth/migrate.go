package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	entmigrations "github.com/kingfs/llm-tracelab/ent"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

func MigrateUp(dbPath string, steps int) error {
	return MigrateDatabaseUp("sqlite", dbPath, steps)
}

func MigrateDatabaseUp(driver string, dsn string, steps int) error {
	if steps == 0 {
		adopted, err := adoptLegacyTraceDatabase(driver, dsn)
		if err != nil {
			return err
		}
		if adopted {
			return nil
		}
	}
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

func adoptLegacyTraceDatabase(driverName string, dsn string) (bool, error) {
	driverName = normalizeDriver(driverName)
	if driverName != "sqlite" {
		return false, nil
	}
	path := config.SQLitePathFromDSN(dsn)
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return false, err
	}
	hasLogs, err := tableExists(db, "logs")
	if err != nil {
		_ = db.Close()
		return false, err
	}
	if !hasLogs {
		_ = db.Close()
		return false, nil
	}
	hasVersion, err := migrationVersionExists(db)
	if err != nil {
		_ = db.Close()
		return false, err
	}
	_ = db.Close()
	if hasVersion {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	traceStore, err := store.NewWithDatabase(filepath.Dir(path), "sqlite", path, 4, 4)
	if err != nil {
		return false, fmt.Errorf("upgrade legacy trace schema: %w", err)
	}
	_ = traceStore.Close()

	authStore, err := OpenDatabase("sqlite", path, 4, 4)
	if err != nil {
		return false, err
	}
	if err := authStore.EnsureSchema(context.Background()); err != nil {
		_ = authStore.Close()
		return false, fmt.Errorf("ensure unified schema: %w", err)
	}
	_ = authStore.Close()

	latest, err := latestEmbeddedMigrationVersion()
	if err != nil {
		return false, err
	}
	db, err = sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return false, err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool)`); err != nil {
		return false, err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON schema_migrations (version)`); err != nil {
		return false, err
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		return false, err
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, dirty) VALUES (?, ?)`, latest, false); err != nil {
		return false, err
	}
	return true, nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func migrationVersionExists(db *sql.DB) (bool, error) {
	exists, err := tableExists(db, "schema_migrations")
	if err != nil || !exists {
		return false, err
	}
	var version int
	var dirty bool
	err = db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func latestEmbeddedMigrationVersion() (int, error) {
	matches, err := fs.Glob(entmigrations.Migrations, "migrations/*.up.sql")
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, match := range matches {
		name := filepath.Base(match)
		versionText, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			continue
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no embedded up migrations found")
	}
	return latest, nil
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
