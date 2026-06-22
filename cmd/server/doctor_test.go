package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type doctorEnvelopeForTest struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Result  struct {
		Status  string `json:"status"`
		Summary struct {
			Pass  int `json:"pass"`
			Warn  int `json:"warn"`
			Fail  int `json:"fail"`
			Total int `json:"total"`
		} `json:"summary"`
		Config struct {
			Database struct {
				DSN string `json:"dsn"`
			} `json:"database"`
			Tools struct {
				WebSearch struct {
					BaseURL string `json:"base_url"`
				} `json:"web_search"`
			} `json:"tools"`
		} `json:"config"`
		Checks []struct {
			Name    string         `json:"name"`
			Status  string         `json:"status"`
			Message string         `json:"message"`
			Detail  map[string]any `json:"detail"`
		} `json:"checks"`
	} `json:"result"`
}

func TestDoctorJSONEnvelopeWarnsForEmptyServerPort(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
monitor:
  port: "9090"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if !envelope.OK || envelope.Command != "doctor" {
		t.Fatalf("envelope = %+v", envelope)
	}
	if envelope.Result.Status != doctorStatusWarn || envelope.Result.Summary.Warn == 0 || envelope.Result.Summary.Fail != 0 {
		t.Fatalf("doctor summary = %+v", envelope.Result.Summary)
	}
	if got := doctorCheckStatusForTest(envelope, "server.port"); got != doctorStatusWarn {
		t.Fatalf("server.port status = %q, want warn", got)
	}
}

func TestDoctorResponsesServerMissingChatCompletionsBackendFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: anthropic
  protocol_family: anthropic
  api_type: responses
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if envelope.Result.Status != doctorStatusFail || envelope.Result.Summary.Fail == 0 {
		t.Fatalf("doctor summary = %+v", envelope.Result.Summary)
	}
	if got := doctorCheckStatusForTest(envelope, "responses_server.backend"); got != doctorStatusFail {
		t.Fatalf("responses_server.backend status = %q, want fail", got)
	}
}

func TestDoctorResponsesServerMissingDefaultModelWarns(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if envelope.Result.Status != doctorStatusWarn || envelope.Result.Summary.Warn == 0 || envelope.Result.Summary.Fail != 0 {
		t.Fatalf("doctor summary = %+v", envelope.Result.Summary)
	}
	if got := doctorCheckStatusForTest(envelope, "responses_server.default_model"); got != doctorStatusWarn {
		t.Fatalf("responses_server.default_model status = %q, want warn", got)
	}
	if got := doctorCheckStatusForTest(envelope, "responses_server.backend"); got != doctorStatusPass {
		t.Fatalf("responses_server.backend status = %q, want pass", got)
	}
}

func TestDoctorResponsesServerDefaultModelPasses(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if envelope.Result.Status != doctorStatusPass || envelope.Result.Summary.Fail != 0 || envelope.Result.Summary.Warn != 0 {
		t.Fatalf("doctor summary = %+v", envelope.Result.Summary)
	}
	if got := doctorCheckStatusForTest(envelope, "responses_server.default_model"); got != doctorStatusPass {
		t.Fatalf("responses_server.default_model status = %q, want pass", got)
	}
	if got := doctorCheckStatusForTest(envelope, "responses_server.store"); got != doctorStatusPass {
		t.Fatalf("responses_server.store status = %q, want pass", got)
	}
	if got := doctorCheckStatusForTest(envelope, "responses_server.model_profiles"); got != doctorStatusPass {
		t.Fatalf("responses_server.model_profiles status = %q, want pass", got)
	}
}

func TestDoctorResponsesServerUnsupportedStoreDriverFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
database:
  driver: mysql
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if got := doctorCheckStatusForTest(envelope, "responses_server.store"); got != doctorStatusFail {
		t.Fatalf("responses_server.store status = %q, want fail", got)
	}
}

