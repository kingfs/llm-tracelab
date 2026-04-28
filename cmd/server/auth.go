package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/spf13/cobra"
)

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "auth",
		Short:         "Manage users, tokens, and auth database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			printUsage(cmd.ErrOrStderr())
			return cliExitError{code: 2}
		},
	}
	migrateCmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Manage auth database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "auth migrate requires up or down")
			return cliExitError{code: 2}
		},
	}
	migrateCmd.AddCommand(commandAdapter("up", "Apply auth database migrations", func(args []string) int {
		return runAuthMigrate(append([]string{"up"}, args...))
	}))
	migrateCmd.AddCommand(commandAdapter("down", "Roll back auth database migrations", func(args []string) int {
		return runAuthMigrate(append([]string{"down"}, args...))
	}))
	cmd.AddCommand(migrateCmd)
	cmd.AddCommand(commandAdapter("init-user", "Create the initial auth user", runAuthInitUser))
	cmd.AddCommand(commandAdapter("reset-password", "Reset an auth user's password", runAuthResetPassword))
	cmd.AddCommand(commandAdapter("create-token", "Create an API token for an auth user", runAuthCreateToken))
	return cmd
}

func runAuth(args []string) int {
	cmd := newAuthCommand()
	cmd.SetArgs(args)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		var exit cliExitError
		if errors.As(err, &exit) {
			return exit.code
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runAuthMigrate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth migrate requires up or down")
		return 2
	}
	direction := args[0]
	fs := flag.NewFlagSet("auth migrate "+direction, flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	fs.StringVar(configPath, "config", "config.yaml", "Path to configuration file")
	steps := fs.Int("step", 0, "Apply only N migration steps")
	all := fs.Bool("all", false, "Roll back all migrations")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", *configPath, "error", err)
		return 1
	}
	switch direction {
	case "up":
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), *steps); err != nil {
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	case "down":
		if err := auth.MigrateDatabaseDown(cfg.DatabaseDriver(), cfg.DatabaseDSN(), *steps, *all); err != nil {
			slog.Error("Auth migration failed", "error", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown auth migrate direction %q\n", direction)
		return 2
	}
	slog.Info("Database migration finished", "driver", cfg.DatabaseDriver(), "dsn", config.RedactDSN(cfg.DatabaseDSN()), "direction", direction)
	return 0
}

func runAuthInitUser(args []string) int {
	fs := flag.NewFlagSet("auth init-user", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	fs.StringVar(configPath, "config", "config.yaml", "Path to configuration file")
	username := fs.String("username", "admin", "Username")
	password := fs.String("password", "", "Password")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*password) == "" {
		fmt.Fprintln(os.Stderr, "--password is required")
		return 2
	}
	cfg, st, code := openAuthStoreForCommand(*configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	if _, err := st.CreateUser(context.Background(), *username, *password); err != nil {
		slog.Error("Create user failed", "error", err)
		return 1
	}
	slog.Info("User created", "driver", cfg.DatabaseDriver(), "username", *username)
	return 0
}

func runAuthResetPassword(args []string) int {
	fs := flag.NewFlagSet("auth reset-password", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	fs.StringVar(configPath, "config", "config.yaml", "Path to configuration file")
	username := fs.String("username", "admin", "Username")
	password := fs.String("password", "", "New password")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*password) == "" {
		fmt.Fprintln(os.Stderr, "--password is required")
		return 2
	}
	cfg, st, code := openAuthStoreForCommand(*configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	if err := st.ResetPassword(context.Background(), *username, *password); err != nil {
		slog.Error("Reset password failed", "error", err)
		return 1
	}
	slog.Info("Password reset", "driver", cfg.DatabaseDriver(), "username", *username)
	return 0
}

func runAuthCreateToken(args []string) int {
	fs := flag.NewFlagSet("auth create-token", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	fs.StringVar(configPath, "config", "config.yaml", "Path to configuration file")
	username := fs.String("username", "admin", "Username")
	name := fs.String("name", "cli", "Token name")
	scope := fs.String("scope", auth.DefaultTokenScope, "Token scope")
	ttl := fs.Duration("ttl", 0, "Token TTL, 0 means no expiration")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, st, code := openAuthStoreForCommand(*configPath)
	if code != 0 {
		return code
	}
	defer st.Close()
	token, err := st.CreateToken(context.Background(), *username, *name, *scope, *ttl)
	if err != nil {
		slog.Error("Create token failed", "error", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, token.Token)
	_ = cfg
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
