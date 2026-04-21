package evals

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/replay"
)

const BaselineEvaluatorSet = "baseline_v2"

type Result struct {
	EvaluatorKey string  `json:"evaluator_key"`
	Value        float64 `json:"value"`
	Status       string  `json:"status"`
	Label        string  `json:"label"`
	Explanation  string  `json:"explanation"`
}

type Profile struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Deterministic    bool     `json:"deterministic"`
	TTFTBudgetMS     int      `json:"ttft_budget_ms,omitempty"`
	TotalTokenBudget int      `json:"total_token_budget,omitempty"`
	EvaluatorKeys    []string `json:"evaluator_keys"`
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
