package upstream

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/config"
)

func TestResolveProviderPresets(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.UpstreamConfig
		wantProfile string
		wantFamily  string
		wantVersion string
		wantDeploy  string
	}{
		{
			name: "openai_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.openai.com/v1",
				ProviderPreset: "openai",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "openrouter_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://openrouter.ai/api/v1",
				ProviderPreset: "openrouter",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "fireworks_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.fireworks.ai/inference/v1",
				ProviderPreset: "fireworks",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "together_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.together.xyz/v1",
				ProviderPreset: "together",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "groq_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.groq.com/openai/v1",
				ProviderPreset: "groq",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "xai_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.x.ai/v1",
				ProviderPreset: "xai",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "github_models_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://models.inference.ai.azure.com/v1",
				ProviderPreset: "github_models",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileOpenAIDefault,
		},
		{
			name: "azure_v1_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://demo-resource.openai.azure.com/openai/v1",
				ProviderPreset: "azure",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileAzureOpenAIV1,
			wantVersion: DefaultAzureAPIVersion,
		},
		{
			name: "azure_deployment_inferred",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://demo-resource.openai.azure.com/openai/deployments/gpt-4o-mini",
				ProviderPreset: "azure",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileAzureOpenAIDeploy,
			wantVersion: DefaultAzureAPIVersion,
			wantDeploy:  "gpt-4o-mini",
		},
		{
			name: "vllm_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "http://vllm.local:8000/v1",
				ProviderPreset: "vllm",
			},
			wantFamily:  ProtocolFamilyOpenAICompatible,
			wantProfile: RoutingProfileVLLMOpenAI,
		},
		{
			name: "anthropic_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
			},
			wantFamily:  ProtocolFamilyAnthropicMessages,
			wantProfile: RoutingProfileAnthropicDefault,
			wantVersion: DefaultAnthropicAPIVersion,
		},
		{
			name: "google_genai_preset_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://generativelanguage.googleapis.com",
				ProviderPreset: "google_genai",
			},
			wantFamily:  ProtocolFamilyGoogleGenAI,
			wantProfile: RoutingProfileGoogleAIStudio,
		},
		{
			name: "vertex_preset_express_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				ModelResource:  "publishers/google/models",
			},
			wantFamily:  ProtocolFamilyVertexNative,
			wantProfile: RoutingProfileVertexExpress,
		},
		{
			name: "vertex_preset_project_location_inferred",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://us-central1-aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				Project:        "demo-project",
				Location:       "us-central1",
				ModelResource:  "publishers/google/models",
			},
			wantFamily:  ProtocolFamilyVertexNative,
			wantProfile: RoutingProfileVertexProject,
		},
		{
			name: "vertex_express_defaults",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://aiplatform.googleapis.com",
				ProtocolFamily: ProtocolFamilyVertexNative,
				RoutingProfile: RoutingProfileVertexExpress,
				ModelResource:  "publishers/google/models",
			},
			wantFamily:  ProtocolFamilyVertexNative,
			wantProfile: RoutingProfileVertexExpress,
		},
		{
			name: "vertex_project_location_inferred",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://us-central1-aiplatform.googleapis.com",
				ProtocolFamily: ProtocolFamilyVertexNative,
				Project:        "demo-project",
				Location:       "us-central1",
				ModelResource:  "publishers/google/models",
			},
			wantFamily:  ProtocolFamilyVertexNative,
			wantProfile: RoutingProfileVertexProject,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(tt.cfg)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if resolved.ProtocolFamily != tt.wantFamily {
				t.Fatalf("ProtocolFamily = %q, want %q", resolved.ProtocolFamily, tt.wantFamily)
			}
			if resolved.RoutingProfile != tt.wantProfile {
				t.Fatalf("RoutingProfile = %q, want %q", resolved.RoutingProfile, tt.wantProfile)
			}
			if resolved.APIVersion != tt.wantVersion {
				t.Fatalf("APIVersion = %q, want %q", resolved.APIVersion, tt.wantVersion)
			}
			if resolved.Deployment != tt.wantDeploy {
				t.Fatalf("Deployment = %q, want %q", resolved.Deployment, tt.wantDeploy)
			}
		})
	}
}

