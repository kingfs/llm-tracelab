package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/apitoken"
	"github.com/kingfs/llm-tracelab/ent/dao/user"
	"github.com/kingfs/llm-tracelab/internal/config"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	DefaultTokenScope = "all"
	tokenPrefix       = "llmtl"
)

type Store struct {
	client *dao.Client
	db     *sql.DB
	path   string
}

type TokenResult struct {
	Token  string
	Prefix string
}

func DefaultDatabasePath(outputDir string) string {
	return filepath.Join(outputDir, "control.sqlite3")
}

func Open(path string) (*Store, error) {
	return OpenDatabase("sqlite", path, 4, 4)
}

func OpenDatabase(driver string, dsn string, maxOpenConns int, maxIdleConns int) (*Store, error) {
	driver = normalizeDriver(driver)
	if driver != "sqlite" {
		return nil, fmt.Errorf("auth store driver %q is not supported yet", driver)
	}
	path := config.SQLitePathFromDSN(dsn)
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("auth database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	if maxOpenConns > 0 {
		db.SetMaxOpenConns(maxOpenConns)
	}
	if maxIdleConns > 0 {
		db.SetMaxIdleConns(maxIdleConns)
	}
	drv := entsql.OpenDB(dialect.SQLite, db)
	return &Store{
		client: dao.NewClient(dao.Driver(drv)),
		db:     db,
		path:   path,
	}, nil
}

func normalizeDriver(driver string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		return "sqlite"
	}
	return driver
}

func sqliteDSN(dbPath string) string {
	values := url.Values{}
	for _, pragma := range []string{
		"foreign_keys(ON)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"busy_timeout(5000)",
		"wal_autocheckpoint(1000)",
	} {
		values.Add("_pragma", pragma)
	}
	u := url.URL{Scheme: "file", Path: dbPath, RawQuery: values.Encode()}
	return u.String()
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) Client() *dao.Client {
	if s == nil {
		return nil
	}
	return s.client
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("auth store not configured")
	}
	return s.client.Schema.Create(ctx)
}

func (s *Store) CreateUser(ctx context.Context, username string, password string) (*dao.User, error) {
	username = normalizeUsername(username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	row, err := s.client.User.Create().
		SetUsername(username).
		SetPasswordHash(hash).
		SetRole("admin").
		Save(ctx)
	if dao.IsConstraintError(err) {
		return nil, fmt.Errorf("user %q already exists", username)
	}
	return row, err
}

func (s *Store) ResetPassword(ctx context.Context, username string, password string) error {
	username = normalizeUsername(username)
	if username == "" {
		return errors.New("username is required")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	n, err := s.client.User.Update().
		Where(user.UsernameEQ(username)).
		SetPasswordHash(hash).
		SetEnabled(true).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("user %q not found", username)
	}
	return nil
}

func (s *Store) Login(ctx context.Context, username string, password string, ttl time.Duration) (TokenResult, error) {
	username = normalizeUsername(username)
	row, err := s.client.User.Query().Where(user.UsernameEQ(username), user.EnabledEQ(true)).Only(ctx)
	if err != nil {
		return TokenResult{}, errors.New("invalid username or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)) != nil {
		return TokenResult{}, errors.New("invalid username or password")
	}
	if _, err := row.Update().SetLastLoginAt(time.Now().UTC()).Save(ctx); err != nil {
		return TokenResult{}, err
	}
	return s.CreateToken(ctx, username, "monitor-login", DefaultTokenScope, ttl)
}

func (s *Store) CreateToken(ctx context.Context, username string, name string, scope string, ttl time.Duration) (TokenResult, error) {
	username = normalizeUsername(username)
	if ttl < 0 {
		return TokenResult{}, errors.New("token ttl must be non-negative")
	}
	row, err := s.client.User.Query().Where(user.UsernameEQ(username), user.EnabledEQ(true)).Only(ctx)
	if err != nil {
		if dao.IsNotFound(err) {
			return TokenResult{}, fmt.Errorf("enabled user %q not found", username)
		}
		return TokenResult{}, err
	}
	raw, err := generateToken()
	if err != nil {
		return TokenResult{}, err
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = DefaultTokenScope
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "api-token"
	}
	create := s.client.APIToken.Create().
		SetName(name).
		SetTokenHash(hashToken(raw)).
		SetPrefix(tokenDisplayPrefix(raw)).
		SetScope(scope).
		SetUser(row)
	if ttl > 0 {
		create.SetExpiresAt(time.Now().UTC().Add(ttl))
	}
	if _, err := create.Save(ctx); err != nil {
		return TokenResult{}, err
	}
	return TokenResult{Token: raw, Prefix: tokenDisplayPrefix(raw)}, nil
}

func (s *Store) VerifyToken(ctx context.Context, token string) (Principal, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, false, nil
	}
	row, err := s.client.APIToken.Query().
		Where(apitoken.TokenHashEQ(hashToken(token)), apitoken.EnabledEQ(true)).
		WithUser().
		Only(ctx)
	if dao.IsNotFound(err) {
		return Principal{}, false, nil
	}
	if err != nil {
		return Principal{}, false, err
	}
	if row.ExpiresAt != nil && time.Now().UTC().After(*row.ExpiresAt) {
		return Principal{}, false, nil
	}
	u := row.Edges.User
	if u == nil || !u.Enabled {
		return Principal{}, false, nil
	}
	_, _ = row.Update().SetLastUsedAt(time.Now().UTC()).Save(ctx)
	return Principal{
		UserID:   u.ID,
		Username: u.Username,
		Role:     u.Role,
		Scope:    row.Scope,
	}, true, nil
}

func hashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func generateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return tokenPrefix + "_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func tokenDisplayPrefix(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
