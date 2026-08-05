package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"

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

type analyzeBackfillExchangesOptions struct {
	configPath string
	dryRun     bool
	format     string
	stdout     io.Writer
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

type analyzeBatchOptions struct {
	configPath      string
	traceIDs        []string
	requestIDs      []string
	sessionID       string
	all             bool
	query           string
	provider        string
	model           string
	endpoint        string
	upstream        string
	status          string
	observation     string
	missingUsage    bool
	limit           int
	limitSet        bool
	workers         int
	repairUsage     bool
	reparse         bool
	scan            bool
	rewriteCassette bool
	enqueue         bool
	format          string
	stdout          io.Writer
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
	cmd.AddCommand(newAnalyzeBackfillExchangesCommand(runtime))
	cmd.AddCommand(newAnalyzeReanalyzeCommand(runtime))
	cmd.AddCommand(newAnalyzeBatchCommand(runtime))
	cmd.AddCommand(newAnalyzeRefreshCommand(runtime))
	cmd.AddCommand(newAnalyzeSessionCommand(runtime))
	return cmd
}

func newAnalyzeReparseCommand(runtime *cliRuntime) *cobra.Command {
	var traceID string
	cmd := &cobra.Command{
		Use:           "reparse",
		Short:         "Rebuild Observation IR for a recorded trace",
		Hidden:        true,
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
		Hidden:        true,
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
		Hidden:        true,
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

func newAnalyzeBackfillExchangesCommand(runtime *cliRuntime) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:           "backfill-exchanges",
		Short:         "Backfill exchange metadata in the DB index without rewriting cassettes",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runAnalyzeBackfillExchanges(analyzeBackfillExchangesOptions{
					configPath: runtime.configPath(),
					dryRun:     dryRun,
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report exchange metadata backfill counts without updating the DB index")
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
		Hidden:        true,
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

func newAnalyzeBatchCommand(runtime *cliRuntime) *cobra.Command {
	var opts analyzeBatchOptions
	cmd := &cobra.Command{
		Use:           "batch",
		Short:         "Run repair, reparse, scan, or reanalyze work for many traces",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.configPath = runtime.configPath()
			opts.format = runtime.outputFormat()
			opts.stdout = cmd.OutOrStdout()
			opts.limitSet = cmd.Flags().Changed("limit")
			if opts.rewriteCassette && !opts.repairUsage {
				return cliUsageError("--rewrite-cassette requires --repair-usage", "rewrite-cassette")
			}
			if opts.rewriteCassette && opts.enqueue {
				return cliUsageError("--rewrite-cassette is only supported without --enqueue", "rewrite-cassette")
			}
			if !opts.repairUsage && !opts.reparse && !opts.scan {
				opts.reparse = true
				opts.scan = true
			}
			if !opts.all && len(opts.traceIDs) == 0 && len(opts.requestIDs) == 0 && opts.sessionID == "" && !analyzeBatchHasFilter(opts) {
				return cliUsageError("one of --all, --trace-id, --request-id, --session-id, or a filter is required", "all")
			}
			return runCode(func() int {
				return runAnalyzeBatch(opts)
			})
		},
	}
	cmd.Flags().StringArrayVar(&opts.traceIDs, "trace-id", nil, "Trace ID to process; may be repeated")
	cmd.Flags().StringArrayVar(&opts.requestIDs, "request-id", nil, "Request ID to resolve and process; may be repeated")
	cmd.Flags().StringVar(&opts.sessionID, "session-id", "", "Process traces in a session")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Process all client-visible traces; combine with --limit to cap selection")
	cmd.Flags().StringVarP(&opts.query, "query", "q", "", "Free-text trace filter")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "Provider filter")
	cmd.Flags().StringVar(&opts.model, "model", "", "Model substring filter")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "Endpoint substring filter")
	cmd.Flags().StringVar(&opts.upstream, "upstream", "", "Selected upstream substring filter")
	cmd.Flags().StringVar(&opts.status, "status", "", "Status filter: success or error")
	cmd.Flags().StringVar(&opts.observation, "observation", "", "Observation status filter: parsed, failed, queued, running, or unparsed")
	cmd.Flags().BoolVar(&opts.missingUsage, "missing-usage", false, "Only process successful traces with missing token usage")
	cmd.Flags().IntVar(&opts.limit, "limit", 1000, "Maximum traces selected by filters; 0 means no CLI cap")
	cmd.Flags().IntVar(&opts.workers, "workers", 0, "Concurrent trace workers for direct execution; 0 uses an automatic local default")
	cmd.Flags().BoolVar(&opts.repairUsage, "repair-usage", false, "Repair usage before other selected work")
	cmd.Flags().BoolVar(&opts.reparse, "reparse", false, "Rebuild Observation IR")
	cmd.Flags().BoolVar(&opts.scan, "scan", false, "Run deterministic audit scan")
	cmd.Flags().BoolVar(&opts.rewriteCassette, "rewrite-cassette", false, "Rewrite V3 cassette prelude when repairing usage")
	cmd.Flags().BoolVar(&opts.enqueue, "enqueue", false, "Create analysis jobs without executing them in this process")
	return cmd
}

func newAnalyzeRefreshCommand(runtime *cliRuntime) *cobra.Command {
	var opts analyzeBatchOptions
	cmd := &cobra.Command{
		Use:           "refresh",
		Short:         "Refresh derived analysis for traces, sessions, or filtered request sets",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.configPath = runtime.configPath()
			opts.format = runtime.outputFormat()
			opts.stdout = cmd.OutOrStdout()
			opts.limitSet = cmd.Flags().Changed("limit")
			opts.reparse = true
			opts.scan = true
			if opts.rewriteCassette && !opts.repairUsage {
				return cliUsageError("--rewrite-cassette requires --repair-usage", "rewrite-cassette")
			}
			if opts.rewriteCassette && opts.enqueue {
				return cliUsageError("--rewrite-cassette is only supported without --enqueue", "rewrite-cassette")
			}
			if !opts.all && len(opts.traceIDs) == 0 && len(opts.requestIDs) == 0 && opts.sessionID == "" && !analyzeBatchHasFilter(opts) {
				return cliUsageError("one of --all, --trace-id, --request-id, --session-id, or a filter is required", "all")
			}
			return runCode(func() int {
				return runAnalyzeRefresh(opts)
			})
		},
	}
	cmd.Flags().StringArrayVar(&opts.traceIDs, "trace-id", nil, "Trace ID to refresh; may be repeated")
	cmd.Flags().StringArrayVar(&opts.requestIDs, "request-id", nil, "Request ID to resolve and refresh; may be repeated")
	cmd.Flags().StringVar(&opts.sessionID, "session-id", "", "Refresh all traces in a session and rebuild session analysis")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Refresh all client-visible traces; combine with --limit to cap selection")
	cmd.Flags().StringVarP(&opts.query, "query", "q", "", "Free-text trace filter")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "Provider filter")
	cmd.Flags().StringVar(&opts.model, "model", "", "Model substring filter")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "Endpoint substring filter")
	cmd.Flags().StringVar(&opts.upstream, "upstream", "", "Selected upstream substring filter")
	cmd.Flags().StringVar(&opts.status, "status", "", "Status filter: success or error")
	cmd.Flags().StringVar(&opts.observation, "observation", "", "Observation status filter: parsed, failed, queued, running, or unparsed")
	cmd.Flags().BoolVar(&opts.missingUsage, "missing-usage", false, "Only refresh successful traces with missing token usage")
	cmd.Flags().IntVar(&opts.limit, "limit", 1000, "Maximum traces selected by filters; 0 means no CLI cap")
	cmd.Flags().IntVar(&opts.workers, "workers", 0, "Concurrent trace workers for direct execution; 0 uses an automatic local default")
	cmd.Flags().BoolVar(&opts.repairUsage, "repair-usage", false, "Repair token usage before refreshing analysis")
	cmd.Flags().BoolVar(&opts.rewriteCassette, "rewrite-cassette", false, "Rewrite V3 cassette prelude when repairing usage")
	cmd.Flags().BoolVar(&opts.enqueue, "enqueue", false, "Create analysis jobs without executing them in this process")
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
	traceStore, err := openApplicationDatabase(cfg)
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
	traceStore, err := openApplicationDatabase(cfg)
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

