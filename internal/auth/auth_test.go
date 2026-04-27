package auth

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
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

func TestStoreUserLoginAndTokenVerification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "control.sqlite3")
	if err := MigrateUp(dbPath, 0); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	if _, err := st.CreateUser(ctx, "Admin", "change-me-123"); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	loginToken, err := st.Login(ctx, "admin", "change-me-123", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if loginToken.Token == "" || loginToken.Prefix == "" {
		t.Fatalf("login token missing: %+v", loginToken)
	}
	principal, ok, err := st.VerifyToken(ctx, loginToken.Token)
	if err != nil {
		t.Fatalf("VerifyToken() error = %v", err)
	}
	if !ok {
		t.Fatalf("VerifyToken() ok = false, want true")
	}
	if principal.Username != "admin" || principal.Role != "admin" {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestStoreResetPassword(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "control.sqlite3")
	if err := MigrateUp(dbPath, 0); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	if _, err := st.CreateUser(ctx, "admin", "change-me-123"); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := st.ResetPassword(ctx, "admin", "changed-again-123"); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if _, err := st.Login(ctx, "admin", "change-me-123", time.Hour); err == nil {
		t.Fatalf("Login() with old password succeeded, want failure")
	}
	if _, err := st.Login(ctx, "admin", "changed-again-123", time.Hour); err != nil {
		t.Fatalf("Login() with new password error = %v", err)
	}
}
