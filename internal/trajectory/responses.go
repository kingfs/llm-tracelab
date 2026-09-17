package trajectory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type responseData struct {
	Output            []item
	Usage             any
	ID, Model, Status string
	Warnings          []string
}

func parseOutput(body []byte, stream bool) ([]item, any, string, []string) {
	r := parseResponse(body, stream)
	return r.Output, r.Usage, r.Status, r.Warnings
}
func parseResponse(body []byte, stream bool) responseData {
	text := strings.TrimSpace(string(body))
	// Some failed streaming requests return JSON instead of SSE.
	if strings.HasPrefix(text, "{") || (!stream && !strings.HasPrefix(text, "data:") && !strings.HasPrefix(text, "event:") && !strings.HasPrefix(text, ":")) {
		var response item
		if json.Unmarshal(body, &response) != nil || response == nil {
			return responseData{Status: "unknown", Warnings: []string{"invalid_response"}}
		}
		r := responseData{Output: items(response["output"]), Usage: response["usage"], ID: str(response["id"]), Model: str(response["model"]), Status: str(response["status"])}
		if len(r.Output) == 0 && str(response["output_text"]) != "" {
			r.Output = []item{{"type": "message", "role": "assistant", "content": response["output_text"]}}
		}
		if r.Status == "failed" || r.Status == "incomplete" || response["error"] != nil {
			r.Warnings = append(r.Warnings, "response_error")
			r.Output = append(r.Output, errorItem(response))
		}
		return r
	}
	return parseStream(text)
}

type outputSlot struct {
	value     item
	done      bool
	text      map[int]string
	reasoning map[int]string
	arguments string
}

func parseStream(body string) responseData {
	r := responseData{Status: "unknown"}
	slots := map[int]*outputSlot{}
	var final, errors []item
	terminal := false
	consume := func(data, eventName string) {
		if strings.TrimSpace(data) == "[DONE]" {
			return
		}
		var event item
		if json.Unmarshal([]byte(data), &event) != nil || event == nil {
			r.Warnings = append(r.Warnings, "invalid_stream_event")
			return
		}
		typ := str(event["type"])
		if typ == "" {
			typ = eventName
		}
		if response, ok := event["response"].(map[string]any); ok {
			if id := str(response["id"]); id != "" {
				r.ID = id
			}
			if model := str(response["model"]); model != "" {
				r.Model = model
			}
		}
		switch typ {
		case "response.completed", "response.incomplete", "response.failed":
			terminal = true
			r.Status = strings.TrimPrefix(typ, "response.")
			if response, ok := event["response"].(map[string]any); ok {
				if response["usage"] != nil {
					r.Usage = response["usage"]
				}
				final = items(response["output"])
				if r.Status != "completed" {
					errors = append(errors, errorItem(response))
				}
			}
			if r.Status != "completed" {
				r.Warnings = append(r.Warnings, "response_"+r.Status)
			}
			return
		case "error":
			r.Warnings = append(r.Warnings, "stream_error")
			errors = append(errors, errorItem(event))
			return
		}
		index := -1
		if number, ok := event["output_index"].(float64); ok {
			index = int(number)
		} else if id := str(event["item_id"]); id != "" {
			for n, s := range slots {
				if str(s.value["id"]) == id {
					index = n
					break
				}
			}
		}
		if index < 0 {
			return
		}
		s := slots[index]
		if s == nil {
			s = &outputSlot{text: map[int]string{}, reasoning: map[int]string{}}
			slots[index] = s
		}
		contentIndex := 0
		if n, ok := event["content_index"].(float64); ok {
			contentIndex = int(n)
		}
		if n, ok := event["summary_index"].(float64); ok {
			contentIndex = int(n)
		}
		switch typ {
		case "response.output_item.added", "response.output_item.done":
			if value, ok := event["item"].(map[string]any); ok && (!s.done || typ == "response.output_item.done") {
				s.value = value
				s.done = typ == "response.output_item.done"
			}
		case "response.output_text.delta", "response.refusal.delta":
			s.text[contentIndex] += str(event["delta"])
		case "response.output_text.done":
			s.text[contentIndex] = str(event["text"])
		case "response.refusal.done":
			s.text[contentIndex] = str(event["refusal"])
		case "response.function_call_arguments.delta", "response.mcp_call_arguments.delta", "response.custom_tool_call_input.delta":
			s.arguments += str(event["delta"])
		case "response.function_call_arguments.done", "response.mcp_call_arguments.done":
			s.arguments = str(event["arguments"])
		case "response.custom_tool_call_input.done":
			s.arguments = str(event["input"])
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			s.reasoning[contentIndex] += str(event["delta"])
		}
	}
	var data []string
	eventName := ""
	flush := func() {
		if len(data) > 0 {
			consume(strings.Join(data, "\n"), eventName)
		}
		data = nil
		eventName = ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	flush()
	if !terminal {
		r.Warnings = append(r.Warnings, "stream_interrupted")
	}
	// The terminal event is not guaranteed to repeat output items. Empty or
	// partial output must never erase items already delivered by the stream.
	for i, it := range final {
		index := i
		id := str(it["id"])
		matched := false
		if id != "" {
			for n, s := range slots {
				if str(s.value["id"]) == id {
					index = n
					matched = true
					break
				}
			}
		}
		if !matched && slots[index] != nil && id != "" && str(slots[index].value["id"]) != "" && str(slots[index].value["id"]) != id {
			for n := range slots {
				if n >= index {
					index = n + 1
				}
			}
		}
		s := slots[index]
		if s == nil {
			s = &outputSlot{text: map[int]string{}, reasoning: map[int]string{}}
			slots[index] = s
		}
		if s.value == nil {
			s.value = item{}
		}
		for k, v := range it {
			if !emptyValue(v) || emptyValue(s.value[k]) {
				s.value[k] = v
			}
		}
	}
	indices := make([]int, 0, len(slots))
	for n := range slots {
		indices = append(indices, n)
	}
	sort.Ints(indices)
	for _, n := range indices {
		s := slots[n]
		if s.value == nil {
			s.value = item{"type": "message", "role": "assistant", "id": fmt.Sprintf("partial_%d", n)}
			r.Warnings = append(r.Warnings, "missing_output_item")
		}
		if emptyValue(s.value["content"]) && len(s.text) > 0 {
			s.value["content"] = joinedParts(s.text, "output_text")
		}
		if emptyValue(s.value["summary"]) && len(s.reasoning) > 0 {
			s.value["type"] = "reasoning"
			s.value["summary"] = joinedParts(s.reasoning, "summary_text")
		}
		if s.arguments != "" {
			field := "arguments"
			if str(s.value["type"]) == "custom_tool_call" {
				field = "input"
			}
			if emptyValue(s.value[field]) {
				s.value[field] = s.arguments
			}
		}
		r.Output = append(r.Output, s.value)
	}
	r.Output = append(r.Output, errors...)
	return r
}
func emptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch v := v.(type) {
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}
func errorItem(obj item) item {
	return item{"type": "response_error", "error": obj["error"], "status": obj["status"], "incomplete_details": obj["incomplete_details"]}
}
func joinedParts(parts map[int]string, typ string) []any {
	indices := make([]int, 0, len(parts))
	for n := range parts {
		indices = append(indices, n)
	}
	sort.Ints(indices)
	out := make([]any, 0, len(parts))
	for _, n := range indices {
		out = append(out, map[string]any{"type": typ, "text": parts[n]})
	}
	return out
}
