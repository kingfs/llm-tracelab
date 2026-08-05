package providerprobe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/upstream"
)

const (
	StatusDetected = "detected"
	StatusUnknown  = "unknown"
	StatusError    = "error"

	EndpointStatusExists  = "exists"
	EndpointStatusMissing = "missing"
	EndpointStatusError   = "error"

	CapabilityMessages              = upstream.CapabilityMessages
	CapabilityGeminiGenerateContent = upstream.CapabilityGenerateContent
)

type ProbeTarget struct {
	TargetSource            string            `json:"target_source,omitempty"`
	ProviderID              string            `json:"provider_id,omitempty"`
	BaseURL                 string            `json:"base_url"`
	APIKey                  string            `json:"-"`
	Headers                 map[string]string `json:"headers,omitempty"`
	SpecifiedAPIType        string            `json:"specified_api_type,omitempty"`
	SpecifiedProtocolFamily string            `json:"specified_protocol_family,omitempty"`
}

type CapabilitySuggestion struct {
	SuggestedAPIType        string   `json:"suggested_api_type,omitempty"`
	SuggestedProtocolFamily string   `json:"suggested_protocol_family,omitempty"`
	Capabilities            []string `json:"capabilities,omitempty"`
	Confidence              float64  `json:"confidence"`
}

type Report struct {
	TargetSource            string          `json:"target_source,omitempty"`
	ProviderID              string          `json:"provider_id,omitempty"`
	BaseURL                 string          `json:"base_url"`
	SpecifiedAPIType        string          `json:"specified_api_type,omitempty"`
	SpecifiedProtocolFamily string          `json:"specified_protocol_family,omitempty"`
	CheckedEndpoints        []EndpointProbe `json:"checked_endpoints"`
	Status                  string          `json:"status"`
	Error                   string          `json:"error,omitempty"`
	Warnings                []string        `json:"warnings,omitempty"`
	CapabilitySuggestion
}

type BatchReport struct {
	Reports []Report `json:"reports"`
}

