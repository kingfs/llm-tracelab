package observeworker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type Worker struct {
	store     *store.Store
	registry  *observe.Registry
	interval  time.Duration
	batchSize int
}

type Options struct {
	Interval  time.Duration
	BatchSize int
	Registry  *observe.Registry
}

func New(st *store.Store, opts Options) *Worker {
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 10
	}
	registry := opts.Registry
	if registry == nil {
		registry = observe.NewDefaultRegistry()
	}
	return &Worker{
		store:     st,
		registry:  registry,
		interval:  interval,
		batchSize: batchSize,
	}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}
	w.runOnce(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) {
	w.runOnce(ctx)
}

func (w *Worker) runOnce(ctx context.Context) {
	jobs, err := w.store.ListParseJobs("queued", w.batchSize)
	if err != nil {
		slog.Warn("List parse jobs failed", "error", err)
		return
	}
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := w.processJob(ctx, job); err != nil {
			slog.Warn("Parse job failed", "trace_id", job.TraceID, "job_id", job.ID, "error", err)
		}
	}
}

func (w *Worker) processJob(ctx context.Context, job store.ParseJobRecord) error {
	if err := w.store.MarkParseJobRunning(job.ID); err != nil {
		return err
	}
	obs, err := ReparseTrace(ctx, w.store, w.registry, job.TraceID)
	if err != nil {
		_ = w.store.MarkParseJobFailed(job.ID, err.Error())
		return err
	}
	if err := w.store.SaveObservation(obs); err != nil {
		_ = w.store.MarkParseJobFailed(job.ID, err.Error())
		return err
	}
	if err := w.store.MarkParseJobDone(job.ID); err != nil {
		return err
	}
	return nil
}

func ReparseTrace(ctx context.Context, st *store.Store, registry *observe.Registry, traceID string) (observe.TraceObservation, error) {
	if st == nil {
		return observe.TraceObservation{}, fmt.Errorf("trace store is nil")
	}
	if registry == nil {
		registry = observe.NewDefaultRegistry()
	}
	entry, err := st.GetByID(traceID)
	if err != nil {
		return observe.TraceObservation{}, err
	}
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		return observe.TraceObservation{}, err
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return observe.TraceObservation{}, err
	}
	_, reqBody, _, resBody := recordfile.ExtractSections(content, parsed)
	return registry.Parse(ctx, observe.ParseInput{
		TraceID:      entry.ID,
		CassettePath: entry.LogPath,
		Header:       parsed.Header,
		Events:       parsed.Events,
		RequestBody:  reqBody,
		ResponseBody: resBody,
		IsStream:     parsed.Header.Layout.IsStream,
	})
}
