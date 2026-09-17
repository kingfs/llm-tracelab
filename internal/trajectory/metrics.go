package trajectory

import (
	"math"
	"regexp"
	"strings"
)

// Only aggregate counters enter ATIF. Provider attribution lists stay in cassettes.
func normalizeMetrics(usage any) *Metrics {
	obj, ok := usage.(map[string]any)
	if !ok {
		return nil
	}
	result := &Metrics{PromptTokens: tokenCount(obj["input_tokens"]), CompletionTokens: tokenCount(obj["output_tokens"])}
	if details, ok := obj["input_tokens_details"].(map[string]any); ok {
		result.CachedTokens = tokenCount(details["cached_tokens"])
		if n := tokenCount(details["cache_write_tokens"]); n != nil {
			result.Extra = map[string]any{"cache_write_input_tokens": *n}
		}
	}
	if details, ok := obj["output_tokens_details"].(map[string]any); ok {
		if n := tokenCount(details["reasoning_tokens"]); n != nil {
			if result.Extra == nil {
				result.Extra = map[string]any{}
			}
			result.Extra["reasoning_output_tokens"] = *n
		}
	}
	if result.PromptTokens == nil && result.CompletionTokens == nil && result.CachedTokens == nil && len(result.Extra) == 0 {
		return nil
	}
	return result
}
func tokenCount(v any) *int64 {
	n, ok := v.(float64)
	if !ok || n < 0 || n >= float64(math.MaxInt64) || math.Trunc(n) != n {
		return nil
	}
	value := int64(n)
	return &value
}
func aggregateMetrics(steps []Step) FinalMetrics {
	totals := FinalMetrics{TotalSteps: len(steps)}
	for _, s := range steps {
		if s.Metrics == nil {
			continue
		}
		addCount(&totals.TotalPromptTokens, s.Metrics.PromptTokens)
		addCount(&totals.TotalCompletionTokens, s.Metrics.CompletionTokens)
		addCount(&totals.TotalCachedTokens, s.Metrics.CachedTokens)
	}
	return totals
}
func addCount(dst **int64, src *int64) {
	if src == nil {
		return
	}
	if *dst == nil {
		n := *src
		*dst = &n
	} else {
		**dst += *src
	}
}

var codexAgentPattern = regexp.MustCompile(`(?i)(?:codex_cli_rs|codex[-_]cli|codex[-_]tui|codex)/([^\s()]+)`)

func (b *builder) identifyAgent(ex Exchange) {
	if matches := codexAgentPattern.FindStringSubmatch(ex.UserAgent); len(matches) > 1 {
		b.trajectory.Agent = Agent{Name: "codex", Version: matches[1]}
		return
	}
	if strings.Contains(strings.ToLower(ex.Originator), "codex") {
		b.trajectory.Agent.Name = "codex"
	}
}

// Summarize opaque media as references. Never embed encrypted reasoning, base64
// payloads or provider-specific node replicas in a human-readable trajectory.
func contentText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if list, ok := v.([]any); ok {
		var parts []string
		for _, raw := range list {
			m, ok := raw.(map[string]any)
			if !ok {
				parts = append(parts, "[Unmapped content; see source trace]")
				continue
			}
			if text, ok := m["text"].(string); ok {
				parts = append(parts, text)
				continue
			}
			if refusal, ok := m["refusal"].(string); ok {
				parts = append(parts, refusal)
				continue
			}
			parts = append(parts, "["+str(m["type"])+" content; see source trace]")
		}
		return strings.Join(parts, "\n")
	}
	if v == nil {
		return ""
	}
	return render(v)
}
