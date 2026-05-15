package reanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/kingfs/llm-tracelab/internal/analyzer"
	"github.com/kingfs/llm-tracelab/internal/observeworker"
	"github.com/kingfs/llm-tracelab/internal/sessionanalysis"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

const (
	JobTypeTraceReparse     = "trace_reparse"
	JobTypeTraceRescan      = "trace_rescan"
	JobTypeTraceRepair      = "trace_repair_usage"
	JobTypeTraceReanalyze   = "trace_reanalyze"
	JobTypeSessionReanalyze = "session_reanalyze"

	TargetTypeTrace   = "trace"
	TargetTypeSession = "session"

	StepReparseObservation = "reparse_observation"
	StepScanFindings       = "scan_findings"
	StepRepairUsage        = "repair_usage"
	StepSessionAnalysis    = "session_analysis"
)

type Service struct {
	store    *store.Store
	registry *observe.Registry
	runner   *analyzer.Runner
	now      func() time.Time
}

type Options struct {
	Registry *observe.Registry
	Runner   *analyzer.Runner
	Now      func() time.Time
}

type TraceOptions struct {
	Scan bool
}

type RepairUsageOptions struct {
	RewriteCassette bool
}

type SessionOptions struct {
	Reparse bool
	Scan    bool
}

type Result struct {
	Job              store.AnalysisJobRecord `json:"job"`
	Usage            *UsageRepairResult      `json:"usage,omitempty"`
	Observation      *ObservationResult      `json:"observation,omitempty"`
	Findings         *FindingsResult         `json:"findings,omitempty"`
	Session          *SessionResult          `json:"session,omitempty"`
	RequestNodes     int                     `json:"request_nodes,omitempty"`
	ResponseNodes    int                     `json:"response_nodes,omitempty"`
	StreamEvents     int                     `json:"stream_events,omitempty"`
	FindingCount     int                     `json:"finding_count,omitempty"`
	CriticalFindings int                     `json:"critical_findings,omitempty"`
	HighFindings     int                     `json:"high_findings,omitempty"`
}

type SessionResult struct {
	SessionID     string `json:"session_id"`
	TraceCount    int    `json:"trace_count"`
	AnalysisRunID int64  `json:"analysis_run_id"`
	FindingRefs   int    `json:"finding_refs"`
}

type UsageRepairResult struct {
	TraceID         string               `json:"trace_id"`
	Before          recordfile.UsageInfo `json:"before"`
	After           recordfile.UsageInfo `json:"after"`
	Changed         bool                 `json:"changed"`
	IndexUpdated    bool                 `json:"index_updated"`
	CassetteRewrote bool                 `json:"cassette_rewrote"`
}

type ObservationResult struct {
	TraceID       string `json:"trace_id"`
	Parser        string `json:"parser"`
	ParserVersion string `json:"parser_version"`
	Status        string `json:"status"`
}

type FindingsResult struct {
	TraceID  string         `json:"trace_id"`
	Count    int            `json:"count"`
	Severity map[string]int `json:"severity"`
}

func New(st *store.Store, opts Options) *Service {
	registry := opts.Registry
	if registry == nil {
		registry = observe.NewDefaultRegistry()
	}
	runner := opts.Runner
	if runner == nil {
		runner = analyzer.NewRunner()
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: st, registry: registry, runner: runner, now: now}
}

func (s *Service) ReparseTrace(ctx context.Context, traceID string, opts TraceOptions) (Result, error) {
	steps := []string{StepReparseObservation}
	if opts.Scan {
		steps = append(steps, StepScanFindings)
	}
	job, err := s.createTraceJob(JobTypeTraceReparse, traceID, steps)
	if err != nil {
		return Result{}, err
	}
	result, err := s.runTraceJob(ctx, job, opts)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) RescanTrace(ctx context.Context, traceID string) (Result, error) {
	job, err := s.createTraceJob(JobTypeTraceRescan, traceID, []string{StepScanFindings})
	if err != nil {
		return Result{}, err
	}
	result, err := s.runTraceJob(ctx, job, TraceOptions{Scan: true})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) RepairTraceUsage(ctx context.Context, traceID string, opts RepairUsageOptions) (Result, error) {
	job, err := s.createTraceJob(JobTypeTraceRepair, traceID, []string{StepRepairUsage})
	if err != nil {
		return Result{}, err
	}
	result, err := s.runRepairUsageJob(ctx, job, opts)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) ReanalyzeTrace(ctx context.Context, traceID string) (Result, error) {
	job, err := s.createTraceJob(JobTypeTraceReanalyze, traceID, []string{StepReparseObservation, StepScanFindings})
	if err != nil {
		return Result{}, err
	}
	result, err := s.runTraceJob(ctx, job, TraceOptions{Scan: true})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) EnqueueTraceReparse(traceID string, opts TraceOptions) (store.AnalysisJobRecord, error) {
	steps := []string{StepReparseObservation}
	if opts.Scan {
		steps = append(steps, StepScanFindings)
	}
	return s.createTraceJob(JobTypeTraceReparse, traceID, steps)
}