func runAnalyzeBackfillExchanges(opts analyzeBackfillExchangesOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	traceStore, err := openApplicationDatabase(cfg)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()

	result, err := traceStore.BackfillExchangeMetadata(context.Background(), store.ExchangeMetadataBackfillOptions{
		DryRun: opts.dryRun,
	})
	if err != nil {
		slog.Error("Failed to backfill exchange metadata", "error", err)
		return 1
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze backfill-exchanges", result, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "backfilled exchange metadata: scanned=%d updated_model=%d updated_entry=%d legacy_or_unknown=%d missing_cassette=%d conflicts=%d dry_run=%t\n",
			result.Scanned, result.UpdatedModel, result.UpdatedEntry, result.LegacyOrUnknown, result.MissingCassette, result.Conflicts, result.DryRun)
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
	traceStore, err := openApplicationDatabase(cfg)
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

func runAnalyzeBatch(opts analyzeBatchOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	traceStore, err := openApplicationDatabase(cfg)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()

	traceIDs, err := resolveAnalyzeBatchTraceIDs(traceStore, opts)
	if err != nil {
		slog.Error("Failed to resolve batch traces", "error", err)
		return 1
	}
	svc := reanalysis.New(traceStore, reanalysis.Options{})
	results, completed, enqueued, failed := runAnalyzeBatchTraces(context.Background(), svc, traceIDs, opts, normalizeAnalyzeWorkers(opts.workers, cfg.DatabaseMaxOpenConns()))
	output := map[string]any{
		"trace_count":  len(traceIDs),
		"completed":    completed,
		"enqueued":     enqueued,
		"failed":       failed,
		"repair_usage": opts.repairUsage,
		"reparse":      opts.reparse,
		"scan":         opts.scan,
		"results":      results,
	}
	writeErr := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze batch", output, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "processed %d traces (completed=%d queued=%d failed=%d)\n", len(traceIDs), completed, enqueued, failed)
		return err
	})
	if writeErr != nil {
		slog.Error("Write command result failed", "error", writeErr)
		return 1
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func runAnalyzeBatchTraces(ctx context.Context, svc *reanalysis.Service, traceIDs []string, opts analyzeBatchOptions, workers int) ([]map[string]any, int, int, int) {
	if workers <= 1 || len(traceIDs) <= 1 {
		return runAnalyzeBatchTracesSerial(ctx, svc, traceIDs, opts)
	}
	if workers > len(traceIDs) {
		workers = len(traceIDs)
	}
	type item struct {
		index   int
		traceID string
	}
	jobs := make(chan item)
	results := make([]map[string]any, len(traceIDs))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results[job.index] = runAnalyzeBatchTraceRow(ctx, svc, job.traceID, opts)
			}
		}()
	}
	for index, traceID := range traceIDs {
		jobs <- item{index: index, traceID: traceID}
	}
	close(jobs)
	wg.Wait()
	completed, enqueued, failed := countAnalyzeBatchResults(results, opts.enqueue)
	return results, completed, enqueued, failed
}

