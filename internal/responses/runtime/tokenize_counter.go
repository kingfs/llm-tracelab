package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const defaultTokenizeTimeout = 10 * time.Second

type HTTPTokenizeChatPromptTokenCounterConfig struct {
	BaseURL      string
	Model        string
	APIKey       string
	APIKeyHeader string
	Headers      http.Header
	Timeout      time.Duration
	Client       *http.Client
}

type HTTPTokenizeChatPromptTokenCounter struct {
	endpoint     string
	model        string
	apiKey       string
	apiKeyHeader string
	headers      http.Header
	timeout      time.Duration
	client       *http.Client
}

func NewHTTPTokenizeChatPromptTokenCounter(cfg HTTPTokenizeChatPromptTokenCounterConfig) (*HTTPTokenizeChatPromptTokenCounter, error) {
	endpoint, err := tokenizeEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	apiKeyHeader := cfg.APIKeyHeader
	if apiKeyHeader == "" {
		apiKeyHeader = "Authorization"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTokenizeTimeout
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPTokenizeChatPromptTokenCounter{
		endpoint:     endpoint,
		model:        cfg.Model,
		apiKey:       cfg.APIKey,
		apiKeyHeader: apiKeyHeader,
		headers:      cloneHTTPHeader(cfg.Headers),
		timeout:      timeout,
		client:       client,
	}, nil
}

func (c *HTTPTokenizeChatPromptTokenCounter) CountChatPromptTokens(req ChatPromptTokenCountRequest) (int, error) {
	if c == nil {
		return 0, errors.New("tokenize counter is nil")
	}
	if c.endpoint == "" {
		return 0, errors.New("tokenize endpoint is empty")
	}
	prompt, err := encodeChatPromptTokenizePrompt(req)
	if err != nil {
		return 0, fmt.Errorf("encode tokenize prompt: %w", err)
	}
	body := tokenizeRequest{
		Model:  c.effectiveModel(req.Model),
		Prompt: prompt,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("encode tokenize request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("build tokenize request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	for key, values := range c.headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	if c.apiKey != "" {
		value := c.apiKey
		if strings.EqualFold(c.apiKeyHeader, "Authorization") && !strings.HasPrefix(strings.ToLower(value), "bearer ") {
			value = "Bearer " + value
		}
		httpReq.Header.Set(c.apiKeyHeader, value)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("call tokenize endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("tokenize endpoint returned status %d", resp.StatusCode)
	}
	responseData, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("read tokenize response: %w", err)
	}
	count, err := parseTokenizeTokenCount(responseData)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *HTTPTokenizeChatPromptTokenCounter) effectiveModel(reqModel string) string {
	if c.model != "" {
		return c.model
	}
	return reqModel
}

type tokenizeRequest struct {
	Model  string `json:"model,omitempty"`
	Prompt string `json:"prompt"`
}

type chatPromptTokenizeEnvelope struct {
	Messages   []ChatMessage `json:"messages"`
	Tools      []ChatTool    `json:"tools,omitempty"`
	ToolChoice any           `json:"tool_choice,omitempty"`
}

func encodeChatPromptTokenizePrompt(req ChatPromptTokenCountRequest) (string, error) {
	data, err := json.Marshal(chatPromptTokenizeEnvelope{
		Messages:   req.Messages,
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseTokenizeTokenCount(data []byte) (int, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("decode tokenize response: %w", err)
	}
	for _, key := range []string{"count", "token_count"} {
		if value, ok := raw[key]; ok {
			count, err := decodeNonNegativeInt(value)
			if err != nil {
				return 0, fmt.Errorf("invalid tokenize response %q: %w", key, err)
			}
			return count, nil
		}
	}
	for _, key := range []string{"tokens", "token_ids"} {
		if value, ok := raw[key]; ok {
			var tokens []json.RawMessage
			if err := json.Unmarshal(value, &tokens); err != nil {
				return 0, fmt.Errorf("invalid tokenize response %q: expected array", key)
			}
			return len(tokens), nil
		}
	}
	return 0, errors.New("tokenize response did not include a token count")
}

func decodeNonNegativeInt(data []byte) (int, error) {
	var count int
	if err := json.Unmarshal(data, &count); err != nil {
		return 0, errors.New("expected integer")
	}
	if count < 0 {
		return 0, errors.New("expected non-negative integer")
	}
	return count, nil
}

func tokenizeEndpoint(baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", errors.New("tokenize base URL is empty")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse tokenize base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("tokenize base URL must include scheme and host")
	}
	cleanPath := path.Clean(parsed.Path)
	switch cleanPath {
	case ".", "/", "/v1":
		parsed.Path = "/tokenize"
	default:
		parsed.Path = path.Join(cleanPath, "tokenize")
	}
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func cloneHTTPHeader(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	clone := make(http.Header, len(headers))
	for key, values := range headers {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
