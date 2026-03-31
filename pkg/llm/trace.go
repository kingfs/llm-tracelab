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
	endpoint := normalizeEndpoint(rawPath)
	provider := detectProvider(endpoint, upstreamBaseURL)
	operation := detectOperation(endpoint, provider)
	return TraceSemantics{
		Provider:  provider,
		Operation: operation,
		Endpoint:  endpoint,
	}
}

func normalizeEndpoint(rawPath string) string {
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
	return clean
}

func detectProvider(endpoint string, upstreamBaseURL string) string {
	host := strings.ToLower(upstreamBaseURL)
	switch {
	case endpoint == "/v1/messages",
		strings.Contains(host, "anthropic.com"),
		strings.Contains(host, "claude"):
		return ProviderAnthropic
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
