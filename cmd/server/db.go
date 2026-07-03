package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/appdbmigrate"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/spf13/cobra"
)

type dbSecretOptions struct {
	configPath string
	format     string
	stdout     io.Writer
	outPath    string
	yes        bool
}

type appDBMigrateOptions struct {
	configPath string
	direction  string
	steps      int
	all        bool
	dryRun     bool
	checkDB    bool
	format     string
	stdout     io.Writer
}

type dbSummaryRebuildOptions struct {
	configPath string
	sessionID  string
	dryRun     bool
	format     string
	stdout     io.Writer
}

const appDBMigrateDownUnsupportedMessage = "db migrate down is unsupported for the production Postgres migration contract; restore from backup or use a reviewed manual migration plan"

func newDBCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "db",
		Short:         "Manage application database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	migrateCmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Apply or roll back application database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	migrateCmd.AddCommand(newAppDBMigrateDirectionCommand(runtime, "up", "Apply application database migrations"))
	migrateCmd.AddCommand(newAppDBMigrateDirectionCommand(runtime, "down", "Roll back application database migrations"))
	migrateCmd.AddCommand(newAppDBMigrateStatusCommand(runtime))
	migrateCmd.AddCommand(newAppDBMigrateOptimizeIndexesCommand(runtime))
	cmd.AddCommand(migrateCmd)
	cmd.AddCommand(newDBSummaryCommand(runtime))
	cmd.AddCommand(newDBSecretCommand(runtime))
	return cmd
}

func newDBSummaryCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "summary",
		Short:         "Maintain derived summary tables",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	rebuildCmd := &cobra.Command{
		Use:           "rebuild",
		Short:         "Rebuild derived summaries",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	rebuildCmd.AddCommand(newDBSummaryRebuildSessionsCommand(runtime))
	cmd.AddCommand(rebuildCmd)
	return cmd
}

func newDBSummaryRebuildSessionsCommand(runtime *cliRuntime) *cobra.Command {
	var dryRun bool
	var sessionID string
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Rebuild session_summaries from logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runDBSummaryRebuildSessionsWithOptions(dbSummaryRebuildOptions{
					configPath: runtime.configPath(),
					sessionID:  sessionID,
					dryRun:     dryRun,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview rebuild without changing session_summaries")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Rebuild only one session_id")
	return cmd
}

func newAppDBMigrateDirectionCommand(runtime *cliRuntime, direction string, short string) *cobra.Command {
	var steps int
	var all bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   direction,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := appDBMigrateOptions{
				configPath: runtime.configPath(),
				direction:  direction,
				steps:      steps,
				all:        all,
				dryRun:     dryRun,
				format:     runtime.outputFormat(),
				stdout:     cmd.OutOrStdout(),
			}
			code := runAppDBMigrateWithOptions(opts)
			if code == 0 {
				return nil
			}
			if direction == "down" && !dryRun {
				return cliExitError{
					code:     exitCodeUsage,
					category: errorCategoryUsage,
					errCode:  "UNSUPPORTED_DB_MIGRATE_DOWN",
					message:  appDBMigrateDownUnsupportedMessage,
					field:    "direction",
				}
			}
			return cliExitErrorFromCode(code)
		},
	}
	cmd.Flags().IntVar(&steps, "step", 0, "Apply only N migration steps")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview migration without changing the database")
	if direction == "down" {
		cmd.Flags().BoolVar(&all, "all", false, "Roll back all migrations")
	}
	return cmd
}

func newAppDBMigrateOptimizeIndexesCommand(runtime *cliRuntime) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "optimize-indexes",
		Short: "Apply non-transactional PostgreSQL index optimizations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := appDBMigrateOptions{
				configPath: runtime.configPath(),
				direction:  "optimize-indexes",
				dryRun:     dryRun,
				format:     runtime.outputFormat(),
				stdout:     cmd.OutOrStdout(),
			}
			code := runAppDBMigrateWithOptions(opts)
			if code == 0 {
				return nil
			}
			return cliExitErrorFromCode(code)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview index optimizations without changing the database")
	return cmd
}

