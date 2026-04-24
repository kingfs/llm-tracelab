package recordfile

import (
	"bytes"
	"testing"
	"time"
)

func BenchmarkParsePreludeV3(b *testing.B) {
	content := benchmarkRecordContent(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := ParsePrelude(content); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractSections(b *testing.B) {
	content := benchmarkRecordContent(b)
	parsed, err := ParsePrelude(content)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		reqFull, reqBody, resFull, resBody := ExtractSections(content, parsed)
		if len(reqFull) == 0 || len(reqBody) == 0 || len(resFull) == 0 || len(resBody) == 0 {
			b.Fatal("empty extracted section")
		}
	}
}

func benchmarkRecordContent(b *testing.B) []byte {
	b.Helper()

	reqHeader := []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n")
	reqBody := bytes.Repeat([]byte(`{"model":"gpt-5","input":"hello"}`), 64)
	resHeader := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")
	resBody := bytes.Repeat([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}`), 128)

	header := RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: MetaData{
			RequestID:  "bench-req",
			Time:       time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
			Model:      "gpt-5",
			Provider:   "openai",
			Operation:  "responses",
			Endpoint:   "/v1/responses",
			URL:        "/v1/responses",
			Method:     "POST",
			StatusCode: 200,
			DurationMs: 1200,
			TTFTMs:     120,
		},
		Layout: LayoutInfo{
			ReqHeaderLen: int64(len(reqHeader)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHeader)),
			ResBodyLen:   int64(len(resBody)),
		},
		Usage: UsageInfo{
			PromptTokens:     1024,
			CompletionTokens: 128,
			TotalTokens:      1152,
		},
	}
	prelude, err := MarshalPrelude(header, BuildEvents(header))
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
	return content
}
