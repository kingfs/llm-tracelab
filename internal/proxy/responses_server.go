package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/chatclient"
	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
	"github.com/kingfs/llm-tracelab/internal/router"
)

const responsesServerChatCompletionsPath = "/v1/chat/completions"

type responsesChatCompletionsAdapter struct {
	router *router.Router
}

var _ runtime.ChatCompletionsClient = (*responsesChatCompletionsAdapter)(nil)

func (a *responsesChatCompletionsAdapter) ChatCompletion(ctx context.Context, chatReq runtime.ChatCompletionRequest) (runtime.ChatCompletionResponse, error) {
	if a == nil || a.router == nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("responses chat completions router is required")
	}
	body, err := json.Marshal(chatReq)
	if err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("marshal chat completion request: %w", err)
	}
	routeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://llm-tracelab.local"+responsesServerChatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("create chat completion routing request: %w", err)
	}
	routeReq.Header.Set("Content-Type", "application/json")

	selection, err := a.router.SelectWithBody(routeReq, body)
	if err != nil {
		return runtime.ChatCompletionResponse{}, err
	}

	start := time.Now()
	completed := false
	defer func() {
		if !completed {
			a.router.Release(selection)
		}
	}()

	client, err := chatclient.New(chatclient.Options{
		BaseURL: selection.Target.Upstream.BaseURL,
		Headers: upstreamAuthHeaders(selection),
	})
	if err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("build chat completions client: %w", err)
	}

	resp, err := client.ChatCompletion(ctx, chatReq)
	statusCode := http.StatusOK
	if err != nil {
		statusCode = statusCodeFromChatClientError(err)
		a.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: statusCode,
			DurationMs: float64(time.Since(start).Milliseconds()),
			Stream:     chatReq.Stream,
		})
		completed = true
		return runtime.ChatCompletionResponse{}, err
	}

	a.router.Complete(selection, router.Outcome{
		Success:    true,
		StatusCode: statusCode,
		DurationMs: float64(time.Since(start).Milliseconds()),
		Stream:     chatReq.Stream,
	})
	completed = true
	return resp, nil
}

func (h *Handler) serveLocalResponses(w http.ResponseWriter, r *http.Request) {
	// Stage 3A intentionally does not record local /v1/responses server-mode calls.
	// Stage 3B should wire this path into the recorder cassette pipeline.
	if h.responsesHandler == nil {
		http.NotFound(w, r)
		return
	}
	localReq := r.Clone(r.Context())
	localReq.URL = cloneURL(r.URL)
	localReq.URL.Path = "/v1/responses"
	localReq.URL.RawPath = ""
	h.responsesHandler.ServeHTTP(w, localReq)
}

func statusCodeFromChatClientError(err error) int {
	var httpErr chatclient.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

func upstreamAuthHeaders(selection *router.Selection) http.Header {
	if selection == nil || selection.Target == nil {
		return nil
	}
	header := make(http.Header)
	selection.Target.Upstream.ApplyAuthHeaders(header)
	return header
}

func cloneURL(in *url.URL) *url.URL {
	if in == nil {
		return &url.URL{}
	}
	out := *in
	return &out
}