func newAppDBMigrateStatusCommand(runtime *cliRuntime) *cobra.Command {
	var checkDB bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show application database migration status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAppDBMigrateWithOptions(appDBMigrateOptions{
					configPath: runtime.configPath(),
					direction:  "status",
					checkDB:    checkDB,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().BoolVar(&checkDB, "check-db", false, "Read migration status from the configured database")
	return cmd
}

func newDBSecretCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "secret",
		Short:         "Inspect and back up local channel secret encryption keys",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newDBSecretStatusCommand(runtime))
	cmd.AddCommand(newDBSecretExportCommand(runtime))
	cmd.AddCommand(newDBSecretRotateCommand(runtime))
	return cmd
}

func newDBSecretStatusCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show local channel secret encryption key status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runDBSecretStatusWithOptions(dbSecretOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	return cmd
}

func newDBSecretExportCommand(runtime *cliRuntime) *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the local channel secret encryption key for backup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runDBSecretExportWithOptions(dbSecretOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
					outPath:    outPath,
				})
			})
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "Write the exported key to a file instead of stdout")
	return cmd
}

func newDBSecretRotateCommand(runtime *cliRuntime) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the local channel secret encryption key and re-encrypt channel secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runDBSecretRotateWithOptions(dbSecretOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
					yes:        yes,
				})
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm rotation and local master key replacement")
	return cmd
}

func runAppDBMigrateWithOptions(opts appDBMigrateOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := appDBMigrationReport(cfg, opts.direction, opts.steps, opts.all, opts.dryRun, false, opts.checkDB)
	if opts.direction == "status" && opts.checkDB {
		if err := applyAppDBStatusCheck(cfg, result); err != nil {
			slog.Error("Application database status check failed", "error", err)
			return 1
		}
	}
	if opts.dryRun {
		if opts.format != "json" {
			fmt.Fprintf(stdoutOrDefault(opts.stdout), "dry-run db.migrate.%s: no changes will be applied\n", opts.direction)
			writeAppDBMigrationReportText(stdoutOrDefault(opts.stdout), result)
			return 0
		}
		return writeDryRunResult(opts.stdout, opts.format, "db.migrate."+opts.direction, result)
	}
	switch opts.direction {
	case "status":
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.migrate.status", result, func(w io.Writer) error {
			fmt.Fprintf(w, "application database migration status\n")
			writeAppDBMigrationReportText(w, result)
			return nil
		}); err != nil {
			slog.Error("Write db migrate status result failed", "error", err)
			return 1
		}
		return 0
	case "up":
		if err := migrateApplicationDatabaseUp(cfg, opts.steps); err != nil {
			slog.Error("Application database migration failed", "error", err)
			return 1
		}
		result["mutated"] = true
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.migrate.up", result, func(w io.Writer) error {
			fmt.Fprintf(w, "application database schema migration applied\n")
			writeAppDBMigrationReportText(w, result)
			return nil
		}); err != nil {
			slog.Error("Write db migrate result failed", "error", err)
			return 1
		}
		return 0
	case "optimize-indexes":
		optimization, err := optimizeApplicationDatabaseIndexes(cfg)
		if err != nil {
			slog.Error("Application database index optimization failed", "error", err)
			return 1
		}
		result["mutated"] = optimization.Applied
		result["index_optimization_applied"] = optimization.Applied
		result["index_optimization_statement_count"] = len(optimization.Statements)
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.migrate.optimize-indexes", result, func(w io.Writer) error {
			if optimization.Applied {
				fmt.Fprintf(w, "application database index optimizations applied\n")
			} else {
				fmt.Fprintf(w, "application database index optimizations not applicable\n")
			}
			writeAppDBMigrationReportText(w, result)
			return nil
		}); err != nil {
			slog.Error("Write db migrate optimize-indexes result failed", "error", err)
			return 1
		}
		return 0
	case "down":
		fmt.Fprintln(os.Stderr, appDBMigrateDownUnsupportedMessage)
		return 2
	default:
		fmt.Fprintf(os.Stderr, "unknown db migrate direction %q\n", opts.direction)
		return 2
	}
}

