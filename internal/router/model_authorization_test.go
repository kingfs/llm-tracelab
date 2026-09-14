package router

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
)

func TestConfiguredModelAuthorizationOverridesDiscoveryAndFallback(t *testing.T) {
	for _, allowUnknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "strict", true: "allow-unknown"}[allowUnknown], func(t *testing.T) {
			var calls atomic.Int64
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(`{"data":[{"id":"disabled"},{"id":"undiscovered"}]}`))
			}))
			defer upstreamServer.Close()
			cfg := &config.Config{Upstreams: []config.UpstreamTargetConfig{{
				ID: "managed", ConfiguredModelsOnly: true, ModelDiscovery: ModelDiscoveryListModels,
				StaticModels: []string{"enabled", "disabled"}, DisabledModels: []string{"disabled"}, AllowUnknownModels: &allowUnknown,
				Upstream: config.UpstreamConfig{BaseURL: upstreamServer.URL + "/v1", ProviderPreset: "openai"},
			}}}
			cfg.Router.Fallback.OnMissingModel = "allow_static"
			rtr, err := New(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := rtr.Initialize(); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				model   string
				allowed bool
			}{{"enabled", true}, {"disabled", false}, {"unknown", allowUnknown}} {
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+tc.model+`"}`))
				_, err := rtr.Select(req)
				if (err == nil) != tc.allowed {
					t.Fatalf("Select(%s): %v, allowed=%v", tc.model, err, tc.allowed)
				}
			}
			if calls.Load() != 0 {
				t.Fatalf("authorization refresh performed %d discovery calls", calls.Load())
			}
			if got := rtr.AggregatedModels(); len(got) != 1 || got[0] != "enabled" {
				t.Fatalf("models=%v", got)
			}
		})
	}
}

func TestReloadCommitFailurePreservesRuntime(t *testing.T) {
	rtr, err := New(&config.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := []config.UpstreamTargetConfig{{ID: "old", ConfiguredModelsOnly: true, StaticModels: []string{"old-model"}, Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid/v1", ProviderPreset: "openai"}}}
	if err := rtr.Reload(old); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("commit failed")
	if err := rtr.ReloadWithCommit(nil, nil, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if got := rtr.AggregatedModels(); len(got) != 1 || got[0] != "old-model" {
		t.Fatalf("runtime changed after failed commit: %v", got)
	}
}
