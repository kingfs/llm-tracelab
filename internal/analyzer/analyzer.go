package analyzer

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/observe"
)

const excerptLimit = 240

type Detector interface {
	Name() string
	Version() string
	Detect(context.Context, observe.TraceObservation) ([]observe.Finding, error)
}

type Runner struct {
	detectors []Detector
}

func NewRunner(detectors ...Detector) *Runner {
	if len(detectors) == 0 {
		detectors = DefaultDetectors()
	}
	return &Runner{detectors: detectors}
}

func DefaultDetectors() []Detector {
	return []Detector{
		DangerousShellDetector{},
		CredentialDetector{},
		ProviderSafetyDetector{},
		ToolErrorDetector{},
	}
}

func (r *Runner) Analyze(ctx context.Context, obs observe.TraceObservation) ([]observe.Finding, error) {
	var findings []observe.Finding
	for _, detector := range r.detectors {
		if detector == nil {
			continue
		}
		next, err := detector.Detect(ctx, obs)
		if err != nil {
			return nil, fmt.Errorf("%s detector: %w", detector.Name(), err)
		}
		for i := range next {
			if next[i].TraceID == "" {
				next[i].TraceID = obs.TraceID
			}
			if next[i].Detector == "" {
				next[i].Detector = detector.Name()
			}
			if next[i].DetectorVersion == "" {
				next[i].DetectorVersion = detector.Version()
			}
			if next[i].CreatedAt.IsZero() {
				next[i].CreatedAt = time.Now().UTC()
			}
			if next[i].ID == "" {
				next[i].ID = stableFindingID(next[i])
			}
		}
		findings = append(findings, next...)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity == findings[j].Severity {
			return findings[i].ID < findings[j].ID
		}
		return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
	})
	return findings, nil
}

type DangerousShellDetector struct{}

func (DangerousShellDetector) Name() string    { return "dangerous_shell" }
func (DangerousShellDetector) Version() string { return "0.1.0" }

func (d DangerousShellDetector) Detect(_ context.Context, obs observe.TraceObservation) ([]observe.Finding, error) {
	var findings []observe.Finding
	for _, tool := range obs.Tools.Calls {
		text := firstNonEmpty(tool.ArgsText, rawText(tool.ArgsJSON))
		if text == "" {
			continue
		}
		node := findNodeByID(obs, tool.NodeID)
		if finding, ok := d.detectText(obs.TraceID, node, tool.Kind, text); ok {
			findings = append(findings, finding)
		}
	}
	for _, node := range allNodes(obs) {
		if node.NormalizedType != observe.NodeToolCall && node.NormalizedType != observe.NodeServerToolCall && node.NormalizedType != observe.NodeCode {
			continue
		}
		text := firstNonEmpty(node.Text, metadataString(node.Metadata, "arguments"), rawText(node.Raw), rawText(node.JSON))
		if finding, ok := d.detectText(obs.TraceID, node, node.ProviderType, text); ok {
			findings = append(findings, finding)
		}
	}
	return dedupeFindings(findings), nil
}

func (d DangerousShellDetector) detectText(traceID string, node observe.SemanticNode, kind string, text string) (observe.Finding, bool) {
	lower := strings.ToLower(text)
	rules := []struct {
		category string
		severity observe.Severity
		title    string
		match    func(string) bool
	}{
		{"filesystem_destructive_operation", observe.SeverityCritical, "Destructive filesystem command", func(s string) bool {
			return destructiveRMPattern.MatchString(s) || strings.Contains(s, "sudo rm") ||
				strings.Contains(s, "mkfs") || strings.Contains(s, "diskutil erase") || strings.Contains(s, "dd if=")
		}},
		{"dangerous_command", observe.SeverityHigh, "Dangerous shell command", func(s string) bool {
			return strings.Contains(s, "chmod -r 777") || strings.Contains(s, "chown -r") ||
				strings.Contains(s, "nc -e") || strings.Contains(s, "bash -i")
		}},
		{"unsafe_code_execution", observe.SeverityHigh, "Downloaded script execution", func(s string) bool {
			return (strings.Contains(s, "curl") || strings.Contains(s, "wget")) &&
				(strings.Contains(s, "| sh") || strings.Contains(s, "| bash"))
		}},
		{"credential_leak", observe.SeverityHigh, "Credential file access from shell", func(s string) bool {
			return strings.Contains(s, ".ssh/id_rsa") || strings.Contains(s, ".aws/credentials") ||
				strings.Contains(s, "security find-generic-password") || strings.Contains(s, "env | curl")
		}},
	}
	for _, rule := range rules {
		if rule.match(lower) {
			return finding(traceID, rule.category, rule.severity, 0.92, rule.title,
				"Shell-like tool input matched a deterministic high-risk command pattern.",
				node, excerpt(text), d.Name(), d.Version()), true
		}
	}
	return observe.Finding{}, false
}

