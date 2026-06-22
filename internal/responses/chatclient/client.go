package chatclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

const maxErrorBodyBytes = 4096

type Options struct {
	BaseURL    string
	APIKey     string
	Headers    http.Header
	HTTPClient *http.Client
}

type Client struct {
	baseURL    string
	apiKey     string
	headers    http.Header
	httpClient *http.Client
}

var _ runtime.ChatCompletionsClient = (*Client)(nil)

func New(opts Options) (*Client, error) {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     opts.APIKey,
		headers:    cloneHeader(opts.Headers),
		httpClient: httpClient,
	}, nil
}

func (c *Client) ChatCompletion(ctx context.Context, chatReq runtime.ChatCompletionRequest) (runtime.ChatCompletionResponse, error) {
	if c == nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("chat completions client is nil")
	}
	body, err := json.Marshal(chatReq)
	if err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("marshal chat completion request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("create chat completion request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for key, values := range c.headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("send chat completion request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		body, readErr := io.ReadAll(io.LimitReader(httpResp.Body, maxErrorBodyBytes+1))
		if readErr != nil {
			return runtime.ChatCompletionResponse{}, fmt.Errorf("read chat completion error response: %w", readErr)
		}
		truncated := len(body) > maxErrorBodyBytes
		if truncated {
			body = body[:maxErrorBodyBytes]
		}
		return runtime.ChatCompletionResponse{}, HTTPError{
			StatusCode:    httpResp.StatusCode,
			Status:        httpResp.Status,
			Body:          string(body),
			BodyTruncated: truncated,
		}
	}

	if chatReq.Stream {
		chatResp, err := aggregateChatCompletionStream(httpResp.Body)
		if err != nil {
			return runtime.ChatCompletionResponse{}, err
		}
		return chatResp, nil
	}

	var chatResp runtime.ChatCompletionResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&chatResp); err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("decode chat completion response JSON: %w", err)
	}
	return chatResp, nil
}

type HTTPError struct {
	StatusCode    int
	Status        string
	Body          string
	BodyTruncated bool
}

func (e HTTPError) Error() string {
	if e.BodyTruncated {
		return fmt.Sprintf("chat completion request failed: status %d: %s... (truncated)", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("chat completion request failed: status %d: %s", e.StatusCode, e.Body)
}

func cloneHeader(in http.Header) http.Header {
	if len(in) == 0 {
		return nil
	}
	out := make(http.Header, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}
