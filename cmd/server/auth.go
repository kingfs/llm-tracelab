package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

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
	if opts.dryRun {
		return writeDryRunResult(opts.stdout, opts.format, "auth.migrate."+opts.direction, map[string]any{
			"dry_run":   true,
			"mutated":   false,
			"driver":    cfg.DatabaseDriver(),
			"dsn":       config.RedactDSN(cfg.DatabaseDSN()),
			"direction": opts.direction,
			"steps":     opts.steps,
			"all":       opts.all,
		})
	}
	switch opts.direction {
	case "up":
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), opts.steps); err != nil {
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	case "down":
		if err := auth.MigrateDatabaseDown(cfg.DatabaseDriver(), cfg.DatabaseDSN(), opts.steps, opts.all); err != nil {
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
	if cfg.DatabaseAutoMigrate() {
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), 0); err != nil {
			slog.Error("Database migration failed", "error", err)
			return nil, nil, 1
		}
	}
	st, err := auth.OpenDatabase(
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
	if err != nil {
		slog.Error("Open auth store failed", "error", err)
		return nil, nil, 1
	}
	return cfg, st, 0
}
