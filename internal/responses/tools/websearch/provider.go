package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderDisabled = "disabled"
	ProviderMock     = "mock"
	ProviderSearXNG  = "searxng"

	defaultTimeout    = 5 * time.Second
	defaultUserAgent  = "llm-tracelab web_search"
	defaultMaxResults = 5
)

var ErrDisabled = errors.New("web search provider is disabled")

type Query struct {
	Text       string
	MaxResults int
}

type Result struct {
	Results []SearchResult
}

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type Provider interface {
	Search(ctx context.Context, query Query) (Result, error)
}

type HTTPStatusError struct {
	Provider   string
	StatusCode int
	URL        string
}

func (e HTTPStatusError) Error() string {
	return fmt.Sprintf("%s search returned HTTP %d", e.Provider, e.StatusCode)
}

type Options struct {
	Provider   string
	BaseURL    string
	TimeoutMS  int
	UserAgent  string
	MaxResults int
}

func NewProvider(opts Options) (Provider, error) {
	switch normalizeProvider(opts.Provider) {
	case "", ProviderDisabled:
		return DisabledProvider{}, nil
	case ProviderMock:
		return MockProvider{maxResults: normalizeMaxResults(opts.MaxResults, 1)}, nil
	case ProviderSearXNG:
		return NewSearXNGProvider(opts)
	default:
		return nil, fmt.Errorf("unsupported web search provider %q", opts.Provider)
	}
}

func ProviderName(provider Provider) string {
	switch provider.(type) {
	case nil:
		return ProviderDisabled
	case DisabledProvider:
		return ProviderDisabled
	case MockProvider:
		return ProviderMock
	case *SearXNGProvider:
		return ProviderSearXNG
	default:
		return "custom"
	}
}

type SearXNGProvider struct {
	baseURL    *url.URL
	client     *http.Client
	userAgent  string
	maxResults int
}

func NewSearXNGProvider(opts Options) (*SearXNGProvider, error) {
	baseURLText := strings.TrimSpace(opts.BaseURL)
	if baseURLText == "" {
		return nil, errors.New("searxng web search provider requires base_url")
	}
	baseURL, err := url.Parse(baseURLText)
	if err != nil {
		return nil, fmt.Errorf("parse searxng base_url: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("searxng base_url must be absolute: %q", opts.BaseURL)
	}

	timeout := defaultTimeout
	if opts.TimeoutMS > 0 {
		timeout = time.Duration(opts.TimeoutMS) * time.Millisecond
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	return &SearXNGProvider{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
		},
		userAgent:  userAgent,
		maxResults: normalizeMaxResults(opts.MaxResults, defaultMaxResults),
	}, nil
}

func (p *SearXNGProvider) Search(ctx context.Context, query Query) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	text := strings.TrimSpace(query.Text)
	if text == "" {
		return Result{}, errors.New("web search query is required")
	}

	maxResults := p.effectiveMaxResults(query.MaxResults)
	requestURL := p.searchURL(text, maxResults)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Real-IP", "127.0.0.1")
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return Result{}, HTTPStatusError{
			Provider:   ProviderSearXNG,
			StatusCode: resp.StatusCode,
			URL:        requestURL,
		}
	}

	var payload searxngResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("decode searxng search response: %w", err)
	}

	if maxResults > len(payload.Results) {
		maxResults = len(payload.Results)
	}
	results := make([]SearchResult, 0, maxResults)
	for _, item := range payload.Results {
		if len(results) >= maxResults {
			break
		}
		result := SearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Snippet: strings.TrimSpace(item.Snippet),
		}
		if result.Snippet == "" {
			result.Snippet = strings.TrimSpace(item.Content)
		}
		if result.Title == "" && result.URL == "" && result.Snippet == "" {
			continue
		}
		results = append(results, result)
	}
	return Result{Results: results}, nil
}

func (p *SearXNGProvider) effectiveMaxResults(queryMaxResults int) int {
	if queryMaxResults > 0 {
		return queryMaxResults
	}
	return p.maxResults
}

func (p *SearXNGProvider) searchURL(query string, maxResults int) string {
	u := *p.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/search"
	values := u.Query()
	values.Set("q", query)
	values.Set("format", "json")
	if maxResults > 0 {
		values.Set("max_results", strconv.Itoa(maxResults))
	}
	u.RawQuery = values.Encode()
	return u.String()
}

type searxngResponse struct {
	Results []searxngResult `json:"results"`
}

type searxngResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Snippet string `json:"snippet"`
}

type DisabledProvider struct{}

func (DisabledProvider) Search(ctx context.Context, query Query) (Result, error) {
	return Result{}, ErrDisabled
}

type MockProvider struct {
	maxResults int
}

func (p MockProvider) Search(ctx context.Context, query Query) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	text := strings.TrimSpace(query.Text)
	if text == "" {
		text = "empty query"
	}
	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = normalizeMaxResults(p.maxResults, 1)
	}

	results := make([]SearchResult, 0, maxResults)
	escapedQuery := url.QueryEscape(text)
	for i := 1; i <= maxResults; i++ {
		results = append(results, SearchResult{
			Title:   fmt.Sprintf("Mock search result %d for %q", i, text),
			URL:     fmt.Sprintf("https://mock.search.local/results/%d?q=%s", i, escapedQuery),
			Snippet: fmt.Sprintf("Deterministic mock web search result %d for %q.", i, text),
		})
	}
	return Result{Results: results}, nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func normalizeMaxResults(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
