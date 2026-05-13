package observeworker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestWorkerRunOnceParsesQueuedJob(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	traceID := writeIndexedResponseTrace(t, st, dir)
	if err := st.EnqueueParseJob(traceID); err != nil {
		t.Fatalf("EnqueueParseJob() error = %v", err)
	}

	worker := New(st, Options{BatchSize: 5})
	worker.RunOnce(context.Background())

	summary, err := st.GetObservationSummary(traceID)
	if err != nil {
		t.Fatalf("GetObservationSummary() error = %v", err)
	}
	if summary.Parser != "openai" || summary.Status != "parsed" {
		t.Fatalf("summary = %+v", summary)
	}
	nodes, err := st.ListSemanticNodes(traceID)
	if err != nil {
		t.Fatalf("ListSemanticNodes() error = %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("semantic nodes empty")
	}
}

func writeIndexedResponseTrace(t *testing.T, st *store.Store, dir string) string {
	t.Helper()
	reqHead := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n"
	reqBody := `{"model":"gpt-5.1","input":"hello"}`
	resHead := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	resBody := `{"id":"resp_1","object":"response","created_at":1741476777,"status":"completed","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-worker",
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
	logPath := filepath.Join(dir, "worker-trace.http")
	if err := os.WriteFile(logPath, []byte(string(prelude)+reqHead+reqBody+"\n"+resHead+resBody), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := st.UpsertLog(logPath, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}
	entry, err := st.GetByRequestID(header.Meta.RequestID)
	if err != nil {
		t.Fatalf("GetByRequestID() error = %v", err)
	}
	return entry.ID
}
