package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/ent/dao/apitoken"
	"github.com/kingfs/llm-tracelab/ent/dao/user"
)

type verifierFunc func(context.Context, string) (Principal, bool, error)

func (f verifierFunc) VerifyToken(ctx context.Context, token string) (Principal, bool, error) {
	return f(ctx, token)
}

func TestBearerTokenRequiresBearerScheme(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		header string
		token  string
		ok     bool
	}{
		{header: "Bearer secret", token: "secret", ok: true},
		{header: "bearer secret", token: "secret", ok: true},
		{header: "  Bearer   secret  ", token: "secret", ok: true},
		{header: "secret", ok: false},
		{header: "Bearer secret extra", ok: false},
		{header: "Bearer ", ok: false},
		{header: "", ok: false},
	} {
		token, ok := BearerToken(tc.header)
		if ok != tc.ok || token != tc.token {
			t.Fatalf("BearerToken(%q) = %q, %v; want %q, %v", tc.header, token, ok, tc.token, tc.ok)
		}
	}
}

func TestStoreRejectsExpiredTokensAndDisabledUsers(t *testing.T) {
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
	if _, err := st.CreateToken(ctx, "admin", "invalid-negative", DefaultTokenScope, -time.Second); err == nil {
		t.Fatalf("CreateToken(negative ttl) error = nil, want error")
	}
	expired, err := st.CreateToken(ctx, "admin", "expired", DefaultTokenScope, time.Hour)
	if err != nil {
		t.Fatalf("CreateToken(expired) error = %v", err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := st.client.APIToken.Update().
		Where(apitoken.TokenHashEQ(hashToken(expired.Token))).
		SetExpiresAt(past).
		Save(ctx); err != nil {
		t.Fatalf("expire token error = %v", err)
	}
	if _, ok, err := st.VerifyToken(ctx, expired.Token); err != nil || ok {
		t.Fatalf("VerifyToken(expired) = ok %v err %v, want ok false err nil", ok, err)
	}

	active, err := st.CreateToken(ctx, "admin", "active", DefaultTokenScope, time.Hour)
	if err != nil {
		t.Fatalf("CreateToken(active) error = %v", err)
	}
	if _, ok, err := st.VerifyToken(ctx, active.Token); err != nil || !ok {
		t.Fatalf("VerifyToken(active) = ok %v err %v, want ok true err nil", ok, err)
	}
	if _, err := st.client.User.Update().Where(user.UsernameEQ("admin")).SetEnabled(false).Save(ctx); err != nil {
		t.Fatalf("disable user error = %v", err)
	}
	if _, ok, err := st.VerifyToken(ctx, active.Token); err != nil || ok {
		t.Fatalf("VerifyToken(disabled user) = ok %v err %v, want ok false err nil", ok, err)
	}
}

func TestRequestAuthorizedAllowsMissingVerifier(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if !RequestAuthorized(req, nil) {
		t.Fatalf("RequestAuthorized() = false, want true when verifier is nil")
	}
}

func TestMiddlewareStoresVerifiedPrincipal(t *testing.T) {
	t.Parallel()

	verifier := verifierFunc(func(_ context.Context, token string) (Principal, bool, error) {
		return Principal{Username: "admin"}, token == "valid", nil
	})
	var got Principal
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = PrincipalFromContext(r.Context())
	})
	handler := Middleware(next, "test", verifier)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer valid")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got.Username != "admin" {
		t.Fatalf("principal = %+v, want username admin", got)
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

func TestOpenDatabaseAcceptsSQLiteFileDSN(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "control.sqlite3")
	dsn := "file:" + dbPath + "?mode=rwc"
	if err := MigrateDatabaseUp("sqlite", dsn, 0); err != nil {
		t.Fatalf("MigrateDatabaseUp() error = %v", err)
	}
	st, err := OpenDatabase("sqlite", dsn, 4, 4)
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	defer st.Close()
	if st.Path() != dbPath {
		t.Fatalf("store path = %q, want %q", st.Path(), dbPath)
	}
	if _, err := st.CreateUser(context.Background(), "admin", "change-me-123"); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
}

func TestMigrateDatabaseUpCreatesMissingSQLiteParentDir(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "control.sqlite3")
	if err := MigrateDatabaseUp("sqlite", dbPath, 0); err != nil {
		t.Fatalf("MigrateDatabaseUp() error = %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file stat error = %v", err)
	}
}

func TestMigrateDatabaseUpAcceptsRelativeSQLitePath(t *testing.T) {
	t.Chdir(t.TempDir())

	dbPath := filepath.Join("docker-data", "database.sqlite3")
	if err := MigrateDatabaseUp("sqlite", dbPath, 0); err != nil {
		t.Fatalf("MigrateDatabaseUp(relative) error = %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file stat error = %v", err)
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
