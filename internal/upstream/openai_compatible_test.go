package upstream

import (
	"net/http"
	"net/url"
	"testing"
)

func TestJoinRequestPath(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		reqPath  string
		wantPath string
	}{
		{
			name:     "plain_base_path",
			baseURL:  "https://api.openai.com",
			reqPath:  "/v1/responses",
			wantPath: "/v1/responses",
		},
		{
			name:     "base_url_with_v1_prefix",
			baseURL:  "https://openrouter.example.com/v1",
			reqPath:  "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "azure_openai_v1_prefix",
			baseURL:  "https://demo-resource.openai.azure.com/openai/v1",
			reqPath:  "/v1/responses",
			wantPath: "/openai/v1/responses",
		},
		{
			name:     "azure_deployment_path",
			baseURL:  "https://demo-resource.openai.azure.com/openai/deployments/gpt-4o-mini",
			reqPath:  "/v1/chat/completions",
			wantPath: "/openai/deployments/gpt-4o-mini/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := url.Parse(tt.baseURL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			if got := JoinRequestPath(target, tt.reqPath); got != tt.wantPath {
				t.Fatalf("JoinRequestPath() = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

func TestApplyAuthHeaders(t *testing.T) {
	headers := http.Header{}
	ApplyAuthHeaders(headers, "https://demo-resource.openai.azure.com/openai/v1", "azure-secret")
	if got := headers.Get("api-key"); got != "azure-secret" {
		t.Fatalf("api-key = %q, want azure-secret", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}

	headers = http.Header{}
	ApplyAuthHeaders(headers, "https://api.openai.com", "sk-test")
	if got := headers.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", got)
	}
}
