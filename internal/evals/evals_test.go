package evals

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if len(results) != 7 {
		t.Fatalf("len(EvaluateBaseline()) = %d, want 7", len(results))
	}
	assertResultStatus(t, results, "ttft_le_2000ms", "pass")
	assertResultStatus(t, results, "total_tokens_le_32000", "pass")
	assertResultStatus(t, results, "tool_calls_declared", "fail")
	assertResultStatus(t, results, "tool_call_arguments_json", "fail")
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

func TestEvaluateSupportsVersionedProfiles(t *testing.T) {
	entry := store.LogEntry{
		Header: recordfile.RecordHeader{
			Meta:  recordfile.MetaData{StatusCode: 200, TTFTMs: 5000},
			Usage: recordfile.UsageInfo{TotalTokens: 999999},
		},
	}

	v1, err := Evaluate(entry, &replay.Summary{BodyBytes: 1}, "baseline_v1")
	if err != nil {
		t.Fatalf("Evaluate(baseline_v1) error = %v", err)
	}
	if len(v1) != 3 {
		t.Fatalf("len(Evaluate(baseline_v1)) = %d, want 3", len(v1))
	}

	v3, err := Evaluate(entry, &replay.Summary{BodyBytes: 1}, "baseline_v3")
	if err != nil {
		t.Fatalf("Evaluate(baseline_v3) error = %v", err)
	}
	if len(v3) != 6 {
		t.Fatalf("len(Evaluate(baseline_v3)) = %d, want 6", len(v3))
	}

	v4, err := Evaluate(entry, &replay.Summary{BodyBytes: 1}, "baseline_v4")
	if err != nil {
		t.Fatalf("Evaluate(baseline_v4) error = %v", err)
	}
	if len(v4) != 7 {
		t.Fatalf("len(Evaluate(baseline_v4)) = %d, want 7", len(v4))
	}

	if _, err := Evaluate(entry, &replay.Summary{BodyBytes: 1}, "missing_profile"); err == nil {
		t.Fatalf("Evaluate(missing_profile) error = nil, want error")
	}
}

func TestToolCallsDeclaredConformance(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "declared.http")
	badPath := filepath.Join(dir, "undeclared.http")

	if err := os.WriteFile(goodPath, buildToolCallRecordFixture(t, "weather", "weather"), 0o644); err != nil {
		t.Fatalf("WriteFile(goodPath) error = %v", err)
	}
	if err := os.WriteFile(badPath, buildToolCallRecordFixture(t, "weather", "search"), 0o644); err != nil {
		t.Fatalf("WriteFile(badPath) error = %v", err)
	}

	good := toolCallsDeclared(store.LogEntry{LogPath: goodPath})
	if good.Status != "pass" {
		t.Fatalf("toolCallsDeclared(good) = %#v, want pass", good)
	}
	bad := toolCallsDeclared(store.LogEntry{LogPath: badPath})
	if bad.Status != "fail" || !strings.Contains(bad.Explanation, "search") {
		t.Fatalf("toolCallsDeclared(bad) = %#v, want fail mentioning search", bad)
	}
}

func TestToolCallArgumentsJSONConformance(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "args-valid.http")
	badPath := filepath.Join(dir, "args-invalid.http")

	if err := os.WriteFile(goodPath, buildToolCallRecordFixtureWithArgs(t, "weather", "weather", `{"city":"Shanghai"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(goodPath) error = %v", err)
	}
	if err := os.WriteFile(badPath, buildToolCallRecordFixtureWithArgs(t, "weather", "weather", `{"city":"Shanghai"`), 0o644); err != nil {
		t.Fatalf("WriteFile(badPath) error = %v", err)
	}

	good := toolCallArgumentsJSON(store.LogEntry{LogPath: goodPath})
	if good.Status != "pass" {
		t.Fatalf("toolCallArgumentsJSON(good) = %#v, want pass", good)
	}
	bad := toolCallArgumentsJSON(store.LogEntry{LogPath: badPath})
	if bad.Status != "fail" || !strings.Contains(bad.Explanation, "weather") {
		t.Fatalf("toolCallArgumentsJSON(bad) = %#v, want fail mentioning weather", bad)
	}
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

func buildToolCallRecordFixture(t *testing.T, declaredTool string, calledTool string) []byte {
	t.Helper()
	return buildToolCallRecordFixtureWithArgs(t, declaredTool, calledTool, `{"city":"Shanghai"}`)
}

func buildToolCallRecordFixtureWithArgs(t *testing.T, declaredTool string, calledTool string, args string) []byte {
	t.Helper()
	reqBody := `{"model":"gpt-5","messages":[{"role":"user","content":"check weather"}],"tools":[{"type":"function","function":{"name":"` + declaredTool + `","description":"lookup","parameters":{"type":"object"}}}]}`
	resBody := `{"id":"chatcmpl_1","object":"chat.completion","created":1710000000,"model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + calledTool + `","arguments":` + strconv.Quote(args) + `}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	request := "POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n" + reqBody
	response := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n" + resBody

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:     "req-tool",
			Time:          time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-5",
			Provider:      "openai_compatible",
			Operation:     "chat.completions",
			Endpoint:      "/v1/chat/completions",
			URL:           "/v1/chat/completions",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    100,
			TTFTMs:        10,
			ClientIP:      "127.0.0.1",
			ContentLength: int64(len(resBody)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len("POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n")),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")),
			ResBodyLen:   int64(len(resBody)),
		},
		Usage: recordfile.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}
	return append(prelude, []byte(request+"\n"+response)...)
}
