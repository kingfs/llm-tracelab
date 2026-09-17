package trajectory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// parseOutput prefers final response.output, otherwise merges item lifecycle
// events by output_index. Deltas are fallback data, never appended to done items.
func parseOutput(body []byte, stream bool) ([]item, any, string, []string) {
	if !stream && !strings.HasPrefix(strings.TrimSpace(string(body)), "data:") && !strings.HasPrefix(strings.TrimSpace(string(body)), "event:") {
		var response item
		if err := json.Unmarshal(body, &response); err != nil || response == nil {
			return nil, nil, "unknown", []string{"invalid_response"}
		}
		out := items(response["output"])
		if len(out) == 0 && str(response["output_text"]) != "" {
			out = []item{{"type": "message", "role": "assistant", "content": response["output_text"]}}
		}
		var warnings []string
		status := str(response["status"])
		if status == "failed" || status == "incomplete" || response["error"] != nil {
			warnings = append(warnings, "response_error")
			out = append(out, item{"type": "response_error", "error": response["error"], "status": status, "incomplete_details": response["incomplete_details"]})
		}
		return out, response["usage"], status, warnings
	}
	type slot struct {
		value     item
		done      bool
		text      map[int]string
		arguments string
		reasoning map[int]string
	}
	slots := map[int]*slot{}
	var final []item
	var terminalErrors []item
	var usage any
	var warnings []string
	status := "unknown"
	terminal, hasFinal := false, false
	consume := func(data string) {
		if strings.TrimSpace(data) == "[DONE]" {
			return
		}
		var event item
		if json.Unmarshal([]byte(data), &event) != nil || event == nil {
			warnings = append(warnings, "invalid_stream_event")
			return
		}
		typ := str(event["type"])
		if typ == "response.completed" || typ == "response.incomplete" || typ == "response.failed" {
			terminal = true
			status = strings.TrimPrefix(typ, "response.")
			if response, ok := event["response"].(map[string]any); ok {
				usage = response["usage"]
				if _, exists := response["output"]; exists {
					final = items(response["output"])
					hasFinal = status == "completed" || len(final) > 0
				}
				if status != "completed" {
					terminalErrors = append(terminalErrors, item{"type": "response_error", "status": status, "error": response["error"], "incomplete_details": response["incomplete_details"]})
				}
			}
			if status != "completed" {
				warnings = append(warnings, "response_"+status)
			}
			return
		}
		if typ == "error" {
			warnings = append(warnings, "stream_error")
			return
		}
		number, ok := event["output_index"].(float64)
		if !ok {
			return
		}
		index := int(number)
		s := slots[index]
		if s == nil {
			s = &slot{text: map[int]string{}, reasoning: map[int]string{}}
			slots[index] = s
		}
		contentIndex := 0
		if n, ok := event["content_index"].(float64); ok {
			contentIndex = int(n)
		}
		switch typ {
		case "response.output_item.added", "response.output_item.done":
			if m, ok := event["item"].(map[string]any); ok {
				s.value = m
				s.done = typ == "response.output_item.done"
			}
		case "response.output_text.delta", "response.refusal.delta":
			s.text[contentIndex] += str(event["delta"])
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta", "response.mcp_call_arguments.delta":
			s.arguments += str(event["delta"])
		case "response.function_call_arguments.done", "response.mcp_call_arguments.done":
			s.arguments = str(event["arguments"])
		case "response.custom_tool_call_input.done":
			s.arguments = str(event["input"])
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			s.reasoning[contentIndex] += str(event["delta"])
		}
	}
	// SSE supports multiple data lines per event and has no Scanner token limit.
	var data []string
	flush := func() {
		if len(data) > 0 {
			consume(strings.Join(data, "\n"))
			data = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	if !terminal {
		warnings = append(warnings, "stream_interrupted")
	}
	if hasFinal {
		return append(final, terminalErrors...), usage, status, warnings
	}
	indices := make([]int, 0, len(slots))
	for n := range slots {
		indices = append(indices, n)
	}
	sort.Ints(indices)
	out := make([]item, 0, len(indices))
	for _, n := range indices {
		s := slots[n]
		if s.value == nil {
			s.value = item{"type": "message", "role": "assistant", "id": fmt.Sprintf("partial_%d", n)}
			warnings = append(warnings, "missing_output_item")
		}
		if !s.done {
			if len(s.text) > 0 {
				s.value["content"] = joinedParts(s.text, "output_text")
			}
			if len(s.reasoning) > 0 {
				s.value["type"] = "reasoning"
				s.value["summary"] = joinedParts(s.reasoning, "summary_text")
			}
			if s.arguments != "" {
				if str(s.value["type"]) == "custom_tool_call" {
					s.value["input"] = s.arguments
				} else {
					s.value["arguments"] = s.arguments
				}
			}
		}
		out = append(out, s.value)
	}
	return append(out, terminalErrors...), usage, status, warnings
}
func joinedParts(parts map[int]string, typ string) []any {
	indices := make([]int, 0, len(parts))
	for n := range parts {
		indices = append(indices, n)
	}
	sort.Ints(indices)
	out := make([]any, 0, len(indices))
	for _, n := range indices {
		out = append(out, map[string]any{"type": typ, "text": parts[n]})
	}
	return out
}
