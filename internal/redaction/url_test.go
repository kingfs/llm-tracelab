package redaction

import (
	"strings"
	"testing"
)

func TestDisplayURLRedactsUserinfo(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "password",
			raw:  "https://user:secret@example.com/v1",
			want: "https://user:REDACTED@example.com/v1",
		},
		{
			name: "username only",
			raw:  "https://user@example.com/v1",
			want: "https://user:REDACTED@example.com/v1",
		},
		{
			name: "empty username",
			raw:  "https://:secret@example.com/v1",
			want: "https://REDACTED:REDACTED@example.com/v1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayURL(tt.raw); got != tt.want {
				t.Fatalf("DisplayURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDisplayURLRedactsSensitiveQuery(t *testing.T) {
	raw := "https://example.com/v1?api_key=abc&token=def&access_token=ghi&client_secret=jkl&signature=mno&sig=pqr&password=stu&passwd=vwx&credential=yz&model=gpt-5"
	got := DisplayURL(raw)
	for _, leaked := range []string{"abc", "def", "ghi", "jkl", "mno", "pqr", "stu", "vwx", "yz"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("DisplayURL() leaked %q in %q", leaked, got)
		}
	}
	for _, want := range []string{
		"api_key=REDACTED",
		"token=REDACTED",
		"access_token=REDACTED",
		"client_secret=REDACTED",
		"signature=REDACTED",
		"sig=REDACTED",
		"password=REDACTED",
		"passwd=REDACTED",
		"credential=REDACTED",
		"model=gpt-5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DisplayURL() = %q, missing %q", got, want)
		}
	}
}

func TestDisplayURLPreservesNonURL(t *testing.T) {
	raw := "not a url ?api_key=abc"
	if got := DisplayURL(raw); got != raw {
		t.Fatalf("DisplayURL() = %q, want %q", got, raw)
	}
}

func TestDisplayURLPreservesNonSensitiveQuery(t *testing.T) {
	raw := "https://example.com/v1?model=gpt-5&region=us"
	got := DisplayURL(raw)
	for _, want := range []string{"model=gpt-5", "region=us"} {
		if !strings.Contains(got, want) {
			t.Fatalf("DisplayURL() = %q, missing %q", got, want)
		}
	}
}
