package appdbmigrate

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	entmigrations "github.com/kingfs/llm-tracelab/ent"
	"github.com/kingfs/llm-tracelab/internal/config"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

const postgresMigrationRoot = "postgres-migrations"
const sqliteApplicationSchemaMarker = "app_schema_status"
const sqliteApplicationSchemaNamespace = "application"
const ProductionStorageDriver = "postgres"
const PostgresSchemaStrategy = "versioned_checked_in_sql"
const PostgresMigrationAuthority = "ent/postgres-migrations checked-in SQL via db migrate up"
const PostgresStorageContract = "postgres_versioned_migrations_are_the_production_storage_contract"
const SQLiteSchemaStrategy = "startup_schema_fallback"
const SQLiteVersionedMigrationStatus = "not_implemented"
const SQLiteStorageRole = "legacy_dev_test_compatibility"
const SQLiteStorageContract = "sqlite_startup_schema_fallback_for_legacy_dev_test_only"
const SQLiteMigrationAdvice = "SQLite application DB uses startup schema fallback for legacy/dev/test compatibility; run startup or db migrate up for idempotent schema initialization, and use Postgres for versioned production migrations."

var sqliteApplicationRequiredTables = []string{
	"logs",
	"responses",
	"response_items",
	"request_audits",
	"execution_events",
	"upstream_exchanges",
	"tool_call_audits",
}

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

var ErrSQLiteUsesStoreInit = errors.New("sqlite application versioned migration is not implemented; startup schema fallback remains active")

func MigrateDown(driver string, dsn string, steps int, all bool) error {
	driver = normalizeDriver(driver)
	switch driver {
	case "postgres":
		return migratePostgresDown(dsn, steps, all)
	case "sqlite":
		return ErrSQLiteUsesStoreInit
	default:
		return fmt.Errorf("application database driver %q is not supported by versioned migrations yet", driver)
	}
}

type Status struct {
	Driver                string
	Versioned             bool
	ProductionReady       bool
	StorageRole           string
	StorageContract       string
	MigrationAuthority    string
	SchemaStrategy        string
	Available             bool
	Version               uint
	Dirty                 bool
	SchemaMarker          string
	SchemaMarkerVersion   int
	RequiredTablesPresent bool
	MissingTables         []string
	Message               string
	Advice                string
}

func CheckStatus(driver string, dsn string) (Status, error) {
	driver = normalizeDriver(driver)
	status := Status{Driver: driver}
	switch driver {
	case "postgres":
		status.Versioned = true
		status.ProductionReady = true
		status.StorageRole = "production"
		status.StorageContract = PostgresStorageContract
		status.MigrationAuthority = PostgresMigrationAuthority
		status.SchemaStrategy = PostgresSchemaStrategy
		return checkPostgresStatus(dsn, status)
	case "sqlite":
		status.StorageRole = SQLiteStorageRole
		status.StorageContract = SQLiteStorageContract
		status.MigrationAuthority = "internal/store raw DDL startup initialization"
		status.SchemaStrategy = SQLiteSchemaStrategy
		status.Advice = SQLiteMigrationAdvice
		return checkSQLiteStatus(dsn, status)
	default:
		return status, fmt.Errorf("application database driver %q is not supported by versioned migrations yet", driver)
	}
}

func checkSQLiteStatus(dsn string, status Status) (Status, error) {
	dbPath := config.SQLitePathFromDSN(dsn)
	if strings.TrimSpace(dbPath) == "" {
		status.Message = "sqlite application database path is empty; read-only status check skipped; " + ErrSQLiteUsesStoreInit.Error()
		return status, nil
	}
	if dbPath == ":memory:" {
		status.Message = "sqlite in-memory database status is not inspectable with --check-db; " + ErrSQLiteUsesStoreInit.Error()
		return status, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.Message = "sqlite application database file does not exist; --check-db is read-only and did not create it; " + ErrSQLiteUsesStoreInit.Error()
			return status, nil
		}
		return status, err
	}

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(dbPath))
	if err != nil {
		return status, err
	}
	defer db.Close()

	for _, table := range sqliteApplicationRequiredTables {
		exists, err := sqliteTableExists(db, table)
		if err != nil {
			return status, err
		}
		if !exists {
			status.MissingTables = append(status.MissingTables, table)
		}
	}
	status.RequiredTablesPresent = len(status.MissingTables) == 0
	status.Available = status.RequiredTablesPresent

	markerExists, err := sqliteTableExists(db, sqliteApplicationSchemaMarker)
	if err != nil {
		return status, err
	}
	if !markerExists {
		if status.RequiredTablesPresent {
			status.Message = "sqlite application required tables are present; app_schema_status marker is missing, so this is treated as a compatible legacy startup-schema database; " + ErrSQLiteUsesStoreInit.Error()
		} else {
			status.Message = "sqlite application app_schema_status marker is missing and required tables are incomplete; startup schema fallback must initialize or repair the local database before production-like use; " + ErrSQLiteUsesStoreInit.Error()
		}
		return status, nil
	}

	status.SchemaMarker = sqliteApplicationSchemaMarker
	if err := db.QueryRow(`SELECT version FROM app_schema_status WHERE namespace = ?`, sqliteApplicationSchemaNamespace).Scan(&status.SchemaMarkerVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			status.Message = "sqlite application schema marker table exists but application row is missing; startup schema fallback should rewrite the marker on next schema initialization; " + ErrSQLiteUsesStoreInit.Error()
			return status, nil
		}
		return status, err
	}
	if status.RequiredTablesPresent {
		status.Message = fmt.Sprintf("sqlite application schema marker version %d is present; %s", status.SchemaMarkerVersion, ErrSQLiteUsesStoreInit.Error())
	} else {
		status.Message = fmt.Sprintf("sqlite application schema marker version %d is present but required tables are incomplete; %s", status.SchemaMarkerVersion, ErrSQLiteUsesStoreInit.Error())
	}
	return status, nil
}

func sqliteTableExists(db *sql.DB, table string) (bool, error) {
	var exists bool
	err := db.QueryRow(`SELECT EXISTS (
		SELECT 1
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	)`, table).Scan(&exists)
	return exists, err
}

func sqliteReadOnlyDSN(dbPath string) string {
	values := url.Values{}
	values.Set("mode", "ro")
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}).String()
}

func checkPostgresStatus(dsn string, status Status) (Status, error) {
	if strings.TrimSpace(dsn) == "" {
		return status, fmt.Errorf("postgres application database dsn is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return status, err
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (
		SELECT 1
		FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
	)`).Scan(&exists); err != nil {
		return status, err
	}
	if !exists {
		status.Message = "schema_migrations table does not exist"
		return status, nil
	}
	status.Available = true
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&status.Version, &status.Dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			status.Available = false
			status.Message = "schema_migrations table is empty"
			return status, nil
		}
		return status, err
	}
	return status, nil
}

func migratePostgresUp(dsn string, steps int) error {
	return migratePostgres(dsn, func(m *gomigrate.Migrate) error {
		if steps > 0 {
			return m.Steps(steps)
		}
		return m.Up()
	})
}

func migratePostgresDown(dsn string, steps int, all bool) error {
	return migratePostgres(dsn, func(m *gomigrate.Migrate) error {
		switch {
		case all:
			return m.Down()
		case steps > 0:
			return m.Steps(-steps)
		default:
			return m.Steps(-1)
		}
	})
}

func migratePostgres(dsn string, run func(*gomigrate.Migrate) error) error {
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
	err = run(m)
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
