package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestStoreListsAndRevokesUserTokens(t *testing.T) {
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
		t.Fatalf("CreateUser(admin) error = %v", err)
	}
	if _, err := st.CreateUser(ctx, "other", "change-me-123"); err != nil {
		t.Fatalf("CreateUser(other) error = %v", err)
	}
	adminToken, err := st.CreateToken(ctx, "admin", "local-dev", "api", time.Hour)
	if err != nil {
		t.Fatalf("CreateToken(admin) error = %v", err)
	}
	if _, err := st.CreateToken(ctx, "other", "other-dev", "api", time.Hour); err != nil {
		t.Fatalf("CreateToken(other) error = %v", err)
	}

	tokens, err := st.ListTokens(ctx, "admin")
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("len(tokens) = %d, want 1", len(tokens))
	}
	if tokens[0].Name != "local-dev" || tokens[0].Prefix == "" || tokens[0].Scope != "api" || !tokens[0].Enabled {
		t.Fatalf("token record = %+v", tokens[0])
	}

	if err := st.RevokeToken(ctx, "other", tokens[0].ID); err == nil {
		t.Fatalf("RevokeToken(other owned token) error = nil, want error")
	}
	if err := st.RevokeToken(ctx, "admin", tokens[0].ID); err != nil {
		t.Fatalf("RevokeToken(admin) error = %v", err)
	}
	if _, ok, err := st.VerifyToken(ctx, adminToken.Token); err != nil || ok {
		t.Fatalf("VerifyToken(revoked) = ok %v err %v, want ok false err nil", ok, err)
	}
	tokens, err = st.ListTokens(ctx, "admin")
	if err != nil {
		t.Fatalf("ListTokens(after revoke) error = %v", err)
	}
	if len(tokens) != 1 || tokens[0].Enabled {
		t.Fatalf("tokens after revoke = %+v, want disabled token", tokens)
	}
	if err := st.DeleteToken(ctx, "other", tokens[0].ID); err == nil {
		t.Fatalf("DeleteToken(other owned token) error = nil, want error")
	}
	if err := st.DeleteToken(ctx, "admin", tokens[0].ID); err != nil {
		t.Fatalf("DeleteToken(admin) error = %v", err)
	}
	tokens, err = st.ListTokens(ctx, "admin")
	if err != nil {
		t.Fatalf("ListTokens(after delete) error = %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("tokens after delete = %+v, want empty", tokens)
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

func TestNormalizeDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		driver string
		want   string
	}{
		{name: "empty defaults sqlite", driver: "", want: "sqlite"},
		{name: "trims lowercases", driver: " SQLite ", want: "sqlite"},
		{name: "postgres", driver: "postgres", want: "postgres"},
		{name: "postgresql alias", driver: " PostgreSQL ", want: "postgres"},
		{name: "unsupported preserved", driver: "mysql", want: "mysql"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDriver(tt.driver); got != tt.want {
				t.Fatalf("normalizeDriver(%q) = %q, want %q", tt.driver, got, tt.want)
			}
		})
	}
}

func TestOpenDatabaseRejectsPostgresWithoutDSN(t *testing.T) {
	t.Parallel()

	_, err := OpenDatabase("postgresql", "", 4, 4)
	if err == nil || !strings.Contains(err.Error(), "postgres auth database dsn is required") {
		t.Fatalf("OpenDatabase(postgresql empty dsn) error = %v, want required dsn", err)
	}
}

func TestOpenDatabaseAcceptsPostgresDSNWithoutConnecting(t *testing.T) {
	t.Parallel()

	st, err := OpenDatabase("postgres", "postgres://user:pass@example.invalid/traces?sslmode=disable", 7, 3)
	if err != nil {
		t.Fatalf("OpenDatabase(postgres) error = %v", err)
	}
	defer st.Close()
	if st.Path() != "postgres://user:pass@example.invalid/traces?sslmode=disable" {
		t.Fatalf("store path = %q, want dsn", st.Path())
	}
	if stats := st.db.Stats(); stats.MaxOpenConnections != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", stats.MaxOpenConnections)
	}
}

func TestMigrateDatabaseUpPostgresRequiresDSN(t *testing.T) {
	t.Parallel()

	err := MigrateDatabaseUp("postgresql", "", 0)
	if err == nil || !strings.Contains(err.Error(), "postgres application database dsn is required") {
		t.Fatalf("MigrateDatabaseUp(postgresql empty dsn) error = %v, want required dsn", err)
	}
}

func TestMigrateDatabaseUpPostgresIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LLM_TRACELAB_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set LLM_TRACELAB_TEST_POSTGRES_DSN to a disposable Postgres test database DSN")
	}
	if err := MigrateDatabaseUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateDatabaseUp(postgres) error = %v", err)
	}
	if err := MigrateDatabaseUp("postgres", dsn, 0); err != nil {
		t.Fatalf("MigrateDatabaseUp(postgres idempotent) error = %v", err)
	}

	st, err := OpenDatabase("postgres", dsn, 4, 4)
	if err != nil {
		t.Fatalf("OpenDatabase(postgres) error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	username := "admin_" + strings.ReplaceAll(t.Name(), "/", "_")
	normalizedUsername := normalizeUsername(username)
	if _, err := st.CreateUser(ctx, username, "change-me-123"); err != nil {
		t.Fatalf("CreateUser(postgres) error = %v", err)
	}
	if err := st.VerifyPassword(ctx, username, "change-me-123"); err != nil {
		t.Fatalf("VerifyPassword(postgres) error = %v", err)
	}
	token, err := st.CreateToken(ctx, username, "postgres-integration", DefaultTokenScope, time.Hour)
	if err != nil {
		t.Fatalf("CreateToken(postgres) error = %v", err)
	}
	if token.Token == "" || token.Prefix == "" {
		t.Fatalf("CreateToken(postgres) returned empty token: %+v", token)
	}
	principal, ok, err := st.VerifyToken(ctx, token.Token)
	if err != nil {
		t.Fatalf("VerifyToken(postgres) error = %v", err)
	}
	if !ok || principal.Username != normalizedUsername {
		t.Fatalf("VerifyToken(postgres) principal=%+v ok=%v, want username %q", principal, ok, normalizedUsername)
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
