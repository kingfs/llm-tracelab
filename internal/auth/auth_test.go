package auth

import (
	"net/http"
	"testing"
)

func TestBearerTokenRequiresBearerScheme(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		header string
		token  string
		ok     bool
	}{
		{header: "Bearer secret", token: "secret", ok: true},
		{header: "bearer secret", token: "secret", ok: true},
		{header: "secret", ok: false},
		{header: "Bearer ", ok: false},
		{header: "", ok: false},
	} {
		token, ok := BearerToken(tc.header)
		if ok != tc.ok || token != tc.token {
			t.Fatalf("BearerToken(%q) = %q, %v; want %q, %v", tc.header, token, ok, tc.token, tc.ok)
		}
	}
}

func TestAuthorizedAllowsEmptyConfiguredToken(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if !Authorized(req, "") {
		t.Fatalf("Authorized() = false, want true for empty configured token")
	}
}
