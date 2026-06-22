package providerprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/upstream"
)

func TestProbeOpenAIChatOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model is required"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Probe(context.Background(), ProbeTarget{
		ProviderID: "openai-compatible",
		BaseURL:    server.URL,
		APIKey:     "test-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if report.Status != StatusDetected {
		t.Fatalf("Status = %q, want %q", report.Status, StatusDetected)
	}
	if report.SuggestedProtocolFamily != upstream.ProtocolFamilyOpenAICompatible {
		t.Fatalf("SuggestedProtocolFamily = %q", report.SuggestedProtocolFamily)
	}
	if report.SuggestedAPIType != upstream.APITypeChatCompletions {
		t.Fatalf("SuggestedAPIType = %q", report.SuggestedAPIType)
	}
	assertHasCapabilities(t, report.Capabilities, upstream.CapabilityModels, upstream.CapabilityChatCompletions)
	assertLacksCapabilities(t, report.Capabilities, upstream.CapabilityResponses)
}

func TestProbeOpenAIResponsesCapableKeepsSpecifiedAsWarningOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/responses":
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Probe(context.Background(), ProbeTarget{
		BaseURL:                 server.URL + "/api/v1",
		APIKey:                  "test-key",
		SpecifiedAPIType:        upstream.APITypeChatCompletions,
		SpecifiedProtocolFamily: upstream.ProtocolFamilyOpenAICompatible,
	}, server.Client())
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if report.SuggestedAPIType != upstream.APITypeResponses {
		t.Fatalf("SuggestedAPIType = %q, want %q", report.SuggestedAPIType, upstream.APITypeResponses)
	}
	if report.SuggestedProtocolFamily != upstream.ProtocolFamilyOpenAICompatible {
		t.Fatalf("SuggestedProtocolFamily = %q", report.SuggestedProtocolFamily)
	}
	if !containsWarning(report.Warnings, "specified api_type") {
		t.Fatalf("Warnings = %#v, want specified api_type mismatch warning", report.Warnings)
	}
	assertHasCapabilities(t, report.Capabilities, upstream.CapabilityModels, upstream.CapabilityChatCompletions, upstream.CapabilityResponses)
}

func TestProbeAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isAnthropicAuth := r.Header.Get("x-api-key") != "" && r.Header.Get("anthropic-version") != ""
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/messages" && isAnthropicAuth:
			w.WriteHeader(http.StatusBadRequest)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models" && isAnthropicAuth:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Probe(context.Background(), ProbeTarget{
		BaseURL: server.URL,
		APIKey:  "test-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if report.SuggestedProtocolFamily != upstream.ProtocolFamilyAnthropicMessages {
		t.Fatalf("SuggestedProtocolFamily = %q", report.SuggestedProtocolFamily)
	}
	if report.SuggestedAPIType != upstream.APITypeMessages {
		t.Fatalf("SuggestedAPIType = %q", report.SuggestedAPIType)
	}
	assertHasCapabilities(t, report.Capabilities, CapabilityMessages, upstream.CapabilityModels)
}

func TestProbeGemini(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1beta/models" && r.Header.Get("x-goog-api-key") != "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-test"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	report, err := Probe(context.Background(), ProbeTarget{
		BaseURL: server.URL,
		APIKey:  "test-key",
	}, server.Client())
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if report.SuggestedProtocolFamily != upstream.ProtocolFamilyGoogleGenAI {
		t.Fatalf("SuggestedProtocolFamily = %q", report.SuggestedProtocolFamily)
	}
	if report.SuggestedAPIType != upstream.APITypeGemini {
		t.Fatalf("SuggestedAPIType = %q", report.SuggestedAPIType)
	}
	assertHasCapabilities(t, report.Capabilities, upstream.CapabilityModels, CapabilityGeminiGenerateContent)
}

func TestProbeUnknown(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	report, err := Probe(context.Background(), ProbeTarget{BaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if report.Status != StatusUnknown {
		t.Fatalf("Status = %q, want %q", report.Status, StatusUnknown)
	}
	if report.SuggestedAPIType != "" || report.SuggestedProtocolFamily != "" || len(report.Capabilities) != 0 {
		t.Fatalf("unexpected suggestion: %#v", report.CapabilitySuggestion)
	}
}

func TestProbeUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	baseURL := server.URL
	client := server.Client()
	server.Close()

	report, err := Probe(context.Background(), ProbeTarget{BaseURL: baseURL}, client)
	if err != nil {
		t.Fatalf("Probe error = %v", err)
	}
	if report.Status != StatusError {
		t.Fatalf("Status = %q, want %q", report.Status, StatusError)
	}
	if report.Error == "" {
		t.Fatal("Error is empty, want network error detail")
	}
}

func TestProbeReportJSONDoesNotSerializeAPIKey(t *testing.T) {
	target := ProbeTarget{
		ProviderID:              "provider",
		BaseURL:                 "http://127.0.0.1:1",
		APIKey:                  "secret-key",
		SpecifiedAPIType:        "CHAT_COMPLETIONS",
		SpecifiedProtocolFamily: "OPENAI_COMPATIBLE",
	}
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("Marshal ProbeTarget error = %v", err)
	}
	if strings.Contains(string(data), "secret-key") {
		t.Fatalf("serialized target leaked API key: %s", string(data))
	}
}

func assertHasCapabilities(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, capability := range want {
		if !hasCapability(got, capability) {
			t.Fatalf("Capabilities = %#v, want %q", got, capability)
		}
	}
}

func assertLacksCapabilities(t *testing.T, got []string, rejected ...string) {
	t.Helper()
	for _, capability := range rejected {
		if hasCapability(got, capability) {
			t.Fatalf("Capabilities = %#v, did not want %q", got, capability)
		}
	}
}

func hasCapability(values []string, capability string) bool {
	for _, value := range values {
		if value == capability {
			return true
		}
	}
	return false
}

func containsWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}
