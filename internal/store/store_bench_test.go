package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func BenchmarkUpsertLogWithGrouping(b *testing.B) {
	st, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	traceDir := b.TempDir()
	header := benchmarkStoreHeader()
	grouping := GroupingInfo{
		SessionID:       "bench-session",
		SessionSource:   "codex",
		WindowID:        "bench-session:0",
		ClientRequestID: "bench-request",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(traceDir, fmt.Sprintf("trace-%d.http", i))
		if err := os.WriteFile(path, []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\n\r\n{}\nHTTP/1.1 200 OK\r\n\r\n{}"), 0o644); err != nil {
			b.Fatal(err)
		}
		header.Meta.RequestID = fmt.Sprintf("bench-%d", i)
		if err := st.UpsertLogWithGrouping(path, header, grouping); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkStoreHeader() recordfile.RecordHeader {
	return recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:               "bench",
			Time:                    time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
			Model:                   "gpt-5",
			Provider:                "openai",
			Operation:               "responses",
			Endpoint:                "/v1/responses",
			URL:                     "/v1/responses",
			Method:                  "POST",
			StatusCode:              200,
			DurationMs:              1200,
			TTFTMs:                  120,
			ContentLength:           256,
			SelectedUpstreamID:      "default",
			SelectedUpstreamBaseURL: "https://api.openai.com/v1",
			RoutingPolicy:           "p2c",
			RoutingScore:            0.1,
			RoutingCandidateCount:   1,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: 64,
			ReqBodyLen:   2,
			ResHeaderLen: 32,
			ResBodyLen:   2,
		},
		Usage: recordfile.UsageInfo{
			PromptTokens:     16,
			CompletionTokens: 8,
			TotalTokens:      24,
		},
	}
}
