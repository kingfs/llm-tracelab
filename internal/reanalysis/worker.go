package reanalysis

import (
	"context"
	"log/slog"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
)

type Worker struct {
	store     *store.Store
	service   *Service
	interval  time.Duration
	batchSize int
}

type WorkerOptions struct {
	Interval  time.Duration
	BatchSize int
	Service   *Service
}

func NewWorker(st *store.Store, opts WorkerOptions) *Worker {
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 5
	}
	service := opts.Service
	if service == nil {
		service = New(st, Options{})
	}
	return &Worker{
		store:     st,
		service:   service,
		interval:  interval,
		batchSize: batchSize,
	}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.store == nil || w.service == nil {
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
	jobs, err := w.store.ListAnalysisJobsForWorker(w.batchSize)
	if err != nil {
		slog.Warn("List analysis jobs failed", "error", err)
		return
	}
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := w.service.ExecuteJob(ctx, job); err != nil {
			slog.Warn("Analysis job failed", "job_id", job.ID, "job_type", job.JobType, "target_id", job.TargetID, "error", err)
		}
	}
}
