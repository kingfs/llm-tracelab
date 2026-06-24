package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/store"
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
	check := doctorCheckForTest(envelope, "responses_server.model_catalog_drift")
	if check.Status != doctorStatusWarn || check.Detail["skipped_reason"] != "responses_server.default_model is empty" {
		t.Fatalf("responses_server.model_catalog_drift = %+v, want skipped warning", check)
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

func TestDoctorCodexConfigDriftSkipsLocalConfigWithoutFlag(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	codexDir := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.codex) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(`api_key = "doctor-codex-home-secret"`), 0o644); err != nil {
		t.Fatalf("WriteFile(home codex config) error = %v", err)
	}
	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-5
  model_profiles:
    - name: "gpt-5"
      context_window_tokens: 200
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
	if strings.Contains(out, "doctor-codex-home-secret") {
		t.Fatalf("doctor output leaked unconfigured Codex config secret: %s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.codex_config_drift")
	if check.Status != doctorStatusPass || check.Detail["status"] != "not_configured" || check.Detail["configured"] != false {
		t.Fatalf("responses_server.codex_config_drift = %+v, want not_configured pass", check)
	}
}

func TestDoctorCodexConfigDriftReportsMatchingLocalConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  path: /v1/responses
  default_model: gpt-5
  model_profiles:
    - name: "gpt-5"
      context_window_tokens: 200
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
	codexPath := filepath.Join(dir, "codex.toml")
	codexBody := `
[profiles.gpt-5]
model_provider = "llm-tracelab"
model = "gpt-5"
model_context_window = 200
model_auto_compact_token_limit = 160

[model_providers.llm-tracelab]
name = "llm-tracelab"
base_url = "http://127.0.0.1:8080/v1"
env_key = "LLM_TRACELAB_API_KEY"
wire_api = "responses"
request_max_retries = 2
stream_max_retries = 2
stream_idle_timeout_ms = 120000
`
	if err := os.WriteFile(codexPath, []byte(codexBody), 0o644); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}

	out, err := executeDoctorForTest(configPath, "--format", "json", "--codex-config", codexPath)
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.codex_config_drift")
	if check.Status != doctorStatusPass || check.Detail["status"] != "ok" || check.Detail["profile_present"] != true || check.Detail["provider_present"] != true {
		t.Fatalf("responses_server.codex_config_drift = %+v, want ok", check)
	}
	if warnings, ok := check.Detail["drift_warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("drift_warnings = %#v, want empty", check.Detail["drift_warnings"])
	}
}

func TestDoctorCodexConfigDriftWarnsForLocalDrift(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-5
  model_profiles:
    - name: "gpt-5"
      context_window_tokens: 200
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
	codexPath := filepath.Join(dir, "codex.toml")
	codexBody := `
[profiles.other-model]
model_provider = "other-provider"
model = "other-model"
model_context_window = 100
model_auto_compact_token_limit = 80

[model_providers.other-provider]
base_url = "http://127.0.0.1:9999/v1"
env_key = "OTHER_KEY"
wire_api = "chat"
`
	if err := os.WriteFile(codexPath, []byte(codexBody), 0o644); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}

	out, err := executeDoctorForTest(configPath, "--format", "json", "--codex-config", codexPath)
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.codex_config_drift")
	if check.Status != doctorStatusWarn || envelope.Result.Status != doctorStatusWarn || envelope.Result.Summary.Fail != 0 {
		t.Fatalf("doctor result = %+v check = %+v, want drift warning without failure", envelope.Result.Summary, check)
	}
	for _, want := range []string{"profile \"gpt-5\" is missing", "provider \"llm-tracelab\" is missing", "profile.model_provider is missing", "provider.base_url is missing"} {
		if !doctorDetailStringSliceContains(check.Detail, "drift_warnings", want) {
			t.Fatalf("drift_warnings missing %q: %#v", want, check.Detail["drift_warnings"])
		}
	}
}