func appDBMigrationReport(cfg *config.Config, direction string, steps int, all bool, dryRun bool, mutated bool, checkDB bool) map[string]any {
	source := "sqlite-startup-schema-fallback"
	sourcePath := "internal/store raw DDL startup initialization"
	versioned := false
	driver := normalizeAuthStoreDriver(cfg.DatabaseDriver())
	productionReady := false
	storageRole := appdbmigrate.SQLiteStorageRole
	storageContract := appdbmigrate.SQLiteStorageContract
	migrationAuthority := "internal/store raw DDL startup initialization"
	schemaStrategy := appdbmigrate.SQLiteSchemaStrategy
	if appDBMigrationMode(driver) == "versioned-sql" {
		source = "postgres-checked-in-sql"
		sourcePath = "ent/postgres-migrations"
		versioned = true
		productionReady = true
		storageRole = "production"
		storageContract = appdbmigrate.PostgresStorageContract
		migrationAuthority = appdbmigrate.PostgresMigrationAuthority
		schemaStrategy = appdbmigrate.PostgresSchemaStrategy
	}
	result := map[string]any{
		"dry_run":                   dryRun,
		"mutated":                   mutated,
		"driver":                    cfg.DatabaseDriver(),
		"dsn":                       config.RedactDSN(cfg.DatabaseDSN()),
		"direction":                 direction,
		"steps":                     steps,
		"all":                       all,
		"database_namespace":        "application",
		"migration_mode":            appDBMigrationMode(cfg.DatabaseDriver()),
		"migration_source":          source,
		"migration_source_path":     sourcePath,
		"migration_authority":       migrationAuthority,
		"schema_strategy":           schemaStrategy,
		"schema_versioned":          versioned,
		"production_storage_driver": appdbmigrate.ProductionStorageDriver,
		"production_ready":          productionReady,
		"storage_role":              storageRole,
		"storage_contract":          storageContract,
		"status_check":              appDBStatusCheckMode(checkDB),
		"rollback_supported":        false,
		"auth_migration_scope":      "excluded",
		"auth_migration_command":    "auth migrate",
	}
	if driver == "sqlite" {
		result["sqlite_schema_strategy"] = appdbmigrate.SQLiteSchemaStrategy
		result["sqlite_versioned_migration_status"] = appdbmigrate.SQLiteVersionedMigrationStatus
		result["sqlite_migration_advice"] = appdbmigrate.SQLiteMigrationAdvice
	}
	if direction == "optimize-indexes" {
		statements := appdbmigrate.PostgresIndexOptimizationStatements()
		result["index_optimization_authority"] = appdbmigrate.PostgresIndexOptimizationAuthority
		result["index_optimization_non_transactional"] = true
		result["index_optimization_concurrent"] = true
		result["index_optimization_applicable"] = driver == "postgres"
		result["index_optimization_statement_count"] = len(statements)
		if driver == "postgres" {
			result["index_optimization_statements"] = statements
		} else {
			result["index_optimization_statement_count"] = 0
			result["index_optimization_status"] = "not_applicable"
		}
	}
	return result
}

func appDBStatusCheckMode(checkDB bool) string {
	if checkDB {
		return "database"
	}
	return "configuration-only"
}

func applyAppDBStatusCheck(cfg *config.Config, result map[string]any) error {
	status, err := appdbmigrate.CheckStatus(cfg.DatabaseDriver(), cfg.DatabaseDSN())
	if err != nil {
		return err
	}
	result["database_status_available"] = status.Available
	result["database_status_versioned"] = status.Versioned
	result["database_status_driver"] = status.Driver
	result["database_status_production_ready"] = status.ProductionReady
	result["database_status_storage_role"] = status.StorageRole
	result["database_status_storage_contract"] = status.StorageContract
	result["database_status_migration_authority"] = status.MigrationAuthority
	result["database_status_schema_strategy"] = status.SchemaStrategy
	if status.Driver == "sqlite" {
		result["database_required_tables_present"] = status.RequiredTablesPresent
	}
	if status.Available && status.Versioned {
		result["database_migration_version"] = status.Version
		result["database_migration_dirty"] = status.Dirty
	}
	if status.SchemaMarker != "" {
		result["database_schema_marker"] = status.SchemaMarker
		result["database_schema_marker_version"] = status.SchemaMarkerVersion
	}
	if len(status.MissingTables) > 0 {
		result["database_missing_tables"] = status.MissingTables
	}
	if status.Message != "" {
		result["database_status_message"] = status.Message
	}
	if status.Advice != "" {
		result["database_status_advice"] = status.Advice
	}
	return nil
}

