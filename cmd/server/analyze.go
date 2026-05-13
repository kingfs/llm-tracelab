package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/spf13/cobra"
)

type analyzeReparseOptions struct {
	configPath string
	traceID    string
	format     string
	stdout     io.Writer
}

func newAnalyzeCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "analyze",
		Short:         "Analyze recorded traces and rebuild derived observations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newAnalyzeReparseCommand(runtime))
	return cmd
}

func newAnalyzeReparseCommand(runtime *cliRuntime) *cobra.Command {
	var traceID string
	cmd := &cobra.Command{
		Use:           "reparse",
		Short:         "Rebuild Observation IR for a recorded trace",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if traceID == "" {
				return cliUsageError("--trace-id is required", "trace-id")
			}
			return runCode(func() int {
				return runAnalyzeReparse(analyzeReparseOptions{
					configPath: runtime.configPath(),
					traceID:    traceID,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Trace ID to reparse")
	return cmd
}

func runAnalyzeReparse(opts analyzeReparseOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
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

	entry, err := traceStore.GetByID(opts.traceID)
	if err != nil {
		slog.Error("Trace not found", "trace_id", opts.traceID, "error", err)
		return 1
	}
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		slog.Error("Failed to read cassette", "path", entry.LogPath, "error", err)
		return 1
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		slog.Error("Failed to parse cassette prelude", "path", entry.LogPath, "error", err)
		return 1
	}
	_, reqBody, _, resBody := recordfile.ExtractSections(content, parsed)
	registry := observe.NewRegistry(observe.NewOpenAIParser())
	obs, err := registry.Parse(context.Background(), observe.ParseInput{
		TraceID:      entry.ID,
		CassettePath: entry.LogPath,
		Header:       parsed.Header,
		Events:       parsed.Events,
		RequestBody:  reqBody,
		ResponseBody: resBody,
		IsStream:     parsed.Header.Layout.IsStream,
	})
	if err != nil {
		slog.Error("Failed to reparse trace", "trace_id", entry.ID, "error", err)
		return 1
	}
	if err := traceStore.SaveObservation(obs); err != nil {
		slog.Error("Failed to save observation", "trace_id", entry.ID, "error", err)
		return 1
	}
	result := map[string]any{
		"trace_id":       obs.TraceID,
		"parser":         obs.Parser,
		"parser_version": obs.ParserVersion,
		"status":         obs.Status,
		"request_nodes":  len(obs.Request.Nodes),
		"response_nodes": len(obs.Response.Nodes),
		"stream_events":  len(obs.Stream.Events),
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze reparse", result, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "reparsed trace %s with %s@%s (%d request nodes, %d response nodes, %d stream events)\n",
			obs.TraceID, obs.Parser, obs.ParserVersion, len(obs.Request.Nodes), len(obs.Response.Nodes), len(obs.Stream.Events))
		return err
	}); err != nil {
		slog.Error("Write command result failed", "error", err)
		return 1
	}
	return 0
}
