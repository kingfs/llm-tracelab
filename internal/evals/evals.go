package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/replay"
)

const BaselineEvaluatorSet = "baseline_v4"

type Result struct {
	EvaluatorKey string  `json:"evaluator_key"`
	Value        float64 `json:"value"`
	Status       string  `json:"status"`
	Label        string  `json:"label"`
	Explanation  string  `json:"explanation"`
}

type Profile struct {
	Name                    string   `json:"name"`
	Description             string   `json:"description"`
	Deterministic           bool     `json:"deterministic"`
	TTFTBudgetMS            int      `json:"ttft_budget_ms,omitempty"`
	TotalTokenBudget        int      `json:"total_token_budget,omitempty"`
	RequireDeclaredToolCall bool     `json:"require_declared_tool_call,omitempty"`
	RequireToolArgsJSON     bool     `json:"require_tool_args_json,omitempty"`
	EvaluatorKeys           []string `json:"evaluator_keys"`
}

var profiles = map[string]Profile{
	"baseline_v1": {
		Name:          "baseline_v1",
		Description:   "Legacy deterministic checks for status, recorded error, and response body presence.",
		Deterministic: true,
		EvaluatorKeys: []string{
			"http_status_2xx",
			"no_recorded_error",
			"response_has_body",
		},
	},
	"baseline_v2": {
		Name:             "baseline_v2",
		Description:      "Deterministic baseline checks plus TTFT and total-token budget constraints.",
		Deterministic:    true,
		TTFTBudgetMS:     2000,
		TotalTokenBudget: 32000,
		EvaluatorKeys: []string{
			"http_status_2xx",
			"no_recorded_error",
			"response_has_body",
			"ttft_le_2000ms",
			"total_tokens_le_32000",
		},
	},
	"baseline_v3": {
		Name:                    "baseline_v3",
		Description:             "Deterministic baseline checks plus TTFT, total-token budgets, and declared tool-call conformance.",
		Deterministic:           true,
		TTFTBudgetMS:            2000,
		TotalTokenBudget:        32000,
		RequireDeclaredToolCall: true,
		EvaluatorKeys: []string{
			"http_status_2xx",
			"no_recorded_error",
			"response_has_body",
			"ttft_le_2000ms",
			"total_tokens_le_32000",
			"tool_calls_declared",
		},
	},
	"baseline_v4": {
		Name:                    "baseline_v4",
		Description:             "Deterministic baseline checks plus TTFT, total-token budgets, declared tool-call conformance, and tool-call argument JSON validation.",
		Deterministic:           true,
		TTFTBudgetMS:            2000,
		TotalTokenBudget:        32000,
		RequireDeclaredToolCall: true,
		RequireToolArgsJSON:     true,
		EvaluatorKeys: []string{
			"http_status_2xx",
			"no_recorded_error",
			"response_has_body",
			"ttft_le_2000ms",
			"total_tokens_le_32000",
			"tool_calls_declared",
			"tool_call_arguments_json",
		},
	},
}

func ListProfiles() []Profile {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Profile, 0, len(names))
	for _, name := range names {
		out = append(out, profiles[name])
	}
	return out
}

func GetProfile(name string) (Profile, bool) {
	profile, ok := profiles[strings.TrimSpace(name)]
	return profile, ok
}

func Evaluate(entry store.LogEntry, summary *replay.Summary, evaluatorSet string) ([]Result, error) {
	profile, ok := GetProfile(evaluatorSet)
	if !ok {
		return nil, fmt.Errorf("unknown evaluator_set %q", strings.TrimSpace(evaluatorSet))
	}

	results := []Result{
		status2xx(entry),
		noRecordedError(entry),
		responseHasBody(summary),
	}
	if profile.TTFTBudgetMS > 0 {
		results = append(results, ttftWithinBudget(entry, profile.TTFTBudgetMS))
	}
	if profile.TotalTokenBudget > 0 {
		results = append(results, totalTokensWithinBudget(entry, profile.TotalTokenBudget))
	}
	if profile.RequireDeclaredToolCall {
		results = append(results, toolCallsDeclared(entry))
	}
	if profile.RequireToolArgsJSON {
		results = append(results, toolCallArgumentsJSON(entry))
	}
	return results, nil
}

func EvaluateBaseline(entry store.LogEntry, summary *replay.Summary) []Result {
	results, err := Evaluate(entry, summary, BaselineEvaluatorSet)
	if err != nil {
		panic(err)
	}
	return results
}

func status2xx(entry store.LogEntry) Result {
	if entry.Header.Meta.StatusCode >= 200 && entry.Header.Meta.StatusCode < 300 {
		return Result{
			EvaluatorKey: "http_status_2xx",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  fmt.Sprintf("status code %d is successful", entry.Header.Meta.StatusCode),
		}
	}
	return Result{
		EvaluatorKey: "http_status_2xx",
		Value:        0,
		Status:       "fail",
		Label:        "fail",
		Explanation:  fmt.Sprintf("status code %d is not successful", entry.Header.Meta.StatusCode),
	}
}

func noRecordedError(entry store.LogEntry) Result {
	if strings.TrimSpace(entry.Header.Meta.Error) == "" {
		return Result{
			EvaluatorKey: "no_recorded_error",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  "recorded metadata has no error text",
		}
	}
	return Result{
		EvaluatorKey: "no_recorded_error",
		Value:        0,
		Status:       "fail",
		Label:        "fail",
		Explanation:  "recorded metadata contains error text",
	}
}

func responseHasBody(summary *replay.Summary) Result {
	if summary != nil && summary.BodyBytes > 0 {
		return Result{
			EvaluatorKey: "response_has_body",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  fmt.Sprintf("replayed response has %d bytes", summary.BodyBytes),
		}
	}
	return Result{
		EvaluatorKey: "response_has_body",
		Value:        0,
		Status:       "fail",
		Label:        "fail",
		Explanation:  "replayed response body is empty",
	}
}

