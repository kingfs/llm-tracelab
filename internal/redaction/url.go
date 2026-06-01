package redaction

import (
	"net/url"
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

// DisplayURL returns a diagnostics-safe URL string without destroying malformed
// input that may still be useful while debugging configuration.
func DisplayURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	if parsed.User != nil {
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
