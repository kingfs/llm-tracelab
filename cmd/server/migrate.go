package main

import (
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
}

func newMigrateCommand(runtime *cliRuntime) *cobra.Command {
	var rewriteV2 bool
	var rebuildIndex bool
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
				})
			})
		},
	}
	cmd.Flags().BoolVar(&rewriteV2, "rewrite-v2", true, "Rewrite legacy V2 cassette files to V3")
	cmd.Flags().BoolVar(&rebuildIndex, "rebuild-index", true, "Rebuild SQLite metadata index from cassette files")
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
	})
}

func runMigrateWithOptions(opts migrateOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
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
	return 0
}
