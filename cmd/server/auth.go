package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/appdbmigrate"
	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/spf13/cobra"
)

type authMigrateOptions struct {
	configPath string
	direction  string
	steps      int
	all        bool
	dryRun     bool
	checkDB    bool
	format     string
	stdout     io.Writer
}

type authUserOptions struct {
	configPath string
	username   string
	password   string
	dryRun     bool
	format     string
	stdout     io.Writer
}

type authTokenOptions struct {
	configPath string
	username   string
	name       string
	scope      string
	ttl        time.Duration
	dryRun     bool
	format     string
	stdout     io.Writer
}

func newAuthCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "auth",
		Short:         "Manage users, tokens, and auth database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	migrateCmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Manage auth database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand(runtime, "up", "Apply auth database migrations"))
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand(runtime, "down", "Roll back auth database migrations"))
	migrateCmd.AddCommand(newAuthMigrateStatusCommand(runtime))
	cmd.AddCommand(migrateCmd)
	cmd.AddCommand(newAuthInitUserCommand(runtime))
	cmd.AddCommand(newAuthResetPasswordCommand(runtime))
	cmd.AddCommand(newAuthCreateTokenCommand(runtime))
	return cmd
}

func newAuthMigrateDirectionCommand(runtime *cliRuntime, direction string, short string) *cobra.Command {
	var steps int
	var all bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   direction,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAuthMigrateWithOptions(authMigrateOptions{
					configPath: runtime.configPath(),
					direction:  direction,
					steps:      steps,
					all:        all,
					dryRun:     dryRun,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().IntVar(&steps, "step", 0, "Apply only N migration steps")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview migration without changing the database")
	if direction == "down" {
		cmd.Flags().BoolVar(&all, "all", false, "Roll back all migrations")
	}
	return cmd
}

func newAuthMigrateStatusCommand(runtime *cliRuntime) *cobra.Command {
	var checkDB bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show auth database migration status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAuthMigrateWithOptions(authMigrateOptions{
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

func newAuthInitUserCommand(runtime *cliRuntime) *cobra.Command {
	var username string
	var password string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "init-user",
		Short: "Create the initial auth user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAuthInitUserWithOptions(authUserOptions{
					configPath: runtime.configPath(),
					username:   username,
					password:   password,
					dryRun:     dryRun,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "Username")
	cmd.Flags().StringVar(&password, "password", "", "Password")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview user creation without changing the database")
	return cmd
}

func newAuthResetPasswordCommand(runtime *cliRuntime) *cobra.Command {
	var username string
	var password string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Reset an auth user's password",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAuthResetPasswordWithOptions(authUserOptions{
					configPath: runtime.configPath(),
					username:   username,
					password:   password,
					dryRun:     dryRun,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "Username")
	cmd.Flags().StringVar(&password, "password", "", "New password")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview password reset without changing the database")
	return cmd
}

func newAuthCreateTokenCommand(runtime *cliRuntime) *cobra.Command {
	var username string
	var name string
	var scope string
	var ttl time.Duration
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "create-token",
		Short: "Create an API token for an auth user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAuthCreateTokenWithOptions(authTokenOptions{
					configPath: runtime.configPath(),
					username:   username,
					name:       name,
					scope:      scope,
					ttl:        ttl,
					dryRun:     dryRun,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "Username")
	cmd.Flags().StringVar(&name, "name", "cli", "Token name")
	cmd.Flags().StringVar(&scope, "scope", auth.DefaultTokenScope, "Token scope")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "Token TTL, 0 means no expiration")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview token creation without changing the database")
	return cmd
}

func runAuthMigrateWithOptions(opts authMigrateOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := authMigrationReport(cfg, opts.direction, opts.steps, opts.all, opts.dryRun, false, opts.checkDB)
	if opts.direction == "status" && opts.checkDB {
		if err := applyAuthStatusCheck(cfg, result); err != nil {
			slog.Error("Auth database status check failed", "error", err)
			return 1
		}
	}
	if opts.dryRun {
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "auth.migrate."+opts.direction, result, func(w io.Writer) error {
			fmt.Fprintf(w, "dry-run auth.migrate.%s: no changes will be applied\n", opts.direction)
			writeAuthMigrationReportText(w, result)
			return nil
		}); err != nil {
			slog.Error("Write auth migrate dry-run result failed", "error", err)
			return 1
		}
		return 0
	}
	switch opts.direction {
	case "status":
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "auth.migrate.status", result, func(w io.Writer) error {
			fmt.Fprintf(w, "auth database migration status\n")
			writeAuthMigrationReportText(w, result)
			return nil
		}); err != nil {
			slog.Error("Write auth migrate status result failed", "error", err)
			return 1
		}
		return 0
	case "up":
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), opts.steps); err != nil {
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	case "down":
		if err := auth.MigrateDatabaseDown(cfg.DatabaseDriver(), cfg.DatabaseDSN(), opts.steps, opts.all); err != nil {
			if errors.Is(err, auth.ErrPostgresAuthRollbackUnsupported) {
				fmt.Fprintln(os.Stderr, err.Error())
				return 2
			}
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown auth migrate direction %q\n", opts.direction)
		return 2
	}
	slog.Info("Database migration finished", "driver", cfg.DatabaseDriver(), "dsn", config.RedactDSN(cfg.DatabaseDSN()), "direction", opts.direction)
	return 0
}

