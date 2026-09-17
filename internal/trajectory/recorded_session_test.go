package trajectory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

// Optional local regression runner. Private cassettes never enter the repository.
// Manifest entries contain path and trace_id; output is a separate JSONL file.
func TestRecordedSessionATIF(t *testing.T) {
	manifest := os.Getenv("ATIF_CASSETTE_MANIFEST")
	if manifest == "" {
		t.Skip("set ATIF_CASSETTE_MANIFEST to validate a recorded session")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Path    string `json:"path"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	var exchanges []Exchange
	var sessionID string
	for _, entry := range entries {
		raw, err := os.ReadFile(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		prelude, err := recordfile.ParsePrelude(raw)
		if err != nil {
			t.Fatal(err)
		}
		requestFull, requestBody, _, responseBody := recordfile.ExtractSections(raw, prelude)
		h := prelude.Header
		ex := Exchange{TraceID: entry.TraceID, Time: h.Meta.Time, DurationMs: h.Meta.DurationMs, Model: h.Meta.Model, Endpoint: h.Meta.Endpoint, StatusCode: h.Meta.StatusCode, Request: requestBody, Response: responseBody, Stream: h.Layout.IsStream, ExchangeKind: h.Meta.ExchangeKind}
		request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(requestFull)))
		if err != nil {
			t.Fatal(err)
		}
		id := request.Header.Get("Session-Id")
		if sessionID == "" {
			sessionID = id
		} else if id != sessionID {
			t.Fatal("manifest contains multiple sessions")
		}
		ex.UserAgent = request.UserAgent()
		ex.Originator = request.Header.Get("Originator")
		_ = request.Body.Close()
		exchanges = append(exchanges, ex)
	}
	result := build(t, exchanges...)
	result.SessionID = sessionID
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if output := os.Getenv("ATIF_SESSION_OUTPUT"); output != "" {
		if err := os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	responses, calls, results := 0, 0, 0
	for _, step := range result.Steps {
		if step.Extra["origin"] == "response" {
			responses++
		}
		calls += len(step.ToolCalls)
		if step.Observation != nil {
			results += len(step.Observation.Results)
		}
	}
	t.Logf("requests=%d response_steps=%d steps=%d tools=%d results=%d bytes=%d warnings=%v", len(entries), responses, len(result.Steps), calls, results, len(encoded)+1, result.Extra["warnings"])
	if responses != len(entries) {
		t.Fatalf("missing recorded response steps: %d / %d", responses, len(entries))
	}
}
