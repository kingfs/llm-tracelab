package trajectory

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

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

// Match semantic items across response -> next request. Codex drops provider
// metadata, status, logprobs and empty content when resubmitting history.
func signature(it item) string {
	typ := str(it["type"])
	normalized := item{"type": typ}
	switch {
	case typ == "message" || (typ == "" && it["role"] != nil):
		normalized = item{"type": "message", "role": it["role"], "content": render(it["content"])}
	case typ == "reasoning":
		normalized["summary"] = render(it["summary"])
		normalized["content"] = render(it["content"])
		normalized["encrypted_content"] = str(it["encrypted_content"])
	case isToolCall(typ):
		for _, key := range []string{"call_id", "name", "input", "action", "arguments"} {
			if value, ok := it[key]; ok {
				normalized[key] = value
			}
		}
		if args, ok := it["arguments"].(string); ok {
			var value any
			if json.Unmarshal([]byte(args), &value) == nil {
				normalized["arguments"] = value
			}
		}
	case isToolResult(typ):
		normalized["call_id"] = it["call_id"]
		normalized["output"] = it["output"]
	default:
		for k, v := range it {
			if k != "id" && k != "status" && k != "metadata" && k != "internal_chat_message_metadata_passthrough" {
				normalized[k] = v
			}
		}
	}
	data, _ := json.Marshal(normalized)
	return fmt.Sprintf("%x", sha256.Sum256(data))
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
