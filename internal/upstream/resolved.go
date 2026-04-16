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
	ProtocolFamilyOpenAICompatible  = "openai_compatible"
	ProtocolFamilyAnthropicMessages = "anthropic_messages"
	ProtocolFamilyGoogleGenAI       = "google_genai"
	ProtocolFamilyVertexNative      = "vertex_native"

	RoutingProfileOpenAIDefault     = "openai_default"
	RoutingProfileAzureOpenAIV1     = "azure_openai_v1"
	RoutingProfileAzureOpenAIDeploy = "azure_openai_deployment"
	RoutingProfileVLLMOpenAI        = "vllm_openai"
	RoutingProfileAnthropicDefault  = "anthropic_default"
	RoutingProfileGoogleAIStudio    = "google_ai_studio"
	RoutingProfileVertexExpress     = "vertex_express"
	RoutingProfileVertexProject     = "vertex_project_location"
	ConnectivityPathOpenAIModels    = "/v1/models"
	ConnectivityPathAnthropicModels = "/v1/models"
	ConnectivityPathGoogleModels    = "/v1beta/models"
	ConnectivityPathVertexModels    = "/v1/publishers/google/models"
	DefaultAzureAPIVersion          = "preview"
	DefaultAnthropicAPIVersion      = "2023-06-01"
)

type providerPresetSpec struct {
	ProtocolFamily  string
	RoutingProfile  string
	SupportLevel    string
	AllowedProfiles []string
}

