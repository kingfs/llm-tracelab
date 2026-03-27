package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestStatsHandlesAverageTTFTAsFloat(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, statusCode int, ttftMs int64, totalTokens int) {
		t.Helper()

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}

		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:     name,
				Time:          time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
				Model:         "gpt-test",
				URL:           "/v1/chat/completions",
				Method:        "POST",
				StatusCode:    statusCode,
				DurationMs:    ttftMs,
				TTFTMs:        ttftMs,
				ClientIP:      "127.0.0.1",
				ContentLength: 4,
			},
			Layout: recordfile.LayoutInfo{},
			Usage: recordfile.UsageInfo{
				TotalTokens: totalTokens,
			},
		}

		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", path, err)
		}
	}

	writeLog("success-a.http", 200, 800, 10)
	writeLog("success-b.http", 201, 813, 20)
	writeLog("failed.http", 500, 999, 99)

	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.TotalRequest != 3 {
		t.Fatalf("TotalRequest = %d, want 3", stats.TotalRequest)
	}
	if stats.SuccessRequest != 2 {
		t.Fatalf("SuccessRequest = %d, want 2", stats.SuccessRequest)
	}
	if stats.FailedRequest != 1 {
		t.Fatalf("FailedRequest = %d, want 1", stats.FailedRequest)
	}
	if stats.TotalTokens != 30 {
		t.Fatalf("TotalTokens = %d, want 30", stats.TotalTokens)
	}
	if stats.AvgTTFT != 807 {
		t.Fatalf("AvgTTFT = %d, want 807", stats.AvgTTFT)
	}
}
