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
}