var providerPresetRegistry = map[string]providerPresetSpec{
	"alibaba":          {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"anthropic":        {ProtocolFamily: ProtocolFamilyAnthropicMessages, RoutingProfile: RoutingProfileAnthropicDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileAnthropicDefault}},
	"azure":            {ProtocolFamily: ProtocolFamilyOpenAICompatible, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileAzureOpenAIV1, RoutingProfileAzureOpenAIDeploy}},
	"azure_openai":     {ProtocolFamily: ProtocolFamilyOpenAICompatible, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileAzureOpenAIV1, RoutingProfileAzureOpenAIDeploy}},
	"baseten":          {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"cerebras":         {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"deepseek":         {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"fireworks":        {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"github":           {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"github_models":    {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"groq":             {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"google":           {ProtocolFamily: ProtocolFamilyGoogleGenAI, RoutingProfile: RoutingProfileGoogleAIStudio, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileGoogleAIStudio}},
	"google_ai_studio": {ProtocolFamily: ProtocolFamilyGoogleGenAI, RoutingProfile: RoutingProfileGoogleAIStudio, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileGoogleAIStudio}},
	"google_genai":     {ProtocolFamily: ProtocolFamilyGoogleGenAI, RoutingProfile: RoutingProfileGoogleAIStudio, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileGoogleAIStudio}},
	"gemini":           {ProtocolFamily: ProtocolFamilyGoogleGenAI, RoutingProfile: RoutingProfileGoogleAIStudio, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileGoogleAIStudio}},
	"hugging_face":     {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"moonshot":         {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"nvidia_nim":       {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"openai":           {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"openrouter":       {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"perplexity":       {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "compatible", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"together":         {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
	"vllm":             {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileVLLMOpenAI, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileVLLMOpenAI}},
	"vertex":           {ProtocolFamily: ProtocolFamilyVertexNative, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileVertexExpress, RoutingProfileVertexProject}},
	"xai":              {ProtocolFamily: ProtocolFamilyOpenAICompatible, RoutingProfile: RoutingProfileOpenAIDefault, SupportLevel: "verified", AllowedProfiles: []string{RoutingProfileOpenAIDefault}},
}

type ResolvedUpstream struct {
	BaseURL        string
	APIKey         string
	ProviderPreset string
	ProtocolFamily string
	RoutingProfile string
	APIVersion     string
	Deployment     string
	Project        string
	Location       string
	ModelResource  string
	Headers        map[string]string
}

type StartupDiagnostics struct {
	ConnectivityEndpoint string
	ConnectivityURL      string
	ModelRoutingHint     string
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
		Project:        strings.TrimSpace(cfg.Project),
		Location:       strings.TrimSpace(cfg.Location),
		ModelResource:  strings.Trim(strings.TrimSpace(cfg.ModelResource), "/"),
		Headers:        cloneStringMap(cfg.Headers),
	}
	if err := validatePresetSelection(resolved.ProviderPreset, resolved.ProtocolFamily); err != nil {
		return ResolvedUpstream{}, err
	}

	applyPresetDefaults(&resolved, parsed)
	inferDefaults(&resolved, parsed)

	if resolved.ProtocolFamily == "" {
		resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
	}
	switch resolved.ProtocolFamily {
	case ProtocolFamilyOpenAICompatible:
		if resolved.RoutingProfile == "" {
			resolved.RoutingProfile = RoutingProfileOpenAIDefault
		}
		switch resolved.RoutingProfile {
		case RoutingProfileOpenAIDefault, RoutingProfileVLLMOpenAI:
			if err := validateResolvedPreset(resolved); err != nil {
				return ResolvedUpstream{}, err
			}
			return resolved, nil
		case RoutingProfileAzureOpenAIV1:
			if resolved.APIVersion == "" {
				resolved.APIVersion = DefaultAzureAPIVersion
			}
			if err := validateResolvedPreset(resolved); err != nil {
				return ResolvedUpstream{}, err
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
			if err := validateResolvedPreset(resolved); err != nil {
				return ResolvedUpstream{}, err
			}
			return resolved, nil
		default:
			return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.routing_profile %q", resolved.RoutingProfile)
		}
	case ProtocolFamilyAnthropicMessages:
		if resolved.RoutingProfile == "" {
			resolved.RoutingProfile = RoutingProfileAnthropicDefault
		}
		if resolved.RoutingProfile != RoutingProfileAnthropicDefault {
			return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.routing_profile %q for protocol_family=%q", resolved.RoutingProfile, resolved.ProtocolFamily)
		}
		if resolved.APIVersion == "" {
			resolved.APIVersion = DefaultAnthropicAPIVersion
		}
		if err := validateResolvedPreset(resolved); err != nil {
			return ResolvedUpstream{}, err
		}
		return resolved, nil
	case ProtocolFamilyGoogleGenAI:
		if resolved.RoutingProfile == "" {
			resolved.RoutingProfile = RoutingProfileGoogleAIStudio
		}
		if resolved.RoutingProfile != RoutingProfileGoogleAIStudio {
			return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.routing_profile %q for protocol_family=%q", resolved.RoutingProfile, resolved.ProtocolFamily)
		}
		if err := validateResolvedPreset(resolved); err != nil {
			return ResolvedUpstream{}, err
		}
		return resolved, nil
	case ProtocolFamilyVertexNative:
		if resolved.RoutingProfile == "" {
			resolved.RoutingProfile = RoutingProfileVertexExpress
		}
		switch resolved.RoutingProfile {
		case RoutingProfileVertexExpress:
			if resolved.ModelResource == "" {
				return ResolvedUpstream{}, fmt.Errorf("upstream.model_resource is required for routing_profile=%q", resolved.RoutingProfile)
			}
			if err := validateResolvedPreset(resolved); err != nil {
				return ResolvedUpstream{}, err
			}
			return resolved, nil
		case RoutingProfileVertexProject:
			if resolved.Project == "" {
				return ResolvedUpstream{}, fmt.Errorf("upstream.project is required for routing_profile=%q", resolved.RoutingProfile)
			}
			if resolved.Location == "" {
				return ResolvedUpstream{}, fmt.Errorf("upstream.location is required for routing_profile=%q", resolved.RoutingProfile)
			}
			if resolved.ModelResource == "" {
				return ResolvedUpstream{}, fmt.Errorf("upstream.model_resource is required for routing_profile=%q", resolved.RoutingProfile)
			}
			if err := validateResolvedPreset(resolved); err != nil {
				return ResolvedUpstream{}, err
			}
			return resolved, nil
		default:
			return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.routing_profile %q for protocol_family=%q", resolved.RoutingProfile, resolved.ProtocolFamily)
		}
	default:
		return ResolvedUpstream{}, fmt.Errorf("unsupported upstream.protocol_family %q", resolved.ProtocolFamily)
	}
}

func validatePresetSelection(providerPreset string, protocolFamily string) error {
	if providerPreset == "" {
		return nil
	}
	spec, ok := providerPresetRegistry[providerPreset]
	if !ok {
		return fmt.Errorf("unsupported upstream.provider_preset %q", providerPreset)
	}
	if protocolFamily != "" && spec.ProtocolFamily != "" && protocolFamily != spec.ProtocolFamily {
		return fmt.Errorf("upstream.provider_preset=%q requires protocol_family=%q, got %q", providerPreset, spec.ProtocolFamily, protocolFamily)
	}
	return nil
}

func validateResolvedPreset(resolved ResolvedUpstream) error {
	if resolved.ProviderPreset == "" {
		return nil
	}
	spec, ok := providerPresetRegistry[resolved.ProviderPreset]
	if !ok {
		return fmt.Errorf("unsupported upstream.provider_preset %q", resolved.ProviderPreset)
	}
	if spec.ProtocolFamily != "" && resolved.ProtocolFamily != spec.ProtocolFamily {
		return fmt.Errorf("upstream.provider_preset=%q resolved to protocol_family=%q, got %q", resolved.ProviderPreset, spec.ProtocolFamily, resolved.ProtocolFamily)
	}
	if len(spec.AllowedProfiles) == 0 || resolved.RoutingProfile == "" {
		return nil
	}
	for _, profile := range spec.AllowedProfiles {
		if resolved.RoutingProfile == profile {
			return nil
		}
	}
	return fmt.Errorf("upstream.provider_preset=%q does not support routing_profile=%q", resolved.ProviderPreset, resolved.RoutingProfile)
}

func PresetSupportMatrix() map[string]providerPresetSpec {
	out := make(map[string]providerPresetSpec, len(providerPresetRegistry))
	for key, value := range providerPresetRegistry {
		out[key] = value
	}
	return out
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
	if u.RoutingProfile == RoutingProfileGoogleAIStudio && strings.Contains(target.Path, ":streamGenerateContent") {
		q := target.Query()
		if q.Get("alt") == "" {
			q.Set("alt", "sse")
		}
		target.RawQuery = q.Encode()
	}
	if (u.RoutingProfile == RoutingProfileVertexExpress || u.RoutingProfile == RoutingProfileVertexProject) && strings.Contains(target.Path, ":streamGenerateContent") {
		q := target.Query()
		if q.Get("alt") == "" {
			q.Set("alt", "sse")
		}
		target.RawQuery = q.Encode()
	}
	return target.String(), nil
}

func (u ResolvedUpstream) ApplyAuthHeaders(header http.Header) {
	if header == nil || u.APIKey == "" {
		applyStaticHeaders(header, u.Headers)
		return
	}
	switch u.RoutingProfile {
	case RoutingProfileAzureOpenAIV1, RoutingProfileAzureOpenAIDeploy:
		header.Del("Authorization")
		header.Del("x-api-key")
		header.Set("api-key", u.APIKey)
	case RoutingProfileAnthropicDefault:
		header.Del("Authorization")
		header.Del("api-key")
		header.Del("x-goog-api-key")
		header.Set("x-api-key", u.APIKey)
		if u.APIVersion != "" && header.Get("anthropic-version") == "" {
			header.Set("anthropic-version", u.APIVersion)
		}
	case RoutingProfileGoogleAIStudio:
		header.Del("Authorization")
		header.Del("api-key")
		header.Del("x-api-key")
		header.Set("x-goog-api-key", u.APIKey)
	default:
		header.Del("api-key")
		header.Del("x-api-key")
		header.Del("x-goog-api-key")
		header.Set("Authorization", "Bearer "+u.APIKey)
	}
	applyStaticHeaders(header, u.Headers)
}

func (u ResolvedUpstream) ConnectivityCheckURL() (string, error) {
	switch u.ProtocolFamily {
	case ProtocolFamilyAnthropicMessages:
		return u.BuildURL(ConnectivityPathAnthropicModels)
	case ProtocolFamilyGoogleGenAI:
		return u.BuildURL(ConnectivityPathGoogleModels)
	case ProtocolFamilyVertexNative:
		return u.BuildURL(u.vertexConnectivityPath())
	default:
		return u.BuildURL(ConnectivityPathOpenAIModels)
	}
}

func (u ResolvedUpstream) ConnectivityCheckEndpoint() string {
	switch u.ProtocolFamily {
	case ProtocolFamilyAnthropicMessages:
		return ConnectivityPathAnthropicModels
	case ProtocolFamilyGoogleGenAI:
		return ConnectivityPathGoogleModels
	case ProtocolFamilyVertexNative:
		return u.vertexConnectivityPath()
	default:
		return ConnectivityPathOpenAIModels
	}
}

func (u ResolvedUpstream) vertexConnectivityPath() string {
	resourceBase := strings.Trim(u.ModelResource, "/")
	if resourceBase == "" {
		resourceBase = "publishers/google/models"
	}
	if u.RoutingProfile == RoutingProfileVertexProject {
		return "/v1/projects/" + u.Project + "/locations/" + u.Location + "/" + resourceBase
	}
	return "/v1/" + resourceBase
}

func (u ResolvedUpstream) ModelRoutingHint() string {
	switch u.ProtocolFamily {
	case ProtocolFamilyGoogleGenAI:
		return "model is selected in the request path; normalized endpoint /v1beta/models:generateContent maps to concrete /v1beta/models/{model}:generateContent"
	case ProtocolFamilyVertexNative:
		switch u.RoutingProfile {
		case RoutingProfileVertexExpress:
			return "model is selected in the request path under /v1/{model_resource}/{model}:generateContent"
		case RoutingProfileVertexProject:
			return "model is selected in the request path under /v1/projects/{project}/locations/{location}/{model_resource}/{model}:generateContent"
		default:
			return "model is selected in the request path for vertex-native requests"
		}
	case ProtocolFamilyAnthropicMessages:
		return "model stays in the request body as messages.model"
	default:
		switch u.RoutingProfile {
		case RoutingProfileAzureOpenAIDeploy:
			return "model is selected by deployment in the request path; client body model is not used for upstream routing"
		default:
			return "model stays in the request body"
		}
	}
}

func (u ResolvedUpstream) StartupDiagnostics() (StartupDiagnostics, error) {
	connectivityURL, err := u.ConnectivityCheckURL()
	if err != nil {
		return StartupDiagnostics{}, err
	}
	return StartupDiagnostics{
		ConnectivityEndpoint: u.ConnectivityCheckEndpoint(),
		ConnectivityURL:      connectivityURL,
		ModelRoutingHint:     u.ModelRoutingHint(),
	}, nil
}

func applyPresetDefaults(resolved *ResolvedUpstream, parsed *url.URL) {
	spec, ok := providerPresetRegistry[resolved.ProviderPreset]
	if !ok {
		return
	}
	if resolved.ProtocolFamily == "" {
		resolved.ProtocolFamily = spec.ProtocolFamily
	}
	if resolved.RoutingProfile != "" {
		return
	}
	switch resolved.ProviderPreset {
	case "azure", "azure_openai":
		if resolved.Deployment != "" || strings.Contains(strings.ToLower(parsed.Path), "/deployments/") {
			resolved.RoutingProfile = RoutingProfileAzureOpenAIDeploy
		} else {
			resolved.RoutingProfile = RoutingProfileAzureOpenAIV1
		}
	case "vertex":
		if strings.EqualFold(parsed.Host, "aiplatform.googleapis.com") {
			resolved.RoutingProfile = RoutingProfileVertexExpress
		} else {
			resolved.RoutingProfile = RoutingProfileVertexProject
		}
	default:
		resolved.RoutingProfile = spec.RoutingProfile
	}
}

func inferDefaults(resolved *ResolvedUpstream, parsed *url.URL) {
	host := strings.ToLower(parsed.Host)
	basePath := strings.ToLower(parsed.Path)
	if resolved.RoutingProfile != "" {
		return
	}
	if resolved.ProtocolFamily == "" {
		switch {
		case strings.Contains(host, "anthropic.com"), strings.Contains(host, "claude"):
			resolved.ProtocolFamily = ProtocolFamilyAnthropicMessages
		case strings.Contains(host, "aiplatform.googleapis.com"):
			resolved.ProtocolFamily = ProtocolFamilyVertexNative
		case strings.Contains(host, "generativelanguage.googleapis.com"), strings.Contains(host, "googleapis.com"):
			resolved.ProtocolFamily = ProtocolFamilyGoogleGenAI
		default:
			resolved.ProtocolFamily = ProtocolFamilyOpenAICompatible
		}
	}
	if resolved.ProtocolFamily == ProtocolFamilyAnthropicMessages {
		resolved.RoutingProfile = RoutingProfileAnthropicDefault
		return
	}
	if resolved.ProtocolFamily == ProtocolFamilyGoogleGenAI {
		resolved.RoutingProfile = RoutingProfileGoogleAIStudio
		return
	}
	if resolved.ProtocolFamily == ProtocolFamilyVertexNative {
		if host == "aiplatform.googleapis.com" {
			resolved.RoutingProfile = RoutingProfileVertexExpress
		} else {
			resolved.RoutingProfile = RoutingProfileVertexProject
		}
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

func applyStaticHeaders(header http.Header, values map[string]string) {
	for key, value := range values {
		if strings.TrimSpace(key) == "" || value == "" {
			continue
		}
		header.Set(key, value)
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
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
	if resolved.RoutingProfile == RoutingProfileVertexExpress || resolved.RoutingProfile == RoutingProfileVertexProject {
		return joinVertexRequestPath(reqPath, resolved)
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

func joinVertexRequestPath(reqPath string, resolved ResolvedUpstream) string {
	resourceBase := resolved.ModelResource
	if resourceBase == "" {
		resourceBase = "publishers/google/models"
	}
	resourceBase = strings.Trim(resourceBase, "/")
	resourceRoot := "/" + resourceBase
	if resolved.RoutingProfile == RoutingProfileVertexProject {
		resourceRoot = "/projects/" + resolved.Project + "/locations/" + resolved.Location + resourceRoot
	}
	if strings.HasPrefix(reqPath, "/v1"+resourceRoot) {
		return reqPath
	}
	if resolved.RoutingProfile == RoutingProfileVertexExpress && strings.HasPrefix(reqPath, "/v1/publishers/") {
		return reqPath
	}
	switch llm.NormalizeEndpoint(reqPath) {
	case "/v1/publishers/models:generateContent":
		return "/v1" + resourceRoot + suffixAfterModel(reqPath, ":generateContent")
	case "/v1/publishers/models:streamGenerateContent":
		return "/v1" + resourceRoot + suffixAfterModel(reqPath, ":streamGenerateContent")
	case "/v1/publishers/models":
		return "/v1" + resourceRoot
	default:
		return path.Join("/v1", resourceRoot, strings.TrimPrefix(reqPath, "/v1/"))
	}
}

func suffixAfterModel(reqPath string, fallback string) string {
	if idx := strings.Index(reqPath, "/models/"); idx >= 0 {
		rest := reqPath[idx+len("/models/"):]
		if rest != "" {
			return "/models/" + rest
		}
	}
	return "/models/" + fallback
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
