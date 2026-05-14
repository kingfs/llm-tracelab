package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	outputDir, err := os.MkdirTemp("", "llm-tracelab-monitor-e2e-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outputDir)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-5", "object": "model"},
				{"id": "gpt-4.1", "object": "model"},
				{"id": "gpt-new-real", "object": "model"},
			},
		})
	}))
	defer upstream.Close()

	st, err := store.New(outputDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := seedStore(outputDir, st, upstream.URL); err != nil {
		return err
	}
	routedTrace, err := st.GetByRequestID("trace-routed")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/__fixture/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"routed_trace_id": routedTrace.ID,
		})
	})
	monitor.RegisterRoutes(mux, st, monitor.RouteOptions{ChannelService: channel.NewService(st)})

	addr := strings.TrimSpace(os.Getenv("MONITOR_REAL_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:4183"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	fmt.Printf("MONITOR_REAL_SERVER_URL=http://%s\n", listener.Addr().String())
	fmt.Println("MONITOR_REAL_SERVER_READY")

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-context.Background().Done():
		return server.Shutdown(context.Background())
	}
}

func seedStore(outputDir string, st *store.Store, upstreamURL string) error {
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:                 "openai-primary",
		Name:               "OpenAI Primary",
		BaseURL:            upstreamURL + "/v1",
		ProviderPreset:     "openai",
		APIKeyCiphertext:   []byte("sk-e2e-secret"),
		APIKeyHint:         "sk-...cret",
		HeadersJSON:        `{"Authorization":"Bearer hidden","X-Test":"visible"}`,
		Enabled:            true,
		Priority:           100,
		Weight:             1,
		CapacityHint:       1,
		ModelDiscovery:     "list_models",
		AllowUnknownModels: true,
	}); err != nil {
		return err
	}
	if err := st.ReplaceChannelModels("openai-primary", []store.ChannelModelRecord{
		{Model: "gpt-5", DisplayName: "gpt-5", Source: "manual", Enabled: true},
		{Model: "gpt-4.1", DisplayName: "gpt-4.1", Source: "manual", Enabled: true},
	}); err != nil {
		return err
	}
	if _, err := st.UpsertChannelConfig(store.ChannelConfigRecord{
		ID:             "broken-auth",
		Name:           "Broken Auth",
		BaseURL:        upstreamURL + "/v1/missing",
		ProviderPreset: "openai",
		Enabled:        true,
		ModelDiscovery: "list_models",
	}); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := writeTrace(outputDir, st, recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:                      "trace-routed",
			Time:                           now.Add(-1 * time.Hour),
			Model:                          "gpt-5",
			Provider:                       "openai",
			Operation:                      "responses.create",
			Endpoint:                       "/v1/responses",
			URL:                            "/v1/responses",
			Method:                         "POST",
			StatusCode:                     200,
			DurationMs:                     1200,
			TTFTMs:                         120,
			SelectedUpstreamID:             "openai-primary",
			SelectedUpstreamBaseURL:        upstreamURL + "/v1",
			SelectedUpstreamProviderPreset: "openai",
			RoutingPolicy:                  "p2c",
			RoutingScore:                   0.82,
			RoutingCandidateCount:          2,
		},
		Usage:  recordfile.UsageInfo{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		Layout: traceLayout(`/v1/responses`, `{"model":"gpt-5","input":"hello"}`, `{"output_text":"done"}`, false),
	}, `{"model":"gpt-5","input":"hello"}`, `{"output_text":"done"}`, false); err != nil {
		return err
	}
	return writeTrace(outputDir, st, recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:                      "trace-failed",
			Time:                           now.Add(-30 * time.Minute),
			Model:                          "gpt-5",
			Provider:                       "openai",
			Operation:                      "responses.create",
			Endpoint:                       "/v1/responses",
			URL:                            "/v1/responses",
			Method:                         "POST",
			StatusCode:                     429,
			DurationMs:                     300,
			TTFTMs:                         0,
			Error:                          "rate limited",
			SelectedUpstreamID:             "openai-primary",
			SelectedUpstreamBaseURL:        upstreamURL + "/v1",
			SelectedUpstreamProviderPreset: "openai",
			RoutingPolicy:                  "p2c",
			RoutingScore:                   0.5,
			RoutingCandidateCount:          2,
		},
		Usage:  recordfile.UsageInfo{PromptTokens: 80, CompletionTokens: 0, TotalTokens: 80},
		Layout: traceLayout(`/v1/responses`, `{"model":"gpt-5","input":"again"}`, `{"error":{"message":"rate limited"}}`, false),
	}, `{"model":"gpt-5","input":"again"}`, `{"error":{"message":"rate limited"}}`, false)
}

func writeTrace(outputDir string, st *store.Store, header recordfile.RecordHeader, reqBody string, resBody string, stream bool) error {
	path := filepath.Join(outputDir, header.Meta.RequestID+".http")
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		return err
	}
	responseHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	if header.Meta.StatusCode >= 400 {
		responseHeader = fmt.Sprintf("HTTP/1.1 %d Error\r\nContent-Type: application/json\r\n\r\n", header.Meta.StatusCode)
	}
	if stream {
		responseHeader = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
	}
	requestHeader := "POST " + header.Meta.URL + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	content := append(prelude, []byte(requestHeader+reqBody+"\n"+responseHeader+resBody)...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	return st.UpsertLog(path, header)
}

func traceLayout(url string, reqBody string, resBody string, stream bool) recordfile.LayoutInfo {
	reqHeader := "POST " + url + " HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n"
	resHeader := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"
	if stream {
		resHeader = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
	}
	return recordfile.LayoutInfo{
		ReqHeaderLen: int64(len(reqHeader)),
		ReqBodyLen:   int64(len(reqBody)),
		ResHeaderLen: int64(len(resHeader)),
		ResBodyLen:   int64(len(resBody)),
		IsStream:     stream,
	}
}

func init() {
	log.SetFlags(0)
	log.SetPrefix(strings.TrimSpace(os.Args[0]) + ": ")
}
