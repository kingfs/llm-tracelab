package trajectory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

func exchange(id, req, res string) Exchange {
	return Exchange{TraceID: id, Time: time.Unix(100, 0), Model: "test-model", Endpoint: "/v1/responses", StatusCode: 200, Request: []byte(req), Response: []byte(res)}
}
func build(t *testing.T, ex ...Exchange) Trajectory {
	t.Helper()
	result, err := Build(context.Background(), "session", "header.x_codex_turn_metadata.session_id", ex)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	schemaData, err := os.ReadFile("testdata/atif.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("ATIF schema validation: %v", err)
	}
	for i, s := range result.Steps {
		if s.StepID != i+1 {
			t.Fatalf("nonsequential step %+v", s)
		}
		if s.Source != "agent" && (len(s.ToolCalls) > 0 || s.ModelName != "") {
			t.Fatalf("agent fields on non-agent %+v", s)
		}
		if s.Observation != nil {
			for _, r := range s.Observation.Results {
				if r.SourceCallID != "" {
					found := false
					for _, call := range s.ToolCalls {
						found = found || call.ToolCallID == r.SourceCallID
					}
					if !found {
						t.Fatal("result references another step")
					}
				}
			}
		}
	}
	return result
}
func hasWarning(result Trajectory, code string) bool {
	for _, w := range result.Extra["warnings"].([]Warning) {
		if w.Code == code {
			return true
		}
	}
	return false
}
func TestBuildCumulativeHistoryAndHumanCorrection(t *testing.T) {
	first := exchange("a", `{"model":"m1","instructions":"rules","input":[{"role":"user","content":"fix"}]}`, `{"status":"completed","output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"shell","arguments":"{\"cmd\":\"test\"}"}],"usage":{"input_tokens":10}}`)
	second := exchange("b", `{"model":"m1","instructions":"rules","input":[{"role":"user","content":"fix"},{"type":"function_call","call_id":"call1","name":"shell","arguments":"{\"cmd\":\"test\"}"},{"type":"function_call_output","call_id":"call1","output":"failed"},{"role":"user","content":"try another way"}]}`, `{"status":"completed","output":[{"type":"message","id":"msg1","role":"assistant","content":[{"type":"output_text","text":"fixed"}]}]}`)
	result := build(t, second, first) // deliberately out of order
	if len(result.Steps) != 5 {
		t.Fatalf("steps=%+v", result.Steps)
	}
	call := result.Steps[2]
	if call.ToolCalls[0].ToolCallID != "call1" || call.Observation.Results[0].Content != "failed" {
		t.Fatalf("call=%+v", call)
	}
	if result.Steps[3].Message != "try another way" || result.Steps[4].Message != "fixed" {
		t.Fatal(result.Steps)
	}
	if result.Extra["has_warnings"] != false {
		t.Fatal(result.Extra)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\n") {
		t.Fatal("JSONL record contains unescaped newline")
	}
	// Optional artifact for validation against Harbor's upstream Pydantic model.
	if dir := os.Getenv("ATIF_TEST_OUTPUT_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestBuildRepeatedUserMessageIsNotGloballyDeduplicated(t *testing.T) {
	a := exchange("a", `{"input":[{"role":"user","content":"continue"}]}`, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	b := exchange("b", `{"input":[{"role":"user","content":"continue"},{"role":"assistant","content":"ok"},{"role":"user","content":"continue"}]}`, `{"output":[]}`)
	got := build(t, a, b)
	if len(got.Steps) != 4 || got.Steps[2].Message != "continue" {
		t.Fatal(got.Steps)
	}
	b.Request = []byte(`{"previous_response_id":"r1","input":[{"role":"user","content":"continue"}]}`)
	got = build(t, a, b)
	if len(got.Steps) != 4 {
		t.Fatal("incremental input was lost", got.Steps)
	}
}
func TestBuildToolResultMissingOrOrphanAndUnknownContent(t *testing.T) {
	got := build(t, exchange("a", `{"input":[{"type":"function_call_output","call_id":"absent","output":"result"}]}`, `{"output":[{"type":"custom_tool_call","call_id":"c","name":"apply_patch","input":"*** patch ***"},{"type":"compaction","encrypted_content":"opaque"}]}`))
	if !hasWarning(got, "missing_tool_result") || !hasWarning(got, "orphan_tool_result") || !hasWarning(got, "unknown_item_type") {
		t.Fatal(got.Extra)
	}
	if got.Steps[0].Observation.Results[0].SourceCallID != "" {
		t.Fatal("invalid cross-step reference")
	}
	if got.Steps[1].ToolCalls[0].Arguments["input"] != "*** patch ***" {
		t.Fatal(got.Steps)
	}
}
func TestBuildMalformedAndUnavailableRecords(t *testing.T) {
	a := exchange("a", `{broken`, ``)
	b := exchange("b", `{}`, ``)
	b.Error = "missing"
	c := exchange("c", `{"messages":[]}`, `{}`)
	c.Endpoint = "/v1/chat/completions"
	got := build(t, a, b, c)
	for _, w := range []string{"invalid_request", "cassette_unavailable", "unsupported_endpoint"} {
		if !hasWarning(got, w) {
			t.Fatal(w, got.Extra)
		}
	}
	if len(got.Steps) != 3 {
		t.Fatal(got.Steps)
	}
}
func TestBuildCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, "s", "", []Exchange{exchange("a", `{}`, `{}`)}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestParseOutputStreamUsesFinalItemsOnce(t *testing.T) {
	body := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}

data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}

data: {"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":42}}}