func (s *Service) EnqueueTraceRescan(traceID string) (store.AnalysisJobRecord, error) {
	return s.createTraceJob(JobTypeTraceRescan, traceID, []string{StepScanFindings})
}

func (s *Service) EnqueueTraceRepairUsage(traceID string) (store.AnalysisJobRecord, error) {
	return s.createTraceJob(JobTypeTraceRepair, traceID, []string{StepRepairUsage})
}

func (s *Service) EnqueueTraceReanalyze(traceID string) (store.AnalysisJobRecord, error) {
	return s.createTraceJob(JobTypeTraceReanalyze, traceID, []string{StepReparseObservation, StepScanFindings})
}

func (s *Service) ReanalyzeSession(ctx context.Context, sessionID string, opts SessionOptions) (Result, error) {
	job, err := s.createSessionJob(sessionID, opts)
	if err != nil {
		return Result{}, err
	}
	return s.runSessionJob(ctx, job, opts)
}

func (s *Service) EnqueueSessionReanalyze(sessionID string, opts SessionOptions) (store.AnalysisJobRecord, error) {
	return s.createSessionJob(sessionID, opts)
}

func (s *Service) ExecuteJob(ctx context.Context, job store.AnalysisJobRecord) (Result, error) {
	switch job.JobType {
	case JobTypeTraceReparse:
		return s.runTraceJob(ctx, job, TraceOptions{Scan: stepsContain(job.StepsJSON, StepScanFindings)})
	case JobTypeTraceRescan:
		return s.runTraceJob(ctx, job, TraceOptions{Scan: true})
	case JobTypeTraceRepair:
		return s.runRepairUsageJob(ctx, job, RepairUsageOptions{})
	case JobTypeTraceReanalyze:
		return s.runTraceJob(ctx, job, TraceOptions{Scan: true})
	case JobTypeSessionReanalyze:
		return s.runSessionJob(ctx, job, SessionOptions{
			Reparse: stepsContain(job.StepsJSON, StepReparseObservation),
			Scan:    stepsContain(job.StepsJSON, StepScanFindings),
		})
	default:
		return Result{}, fmt.Errorf("unsupported analysis job type %q", job.JobType)
	}
}

func (s *Service) runRepairUsageJob(ctx context.Context, job store.AnalysisJobRecord, opts RepairUsageOptions) (Result, error) {
	if err := s.store.MarkAnalysisJobRunning(job.ID); err != nil {
		return Result{}, err
	}
	var err error
	defer func() {
		if err != nil {
			_ = s.store.MarkAnalysisJobFailed(job.ID, err.Error())
		}
	}()

	select {
	case <-ctx.Done():
		err = ctx.Err()
		return Result{}, err
	default:
	}

	repair, err := s.repairUsage(job.TargetID, opts)
	if err != nil {
		return Result{}, err
	}
	result := Result{Job: job, Usage: &repair}
	resultJSON, marshalErr := json.Marshal(resultSummary(result))
	if marshalErr != nil {
		err = marshalErr
		return Result{}, err
	}
	if err = s.store.MarkAnalysisJobCompleted(job.ID, string(resultJSON)); err != nil {
		return Result{}, err
	}
	result.Job, err = s.store.GetAnalysisJob(job.ID)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) repairUsage(traceID string, opts RepairUsageOptions) (UsageRepairResult, error) {
	entry, err := s.store.GetByID(traceID)
	if err != nil {
		return UsageRepairResult{}, err
	}
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		return UsageRepairResult{}, err
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return UsageRepairResult{}, err
	}
	_, _, _, resBody := recordfile.ExtractSections(content, parsed)
	pipeline := llm.NewResponsePipeline(parsed.Header.Meta.Provider, parsed.Header.Meta.Endpoint, parsed.Header.Layout.IsStream)
	pipeline.Feed(resBody)
	pipeline.Finalize()
	usage, ok := pipeline.Usage()
	if !ok {
		return UsageRepairResult{}, fmt.Errorf("usage not found in recorded response")
	}
	after := recordfile.UsageInfo(usage)
	before := parsed.Header.Usage
	changed := !usageEqual(before, after)
	if err := s.store.UpdateLogUsage(traceID, after); err != nil {
		return UsageRepairResult{}, err
	}
	repair := UsageRepairResult{
		TraceID:         traceID,
		Before:          before,
		After:           after,
		Changed:         changed,
		IndexUpdated:    true,
		CassetteRewrote: false,
	}
	if opts.RewriteCassette {
		if parsed.Header.Version != "LLM_PROXY_V3" {
			return UsageRepairResult{}, fmt.Errorf("cassette usage rewrite requires LLM_PROXY_V3, got %q", parsed.Header.Version)
		}
		header := parsed.Header
		header.Usage = after
		prelude, err := recordfile.MarshalPrelude(header, parsed.Events)
		if err != nil {
			return UsageRepairResult{}, err
		}
		payload := content[parsed.PayloadOffset:]
		updated := make([]byte, 0, len(prelude)+len(payload))
		updated = append(updated, prelude...)
		updated = append(updated, payload...)
		if err := os.WriteFile(entry.LogPath, updated, 0o644); err != nil {
			return UsageRepairResult{}, err
		}
		repair.CassetteRewrote = true
		if !bytes.Equal(content, updated) {
			repair.Changed = true
		}
	}
	return repair, nil
}