type CredentialDetector struct{}

func (CredentialDetector) Name() string    { return "credential" }
func (CredentialDetector) Version() string { return "0.1.0" }

func (d CredentialDetector) Detect(_ context.Context, obs observe.TraceObservation) ([]observe.Finding, error) {
	var findings []observe.Finding
	for _, node := range allNodes(obs) {
		texts := []string{node.Text, metadataString(node.Metadata, "arguments"), rawText(node.Raw), rawText(node.JSON)}
		for _, text := range texts {
			if text == "" {
				continue
			}
			if match, title := credentialMatch(text); match {
				findings = append(findings, finding(obs.TraceID, "credential_leak", observe.SeverityHigh, 0.9, title,
					"Node text or structured payload matched a known credential pattern.",
					node, excerpt(text), d.Name(), d.Version()))
				break
			}
		}
	}
	return dedupeFindings(findings), nil
}

type ProviderSafetyDetector struct{}

func (ProviderSafetyDetector) Name() string    { return "provider_safety" }
func (ProviderSafetyDetector) Version() string { return "0.1.0" }

func (d ProviderSafetyDetector) Detect(_ context.Context, obs observe.TraceObservation) ([]observe.Finding, error) {
	var findings []observe.Finding
	for _, node := range allNodes(obs) {
		switch node.NormalizedType {
		case observe.NodeSafety:
			findings = append(findings, finding(obs.TraceID, "provider_safety_block", observe.SeverityMedium, 0.95, "Provider safety signal",
				"Provider response contained a structured safety or content-filter signal.",
				node, excerpt(firstNonEmpty(node.Text, rawText(node.Raw), rawText(node.JSON))), d.Name(), d.Version()))
		case observe.NodeRefusal:
			findings = append(findings, finding(obs.TraceID, "model_refusal", observe.SeverityLow, 0.95, "Model refusal",
				"Model response contained a refusal node.",
				node, excerpt(firstNonEmpty(node.Text, rawText(node.Raw), rawText(node.JSON))), d.Name(), d.Version()))
		}
	}
	if obs.Safety.Blocked {
		findings = append(findings, finding(obs.TraceID, "provider_safety_block", observe.SeverityMedium, 0.9, "Provider safety block",
			"Observation safety summary indicates the provider blocked or filtered the response.",
			observe.SemanticNode{}, "blocked=true", d.Name(), d.Version()))
	}
	if obs.Safety.Refused {
		findings = append(findings, finding(obs.TraceID, "model_refusal", observe.SeverityLow, 0.9, "Model refusal",
			"Observation safety summary indicates a model refusal.",
			observe.SemanticNode{}, "refused=true", d.Name(), d.Version()))
	}
	return dedupeFindings(findings), nil
}

type ToolErrorDetector struct{}

func (ToolErrorDetector) Name() string    { return "tool_error" }
func (ToolErrorDetector) Version() string { return "0.1.0" }

func (d ToolErrorDetector) Detect(_ context.Context, obs observe.TraceObservation) ([]observe.Finding, error) {
	var findings []observe.Finding
	for _, result := range obs.Tools.Results {
		if !result.IsError {
			continue
		}
		node := findNodeByID(obs, result.NodeID)
		findings = append(findings, finding(obs.TraceID, "tool_result_error", observe.SeverityMedium, 0.95, "Tool result error",
			"Tool result was marked as an error by the provider or parser.",
			node, excerpt(firstNonEmpty(result.Text, rawText(result.JSON))), d.Name(), d.Version()))
	}
	for _, node := range allNodes(obs) {
		if node.NormalizedType != observe.NodeToolResult && node.NormalizedType != observe.NodeServerToolResult && node.NormalizedType != observe.NodeError {
			continue
		}
		status := strings.ToLower(metadataString(node.Metadata, "status"))
		text := strings.ToLower(firstNonEmpty(node.Text, rawText(node.Raw), rawText(node.JSON)))
		if node.NormalizedType == observe.NodeError || strings.Contains(status, "error") || strings.Contains(text, `"error"`) {
			findings = append(findings, finding(obs.TraceID, "tool_result_error", observe.SeverityMedium, 0.9, "Tool or provider error",
				"Tool result or provider output contains an error signal.",
				node, excerpt(firstNonEmpty(node.Text, rawText(node.Raw), rawText(node.JSON))), d.Name(), d.Version()))
		}
	}
	return dedupeFindings(findings), nil
}

var credentialPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"OpenAI API key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{"AWS access key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"GitHub token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`)},
	{"Private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"Bearer token", regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"Database connection string", regexp.MustCompile(`(?i)(postgres|mysql|mongodb)://[^ \n\t]+`)},
	{"Cookie header", regexp.MustCompile(`(?i)\bcookie:\s*[^;\n]+=[^;\n]+`)},
}

var destructiveRMPattern = regexp.MustCompile("(^|[\\s;&|:\"'])rm\\s+-[a-z]*r[a-z]*f[a-z]*\\s+(/|~)([\\s\"'`,}$]|$|[;&|])")

func credentialMatch(text string) (bool, string) {
	for _, pattern := range credentialPatterns {
		if pattern.re.MatchString(text) {
			return true, pattern.name + " exposure"
		}
	}
	if parsed, err := url.Parse(text); err == nil && parsed.User != nil && parsed.Scheme != "" && parsed.Host != "" {
		return true, "Credentialed URL exposure"
	}
	return false, ""
}

func finding(traceID, category string, severity observe.Severity, confidence float64, title, description string, node observe.SemanticNode, evidence, detector, version string) observe.Finding {
	f := observe.Finding{
		TraceID:         traceID,
		Category:        category,
		Severity:        severity,
		Confidence:      confidence,
		Title:           title,
		Description:     description,
		EvidenceExcerpt: evidence,
		NodeID:          node.ID,
		Detector:        detector,
		DetectorVersion: version,
		CreatedAt:       time.Now().UTC(),
	}
	if node.ID != "" || node.Path != "" {
		f.EvidencePath = observe.EvidencePath(traceID, node)
	} else {
		f.EvidencePath = "trace#" + traceID
	}
	f.ID = stableFindingID(f)
	return f
}

func stableFindingID(f observe.Finding) string {
	key := strings.Join([]string{f.TraceID, f.Category, string(f.Severity), f.NodeID, f.EvidencePath, f.Detector, f.DetectorVersion}, "|")
	sum := sha1.Sum([]byte(key))
	return "finding_" + hex.EncodeToString(sum[:])[:16]
}

func allNodes(obs observe.TraceObservation) []observe.SemanticNode {
	var roots []observe.SemanticNode
	roots = append(roots, obs.Request.Nodes...)
	roots = append(roots, obs.Response.Nodes...)
	roots = append(roots, obs.Stream.AccumulatedToolCalls...)
	var out []observe.SemanticNode
	var walk func([]observe.SemanticNode)
	walk = func(nodes []observe.SemanticNode) {
		for _, node := range nodes {
			out = append(out, node)
			walk(node.Children)
		}
	}
	walk(roots)
	return out
}

func findNodeByID(obs observe.TraceObservation, id string) observe.SemanticNode {
	if id == "" {
		return observe.SemanticNode{}
	}
	for _, node := range allNodes(obs) {
		if node.ID == id {
			return node
		}
	}
	return observe.SemanticNode{ID: id}
}

func dedupeFindings(findings []observe.Finding) []observe.Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]observe.Finding, 0, len(findings))
	for _, finding := range findings {
		if finding.ID == "" {
			finding.ID = stableFindingID(finding)
		}
		if _, ok := seen[finding.ID]; ok {
			continue
		}
		seen[finding.ID] = struct{}{}
		out = append(out, finding)
	}
	return out
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.RawMessage:
		return string(typed)
	case []byte:
		return string(typed)
	default:
		buf, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(buf)
	}
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func excerpt(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= excerptLimit {
		return text
	}
	return text[:excerptLimit]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func severityRank(severity observe.Severity) int {
	switch severity {
	case observe.SeverityCritical:
		return 5
	case observe.SeverityHigh:
		return 4
	case observe.SeverityMedium:
		return 3
	case observe.SeverityLow:
		return 2
	case observe.SeverityInfo:
		return 1
	default:
		return 0
	}
}
