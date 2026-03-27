package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestHandlerResponsesUsageEndToEnd(t *testing.T) {
	tests := []struct {
		name                 string
		requestBody          string
		responseContentType  string
		responseBody         string
		wantPromptTokens     int
		wantCompletionTokens int
		wantTotalTokens      int
		wantIsStream         bool
	}{
		{
			name:                "stream_response_completed_event",
			requestBody:         `{"model":"gpt-5.1-codex","stream":true}`,
			responseContentType: "text/event-stream",
			responseBody: "event: response.created\n" +
				"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"event: response.output_text.delta\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
				"event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":7048,\"output_tokens\":28,\"total_tokens\":7076}}}\n\n",
			wantPromptTokens:     7048,
			wantCompletionTokens: 28,
			wantTotalTokens:      7076,
			wantIsStream:         true,
		},
		{
			name:                 "non_stream_top_level_usage",
			requestBody:          `{"model":"gpt-5.1-codex","stream":false}`,
			responseContentType:  "application/json",
			responseBody:         `{"id":"resp_2","object":"response","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			wantPromptTokens:     11,
			wantCompletionTokens: 7,
			wantTotalTokens:      18,
			wantIsStream:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			st, err := store.New(outputDir)
			if err != nil {
				t.Fatalf("store.New() error = %v", err)
			}
			defer st.Close()

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", tt.responseContentType)
				_, _ = io.WriteString(w, tt.responseBody)
			}))
			defer upstream.Close()

			cfg := &config.Config{}
			cfg.Upstream.BaseURL = upstream.URL
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}

			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/responses", bytes.NewBufferString(tt.requestBody))
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")

			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("resp.StatusCode = %d, want 200", resp.StatusCode)
			}

			recordPath := findRecordedHTTP(t, outputDir)
			parsed, err := waitForRecordedPrelude(recordPath, time.Second)
			if err != nil {
				t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
			}

			got := parsed.Header.Usage
			if got.PromptTokens != tt.wantPromptTokens || got.CompletionTokens != tt.wantCompletionTokens || got.TotalTokens != tt.wantTotalTokens {
				t.Fatalf("recorded usage = %+v, want prompt=%d completion=%d total=%d", got, tt.wantPromptTokens, tt.wantCompletionTokens, tt.wantTotalTokens)
			}
			if parsed.Header.Meta.URL != "/v1/responses" {
				t.Fatalf("recorded URL = %q, want /v1/responses", parsed.Header.Meta.URL)
			}
			if parsed.Header.Layout.IsStream != tt.wantIsStream {
				t.Fatalf("recorded IsStream = %v, want %v", parsed.Header.Layout.IsStream, tt.wantIsStream)
			}

			entries, err := waitForRecentEntries(st, 1, time.Second)
			if err != nil {
				t.Fatalf("waitForRecentEntries() error = %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("ListRecent() len = %d, want 1", len(entries))
			}
			if entries[0].Header.Usage.TotalTokens != tt.wantTotalTokens {
				t.Fatalf("indexed total tokens = %d, want %d", entries[0].Header.Usage.TotalTokens, tt.wantTotalTokens)
			}
		})
	}
}

func waitForRecentEntries(st *store.Store, limit int, timeout time.Duration) ([]store.LogEntry, error) {
	deadline := time.Now().Add(timeout)
	var lastEntries []store.LogEntry
	var lastErr error

	for {
		lastEntries, lastErr = st.ListRecent(limit)
		if lastErr == nil && len(lastEntries) >= limit {
			return lastEntries, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, lastErr
			}
			return lastEntries, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForRecordedPrelude(path string, timeout time.Duration) (*recordfile.ParsedPrelude, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for {
		content, err := os.ReadFile(path)
		if err == nil {
			parsed, parseErr := recordfile.ParsePrelude(content)
			if parseErr == nil {
				return parsed, nil
			}
			lastErr = parseErr
		} else {
			lastErr = err
		}

		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("timed out waiting for parsable recorded prelude")
			}
			return nil, lastErr
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func findRecordedHTTP(t *testing.T, root string) string {
	t.Helper()

	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".http" {
			return nil
		}
		found = path
		return filepath.SkipAll
	})
	if err != nil {
		t.Fatalf("Walk(%q) error = %v", root, err)
	}
	if found == "" {
		t.Fatalf("no recorded .http file found under %q", root)
	}
	return found
}
