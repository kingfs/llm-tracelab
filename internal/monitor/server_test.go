package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestListAPIHandlerReturnsPagedItems(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"messages":[{"role":"user","content":"Inspect this request."}],"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file.","parameters":{"type":"object","properties":{"file_path":{"type":"string"}}}}}]}`
	content := buildRecordFixture(t, "/v1/chat/completions", false, reqBody, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/traces?page=1&page_size=50", nil)
	rr := httptest.NewRecorder()
	listAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload listResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].ID == "" {
		t.Fatalf("trace id missing from list payload")
	}
	if payload.Items[0].Operation != "chat.completions" {
		t.Fatalf("operation = %q, want chat.completions", payload.Items[0].Operation)
	}
}

func TestSessionListAPIHandlerReturnsGroupedItems(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	traceA := filepath.Join(outputDir, "trace-a.http")
	traceB := filepath.Join(outputDir, "trace-b.http")
	sessionID := "019d9659-34ca-7b03-917e-9f6bb8bc550b"
	contentA := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: " + sessionID},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	contentB := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		true,
		[]string{`X-Codex-Turn-Metadata: {"session_id":"` + sessionID + `"}`},
		`{"input":"hello again"}`,
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n",
	)
	if err := os.WriteFile(traceA, contentA, 0o644); err != nil {
		t.Fatalf("WriteFile(traceA) error = %v", err)
	}
	if err := os.WriteFile(traceB, contentB, 0o644); err != nil {
		t.Fatalf("WriteFile(traceB) error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?page=1&page_size=50", nil)
	rr := httptest.NewRecorder()
	sessionListAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].SessionID != sessionID {
		t.Fatalf("SessionID = %q, want %q", payload.Items[0].SessionID, sessionID)
	}
	if payload.Items[0].RequestCount != 2 {
		t.Fatalf("RequestCount = %d, want 2", payload.Items[0].RequestCount)
	}
}

func TestSessionListAPIHandlerAppliesFilters(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	traceA := filepath.Join(outputDir, "trace-a.http")
	traceB := filepath.Join(outputDir, "trace-b.http")
	contentA := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: sess-openai"},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	contentB := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/chat/completions",
		false,
		nil,
		`{"model":"gemini-pro","messages":[{"role":"user","content":"hello"}]}`,
		`{"choices":[{"message":{"content":"done"}}]}`,
	)
	if err := os.WriteFile(traceA, contentA, 0o644); err != nil {
		t.Fatalf("WriteFile(traceA) error = %v", err)
	}
	if err := os.WriteFile(traceB, contentB, 0o644); err != nil {
		t.Fatalf("WriteFile(traceB) error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?provider=openai_compatible&q=sess-openai", nil)
	rr := httptest.NewRecorder()
	sessionListAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].SessionID != "sess-openai" {
		t.Fatalf("SessionID = %q, want sess-openai", payload.Items[0].SessionID)
	}
}

func TestSessionDetailAPIHandlerReturnsTraceList(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	sessionID := "sess-window"
	content := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"X-Codex-Window-Id: " + sessionID + ":0"},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	rr := httptest.NewRecorder()
	sessionDetailAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Summary.SessionID != sessionID {
		t.Fatalf("summary session id = %q, want %q", payload.Summary.SessionID, sessionID)
	}
	if payload.Summary.SessionSource != "header.x_codex_window_id" {
		t.Fatalf("summary session source = %q", payload.Summary.SessionSource)
	}
	if len(payload.Traces) != 1 {
		t.Fatalf("len(payload.Traces) = %d, want 1", len(payload.Traces))
	}
	if payload.Traces[0].SessionID != sessionID {
		t.Fatalf("trace session id = %q, want %q", payload.Traces[0].SessionID, sessionID)
	}
}

func TestSessionDetailAPIHandlerReturnsBreakdownAndFailureCount(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	traceA := filepath.Join(outputDir, "trace-a.http")
	traceB := filepath.Join(outputDir, "trace-b.http")
	sessionID := "sess-breakdown"
	contentA := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: " + sessionID},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	contentB := buildRecordFixtureWithStatusAndHeaders(
		t,
		"/v1/chat/completions",
		false,
		"500 Internal Server Error",
		[]string{"Session_id: " + sessionID},
		`{"messages":[{"role":"user","content":"boom"}]}`,
		`{"error":"failed"}`,
	)
	if err := os.WriteFile(traceA, contentA, 0o644); err != nil {
		t.Fatalf("WriteFile(traceA) error = %v", err)
	}
	if err := os.WriteFile(traceB, contentB, 0o644); err != nil {
		t.Fatalf("WriteFile(traceB) error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	rr := httptest.NewRecorder()
	sessionDetailAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload sessionDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Breakdown.FailedTraces != 1 {
		t.Fatalf("FailedTraces = %d, want 1", payload.Breakdown.FailedTraces)
	}
	if len(payload.Breakdown.Models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(payload.Breakdown.Models))
	}
	if payload.Breakdown.Models[0].Count != 2 {
		t.Fatalf("model count = %d, want 2", payload.Breakdown.Models[0].Count)
	}
	if len(payload.Breakdown.Endpoints) != 2 {
		t.Fatalf("len(endpoints) = %d, want 2", len(payload.Breakdown.Endpoints))
	}
	if len(payload.Timeline) != 2 {
		t.Fatalf("len(timeline) = %d, want 2", len(payload.Timeline))
	}
	if payload.Timeline[0].Time.After(payload.Timeline[1].Time) {
		t.Fatalf("timeline not sorted ascending: %+v", payload.Timeline)
	}
	statusCodes := []int{payload.Timeline[0].StatusCode, payload.Timeline[1].StatusCode}
	sort.Ints(statusCodes)
	if statusCodes[0] != 200 || statusCodes[1] != 500 {
		t.Fatalf("timeline status codes = %+v, want [200 500]", statusCodes)
	}
}

func TestUpstreamListAPIHandlerReturnsRouterSnapshots(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Upstreams: []config.UpstreamTargetConfig{
			{
				ID:             "openai-primary",
				Enabled:        boolPtr(true),
				Priority:       100,
				ModelDiscovery: router.ModelDiscoveryStaticOnly,
				StaticModels:   []string{"gpt-5", "gpt-4.1"},
				Upstream: config.UpstreamConfig{
					BaseURL:        "https://api.openai.com/v1",
					ProviderPreset: "openai",
				},
			},
		},
	}
	rtr, err := router.New(cfg, nil)
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}
	if err := rtr.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams", nil)
	rr := httptest.NewRecorder()
	upstreamListAPIHandler(nil, rtr).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload upstreamListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].ID != "openai-primary" {
		t.Fatalf("ID = %q, want openai-primary", payload.Items[0].ID)
	}
	if len(payload.Items[0].Models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(payload.Items[0].Models))
	}
	if payload.Items[0].HealthState != router.HealthHealthy {
		t.Fatalf("HealthState = %q, want %q", payload.Items[0].HealthState, router.HealthHealthy)
	}
}

func TestUpstreamListAPIHandlerFallsBackToStore(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if err := st.UpsertUpstreamTarget(store.UpstreamTargetRecord{
		ID:                "openrouter-fallback",
		BaseURL:           "https://openrouter.ai/api/v1",
		ProviderPreset:    "openrouter",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          90,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     time.Date(2026, 4, 17, 8, 0, 0, 0, time.UTC),
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}
	if err := st.ReplaceUpstreamModels("openrouter-fallback", []store.UpstreamModelRecord{
		{UpstreamID: "openrouter-fallback", Model: "gpt-5", Source: "catalog", SeenAt: time.Date(2026, 4, 17, 8, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatalf("ReplaceUpstreamModels() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams", nil)
	rr := httptest.NewRecorder()
	upstreamListAPIHandler(st, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload upstreamListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	if payload.Items[0].ID != "openrouter-fallback" {
		t.Fatalf("ID = %q, want openrouter-fallback", payload.Items[0].ID)
	}
	if len(payload.Items[0].Models) != 1 || payload.Items[0].Models[0] != "gpt-5" {
		t.Fatalf("Models = %#v, want [gpt-5]", payload.Items[0].Models)
	}
}

func TestUpstreamListAPIHandlerIncludesAnalytics(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if err := st.UpsertUpstreamTarget(store.UpstreamTargetRecord{
		ID:                "openai-primary",
		BaseURL:           "https://api.openai.com/v1",
		ProviderPreset:    "openai",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          100,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     time.Date(2026, 4, 18, 8, 0, 0, 0, time.UTC),
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}
	if err := st.ReplaceUpstreamModels("openai-primary", []store.UpstreamModelRecord{
		{UpstreamID: "openai-primary", Model: "gpt-5", Source: "catalog", SeenAt: time.Date(2026, 4, 18, 8, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatalf("ReplaceUpstreamModels() error = %v", err)
	}

	logPath := filepath.Join(outputDir, "trace.http")
	if err := os.WriteFile(logPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:                      "req-1",
			Time:                           time.Date(2026, 4, 18, 8, 1, 0, 0, time.UTC),
			Model:                          "gpt-5",
			Provider:                       "openai_compatible",
			Operation:                      "responses",
			Endpoint:                       "/v1/responses",
			URL:                            "/v1/responses",
			Method:                         "POST",
			StatusCode:                     200,
			DurationMs:                     40,
			TTFTMs:                         90,
			ClientIP:                       "127.0.0.1",
			ContentLength:                  6,
			SelectedUpstreamID:             "openai-primary",
			SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
			SelectedUpstreamProviderPreset: "openai",
			RoutingPolicy:                  "p2c",
		},
		Usage: recordfile.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 6,
			TotalTokens:      16,
		},
	}
	if err := st.UpsertLog(logPath, header); err != nil {
		t.Fatalf("UpsertLog() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams", nil)
	rr := httptest.NewRecorder()
	upstreamListAPIHandler(st, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload upstreamListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	item := payload.Items[0]
	if item.RequestCount != 1 || item.SuccessRequest != 1 || item.TotalTokens != 16 {
		t.Fatalf("analytics = %+v", item)
	}
	if item.LastModel != "gpt-5" {
		t.Fatalf("LastModel = %q, want gpt-5", item.LastModel)
	}
	if len(item.RecentModels) != 1 || item.RecentModels[0] != "gpt-5" {
		t.Fatalf("RecentModels = %#v, want [gpt-5]", item.RecentModels)
	}
	if payload.Window != "24h" {
		t.Fatalf("Window = %q, want 24h", payload.Window)
	}
	if payload.Model != "" {
		t.Fatalf("Model = %q, want empty", payload.Model)
	}
}

func TestUpstreamListAPIHandlerAppliesWindowAndModelFilters(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, model string, statusCode int) {
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      name,
				Time:                           recordedAt,
				Model:                          model,
				Provider:                       "openai_compatible",
				Operation:                      "responses",
				Endpoint:                       "/v1/responses",
				URL:                            "/v1/responses",
				Method:                         "POST",
				StatusCode:                     statusCode,
				DurationMs:                     40,
				TTFTMs:                         90,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  6,
				Error:                          map[bool]string{true: "upstream overloaded", false: ""}[statusCode >= 400],
				SelectedUpstreamID:             "openai-primary",
				SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	now := time.Now().UTC()
	if err := st.UpsertUpstreamTarget(store.UpstreamTargetRecord{
		ID:                "openai-primary",
		BaseURL:           "https://api.openai.com/v1",
		ProviderPreset:    "openai",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          100,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     now,
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}
	writeLog("recent-match.http", now.Add(-30*time.Minute), "gpt-5", 503)
	writeLog("old-match.http", now.Add(-48*time.Hour), "gpt-5", 200)
	writeLog("recent-other.http", now.Add(-20*time.Minute), "gemini-2.5-flash", 200)

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams?window=1h&model=gpt-5", nil)
	rr := httptest.NewRecorder()
	upstreamListAPIHandler(st, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload upstreamListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Window != "1h" || payload.Model != "gpt-5" {
		t.Fatalf("payload filters = window:%q model:%q", payload.Window, payload.Model)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("len(payload.Items) = %d, want 1", len(payload.Items))
	}
	item := payload.Items[0]
	if item.RequestCount != 1 || item.FailedRequest != 1 {
		t.Fatalf("filtered analytics = %+v", item)
	}
	if len(item.RecentFailures) != 1 || item.RecentFailures[0].Model != "gpt-5" {
		t.Fatalf("RecentFailures = %#v, want one gpt-5 failure", item.RecentFailures)
	}
}

func TestUpstreamListAPIHandlerIncludesRoutingFailureAnalytics(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	writeLog := func(name string, recordedAt time.Time, model string, reason string) {
		t.Helper()
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:            name,
				Time:                 recordedAt,
				Model:                model,
				Provider:             "openai_compatible",
				Operation:            "responses",
				Endpoint:             "/v1/responses",
				URL:                  "/v1/responses",
				Method:               "POST",
				StatusCode:           http.StatusBadGateway,
				DurationMs:           20,
				TTFTMs:               0,
				ClientIP:             "127.0.0.1",
				ContentLength:        10,
				Error:                "selection failed",
				RoutingPolicy:        "p2c",
				RoutingFailureReason: reason,
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	now := time.Now().UTC()
	writeLog("match-a.http", now.Add(-20*time.Minute), "gpt-5", "no_supporting_target")
	writeLog("match-b.http", now.Add(-10*time.Minute), "gpt-5", "all_targets_open")
	writeLog("other-model.http", now.Add(-5*time.Minute), "gemini-2.5-flash", "no_supporting_target")

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams?window=1h&model=gpt-5", nil)
	rr := httptest.NewRecorder()
	upstreamListAPIHandler(st, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload upstreamListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.RoutingFailures.Total != 2 {
		t.Fatalf("RoutingFailures.Total = %d, want 2", payload.RoutingFailures.Total)
	}
	if len(payload.RoutingFailures.Reasons) != 2 {
		t.Fatalf("RoutingFailures.Reasons = %#v, want 2 items", payload.RoutingFailures.Reasons)
	}
	if len(payload.RoutingFailures.Recent) != 2 {
		t.Fatalf("RoutingFailures.Recent = %#v, want 2 items", payload.RoutingFailures.Recent)
	}
	if payload.RoutingFailures.Recent[0].Reason != "all_targets_open" {
		t.Fatalf("most recent reason = %q, want all_targets_open", payload.RoutingFailures.Recent[0].Reason)
	}
	if len(payload.RoutingFailures.Timeline) != 12 {
		t.Fatalf("RoutingFailures.Timeline = %#v, want 12 buckets", payload.RoutingFailures.Timeline)
	}
	totalTimeline := 0
	for _, item := range payload.RoutingFailures.Timeline {
		totalTimeline += item.Count
	}
	if totalTimeline != 2 {
		t.Fatalf("timeline total = %d, want 2", totalTimeline)
	}
}

func TestUpstreamDetailAPIHandlerReturnsBreakdownAndTraces(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	if err := st.UpsertUpstreamTarget(store.UpstreamTargetRecord{
		ID:                "openai-primary",
		BaseURL:           "https://api.openai.com/v1",
		ProviderPreset:    "openai",
		ProtocolFamily:    "openai_compatible",
		RoutingProfile:    "openai_default",
		Enabled:           true,
		Priority:          100,
		Weight:            1,
		CapacityHint:      1,
		LastRefreshAt:     time.Now().UTC(),
		LastRefreshStatus: "ready",
	}); err != nil {
		t.Fatalf("UpsertUpstreamTarget() error = %v", err)
	}

	writeLog := func(name string, recordedAt time.Time, endpoint string, model string, statusCode int, errText string) {
		t.Helper()
		path := filepath.Join(outputDir, name)
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
		header := recordfile.RecordHeader{
			Version: "LLM_PROXY_V3",
			Meta: recordfile.MetaData{
				RequestID:                      name,
				Time:                           recordedAt,
				Model:                          model,
				Provider:                       "openai_compatible",
				Operation:                      "responses",
				Endpoint:                       endpoint,
				URL:                            endpoint,
				Method:                         "POST",
				StatusCode:                     statusCode,
				DurationMs:                     44,
				TTFTMs:                         12,
				ClientIP:                       "127.0.0.1",
				ContentLength:                  7,
				Error:                          errText,
				SelectedUpstreamID:             "openai-primary",
				SelectedUpstreamBaseURL:        "https://api.openai.com/v1",
				SelectedUpstreamProviderPreset: "openai",
				RoutingPolicy:                  "p2c",
			},
		}
		if err := st.UpsertLog(path, header); err != nil {
			t.Fatalf("UpsertLog(%q) error = %v", name, err)
		}
	}

	now := time.Now().UTC()
	writeLog("match-a.http", now.Add(-20*time.Minute), "/v1/responses", "gpt-5", 200, "")
	writeLog("match-b.http", now.Add(-10*time.Minute), "/v1/chat/completions", "gpt-5", 503, "upstream overloaded")
	writeLog("other-model.http", now.Add(-5*time.Minute), "/v1/responses", "gemini-2.5-flash", 200, "")

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams/openai-primary?window=1h&model=gpt-5", nil)
	rr := httptest.NewRecorder()
	upstreamDetailAPIHandler(st, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload upstreamDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Target.ID != "openai-primary" {
		t.Fatalf("Target.ID = %q, want openai-primary", payload.Target.ID)
	}
	if payload.Window != "1h" || payload.Model != "gpt-5" {
		t.Fatalf("filters = window:%q model:%q", payload.Window, payload.Model)
	}
	if len(payload.Traces) != 2 {
		t.Fatalf("len(Traces) = %d, want 2", len(payload.Traces))
	}
	if payload.Breakdown.FailedTraces != 1 {
		t.Fatalf("FailedTraces = %d, want 1", payload.Breakdown.FailedTraces)
	}
	if len(payload.Breakdown.Models) != 1 || payload.Breakdown.Models[0].Label != "gpt-5" {
		t.Fatalf("Models = %#v, want [gpt-5]", payload.Breakdown.Models)
	}
	if len(payload.Breakdown.Endpoints) != 2 {
		t.Fatalf("Endpoints = %#v, want 2 items", payload.Breakdown.Endpoints)
	}
	if len(payload.Timeline) != 1 || payload.Timeline[0].StatusCode != 503 {
		t.Fatalf("Timeline = %#v, want one 503 failure", payload.Timeline)
	}
}

func TestUpstreamDetailAPIHandlerReturnsNotFound(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/upstreams/missing", nil)
	rr := httptest.NewRecorder()
	upstreamDetailAPIHandler(st, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestTraceDetailAPIHandlerReturnsConversationData(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"messages":[{"role":"system","content":"You are a personal assistant."},{"role":"user","content":"Inspect this request."}],"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file.","parameters":{"type":"object","properties":{"file_path":{"type":"string"}}}}}]}`
	content := buildRecordFixture(t, "/v1/chat/completions", false, reqBody, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Messages) == 0 {
		t.Fatalf("messages missing from detail payload")
	}
	if payload.Messages[0].Content != "You are a personal assistant." {
		t.Fatalf("unexpected first message content: %q", payload.Messages[0].Content)
	}
}