func ttftWithinBudget(entry store.LogEntry, budgetMS int) Result {
	evaluatorKey := fmt.Sprintf("ttft_le_%dms", budgetMS)
	ttft := entry.Header.Meta.TTFTMs
	switch {
	case ttft <= 0:
		return Result{
			EvaluatorKey: evaluatorKey,
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "recorded TTFT is missing or non-positive",
		}
	case ttft <= int64(budgetMS):
		return Result{
			EvaluatorKey: evaluatorKey,
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  fmt.Sprintf("recorded TTFT %dms is within %dms budget", ttft, budgetMS),
		}
	default:
		return Result{
			EvaluatorKey: evaluatorKey,
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  fmt.Sprintf("recorded TTFT %dms exceeds %dms budget", ttft, budgetMS),
		}
	}
}

func totalTokensWithinBudget(entry store.LogEntry, budget int) Result {
	evaluatorKey := fmt.Sprintf("total_tokens_le_%d", budget)
	totalTokens := entry.Header.Usage.TotalTokens
	switch {
	case totalTokens < 0:
		return Result{
			EvaluatorKey: evaluatorKey,
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "recorded total token count is negative",
		}
	case totalTokens <= budget:
		return Result{
			EvaluatorKey: evaluatorKey,
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  fmt.Sprintf("recorded total tokens %d are within %d token budget", totalTokens, budget),
		}
	default:
		return Result{
			EvaluatorKey: evaluatorKey,
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  fmt.Sprintf("recorded total tokens %d exceed %d token budget", totalTokens, budget),
		}
	}
}

func toolCallsDeclared(entry store.LogEntry) Result {
	if strings.TrimSpace(entry.LogPath) == "" {
		return Result{
			EvaluatorKey: "tool_calls_declared",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "trace log path is missing",
		}
	}
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		return Result{
			EvaluatorKey: "tool_calls_declared",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  fmt.Sprintf("read trace log: %v", err),
		}
	}
	parsed, err := monitor.ParseLogFile(content)
	if err != nil {
		return Result{
			EvaluatorKey: "tool_calls_declared",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  fmt.Sprintf("parse trace log: %v", err),
		}
	}
	if len(parsed.ResponseToolCalls) == 0 {
		return Result{
			EvaluatorKey: "tool_calls_declared",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  "no response tool calls recorded",
		}
	}

	declared := map[string]struct{}{}
	for _, tool := range parsed.RequestTools {
		name := strings.TrimSpace(strings.ToLower(tool.Name))
		if name != "" {
			declared[name] = struct{}{}
		}
	}
	if len(declared) == 0 {
		return Result{
			EvaluatorKey: "tool_calls_declared",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "response contains tool calls but request declared no tools",
		}
	}

	var missing []string
	for _, call := range parsed.ResponseToolCalls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			missing = append(missing, "<unnamed>")
			continue
		}
		if _, ok := declared[strings.ToLower(name)]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return Result{
			EvaluatorKey: "tool_calls_declared",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  fmt.Sprintf("%d response tool calls matched declared tools", len(parsed.ResponseToolCalls)),
		}
	}
	sort.Strings(missing)
	return Result{
		EvaluatorKey: "tool_calls_declared",
		Value:        0,
		Status:       "fail",
		Label:        "fail",
		Explanation:  fmt.Sprintf("response tool calls missing request declarations: %s", strings.Join(dedupeAdjacentStrings(missing), ", ")),
	}
}

func dedupeAdjacentStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if len(out) > 0 && out[len(out)-1] == value {
			continue
		}
		out = append(out, value)
	}
	return out
}

func toolCallArgumentsJSON(entry store.LogEntry) Result {
	if strings.TrimSpace(entry.LogPath) == "" {
		return Result{
			EvaluatorKey: "tool_call_arguments_json",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  "trace log path is missing",
		}
	}
	content, err := os.ReadFile(entry.LogPath)
	if err != nil {
		return Result{
			EvaluatorKey: "tool_call_arguments_json",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  fmt.Sprintf("read trace log: %v", err),
		}
	}
	parsed, err := monitor.ParseLogFile(content)
	if err != nil {
		return Result{
			EvaluatorKey: "tool_call_arguments_json",
			Value:        0,
			Status:       "fail",
			Label:        "fail",
			Explanation:  fmt.Sprintf("parse trace log: %v", err),
		}
	}
	if len(parsed.ResponseToolCalls) == 0 {
		return Result{
			EvaluatorKey: "tool_call_arguments_json",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  "no response tool calls recorded",
		}
	}

	var invalid []string
	for _, call := range parsed.ResponseToolCalls {
		args := strings.TrimSpace(call.Function.Arguments)
		if args == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(args), &value); err != nil {
			name := strings.TrimSpace(call.Function.Name)
			if name == "" {
				name = "<unnamed>"
			}
			invalid = append(invalid, name)
		}
	}
	if len(invalid) == 0 {
		return Result{
			EvaluatorKey: "tool_call_arguments_json",
			Value:        1,
			Status:       "pass",
			Label:        "pass",
			Explanation:  fmt.Sprintf("%d response tool calls had valid JSON arguments", len(parsed.ResponseToolCalls)),
		}
	}
	sort.Strings(invalid)
	return Result{
		EvaluatorKey: "tool_call_arguments_json",
		Value:        0,
		Status:       "fail",
		Label:        "fail",
		Explanation:  fmt.Sprintf("response tool calls have invalid JSON arguments: %s", strings.Join(dedupeAdjacentStrings(invalid), ", ")),
	}
}
