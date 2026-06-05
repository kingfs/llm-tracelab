package proxy

import (
	"net/http"
	"path"
	"strings"
)

func normalizeClientEntrypoint(r *http.Request) {
	if r == nil || r.URL == nil {
		return
	}
	normalized := canonicalClientPath(r.URL.Path)
	if normalized == r.URL.Path {
		return
	}
	r.URL.Path = normalized
	r.URL.RawPath = ""
	if r.RequestURI != "" {
		r.RequestURI = r.URL.RequestURI()
	}
}

func canonicalClientPath(rawPath string) string {
	if rawPath == "" {
		return rawPath
	}
	clean := path.Clean(rawPath)
	if clean == "." {
		clean = "/"
	}
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	switch clean {
	case "/responses":
		return "/v1/responses"
	case "/anthropic/messages":
		return "/v1/messages"
	case "/anthropic/v1/messages":
		return "/v1/messages"
	case "/anthropic/messages/count_tokens":
		return "/v1/messages/count_tokens"
	case "/anthropic/v1/messages/count_tokens":
		return "/v1/messages/count_tokens"
	default:
		return rawPath
	}
}