func writeAppDBMigrationReportText(w io.Writer, result map[string]any) {
	fmt.Fprintf(w, "driver: %s\n", result["driver"])
	fmt.Fprintf(w, "dsn: %s\n", result["dsn"])
	fmt.Fprintf(w, "database_namespace: %s\n", result["database_namespace"])
	fmt.Fprintf(w, "migration_mode: %s\n", result["migration_mode"])
	fmt.Fprintf(w, "migration_source: %s\n", result["migration_source"])
	fmt.Fprintf(w, "migration_source_path: %s\n", result["migration_source_path"])
	fmt.Fprintf(w, "migration_authority: %s\n", result["migration_authority"])
	fmt.Fprintf(w, "schema_strategy: %s\n", result["schema_strategy"])
	fmt.Fprintf(w, "schema_versioned: %v\n", result["schema_versioned"])
	fmt.Fprintf(w, "production_storage_driver: %s\n", result["production_storage_driver"])
	fmt.Fprintf(w, "production_ready: %v\n", result["production_ready"])
	fmt.Fprintf(w, "storage_role: %s\n", result["storage_role"])
	fmt.Fprintf(w, "storage_contract: %s\n", result["storage_contract"])
	if result["sqlite_schema_strategy"] != nil {
		fmt.Fprintf(w, "sqlite_schema_strategy: %s\n", result["sqlite_schema_strategy"])
		fmt.Fprintf(w, "sqlite_versioned_migration_status: %s\n", result["sqlite_versioned_migration_status"])
		fmt.Fprintf(w, "sqlite_migration_advice: %s\n", result["sqlite_migration_advice"])
	}
	if result["status_check"] != nil {
		fmt.Fprintf(w, "status_check: %s\n", result["status_check"])
	}
	if result["database_status_available"] != nil {
		fmt.Fprintf(w, "database_status_available: %v\n", result["database_status_available"])
		fmt.Fprintf(w, "database_status_production_ready: %v\n", result["database_status_production_ready"])
		fmt.Fprintf(w, "database_status_storage_role: %s\n", result["database_status_storage_role"])
		fmt.Fprintf(w, "database_status_storage_contract: %s\n", result["database_status_storage_contract"])
		fmt.Fprintf(w, "database_status_migration_authority: %s\n", result["database_status_migration_authority"])
		fmt.Fprintf(w, "database_status_schema_strategy: %s\n", result["database_status_schema_strategy"])
	}
	if result["database_migration_version"] != nil {
		fmt.Fprintf(w, "database_migration_version: %v\n", result["database_migration_version"])
		fmt.Fprintf(w, "database_migration_dirty: %v\n", result["database_migration_dirty"])
	}
	if result["database_schema_marker"] != nil {
		fmt.Fprintf(w, "database_schema_marker: %s\n", result["database_schema_marker"])
		fmt.Fprintf(w, "database_schema_marker_version: %v\n", result["database_schema_marker_version"])
	}
	if result["database_required_tables_present"] != nil {
		fmt.Fprintf(w, "database_required_tables_present: %v\n", result["database_required_tables_present"])
	}
	if result["database_missing_tables"] != nil {
		fmt.Fprintf(w, "database_missing_tables: %v\n", result["database_missing_tables"])
	}
	if result["database_status_message"] != nil {
		fmt.Fprintf(w, "database_status_message: %s\n", result["database_status_message"])
	}
	if result["database_status_advice"] != nil {
		fmt.Fprintf(w, "database_status_advice: %s\n", result["database_status_advice"])
	}
	if result["index_optimization_authority"] != nil {
		fmt.Fprintf(w, "index_optimization_authority: %s\n", result["index_optimization_authority"])
		fmt.Fprintf(w, "index_optimization_applicable: %v\n", result["index_optimization_applicable"])
		fmt.Fprintf(w, "index_optimization_non_transactional: %v\n", result["index_optimization_non_transactional"])
		fmt.Fprintf(w, "index_optimization_concurrent: %v\n", result["index_optimization_concurrent"])
		fmt.Fprintf(w, "index_optimization_statement_count: %v\n", result["index_optimization_statement_count"])
		if result["index_optimization_status"] != nil {
			fmt.Fprintf(w, "index_optimization_status: %s\n", result["index_optimization_status"])
		}
	}
	fmt.Fprintf(w, "rollback_supported: %v\n", result["rollback_supported"])
	fmt.Fprintf(w, "auth_migration_scope: %s (%s)\n", result["auth_migration_scope"], result["auth_migration_command"])
}