func authMigrationReport(cfg *config.Config, direction string, steps int, all bool, dryRun bool, mutated bool, checkDB bool) map[string]any {
	source := "sqlite-embedded-sql"
	sourcePath := "ent/migrations"
	mode := "versioned-sql"
	versioned := true
	sharedApplicationNamespace := false
	namespaceSplit := true
	productionReady := false
	storageRole := appdbmigrate.SQLiteStorageRole
	storageContract := "sqlite_auth_migrations_for_legacy_dev_test"
	effectiveNamespace := "auth"
	schemaAuthority := "sqlite_auth_embedded_migrations"
	postgresAuthNamespaceStrategy := "not_applicable"
	independentAuthNamespaceStatus := "implemented"
	independentAuthNamespacePlan := "sqlite auth migrations already use the configured auth database migration namespace"
	authNamespaceAdoptionStatus := "not_required"
	authNamespaceAdoptionPlan := "sqlite auth migrations already own their configured migration namespace"
	authNamespaceDryRunSemantics := "report configured auth migration source and planned operation without mutating the database"
	authNamespaceStatusSemantics := "read the configured sqlite auth migration namespace when --check-db is requested"
	authNamespaceRollbackScope := "auth_database_migration_set"
	rollbackSupported := true
	authNamespaceTestGate := "default offline sqlite tests; no external database required"
	authMigrationCommandRole := "manage sqlite auth-owned migrations"
	const namespaceNote = "postgres auth tables are owned by the application schema_migrations namespace; an independent auth namespace has not been split yet"
	note := "sqlite auth migrations use the configured auth database path and embedded sqlite migration files"
	if normalizeAuthStoreDriver(cfg.DatabaseDriver()) == "postgres" {
		source = "postgres-checked-in-sql"
		sourcePath = "ent/postgres-migrations"
		productionReady = true
		storageRole = "production"
		storageContract = "postgres_application_schema_owns_auth_tables"
		effectiveNamespace = "application"
		schemaAuthority = "application_postgres_migration_set"
		sharedApplicationNamespace = true
		namespaceSplit = false
		postgresAuthNamespaceStrategy = "shared_application_schema_migrations"
		independentAuthNamespaceStatus = "not_implemented"
		independentAuthNamespacePlan = "optional future work: split auth-owned Postgres migrations into a separately versioned auth namespace only after an explicit idempotent adoption design"
		authNamespaceAdoptionStatus = "design_required_not_implemented"
		authNamespaceAdoptionPlan = "future adoption must verify users/api_tokens under the shared application schema_migrations namespace, initialize an independent auth namespace marker idempotently, and avoid dropping or rolling back application tables"
		authNamespaceDryRunSemantics = "report-only; future adoption dry-run must not create an auth namespace marker, mutate auth tables, or change shared schema_migrations"
		authNamespaceStatusSemantics = "read-only; status reports shared application namespace state and auth-owned table health"
		authNamespaceRollbackScope = "unsupported_from_auth_cli_shared_application_migration_set"
		rollbackSupported = false
		authNamespaceTestGate = "default tests stay offline; Postgres adoption/status checks must be DSN-gated"
		authMigrationCommandRole = "apply_or_verify_application_postgres_migration_set_for_auth_tables"
		note = namespaceNote
	}
	return map[string]any{
		"dry_run":                               dryRun,
		"mutated":                               mutated,
		"driver":                                cfg.DatabaseDriver(),
		"dsn":                                   config.RedactDSN(cfg.DatabaseDSN()),
		"direction":                             direction,
		"steps":                                 steps,
		"all":                                   all,
		"database_namespace":                    "auth",
		"effective_database_namespace":          effectiveNamespace,
		"migration_mode":                        mode,
		"migration_source":                      source,
		"migration_source_path":                 sourcePath,
		"schema_authority":                      schemaAuthority,
		"schema_versioned":                      versioned,
		"production_storage_driver":             appdbmigrate.ProductionStorageDriver,
		"production_ready":                      productionReady,
		"storage_role":                          storageRole,
		"storage_contract":                      storageContract,
		"status_check":                          appDBStatusCheckMode(checkDB),
		"rollback_supported":                    rollbackSupported,
		"shared_application_namespace":          sharedApplicationNamespace,
		"independent_auth_namespace":            namespaceSplit,
		"auth_namespace_split":                  namespaceSplit,
		"application_namespace_shared":          sharedApplicationNamespace,
		"auth_migration_command_role":           authMigrationCommandRole,
		"postgres_auth_namespace_strategy":      postgresAuthNamespaceStrategy,
		"independent_auth_namespace_status":     independentAuthNamespaceStatus,
		"independent_auth_namespace_plan":       independentAuthNamespacePlan,
		"auth_namespace_adoption_status":        authNamespaceAdoptionStatus,
		"auth_namespace_adoption_plan":          authNamespaceAdoptionPlan,
		"auth_namespace_dry_run_semantics":      authNamespaceDryRunSemantics,
		"auth_namespace_status_semantics":       authNamespaceStatusSemantics,
		"auth_namespace_rollback_scope":         authNamespaceRollbackScope,
		"auth_namespace_test_gate":              authNamespaceTestGate,
		"auth_required_tables":                  auth.RequiredTables(),
		"auth_tables_checked":                   []string{},
		"auth_required_tables_present":          false,
		"auth_missing_tables":                   []string{},
		"namespace_note":                        note,
		"shared_migration_namespace_constraint": namespaceNote,
	}
}