func TestDoctorCodexConfigDriftDoesNotLeakLocalSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-5
  model_profiles:
    - name: "gpt-5"
      context_window_tokens: 200
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
	codexPath := filepath.Join(dir, "codex.toml")
	codexBody := `
[profiles.gpt-5]
model_provider = "llm-tracelab"
model = "gpt-5"
model_context_window = 100
model_auto_compact_token_limit = 80

[model_providers.llm-tracelab]
base_url = "https://user:doctor-codex-url-secret@example.com/v1?token=doctor-codex-query-secret"
env_key = "LLM_TRACELAB_API_KEY"
wire_api = "chat"
api_key = "doctor-codex-api-secret"
`
	if err := os.WriteFile(codexPath, []byte(codexBody), 0o644); err != nil {
		t.Fatalf("WriteFile(codex) error = %v", err)
	}

	out, err := executeDoctorForTest(configPath, "--format", "json", "--codex-config", codexPath)
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	for _, secret := range []string{"doctor-codex-url-secret", "doctor-codex-query-secret", "doctor-codex-api-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("doctor output leaked local Codex secret marker %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "%3Credacted%3E") {
		t.Fatalf("doctor output = %s, want redacted URL value", out)
	}
}

func TestDoctorResponsesHTTPGuardDisabledPasses(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: false
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
	check := doctorCheckForTest(envelope, "responses_server.http_guard")
	if check.Status != doctorStatusPass || check.Detail["skipped_reason"] != "responses_server.enabled is false" {
		t.Fatalf("responses_server.http_guard = %+v, want disabled pass", check)
	}
}

func TestDoctorResponsesHTTPGuardInvalidPathFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  path: v1/responses
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
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.http_guard")
	if check.Status != doctorStatusFail {
		t.Fatalf("responses_server.http_guard = %+v, want fail", check)
	}
	if check.Detail["responses_path"] != "v1/responses" {
		t.Fatalf("responses_path = %#v, want raw invalid path", check.Detail["responses_path"])
	}
}

func TestDoctorResponsesHTTPGuardTinyBodyWarns(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  max_request_body_bytes: 16
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
	check := doctorCheckForTest(envelope, "responses_server.http_guard")
	if check.Status != doctorStatusWarn {
		t.Fatalf("responses_server.http_guard = %+v, want warn", check)
	}
	if got := int64(check.Detail["max_request_body_bytes"].(float64)); got != 16 {
		t.Fatalf("max_request_body_bytes = %d, want 16", got)
	}
	if !doctorDetailStringSliceContains(check.Detail, "warnings", "below 1024 bytes") {
		t.Fatalf("warnings = %#v, want tiny body warning", check.Detail["warnings"])
	}
}

func TestDoctorResponsesHTTPGuardNormalConfigPasses(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
monitor:
  port: "9090"
responses_server:
  enabled: true
  default_model: gpt-test
  path: /v1/responses
  max_request_body_bytes: 1048576
  force_store: true
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
	check := doctorCheckForTest(envelope, "responses_server.http_guard")
	if check.Status != doctorStatusPass {
		t.Fatalf("responses_server.http_guard = %+v, want pass", check)
	}
	if check.Detail["responses_path"] != "/v1/responses" || check.Detail["normalized_path"] != "/v1/responses" || check.Detail["force_store"] != true {
		t.Fatalf("responses_server.http_guard detail = %+v, want path/force_store detail", check.Detail)
	}
	authDetail, ok := check.Detail["auth"].(map[string]any)
	if !ok || authDetail["proxy_auth_required"] != true || authDetail["verifier_status_check"] != "configuration-only" {
		t.Fatalf("auth detail = %#v, want conservative auth status", check.Detail["auth"])
	}
	managementDetail, ok := check.Detail["management"].(map[string]any)
	if !ok || managementDetail["same_port"] != false || managementDetail["runtime_conflict_possible"] != false {
		t.Fatalf("management detail = %#v, want separate-port no conflict", check.Detail["management"])
	}
}

func TestDoctorResponsesHTTPGuardMCPPathConflictFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
monitor:
  port: "8080"
