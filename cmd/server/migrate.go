package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/migrate"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/spf13/cobra"
)

func newMigrateCommand() *cobra.Command {
	var rewriteV2 bool
	var rebuildIndex bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Rewrite legacy cassettes and rebuild the trace metadata index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runArgs := configArg(cmd)
			runArgs = append(runArgs, fmt.Sprintf("-rewrite-v2=%t", rewriteV2), fmt.Sprintf("-rebuild-index=%t", rebuildIndex))
			return runCode(runMigrate, runArgs)
		},
	}
	cmd.Flags().BoolVar(&rewriteV2, "rewrite-v2", true, "Rewrite legacy V2 cassette files to V3")
	cmd.Flags().BoolVar(&rebuildIndex, "rebuild-index", true, "Rebuild SQLite metadata index from cassette files")
	return cmd
}

func runMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	configPath := fs.String("c", "config.yaml", "Path to configuration file")
	fs.StringVar(configPath, "config", "config.yaml", "Path to configuration file")
	rewriteV2 := fs.Bool("rewrite-v2", true, "Rewrite legacy V2 cassette files to V3")
	rebuildIndex := fs.Bool("rebuild-index", true, "Rebuild SQLite metadata index from cassette files")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", *configPath, "error", err)
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
		RewriteV2: *rewriteV2,
		RebuildDB: *rebuildIndex,
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
