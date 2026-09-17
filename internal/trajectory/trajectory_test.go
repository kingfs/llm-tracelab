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
	if len(got.Steps) != 3 || got.Steps[2].Message != "continue" {
		t.Fatal(got.Steps)
	}
	b.Request = []byte(`{"previous_response_id":"r1","input":[{"role":"user","content":"continue"}]}`)
	got = build(t, a, b)
	if len(got.Steps) != 3 {
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
	if len(got.Steps) != 3 || got.Steps[2].Message != "new context" || !hasWarning(got, "context_discontinuity") {
		t.Fatal(got)
	}
}

func TestRecoveredAssistantHistoryDoesNotAcquireCurrentModel(t *testing.T) {
	got := build(t, exchange("a", `{"model":"current","input":[{"role":"assistant","content":"old model output"}]}`, `{"output":[{"type":"message","role":"assistant","content":"new output"}]}`))
	if got.Steps[0].ModelName != "" || got.Steps[1].ModelName != "current" {
		t.Fatal(got.Steps)
	}
}
