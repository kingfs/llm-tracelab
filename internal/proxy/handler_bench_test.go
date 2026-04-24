package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
)

func BenchmarkHandlerNonStreamProxy(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15},"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}`)
	}))
	defer upstream.Close()

	outputDir := b.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	cfg := &config.Config{}
	cfg.Upstream.BaseURL = upstream.URL + "/v1"
	cfg.Upstream.ProviderPreset = "openai"
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		b.Fatal(err)
	}
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	client := proxyServer.Client()
	body := []byte(`{"model":"gpt-5","input":"hello","stream":false}`)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			b.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			b.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	}
}
