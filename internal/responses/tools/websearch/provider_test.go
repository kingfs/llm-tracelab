package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewProviderReturnsMockProvider(t *testing.T) {
	provider, err := NewProvider(Options{Provider: "mock"})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}
	if _, ok := provider.(MockProvider); !ok {
		t.Fatalf("provider = %T, want MockProvider", provider)
	}
}

func TestNewProviderReturnsSearXNGProvider(t *testing.T) {
	provider, err := NewProvider(Options{Provider: "searxng", BaseURL: "http://127.0.0.1:8888"})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}
	if _, ok := provider.(*SearXNGProvider); !ok {
		t.Fatalf("provider = %T, want *SearXNGProvider", provider)
	}
}

func TestNewProviderRejectsSearXNGWithoutBaseURL(t *testing.T) {
	_, err := NewProvider(Options{Provider: "searxng"})
	if err == nil {
		t.Fatal("NewProvider returned nil error, want base_url error")
	}
}

func TestNewProviderReturnsDisabledProviderByDefault(t *testing.T) {
	provider, err := NewProvider(Options{})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}
	if _, ok := provider.(DisabledProvider); !ok {
		t.Fatalf("provider = %T, want DisabledProvider", provider)
	}
}

func TestProviderName(t *testing.T) {
	searxng, err := NewProvider(Options{Provider: "searxng", BaseURL: "http://127.0.0.1:8888"})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	for _, tc := range []struct {
		name     string
		provider Provider
		want     string
	}{
		{name: "nil", want: ProviderDisabled},
		{name: "disabled", provider: DisabledProvider{}, want: ProviderDisabled},
		{name: "mock", provider: MockProvider{}, want: ProviderMock},
		{name: "searxng", provider: searxng, want: ProviderSearXNG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProviderName(tc.provider); got != tc.want {
				t.Fatalf("ProviderName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewProviderRejectsUnknownProvider(t *testing.T) {
	_, err := NewProvider(Options{Provider: "real"})
	if err == nil {
		t.Fatal("NewProvider returned nil error, want unsupported provider error")
	}
}

func TestDisabledProviderSearchReturnsClearError(t *testing.T) {
	_, err := DisabledProvider{}.Search(context.Background(), Query{Text: "codex", MaxResults: 1})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Search error = %v, want ErrDisabled", err)
	}
}

func TestMockProviderSearchReturnsDeterministicResults(t *testing.T) {
	query := Query{Text: " local responses api ", MaxResults: 2}

	first, err := MockProvider{}.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("first Search returned error: %v", err)
	}
	second, err := MockProvider{}.Search(context.Background(), query)
	if err != nil {
		t.Fatalf("second Search returned error: %v", err)
	}

	if len(first.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(first.Results))
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("mock results are not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.Results[0].Title != `Mock search result 1 for "local responses api"` {
		t.Fatalf("first title = %q", first.Results[0].Title)
	}
	if first.Results[0].URL != "https://mock.search.local/results/1?q=local+responses+api" {
		t.Fatalf("first URL = %q", first.Results[0].URL)
	}
	if first.Results[0].Snippet == "" {
		t.Fatal("first snippet is empty")
	}
}

func TestMockProviderSearchUsesConfiguredMaxResults(t *testing.T) {
	provider, err := NewProvider(Options{Provider: "mock", MaxResults: 3})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	result, err := provider.Search(context.Background(), Query{Text: "codex"})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(result.Results) != 3 {
		t.Fatalf("results len = %d, want 3", len(result.Results))
	}
}

func TestMockProviderSearchHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := MockProvider{}.Search(ctx, Query{Text: "codex", MaxResults: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Search error = %v, want context.Canceled", err)
	}
}

func TestSearXNGProviderSearchReturnsResults(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotFormat string
	var gotMaxResults string
	var gotUserAgent string
	var gotForwardedFor string
	var gotRealIP string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("q")
		gotFormat = r.URL.Query().Get("format")
		gotMaxResults = r.URL.Query().Get("max_results")
		gotUserAgent = r.Header.Get("User-Agent")
		gotForwardedFor = r.Header.Get("X-Forwarded-For")
		gotRealIP = r.Header.Get("X-Real-IP")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"title": "First", "url": "https://example.com/first", "content": "First content"},
				{"title": "Second", "url": "https://example.com/second", "snippet": "Second snippet"}
			]
		}`))
	}))
	defer server.Close()

	provider, err := NewProvider(Options{
		Provider:  "searxng",
		BaseURL:   server.URL + "/instance",
		TimeoutMS: 1000,
		UserAgent: "llm-tracelab-test",
	})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	result, err := provider.Search(context.Background(), Query{Text: " local responses api ", MaxResults: 1})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	if gotPath != "/instance/search" {
		t.Fatalf("path = %q, want /instance/search", gotPath)
	}
	if gotQuery != "local responses api" {
		t.Fatalf("q = %q, want local responses api", gotQuery)
	}
	if gotFormat != "json" {
		t.Fatalf("format = %q, want json", gotFormat)
	}
	if gotMaxResults != "1" {
		t.Fatalf("max_results = %q, want 1", gotMaxResults)
	}
	if gotUserAgent != "llm-tracelab-test" {
		t.Fatalf("User-Agent = %q, want llm-tracelab-test", gotUserAgent)
	}
	if gotForwardedFor != "127.0.0.1" {
		t.Fatalf("X-Forwarded-For = %q, want 127.0.0.1", gotForwardedFor)
	}
	if gotRealIP != "127.0.0.1" {
		t.Fatalf("X-Real-IP = %q, want 127.0.0.1", gotRealIP)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(result.Results))
	}
	if result.Results[0] != (SearchResult{Title: "First", URL: "https://example.com/first", Snippet: "First content"}) {
		t.Fatalf("first result = %#v", result.Results[0])
	}
}

func TestSearXNGProviderUsesDefaultUserAgentAndMaxResults(t *testing.T) {
	var gotUserAgent string
	var gotMaxResults string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		gotMaxResults = r.URL.Query().Get("max_results")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"First","url":"https://example.com/first","content":"First content"}]}`))
	}))
	defer server.Close()

	provider, err := NewProvider(Options{Provider: "searxng", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	_, err = provider.Search(context.Background(), Query{Text: "codex"})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if gotUserAgent != "llm-tracelab web_search" {
		t.Fatalf("User-Agent = %q, want llm-tracelab web_search", gotUserAgent)
	}
	if gotMaxResults != "5" {
		t.Fatalf("max_results = %q, want 5", gotMaxResults)
	}
}

func TestSearXNGProviderSearchReturnsErrorForNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	provider, err := NewProvider(Options{Provider: "searxng", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	_, err = provider.Search(context.Background(), Query{Text: "codex", MaxResults: 1})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("Search error = %v, want HTTP 503 error", err)
	}
	var statusErr HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Search error type = %T, want HTTPStatusError", err)
	}
	if statusErr.Provider != ProviderSearXNG || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTPStatusError = %#v, want searxng 503", statusErr)
	}
	if !strings.Contains(statusErr.URL, "/search") {
		t.Fatalf("HTTPStatusError URL = %q, want search URL", statusErr.URL)
	}
}

func TestSearXNGProviderSearchReturnsErrorForTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	provider, err := NewProvider(Options{Provider: "searxng", BaseURL: server.URL, TimeoutMS: 1})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	_, err = provider.Search(context.Background(), Query{Text: "codex", MaxResults: 1})
	if err == nil {
		t.Fatal("Search returned nil error, want timeout error")
	}
}

func TestSearXNGProviderSearchReturnsErrorForMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":`))
	}))
	defer server.Close()

	provider, err := NewProvider(Options{Provider: "searxng", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewProvider returned error: %v", err)
	}

	_, err = provider.Search(context.Background(), Query{Text: "codex", MaxResults: 1})
	if err == nil || !strings.Contains(err.Error(), "decode searxng search response") {
		t.Fatalf("Search error = %v, want malformed JSON decode error", err)
	}
}
