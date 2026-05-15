package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/reanalysis"
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

type analyzeRepairUsageOptions struct {
	configPath      string
	traceID         string
	rewriteCassette bool
	format          string
	stdout          io.Writer
}

type analyzeReanalyzeOptions struct {
	configPath  string
	traceID     string
	sessionID   string
	repairUsage bool
	reparse     bool
	scan        bool
	format      string
	stdout      io.Writer
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
	cmd.AddCommand(newAnalyzeRepairUsageCommand(runtime))
	cmd.AddCommand(newAnalyzeReanalyzeCommand(runtime))
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

func newAnalyzeRepairUsageCommand(runtime *cliRuntime) *cobra.Command {
	var traceID string
	var rewriteCassette bool
	cmd := &cobra.Command{
		Use:           "repair-usage",
		Short:         "Re-extract usage from a recorded trace and repair derived token metrics",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if traceID == "" {
				return cliUsageError("--trace-id is required", "trace-id")
			}
			return runCode(func() int {
				return runAnalyzeRepairUsage(analyzeRepairUsageOptions{
					configPath:      runtime.configPath(),
					traceID:         traceID,
					rewriteCassette: rewriteCassette,
					format:          runtime.outputFormat(),
					stdout:          cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Trace ID to repair")
	cmd.Flags().BoolVar(&rewriteCassette, "rewrite-cassette", false, "Rewrite V3 cassette prelude with repaired usage")
	return cmd
}

func newAnalyzeReanalyzeCommand(runtime *cliRuntime) *cobra.Command {
	var traceID string
	var sessionID string
	var repairUsage bool
	var reparse bool
	var scan bool
	cmd := &cobra.Command{
		Use:           "reanalyze",
		Short:         "Run composed reanalysis for a trace or session",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if traceID == "" && sessionID == "" {
				return cliUsageError("one of --trace-id or --session-id is required", "trace-id")
			}
			if traceID != "" && sessionID != "" {
				return cliUsageError("--trace-id and --session-id are mutually exclusive", "trace-id")
			}
			return runCode(func() int {
				return runAnalyzeReanalyze(analyzeReanalyzeOptions{
					configPath:  runtime.configPath(),
					traceID:     traceID,
					sessionID:   sessionID,
					repairUsage: repairUsage,
					reparse:     reparse,
					scan:        scan,
					format:      runtime.outputFormat(),
					stdout:      cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().StringVar(&traceID, "trace-id", "", "Trace ID to reanalyze")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to reanalyze")
	cmd.Flags().BoolVar(&repairUsage, "repair-usage", false, "Repair usage before other selected trace work")
	cmd.Flags().BoolVar(&reparse, "reparse", true, "Rebuild Observation IR")
	cmd.Flags().BoolVar(&scan, "scan", true, "Run deterministic audit scan")
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

	reanalysisResult, err := reanalysis.New(traceStore, reanalysis.Options{}).ReparseTrace(context.Background(), opts.traceID, reanalysis.TraceOptions{})
	if err != nil {
		slog.Error("Failed to reparse trace", "trace_id", opts.traceID, "error", err)
		return 1
	}
	output := map[string]any{
		"trace_id":       opts.traceID,
		"parser":         reanalysisResult.Observation.Parser,
		"parser_version": reanalysisResult.Observation.ParserVersion,
		"status":         reanalysisResult.Observation.Status,
		"request_nodes":  reanalysisResult.RequestNodes,
		"response_nodes": reanalysisResult.ResponseNodes,
		"stream_events":  reanalysisResult.StreamEvents,
		"job_id":         reanalysisResult.Job.ID,
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze reparse", output, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "reparsed trace %s with %s@%s (%d request nodes, %d response nodes, %d stream events)\n",
			opts.traceID, output["parser"], output["parser_version"], output["request_nodes"], output["response_nodes"], output["stream_events"])
		return err
	}); err != nil {
		slog.Error("Write command result failed", "error", err)
		return 1
	}
	return 0
}

func runAnalyzeRepairUsage(opts analyzeRepairUsageOptions) int {
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

	result, err := reanalysis.New(traceStore, reanalysis.Options{}).RepairTraceUsage(context.Background(), opts.traceID, reanalysis.RepairUsageOptions{
		RewriteCassette: opts.rewriteCassette,
	})
	if err != nil {
		slog.Error("Failed to repair usage", "trace_id", opts.traceID, "error", err)
		return 1
	}
	output := map[string]any{
		"trace_id":          opts.traceID,
		"changed":           result.Usage.Changed,
		"index_updated":     result.Usage.IndexUpdated,
		"cassette_rewrote":  result.Usage.CassetteRewrote,
		"prompt_tokens":     result.Usage.After.PromptTokens,
		"completion_tokens": result.Usage.After.CompletionTokens,
		"total_tokens":      result.Usage.After.TotalTokens,
		"job_id":            result.Job.ID,
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze repair-usage", output, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "repaired usage for trace %s (total tokens %d, cassette rewrite %t)\n",
			opts.traceID, result.Usage.After.TotalTokens, result.Usage.CassetteRewrote)
		return err
	}); err != nil {
		slog.Error("Write command result failed", "error", err)
		return 1
	}
	return 0
}

func runAnalyzeReanalyze(opts analyzeReanalyzeOptions) int {
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

	svc := reanalysis.New(traceStore, reanalysis.Options{})
	if opts.traceID != "" {
		var result reanalysis.Result
		if opts.repairUsage {
			if result, err = svc.RepairTraceUsage(context.Background(), opts.traceID, reanalysis.RepairUsageOptions{}); err != nil {
				slog.Error("Failed to repair usage", "trace_id", opts.traceID, "error", err)
				return 1
			}
		}
		switch {
		case opts.reparse && opts.scan:
			result, err = svc.ReanalyzeTrace(context.Background(), opts.traceID)
		case opts.reparse:
			result, err = svc.ReparseTrace(context.Background(), opts.traceID, reanalysis.TraceOptions{})
		case opts.scan:
			result, err = svc.RescanTrace(context.Background(), opts.traceID)
		}
		if err != nil {
			slog.Error("Failed to reanalyze trace", "trace_id", opts.traceID, "error", err)
			return 1
		}
		output := map[string]any{
			"trace_id": opts.traceID,
			"job_id":   result.Job.ID,
			"job_type": result.Job.JobType,
			"status":   result.Job.Status,
			"findings": result.FindingCount,
		}
		if result.Observation != nil {
			output["parser"] = result.Observation.Parser
			output["parser_version"] = result.Observation.ParserVersion
		}
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze reanalyze", output, func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "reanalyzed trace %s with job %d (%s)\n", opts.traceID, result.Job.ID, result.Job.JobType)
			return err
		}); err != nil {
			slog.Error("Write command result failed", "error", err)
			return 1
		}
		return 0
	}

	result, err := svc.ReanalyzeSession(context.Background(), opts.sessionID, reanalysis.SessionOptions{Reparse: opts.reparse, Scan: opts.scan})
	if err != nil {
		slog.Error("Failed to reanalyze session", "session_id", opts.sessionID, "error", err)
		return 1
	}
	output := map[string]any{
		"session_id":      opts.sessionID,
		"job_id":          result.Job.ID,
		"status":          result.Job.Status,
		"trace_count":     result.Session.TraceCount,
		"analysis_run_id": result.Session.AnalysisRunID,
		"finding_refs":    result.Session.FindingRefs,
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze reanalyze", output, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "reanalyzed session %s (%d traces)\n", opts.sessionID, result.Session.TraceCount)
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

	result, err := reanalysis.New(traceStore, reanalysis.Options{}).RescanTrace(context.Background(), opts.traceID)
	if err != nil {
		slog.Error("Failed to scan trace", "trace_id", opts.traceID, "error", err)
		return 1
	}
	output := map[string]any{
		"trace_id": opts.traceID,
		"findings": result.FindingCount,
		"job_id":   result.Job.ID,
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze scan", output, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "scanned trace %s (%d findings)\n", opts.traceID, result.FindingCount)
		return err
	}); err != nil {
		slog.Error("Write command result failed", "error", err)
		return 1
	}
	return 0
}