func applyAuthStatusCheck(cfg *config.Config, result map[string]any) error {
	status, err := auth.CheckStatus(cfg.DatabaseDriver(), cfg.DatabaseDSN())
	if err != nil {
		return err
	}
	result["database_status_available"] = status.Available
	result["database_status_versioned"] = status.Versioned
	result["database_status_driver"] = status.Driver
	result["database_status_production_ready"] = status.ProductionReady
	result["database_status_effective_namespace"] = status.EffectiveDatabaseNamespace
	result["database_status_schema_authority"] = status.SchemaAuthority
	result["database_status_storage_contract"] = status.StorageContract
	result["database_status_storage_role"] = status.StorageRole
	result["database_status_namespace_mode"] = status.NamespaceMode
	result["database_status_rollback_supported"] = status.RollbackSupported
	result["database_status_rollback_scope"] = status.RollbackScope
	result["database_status_operator_advice"] = status.OperatorAdvice
	if status.Available && status.Versioned {
		result["database_migration_version"] = status.Version
		result["database_migration_dirty"] = status.Dirty
	}
	if status.DatabasePath != "" {
		result["database_path"] = status.DatabasePath
	}
	if status.SharedApplicationNamespace {
		result["database_status_shared_application_namespace"] = status.SharedApplicationNamespace
	}
	result["auth_required_tables"] = status.RequiredTables
	result["auth_tables_checked"] = status.TablesChecked
	result["auth_required_tables_present"] = status.RequiredTablesPresent
	result["auth_missing_tables"] = status.MissingTables
	if status.Message != "" {
		result["database_status_message"] = status.Message
	}
	return nil
}

