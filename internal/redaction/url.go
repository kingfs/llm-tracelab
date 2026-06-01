package redaction

import (
	"net/url"
	"regexp"
	"strings"
)

const redactedValue = "REDACTED"

var sensitiveURLParamMarkers = []string{
	"key",
	"token",
	"secret",
	"password",
	"passwd",
	"credential",
	"signature",
	"sig",
	"access_token",
	"api_key",
}

var metadataSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|oauth[_-]?token|client[_-]?secret|authorization|x[_-]?api[_-]?key|x[_-]?auth[_-]?token)(\s*[:=]\s*)("[^"]+"|'[^']+'|[^\s,;{}]+)`),
	regexp.MustCompile(`(?is)\{[^{}]*"(type)"\s*:\s*"service_account"[^{}]*\}`),
}

// DisplayURL returns a diagnostics-safe URL string without destroying malformed
// input that may still be useful while debugging configuration.
func DisplayURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.Scheme == "" && parsed.Host == "" && !strings.HasPrefix(raw, "/") {
		return raw
	}
	if parsed.User != nil && parsed.Host != "" {
		username := parsed.User.Username()
		if username != "" {
			parsed.User = url.UserPassword(username, redactedValue)
		} else {
			parsed.User = url.UserPassword(redactedValue, redactedValue)
		}
	}
	if parsed.RawQuery != "" {
		query := parsed.Query()
		for key := range query {
			if isSensitiveURLParam(key) {
				query.Set(key, redactedValue)
			}
		}
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func MetadataText(raw string) string {
	if raw == "" {
		return ""
	}
	out := raw
	for _, pattern := range metadataSecretPatterns {
		out = pattern.ReplaceAllStringFunc(out, redactMetadataSecretMatch)
	}
	return out
}

func SafeCredentialHint(raw string) string {
	hint := strings.TrimSpace(MetadataText(raw))
	if hint == "" || strings.EqualFold(hint, redactedValue) {
		return ""
	}
	if len(hint) > 32 {
		return hint[:32]
	}
	return hint
}

func redactMetadataSecretMatch(match string) string {
	lower := strings.ToLower(match)
	if strings.HasPrefix(lower, "bearer ") {
		return "Bearer " + redactedValue
	}
	if strings.Contains(lower, `"type"`) && strings.Contains(lower, `"service_account"`) {
		return redactedValue
	}
	for _, sep := range []string{":", "="} {
		if idx := strings.Index(match, sep); idx >= 0 {
			return match[:idx+1] + redactedValue
		}
	}
	return redactedValue
}

func isSensitiveURLParam(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	for _, marker := range sensitiveURLParamMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
