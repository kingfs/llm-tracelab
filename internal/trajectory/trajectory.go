// Package trajectory reconstructs client-visible Responses sessions as ATIF.
// It never calls a model or changes the source cassettes.
package trajectory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "ATIF-v1.7"

type Trajectory struct {
	SchemaVersion string         `json:"schema_version"`
	SessionID     string         `json:"session_id"`
	Agent         Agent          `json:"agent"`
	Steps         []Step         `json:"steps"`
	Extra         map[string]any `json:"extra"`
}
type Agent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type Step struct {
	StepID      int            `json:"step_id"`
	Source      string         `json:"source"`
	Message     string         `json:"message"`
	ModelName   string         `json:"model_name,omitempty"`
	ToolCalls   []ToolCall     `json:"tool_calls,omitempty"`
	Observation *Observation   `json:"observation,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
}
type ToolCall struct {
	ToolCallID   string         `json:"tool_call_id"`
	FunctionName string         `json:"function_name"`
	Arguments    map[string]any `json:"arguments"`
	Extra        map[string]any `json:"extra,omitempty"`
}
type Observation struct {
	Results []Result `json:"results"`
}
type Result struct {
	SourceCallID string         `json:"source_call_id,omitempty"`
	Content      string         `json:"content"`
	Extra        map[string]any `json:"extra,omitempty"`
}
type Warning struct {
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

// Exchange contains only client-visible exchanges, never their internal model children.
type Exchange struct {
	TraceID           string
	Time              time.Time
	Model             string
	Endpoint          string
	StatusCode        int
	Request, Response []byte
	Stream            bool
	Error             string
}
type item map[string]any

type builder struct {
	trajectory   Trajectory
	history      []item
	instructions string
	calls        map[string]int
	results      map[string]string
	warnings     []Warning
	exchanges    []map[string]any
	traceID      string
	model        string
}

// Build consumes a fixed exchange snapshot. Unknown protocol items and damaged
// exchanges are retained as explicitly marked system steps, not silently dropped.
func Build(ctx context.Context, sessionID, sessionSource string, exchanges []Exchange) (Trajectory, error) {
	if len(exchanges) == 0 {
		return Trajectory{}, fmt.Errorf("session has no recorded requests")
	}
	exchanges = append([]Exchange(nil), exchanges...)
	sort.SliceStable(exchanges, func(i, j int) bool {
		if exchanges[i].Time.Equal(exchanges[j].Time) {
			return exchanges[i].TraceID < exchanges[j].TraceID
		}
		return exchanges[i].Time.Before(exchanges[j].Time)
	})
	b := builder{trajectory: Trajectory{SchemaVersion: SchemaVersion, SessionID: sessionID, Agent: Agent{Name: "unknown", Version: "unknown"}}, calls: map[string]int{}, results: map[string]string{}}
	if strings.Contains(sessionSource, "codex") {
		b.trajectory.Agent.Name = "codex"
	}
	for _, ex := range exchanges {
		if err := ctx.Err(); err != nil {
			return Trajectory{}, err
		}
		b.traceID, b.model = ex.TraceID, ex.Model
		meta := map[string]any{"trace_id": ex.TraceID, "recorded_at": ex.Time.UTC().Format(time.RFC3339Nano), "endpoint": ex.Endpoint, "http_status": ex.StatusCode}
		b.exchanges = append(b.exchanges, meta)
		if ex.Error != "" {
			b.gap("cassette_unavailable", ex.Error)
			continue
		}
		var req item
		if err := json.Unmarshal(ex.Request, &req); err != nil || req == nil {
			b.gap("invalid_request", "Unable to decode request JSON")
			continue
		}
		// Do not claim support for other protocols or /responses/compact.
		if !strings.HasSuffix(strings.TrimRight(strings.Split(ex.Endpoint, "?")[0], "/"), "/responses") {
			b.gap("unsupported_endpoint", "Exchange retained in extra; endpoint is not a Responses generation")
			meta["request_body"], meta["response_body"] = string(ex.Request), string(ex.Response)
			continue
		}
		if s := str(req["model"]); s != "" {
			b.model = s
		}
		config := item{}
		for k, v := range req {
			if k != "input" && k != "instructions" {
				config[k] = v
			}
		}
		meta["request_config"] = config
		instruction := render(req["instructions"])
		if instruction != b.instructions {
			if instruction != "" {
				b.add(Step{Source: "system", Message: instruction, Extra: map[string]any{"path": "$.instructions"}})
			} else {
				b.warning("instructions_removed")
			}
			b.instructions = instruction
		}
		inputs := items(req["input"])
		n := overlap(b.history, inputs)
		if str(req["previous_response_id"]) != "" {
			n = 0
		}
		if len(b.history) > 0 && len(inputs) > 0 && n < min(len(b.history), len(inputs)) && str(req["previous_response_id"]) == "" {
			b.warning("context_discontinuity")
		}
		for i := n; i < len(inputs); i++ {
			b.appendItem(inputs[i], fmt.Sprintf("$.input[%d]", i), "request")
		}
		outputs, usage, status, warnings := parseOutput(ex.Response, ex.Stream)
		for _, w := range warnings {
			b.warning(w)
		}
		meta["response_status"], meta["usage"] = status, usage
		if ex.StatusCode < 200 || ex.StatusCode >= 300 {
			b.warning("http_error")
			meta["response_body"] = string(ex.Response)
		}
		for i, it := range outputs {
			b.appendItem(it, fmt.Sprintf("$.output[%d]", i), "response")
		}
		// Request context is a snapshot, not a globally deduplicated bag of messages.
		// Preserve the accumulated context for incremental previous_response_id requests.
		if str(req["previous_response_id"]) != "" {
			b.history = append(b.history, inputs[n:]...)
		} else {
			b.history = append([]item(nil), inputs...)
		}
		b.history = append(b.history, outputs...)
	}
	for index := range b.trajectory.Steps {
		step := &b.trajectory.Steps[index]
		if len(step.ToolCalls) > 0 && step.Observation == nil {
			b.warnings = append(b.warnings, Warning{Code: "missing_tool_result", TraceID: str(step.Extra["trace_id"])})
			step.Extra["missing_result_call_id"] = step.ToolCalls[0].ToolCallID
		}
	}
	if len(b.trajectory.Steps) == 0 {
		b.add(Step{Source: "system", Message: "No reconstructable conversation items", Extra: map[string]any{"kind": "empty_trajectory"}})
		b.warning("empty_trajectory")
	}
	// Keep warning order deterministic across exports.
	sort.SliceStable(b.warnings, func(i, j int) bool {
		if b.warnings[i].TraceID == b.warnings[j].TraceID {
			return b.warnings[i].Code < b.warnings[j].Code
		}
		return b.warnings[i].TraceID < b.warnings[j].TraceID
	})
	b.trajectory.Extra = map[string]any{"exporter": "llm-tracelab", "exporter_version": "1", "session_source": sessionSource, "scope": "client_visible_responses", "request_count": len(exchanges), "exchanges": b.exchanges, "warnings": b.warnings, "has_warnings": len(b.warnings) > 0, "completion": "unknown", "ordering": "recorded_at_then_trace_id; output_index_within_response"}
	return b.trajectory, nil
}
func (b *builder) warning(code string) {
	b.warnings = append(b.warnings, Warning{Code: code, TraceID: b.traceID})
}
func (b *builder) gap(code, message string) {
	b.warning(code)
	b.add(Step{Source: "system", Message: message, Extra: map[string]any{"kind": code}})
}
func (b *builder) add(s Step) int {
	s.StepID = len(b.trajectory.Steps) + 1
	if s.Extra == nil {
		s.Extra = map[string]any{}
	}
	s.Extra["trace_id"] = b.traceID
	if s.Source == "agent" && s.Extra["origin"] == "response" {
		s.ModelName = b.model
	}
	b.trajectory.Steps = append(b.trajectory.Steps, s)
	return len(b.trajectory.Steps) - 1
}
func (b *builder) appendItem(it item, path, origin string) {
	typ := str(it["type"])
	extra := map[string]any{"path": path, "origin": origin, "native_item": it}
	switch typ {
	case "function_call", "custom_tool_call", "local_shell_call", "mcp_call", "web_search_call", "file_search_call", "computer_call", "code_interpreter_call":
		id := str(it["call_id"])
		if id == "" {
			id = str(it["id"])
		}
		if id == "" {
			id = fmt.Sprintf("%s:call:%d", b.traceID, len(b.trajectory.Steps)+1)
			b.warning("synthetic_call_id")
		}
		if index, ok := b.calls[id]; ok {
			if origin == "request" && signature(it) == signature(b.trajectory.Steps[index].Extra["native_item"].(item)) {
				return
			}
			b.warning("reused_call_id")
		}
		args := map[string]any{}
		raw := it["arguments"]
		if typ == "custom_tool_call" {
			args["input"] = it["input"]
		} else if s, ok := raw.(string); ok {
			if json.Unmarshal([]byte(s), &args) != nil || args == nil {
				args = map[string]any{"raw_arguments": s}
				b.warning("non_object_tool_arguments")
			}
		} else if m, ok := raw.(map[string]any); ok {
			args = m
		} else if raw != nil {
			args["raw_arguments"] = raw
		}
		name := str(it["name"])
		if name == "" {
			name = typ
		}
		index := b.add(Step{Source: "agent", Message: "", ToolCalls: []ToolCall{{ToolCallID: id, FunctionName: name, Arguments: args}}, Extra: extra})
		b.calls[id] = index
		delete(b.results, id)
	case "function_call_output", "custom_tool_call_output", "mcp_call_output", "local_shell_call_output", "computer_call_output", "web_search_call_output", "file_search_call_output", "code_interpreter_call_output":
		id := str(it["call_id"])
		result := Result{Content: render(it["output"]), Extra: map[string]any{"trace_id": b.traceID, "path": path, "native_item": it}}
		if index, ok := b.calls[id]; ok && id != "" {
			if previous, exists := b.results[id]; exists {
				if previous != signature(it) {
					b.warning("conflicting_tool_result")
					b.add(Step{Source: "system", Message: "Conflicting repeated tool result", Observation: &Observation{Results: []Result{result}}, Extra: extra})
				}
				return
			}
			result.SourceCallID = id
			b.trajectory.Steps[index].Observation = &Observation{Results: []Result{result}}
			b.results[id] = signature(it)
		} else {
			b.warning("orphan_tool_result")
			b.add(Step{Source: "system", Message: "Tool result without a recorded call", Observation: &Observation{Results: []Result{result}}, Extra: extra})
		}
	case "message", "":
		role := str(it["role"])
		source := "system"
		switch role {
		case "assistant":
			source = "agent"
		case "user":
			source = "user"
		case "system", "developer":
		default:
			b.warning("unknown_message_role")
		}
		b.add(Step{Source: source, Message: render(it["content"]), Extra: extra})
	case "reasoning":
		// Summaries and encrypted reasoning are preserved with their native type;
		// neither is mislabeled as a complete, readable internal chain of thought.
		b.add(Step{Source: "agent", Message: render(it["summary"]), Extra: extra})
	default:
		b.warning("unknown_item_type")
		b.add(Step{Source: "system", Message: render(it), Extra: extra})
	}
}
func str(v any) string { s, _ := v.(string); return s }
func render(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if a, ok := v.([]any); ok {
		texts := make([]string, 0, len(a))
		plain := true
		for _, v := range a {
			m, ok := v.(map[string]any)
			if !ok {
				plain = false
				break
			}
			s, ok := m["text"].(string)
			if !ok {
				plain = false
				break
			}
			texts = append(texts, s)
		}
		if plain {
			return strings.Join(texts, "\n")
		}
	}
	data, _ := json.Marshal(v)
	return string(data)
}
func items(v any) []item {
	if s, ok := v.(string); ok {
		return []item{{"type": "message", "role": "user", "content": s}}
	}
	if m, ok := v.(map[string]any); ok {
		return []item{m}
	}
	var out []item
	if a, ok := v.([]any); ok {
		for _, v := range a {
			if m, ok := v.(map[string]any); ok {
				out = append(out, m)
			} else {
				out = append(out, item{"type": "unknown", "value": v})
			}
		}
	}
	return out
}
func signature(it item) string {
	typ := str(it["type"])
	if typ == "message" || (typ == "" && it["role"] != nil) {
		return render([]any{it["role"], render(it["content"])})
	}
	normalized := item{}
	for k, v := range it {
		if k != "id" && k != "status" {
			normalized[k] = v
		}
	}
	return render(normalized)
}

// overlap matches a contiguous prefix against the previous context. It does not
// delete repeated text globally. Complete-prefix matching also handles retries.
func overlap(history, input []item) int {
	prefix := 0
	for prefix < len(history) && prefix < len(input) && signature(history[prefix]) == signature(input[prefix]) {
		prefix++
	}
	if prefix == len(history) || prefix == len(input) {
		return prefix
	}
	for n := min(len(history), len(input)); n > 0; n-- {
		equal := true
		for i := 0; i < n; i++ {
			if signature(history[len(history)-n+i]) != signature(input[i]) {
				equal = false
				break
			}
		}
		if equal {
			return n
		}
	}
	return prefix
}
