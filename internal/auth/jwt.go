package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	MonitorJWTIssuer   = "llm-tracelab-monitor"
	MonitorJWTAudience = "llm-tracelab-monitor-ui"
)

var (
	ErrJWTSecretTooShort = errors.New("jwt secret must be at least 32 bytes")
	ErrInvalidJWT        = errors.New("invalid jwt")
)

type JWTManager struct {
	secret   []byte
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

type JWTOptions struct {
	Secret   []byte
	Issuer   string
	Audience string
	TTL      time.Duration
	Now      func() time.Time
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type jwtClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	Expires   int64  `json:"exp"`
	NotBefore int64  `json:"nbf"`
	IssuedAt  int64  `json:"iat"`
	JWTID     string `json:"jti"`
	UserID    int    `json:"uid"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Scope     string `json:"scope"`
}

func NewJWTManager(opts JWTOptions) (*JWTManager, error) {
	if len(opts.Secret) < 32 {
		return nil, ErrJWTSecretTooShort
	}
	issuer := strings.TrimSpace(opts.Issuer)
	if issuer == "" {
		issuer = MonitorJWTIssuer
	}
	audience := strings.TrimSpace(opts.Audience)
	if audience == "" {
		audience = MonitorJWTAudience
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	secret := make([]byte, len(opts.Secret))
	copy(secret, opts.Secret)
	return &JWTManager{secret: secret, issuer: issuer, audience: audience, ttl: ttl, now: now}, nil
}

func NewEphemeralJWTManager(ttl time.Duration) (*JWTManager, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	return NewJWTManager(JWTOptions{Secret: secret[:], TTL: ttl})
}

func (m *JWTManager) IssueToken(principal Principal) (TokenResult, error) {
	if m == nil {
		return TokenResult{}, errors.New("jwt manager not configured")
	}
	if principal.UserID <= 0 || strings.TrimSpace(principal.Username) == "" {
		return TokenResult{}, errors.New("jwt principal is incomplete")
	}
	now := m.now().UTC()
	jti, err := randomJWTID()
	if err != nil {
		return TokenResult{}, err
	}
	claims := jwtClaims{
		Issuer:    m.issuer,
		Subject:   strconv.Itoa(principal.UserID),
		Audience:  m.audience,
		Expires:   now.Add(m.ttl).Unix(),
		NotBefore: now.Add(-30 * time.Second).Unix(),
		IssuedAt:  now.Unix(),
		JWTID:     jti,
		UserID:    principal.UserID,
		Username:  principal.Username,
		Role:      principal.Role,
		Scope:     principal.Scope,
	}
	token, err := m.sign(jwtHeader{Algorithm: "HS256", Type: "JWT"}, claims)
	if err != nil {
		return TokenResult{}, err
	}
	return TokenResult{Token: token, Prefix: tokenDisplayPrefix(token)}, nil
}

func (m *JWTManager) VerifyToken(ctx context.Context, token string) (Principal, bool, error) {
	_ = ctx
	if m == nil {
		return Principal{}, false, nil
	}
	claims, err := m.verify(strings.TrimSpace(token))
	if errors.Is(err, ErrInvalidJWT) {
		return Principal{}, false, nil
	}
	if err != nil {
		return Principal{}, false, err
	}
	return Principal{
		UserID:   claims.UserID,
		Username: claims.Username,
		Role:     claims.Role,
		Scope:    claims.Scope,
	}, true, nil
}

func (m *JWTManager) sign(header jwtHeader, claims jwtClaims) (string, error) {
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(unsigned))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return unsigned + "." + signature, nil
}

func (m *JWTManager) verify(token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return jwtClaims{}, ErrInvalidJWT
	}
	var header jwtHeader
	if err := decodeJWTPart(parts[0], &header); err != nil {
		return jwtClaims{}, ErrInvalidJWT
	}
	if header.Algorithm != "HS256" || header.Type != "JWT" {
		return jwtClaims{}, ErrInvalidJWT
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtClaims{}, ErrInvalidJWT
	}
	unsigned := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(unsigned))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return jwtClaims{}, ErrInvalidJWT
	}
	var claims jwtClaims
	if err := decodeJWTPart(parts[1], &claims); err != nil {
		return jwtClaims{}, ErrInvalidJWT
	}
	now := m.now().UTC().Unix()
	if claims.Issuer != m.issuer || claims.Audience != m.audience {
		return jwtClaims{}, ErrInvalidJWT
	}
	if claims.UserID <= 0 || strings.TrimSpace(claims.Username) == "" || claims.Subject != strconv.Itoa(claims.UserID) {
		return jwtClaims{}, ErrInvalidJWT
	}
	if claims.Expires <= now || claims.NotBefore > now+30 || claims.IssuedAt > now+30 {
		return jwtClaims{}, ErrInvalidJWT
	}
	return claims, nil
}

func decodeJWTPart(part string, dst any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func randomJWTID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate jwt id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
