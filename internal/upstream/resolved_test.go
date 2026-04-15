package upstream

import (
	"net/http"
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
				BaseURL:        "https://api.openai.com",
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
				BaseURL:        "https://models.inference.ai.azure.com",
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

func TestResolvedUpstreamBuildURL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.UpstreamConfig
		path    string
		wantURL string
	}{
		{
			name: "openai_default",
			cfg: config.UpstreamConfig{
				BaseURL: "https://api.openai.com",
			},
			path:    "/v1/responses",
			wantURL: "https://api.openai.com/v1/responses",
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
		BaseURL: "https://api.openai.com",
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
}
