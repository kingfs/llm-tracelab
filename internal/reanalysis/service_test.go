package reanalysis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestServiceReanalyzeTraceRebuildsObservationFindingsAndJob(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	traceID := writeIndexedResponseTrace(t, st, dir)
	result, err := New(st, Options{}).ReanalyzeTrace(context.Background(), traceID)
	if err != nil {
		t.Fatalf("ReanalyzeTrace() error = %v", err)
	}
	if result.Job.Status != "completed" || result.Job.JobType != JobTypeTraceReanalyze {
		t.Fatalf("job = %+v, want completed trace_reanalyze", result.Job)
	}
	if result.Observation == nil || result.Observation.Parser != "openai" || result.RequestNodes == 0 || result.ResponseNodes == 0 {
		t.Fatalf("observation result = %+v request=%d response=%d", result.Observation, result.RequestNodes, result.ResponseNodes)
	}
	if result.Findings == nil || result.Findings.Count != 1 || result.HighFindings != 1 {
		t.Fatalf("findings result = %+v high=%d", result.Findings, result.HighFindings)
	}

	summary, err := st.GetObservationSummary(traceID)
	if err != nil {
		t.Fatalf("GetObservationSummary() error = %v", err)
	}
	if summary.Status != "parsed" {
		t.Fatalf("summary status = %q, want parsed", summary.Status)
	}
	findings, err := st.ListFindings(traceID, store.FindingFilter{Category: "credential_leak"})
	if err != nil {
		t.Fatalf("ListFindings() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one credential finding", findings)
	}
}

func TestServiceRescanTraceRequiresObservation(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	traceID := writeIndexedResponseTrace(t, st, dir)
	if _, err := New(st, Options{}).RescanTrace(context.Background(), traceID); err == nil {
		t.Fatalf("RescanTrace() error = nil, want missing observation error")
	}
	jobs, err := st.ListAnalysisJobs("failed", "trace", traceID, 10)
	if err != nil {
		t.Fatalf("ListAnalysisJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].JobType != JobTypeTraceRescan {
		t.Fatalf("jobs = %+v, want failed rescan job", jobs)
	}
}

func TestServiceRepairTraceUsageUpdatesIndex(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	traceID := writeIndexedResponseTrace(t, st, dir)
	result, err := New(st, Options{}).RepairTraceUsage(context.Background(), traceID, RepairUsageOptions{})
	if err != nil {
		t.Fatalf("RepairTraceUsage() error = %v", err)
	}
	if result.Usage == nil || !result.Usage.Changed || !result.Usage.IndexUpdated || result.Usage.CassetteRewrote {
		t.Fatalf("usage result = %+v", result.Usage)
	}
	if result.Usage.After.PromptTokens != 1 || result.Usage.After.CompletionTokens != 1 || result.Usage.After.TotalTokens != 2 {
		t.Fatalf("usage after = %+v", result.Usage.After)
	}
	entry, err := st.GetByID(traceID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if entry.Header.Usage.TotalTokens != 2 {
		t.Fatalf("indexed usage = %+v, want total 2", entry.Header.Usage)
	}
}

func TestServiceRepairTraceUsageCanRewriteV3Prelude(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	traceID := writeIndexedResponseTrace(t, st, dir)
	entry, err := st.GetByID(traceID)
	if err != nil {
		t.Fatalf("GetByID(before) error = %v", err)
	}
	beforeContent, err := os.ReadFile(entry.LogPath)
	if err != nil {
		t.Fatalf("ReadFile(before) error = %v", err)
	}
	beforeParsed, err := recordfile.ParsePrelude(beforeContent)
	if err != nil {
		t.Fatalf("ParsePrelude(before) error = %v", err)
	}
	beforePayload := string(beforeContent[beforeParsed.PayloadOffset:])

	result, err := New(st, Options{}).RepairTraceUsage(context.Background(), traceID, RepairUsageOptions{RewriteCassette: true})
	if err != nil {
		t.Fatalf("RepairTraceUsage(rewrite) error = %v", err)
	}
	if result.Usage == nil || !result.Usage.CassetteRewrote {
		t.Fatalf("usage result = %+v", result.Usage)
	}
	afterContent, err := os.ReadFile(entry.LogPath)
	if err != nil {
		t.Fatalf("ReadFile(after) error = %v", err)
	}
	afterParsed, err := recordfile.ParsePrelude(afterContent)
	if err != nil {
		t.Fatalf("ParsePrelude(after) error = %v", err)
	}
	if afterParsed.Header.Usage.TotalTokens != 2 {
		t.Fatalf("rewritten header usage = %+v, want total 2", afterParsed.Header.Usage)
	}
	if got := string(afterContent[afterParsed.PayloadOffset:]); got != beforePayload {
		t.Fatalf("payload changed after rewrite")
	}
	if !strings.Contains(string(afterContent[:afterParsed.PayloadOffset]), `"total_tokens":2`) {
		t.Fatalf("rewritten prelude does not contain repaired total tokens")
	}
}

func writeIndexedResponseTrace(t *testing.T, st *store.Store, dir string) string {
	t.Helper()
	reqHead := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n"
	reqBody := `{"model":"gpt-5.1","input":"hello with sk-test_abcdefghijklmnopqrstuvwxyz"}`
	resHead := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	resBody := `{"id":"resp_1","object":"response","created_at":1741476777,"status":"completed","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-reanalysis",
			Time:          time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1",
			Provider:      "openai_compatible",
			Operation:     "responses",
			Endpoint:      "/v1/responses",
			URL:           "/v1/responses",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    20,
			TTFTMs:        5,
			ClientIP:      "127.0.0.1",
			ContentLength: int64(len(reqBody)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHead)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHead)),
			ResBodyLen:   int64(len(resBody)),
		},
	}
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	logPath := filepath.Join(dir, "trace-reanalysis.http")
	if err := os.WriteFile(logPath, []byte(string(prelude)+reqHead+reqBody+"\n"+resHead+resBody), 0o644); err != nil {
		t.Fatalf("WriteFile(trace) error = %v", err)
	}
	if err := st.UpsertLog(logPath, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}
	entry, err := st.GetByRequestID("req-reanalysis")
	if err != nil {
		t.Fatalf("GetByRequestID() error = %v", err)
	}
	return entry.ID
}