`
	out, usage, status, warnings := parseOutput([]byte(body), true)
	if len(out) != 1 || render(out[0]["content"]) != "hello" || usage == nil || status != "completed" || len(warnings) != 0 {
		t.Fatalf("%+v %v %s %v", out, usage, status, warnings)
	}
}
func TestParseOutputStreamOrderingAndInterruptedArguments(t *testing.T) {
	body := `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"aaa","call_id":"c2","name":"two","arguments":""}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"zzz","call_id":"c1","name":"one","arguments":"{}"}}

data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"x\":1}"}

`
	out, _, _, warnings := parseOutput([]byte(body), true)
	if len(out) != 2 || out[0]["name"] != "one" || out[1]["arguments"] != `{"x":1}` || len(warnings) != 1 || warnings[0] != "stream_interrupted" {
		t.Fatalf("%+v %v", out, warnings)
	}
}
func TestParseOutputLargeStreamAndErrors(t *testing.T) {
	text := strings.Repeat("a", 100000)
	response := map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{map[string]any{"type": "message", "role": "assistant", "content": text}}}}
	raw, _ := json.Marshal(response)
	out, _, _, warnings := parseOutput(append(append([]byte("data: "), raw...), []byte("\n\n")...), true)
	if len(out) != 1 || out[0]["content"] != text || len(warnings) != 0 {
		t.Fatal("large stream failed")
	}
	_, _, _, warnings = parseOutput([]byte("data: {bad}\n\n"), true)
	if len(warnings) != 2 {
		t.Fatal(warnings)
	}
}

func TestIncompleteResponseRetainsStreamedOutput(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"output\":[],\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"
	out, _, status, warnings := parseOutput([]byte(body), true)
	if len(out) != 2 || render(out[0]["content"]) != "partial" || status != "incomplete" || len(warnings) == 0 {
		t.Fatalf("%+v %s %v", out, status, warnings)
	}
}
func TestContextChangePreservedAndWarned(t *testing.T) {
	a := exchange("a", `{"input":[{"role":"user","content":"task"},{"role":"user","content":"old context"}]}`, `{"output":[]}`)
	b := exchange("b", `{"input":[{"role":"user","content":"task"},{"role":"user","content":"new context"}]}`, `{"output":[]}`)
	got := build(t, a, b)
	if len(got.Steps) != 5 || got.Steps[3].Message != "new context" || !hasWarning(got, "context_discontinuity") {
		t.Fatal(got)
	}
}

func TestRecoveredAssistantHistoryDoesNotAcquireCurrentModel(t *testing.T) {
	got := build(t, exchange("a", `{"model":"current","input":[{"role":"assistant","content":"old model output"}]}`, `{"output":[{"type":"message","role":"assistant","content":"new output"}]}`))
	if got.Steps[0].ModelName != "" || got.Steps[1].ModelName != "current" {
		t.Fatal(got.Steps)
	}
}

func TestCompletedEmptyOutputDoesNotEraseStreamItems(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"last response\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{\"input_tokens\":10}}}\n\n"
	out, _, _, _ := parseOutput([]byte(body), true)
	if len(out) != 1 || render(out[0]["content"]) != "last response" {
		t.Fatalf("completed event erased streamed output: %+v", out)
	}
}

func TestResponseGroupMetricsAndCompactOutput(t *testing.T) {
	response := `{"id":"resp1","model":"actual-model","output":[{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"Check first"}],"encrypted_content":"OPAQUE_REASONING"},{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"Running checks"}]},{"type":"function_call","id":"fc1","call_id":"c1","name":"shell","arguments":"{\"cmd\":\"test\"}"},{"type":"function_call","id":"fc2","call_id":"c2","name":"shell","arguments":"{}"}],"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":7},"attribution":{"items":[{"debug":"ATTRIBUTION_DETAIL"}]}}}`
	first := exchange("a", `{"model":"alias","input":"fix","tools":[{"name":"shell","parameters":{"description":"REPEATED_TOOL_SCHEMA"}}]}`, response)
	first.UserAgent = "codex_cli_rs/0.154.0 (Linux)"
	first.DurationMs = 250
	second := exchange("b", `{"previous_response_id":"resp1","input":[{"type":"function_call_output","call_id":"c1","output":"ok"},{"type":"function_call_output","call_id":"c2","output":"ok2"}]}`, `{"output":[{"type":"message","role":"assistant","content":"done"}],"usage":{"input_tokens":200,"output_tokens":30,"input_tokens_details":{"cached_tokens":50}}}`)
	second.Time = first.Time.Add(time.Second)
	got := build(t, first, second)
	if got.SchemaVersion != "ATIF-v1.8" || len(got.Steps) != 3 {
		t.Fatal(got)
	}
	step := got.Steps[1]
	if step.Message != "Running checks" || step.Extra["reasoning_summary"] != "Check first" || step.ReasoningContent != "" || len(step.ToolCalls) != 2 || len(step.Observation.Results) != 2 {
		t.Fatal(step)
	}
	if step.ModelName != "actual-model" || step.Timestamp != "1970-01-01T00:01:40.25Z" || step.LLMCallCount == nil || *step.LLMCallCount != 1 {
		t.Fatal(step)
	}
	if *step.Metrics.PromptTokens != 100 || *got.FinalMetrics.TotalPromptTokens != 300 || *got.FinalMetrics.TotalCompletionTokens != 50 || *got.FinalMetrics.TotalCachedTokens != 90 || got.FinalMetrics.TotalSteps != 3 {
		t.Fatal(got.FinalMetrics)
	}
	if got.Agent.Name != "codex" || got.Agent.Version != "0.154.0" {
		t.Fatal(got.Agent)
	}
	data, _ := json.Marshal(got)
	for _, forbidden := range []string{"native_item", "attribution", "ATTRIBUTION_DETAIL", "OPAQUE_REASONING", "REPEATED_TOOL_SCHEMA", "request_config"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("export contains %s", forbidden)
		}
	}
}
func TestEmptyCompletedStreamFinalResponseIsPresentWithoutNextRequest(t *testing.T) {
	ex := exchange("a", `{"input":"hello"}`, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":\"last message\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")
	ex.Stream = true
	got := build(t, ex)
	if len(got.Steps) != 2 || got.Steps[1].Message != "last message" || got.Steps[1].Extra["origin"] != "response" || *got.FinalMetrics.TotalCompletionTokens != 2 {
		t.Fatal(got)
	}
}
func TestPartialFinalOutputKeepsEarlierItems(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"r\",\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"think\"}]}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":\"answer\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":\"answer\"}]}}\n\n"
	out, _, _, _ := parseOutput([]byte(body), true)
	if len(out) != 2 || out[0]["id"] != "r" || out[1]["id"] != "m" {
		t.Fatal(out)
	}
}
func TestJSONErrorForStreamingRequest(t *testing.T) {
	r := parseResponse([]byte(`{"error":{"message":"bad request"}}`), true)
	if len(r.Output) != 1 || r.Output[0]["type"] != "response_error" || len(r.Warnings) != 1 {
		t.Fatal(r)
	}
}
func TestMissingUsageIsUnknownAndLocalRuntimeDoesNotInventInferenceCount(t *testing.T) {
	ex := exchange("a", `{"input":"hi"}`, `{"output":[{"type":"message","role":"assistant","content":"ok"}]}`)
	ex.ExchangeKind = "entry"
	got := build(t, ex)
	if got.Steps[1].Metrics != nil || got.Steps[1].LLMCallCount != nil || got.FinalMetrics.TotalPromptTokens != nil {
		t.Fatal(got)
	}
}

func TestProviderMetadataDoesNotDuplicateResubmittedHistory(t *testing.T) {
	first := exchange("a", `{"input":"task"}`, `{"output":[{"id":"r1","type":"reasoning","content":[],"encrypted_content":"opaque","summary":[],"metadata":{"transport":"one"}},{"id":"c1","type":"custom_tool_call","call_id":"call1","name":"shell","input":"test","internal_chat_message_metadata_passthrough":{"channel":"analysis"},"metadata":{"transport":"two"}}]}`)
	second := exchange("b", `{"input":[{"role":"user","content":"task"},{"id":"r1","type":"reasoning","encrypted_content":"opaque","summary":[]},{"id":"c1","type":"custom_tool_call","call_id":"call1","name":"shell","input":"test"},{"type":"custom_tool_call_output","call_id":"call1","output":"ok"}]}`, `{"output":[{"type":"message","role":"assistant","content":"done"}]}`)
	got := build(t, first, second)
	if len(got.Steps) != 3 || len(got.Steps[1].ToolCalls) != 1 || got.Steps[1].Observation == nil || got.Extra["has_warnings"] != false {
		t.Fatal(got)
	}
}

func TestTerminalArgumentsOverridePartialDeltas(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"fc1\",\"type\":\"function_call\",\"arguments\":\"\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"id\":\"fc1\",\"type\":\"function_call\",\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}]}}\n\n"
	response := parseResponse([]byte(body), true)
	if len(response.Output) != 1 || response.Output[0]["arguments"] != `{"command":"pwd"}` {
		t.Fatalf("terminal arguments lost: %v", response.Output)
	}
}
