package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/migrate"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type migrateOptions struct {
	configPath   string
	rewriteV2    bool
	rebuildIndex bool
	dryRun       bool
	format       string
	stdout       io.Writer
}

func newMigrateCommand(runtime *cliRuntime) *cobra.Command {
	var rewriteV2 bool
	var rebuildIndex bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Rewrite legacy cassettes and rebuild the trace metadata index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runMigrateWithOptions(migrateOptions{
					configPath:   runtime.configPath(),
					rewriteV2:    rewriteV2,
					rebuildIndex: rebuildIndex,
					dryRun:       dryRun,
					format:       runtime.outputFormat(),
					stdout:       cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().BoolVar(&rewriteV2, "rewrite-v2", true, "Rewrite legacy V2 cassette files to V3")
	cmd.Flags().BoolVar(&rebuildIndex, "rebuild-index", true, "Rebuild SQLite metadata index from cassette files")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview migration without changing cassette files or the metadata index")
	return cmd
}

func runMigrate(args []string) int {
	fs := pflag.NewFlagSet("migrate", pflag.ContinueOnError)
	configPath := fs.StringP("config", "c", "config.yaml", "Path to configuration file")
	rewriteV2 := fs.Bool("rewrite-v2", true, "Rewrite legacy V2 cassette files to V3")
	rebuildIndex := fs.Bool("rebuild-index", true, "Rebuild SQLite metadata index from cassette files")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(normalizeLegacyFlagArgs(args)); err != nil {
		return 2
	}
	return runMigrateWithOptions(migrateOptions{
		configPath:   *configPath,
		rewriteV2:    *rewriteV2,
		rebuildIndex: *rebuildIndex,
		format:       "text",
		stdout:       os.Stdout,
	})
}

func runMigrateWithOptions(opts migrateOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	if opts.dryRun {
		return writeDryRunResult(opts.stdout, opts.format, "migrate", map[string]any{
			"dry_run":         true,
			"mutated":         false,
			"output_dir":      cfg.TraceOutputDir(),
			"rewrite_v2":      opts.rewriteV2,
			"rebuild_index":   opts.rebuildIndex,
			"database_driver": cfg.DatabaseDriver(),
			"database_dsn":    config.RedactDSN(cfg.DatabaseDSN()),
		})
	}
	if cfg.DatabaseAutoMigrate() {
		if err := auth.MigrateDatabaseUp(cfg.DatabaseDriver(), cfg.DatabaseDSN(), 0); err != nil {
			slog.Error("Database migration failed", "error", err)
			return 1
		}
	}

	traceStore, err := store.NewWithDatabase(
		cfg.TraceOutputDir(),
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
	)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()

	result, err := migrate.Run(traceStore, migrate.Options{
		OutputDir: cfg.TraceOutputDir(),
		RewriteV2: opts.rewriteV2,
		RebuildDB: opts.rebuildIndex,
	})
	if err != nil {
		slog.Error("Migration failed", "error", err)
		return 1
	}

	slog.Info(
		"Migration finished",
		"output_dir", cfg.TraceOutputDir(),
		"scanned_files", result.ScannedFiles,
		"converted_files", result.ConvertedFiles,
		"skipped_v3_files", result.SkippedV3Files,
		"indexed_rows", result.RebuiltIndexRows,
	)
	if opts.format == "json" {
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "migrate", map[string]any{
			"mutated":            opts.rewriteV2 || opts.rebuildIndex,
			"output_dir":         cfg.TraceOutputDir(),
			"scanned_files":      result.ScannedFiles,
			"converted_files":    result.ConvertedFiles,
			"skipped_v3_files":   result.SkippedV3Files,
			"rebuilt_index_rows": result.RebuiltIndexRows,
		}, nil); err != nil {
			slog.Error("Write command result failed", "error", err)
			return 1
		}
	}
	return 0
}
