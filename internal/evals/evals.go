package evals

import (
	"fmt"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/replay"
)

const BaselineEvaluatorSet = "baseline_v1"

type Result struct {
	EvaluatorKey string  `json:"evaluator_key"`
	Value        float64 `json:"value"`
	Status       string  `json:"status"`
	Label        string  `json:"label"`
	Explanation  string  `json:"explanation"`
}

func EvaluateBaseline(entry store.LogEntry, summary *replay.Summary) []Result {
	results := []Result{
		status2xx(entry),
		noRecordedError(entry),
		responseHasBody(summary),
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