func migrateApplicationDatabaseUp(cfg *config.Config, steps int) error {
	if appDBMigrationMode(cfg.DatabaseDriver()) == "versioned-sql" {
		return appdbmigrate.MigrateUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), steps)
	}
	st, err := initializeApplicationDatabase(cfg)
	if err != nil {
		return err
	}
	return st.Close()
}

func optimizeApplicationDatabaseIndexes(cfg *config.Config) (appdbmigrate.IndexOptimizationResult, error) {
	if appDBMigrationMode(cfg.DatabaseDriver()) != "versioned-sql" {
		return appdbmigrate.IndexOptimizationResult{
			Driver:  normalizeAuthStoreDriver(cfg.DatabaseDriver()),
			Applied: false,
		}, nil
	}
	return appdbmigrate.OptimizeIndexes(context.Background(), cfg.DatabaseDriver(), cfg.DatabaseDSN())
}

func appDBMigrationMode(driver string) string {
	switch normalizeAuthStoreDriver(driver) {
	case "postgres":
		return "versioned-sql"
	default:
		return "schema-init"
	}
}

func openApplicationDatabase(cfg *config.Config) (*store.Store, error) {
	if cfg.DatabaseAutoMigrate() {
		if err := migrateApplicationDatabaseUp(cfg, 0); err != nil {
			return nil, err
		}
	}
	return store.NewWithDatabaseOptions(
		cfg.TraceOutputDir(),
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
		store.DatabaseOptions{
			AutoMigrate:           false,
			UseSessionSummaryRead: cfg.DatabaseUseSessionSummaryRead(),
		},
	)
}

func initializeApplicationDatabase(cfg *config.Config) (*store.Store, error) {
	return store.NewWithDatabase(
		cfg.TraceOutputDir(),
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
}

func runDBSummaryRebuildSessionsWithOptions(opts dbSummaryRebuildOptions) int {
	st, closeStore, code := openTraceStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer closeStore()

	sessionID := strings.TrimSpace(opts.sessionID)
	stats, err := st.SessionSummaryRebuildStats(sessionID)
	if err != nil {
		slog.Error("Inspect session summary rebuild candidates failed", "error", err)
		return 1
	}
	result := map[string]any{
		"dry_run":          opts.dryRun,
		"mutated":          false,
		"scope":            "all",
		"candidate_count":  stats.CandidateCount,
		"existing_count":   stats.ExistingCount,
		"would_delete_all": stats.WouldDeleteAll,
		"would_delete_one": stats.WouldDeleteOne,
	}
	if sessionID != "" {
		result["scope"] = "session"
		result["session_id"] = sessionID
	}
	if opts.dryRun {
		if opts.format != "json" {
			fmt.Fprintf(stdoutOrDefault(opts.stdout), "dry-run db.summary.rebuild.sessions: no changes will be applied\n")
			writeDBSummaryRebuildSessionsText(stdoutOrDefault(opts.stdout), result)
			return 0
		}
		return writeDryRunResult(opts.stdout, opts.format, "db.summary.rebuild.sessions", result)
	}

	if sessionID != "" {
		if err := st.RebuildSessionSummary(sessionID); err != nil {
			slog.Error("Rebuild session summary failed", "session_id", sessionID, "error", err)
			return 1
		}
		result["rebuilt_one"] = true
	} else {
		if err := st.RebuildSessionSummaries(); err != nil {
			slog.Error("Rebuild session summaries failed", "error", err)
			return 1
		}
		result["rebuilt_all"] = true
	}
	result["mutated"] = true
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.summary.rebuild.sessions", result, func(w io.Writer) error {
		fmt.Fprintf(w, "session summaries rebuilt\n")
		writeDBSummaryRebuildSessionsText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write session summary rebuild result failed", "error", err)
		return 1
	}
	return 0
}

func writeDBSummaryRebuildSessionsText(w io.Writer, result map[string]any) {
	fmt.Fprintf(w, "scope: %s\n", result["scope"])
	if result["session_id"] != nil {
		fmt.Fprintf(w, "session_id: %s\n", result["session_id"])
	}
	fmt.Fprintf(w, "candidate_count: %v\n", result["candidate_count"])
	fmt.Fprintf(w, "existing_count: %v\n", result["existing_count"])
	fmt.Fprintf(w, "mutated: %v\n", result["mutated"])
}

