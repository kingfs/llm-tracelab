package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
)

type configurationHandlerFactory func(*store.Store, *router.Router, *channel.Service) http.HandlerFunc

// Buffer the small JSON response until both persistence and runtime publication
// succeed. Existing handlers operate on the transaction, with reload deferred.
func configurationAPIHandler(st *store.Store, rtr *router.Router, svc *channel.Service, factory configurationHandlerFactory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || strings.HasSuffix(r.URL.Path, "/validate") {
			factory(st, rtr, svc)(w, r)
			return
		}
		svc := effectiveChannelService(st, svc)
		if svc.ReadOnly() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "upstreams are managed by YAML credentials; edit YAML and restart instead"})
			return
		}
		response := &configurationResponse{header: make(http.Header)}
		rejected := errors.New("configuration request rejected")
		err := st.ConfigurationTransaction(r.Context(), func(tx *store.Store, commit func() error) error {
			txService := svc.WithStore(tx)
			factory(tx, nil, txService)(response, r)
			if response.status >= http.StatusBadRequest {
				// Failed discovery keeps its diagnostic run, but never changes routing.
				if response.status == http.StatusBadGateway && strings.HasSuffix(r.URL.Path, "/probe") {
					var result channelProbeResponse
					if json.Unmarshal(response.body.Bytes(), &result) == nil && result.Status == "failed" {
						return commit()
					}
				}
				return rejected
			}
			if err := txService.MarkConfigurationInitialized(); err != nil {
				return err
			}
			targets, err := txService.RuntimeTargets()
			if err != nil {
				return err
			}
			if rtr == nil {
				prepared, err := router.New(&config.Config{}, nil)
				if err != nil {
					return err
				}
				return prepared.ReloadWithCommit(targets, tx, commit)
			}
			return rtr.ReloadWithCommit(targets, tx, commit)
		})
		if err != nil && !errors.Is(err, rejected) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "configuration not applied: " + err.Error()})
			return
		}
		for key, values := range response.header {
			w.Header()[key] = values
		}
		if response.status != 0 {
			w.WriteHeader(response.status)
		}
		_, _ = w.Write(response.body.Bytes())
	}
}

type configurationResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *configurationResponse) Header() http.Header { return w.header }
func (w *configurationResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *configurationResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}
func channelListCreateAPIHandler(st *store.Store, rtr *router.Router, svc *channel.Service) http.HandlerFunc {
	return configurationAPIHandler(st, rtr, svc, channelListCreateAPIHandlerUncommitted)
}

func channelDetailAPIHandler(st *store.Store, rtr *router.Router, svc *channel.Service) http.HandlerFunc {
	return configurationAPIHandler(st, rtr, svc, channelDetailAPIHandlerUncommitted)
}

func providerSetupAPIHandler(st *store.Store, rtr *router.Router, svc *channel.Service) http.HandlerFunc {
	return configurationAPIHandler(st, rtr, svc, providerSetupAPIHandlerUncommitted)
}

func providerProbeReportApplyAPIHandler(st *store.Store, rtr *router.Router, svc *channel.Service) http.HandlerFunc {
	return configurationAPIHandler(st, rtr, svc, providerProbeReportApplyAPIHandlerUncommitted)
}