func TestDoctorResponsesServerInvalidModelProfileRelationFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  auto_compact: true
  model_profiles:
    - name: gpt-test
      context_window_tokens: 1024
      max_output_tokens: 1024
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if got := doctorCheckStatusForTest(envelope, "responses_server.model_profiles"); got != doctorStatusFail {
		t.Fatalf("responses_server.model_profiles status = %q, want fail", got)
	}
}

func TestDoctorWebSearchSearXNGWithoutBaseURLFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
tools:
  web_search:
    enabled: true
    provider: searxng
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if got := doctorCheckStatusForTest(envelope, "web_search.provider"); got != doctorStatusFail {
		t.Fatalf("web_search.provider status = %q, want fail", got)
	}
}

func TestDoctorFailOnWarnReturnsError(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--fail-on-warn")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want fail-on-warn error, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if envelope.Result.Status != doctorStatusWarn {
		t.Fatalf("doctor status = %q, want warn", envelope.Result.Status)
	}
}

func TestDoctorJSONDoesNotLeakSecrets(t *testing.T) {
	t.Setenv("DOCTOR_TEST_OPENAI_KEY", "sk-doctor-secret")

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: postgres
  dsn: postgres://app:doctor-db-secret@example.com:5432/traces?sslmode=disable&api_key=doctor-db-query-secret
  auto_migrate: false
trace:
  output_dir: "`+t.TempDir()+`"
tools:
  web_search:
    enabled: true
    provider: searxng
    base_url: https://search.example.com?api_key=doctor-search-secret
upstreams:
  - id: openai
    enabled: true
    upstream:
      base_url: https://user:doctor-url-secret@api.example.com/v1?token=doctor-url-token
      api_key: "$env:DOCTOR_TEST_OPENAI_KEY"
      provider_preset: openai
      protocol_family: openai
      api_type: chat_completions
      headers:
        Authorization: Bearer doctor-header-secret
    credentials:
      - id: primary
        api_key: doctor-credential-secret
        headers:
          X-API-Key: doctor-credential-header-secret
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	for _, secret := range []string{
		"doctor-db-secret",
		"doctor-db-query-secret",
		"doctor-search-secret",
		"doctor-url-secret",
		"doctor-url-token",
		"sk-doctor-secret",
		"doctor-header-secret",
		"doctor-credential-secret",
		"doctor-credential-header-secret",
	} {
		if strings.Contains(out, secret) {
			t.Fatalf("doctor output leaked secret marker %q: %s", secret, out)
		}
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	if !strings.Contains(envelope.Result.Config.Database.DSN, "<redacted>") {
		t.Fatalf("database dsn = %q, want redacted", envelope.Result.Config.Database.DSN)
	}
	if !strings.Contains(envelope.Result.Config.Tools.WebSearch.BaseURL, "%3Credacted%3E") {
		t.Fatalf("web_search base_url = %q, want redacted query", envelope.Result.Config.Tools.WebSearch.BaseURL)
	}
}

func TestDoctorDefaultDoesNotProbeProviders(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer upstreamServer.Close()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: "`+upstreamServer.URL+`/v1"
  api_key: doctor-default-secret
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("doctor made %d provider probe requests without --probe-providers, want 0", got)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "provider_probe.network")
	if check.Status != doctorStatusPass || check.Detail["enabled"] != false {
		t.Fatalf("provider_probe.network = %+v, want skipped pass", check)
	}
}

