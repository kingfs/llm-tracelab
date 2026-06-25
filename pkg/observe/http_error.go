package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func appendHTTPErrorResponseIfNonLLM(input ParseInput, obs *TraceObservation) bool {
	statusCode := input.Header.Meta.StatusCode
	if statusCode < 400 {
		return false
	}
	body := bytes.TrimSpace(input.ResponseBody)
	if len(body) == 0 {
		appendHTTPErrorResponseNode(input, obs, "HTTP "+strconv.Itoa(statusCode), nil)
		return true
	}
	if _, err := decodeJSONObject(body); err == nil {
		return false
	}
	appendHTTPErrorResponseNode(input, obs, responseErrorText(body), body)
	return true
}

func appendHTTPErrorResponseNode(input ParseInput, obs *TraceObservation, text string, raw []byte) {
	statusCode := input.Header.Meta.StatusCode
	if text == "" {
		text = "HTTP " + strconv.Itoa(statusCode)
	}
	node := SemanticNode{
		ID:             StableNodeID("response", "$.http_error", "http_error", 0),
		ProviderType:   "http_error",
		NormalizedType: NodeError,
		Path:           "$.http_error",
		Text:           text,
		Metadata: map[string]any{
			"status_code":    statusCode,
			"endpoint":       input.Header.Meta.Endpoint,
			"operation":      input.Header.Meta.Operation,
			"non_llm_body":   true,
			"body_truncated": len(bytes.TrimSpace(raw)) > 4096,
		},
	}
	if len(bytes.TrimSpace(raw)) > 0 && json.Valid(raw) {
		node.Raw = cloneRaw(raw)
	}
	obs.Response.Errors = append(obs.Response.Errors, node)
	obs.Response.Nodes = append(obs.Response.Nodes, node)
	obs.Warnings = append(obs.Warnings, ParseWarning{
		Code:    "http_error_response",
		Message: fmt.Sprintf("HTTP %d response is not a provider JSON object; recorded as transport/proxy error", statusCode),
		Path:    "$.http_error",
	})
}

func responseErrorText(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	text = strings.ToValidUTF8(text, "\uFFFD")
	if len(text) <= 4096 {
		return text
	}
	return text[:4096]
}
