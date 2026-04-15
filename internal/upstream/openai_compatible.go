package upstream

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/llm"
)

func BuildUpstreamURL(baseURL string, clientPath string) (string, error) {
	target, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	target.Path = JoinRequestPath(target, clientPath)
	target.RawPath = target.Path
	return target.String(), nil
}

func JoinRequestPath(target *url.URL, clientPath string) string {
	basePath := cleanURLPath("/")
	if target != nil {
		basePath = cleanURLPath(target.Path)
	}
	reqPath := cleanURLPath(clientPath)
	if reqPath == "/" {
		return basePath
	}

	trimmedReqPath := reqPath
	switch {
	case strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(reqPath, "/v1/"):
		trimmedReqPath = strings.TrimPrefix(reqPath, "/v1")
	case strings.Contains(basePath, "/deployments/") && strings.HasPrefix(reqPath, "/v1/"):
		trimmedReqPath = strings.TrimPrefix(reqPath, "/v1")
	case llm.NormalizeEndpoint(basePath) == llm.NormalizeEndpoint(reqPath):
		return basePath
	}

	joined := path.Join(basePath, trimmedReqPath)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	return joined
}

func ApplyAuthHeaders(header http.Header, baseURL string, apiKey string) {
	if apiKey == "" || header == nil {
		return
	}
	if IsAzureBaseURL(baseURL) {
		header.Del("Authorization")
		header.Set("api-key", apiKey)
		return
	}
	header.Del("api-key")
	header.Set("Authorization", "Bearer "+apiKey)
}

func IsAzureBaseURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	basePath := strings.ToLower(parsed.Path)
	return strings.Contains(host, "azure.com") ||
		strings.Contains(host, "azure.net") ||
		strings.Contains(basePath, "/openai/")
}

func cleanURLPath(raw string) string {
	if raw == "" {
		return "/"
	}
	clean := path.Clean(raw)
	if clean == "." {
		return "/"
	}
	if !strings.HasPrefix(clean, "/") {
		return "/" + clean
	}
	return clean
}