func TestDoctorProbeProvidersReportsDetectedSummary(t *testing.T) {
	t.Parallel()

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer doctor-probe-secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model is required"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"input is required"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstreamServer.Close()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstreams:
  - id: openai-local
    upstream:
      base_url: "`+upstreamServer.URL+`/v1"
      api_key: doctor-probe-secret
      provider_preset: openai
      protocol_family: openai_compatible
      api_type: responses
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--probe-providers")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	if strings.Contains(out, "doctor-probe-secret") || strings.Contains(out, "Authorization") {
		t.Fatalf("doctor provider probe output leaked secret/header: %s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "provider_probe.network")
	if check.Status != doctorStatusPass {
		t.Fatalf("provider_probe.network status = %q, want pass; check=%+v", check.Status, check)
	}
	summary := doctorProbeSummaryForTest(t, check.Detail)
	if summary["total"] != 1 || summary["detected"] != 1 || summary["error"] != 0 || summary["unknown"] != 0 {
		t.Fatalf("provider probe summary = %+v, want detected single target", summary)
	}

	textOut, err := executeDoctorForTest(configPath, "--probe-providers")
	if err != nil {
		t.Fatalf("doctor text Execute() error = %v, output=%s", err, textOut)
	}
	if !strings.Contains(textOut, "provider network probe completed: total=1 detected=1 error=0 unknown=0") {
		t.Fatalf("doctor text output missing provider probe summary: %s", textOut)
	}
}

func TestDoctorProbeProvidersReportsNetworkFailure(t *testing.T) {
	t.Parallel()

	upstreamServer := httptest.NewServer(http.NotFoundHandler())
	probeURL := upstreamServer.URL
	upstreamServer.Close()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
upstreams:
  - id: closed-local
    upstream:
      base_url: "`+probeURL+`/v1"
      api_key: doctor-failure-secret
      provider_preset: openai
      protocol_family: openai_compatible
      api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--probe-providers")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want provider probe failure, output=%s", out)
	}
	if strings.Contains(out, "doctor-failure-secret") {
		t.Fatalf("doctor provider probe failure output leaked secret: %s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "provider_probe.network")
	if check.Status != doctorStatusFail {
		t.Fatalf("provider_probe.network status = %q, want fail; check=%+v", check.Status, check)
	}
	summary := doctorProbeSummaryForTest(t, check.Detail)
	if summary["total"] != 1 || summary["error"] != 1 {
		t.Fatalf("provider probe failure summary = %+v, want one error", summary)
	}
}

func TestDoctorProbeProvidersWithNoProviderDoesNotFail(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--probe-providers")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "provider_probe.network")
	if check.Status != doctorStatusWarn {
		t.Fatalf("provider_probe.network status = %q, want warn; check=%+v", check.Status, check)
	}
	summary := doctorProbeSummaryForTest(t, check.Detail)
	if summary["total"] != 0 || summary["detected"] != 0 || summary["error"] != 0 || summary["unknown"] != 0 {
		t.Fatalf("provider probe no-provider summary = %+v, want all zero", summary)
	}
}

func executeDoctorForTest(configPath string, args ...string) (string, error) {
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	fullArgs := append([]string{"-c", configPath, "doctor"}, args...)
	cmd.SetArgs(fullArgs)
	err := cmd.Execute()
	return out.String(), err
}

func writeDoctorTestConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return configPath
}

func decodeDoctorEnvelopeForTest(t *testing.T, output string) doctorEnvelopeForTest {
	t.Helper()
	var envelope doctorEnvelopeForTest
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, output=%q", err, output)
	}
	return envelope
}

func doctorCheckStatusForTest(envelope doctorEnvelopeForTest, name string) string {
	check := doctorCheckForTest(envelope, name)
	return check.Status
}

func doctorCheckForTest(envelope doctorEnvelopeForTest, name string) struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail"`
} {
	for _, check := range envelope.Result.Checks {
		if check.Name == name {
			return check
		}
	}
	return struct {
		Name    string         `json:"name"`
		Status  string         `json:"status"`
		Message string         `json:"message"`
		Detail  map[string]any `json:"detail"`
	}{}
}

func doctorProbeSummaryForTest(t *testing.T, detail map[string]any) map[string]int {
	t.Helper()
	raw, ok := detail["summary"].(map[string]any)
	if !ok {
		t.Fatalf("provider probe detail summary = %#v, want object", detail["summary"])
	}
	out := make(map[string]int, len(raw))
	for key, value := range raw {
		number, ok := value.(float64)
		if !ok {
			t.Fatalf("provider probe summary[%s] = %#v, want number", key, value)
		}
		out[key] = int(number)
	}
	return out
}
