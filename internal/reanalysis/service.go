package reanalysis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kingfs/llm-tracelab/internal/analyzer"
	"github.com/kingfs/llm-tracelab/internal/observeworker"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
)

const (
	JobTypeTraceReparse   = "trace_reparse"
	JobTypeTraceRescan    = "trace_rescan"
	JobTypeTraceReanalyze = "trace_reanalyze"

	TargetTypeTrace = "trace"

	StepReparseObservation = "reparse_observation"
	StepScanFindings       = "scan_findings"
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

type Result struct {
	Job              store.AnalysisJobRecord `json:"job"`
	Observation      *ObservationResult      `json:"observation,omitempty"`
	Findings         *FindingsResult         `json:"findings,omitempty"`
	RequestNodes     int                     `json:"request_nodes,omitempty"`
	ResponseNodes    int                     `json:"response_nodes,omitempty"`
	StreamEvents     int                     `json:"stream_events,omitempty"`
	FindingCount     int                     `json:"finding_count,omitempty"`
	CriticalFindings int                     `json:"critical_findings,omitempty"`
	HighFindings     int                     `json:"high_findings,omitempty"`
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
	return out
}
