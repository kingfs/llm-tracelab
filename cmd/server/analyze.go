package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/kingfs/llm-tracelab/internal/analyzer"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/observeworker"
	"github.com/kingfs/llm-tracelab/internal/sessionanalysis"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/spf13/cobra"
)

type analyzeReparseOptions struct {
	configPath string
	traceID    string
	format     string
	stdout     io.Writer
}

type analyzeScanOptions struct {
	configPath string
	traceID    string
	format     string
	stdout     io.Writer
}

type analyzeSessionOptions struct {
	configPath string
	sessionID  string
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
	cmd.AddCommand(newAnalyzeScanCommand(runtime))
	cmd.AddCommand(newAnalyzeSessionCommand(runtime))
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

func newAnalyzeScanCommand(runtime *cliRuntime) *cobra.Command {
	var traceID string
	cmd := &cobra.Command{
		Use:           "scan",
		Short:         "Run deterministic audit detectors for a parsed trace",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if traceID == "" {
				return cliUsageError("--trace-id is required", "trace-id")
			}
			return runCode(func() int {
				return runAnalyzeScan(analyzeScanOptions{
					configPath: runtime.configPath(),
					traceID:    traceID,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Trace ID to scan")
	return cmd
}

func newAnalyzeSessionCommand(runtime *cliRuntime) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:           "session",
		Short:         "Build deterministic session analysis for recorded traces",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return cliUsageError("--session-id is required", "session-id")
			}
			return runCode(func() int {
				return runAnalyzeSession(analyzeSessionOptions{
					configPath: runtime.configPath(),
					sessionID:  sessionID,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to analyze")
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

	registry := observe.NewDefaultRegistry()
	obs, err := observeworker.ReparseTrace(context.Background(), traceStore, registry, opts.traceID)
	if err != nil {
		slog.Error("Failed to reparse trace", "trace_id", opts.traceID, "error", err)
		return 1
	}
	if err := traceStore.SaveObservation(obs); err != nil {
		slog.Error("Failed to save observation", "trace_id", obs.TraceID, "error", err)
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

func runAnalyzeSession(opts analyzeSessionOptions) int {
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

	summary, err := traceStore.GetSession(opts.sessionID)
	if err != nil {
		slog.Error("Failed to load session", "session_id", opts.sessionID, "error", err)
		return 1
	}
	traces, err := traceStore.ListTracesBySession(opts.sessionID)
	if err != nil {
		slog.Error("Failed to load session traces", "session_id", opts.sessionID, "error", err)
		return 1
	}
	findingsByTrace := map[string][]observe.Finding{}
	for _, trace := range traces {
		findings, err := traceStore.ListFindings(trace.ID, store.FindingFilter{})
		if err != nil {
			slog.Error("Failed to load trace findings", "trace_id", trace.ID, "error", err)
			return 1
		}
		findingsByTrace[trace.ID] = findings
	}
	output := sessionanalysis.Build(summary, traces, findingsByTrace)
	outputJSON, err := sessionanalysis.Marshal(output)
	if err != nil {
		slog.Error("Failed to marshal session analysis", "session_id", opts.sessionID, "error", err)
		return 1
	}
	runID, err := traceStore.SaveAnalysisRun(store.AnalysisRunRecord{
		SessionID:       opts.sessionID,
		Kind:            sessionanalysis.Kind,
		Analyzer:        sessionanalysis.AnalyzerName,
		AnalyzerVersion: sessionanalysis.AnalyzerVersion,
		InputRef:        "session:" + opts.sessionID,
		OutputJSON:      outputJSON,
		Status:          "completed",
	})
	if err != nil {
		slog.Error("Failed to save analysis run", "session_id", opts.sessionID, "error", err)
		return 1
	}
	result := map[string]any{
		"id":               runID,
		"session_id":       opts.sessionID,
		"kind":             sessionanalysis.Kind,
		"analyzer":         sessionanalysis.AnalyzerName,
		"analyzer_version": sessionanalysis.AnalyzerVersion,
		"trace_count":      len(output.TraceRefs),
		"finding_refs":     len(output.FindingRefs),
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze session", result, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "analyzed session %s (%d traces, %d finding refs)\n", opts.sessionID, len(output.TraceRefs), len(output.FindingRefs))
		return err
	}); err != nil {
		slog.Error("Write command result failed", "error", err)
		return 1
	}
	return 0
}

func runAnalyzeScan(opts analyzeScanOptions) int {
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

	obs, err := traceStore.GetObservation(opts.traceID)
	if err != nil {
		slog.Error("Failed to load observation", "trace_id", opts.traceID, "error", err)
		return 1
	}
	findings, err := analyzer.NewRunner().Analyze(context.Background(), obs)
	if err != nil {
		slog.Error("Failed to scan trace", "trace_id", opts.traceID, "error", err)
		return 1
	}
	if err := traceStore.SaveFindings(opts.traceID, findings); err != nil {
		slog.Error("Failed to save findings", "trace_id", opts.traceID, "error", err)
		return 1
	}
	result := map[string]any{
		"trace_id": opts.traceID,
		"findings": len(findings),
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze scan", result, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "scanned trace %s (%d findings)\n", opts.traceID, len(findings))
		return err
	}); err != nil {
		slog.Error("Write command result failed", "error", err)
		return 1
	}
	return 0
}