mcp:
  enabled: true
  path: /v1/responses
responses_server:
  enabled: true
  default_model: gpt-test
  path: /v1/responses
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
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.http_guard")
	if check.Status != doctorStatusFail {
		t.Fatalf("responses_server.http_guard = %+v, want fail", check)
	}
	managementDetail, ok := check.Detail["management"].(map[string]any)
	if !ok || managementDetail["mcp_path_overlap"] != true || managementDetail["runtime_conflict_possible"] != true {
		t.Fatalf("management detail = %#v, want mcp path conflict", check.Detail["management"])
	}
}

func TestDoctorResponsesModelCatalogDriftDisabledPasses(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: false
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
	check := doctorCheckForTest(envelope, "responses_server.model_catalog_drift")
	if check.Status != doctorStatusPass || check.Detail["skipped_reason"] != "responses_server.enabled is false" {
		t.Fatalf("responses_server.model_catalog_drift = %+v, want disabled pass", check)
	}
}

func TestDoctorResponsesModelCatalogDriftDBUnavailablePasses(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  model_profiles:
    - name: gpt-test
      context_window_tokens: 2048
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
	check := doctorCheckForTest(envelope, "responses_server.model_catalog_drift")
	if check.Status != doctorStatusPass || check.Detail["database_available"] != false || check.Detail["catalog_source"] != "unavailable" || check.Detail["channel_source"] != "unavailable" {
		t.Fatalf("responses_server.model_catalog_drift = %+v, want unavailable pass", check)
	}
	if strings.Contains(out, "api.example.com") {
		t.Fatalf("doctor output exposed upstream URL while checking unavailable DB: %s", out)
	}
}

func TestDoctorResponsesModelCatalogDriftCatalogAndChannelHitsPass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	writeDoctorModelCatalogDriftStore(t, dir, dbPath, true)
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-5
  model_profiles:
    - name: gpt-5
      context_window_tokens: 2048
database:
  driver: sqlite
  dsn: "`+dbPath+`"
trace:
  output_dir: "`+dir+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.model_catalog_drift")
	if check.Status != doctorStatusPass || check.Detail["database_available"] != true || check.Detail["catalog_model_present"] != true || check.Detail["channel_model_present"] != true {
		t.Fatalf("responses_server.model_catalog_drift = %+v, want catalog/channel pass", check)
	}
	if got := int(check.Detail["channel_model_count"].(float64)); got != 1 {
		t.Fatalf("channel_model_count = %d, want 1", got)
	}
	if warnings, ok := check.Detail["drift_warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("drift_warnings = %#v, want empty", check.Detail["drift_warnings"])
	}
}

