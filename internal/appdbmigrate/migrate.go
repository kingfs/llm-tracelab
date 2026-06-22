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
	Driver    string
	Versioned bool
	Available bool
	Version   uint
	Dirty     bool
	Message   string
}

func CheckStatus(driver string, dsn string) (Status, error) {
	driver = normalizeDriver(driver)
	status := Status{Driver: driver}
	switch driver {
	case "postgres":
		status.Versioned = true
		return checkPostgresStatus(dsn, status)
	case "sqlite":
		status.Message = ErrSQLiteUsesStoreInit.Error()
		return status, nil
	default:
		return status, fmt.Errorf("application database driver %q is not supported by versioned migrations yet", driver)
	}
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
