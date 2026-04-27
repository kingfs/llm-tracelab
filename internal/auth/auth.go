package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type TokenVerifier interface {
	VerifyToken(ctx context.Context, token string) (Principal, bool, error)
}

type Principal struct {
	UserID   int
	Username string
	Role     string
	Scope    string
}

func Required(token string) bool {
	return strings.TrimSpace(token) != ""
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

func Authorized(r *http.Request, token string) bool {
	expected := strings.TrimSpace(token)
	if expected == "" {
		return true
	}
	provided, ok := BearerToken(r.Header.Get("Authorization"))
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func RequestAuthorized(r *http.Request, staticToken string, verifier TokenVerifier) bool {
	if !Required(staticToken) && verifier == nil {
		return true
	}
	provided, ok := BearerToken(r.Header.Get("Authorization"))
	if !ok {
		return false
	}
	if Required(staticToken) && subtle.ConstantTimeCompare([]byte(provided), []byte(strings.TrimSpace(staticToken))) == 1 {
		return true
	}
	if verifier == nil {
		return false
	}
	_, verified, err := verifier.VerifyToken(r.Context(), provided)
	return err == nil && verified
}

func Middleware(next http.Handler, token string, realm string, verifiers ...TokenVerifier) http.Handler {
	var verifier TokenVerifier
	if len(verifiers) > 0 {
		verifier = verifiers[0]
	}
	if !Required(token) && verifier == nil {
		return next
	}
	if strings.TrimSpace(realm) == "" {
		realm = "llm-tracelab"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !RequestAuthorized(r, token, verifier) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
