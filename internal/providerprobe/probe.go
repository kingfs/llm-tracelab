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

	CapabilityMessages              = "messages"
	CapabilityGeminiGenerateContent = "gemini_generate_content"
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

type probeSpec struct {
	id             string
	method         string
	path           string
	protocolFamily string
	capability     string
	weight         float64
	auth           authStyle
	body           string
}

type authStyle string

const (
	authOpenAI    authStyle = "openai"
	authAnthropic authStyle = "anthropic"
	authGemini    authStyle = "gemini"
)

var defaultProbeSpecs = []probeSpec{
	{
		id:             "openai_models",
		method:         http.MethodGet,
		path:           "/v1/models",
		protocolFamily: upstream.ProtocolFamilyOpenAICompatible,
		capability:     upstream.CapabilityModels,
		weight:         1,
		auth:           authOpenAI,
	},
	{
		id:             "openai_chat_completions",
		method:         http.MethodPost,
		path:           "/v1/chat/completions",
		protocolFamily: upstream.ProtocolFamilyOpenAICompatible,
		capability:     upstream.CapabilityChatCompletions,
		weight:         3,
		auth:           authOpenAI,
		body:           "{}",
	},
	{
		id:             "openai_responses",
		method:         http.MethodPost,
		path:           "/v1/responses",
		protocolFamily: upstream.ProtocolFamilyOpenAICompatible,
		capability:     upstream.CapabilityResponses,
		weight:         4,
		auth:           authOpenAI,
		body:           "{}",
	},
	{
		id:             "anthropic_messages",
		method:         http.MethodPost,
		path:           "/v1/messages",
		protocolFamily: upstream.ProtocolFamilyAnthropicMessages,
		capability:     CapabilityMessages,
		weight:         4,
		auth:           authAnthropic,
		body:           "{}",
	},
	{
		id:             "anthropic_models",
		method:         http.MethodGet,
		path:           "/v1/models",
		protocolFamily: upstream.ProtocolFamilyAnthropicMessages,
		capability:     upstream.CapabilityModels,
		weight:         1,
		auth:           authAnthropic,
	},
	{
		id:             "gemini_models",
		method:         http.MethodGet,
		path:           "/v1beta/models",
		protocolFamily: upstream.ProtocolFamilyGoogleGenAI,
		capability:     upstream.CapabilityModels,
		weight:         4,
		auth:           authGemini,
	},
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
	for _, spec := range defaultProbeSpecs {
		probe := runEndpointProbe(ctx, target, spec, client)
		report.CheckedEndpoints = append(report.CheckedEndpoints, probe)
		if probe.Error != "" && firstProbeError == "" {
			firstProbeError = probe.Error
		}
		if !probe.Exists {
			continue
		}
		scores[spec.protocolFamily] += spec.weight
		if spec.capability != "" {
			capabilities[spec.capability] = struct{}{}
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

func runEndpointProbe(ctx context.Context, target ProbeTarget, spec probeSpec, client *http.Client) EndpointProbe {
	targetURL, err := buildProbeURL(target.BaseURL, spec.path)
	probe := EndpointProbe{
		ID:             spec.id,
		Method:         spec.method,
		Path:           spec.path,
		URL:            targetURL,
		ProtocolFamily: spec.protocolFamily,
		Status:         EndpointStatusError,
	}
	if spec.capability != "" {
		probe.Capabilities = []string{spec.capability}
	}
	if err != nil {
		probe.Error = err.Error()
		return probe
	}

	var body io.Reader
	if spec.body != "" {
		body = bytes.NewBufferString(spec.body)
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, targetURL, body)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	req.Header.Set("Accept", "application/json")
	if spec.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	applyProbeAuth(req.Header, target, spec.auth)

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
		Capabilities:            sortedCapabilities(capabilities),
	}
	switch bestFamily {
	case upstream.ProtocolFamilyAnthropicMessages:
		out.SuggestedAPIType = upstream.APITypeMessages
	case upstream.ProtocolFamilyGoogleGenAI:
		out.SuggestedAPIType = upstream.APITypeGemini
		out.Capabilities = appendCapability(out.Capabilities, CapabilityGeminiGenerateContent)
	case upstream.ProtocolFamilyOpenAICompatible:
		if _, ok := capabilities[upstream.CapabilityResponses]; ok {
			out.SuggestedAPIType = upstream.APITypeResponses
		} else {
			out.SuggestedAPIType = upstream.APITypeChatCompletions
		}
	}
	out.Confidence = confidenceForScore(bestScore)
	return out
}

func appendCapability(capabilities []string, capability string) []string {
	for _, existing := range capabilities {
		if existing == capability {
			return capabilities
		}
	}
	capabilities = append(capabilities, capability)
	sort.Strings(capabilities)
	return capabilities
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

func applyProbeAuth(header http.Header, target ProbeTarget, auth authStyle) {
	apiKey := strings.TrimSpace(target.APIKey)
	if apiKey != "" {
		switch auth {
		case authAnthropic:
			header.Set("x-api-key", apiKey)
			if header.Get("anthropic-version") == "" {
				header.Set("anthropic-version", upstream.DefaultAnthropicAPIVersion)
			}
		case authGemini:
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