func TestTraceDetailAPIHandlerReturnsSessionContext(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	sessionID := "sess-trace-detail"
	content := buildRecordFixtureWithRequestHeaders(
		t,
		"/v1/responses",
		false,
		[]string{"Session_id: " + sessionID},
		`{"input":"hello"}`,
		`{"output_text":"done"}`,
	)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Session == nil {
		t.Fatalf("session missing from detail payload")
	}
	if payload.Session.SessionID != sessionID {
		t.Fatalf("SessionID = %q, want %q", payload.Session.SessionID, sessionID)
	}
	if payload.Session.SessionSource != "header.session_id" {
		t.Fatalf("SessionSource = %q, want header.session_id", payload.Session.SessionSource)
	}
}

func TestTraceRawAPIHandlerReturnsProtocolAndMeta(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	content := buildRecordFixture(t, "/v1/chat/completions", true, `{"messages":[{"role":"user","content":"hello"}]}`, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n")
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID+"/raw", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload rawDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(payload.RequestProtocol, "POST /v1/chat/completions HTTP/1.1") {
		t.Fatalf("request protocol = %q", payload.RequestProtocol)
	}
	if !strings.Contains(payload.ResponseProtocol, "HTTP/1.1 200 OK") {
		t.Fatalf("response protocol = %q", payload.ResponseProtocol)
	}
	if payload.Header.Version != "LLM_PROXY_V3" {
		t.Fatalf("header version = %q, want LLM_PROXY_V3", payload.Header.Version)
	}
	if len(payload.Events) == 0 {
		t.Fatalf("events missing from payload")
	}
}

func TestTraceRawAPIHandlerReturnsEventAttributes(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	header := buildRecordHeader("/v1/responses", true, `{"input":"hello"}`, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n")
	events := append(recordfile.BuildEvents(header), recordfile.RecordEvent{
		Type: "llm.usage",
		Attributes: map[string]interface{}{
			"total_tokens": 18,
		},
	})
	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	payload := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n" +
		`{"input":"hello"}` + "\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" +
		`data: {"type":"response.output_text.delta","delta":"hi"}` + "\n"
	if err := os.WriteFile(tracePath, append(prelude, []byte(payload)...), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID+"/raw", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payloadResp rawDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payloadResp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	found := false
	for _, event := range payloadResp.Events {
		if event["type"] == "llm.usage" {
			attrs, ok := event["attributes"].(map[string]interface{})
			if !ok {
				t.Fatalf("attributes missing on usage event: %+v", event)
			}
			if attrs["total_tokens"] != float64(18) {
				t.Fatalf("total_tokens = %#v, want 18", attrs["total_tokens"])
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("llm.usage event not found in payload: %+v", payloadResp.Events)
	}
}

func TestTraceDetailAPIHandlerFiltersNoisyDeltaEvents(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	header := buildRecordHeader("/v1/responses", true, `{"input":"hello"}`, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n")
	events := append(recordfile.BuildEvents(header),
		recordfile.RecordEvent{Type: "llm.output_text.delta", Message: "hi"},
		recordfile.RecordEvent{Type: "llm.reasoning.delta", Message: "think"},
		recordfile.RecordEvent{
			Type: "llm.tool_call.delta",
			Attributes: map[string]interface{}{
				"id":   "call_1",
				"name": "search",
			},
		},
		recordfile.RecordEvent{
			Type: "llm.usage",
			Attributes: map[string]interface{}{
				"total_tokens": 18,
			},
		},
	)
	prelude, err := recordfile.MarshalPrelude(header, events)
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	payload := "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n" +
		`{"input":"hello"}` + "\n" +
		"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" +
		`data: {"type":"response.output_text.delta","delta":"hi"}` + "\n"
	if err := os.WriteFile(tracePath, append(prelude, []byte(payload)...), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payloadResp detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payloadResp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, event := range payloadResp.Events {
		if strings.HasSuffix(event["type"].(string), ".delta") {
			t.Fatalf("noisy delta event should be filtered from detail timeline: %+v", event)
		}
	}
}

func TestTraceDetailAPIHandlerEnrichesRequestAndResponseTimeline(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"input":[{"type":"message","role":"system","content":[{"type":"input_text","text":"You are helpful."}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"why failed?"}]},{"type":"function_call","call_id":"call_hist","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},{"type":"function_call_output","call_id":"call_hist","output":"{\"cwd\":\"/tmp\"}"}]}`
	resBody := strings.Join([]string{
		"event: response.reasoning_summary_text.delta",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"inspect logs","item_id":"rs_1"}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_live","name":"exec_command"}}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":\"ls\"}","item_id":"fc_1"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"final answer"}`,
		"",
	}, "\n")
	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	var (
		requestMessage  string
		responseMessage string
		requestItems    []interface{}
		responseItems   []interface{}
	)
	for _, event := range payload.Events {
		switch event["type"] {
		case "request":
			requestMessage, _ = event["message"].(string)
			requestItems, _ = event["timeline_items"].([]interface{})
		case "response":
			responseMessage, _ = event["message"].(string)
			responseItems, _ = event["timeline_items"].([]interface{})
		}
	}

	if !strings.Contains(requestMessage, "├─ system: You are helpful.") {
		t.Fatalf("request timeline missing system message: %q", requestMessage)
	}
	if !strings.Contains(requestMessage, `function call exec_command [call_hist]: {"cmd":"pwd"}`) {
		t.Fatalf("request timeline missing tool call: %q", requestMessage)
	}
	if !strings.Contains(requestMessage, `tool response exec_command [call_hist]: {"cwd":"/tmp"}`) {
		t.Fatalf("request timeline missing tool result: %q", requestMessage)
	}
	if !strings.Contains(responseMessage, "Thinking: inspect logs") {
		t.Fatalf("response timeline missing thinking: %q", responseMessage)
	}
	if !strings.Contains(responseMessage, `tool call exec_command [call_live]: {"cmd":"ls"}`) {
		t.Fatalf("response timeline missing tool call: %q", responseMessage)
	}
	if !strings.Contains(responseMessage, "Final output: final answer") {
		t.Fatalf("response timeline missing final output: %q", responseMessage)
	}
	if len(requestItems) != 4 {
		t.Fatalf("len(request timeline_items) = %d, want 4", len(requestItems))
	}
	if len(responseItems) != 3 {
		t.Fatalf("len(response timeline_items) = %d, want 3", len(responseItems))
	}
	firstRequest, ok := requestItems[0].(map[string]interface{})
	if !ok || firstRequest["kind"] != "message" || firstRequest["role"] != "system" {
		t.Fatalf("unexpected first request timeline item: %#v", requestItems[0])
	}
	secondResponse, ok := responseItems[1].(map[string]interface{})
	if !ok || secondResponse["kind"] != "tool_call" || secondResponse["name"] != "exec_command" {
		t.Fatalf("unexpected response tool call item: %#v", responseItems[1])
	}
}

func TestTraceDetailAPIHandlerSurfacesProviderErrorBlocks(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"stream failure"}]}]}`
	resBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		`data: {"type":"response.failed","response":{"error":{"message":"stream aborted","type":"server_error","code":"stream_aborted"}}}`,
	}, "\n")
	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.AIContent != "partial" {
		t.Fatalf("AIContent = %q, want partial", payload.AIContent)
	}
	if len(payload.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(payload.AIBlocks))
	}
	if payload.AIBlocks[0].Title != "Provider Error" || !strings.Contains(payload.AIBlocks[0].Text, "stream aborted") {
		t.Fatalf("AIBlocks[0] = %+v", payload.AIBlocks[0])
	}

	var responseItems []interface{}
	for _, event := range payload.Events {
		if event["type"] == "response" {
			responseItems, _ = event["timeline_items"].([]interface{})
			break
		}
	}
	if len(responseItems) != 2 {
		t.Fatalf("len(response timeline_items) = %d, want 2", len(responseItems))
	}
	second, ok := responseItems[1].(map[string]interface{})
	if !ok || second["kind"] != "provider_error" || second["label"] != "Provider Error" {
		t.Fatalf("unexpected provider error timeline item: %#v", responseItems[1])
	}
	if body, _ := second["body"].(string); !strings.Contains(body, "stream aborted") {
		t.Fatalf("provider error body = %q, want contain stream aborted", body)
	}
}

func TestTraceDetailAPIHandlerSurfacesRefusalBlocks(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"give disallowed instructions"}]}]}`
	resBody := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"checking safety"}`,
		`data: {"type":"response.refusal.delta","delta":"I can't help with that."}`,
	}, "\n")
	content := buildRecordFixture(t, "/v1/responses", true, reqBody, resBody)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID, nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var payload detailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.AIReasoning != "checking safety" {
		t.Fatalf("AIReasoning = %q, want checking safety", payload.AIReasoning)
	}
	if len(payload.AIBlocks) != 1 {
		t.Fatalf("len(AIBlocks) = %d, want 1", len(payload.AIBlocks))
	}
	if payload.AIBlocks[0].Title != "Refusal" || !strings.Contains(payload.AIBlocks[0].Text, "can't help") {
		t.Fatalf("AIBlocks[0] = %+v", payload.AIBlocks[0])
	}

	var responseItems []interface{}
	for _, event := range payload.Events {
		if event["type"] == "response" {
			responseItems, _ = event["timeline_items"].([]interface{})
			break
		}
	}
	if len(responseItems) != 2 {
		t.Fatalf("len(response timeline_items) = %d, want 2", len(responseItems))
	}
	second, ok := responseItems[1].(map[string]interface{})
	if !ok || second["kind"] != "refusal" || second["label"] != "Refusal" {
		t.Fatalf("unexpected refusal timeline item: %#v", responseItems[1])
	}
	if body, _ := second["body"].(string); !strings.Contains(body, "can't help") {
		t.Fatalf("refusal body = %q, want contain refusal text", body)
	}
}

func TestTraceDownloadAPIHandlerSetsAttachmentDisposition(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "trace.http")
	content := buildRecordFixture(t, "/v1/chat/completions", false, `{"messages":[{"role":"user","content":"hello"}]}`, `{"choices":[{"message":{"content":"done"}}]}`)
	if err := os.WriteFile(tracePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	if err := st.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	items, err := st.ListRecent(10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/traces/"+items[0].ID+"/download", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename="trace.http"` {
		t.Fatalf("Content-Disposition = %q, want attachment download", got)
	}
}

