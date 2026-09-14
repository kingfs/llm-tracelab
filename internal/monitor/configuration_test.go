package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
)

func configurationFixture(t *testing.T) (*store.Store, *router.Router, *channel.Service) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.UpsertChannelConfig(store.ChannelConfigRecord{ID: "primary", Name: "Primary", Enabled: true, BaseURL: "https://example.invalid/v1", ProviderPreset: "openai", ModelDiscovery: "list_models", AllowUnknownModels: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"one", "two"} {
		if _, err := st.UpsertChannelModel("primary", store.ChannelModelRecord{Model: model, Enabled: true, Source: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	svc := channel.NewService(st)
	rtr, err := router.New(&config.Config{}, st)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := svc.RuntimeTargets()
	if err != nil {
		t.Fatal(err)
	}
	if err := rtr.Reload(targets); err != nil {
		t.Fatal(err)
	}
	return st, rtr, svc
}

func configurationRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
	return response
}

func TestConfigurationMutationRollsBackOnInvalidRouter(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	response := configurationRequest(channelDetailAPIHandler(st, rtr, svc), http.MethodPatch, "/api/channels/primary", `{"base_url":"://invalid"}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	saved, err := st.GetChannelConfig("primary")
	if err != nil {
		t.Fatal(err)
	}
	if saved.BaseURL != "https://example.invalid/v1" {
		t.Fatalf("failed update persisted: %s", saved.BaseURL)
	}
	if got := rtr.AggregatedModels(); len(got) != 2 {
		t.Fatalf("runtime changed: %v", got)
	}
}

func TestConfigurationBatchFailureIsAtomic(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	response := configurationRequest(channelDetailAPIHandler(st, rtr, svc), http.MethodPatch, "/api/channels/primary/models/batch", `{"models":["one","missing"],"enabled":false}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	models, err := st.ListChannelModels("primary", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("partial batch persisted: %v", models)
	}
	if got := rtr.AggregatedModels(); len(got) != 2 {
		t.Fatalf("runtime changed: %v", got)
	}
}

func TestConfigurationDisableAndReenablePreservesModelChoices(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	h := channelDetailAPIHandler(st, rtr, svc)
	for _, op := range []struct{ path, body string }{{"/api/channels/primary/models/one", `{"enabled":false}`}, {"/api/channels/primary", `{"enabled":false}`}} {
		rr := configurationRequest(h, http.MethodPatch, op.path, op.body)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
		}
	}
	if len(rtr.Targets()) != 0 {
		t.Fatal("disabled provider remains in runtime")
	}
	rr := configurationRequest(h, http.MethodPatch, "/api/channels/primary", `{"enabled":true}`)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	if got := rtr.AggregatedModels(); len(got) != 1 || got[0] != "two" {
		t.Fatalf("model choices changed: %v", got)
	}
	if _, err := rtr.Select(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"one"}`))); err == nil {
		t.Fatal("disabled model admitted through unknown-model fallback")
	}
}

func TestConfigurationConcurrentMutationsKeepLatestSnapshot(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	h := channelDetailAPIHandler(st, rtr, svc)
	var wg sync.WaitGroup
	for _, model := range []string{"one", "two"} {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			rr := configurationRequest(h, http.MethodPatch, "/api/channels/primary/models/"+model, `{"enabled":false}`)
			if rr.Code != http.StatusOK {
				t.Errorf("status=%d body=%s", rr.Code, rr.Body)
			}
		}(model)
	}
	wg.Wait()
	if got := rtr.AggregatedModels(); len(got) != 0 {
		t.Fatalf("stale snapshot: %v", got)
	}
}

func TestYAMLCredentialsRejectDatabaseMutation(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	rr := configurationRequest(channelDetailAPIHandler(st, rtr, svc.WithReadOnly(true)), http.MethodPatch, "/api/channels/primary", `{"enabled":false}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	record, err := st.GetChannelConfig("primary")
	if err != nil {
		t.Fatal(err)
	}
	if !record.Enabled || len(rtr.Targets()) != 1 {
		t.Fatal("YAML runtime changed through database management")
	}
}

func TestModelAliasMutationPublishesRuntimeAndRollsBack(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, st, RouteOptions{Router: rtr, ChannelService: svc})
	rr := configurationRequest(mux, http.MethodPost, "/api/model-aliases", `{"id":"alias-one","alias":"friendly","target_model":"one","channel_id":"primary","enabled":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	if rtr.Targets()[0].ResolveModelAlias("friendly") != "one" {
		t.Fatal("alias was saved without publishing runtime")
	}
	// A bad active configuration forces preparation failure after the raw SQL write.
	record, err := st.GetChannelConfig("primary")
	if err != nil {
		t.Fatal(err)
	}
	record.BaseURL = "://invalid"
	if _, err := st.UpsertChannelConfig(record); err != nil {
		t.Fatal(err)
	}
	rr = configurationRequest(mux, http.MethodPatch, "/api/model-aliases/alias-one", `{"enabled":false}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	alias, err := st.GetModelAlias("alias-one")
	if err != nil {
		t.Fatal(err)
	}
	if !alias.Enabled || rtr.Targets()[0].ResolveModelAlias("friendly") != "one" {
		t.Fatal("failed alias update changed database or runtime")
	}
}

func TestDeleteChannelRollbackRestoresItsModels(t *testing.T) {
	st, rtr, svc := configurationFixture(t)
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{ID: "broken", Name: "Broken", Enabled: true, BaseURL: "://invalid"}); err != nil {
		t.Fatal(err)
	}
	rr := configurationRequest(channelDetailAPIHandler(st, rtr, svc), http.MethodDelete, "/api/channels/primary", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	if _, err := st.GetChannelConfig("primary"); err != nil {
		t.Fatal("nested delete transaction escaped rollback:", err)
	}
	models, err := st.ListChannelModels("primary", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models not restored: %v", models)
	}
}

func TestChannelBootstrapSettingsReportAndResetTheMarker(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := channel.NewService(st)
	mux := http.NewServeMux()
	RegisterRoutes(mux, st, RouteOptions{ChannelService: svc})

	rr := configurationRequest(mux, http.MethodGet, "/api/settings/channels", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"initialized":false`) {
		t.Fatalf("GET on a fresh database status=%d body=%s", rr.Code, rr.Body)
	}

	if err := svc.MarkConfigurationInitialized(); err != nil {
		t.Fatal(err)
	}
	rr = configurationRequest(mux, http.MethodGet, "/api/settings/channels", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"initialized":true`) {
		t.Fatalf("GET after marking status=%d body=%s", rr.Code, rr.Body)
	}

	rr = configurationRequest(mux, http.MethodDelete, "/api/settings/channels", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"initialized":false`) {
		t.Fatalf("DELETE status=%d body=%s", rr.Code, rr.Body)
	}
	if initialized, err := svc.HasConfiguration(); err != nil || initialized {
		t.Fatalf("marker survived the reset: initialized=%v err=%v", initialized, err)
	}

	rr = configurationRequest(mux, http.MethodPost, "/api/settings/channels", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("POST status=%d body=%s, want 404", rr.Code, rr.Body)
	}
}
