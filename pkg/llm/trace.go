package llm

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

const (
	ProviderUnknown          = "unknown"
	ProviderOpenAICompatible = "openai_compatible"
	ProviderAzureOpenAI      = "azure_openai"
	ProviderVLLM             = "vllm"
	ProviderAnthropic        = "anthropic"

	OperationUnknown         = "unknown"
	OperationChatCompletions = "chat.completions"
	OperationResponses       = "responses"
	OperationMessages        = "messages"
	OperationEmbeddings      = "embeddings"
	OperationModels          = "models"
)

type TraceSemantics struct {
	Provider  string `json:"provider"`
	Operation string `json:"operation"`
	Endpoint  string `json:"endpoint"`
}

func ClassifyHTTPRequest(req *http.Request, upstreamBaseURL string) TraceSemantics {
	if req == nil {
		return TraceSemantics{
			Provider:  ProviderUnknown,
			Operation: OperationUnknown,
			Endpoint:  "/",
		}
	}
	return ClassifyPath(req.URL.Path, upstreamBaseURL)
}

func ClassifyPath(rawPath string, upstreamBaseURL string) TraceSemantics {
	endpoint := NormalizeEndpoint(rawPath)
	provider := detectProvider(endpoint, upstreamBaseURL)
	operation := detectOperation(endpoint, provider)
	return TraceSemantics{
		Provider:  provider,
		Operation: operation,
		Endpoint:  endpoint,
	}
}

func NormalizeEndpoint(rawPath string) string {
	if rawPath == "" {
		return "/"
	}
	if parsed, err := url.Parse(rawPath); err == nil && parsed.Path != "" {
		rawPath = parsed.Path
	}
	clean := path.Clean(rawPath)
	if clean == "." {
		return "/"
	}
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	for _, rule := range []struct {
		canonical string
		suffixes  []string
	}{
		{canonical: "/v1/chat/completions", suffixes: []string{"/v1/chat/completions", "/chat/completions"}},
		{canonical: "/v1/responses", suffixes: []string{"/v1/responses", "/responses"}},
		{canonical: "/v1/messages", suffixes: []string{"/v1/messages", "/messages"}},
		{canonical: "/v1/embeddings", suffixes: []string{"/v1/embeddings", "/embeddings"}},
		{canonical: "/v1/models", suffixes: []string{"/v1/models", "/models"}},
	} {
		for _, suffix := range rule.suffixes {
			if clean == suffix {
				return rule.canonical
			}
			if strings.HasSuffix(clean, suffix) {
				return rule.canonical
			}
		}
	}
	return clean
}

func detectProvider(endpoint string, upstreamBaseURL string) string {
	parsed, _ := url.Parse(upstreamBaseURL)
	host := strings.ToLower(parsed.Host)
	basePath := strings.ToLower(parsed.Path)
	switch {
	case endpoint == "/v1/messages",
		strings.Contains(host, "anthropic.com"),
		strings.Contains(host, "claude"):
		return ProviderAnthropic
	case isOpenAICompatibleEndpoint(endpoint) &&
		(strings.Contains(host, "azure.com") ||
			strings.Contains(host, "azure.net") ||
			strings.Contains(basePath, "/openai/")):
		return ProviderAzureOpenAI
	case isOpenAICompatibleEndpoint(endpoint) && strings.Contains(host, "vllm"):
		return ProviderVLLM
	case endpoint == "/v1/chat/completions",
		endpoint == "/v1/responses",
		endpoint == "/v1/embeddings",
		endpoint == "/v1/models":
		return ProviderOpenAICompatible
	default:
		return ProviderUnknown
	}
}

func detectOperation(endpoint string, provider string) string {
	switch endpoint {
	case "/v1/chat/completions":
		return OperationChatCompletions
	case "/v1/responses":
		return OperationResponses
	case "/v1/messages":
		return OperationMessages
	case "/v1/embeddings":
		return OperationEmbeddings
	case "/v1/models":
		return OperationModels
	default:
		if provider == ProviderAnthropic {
			return OperationMessages
		}
		return OperationUnknown
	}
}

func isOpenAICompatibleEndpoint(endpoint string) bool {
	switch endpoint {
	case "/v1/chat/completions", "/v1/responses", "/v1/embeddings", "/v1/models":
		return true
	default:
		return false
	}
}

func IsOpenAICompatibleProvider(provider string) bool {
	switch provider {
	case ProviderOpenAICompatible, ProviderAzureOpenAI, ProviderVLLM:
		return true
	default:
		return false
	}
}