func TestTraceAPIHandlerReturnsNotFoundForUnknownID(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/traces/missing/raw", nil)
	rr := httptest.NewRecorder()
	traceAPIHandler(st).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestListAPIHandlerReturnsStoreNotConfiguredError(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
	rr := httptest.NewRecorder()
	listAPIHandler(nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["error"] != "store not configured" {
		t.Fatalf("error = %q, want store not configured", payload["error"])
	}
}

func TestTraceDetailAPIHandlerReturnsParseErrorForInvalidCassette(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	tracePath := filepath.Join(outputDir, "broken.http")
	if err := os.WriteFile(tracePath, []byte("not a valid cassette"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	entry := store.LogEntry{
		ID:      "broken",
		LogPath: tracePath,
	}
	rr := httptest.NewRecorder()
	handleTraceDetail(rr, tracePath, entry)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !strings.Contains(payload["error"], "parse error:") {
		t.Fatalf("error = %q, want parse error prefix", payload["error"])
	}
}

func TestTraceRawAPIHandlerReturnsFileNotFoundError(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	handleTraceRaw(rr, filepath.Join(t.TempDir(), "missing.http"), store.LogEntry{ID: "missing"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["error"] != "file not found" {
		t.Fatalf("error = %q, want file not found", payload["error"])
	}
}

func buildRecordHeader(url string, isStream bool, reqBody string, resBody string) recordfile.RecordHeader {
	reqHeader := "POST " + url + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	resHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	if isStream {
		resHeader = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
	}
	return recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req_1",
			Time:          time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5.1-codex",
			URL:           url,
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    100,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: int64(len(resBody)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHeader)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHeader)),
			ResBodyLen:   int64(len(resBody)),
			IsStream:     isStream,
		},
	}
}

func buildRecordFixtureWithRequestHeaders(t *testing.T, url string, isStream bool, extraHeaders []string, reqBody string, resBody string) []byte {
	t.Helper()
	return buildRecordFixtureWithStatusAndHeaders(t, url, isStream, "200 OK", extraHeaders, reqBody, resBody)
}

func buildRecordFixtureWithStatusAndHeaders(t *testing.T, url string, isStream bool, status string, extraHeaders []string, reqBody string, resBody string) []byte {
	t.Helper()

	requestHeaderLines := []string{
		"POST " + url + " HTTP/1.1",
		"Host: example.com",
		"Content-Type: application/json",
	}
	requestHeaderLines = append(requestHeaderLines, extraHeaders...)
	request := strings.Join(requestHeaderLines, "\r\n") + "\r\n\r\n" + reqBody
	responseHeader := "HTTP/1.1 " + status + "\r\nContent-Type: application/json\r\n\r\n"
	if isStream {
		responseHeader = "HTTP/1.1 " + status + "\r\nContent-Type: text/event-stream\r\n\r\n"
	}
	header := buildRecordHeader(url, isStream, reqBody, resBody)
	header.Meta.StatusCode = parseStatusCode(status)
	header.Layout.ReqHeaderLen = int64(len(strings.Join(requestHeaderLines, "\r\n") + "\r\n\r\n"))
	header.Layout.ResHeaderLen = int64(len(responseHeader))
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	payload := request + "\n" + responseHeader + resBody
	return append(prelude, []byte(payload)...)
}

func parseStatusCode(status string) int {
	parts := strings.SplitN(status, " ", 2)
	if len(parts) == 0 {
		return 200
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return 200
	}
	return code
}