func runDBSecretStatusWithOptions(opts dbSecretOptions) int {
	st, closeStore, code := openTraceStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer closeStore()
	status := st.SecretStatus()
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.secret.status", status, func(w io.Writer) error {
		fmt.Fprintf(w, "mode: %s\n", status.Mode)
		fmt.Fprintf(w, "key_path: %s\n", status.KeyPath)
		fmt.Fprintf(w, "exists: %t\n", status.Exists)
		fmt.Fprintf(w, "readable: %t\n", status.Readable)
		if status.Fingerprint != "" {
			fmt.Fprintf(w, "fingerprint: %s\n", status.Fingerprint)
		}
		if status.Error != "" {
			fmt.Fprintf(w, "error: %s\n", status.Error)
		}
		return nil
	}); err != nil {
		slog.Error("Write db secret status failed", "error", err)
		return 1
	}
	if status.Error != "" || !status.Readable {
		return 1
	}
	return 0
}

func runDBSecretExportWithOptions(opts dbSecretOptions) int {
	st, closeStore, code := openTraceStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer closeStore()
	key, status, err := st.ExportLocalSecretKey()
	if err != nil {
		slog.Error("Export local secret key failed", "error", err)
		return 1
	}
	if strings.TrimSpace(opts.outPath) != "" {
		if err := os.WriteFile(opts.outPath, key, 0o600); err != nil {
			slog.Error("Write local secret key export failed", "path", opts.outPath, "error", err)
			return 1
		}
		result := map[string]any{
			"written":     true,
			"out":         opts.outPath,
			"mode":        status.Mode,
			"key_path":    status.KeyPath,
			"fingerprint": status.Fingerprint,
		}
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.secret.export", result, func(w io.Writer) error {
			fmt.Fprintf(w, "exported local secret key to %s\n", opts.outPath)
			fmt.Fprintf(w, "fingerprint: %s\n", status.Fingerprint)
			return nil
		}); err != nil {
			slog.Error("Write db secret export result failed", "error", err)
			return 1
		}
		return 0
	}
	if opts.format == "json" {
		result := map[string]any{
			"mode":        status.Mode,
			"key_path":    status.KeyPath,
			"fingerprint": status.Fingerprint,
			"key":         strings.TrimSpace(string(key)),
		}
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.secret.export", result, nil); err != nil {
			slog.Error("Write db secret export result failed", "error", err)
			return 1
		}
		return 0
	}
	if _, err := stdoutOrDefault(opts.stdout).Write(key); err != nil {
		slog.Error("Write local secret key export failed", "error", err)
		return 1
	}
	return 0
}

func runDBSecretRotateWithOptions(opts dbSecretOptions) int {
	if !opts.yes {
		fmt.Fprintln(stdoutOrDefault(opts.stdout), "refusing to rotate local secret key without --yes")
		return 2
	}
	st, closeStore, code := openTraceStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer closeStore()
	result, err := st.RotateLocalSecretKey()
	if err != nil {
		slog.Error("Rotate local secret key failed", "error", err)
		return 1
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "db.secret.rotate", result, func(w io.Writer) error {
		fmt.Fprintf(w, "rotated local secret key\n")
		fmt.Fprintf(w, "key_path: %s\n", result.KeyPath)
		fmt.Fprintf(w, "backup_path: %s\n", result.BackupPath)
		fmt.Fprintf(w, "old_fingerprint: %s\n", result.OldFingerprint)
		fmt.Fprintf(w, "new_fingerprint: %s\n", result.NewFingerprint)
		fmt.Fprintf(w, "channels: %d\n", result.ChannelCount)
		fmt.Fprintf(w, "api_keys: %d\n", result.APIKeyCount)
		fmt.Fprintf(w, "secret_headers: %d\n", result.HeaderCount)
		return nil
	}); err != nil {
		slog.Error("Write db secret rotate result failed", "error", err)
		return 1
	}
	return 0
}

func openTraceStoreForCommand(configPath string) (*store.Store, func(), int) {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", configPath, "error", err)
		return nil, func() {}, 1
	}
	st, err := openApplicationDatabase(cfg)
	if err != nil {
		slog.Error("Open trace store failed", "error", err)
		return nil, func() {}, 1
	}
	return st, func() { _ = st.Close() }, 0
}