func writeAuthMigrationReportText(w io.Writer, result map[string]any) {
	fmt.Fprintf(w, "driver: %s\n", result["driver"])
	fmt.Fprintf(w, "dsn: %s\n", result["dsn"])
	fmt.Fprintf(w, "database_namespace: %s\n", result["database_namespace"])
	fmt.Fprintf(w, "effective_database_namespace: %s\n", result["effective_database_namespace"])
	fmt.Fprintf(w, "migration_mode: %s\n", result["migration_mode"])
	fmt.Fprintf(w, "migration_source: %s\n", result["migration_source"])
	fmt.Fprintf(w, "migration_source_path: %s\n", result["migration_source_path"])
	fmt.Fprintf(w, "schema_authority: %s\n", result["schema_authority"])
	fmt.Fprintf(w, "schema_versioned: %v\n", result["schema_versioned"])
	fmt.Fprintf(w, "production_storage_driver: %s\n", result["production_storage_driver"])
	fmt.Fprintf(w, "production_ready: %v\n", result["production_ready"])
	fmt.Fprintf(w, "storage_role: %s\n", result["storage_role"])
	fmt.Fprintf(w, "storage_contract: %s\n", result["storage_contract"])
	fmt.Fprintf(w, "shared_application_namespace: %v\n", result["shared_application_namespace"])
	fmt.Fprintf(w, "independent_auth_namespace: %v\n", result["independent_auth_namespace"])
	fmt.Fprintf(w, "auth_namespace_split: %v\n", result["auth_namespace_split"])
	fmt.Fprintf(w, "postgres_auth_namespace_strategy: %s\n", result["postgres_auth_namespace_strategy"])
	fmt.Fprintf(w, "independent_auth_namespace_status: %s\n", result["independent_auth_namespace_status"])
	fmt.Fprintf(w, "independent_auth_namespace_plan: %s\n", result["independent_auth_namespace_plan"])
	fmt.Fprintf(w, "auth_namespace_adoption_status: %s\n", result["auth_namespace_adoption_status"])
	fmt.Fprintf(w, "auth_namespace_adoption_plan: %s\n", result["auth_namespace_adoption_plan"])
	fmt.Fprintf(w, "auth_namespace_dry_run_semantics: %s\n", result["auth_namespace_dry_run_semantics"])
	fmt.Fprintf(w, "auth_namespace_status_semantics: %s\n", result["auth_namespace_status_semantics"])
	fmt.Fprintf(w, "auth_namespace_rollback_scope: %s\n", result["auth_namespace_rollback_scope"])
	fmt.Fprintf(w, "auth_namespace_test_gate: %s\n", result["auth_namespace_test_gate"])
	fmt.Fprintf(w, "auth_migration_command_role: %s\n", result["auth_migration_command_role"])
	fmt.Fprintf(w, "auth_required_tables: %s\n", strings.Join(stringSliceResult(result["auth_required_tables"]), ","))
	fmt.Fprintf(w, "auth_tables_checked: %s\n", strings.Join(stringSliceResult(result["auth_tables_checked"]), ","))
	fmt.Fprintf(w, "auth_required_tables_present: %v\n", result["auth_required_tables_present"])
	fmt.Fprintf(w, "auth_missing_tables: %s\n", strings.Join(stringSliceResult(result["auth_missing_tables"]), ","))
	if result["status_check"] != nil {
		fmt.Fprintf(w, "status_check: %s\n", result["status_check"])
	}
	if result["database_status_available"] != nil {
		fmt.Fprintf(w, "database_status_available: %v\n", result["database_status_available"])
		fmt.Fprintf(w, "database_status_production_ready: %v\n", result["database_status_production_ready"])
		fmt.Fprintf(w, "database_status_effective_namespace: %s\n", result["database_status_effective_namespace"])
		fmt.Fprintf(w, "database_status_schema_authority: %s\n", result["database_status_schema_authority"])
		fmt.Fprintf(w, "database_status_storage_contract: %s\n", result["database_status_storage_contract"])
		fmt.Fprintf(w, "database_status_storage_role: %s\n", result["database_status_storage_role"])
		fmt.Fprintf(w, "database_status_namespace_mode: %s\n", result["database_status_namespace_mode"])
		fmt.Fprintf(w, "database_status_rollback_supported: %v\n", result["database_status_rollback_supported"])
		fmt.Fprintf(w, "database_status_rollback_scope: %s\n", result["database_status_rollback_scope"])
		fmt.Fprintf(w, "database_status_operator_advice: %s\n", result["database_status_operator_advice"])
	}
	if result["database_migration_version"] != nil {
		fmt.Fprintf(w, "database_migration_version: %v\n", result["database_migration_version"])
		fmt.Fprintf(w, "database_migration_dirty: %v\n", result["database_migration_dirty"])
	}
	if result["database_path"] != nil {
		fmt.Fprintf(w, "database_path: %s\n", result["database_path"])
	}
	if result["database_status_shared_application_namespace"] != nil {
		fmt.Fprintf(w, "database_status_shared_application_namespace: %v\n", result["database_status_shared_application_namespace"])
	}
	if result["database_status_message"] != nil {
		fmt.Fprintf(w, "database_status_message: %s\n", result["database_status_message"])
	}
	fmt.Fprintf(w, "rollback_supported: %v\n", result["rollback_supported"])
	fmt.Fprintf(w, "namespace_note: %s\n", result["namespace_note"])
}