func TestDoctorResponsesModelCatalogDriftWarnsForProfileCatalogChannelMiss(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	writeDoctorModelCatalogDriftStore(t, dir, dbPath, false)
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-5
  model_profiles:
    - name: gpt-5
      context_window_tokens: 2048
database:
  driver: sqlite
  dsn: "`+dbPath+`"
trace:
  output_dir: "`+dir+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	out, err := executeDoctorForTest(configPath, "--format", "json")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.model_catalog_drift")
	if check.Status != doctorStatusWarn || envelope.Result.Status != doctorStatusWarn || envelope.Result.Summary.Fail != 0 {
		t.Fatalf("doctor result = %+v check = %+v, want drift warning without failure", envelope.Result.Summary, check)
	}
	for _, want := range []string{"model_catalog has no entry", "channel_models has no entry"} {
		if !doctorDetailStringSliceContains(check.Detail, "drift_warnings", want) {
			t.Fatalf("drift_warnings missing %q: %#v", want, check.Detail["drift_warnings"])
		}
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

func TestDoctorResponsesStoreHealthDisabledSkips(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: false
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
	check := doctorCheckForTest(envelope, "responses_server.store_health")
	if check.Status != doctorStatusPass || check.Detail["skipped_reason"] != "responses_server.enabled is false" {
		t.Fatalf("responses_server.store_health = %+v, want disabled pass with skipped reason", check)
	}
}

func TestDoctorResponsesStoreHealthOfflineReadyPasses(t *testing.T) {
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
	check := doctorCheckForTest(envelope, "responses_server.store_health")
	if check.Status != doctorStatusPass || check.Detail["status_check"] != "configuration-only" || check.Detail["check_db_required"] != false {
		t.Fatalf("responses_server.store_health = %+v, want offline ready pass", check)
	}
	if got := len(check.Detail["required_tables"].([]any)); got != 7 {
		t.Fatalf("required_tables count = %d, want 7", got)
	}
}

func TestDoctorResponsesStoreHealthOfflineForceStoreWarns(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  force_store: true
database:
  driver: sqlite
  auto_migrate: false
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
	check := doctorCheckForTest(envelope, "responses_server.store_health")
	if check.Status != doctorStatusWarn || check.Detail["check_db_required"] != true {
		t.Fatalf("responses_server.store_health = %+v, want force_store offline warning", check)
	}
	if warnings, ok := check.Detail["warnings"].([]any); !ok || len(warnings) == 0 {
		t.Fatalf("warnings = %#v, want non-empty warning list", check.Detail["warnings"])
	}
}

func TestDoctorResponsesStoreHealthCheckDBMissingTableFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE responses (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create partial sqlite schema error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
database:
  driver: sqlite
  dsn: "`+dbPath+`"
  auto_migrate: false
trace:
  output_dir: "`+dir+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--check-db")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want missing table failure, output=%s", out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.store_health")
	if check.Status != doctorStatusFail || check.Detail["database_required_tables_present"] != false {
		t.Fatalf("responses_server.store_health = %+v, want missing table fail", check)
	}
	if !doctorDetailStringSliceContains(check.Detail, "database_missing_tables", "response_items") || !doctorDetailStringSliceContains(check.Detail, "database_missing_tables", "app_settings") {
		t.Fatalf("database_missing_tables = %#v, want response_items and app_settings", check.Detail["database_missing_tables"])
	}
}

func TestDoctorResponsesStoreHealthCheckDBSQLiteTablesPass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trace_index.sqlite3")
	st, err := store.NewWithDatabase(dir, "sqlite", dbPath, 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  force_store: true
database:
  driver: sqlite
  dsn: "`+dbPath+`"
  auto_migrate: false
trace:
  output_dir: "`+dir+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--check-db")
	if err != nil {
		t.Fatalf("doctor Execute() error = %v, output=%s", err, out)
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.store_health")
	if check.Status != doctorStatusPass || check.Detail["status_check"] != "database" || check.Detail["database_required_tables_present"] != true {
		t.Fatalf("responses_server.store_health = %+v, want SQLite table pass", check)
	}
}