func TestResolveKeepsAPISurface(t *testing.T) {
	responsesEnabled := true
	chatDisabled := false
	toolCallingDisabled := false

	resolved, err := Resolve(config.UpstreamConfig{
		BaseURL: "https://api.openai.com/v1",
		APIType: "Responses",
		Mode:    "Server",
		Capabilities: config.UpstreamCapabilitiesConfig{
			Responses:       &responsesEnabled,
			ChatCompletions: &chatDisabled,
			ToolCalling:     &toolCallingDisabled,
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.APIType != APITypeResponses {
		t.Fatalf("APIType = %q, want %q", resolved.APIType, APITypeResponses)
	}
	if resolved.Mode != APIModeServer {
		t.Fatalf("Mode = %q, want %q", resolved.Mode, APIModeServer)
	}
	if enabled, configured := resolved.Capability(CapabilityResponses); !configured || !enabled {
		t.Fatalf("Capability(responses) = enabled:%v configured:%v, want true/true", enabled, configured)
	}
	if enabled, configured := resolved.Capability(CapabilityChatCompletions); !configured || enabled {
		t.Fatalf("Capability(chat_completions) = enabled:%v configured:%v, want false/true", enabled, configured)
	}
	if enabled, configured := resolved.Capability(CapabilityToolCalling); !configured || enabled {
		t.Fatalf("Capability(tool_calling) = enabled:%v configured:%v, want false/true", enabled, configured)
	}
	if resolved.SupportsToolCalling() {
		t.Fatalf("SupportsToolCalling() = true, want false")
	}
	if !resolved.NativeResponsesServerMode() {
		t.Fatalf("NativeResponsesServerMode() = false, want true")
	}
	if resolved.ChatCompletionsServerMode() {
		t.Fatalf("ChatCompletionsServerMode() = true, want false")
	}
}

func TestResolveDefaultsAPISurface(t *testing.T) {
	resolved, err := Resolve(config.UpstreamConfig{
		BaseURL: "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.APIType != APITypeChatCompletions {
		t.Fatalf("APIType = %q, want %q", resolved.APIType, APITypeChatCompletions)
	}
	if resolved.Mode != "" {
		t.Fatalf("Mode = %q, want empty", resolved.Mode)
	}
	if enabled, configured := resolved.Capability(CapabilityResponses); configured || enabled {
		t.Fatalf("Capability(responses) = enabled:%v configured:%v, want false/false", enabled, configured)
	}
	if !resolved.SupportsToolCalling() {
		t.Fatalf("SupportsToolCalling() = false, want default true")
	}
}

func TestResolveDefaultsAPISurfaceByProtocolFamily(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.UpstreamConfig
		wantAPI string
	}{
		{
			name: "anthropic_messages",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
			},
			wantAPI: APITypeMessages,
		},
		{
			name: "google_generate_content",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://generativelanguage.googleapis.com",
				ProviderPreset: "google_genai",
			},
			wantAPI: APITypeGemini,
		},
		{
			name: "vertex_generate_content",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				ModelResource:  "publishers/google/models",
			},
			wantAPI: APITypeGemini,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(tt.cfg)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if resolved.APIType != tt.wantAPI {
				t.Fatalf("APIType = %q, want %q", resolved.APIType, tt.wantAPI)
			}
		})
	}
}