type EndpointProbe struct {
	ID             string   `json:"id"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	URL            string   `json:"url"`
	ProtocolFamily string   `json:"protocol_family"`
	Status         string   `json:"status"`
	StatusCode     int      `json:"status_code,omitempty"`
	StatusText     string   `json:"status_text,omitempty"`
	Error          string   `json:"error,omitempty"`
	Exists         bool     `json:"exists"`
	Capabilities   []string `json:"capabilities,omitempty"`
}

func Probe(ctx context.Context, target ProbeTarget, client *http.Client) (Report, error) {
	report := Report{
		TargetSource:            normalize(target.TargetSource),
		ProviderID:              strings.TrimSpace(target.ProviderID),
		BaseURL:                 strings.TrimSpace(target.BaseURL),
		SpecifiedAPIType:        normalize(target.SpecifiedAPIType),
		SpecifiedProtocolFamily: normalize(target.SpecifiedProtocolFamily),
		Status:                  StatusUnknown,
	}
	if report.BaseURL == "" {
		err := fmt.Errorf("providerprobe: base_url is required")
		report.Status = StatusError
		report.Error = err.Error()
		return report, err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	scores := map[string]float64{}
	capabilities := map[string]struct{}{}
	var firstProbeError string
	for _, spec := range upstream.ProviderProbeSpecs() {
		probe := runEndpointProbe(ctx, target, spec, client)
		report.CheckedEndpoints = append(report.CheckedEndpoints, probe)
		if probe.Error != "" && firstProbeError == "" {
			firstProbeError = probe.Error
		}
		if !probe.Exists {
			continue
		}
		scores[spec.ProtocolFamily] += spec.Weight
		if spec.Capability != "" {
			capabilities[spec.Capability] = struct{}{}
		}
	}

	report.CapabilitySuggestion = buildSuggestion(scores, capabilities)
	report.Warnings = append(report.Warnings, specifiedMismatchWarnings(report)...)
	switch {
	case report.SuggestedProtocolFamily != "":
		report.Status = StatusDetected
	case firstProbeError != "" && allEndpointProbesErrored(report.CheckedEndpoints):
		report.Status = StatusError
		report.Error = firstProbeError
	default:
		report.Status = StatusUnknown
	}
	if report.Status == StatusUnknown {
		report.Warnings = append(report.Warnings, "no known provider endpoint signals were detected")
	}
	return report, nil
}

func ProbeBatch(ctx context.Context, targets []ProbeTarget, client *http.Client) BatchReport {
	reports := make([]Report, 0, len(targets))
	for _, target := range targets {
		report, _ := Probe(ctx, target, client)
		reports = append(reports, report)
	}
	return BatchReport{Reports: reports}
}

func runEndpointProbe(ctx context.Context, target ProbeTarget, spec upstream.ProviderProbeSpec, client *http.Client) EndpointProbe {
	targetURL, err := buildProbeURL(target.BaseURL, spec.Path)
	probe := EndpointProbe{
		ID:             spec.ID,
		Method:         spec.Method,
		Path:           spec.Path,
		URL:            targetURL,
		ProtocolFamily: spec.ProtocolFamily,
		Status:         EndpointStatusError,
	}
	if spec.Capability != "" {
		probe.Capabilities = []string{spec.Capability}
	}
	if err != nil {
		probe.Error = err.Error()
		return probe
	}

	var body io.Reader
	if spec.Body != "" {
		body = bytes.NewBufferString(spec.Body)
	}
	req, err := http.NewRequestWithContext(ctx, spec.Method, targetURL, body)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	req.Header.Set("Accept", "application/json")
	if spec.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	applyProbeAuth(req.Header, target, spec.Auth)

	resp, err := client.Do(req)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	probe.StatusCode = resp.StatusCode
	probe.StatusText = resp.Status
	probe.Exists = endpointExists(resp.StatusCode)
	if probe.Exists {
		probe.Status = EndpointStatusExists
		return probe
	}
	probe.Status = EndpointStatusMissing
	return probe
}

func buildSuggestion(scores map[string]float64, capabilities map[string]struct{}) CapabilitySuggestion {
	bestFamily := ""
	bestScore := 0.0
	for family, score := range scores {
		if score > bestScore || (score == bestScore && family < bestFamily) {
			bestFamily = family
			bestScore = score
		}
	}
	if bestFamily == "" {
		return CapabilitySuggestion{Confidence: 0}
	}

	out := CapabilitySuggestion{
		SuggestedProtocolFamily: bestFamily,
		Capabilities:            upstream.AppendImpliedCapabilities(bestFamily, sortedCapabilities(capabilities)),
	}
	out.SuggestedAPIType = upstream.SuggestedAPIType(bestFamily, out.Capabilities)
	out.Confidence = confidenceForScore(bestScore)
	return out
}

func specifiedMismatchWarnings(report Report) []string {
	var warnings []string
	if report.SpecifiedAPIType != "" && report.SuggestedAPIType != "" && report.SpecifiedAPIType != report.SuggestedAPIType {
		warnings = append(warnings, fmt.Sprintf("specified api_type %q differs from probed suggestion %q", report.SpecifiedAPIType, report.SuggestedAPIType))
	}
	if report.SpecifiedProtocolFamily != "" && report.SuggestedProtocolFamily != "" && report.SpecifiedProtocolFamily != report.SuggestedProtocolFamily {
		warnings = append(warnings, fmt.Sprintf("specified protocol_family %q differs from probed suggestion %q", report.SpecifiedProtocolFamily, report.SuggestedProtocolFamily))
	}
	return warnings
}

func endpointExists(statusCode int) bool {
	if statusCode >= 200 && statusCode < 300 {
		return true
	}
	switch statusCode {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusMethodNotAllowed,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func allEndpointProbesErrored(probes []EndpointProbe) bool {
	if len(probes) == 0 {
		return false
	}
	for _, probe := range probes {
		if probe.Error == "" {
			return false
		}
	}
	return true
}

func sortedCapabilities(capabilities map[string]struct{}) []string {
	out := make([]string, 0, len(capabilities))
	for capability := range capabilities {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

func confidenceForScore(score float64) float64 {
	switch {
	case score >= 5:
		return 0.9
	case score >= 4:
		return 0.8
	case score >= 3:
		return 0.7
	case score >= 1:
		return 0.45
	default:
		return 0
	}
}

func buildProbeURL(baseURL string, endpointPath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base_url %q", baseURL)
	}
	parsed.Path = joinProbePath(parsed.Path, endpointPath)
	parsed.RawPath = ""
	return parsed.String(), nil
}

func joinProbePath(basePath string, endpointPath string) string {
	base := cleanPath(basePath)
	endpoint := cleanPath(endpointPath)
	if base == "/" {
		return endpoint
	}
	for _, prefix := range []string{"/v1beta", "/v1"} {
		if strings.HasSuffix(base, prefix) && strings.HasPrefix(endpoint, prefix+"/") {
			endpoint = strings.TrimPrefix(endpoint, prefix)
			break
		}
	}
	joined := path.Join(base, endpoint)
	if !strings.HasPrefix(joined, "/") {
		return "/" + joined
	}
	return joined
}

func cleanPath(value string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(value))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func applyProbeAuth(header http.Header, target ProbeTarget, auth string) {
	apiKey := strings.TrimSpace(target.APIKey)
	if apiKey != "" {
		switch auth {
		case upstream.ProbeAuthAnthropic:
			header.Set("x-api-key", apiKey)
			if header.Get("anthropic-version") == "" {
				header.Set("anthropic-version", upstream.DefaultAnthropicAPIVersion)
			}
		case upstream.ProbeAuthGemini:
			header.Set("x-goog-api-key", apiKey)
		default:
			header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	for key, value := range target.Headers {
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		header.Set(key, value)
	}
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
