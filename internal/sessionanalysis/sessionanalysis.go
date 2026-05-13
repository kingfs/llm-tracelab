package sessionanalysis

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/observe"
)

const (
	AnalyzerName    = "session_summary"
	AnalyzerVersion = "0.1.0"
	Kind            = "session_summary"
)

type Output struct {
	SessionID        string            `json:"session_id"`
	RequestCount     int               `json:"request_count"`
	SuccessRate      float64           `json:"success_rate"`
	TotalTokens      int               `json:"total_tokens"`
	AvgTTFT          int               `json:"avg_ttft"`
	Models           []Count           `json:"models"`
	Providers        []Count           `json:"providers"`
	Endpoints        []Count           `json:"endpoints"`
	RepeatedFindings []RepeatedFinding `json:"repeated_findings"`
	TraceRefs        []TraceRef        `json:"trace_refs"`
	FindingRefs      []FindingRef      `json:"finding_refs"`
}

type Count struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type RepeatedFinding struct {
	Category string `json:"category"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

type TraceRef struct {
	TraceID    string `json:"trace_id"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	Endpoint   string `json:"endpoint"`
	StatusCode int    `json:"status_code"`
}

type FindingRef struct {
	TraceID   string `json:"trace_id"`
	FindingID string `json:"finding_id"`
	Category  string `json:"category"`
	Severity  string `json:"severity"`
	NodeID    string `json:"node_id,omitempty"`
	Evidence  string `json:"evidence_path"`
}

func Build(summary store.SessionSummary, traces []store.LogEntry, findingsByTrace map[string][]observe.Finding) Output {
	out := Output{
		SessionID:    summary.SessionID,
		RequestCount: summary.RequestCount,
		SuccessRate:  summary.SuccessRate,
		TotalTokens:  summary.TotalTokens,
		AvgTTFT:      summary.AvgTTFT,
	}
	models := map[string]int{}
	providers := map[string]int{}
	endpoints := map[string]int{}
	repeated := map[string]RepeatedFinding{}
	for _, trace := range traces {
		model := firstNonEmpty(trace.Header.Meta.Model, "unknown-model")
		provider := firstNonEmpty(trace.Header.Meta.Provider, "unknown-provider")
		endpoint := firstNonEmpty(trace.Header.Meta.Endpoint, trace.Header.Meta.Operation, trace.Header.Meta.URL, "unknown-endpoint")
		models[model]++
		providers[provider]++
		endpoints[endpoint]++
		out.TraceRefs = append(out.TraceRefs, TraceRef{
			TraceID:    trace.ID,
			Model:      model,
			Provider:   provider,
			Endpoint:   endpoint,
			StatusCode: trace.Header.Meta.StatusCode,
		})
		for _, finding := range findingsByTrace[trace.ID] {
			key := finding.Category + "|" + string(finding.Severity)
			item := repeated[key]
			item.Category = finding.Category
			item.Severity = string(finding.Severity)
			item.Count++
			repeated[key] = item
			out.FindingRefs = append(out.FindingRefs, FindingRef{
				TraceID:   trace.ID,
				FindingID: finding.ID,
				Category:  finding.Category,
				Severity:  string(finding.Severity),
				NodeID:    finding.NodeID,
				Evidence:  finding.EvidencePath,
			})
		}
	}
	out.Models = sortedCounts(models)
	out.Providers = sortedCounts(providers)
	out.Endpoints = sortedCounts(endpoints)
	for _, item := range repeated {
		if item.Count > 1 {
			out.RepeatedFindings = append(out.RepeatedFindings, item)
		}
	}
	sort.Slice(out.RepeatedFindings, func(i, j int) bool {
		if out.RepeatedFindings[i].Count != out.RepeatedFindings[j].Count {
			return out.RepeatedFindings[i].Count > out.RepeatedFindings[j].Count
		}
		return out.RepeatedFindings[i].Category < out.RepeatedFindings[j].Category
	})
	return out
}

func Marshal(output Output) (string, error) {
	buf, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func sortedCounts(values map[string]int) []Count {
	out := make([]Count, 0, len(values))
	for label, count := range values {
		out = append(out, Count{Label: label, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
