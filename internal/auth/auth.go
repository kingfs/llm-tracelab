package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func Required(token string) bool {
	return strings.TrimSpace(token) != ""
}

func BearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", false
	}
	token := strings.TrimSpace(header[len("bearer "):])
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

func Middleware(next http.Handler, token string, realm string) http.Handler {
	if !Required(token) {
		return next
	}
	if strings.TrimSpace(realm) == "" {
		realm = "llm-tracelab"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Authorized(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