func TestDoctorResponsesStoreHealthCheckDBDoesNotLeakDSNSecret(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
responses_server:
  enabled: true
  default_model: gpt-test
  force_store: true
database:
  driver: postgres
  dsn: postgres://app:doctor-store-secret@127.0.0.1:1/traces?sslmode=disable&api_key=doctor-store-query-secret
  auto_migrate: false
trace:
  output_dir: "`+t.TempDir()+`"
upstream:
  base_url: https://api.example.com/v1
  provider_preset: openai
  protocol_family: openai_compatible
  api_type: chat_completions
`)

	out, err := executeDoctorForTest(configPath, "--format", "json", "--check-db")
	if err == nil {
		t.Fatalf("doctor Execute() error = nil, want database failure, output=%s", out)
	}
	for _, secret := range []string{"doctor-store-secret", "doctor-store-query-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("doctor output leaked secret marker %q: %s", secret, out)
		}
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "responses_server.store_health")
	if check.Status != doctorStatusFail || !strings.Contains(fmt.Sprint(check.Detail["database_dsn"]), "<redacted>") {
		t.Fatalf("responses_server.store_health = %+v, want redacted database failure", check)
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

func TestDoctorMCPToolsDisabledPasses(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
tools:
  mcp:
    enabled: false
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
	check := doctorCheckForTest(envelope, "tools.mcp.config")
	if check.Status != doctorStatusPass || check.Detail["skipped_reason"] != "tools.mcp.enabled is false" {
		t.Fatalf("tools.mcp.config = %+v, want skipped pass", check)
	}
}

func TestDoctorMCPToolsEnabledWithoutServersFails(t *testing.T) {
	t.Parallel()

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
tools:
  mcp:
    enabled: true
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
	check := doctorCheckForTest(envelope, "tools.mcp.config")
	if check.Status != doctorStatusFail || check.Detail["server_count"] != float64(0) {
		t.Fatalf("tools.mcp.config = %+v, want no-server failure", check)
	}
}

func TestDoctorMCPToolsInvalidServerFailsWithoutLeakingBearerToken(t *testing.T) {
	t.Setenv("DOCTOR_MCP_TOKEN", "doctor-mcp-token-secret")
	t.Setenv("DOCTOR_MCP_EMPTY_TOKEN", "")

	configPath := writeDoctorTestConfig(t, `
server:
  port: "8080"
database:
  driver: sqlite
trace:
  output_dir: "`+t.TempDir()+`"
tools:
  mcp:
    enabled: true
    servers:
      - id: missing-url
        bearer_token_env: DOCTOR_MCP_TOKEN
      - id: empty-token
        url: https://mcp.example.com/sse?api_key=doctor-mcp-query-secret
        bearer_token_env: DOCTOR_MCP_EMPTY_TOKEN
      - id: disabled
        enabled: false
        bearer_token_env: DOCTOR_MCP_TOKEN
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
	for _, secret := range []string{"doctor-mcp-token-secret", "doctor-mcp-query-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("doctor output leaked secret marker %q: %s", secret, out)
		}
	}
	envelope := decodeDoctorEnvelopeForTest(t, out)
	check := doctorCheckForTest(envelope, "tools.mcp.config")
	if check.Status != doctorStatusFail || check.Detail["enabled_servers"] != float64(2) {
		t.Fatalf("tools.mcp.config = %+v, want invalid server failure", check)
	}
	if !doctorDetailStringSliceContains(check.Detail, "failures", "missing-url") || !doctorDetailStringSliceContains(check.Detail, "failures", "environment variable is empty or unset") {
		t.Fatalf("tools.mcp.config failures = %+v", check.Detail["failures"])
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
auth:
  database_path: /tmp/doctor-auth-path-secret.sqlite3
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
		"doctor-auth-path-secret",
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

func writeDoctorModelCatalogDriftStore(t *testing.T, dir string, dbPath string, includeModel bool) {
	t.Helper()
	st, err := store.NewWithDatabase(dir, "sqlite", dbPath, 4, 4)
	if err != nil {
		t.Fatalf("NewWithDatabase() error = %v", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()
	if !includeModel {
		return
	}
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "openai-primary",
		Name:           "OpenAI Primary",
		BaseURL:        "https://api.openai.com/v1",
		HeadersJSON:    "{}",
		Enabled:        true,
		Priority:       10,
		Weight:         1,
		CapacityHint:   1,
		ModelDiscovery: "list_models",
	}); err != nil {
		t.Fatalf("UpsertChannelConfig() error = %v", err)
	}
	if err := st.ReplaceChannelModels("openai-primary", []store.ChannelModelRecord{
		{Model: "gpt-5", Source: "manual", Enabled: true},
	}); err != nil {
		t.Fatalf("ReplaceChannelModels() error = %v", err)
	}
	if err := st.UpsertModelCatalog(store.ModelCatalogRecord{Model: "gpt-5", DisplayName: "GPT-5"}); err != nil {
		t.Fatalf("UpsertModelCatalog() error = %v", err)
	}
}

func doctorDetailStringSliceContains(detail map[string]any, key string, fragment string) bool {
	items, ok := detail[key].([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		text, ok := item.(string)
		if ok && strings.Contains(text, fragment) {
			return true
		}
	}
	return false
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
