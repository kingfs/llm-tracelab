package router

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
)

func BenchmarkRouterSelectStaticCatalog(b *testing.B) {
	cfg := &config.Config{}
	for i := 0; i < 8; i++ {
		cfg.Upstreams = append(cfg.Upstreams, config.UpstreamTargetConfig{
			ID:             fmt.Sprintf("upstream-%d", i),
			Enabled:        boolPtr(true),
			Priority:       100 - i,
			ModelDiscovery: ModelDiscoveryStaticOnly,
			StaticModels:   []string{"gpt-5", "gpt-5.5", fmt.Sprintf("model-%d", i)},
			Upstream: config.UpstreamConfig{
				BaseURL:        fmt.Sprintf("https://example-%d.test/v1", i),
				ProviderPreset: "openai",
			},
		})
	}
	cfg.Router.Selection.Epsilon = 0

	rtr, err := New(cfg, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := rtr.Initialize(); err != nil {
		b.Fatal(err)
	}

	body := []byte(`{"model":"gpt-5.5","input":"hello","stream":true,"max_output_tokens":1024}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, err := http.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", bytes.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		selection, err := rtr.Select(req)
		if err != nil {
			b.Fatal(err)
		}
		rtr.Complete(selection, Outcome{Success: true, StatusCode: http.StatusOK, DurationMs: 1000, TTFTMs: 120, Stream: true})
	}
}
