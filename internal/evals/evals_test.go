package evals

import (
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/kingfs/llm-tracelab/pkg/replay"
)

func TestEvaluateBaselineIncludesLatencyAndTokenBudgetChecks(t *testing.T) {
	entry := store.LogEntry{
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				StatusCode: 200,
				TTFTMs:     150,
				Time:       time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			},
			Usage: recordfile.UsageInfo{
				TotalTokens: 128,
			},
		},
	}
	summary := &replay.Summary{BodyBytes: 32}

	results := EvaluateBaseline(entry, summary)
	if len(results) != 5 {
		t.Fatalf("len(EvaluateBaseline()) = %d, want 5", len(results))
	}
	assertResultStatus(t, results, "ttft_le_2000ms", "pass")
	assertResultStatus(t, results, "total_tokens_le_32000", "pass")
}

func TestEvaluateBaselineFailsBudgetChecksWhenExceeded(t *testing.T) {
	entry := store.LogEntry{
		Header: recordfile.RecordHeader{
			Meta: recordfile.MetaData{
				StatusCode: 200,
				TTFTMs:     2501,
			},
			Usage: recordfile.UsageInfo{
				TotalTokens: 32001,
			},
		},
	}

	results := EvaluateBaseline(entry, &replay.Summary{BodyBytes: 1})
	assertResultStatus(t, results, "ttft_le_2000ms", "fail")
	assertResultStatus(t, results, "total_tokens_le_32000", "fail")
}

func assertResultStatus(t *testing.T, results []Result, evaluatorKey string, want string) {
	t.Helper()
	for _, result := range results {
		if result.EvaluatorKey == evaluatorKey {
			if result.Status != want {
				t.Fatalf("%s status = %q, want %q", evaluatorKey, result.Status, want)
			}
			return
		}
	}
	t.Fatalf("missing evaluator %q in %#v", evaluatorKey, results)
}
