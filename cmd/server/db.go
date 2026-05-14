package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/spf13/cobra"
)

type dbSecretOptions struct {
	configPath string
	format     string
	stdout     io.Writer
	outPath    string
}

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
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand(runtime, "up", "Apply application database migrations"))
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand(runtime, "down", "Roll back application database migrations"))
	cmd.AddCommand(migrateCmd)
	cmd.AddCommand(newDBSecretCommand(runtime))
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

func openTraceStoreForCommand(configPath string) (*store.Store, func(), int) {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", configPath, "error", err)
		return nil, func() {}, 1
	}
	st, err := store.NewWithDatabase(
		cfg.TraceOutputDir(),
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
	if err != nil {
		slog.Error("Open trace store failed", "error", err)
		return nil, func() {}, 1
	}
	return st, func() { _ = st.Close() }, 0
}
