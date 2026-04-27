package auth

import (
	"context"
	"net/http"
	"strings"
)

type principalContextKey struct{}

type TokenVerifier interface {
	VerifyToken(ctx context.Context, token string) (Principal, bool, error)
}

type Principal struct {
	UserID   int
	Username string
	Role     string
	Scope    string
}

func BearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	return token, token != ""
}

func VerifyRequest(r *http.Request, verifier TokenVerifier) (Principal, bool) {
	if verifier == nil {
		return Principal{}, true
	}
	provided, ok := BearerToken(r.Header.Get("Authorization"))
	if !ok {
		return Principal{}, false
	}
	principal, verified, err := verifier.VerifyToken(r.Context(), provided)
	return principal, err == nil && verified
}

func RequestAuthorized(r *http.Request, verifier TokenVerifier) bool {
	_, ok := VerifyRequest(r, verifier)
	return ok
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func Middleware(next http.Handler, realm string, verifier TokenVerifier) http.Handler {
	if verifier == nil {
		return next
	}
	if strings.TrimSpace(realm) == "" {
		realm = "llm-tracelab"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := VerifyRequest(r, verifier)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}
