package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	for _, check := range envelope.Result.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	return ""
}