func runAnalyzeBatchTracesSerial(ctx context.Context, svc *reanalysis.Service, traceIDs []string, opts analyzeBatchOptions) ([]map[string]any, int, int, int) {
	results := make([]map[string]any, 0, len(traceIDs))
	for _, traceID := range traceIDs {
		results = append(results, runAnalyzeBatchTraceRow(ctx, svc, traceID, opts))
	}
	completed, enqueued, failed := countAnalyzeBatchResults(results, opts.enqueue)
	return results, completed, enqueued, failed
}

func runAnalyzeBatchTraceRow(ctx context.Context, svc *reanalysis.Service, traceID string, opts analyzeBatchOptions) map[string]any {
	row := map[string]any{"trace_id": traceID}
	jobIDs, traceErr := runAnalyzeBatchTrace(ctx, svc, traceID, opts)
	row["job_ids"] = jobIDs
	if traceErr != nil {
		row["status"] = "failed"
		row["error"] = traceErr.Error()
		slog.Error("Failed to process batch trace", "trace_id", traceID, "error", traceErr)
	} else if opts.enqueue {
		row["status"] = "queued"
	} else {
		row["status"] = "completed"
	}
	return row
}

func countAnalyzeBatchResults(results []map[string]any, enqueuedStatus bool) (int, int, int) {
	completed := 0
	enqueued := 0
	failed := 0
	for _, row := range results {
		switch row["status"] {
		case "failed":
			failed++
		case "queued":
			enqueued++
		case "completed":
			completed++
		default:
			if enqueuedStatus {
				enqueued++
			} else {
				completed++
			}
		}
	}
	return completed, enqueued, failed
}