func (s *Service) createTraceJob(jobType string, traceID string, steps []string) (store.AnalysisJobRecord, error) {
	if s == nil || s.store == nil {
		return store.AnalysisJobRecord{}, fmt.Errorf("reanalysis store is nil")
	}
	if traceID == "" {
		return store.AnalysisJobRecord{}, fmt.Errorf("trace id is required")
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return store.AnalysisJobRecord{}, err
	}
	now := s.now()
	return s.store.CreateAnalysisJob(store.AnalysisJobRecord{
		JobType:    jobType,
		TargetType: TargetTypeTrace,
		TargetID:   traceID,
		Status:     "queued",
		StepsJSON:  string(stepsJSON),
		ResultJSON: "{}",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

func (s *Service) createSessionJob(sessionID string, opts SessionOptions) (store.AnalysisJobRecord, error) {
	if s == nil || s.store == nil {
		return store.AnalysisJobRecord{}, fmt.Errorf("reanalysis store is nil")
	}
	if sessionID == "" {
		return store.AnalysisJobRecord{}, fmt.Errorf("session id is required")
	}
	steps := []string{}
	if opts.Reparse {
		steps = append(steps, StepReparseObservation)
	}
	if opts.Scan {
		steps = append(steps, StepScanFindings)
	}
	steps = append(steps, StepSessionAnalysis)
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return store.AnalysisJobRecord{}, err
	}
	now := s.now()
	return s.store.CreateAnalysisJob(store.AnalysisJobRecord{
		JobType:    JobTypeSessionReanalyze,
		TargetType: TargetTypeSession,
		TargetID:   sessionID,
		Status:     "queued",
		StepsJSON:  string(stepsJSON),
		ResultJSON: "{}",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
}

func (s *Service) runTraceJob(ctx context.Context, job store.AnalysisJobRecord, opts TraceOptions) (Result, error) {
	if err := s.store.MarkAnalysisJobRunning(job.ID); err != nil {
		return Result{}, err
	}
	result := Result{Job: job}
	var err error
	defer func() {
		if err != nil {
			_ = s.store.MarkAnalysisJobFailed(job.ID, err.Error())
		}
	}()

	var obs observe.TraceObservation
	if job.JobType == JobTypeTraceRescan {
		obs, err = s.store.GetObservation(job.TargetID)
	} else {
		obs, err = observeworker.ReparseTrace(ctx, s.store, s.registry, job.TargetID)
		if err == nil {
			err = s.store.SaveObservation(obs)
		}
	}
	if err != nil {
		return Result{}, err
	}

	if job.JobType != JobTypeTraceRescan {
		result.Observation = &ObservationResult{
			TraceID:       obs.TraceID,
			Parser:        obs.Parser,
			ParserVersion: obs.ParserVersion,
			Status:        string(obs.Status),
		}
		result.RequestNodes = len(obs.Request.Nodes)
		result.ResponseNodes = len(obs.Response.Nodes)
		result.StreamEvents = len(obs.Stream.Events)
	}

	if opts.Scan {
		findings, scanErr := s.runner.Analyze(ctx, obs)
		if scanErr != nil {
			err = scanErr
			return Result{}, err
		}
		if saveErr := s.store.SaveFindings(job.TargetID, findings); saveErr != nil {
			err = saveErr
			return Result{}, err
		}
		findingsResult := findingsResult(job.TargetID, findings)
		result.Findings = &findingsResult
		result.FindingCount = findingsResult.Count
		result.CriticalFindings = findingsResult.Severity[string(observe.SeverityCritical)]
		result.HighFindings = findingsResult.Severity[string(observe.SeverityHigh)]
	}

	resultJSON, marshalErr := json.Marshal(resultSummary(result))
	if marshalErr != nil {
		err = marshalErr
		return Result{}, err
	}
	if err = s.store.MarkAnalysisJobCompleted(job.ID, string(resultJSON)); err != nil {
		return Result{}, err
	}
	result.Job, err = s.store.GetAnalysisJob(job.ID)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) runSessionJob(ctx context.Context, job store.AnalysisJobRecord, opts SessionOptions) (Result, error) {
	if err := s.store.MarkAnalysisJobRunning(job.ID); err != nil {
		return Result{}, err
	}
	result := Result{Job: job}
	var err error
	defer func() {
		if err != nil {
			_ = s.store.MarkAnalysisJobFailed(job.ID, err.Error())
		}
	}()

	summary, err := s.store.GetSession(job.TargetID)
	if err != nil {
		return Result{}, err
	}
	traces, err := s.store.ListTracesBySession(job.TargetID)
	if err != nil {
		return Result{}, err
	}
	if opts.Reparse || opts.Scan {
		for _, trace := range traces {
			select {
			case <-ctx.Done():
				err = ctx.Err()
				return Result{}, err
			default:
			}
			if opts.Reparse && opts.Scan {
				if _, err = s.ReanalyzeTrace(ctx, trace.ID); err != nil {
					return Result{}, err
				}
				continue
			}
			if opts.Reparse {
				if _, err = s.ReparseTrace(ctx, trace.ID, TraceOptions{}); err != nil {
					return Result{}, err
				}
				continue
			}
			if opts.Scan {
				if _, err = s.RescanTrace(ctx, trace.ID); err != nil {
					return Result{}, err
				}
			}
		}
		traces, err = s.store.ListTracesBySession(job.TargetID)
		if err != nil {
			return Result{}, err
		}
	}
	findingsByTrace := map[string][]observe.Finding{}
	for _, trace := range traces {
		findings, findErr := s.store.ListFindings(trace.ID, store.FindingFilter{})
		if findErr != nil {
			err = findErr
			return Result{}, err
		}
		findingsByTrace[trace.ID] = findings
	}
	output := sessionanalysis.Build(summary, traces, findingsByTrace)
	outputJSON, err := sessionanalysis.Marshal(output)
	if err != nil {
		return Result{}, err
	}
	runID, err := s.store.SaveAnalysisRun(store.AnalysisRunRecord{
		SessionID:       job.TargetID,
		Kind:            sessionanalysis.Kind,
		Analyzer:        sessionanalysis.AnalyzerName,
		AnalyzerVersion: sessionanalysis.AnalyzerVersion,
		InputRef:        "session:" + job.TargetID,
		OutputJSON:      outputJSON,
		Status:          "completed",
	})
	if err != nil {
		return Result{}, err
	}
	result.Session = &SessionResult{
		SessionID:     job.TargetID,
		TraceCount:    len(output.TraceRefs),
		AnalysisRunID: runID,
		FindingRefs:   len(output.FindingRefs),
	}
	resultJSON, marshalErr := json.Marshal(resultSummary(result))
	if marshalErr != nil {
		err = marshalErr
		return Result{}, err
	}
	if err = s.store.MarkAnalysisJobCompleted(job.ID, string(resultJSON)); err != nil {
		return Result{}, err
	}
	result.Job, err = s.store.GetAnalysisJob(job.ID)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func findingsResult(traceID string, findings []observe.Finding) FindingsResult {
	out := FindingsResult{
		TraceID:  traceID,
		Count:    len(findings),
		Severity: map[string]int{},
	}
	for _, finding := range findings {
		out.Severity[string(finding.Severity)]++
	}
	return out
}

func resultSummary(result Result) map[string]any {
	out := map[string]any{}
	if result.Observation != nil {
		out["observation"] = result.Observation
		out["request_nodes"] = result.RequestNodes
		out["response_nodes"] = result.ResponseNodes
		out["stream_events"] = result.StreamEvents
	}
	if result.Findings != nil {
		out["findings"] = result.Findings
	}
	if result.Usage != nil {
		out["usage"] = result.Usage
	}
	if result.Session != nil {
		out["session"] = result.Session
	}
	return out
}

func stepsContain(stepsJSON string, step string) bool {
	var steps []string
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return false
	}
	for _, current := range steps {
		if current == step {
			return true
		}
	}
	return false
}

func usageEqual(a recordfile.UsageInfo, b recordfile.UsageInfo) bool {
	aCached := 0
	if a.PromptTokenDetails != nil {
		aCached = a.PromptTokenDetails.CachedTokens
	}
	bCached := 0
	if b.PromptTokenDetails != nil {
		bCached = b.PromptTokenDetails.CachedTokens
	}
	return a.PromptTokens == b.PromptTokens &&
		a.CompletionTokens == b.CompletionTokens &&
		a.TotalTokens == b.TotalTokens &&
		aCached == bCached
}
