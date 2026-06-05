package proxy

import "testing"

func TestCanonicalClientPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{path: "/v1/chat/completions", want: "/v1/chat/completions"},
		{path: "/v1/responses", want: "/v1/responses"},
		{path: "/responses", want: "/v1/responses"},
		{path: "/anthropic/messages", want: "/v1/messages"},
		{path: "/anthropic/v1/messages", want: "/v1/messages"},
		{path: "/anthropic/messages/count_tokens", want: "/v1/messages/count_tokens"},
		{path: "/anthropic/v1/messages/count_tokens", want: "/v1/messages/count_tokens"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := canonicalClientPath(tt.path); got != tt.want {
				t.Fatalf("canonicalClientPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