func TestResolveRejectsInvalidPresetSelections(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.UpstreamConfig
		wantErr string
	}{
		{
			name: "unknown_preset",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.openai.com/v1",
				ProviderPreset: "unknown_vendor",
			},
			wantErr: `unsupported upstream.provider_preset "unknown_vendor"`,
		},
		{
			name: "preset_family_mismatch",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
				ProtocolFamily: ProtocolFamilyGoogleGenAI,
			},
			wantErr: `upstream.provider_preset="anthropic" requires protocol_family="anthropic_messages", got "google_genai"`,
		},
		{
			name: "preset_profile_mismatch",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://openrouter.ai/api/v1",
				ProviderPreset: "openrouter",
				RoutingProfile: RoutingProfileAzureOpenAIV1,
			},
			wantErr: `upstream.provider_preset="openrouter" does not support routing_profile="azure_openai_v1"`,
		},
		{
			name: "vertex_missing_model_resource",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://aiplatform.googleapis.com",
				ProtocolFamily: ProtocolFamilyVertexNative,
				RoutingProfile: RoutingProfileVertexExpress,
			},
			wantErr: `upstream.model_resource is required for routing_profile="vertex_express"`,
		},
		{
			name: "vertex_preset_profile_mismatch",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				RoutingProfile: RoutingProfileGoogleAIStudio,
				ModelResource:  "publishers/google/models",
			},
			wantErr: `upstream.provider_preset="vertex" does not support routing_profile="google_ai_studio"`,
		},
		{
			name: "vertex_project_missing_location",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://us-central1-aiplatform.googleapis.com",
				ProtocolFamily: ProtocolFamilyVertexNative,
				RoutingProfile: RoutingProfileVertexProject,
				Project:        "demo-project",
				ModelResource:  "publishers/google/models",
			},
			wantErr: `upstream.location is required for routing_profile="vertex_project_location"`,
		},
		{
			name: "openai_root_base_url_requires_api_prefix",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.openai.com",
			},
			wantErr: `upstream.base_url must include the upstream API path prefix for protocol_family="openai_compatible" (examples: /v1, /api/v1, /openai, /openai/v1)`,
		},
		{
			name: "unknown_api_type",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.openai.com/v1",
				APIType: "not_real",
			},
			wantErr: `unsupported upstream.api_type "not_real"`,
		},
		{
			name: "unknown_mode",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.openai.com/v1",
				Mode:    "sidecar",
			},
			wantErr: `unsupported upstream.mode "sidecar"`,
		},
		{
			name: "anthropic_api_type_mismatch",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
				APIType:        APITypeChatCompletions,
			},
			wantErr: `upstream.api_type="chat_completions" is incompatible with protocol_family="anthropic_messages"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.cfg)
			if err == nil {
				t.Fatalf("Resolve() error = nil, want non-nil")
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("Resolve() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestResolveAllowsDeepSeekRootBaseURL(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.UpstreamConfig
		wantPreset string
	}{
		{
			name: "explicit_deepseek_preset",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.deepseek.com",
				ProviderPreset: "deepseek",
			},
			wantPreset: "deepseek",
		},
		{
			name: "infer_deepseek_from_official_host",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.deepseek.com",
			},
			wantPreset: "deepseek",
		},
		{
			name: "coerce_openai_preset_on_official_host",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.deepseek.com",
				ProviderPreset: "openai",
			},
			wantPreset: "deepseek",
		},
		{
			name: "infer_deepseek_from_official_host_with_port",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.deepseek.com:443",
			},
			wantPreset: "deepseek",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(tt.cfg)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}

			if resolved.ProviderPreset != tt.wantPreset {
				t.Fatalf("ProviderPreset = %q, want %q", resolved.ProviderPreset, tt.wantPreset)
			}
			wantBaseURL := strings.TrimRight(tt.cfg.BaseURL, "/")
			if resolved.BaseURL != wantBaseURL {
				t.Fatalf("BaseURL = %q, want %q", resolved.BaseURL, wantBaseURL)
			}
			if got, err := resolved.BuildURL("/v1/responses"); err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			} else if want := wantBaseURL + "/responses"; got != want {
				t.Fatalf("BuildURL() = %q, want %q", got, want)
			}
			if got, err := resolved.ConnectivityCheckURL(); err != nil {
				t.Fatalf("ConnectivityCheckURL() error = %v", err)
			} else if want := wantBaseURL + "/models"; got != want {
				t.Fatalf("ConnectivityCheckURL() = %q, want %q", got, want)
			}
		})
	}
}

func TestResolvedUpstreamBuildURL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.UpstreamConfig
		path    string
		wantURL string
	}{
		{
			name: "openai_default_with_v1_base_path",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.openai.com/v1",
			},
			path:    "/v1/responses",
			wantURL: "https://api.openai.com/v1/responses",
		},
		{
			name: "openai_default_with_openai_base_path",
			cfg: config.UpstreamConfig{
				BaseURL: "https://compat.example.com/openai",
			},
			path:    "/v1/models",
			wantURL: "https://compat.example.com/openai/models",
		},
		{
			name: "base_url_with_v1_prefix",
			cfg: config.UpstreamConfig{
				BaseURL: "https://openrouter.example.com/v1",
			},
			path:    "/v1/chat/completions",
			wantURL: "https://openrouter.example.com/v1/chat/completions",
		},
		{
			name: "chat_completions_tokenize_uses_top_level_route",
			cfg: config.UpstreamConfig{
				BaseURL: "http://10.2.69.245:38080/v1",
			},
			path:    "/v1/tokenize",
			wantURL: "http://10.2.69.245:38080/tokenize",
		},
		{
			name: "vllm_tokenize_uses_top_level_route",
			cfg: config.UpstreamConfig{
				BaseURL:        "http://vllm.local:8000/v1",
				ProviderPreset: "vllm",
			},
			path:    "/v1/tokenize",
			wantURL: "http://vllm.local:8000/tokenize",
		},
		{
			name: "vllm_detokenize_uses_top_level_route",
			cfg: config.UpstreamConfig{
				BaseURL:        "http://vllm.local:8000/v1",
				ProviderPreset: "vllm",
			},
			path:    "/detokenize",
			wantURL: "http://vllm.local:8000/detokenize",
		},
		{
			name: "azure_v1_adds_api_version",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://demo-resource.openai.azure.com/openai/v1",
				ProviderPreset: "azure",
				APIVersion:     "2025-03-01-preview",
			},
			path:    "/v1/responses",
			wantURL: "https://demo-resource.openai.azure.com/openai/v1/responses?api-version=2025-03-01-preview",
		},
		{
			name: "azure_deployment_rewrites_path",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://demo-resource.openai.azure.com",
				ProviderPreset: "azure",
				RoutingProfile: RoutingProfileAzureOpenAIDeploy,
				Deployment:     "gpt-4o-mini",
				APIVersion:     "2025-03-01-preview",
			},
			path:    "/v1/chat/completions",
			wantURL: "https://demo-resource.openai.azure.com/openai/deployments/gpt-4o-mini/chat/completions?api-version=2025-03-01-preview",
		},
		{
			name: "anthropic_messages",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
			},
			path:    "/v1/messages",
			wantURL: "https://api.anthropic.com/v1/messages",
		},
		{
			name: "anthropic_messages_preserves_query",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
			},
			path:    "/v1/messages?beta=true",
			wantURL: "https://api.anthropic.com/v1/messages?beta=true",
		},
		{
			name: "google_generate_content",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://generativelanguage.googleapis.com",
				ProviderPreset: "google_genai",
			},
			path:    "/v1beta/models/gemini-2.5-flash:generateContent",
			wantURL: "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent",
		},
		{
			name: "google_stream_generate_content_adds_alt_sse",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://generativelanguage.googleapis.com",
				ProviderPreset: "google_genai",
			},
			path:    "/v1beta/models/gemini-2.5-flash:streamGenerateContent",
			wantURL: "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			name: "vertex_express_generate_content",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				ModelResource:  "publishers/google/models",
			},
			path:    "/v1/publishers/google/models/gemini-2.5-flash:generateContent",
			wantURL: "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-flash:generateContent",
		},
		{
			name: "vertex_project_location_stream_adds_alt_sse",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://us-central1-aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				Project:        "demo-project",
				Location:       "us-central1",
				ModelResource:  "publishers/google/models",
			},
			path:    "/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent",
			wantURL: "https://us-central1-aiplatform.googleapis.com/v1/projects/demo-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(tt.cfg)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			got, err := resolved.BuildURL(tt.path)
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if got != tt.wantURL {
				t.Fatalf("BuildURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestResolvedUpstreamApplyAuthHeaders(t *testing.T) {
	resolved, err := Resolve(config.UpstreamConfig{
		BaseURL:        "https://demo-resource.openai.azure.com/openai/v1",
		ProviderPreset: "azure",
		ApiKey:         "azure-secret",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	headers := http.Header{}
	resolved.ApplyAuthHeaders(headers)
	if got := headers.Get("api-key"); got != "azure-secret" {
		t.Fatalf("api-key = %q, want azure-secret", got)
	}

	resolved, err = Resolve(config.UpstreamConfig{
		BaseURL: "https://api.openai.com/v1",
		ApiKey:  "sk-test",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	headers = http.Header{}
	resolved.ApplyAuthHeaders(headers)
	if got := headers.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", got)
	}

	resolved, err = Resolve(config.UpstreamConfig{
		BaseURL:        "https://api.anthropic.com",
		ProviderPreset: "anthropic",
		ApiKey:         "anth-secret",
		Headers: map[string]string{
			"anthropic-beta": "tools-2024-04-04",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	headers = http.Header{}
	resolved.ApplyAuthHeaders(headers)
	if got := headers.Get("x-api-key"); got != "anth-secret" {
		t.Fatalf("x-api-key = %q, want anth-secret", got)
	}
	if got := headers.Get("anthropic-version"); got != DefaultAnthropicAPIVersion {
		t.Fatalf("anthropic-version = %q, want %q", got, DefaultAnthropicAPIVersion)
	}
	if got := headers.Get("anthropic-beta"); got != "tools-2024-04-04" {
		t.Fatalf("anthropic-beta = %q, want tools-2024-04-04", got)
	}

	resolved, err = Resolve(config.UpstreamConfig{
		BaseURL:        "https://generativelanguage.googleapis.com",
		ProviderPreset: "google_genai",
		ApiKey:         "goog-secret",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	headers = http.Header{}
	resolved.ApplyAuthHeaders(headers)
	if got := headers.Get("x-goog-api-key"); got != "goog-secret" {
		t.Fatalf("x-goog-api-key = %q, want goog-secret", got)
	}

	resolved, err = Resolve(config.UpstreamConfig{
		BaseURL:        "https://aiplatform.googleapis.com",
		ProviderPreset: "vertex",
		ModelResource:  "publishers/google/models",
		ApiKey:         "vertex-secret",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	headers = http.Header{}
	resolved.ApplyAuthHeaders(headers)
	if got := headers.Get("Authorization"); got != "Bearer vertex-secret" {
		t.Fatalf("Authorization = %q, want Bearer vertex-secret", got)
	}
}

func TestResolvedUpstreamStartupDiagnostics(t *testing.T) {
	tests := []struct {
		name                    string
		cfg                     config.UpstreamConfig
		wantConnectivityURL     string
		wantConnectivityPath    string
		wantModelRoutingContain string
	}{
		{
			name: "openai_default",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.openai.com/v1",
			},
			wantConnectivityURL:     "https://api.openai.com/v1/models",
			wantConnectivityPath:    ConnectivityPathOpenAIModels,
			wantModelRoutingContain: "request body",
		},
		{
			name: "azure_deployment",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://demo-resource.openai.azure.com",
				ProviderPreset: "azure",
				RoutingProfile: RoutingProfileAzureOpenAIDeploy,
				Deployment:     "gpt-4o-mini",
			},
			wantConnectivityURL:     "https://demo-resource.openai.azure.com/openai/deployments/gpt-4o-mini/models?api-version=preview",
			wantConnectivityPath:    ConnectivityPathOpenAIModels,
			wantModelRoutingContain: "deployment",
		},
		{
			name: "anthropic",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://api.anthropic.com",
				ProviderPreset: "anthropic",
			},
			wantConnectivityURL:     "https://api.anthropic.com/v1/models",
			wantConnectivityPath:    ConnectivityPathAnthropicModels,
			wantModelRoutingContain: "messages.model",
		},
		{
			name: "google",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://generativelanguage.googleapis.com",
				ProviderPreset: "google_genai",
			},
			wantConnectivityURL:     "https://generativelanguage.googleapis.com/v1beta/models",
			wantConnectivityPath:    ConnectivityPathGoogleModels,
			wantModelRoutingContain: "request path",
		},
		{
			name: "vertex_project",
			cfg: config.UpstreamConfig{
				BaseURL:        "https://us-central1-aiplatform.googleapis.com",
				ProviderPreset: "vertex",
				Project:        "demo-project",
				Location:       "us-central1",
				ModelResource:  "publishers/google/models",
			},
			wantConnectivityURL:     "https://us-central1-aiplatform.googleapis.com/v1/projects/demo-project/locations/us-central1/publishers/google/models",
			wantConnectivityPath:    "/v1/projects/demo-project/locations/us-central1/publishers/google/models",
			wantModelRoutingContain: "/v1/projects/{project}/locations/{location}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := Resolve(tt.cfg)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			diagnostics, err := resolved.StartupDiagnostics()
			if err != nil {
				t.Fatalf("StartupDiagnostics() error = %v", err)
			}
			if diagnostics.ConnectivityURL != tt.wantConnectivityURL {
				t.Fatalf("ConnectivityURL = %q, want %q", diagnostics.ConnectivityURL, tt.wantConnectivityURL)
			}
			if diagnostics.ConnectivityEndpoint != tt.wantConnectivityPath {
				t.Fatalf("ConnectivityEndpoint = %q, want %q", diagnostics.ConnectivityEndpoint, tt.wantConnectivityPath)
			}
			if !strings.Contains(diagnostics.ModelRoutingHint, tt.wantModelRoutingContain) {
				t.Fatalf("ModelRoutingHint = %q, want contain %q", diagnostics.ModelRoutingHint, tt.wantModelRoutingContain)
			}
		})
	}
}

func perModelBoolPtr(v bool) *bool { return &v }

func TestResolvedUpstreamPerModelCapabilities(t *testing.T) {
	resolved, err := Resolve(config.UpstreamConfig{
		BaseURL:        "https://api.example.com/v1",
		ProviderPreset: "openai",
		APIType:        "responses_native",
		Capabilities: config.UpstreamCapabilitiesConfig{
			Responses:       perModelBoolPtr(true),
			ChatCompletions: perModelBoolPtr(false),
		},
		ModelCapabilities: map[string]config.UpstreamCapabilitiesConfig{
			"native-only": {Responses: perModelBoolPtr(true), ChatCompletions: perModelBoolPtr(false)},
			"CHAT-ONLY":   {Responses: perModelBoolPtr(false), ChatCompletions: perModelBoolPtr(true)},
			"tools-off":   {ToolCalling: perModelBoolPtr(false)},
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	tests := []struct {
		name              string
		model             string
		wantResponses     bool
		wantChat          bool
		wantResponsesPath bool
		wantChatPath      bool
		wantToolCalling   bool
	}{
		{name: "explicit native", model: "native-only", wantResponses: true, wantChat: false, wantResponsesPath: true, wantChatPath: false, wantToolCalling: true},
		{name: "explicit chat overrides target", model: "chat-only", wantResponses: false, wantChat: true, wantResponsesPath: true, wantChatPath: true, wantToolCalling: true},
		{name: "unlisted model falls back to target", model: "some-other-model", wantResponses: true, wantChat: false, wantResponsesPath: true, wantChatPath: false, wantToolCalling: true},
		{name: "tool override only", model: "tools-off", wantResponses: true, wantChat: false, wantResponsesPath: true, wantChatPath: false, wantToolCalling: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolved.SupportsResponsesAPIForModel(tt.model); got != tt.wantResponses {
				t.Fatalf("SupportsResponsesAPIForModel(%q) = %v, want %v", tt.model, got, tt.wantResponses)
			}
			if got := resolved.SupportsChatCompletionsAPIForModel(tt.model); got != tt.wantChat {
				t.Fatalf("SupportsChatCompletionsAPIForModel(%q) = %v, want %v", tt.model, got, tt.wantChat)
			}
			if got := resolved.SupportsEndpointForModel("/v1/responses", tt.model); got != tt.wantResponsesPath {
				t.Fatalf("SupportsEndpointForModel(/v1/responses, %q) = %v, want %v", tt.model, got, tt.wantResponsesPath)
			}
			if got := resolved.SupportsEndpointForModel("/v1/chat/completions", tt.model); got != tt.wantChatPath {
				t.Fatalf("SupportsEndpointForModel(/v1/chat/completions, %q) = %v, want %v", tt.model, got, tt.wantChatPath)
			}
			if got := resolved.SupportsToolCallingForModel(tt.model); got != tt.wantToolCalling {
				t.Fatalf("SupportsToolCallingForModel(%q) = %v, want %v", tt.model, got, tt.wantToolCalling)
			}
		})
	}

	// Model keys are normalized, so a differently-cased lookup still matches.
	if !resolved.SupportsChatCompletionsAPIForModel("chat-only") {
		t.Fatalf("SupportsChatCompletionsAPIForModel(chat-only) = false, want true")
	}

	// Without per-model entries the target-level behaviour is unchanged.
	plain, err := Resolve(config.UpstreamConfig{
		BaseURL:        "https://api.example.com/v1",
		ProviderPreset: "openai",
		APIType:        "chat_completions",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !plain.SupportsChatCompletionsAPIForModel("anything") {
		t.Fatalf("plain chat target should support Chat Completions")
	}
	if plain.SupportsResponsesAPIForModel("anything") {
		t.Fatalf("plain chat target should not report native Responses support")
	}
	if !plain.SupportsEndpointForModel("/v1/responses", "anything") {
		t.Fatalf("plain chat target should still be eligible as a local Responses backend")
	}
}

// The bootstrap upstream synthesized from LLM_TRACELAB_BOOTSTRAP_UPSTREAM_*
// must satisfy the same registry validation as a hand-written config, so the
// two pieces cannot drift apart.
func TestBootstrapUpstreamFromConfigResolves(t *testing.T) {
	t.Setenv("LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL", "https://api.example.com/v1")
	t.Setenv("LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY", "bootstrap-placeholder-key")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: \"8080\"\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	targets := cfg.EffectiveUpstreams()
	if len(targets) != 1 {
		t.Fatalf("len(EffectiveUpstreams()) = %d, want 1", len(targets))
	}
	resolved, err := Resolve(targets[0].Upstream)
	if err != nil {
		t.Fatalf("Resolve(bootstrap upstream): %v", err)
	}
	if resolved.RoutingProfile != RoutingProfileOpenAIDefault {
		t.Fatalf("routing_profile = %q, want %q", resolved.RoutingProfile, RoutingProfileOpenAIDefault)
	}
}
