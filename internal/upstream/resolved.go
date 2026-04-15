package upstream

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const (
	ProtocolFamilyOpenAICompatible = "openai_compatible"

	RoutingProfileOpenAIDefault     = "openai_default"
	RoutingProfileAzureOpenAIV1     = "azure_openai_v1"
	RoutingProfileAzureOpenAIDeploy = "azure_openai_deployment"
	RoutingProfileVLLMOpenAI        = "vllm_openai"
	ConnectivityPathOpenAIModels    = "/v1/models"
	DefaultAzureAPIVersion          = "preview"
)

type ResolvedUpstream struct {
	BaseURL        string
	APIKey         string
	ProviderPreset string
	ProtocolFamily string
	RoutingProfile string
	APIVersion     string
	Deployment     string
}

func Resolve(cfg config.UpstreamConfig) (ResolvedUpstream, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return ResolvedUpstream{}, fmt.Errorf("upstream.base_url is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ResolvedUpstream{}, fmt.Errorf("invalid upstream.base_url: %w", err)
	}

	resolved := ResolvedUpstream{
		BaseURL:        strings.TrimRight(parsed.String(), "/"),
		APIKey:         cfg.ApiKey,
		ProviderPreset: normalizeSlug(cfg.ProviderPreset),
		ProtocolFamily: normalizeSlug(cfg.ProtocolFamily),
		RoutingProfile: normalizeSlug(cfg.RoutingProfile),
		APIVersion:     strings.TrimSpace(cfg.APIVersion),
		Deployment:     strings.TrimSpace(cfg.Deployment),
	}

	applyPresetDefaults(&resolved, parsed)
	inferDefaults(&resolved, parsed)

	if resolved.ProtocolFamily == "" {
		resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
	}
	if resolved.ProtocolFamily != ProtocolFamilyOpenAICompatible {
		return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.protocol_family %q", resolved.ProtocolFamily)
	}
	if resolved.RoutingProfile == "" {
		resolved.RoutingProfile = RoutingProfileOpenAIDefault
	}

	switch resolved.RoutingProfile {
	case RoutingProfileOpenAIDefault, RoutingProfileVLLMOpenAI:
		return resolved, nil
	case RoutingProfileAzureOpenAIV1:
		if resolved.APIVersion == "" {
			resolved.APIVersion = DefaultAzureAPIVersion
		}
		return resolved, nil
	case RoutingProfileAzureOpenAIDeploy:
		if resolved.APIVersion == "" {
			resolved.APIVersion = DefaultAzureAPIVersion
		}
		if resolved.Deployment == "" {
			resolved.Deployment = deploymentFromBasePath(parsed.Path)
		}
		if resolved.Deployment == "" {
			return ResolvedUpstream{}, fmt.Errorf("upstream.deployment is required for routing_profile=%q", resolved.RoutingProfile)
		}
		return resolved, nil
	default:
		return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.routing_profile %q", resolved.RoutingProfile)
	}
}

func (u ResolvedUpstream) BuildURL(clientPath string) (string, error) {
	target, err := url.Parse(u.BaseURL)
	if err != nil {
		return "", err
	}
	target.Path = joinRequestPath(target, clientPath, u)
	target.RawPath = target.Path
	if u.RoutingProfile == RoutingProfileAzureOpenAIV1 || u.RoutingProfile == RoutingProfileAzureOpenAIDeploy {
		q := target.Query()
		if u.APIVersion != "" && q.Get("api-version") == "" {
			q.Set("api-version", u.APIVersion)
		}
		target.RawQuery = q.Encode()
	}
	return target.String(), nil
}

func (u ResolvedUpstream) ApplyAuthHeaders(header http.Header) {
	if header == nil || u.APIKey == "" {
		return
	}
	switch u.RoutingProfile {
	case RoutingProfileAzureOpenAIV1, RoutingProfileAzureOpenAIDeploy:
		header.Del("Authorization")
		header.Set("api-key", u.APIKey)
	default:
		header.Del("api-key")
		header.Set("Authorization", "Bearer "+u.APIKey)
	}
}

func (u ResolvedUpstream) ConnectivityCheckURL() (string, error) {
	return u.BuildURL(ConnectivityPathOpenAIModels)
}

func applyPresetDefaults(resolved *ResolvedUpstream, parsed *url.URL) {
	switch resolved.ProviderPreset {
	case "openai", "openrouter", "fireworks", "together", "deepseek", "perplexity", "moonshot", "alibaba", "cerebras", "groq", "baseten", "nvidia_nim", "hugging_face":
		if resolved.ProtocolFamily == "" {
			resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
		}
		if resolved.RoutingProfile == "" {
			resolved.RoutingProfile = RoutingProfileOpenAIDefault
		}
	case "azure", "azure_openai":
		if resolved.ProtocolFamily == "" {
			resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
		}
		if resolved.RoutingProfile == "" {
			if resolved.Deployment != "" || strings.Contains(strings.ToLower(parsed.Path), "/deployments/") {
				resolved.RoutingProfile = RoutingProfileAzureOpenAIDeploy
			} else {
				resolved.RoutingProfile = RoutingProfileAzureOpenAIV1
			}
		}
	case "vllm":
		if resolved.ProtocolFamily == "" {
			resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
		}
		if resolved.RoutingProfile == "" {
			resolved.RoutingProfile = RoutingProfileVLLMOpenAI
		}
	}
}

func inferDefaults(resolved *ResolvedUpstream, parsed *url.URL) {
	host := strings.ToLower(parsed.Host)
	basePath := strings.ToLower(parsed.Path)
	if resolved.ProtocolFamily == "" {
		resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
	}
	if resolved.RoutingProfile != "" {
		return
	}
	switch {
	case strings.Contains(host, "azure.com"), strings.Contains(host, "azure.net"), strings.Contains(basePath, "/openai/"):
		if resolved.Deployment != "" || strings.Contains(basePath, "/deployments/") {
			resolved.RoutingProfile = RoutingProfileAzureOpenAIDeploy
		} else {
			resolved.RoutingProfile = RoutingProfileAzureOpenAIV1
		}
	case strings.Contains(host, "vllm"):
		resolved.RoutingProfile = RoutingProfileVLLMOpenAI
	default:
		resolved.RoutingProfile = RoutingProfileOpenAIDefault
	}
}

func joinRequestPath(target *url.URL, clientPath string, resolved ResolvedUpstream) string {
	basePath := cleanURLPath("/")
	if target != nil {
		basePath = cleanURLPath(target.Path)
	}
	reqPath := cleanURLPath(clientPath)
	if reqPath == "/" {
		return basePath
	}
	if resolved.RoutingProfile == RoutingProfileAzureOpenAIDeploy {
		if resolved.Deployment != "" {
			return "/openai/deployments/" + resolved.Deployment + stripOpenAIVersionPrefix(reqPath)
		}
		if strings.Contains(basePath, "/deployments/") {
			return basePath + stripOpenAIVersionPrefix(reqPath)
		}
	}

	trimmedReqPath := reqPath
	switch {
	case strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(reqPath, "/v1/"):
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

func deploymentFromBasePath(basePath string) string {
	parts := strings.Split(cleanURLPath(basePath), "/")
	for idx := range parts {
		if parts[idx] == "deployments" && idx+1 < len(parts) {
			return parts[idx+1]
		}
	}
	return ""
}

func stripOpenAIVersionPrefix(reqPath string) string {
	if strings.HasPrefix(reqPath, "/v1/") {
		return strings.TrimPrefix(reqPath, "/v1")
	}
	return reqPath
}

func normalizeSlug(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
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
