package sessionanalysis

import (
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestBuildSessionAnalysis(t *testing.T) {
	summary := store.SessionSummary{
		SessionID:    "sess-1",
		RequestCount: 2,
		SuccessRate:  0.5,
		TotalTokens:  30,
		AvgTTFT:      12,
	}
	traces := []store.LogEntry{
		trace("trace-1", "gpt-5", "openai_compatible", "/v1/responses", 200),
		trace("trace-2", "claude", "anthropic", "/v1/messages", 500),
	}
	findings := map[string][]observe.Finding{
		"trace-1": {{ID: "finding-1", Category: "credential_leak", Severity: observe.SeverityHigh, EvidencePath: "trace#trace-1#node#n1", NodeID: "n1"}},
		"trace-2": {{ID: "finding-2", Category: "credential_leak", Severity: observe.SeverityHigh, EvidencePath: "trace#trace-2#node#n2", NodeID: "n2"}},
	}

	out := Build(summary, traces, findings)
	if out.SessionID != "sess-1" || len(out.TraceRefs) != 2 || len(out.FindingRefs) != 2 {
		t.Fatalf("output = %+v", out)
	}
	if len(out.RepeatedFindings) != 1 || out.RepeatedFindings[0].Category != "credential_leak" {
		t.Fatalf("repeated findings = %+v", out.RepeatedFindings)
	}
	if _, err := Marshal(out); err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
}

func trace(id string, model string, provider string, endpoint string, status int) store.LogEntry {
	return store.LogEntry{
		ID: id,
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				Time:       time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
				Model:      model,
				Provider:   provider,
				Endpoint:   endpoint,
				StatusCode: status,
			},
		},
	}
}
