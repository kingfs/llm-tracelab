package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	entmigrations "github.com/kingfs/llm-tracelab/ent"
	"github.com/kingfs/llm-tracelab/internal/appdbmigrate"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

var authRequiredTables = []string{
	"users",
	"api_tokens",
}

var ErrPostgresAuthRollbackUnsupported = errors.New("postgres auth migrate down is unsupported because auth tables are owned by the application schema_migrations namespace; restore from backup or use a reviewed application migration plan")

func RequiredTables() []string {
	return cloneStrings(authRequiredTables)
}

func MigrateUp(dbPath string, steps int) error {
	return MigrateDatabaseUp("sqlite", dbPath, steps)
}

func MigrateDatabaseUp(driver string, dsn string, steps int) error {
	if normalizeDriver(driver) == "postgres" {
		return appdbmigrate.MigrateUp(driver, dsn, steps)
	}
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
	if normalizeDriver(driver) == "postgres" {
		return ErrPostgresAuthRollbackUnsupported
	}
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

type MigrationStatus struct {
	Driver                     string
	Versioned                  bool
	ProductionReady            bool
	Available                  bool
	Version                    uint
	Dirty                      bool
	DatabasePath               string
	EffectiveDatabaseNamespace string
	SchemaAuthority            string
	StorageContract            string
	StorageRole                string
	NamespaceMode              string
	RollbackSupported          bool
	RollbackScope              string
	OperatorAdvice             string
	SharedApplicationNamespace bool
	RequiredTables             []string
	TablesChecked              []string
	RequiredTablesPresent      bool
	MissingTables              []string
	Message                    string
}

func CheckStatus(driver string, dsn string) (MigrationStatus, error) {
	driver = normalizeDriver(driver)
	status := MigrationStatus{
		Driver:            driver,
		Versioned:         true,
		RollbackScope:     "auth_database_migration_set",
		RollbackSupported: true,
		RequiredTables:    cloneStrings(authRequiredTables),
		TablesChecked:     []string{},
		MissingTables:     []string{},
	}
	switch driver {
	case "postgres":
		appStatus, err := appdbmigrate.CheckStatus(driver, dsn)
		if err != nil {
			return status, err
		}
		status.Available = appStatus.Available
		status.Version = appStatus.Version
		status.Dirty = appStatus.Dirty
		status.ProductionReady = true
		status.EffectiveDatabaseNamespace = "application"
		status.SchemaAuthority = "application_postgres_migration_set"
		status.StorageContract = "postgres_application_schema_owns_auth_tables"
		status.StorageRole = "production"
		status.NamespaceMode = "shared_application_schema_migrations"
		status.RollbackSupported = false
		status.RollbackScope = "unsupported_from_auth_cli_shared_application_migration_set"
		status.OperatorAdvice = "run db migrate up as the canonical Postgres schema setup; use auth migrate status --check-db only to verify users/api_tokens and shared schema_migrations state"
		status.SharedApplicationNamespace = true
		status.Message = appStatus.Message
		if status.Message == "" {
			status.Message = "postgres auth tables are owned by the application schema_migrations namespace"
		}
		if err := checkPostgresAuthTables(dsn, &status); err != nil {
			return status, err
		}
		return status, nil
	case "sqlite":
		return checkSQLiteMigrationStatus(dsn, status)
	default:
		return status, fmt.Errorf("auth database driver %q is not supported by migrations yet", driver)
	}
}

func checkSQLiteMigrationStatus(dsn string, status MigrationStatus) (MigrationStatus, error) {
	dbPath := config.SQLitePathFromDSN(dsn)
	status.DatabasePath = dbPath
	status.EffectiveDatabaseNamespace = "auth"
	status.SchemaAuthority = "sqlite_auth_embedded_migrations"
	status.StorageContract = "sqlite_auth_migrations_for_legacy_dev_test"
	status.StorageRole = appdbmigrate.SQLiteStorageRole
	status.NamespaceMode = "independent_sqlite_auth_schema_migrations"
	status.OperatorAdvice = "SQLite auth migrations remain available for legacy/dev/test compatibility; use Postgres for production storage"
	if strings.TrimSpace(dbPath) == "" {
		status.Message = "sqlite auth database path is empty"
		return status, nil
	}
	if dbPath == ":memory:" {
		status.Message = "sqlite in-memory auth database status is not inspectable"
		return status, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.Message = "sqlite auth database file does not exist"
			return status, nil
		}
		return status, err
	}

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(dbPath))
	if err != nil {
		return status, err
	}
	defer db.Close()
	if err := checkSQLiteAuthTables(db, &status); err != nil {
		return status, err
	}
	exists, err := tableExists(db, "schema_migrations")
	if err != nil {
		return status, err
	}
	if !exists {
		status.Message = "sqlite auth schema_migrations table does not exist"
		return status, nil
	}
	status.Available = true
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&status.Version, &status.Dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			status.Available = false
			status.Message = "sqlite auth schema_migrations table is empty"
			return status, nil
		}
		return status, err
	}
	status.Message = "sqlite auth migration status read from configured auth database path"
	return status, nil
}

func checkSQLiteAuthTables(db *sql.DB, status *MigrationStatus) error {
	status.TablesChecked = cloneStrings(status.RequiredTables)
	for _, table := range status.RequiredTables {
		exists, err := tableExists(db, table)
		if err != nil {
			return err
		}
		if !exists {
			status.MissingTables = append(status.MissingTables, table)
		}
	}
	status.RequiredTablesPresent = len(status.MissingTables) == 0
	return nil
}

func checkPostgresAuthTables(dsn string, status *MigrationStatus) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("postgres auth database dsn is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	status.TablesChecked = cloneStrings(status.RequiredTables)
	for _, table := range status.RequiredTables {
		exists, err := postgresTableExists(db, table)
		if err != nil {
			return err
		}
		if !exists {
			status.MissingTables = append(status.MissingTables, table)
		}
	}
	status.RequiredTablesPresent = len(status.MissingTables) == 0
	return nil
}

func postgresTableExists(db *sql.DB, table string) (bool, error) {
	var exists bool
	err := db.QueryRow(`SELECT EXISTS (
		SELECT 1
		FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = $1
	)`, table).Scan(&exists)
	return exists, err
}

func cloneStrings(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func sqliteReadOnlyDSN(dbPath string) string {
	values := url.Values{}
	values.Set("mode", "ro")
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}).String()
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