func stringSliceResult(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func runAuthInitUserWithOptions(opts authUserOptions) int {
	if strings.TrimSpace(opts.password) == "" {
		fmt.Fprintln(os.Stderr, "--password is required")
		return 2
	}
	if opts.dryRun {
		return writeDryRunResult(opts.stdout, opts.format, "auth.init-user", map[string]any{
			"dry_run":  true,
			"mutated":  false,
			"username": opts.username,
		})
	}
	cfg, st, code := openAuthStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	if _, err := st.CreateUser(context.Background(), opts.username, opts.password); err != nil {
		slog.Error("Create user failed", "error", err)
		return 1
	}
	slog.Info("User created", "driver", cfg.DatabaseDriver(), "username", opts.username)
	if opts.format == "json" {
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "auth.init-user", map[string]any{
			"mutated":  true,
			"username": opts.username,
		}, nil); err != nil {
			slog.Error("Write command result failed", "error", err)
			return 1
		}
	}
	return 0
}

func runAuthResetPasswordWithOptions(opts authUserOptions) int {
	if strings.TrimSpace(opts.password) == "" {
		fmt.Fprintln(os.Stderr, "--password is required")
		return 2
	}
	if opts.dryRun {
		return writeDryRunResult(opts.stdout, opts.format, "auth.reset-password", map[string]any{
			"dry_run":  true,
			"mutated":  false,
			"username": opts.username,
		})
	}
	cfg, st, code := openAuthStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	if err := st.ResetPassword(context.Background(), opts.username, opts.password); err != nil {
		slog.Error("Reset password failed", "error", err)
		return 1
	}
	slog.Info("Password reset", "driver", cfg.DatabaseDriver(), "username", opts.username)
	if opts.format == "json" {
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "auth.reset-password", map[string]any{
			"mutated":  true,
			"username": opts.username,
		}, nil); err != nil {
			slog.Error("Write command result failed", "error", err)
			return 1
		}
	}
	return 0
}

func runAuthCreateTokenWithOptions(opts authTokenOptions) int {
	if opts.dryRun {
		return writeDryRunResult(opts.stdout, opts.format, "auth.create-token", map[string]any{
			"dry_run":  true,
			"mutated":  false,
			"username": opts.username,
			"name":     opts.name,
			"scope":    opts.scope,
			"ttl":      opts.ttl.String(),
		})
	}
	cfg, st, code := openAuthStoreForCommand(opts.configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	token, err := st.CreateToken(context.Background(), opts.username, opts.name, opts.scope, opts.ttl)
	if err != nil {
		slog.Error("Create token failed", "error", err)
		return 1
	}
	if opts.format == "json" {
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "auth.create-token", map[string]any{
			"mutated":  true,
			"username": opts.username,
			"name":     opts.name,
			"scope":    opts.scope,
			"token":    token.Token,
		}, nil); err != nil {
			slog.Error("Write command result failed", "error", err)
			return 1
		}
	} else {
		fmt.Fprintln(stdoutOrDefault(opts.stdout), token.Token)
	}
	_ = cfg
	return 0
}

func stdoutOrDefault(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stdout
}

func writeDryRunResult(stdout io.Writer, format string, command string, result map[string]any) int {
	if format == "json" {
		if err := writeCLIResult(stdoutOrDefault(stdout), format, command, result, nil); err != nil {
			slog.Error("Write dry-run result failed", "error", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdoutOrDefault(stdout), "dry-run %s: no changes will be applied\n", command)
	return 0
}

func openAuthStoreForCommand(configPath string) (*config.Config, *auth.Store, int) {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", configPath, "error", err)
		return nil, nil, 1
	}
	st, err := openAuthStoreWithAutoSchema(cfg)
	if err != nil {
		slog.Error("Open auth store failed", "error", err)
		return nil, nil, 1
	}
	return cfg, st, 0
}