func normalizeAnalyzeWorkers(requested int, maxOpenConns int) int {
	if requested > 0 {
		return requested
	}
	workers := runtime.NumCPU()
	if maxOpenConns > 0 && workers > maxOpenConns {
		workers = maxOpenConns
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

func runAnalyzeRefresh(opts analyzeBatchOptions) int {
	if strings.TrimSpace(opts.sessionID) == "" {
		return runAnalyzeBatch(opts)
	}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	traceStore, err := openApplicationDatabase(cfg)
	if err != nil {
		slog.Error("Failed to initialize trace store", "error", err)
		return 1
	}
	defer traceStore.Close()
	svc := reanalysis.New(traceStore, reanalysis.Options{})
	if opts.enqueue {
		job, err := svc.EnqueueSessionReanalyze(opts.sessionID, reanalysis.SessionOptions{Reparse: true, Scan: true})
		if err != nil {
			slog.Error("Failed to enqueue session refresh", "session_id", opts.sessionID, "error", err)
			return 1
		}
		output := map[string]any{
			"session_id": opts.sessionID,
			"job_id":     job.ID,
			"status":     job.Status,
			"enqueued":   1,
		}
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze refresh", output, func(w io.Writer) error {
			_, err := fmt.Fprintf(w, "queued session refresh %s with job %d\n", opts.sessionID, job.ID)
			return err
		}); err != nil {
			slog.Error("Write command result failed", "error", err)
			return 1
		}
		return 0
	}
	if opts.repairUsage {
		traceIDs, err := resolveAnalyzeBatchTraceIDs(traceStore, opts)
		if err != nil {
			slog.Error("Failed to resolve session traces for usage repair", "session_id", opts.sessionID, "error", err)
			return 1
		}
		for _, traceID := range traceIDs {
			if _, err := svc.RepairTraceUsage(context.Background(), traceID, reanalysis.RepairUsageOptions{RewriteCassette: opts.rewriteCassette}); err != nil {
				slog.Error("Failed to repair usage before session refresh", "trace_id", traceID, "error", err)
				return 1
			}
		}
	}
	result, err := svc.ReanalyzeSession(context.Background(), opts.sessionID, reanalysis.SessionOptions{Reparse: true, Scan: true})
	if err != nil {
		slog.Error("Failed to refresh session", "session_id", opts.sessionID, "error", err)
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
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "analyze refresh", output, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "refreshed session %s (%d traces)\n", opts.sessionID, result.Session.TraceCount)
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
	traceStore, err := openApplicationDatabase(cfg)
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

func analyzeBatchHasFilter(opts analyzeBatchOptions) bool {
	return strings.TrimSpace(opts.query) != "" ||
		strings.TrimSpace(opts.provider) != "" ||
		strings.TrimSpace(opts.model) != "" ||
		strings.TrimSpace(opts.endpoint) != "" ||
		strings.TrimSpace(opts.upstream) != "" ||
		strings.TrimSpace(opts.status) != "" ||
		strings.TrimSpace(opts.observation) != "" ||
		opts.missingUsage
}

func resolveAnalyzeBatchTraceIDs(st *store.Store, opts analyzeBatchOptions) ([]string, error) {
	seen := map[string]struct{}{}
	add := func(traceID string, out *[]string) {
		traceID = strings.TrimSpace(traceID)
		if traceID == "" {
			return
		}
		if _, ok := seen[traceID]; ok {
			return
		}
		seen[traceID] = struct{}{}
		*out = append(*out, traceID)
	}
	var traceIDs []string
	for _, traceID := range opts.traceIDs {
		add(traceID, &traceIDs)
	}
	for _, requestID := range opts.requestIDs {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			continue
		}
		entry, err := st.GetByRequestID(requestID)
		if err != nil {
			return nil, fmt.Errorf("resolve request id %q: %w", requestID, err)
		}
		add(entry.ID, &traceIDs)
	}
	if sessionID := strings.TrimSpace(opts.sessionID); sessionID != "" {
		entries, err := st.ListTracesBySession(sessionID)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			add(entry.ID, &traceIDs)
		}
		return limitTraceIDs(traceIDs, opts.limit), nil
	}
	if len(traceIDs) > 0 {
		return limitTraceIDs(traceIDs, opts.limit), nil
	}
	limit := analyzeSelectionLimit(opts)
	return st.ListTraceIDs(store.ListFilter{
		Query:             strings.TrimSpace(opts.query),
		Provider:          strings.TrimSpace(opts.provider),
		Model:             strings.TrimSpace(opts.model),
		Endpoint:          strings.TrimSpace(opts.endpoint),
		SelectedUpstream:  strings.TrimSpace(opts.upstream),
		Status:            strings.TrimSpace(opts.status),
		ObservationStatus: strings.TrimSpace(opts.observation),
		MissingUsage:      opts.missingUsage,
	}, limit)
}

