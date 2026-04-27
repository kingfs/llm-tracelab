package upstream

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
)

func TestCheckConnectivityPrintsAvailableModelsAndDiagnostics(t *testing.T) {
	var (
		gotPath   string
		gotAPIKey string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-flash"},{"name":"models/gemini-2.5-pro"}]}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	err := checkConnectivity(config.UpstreamConfig{
		BaseURL:        srv.URL,
		ProviderPreset: "google_genai",
		ApiKey:         "goog-secret",
	}, srv.Client(), &out)
	if err != nil {
		t.Fatalf("checkConnectivity() error = %v", err)
	}
	if gotPath != "/v1beta/models" {
		t.Fatalf("request path = %q, want /v1beta/models", gotPath)
	}
	if gotAPIKey != "goog-secret" {
		t.Fatalf("x-goog-api-key = %q, want goog-secret", gotAPIKey)
	}
	stdout := out.String()
	for _, want := range []string{
		"=== AVAILABLE MODELS ===",
		"- models/gemini-2.5-flash",
		"- models/gemini-2.5-pro",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want contain %q", stdout, want)
		}
	}
	logOutput := logs.String()
	for _, want := range []string{
		"Starting upstream connectivity check...",
		"connectivity_endpoint=/v1beta/models",
		"provider_preset=google_genai",
		"routing_profile=google_ai_studio",
		"model_routing_hint=\"model is selected in the request path",
		"Upstream connectivity check passed.",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("logs = %q, want contain %q", logOutput, want)
		}
	}
}

func TestCheckConnectivityPrintsRequestDumpOnTransportError(t *testing.T) {
	var out bytes.Buffer
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp 127.0.0.1:443: connect: connection refused")
		}),
	}

	err := checkConnectivity(config.UpstreamConfig{
		BaseURL: "https://api.openai.com/v1",
	}, client, &out)
	if err == nil {
		t.Fatal("checkConnectivity() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %q, want connection refused", err.Error())
	}

	stdout := out.String()
	for _, want := range []string{
		"=== REQUEST DUMP ===",
		"GET /v1/models HTTP/1.1",
		"Host: api.openai.com",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want contain %q", stdout, want)
		}
	}
	logOutput := logs.String()
	for _, want := range []string{
		"Starting upstream connectivity check...",
		"connectivity_endpoint=/models",
		"Upstream check connection failed",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("logs = %q, want contain %q", logOutput, want)
		}
	}
}

func TestDefaultConnectivityHTTPClientVerifiesTLSByDefault(t *testing.T) {
	t.Parallel()

	client := defaultConnectivityHTTPClient()
	if client.Timeout == 0 {
		t.Fatal("default connectivity client has no timeout")
	}
	transport, _ := client.Transport.(*http.Transport)
	if transport != nil && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("default connectivity client disables TLS certificate verification")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCheckConnectivityPrintsFailedInteraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	err := checkConnectivity(config.UpstreamConfig{
		BaseURL: srv.URL + "/v1",
	}, srv.Client(), &out)
	if err == nil {
		t.Fatal("checkConnectivity() error = nil, want non-nil")
	}
	if err.Error() != "upstream status: 429 Too Many Requests" {
		t.Fatalf("error = %q, want upstream status: 429 Too Many Requests", err.Error())
	}

	stdout := out.String()
	for _, want := range []string{
		"=== FAILED INTERACTION ===",
		"--- REQUEST ---",
		"GET /v1/models HTTP/1.1",
		"--- RESPONSE ---",
		"HTTP/1.1 429 Too Many Requests",
		`{"error":{"message":"rate limited"}}`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want contain %q", stdout, want)
		}
	}
	logOutput := logs.String()
	for _, want := range []string{
		"Starting upstream connectivity check...",
		"connectivity_endpoint=/models",
		"Upstream check returned non-200 status",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("logs = %q, want contain %q", logOutput, want)
		}
	}
}
