package replay

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func BenchmarkTransportRoundTrip(b *testing.B) {
	path := writeBenchmarkReplayFile(b)
	tr := NewTransport(path)
	req, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello"}`))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := tr.RoundTrip(req)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			b.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func writeBenchmarkReplayFile(b *testing.B) string {
	b.Helper()

	reqHeader := []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n")
	reqBody := []byte(`{"model":"gpt-5","input":"hello"}`)
	resHeader := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")
	resBody := []byte(`{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}`)
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:  "bench-replay",
			Time:       time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
			Model:      "gpt-5",
			Provider:   "openai",
			Operation:  "responses",
			Endpoint:   "/v1/responses",
			URL:        "/v1/responses",
			Method:     http.MethodPost,
			StatusCode: http.StatusOK,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHeader)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHeader)),
			ResBodyLen:   int64(len(resBody)),
		},
	}
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		b.Fatal(err)
	}

	var content []byte
	content = append(content, prelude...)
	content = append(content, reqHeader...)
	content = append(content, reqBody...)
	content = append(content, '\n')
	content = append(content, resHeader...)
	content = append(content, resBody...)

	path := filepath.Join(b.TempDir(), "bench.http")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}
