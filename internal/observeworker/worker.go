package observeworker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	exchange := parsed.Header.Meta
	if indexed, err := st.GetTraceExchangeMetadata(entry.ID); err == nil {
		mergeExchangeMetadata(&exchange, indexed)
	}
	applyExchangeFallbacks(&exchange, entry.LogPath)
	return registry.Parse(ctx, observe.ParseInput{
		TraceID:          entry.ID,
		CassettePath:     entry.LogPath,
		Header:           parsed.Header,
		Events:           parsed.Events,
		RequestBody:      reqBody,
		ResponseBody:     resBody,
		IsStream:         parsed.Header.Layout.IsStream,
		ExchangeKind:     exchange.ExchangeKind,
		ExchangeRole:     exchange.ExchangeRole,
		ParentExchangeID: exchange.ParentExchangeID,
		SequenceIndex:    exchange.SequenceIndex,
		RequestAuditID:   exchange.RequestAuditID,
		ResponseID:       exchange.ResponseID,
	})
}

func mergeExchangeMetadata(dst *recordfile.MetaData, src recordfile.MetaData) {
	if dst.ExchangeKind == "" {
		dst.ExchangeKind = src.ExchangeKind
	}
	if dst.ExchangeRole == "" {
		dst.ExchangeRole = src.ExchangeRole
	}
	if dst.ParentExchangeID == "" {
		dst.ParentExchangeID = src.ParentExchangeID
	}
	if dst.SequenceIndex == 0 {
		dst.SequenceIndex = src.SequenceIndex
	}
	if dst.RequestAuditID == "" {
		dst.RequestAuditID = src.RequestAuditID
	}
	if dst.ResponseID == "" {
		dst.ResponseID = src.ResponseID
	}
}

func applyExchangeFallbacks(meta *recordfile.MetaData, cassettePath string) {
	if meta.ExchangeKind == "" && isEntryExchangeHint(*meta, cassettePath) {
		meta.ExchangeKind = "entry"
	}
	if meta.ExchangeKind == "" && isModelExchangeHint(*meta) {
		meta.ExchangeKind = "model"
	}
	if meta.ExchangeRole == "" {
		switch meta.ExchangeKind {
		case "entry":
			meta.ExchangeRole = "client_request"
		case "model":
			meta.ExchangeRole = "primary_model_call"
		}
	}
}

func isEntryExchangeHint(meta recordfile.MetaData, cassettePath string) bool {
	if meta.ExchangeRole == "client_request" {
		return true
	}
	lowerPath := strings.ToLower(filepath.ToSlash(cassettePath))
	if strings.Contains(lowerPath, "/entry/") || strings.Contains(lowerPath, "_entry") || strings.Contains(lowerPath, "-entry") {
		return true
	}
	return false
}

func isModelExchangeHint(meta recordfile.MetaData) bool {
	return meta.Model != "" || meta.Provider != "" || meta.SelectedUpstreamID != "" || meta.SelectedUpstreamBaseURL != ""
}