func analyzeSelectionLimit(opts analyzeBatchOptions) int {
	if opts.all && !opts.limitSet {
		return 0
	}
	return opts.limit
}

func limitTraceIDs(traceIDs []string, limit int) []string {
	if limit <= 0 || len(traceIDs) <= limit {
		return traceIDs
	}
	return traceIDs[:limit]
}

func runAnalyzeBatchTrace(ctx context.Context, svc *reanalysis.Service, traceID string, opts analyzeBatchOptions) ([]int64, error) {
	var jobIDs []int64
	if opts.enqueue {
		if opts.repairUsage {
			job, err := svc.EnqueueTraceRepairUsage(traceID)
			if err != nil {
				return jobIDs, err
			}
			jobIDs = append(jobIDs, job.ID)
		}
		switch {
		case opts.reparse && opts.scan:
			job, err := svc.EnqueueTraceReanalyze(traceID)
			if err != nil {
				return jobIDs, err
			}
			jobIDs = append(jobIDs, job.ID)
		case opts.reparse:
			job, err := svc.EnqueueTraceReparse(traceID, reanalysis.TraceOptions{})
			if err != nil {
				return jobIDs, err
			}
			jobIDs = append(jobIDs, job.ID)
		case opts.scan:
			job, err := svc.EnqueueTraceRescan(traceID)
			if err != nil {
				return jobIDs, err
			}
			jobIDs = append(jobIDs, job.ID)
		}
		return jobIDs, nil
	}
	if opts.repairUsage {
		result, err := svc.RepairTraceUsage(ctx, traceID, reanalysis.RepairUsageOptions{RewriteCassette: opts.rewriteCassette})
		if err != nil {
			return jobIDs, err
		}
		jobIDs = append(jobIDs, result.Job.ID)
	}
	switch {
	case opts.reparse && opts.scan:
		result, err := svc.ReanalyzeTrace(ctx, traceID)
		if err != nil {
			return jobIDs, err
		}
		jobIDs = append(jobIDs, result.Job.ID)
	case opts.reparse:
		result, err := svc.ReparseTrace(ctx, traceID, reanalysis.TraceOptions{})
		if err != nil {
			return jobIDs, err
		}
		jobIDs = append(jobIDs, result.Job.ID)
	case opts.scan:
		result, err := svc.RescanTrace(ctx, traceID)
		if err != nil {
			return jobIDs, err
		}
		jobIDs = append(jobIDs, result.Job.ID)
	}
	return jobIDs, nil
}

func runAnalyzeScan(opts analyzeScanOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	traceStore, err := openApplicationDatabase(cfg)
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
