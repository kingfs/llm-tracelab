package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

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
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand("up", "Apply auth database migrations"))
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand("down", "Roll back auth database migrations"))
	cmd.AddCommand(migrateCmd)
	cmd.AddCommand(newAuthInitUserCommand())
	cmd.AddCommand(newAuthResetPasswordCommand())
	cmd.AddCommand(newAuthCreateTokenCommand())
	return cmd
}

func newAuthMigrateDirectionCommand(direction string, short string) *cobra.Command {
	var steps int
	var all bool
	cmd := &cobra.Command{
		Use:   direction,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runArgs := append([]string{direction}, configArg(cmd)...)
			runArgs = append(runArgs, fmt.Sprintf("-step=%d", steps), fmt.Sprintf("-all=%t", all))
			return runCode(runAuthMigrate, runArgs)
		},
	}
	cmd.Flags().IntVar(&steps, "step", 0, "Apply only N migration steps")
	if direction == "down" {
		cmd.Flags().BoolVar(&all, "all", false, "Roll back all migrations")
	}
	return cmd
}

func newAuthInitUserCommand() *cobra.Command {
	var username string
	var password string
	cmd := &cobra.Command{
		Use:   "init-user",
		Short: "Create the initial auth user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runArgs := append(configArg(cmd), "--username", username, "--password", password)
			return runCode(runAuthInitUser, runArgs)
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "Username")
	cmd.Flags().StringVar(&password, "password", "", "Password")
	return cmd
}

func newAuthResetPasswordCommand() *cobra.Command {
	var username string
	var password string
	cmd := &cobra.Command{
		Use:   "reset-password",
		Short: "Reset an auth user's password",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runArgs := append(configArg(cmd), "--username", username, "--password", password)
			return runCode(runAuthResetPassword, runArgs)
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "Username")
	cmd.Flags().StringVar(&password, "password", "", "New password")
	return cmd
}

func newAuthCreateTokenCommand() *cobra.Command {
	var username string
	var name string
	var scope string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "create-token",
		Short: "Create an API token for an auth user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runArgs := append(configArg(cmd), "--username", username, "--name", name, "--scope", scope, "--ttl", ttl.String())
			return runCode(runAuthCreateToken, runArgs)
		},
	}
	cmd.Flags().StringVar(&username, "username", "admin", "Username")
	cmd.Flags().StringVar(&name, "name", "cli", "Token name")
	cmd.Flags().StringVar(&scope, "scope", auth.DefaultTokenScope, "Token scope")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "Token TTL, 0 means no expiration")
	return cmd
}

func runAuth(args []string) int {
	return run(append([]string{"auth"}, args...))
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
