package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/kingfs/llm-tracelab/internal/recorder"
	"github.com/kingfs/llm-tracelab/internal/responses/chatclient"
	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const (
	responsesServerChatCompletionsPath      = "/v1/chat/completions"
	responsesServerMaxChatErrorBodyBytes    = 4096
	responsesServerAcceptEncodingNoCompress = "identity"
)

type responsesChatCompletionsAdapter struct {
	router        *router.Router
	recorder      *recorder.Recorder
	routingPolicy string
	httpClient    *http.Client
}

var _ runtime.ChatCompletionsClient = (*responsesChatCompletionsAdapter)(nil)

func (a *responsesChatCompletionsAdapter) ChatCompletion(ctx context.Context, chatReq runtime.ChatCompletionRequest) (runtime.ChatCompletionResponse, error) {
	if a == nil || a.router == nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("responses chat completions router is required")
	}
	if a.recorder == nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("responses chat completions recorder is required")
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

	httpReq, recordReq, err := buildResponsesServerChatRequest(ctx, selection, body)
	if err != nil {
		a.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: http.StatusInternalServerError,
			DurationMs: float64(time.Since(start).Milliseconds()),
			Stream:     chatReq.Stream,
		})
		completed = true
		return runtime.ChatCompletionResponse{}, err
	}

	logInfo, err := a.recorder.PrepareLogFileWithOptionsAndBody(recordReq, recorder.PrepareOptions{
		SiteURL:                        selection.Target.Upstream.BaseURL,
		SelectedUpstreamID:             selection.Target.ID,
		SelectedUpstreamProviderPreset: selection.Target.Upstream.ProviderPreset,
		RoutingPolicy:                  a.routingPolicy,
		RoutingScore:                   selection.Score,
		RoutingCandidateCount:          selection.CandidateCount,
	}, body)
	if err != nil {
		a.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: http.StatusInternalServerError,
			DurationMs: float64(time.Since(start).Milliseconds()),
			Stream:     chatReq.Stream,
		})
		completed = true
		return runtime.ChatCompletionResponse{}, fmt.Errorf("prepare chat completion recording: %w", err)
	}
	logInfo.Events = append(logInfo.Events, recorder.RecordEvent{
		Type: "routing.selection",
		Time: start,
		Attributes: map[string]interface{}{
			"upstream_id":       selection.Target.ID,
			"provider_preset":   selection.Target.Upstream.ProviderPreset,
			"candidate_count":   selection.CandidateCount,
			"routing_score":     selection.Score,
			"routing_policy":    a.routingPolicy,
			"candidate_targets": selection.Candidates,
		},
	})
	logInfo.Events = append(logInfo.Events, routingDecisionEvents(selection.Decision, start)...)

	httpClient := a.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		logInfo.Header.Meta.Error = "send chat completion request: " + err.Error()
		if uErr := a.recorder.UpdateLogFile(logInfo); uErr != nil {
			slog.Error("Failed to update responses chat completion log file", "path", logInfo.Path, "err", uErr)
		}
		a.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: 0,
			DurationMs: float64(time.Since(start).Milliseconds()),
			Stream:     chatReq.Stream,
		})
		completed = true
		return runtime.ChatCompletionResponse{}, fmt.Errorf("send chat completion request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := recordResponsesServerChatResponse(logInfo, httpResp)
	if err != nil {
		logInfo.Header.Meta.Error = err.Error()
		if uErr := a.recorder.UpdateLogFile(logInfo); uErr != nil {
			slog.Error("Failed to update responses chat completion log file", "path", logInfo.Path, "err", uErr)
		}
		a.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: httpResp.StatusCode,
			DurationMs: float64(time.Since(start).Milliseconds()),
			Stream:     chatReq.Stream,
		})
		completed = true
		return runtime.ChatCompletionResponse{}, err
	}

	duration := time.Since(start)
	statusCode := httpResp.StatusCode
	var chatResp runtime.ChatCompletionResponse
	responseErr := chatCompletionResponseError(statusCode, httpResp.Status, respBody)
	if responseErr == nil {
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			responseErr = fmt.Errorf("decode chat completion response JSON: %w", err)
		}
	}
	if responseErr != nil {
		logInfo.Header.Meta.Error = responseErr.Error()
	}

	logInfo.Header.Meta.DurationMs = duration.Milliseconds()
	logInfo.Header.Meta.StatusCode = statusCode
	logInfo.Header.Meta.ContentLength = int64(len(respBody))
	logInfo.Header.Layout.IsStream = false
	logInfo.Events = append(logInfo.Events, routingOutcomeEvent(selection, statusCode, duration, logInfo.Header.Meta.Error))
	if uErr := a.recorder.UpdateLogFile(logInfo); uErr != nil {
		slog.Error("Failed to update responses chat completion log file", "path", logInfo.Path, "err", uErr)
	}

	a.router.Complete(selection, router.Outcome{
		Success:    responseErr == nil && statusCode >= 200 && statusCode < 300,
		StatusCode: statusCode,
		DurationMs: float64(duration.Milliseconds()),
		Stream:     chatReq.Stream,
	})
	completed = true
	if responseErr != nil {
		return runtime.ChatCompletionResponse{}, responseErr
	}
	return chatResp, nil
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

func buildResponsesServerChatRequest(ctx context.Context, selection *router.Selection, body []byte) (*http.Request, *http.Request, error) {
	if selection == nil || selection.Target == nil {
		return nil, nil, fmt.Errorf("responses chat completions selection target is required")
	}
	fullURL, err := selection.Target.Upstream.BuildURL(responsesServerChatCompletionsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("build chat completion target URL: %w", err)
	}
	actualReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("create chat completion request: %w", err)
	}
	applyResponsesServerChatHeaders(actualReq, selection, body)

	recordURL, err := url.Parse(fullURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse chat completion target URL: %w", err)
	}
	recordHost := recordURL.Host
	recordURL.Scheme = ""
	recordURL.Host = ""
	recordReq := actualReq.Clone(ctx)
	recordReq.Body = io.NopCloser(bytes.NewReader(body))
	recordReq.URL = recordURL
	recordReq.Host = recordHost
	recordReq.RequestURI = recordURL.RequestURI()
	recordReq.ContentLength = int64(len(body))
	return actualReq, recordReq, nil
}

func applyResponsesServerChatHeaders(req *http.Request, selection *router.Selection, body []byte) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", responsesServerAcceptEncodingNoCompress)
	if len(body) > 0 {
		req.ContentLength = int64(len(body))
	}
	selection.Target.Upstream.ApplyAuthHeaders(req.Header)
}

func recordResponsesServerChatResponse(logInfo *recorder.LogInfo, resp *http.Response) ([]byte, error) {
	if logInfo == nil || logInfo.File == nil {
		return nil, fmt.Errorf("responses chat completions log file is required")
	}
	if _, err := logInfo.File.Write([]byte("\n")); err != nil {
		return nil, fmt.Errorf("write chat completion response separator: %w", err)
	}
	headerBuf := bytes.NewBufferString(fmt.Sprintf("%s %s\r\n", resp.Proto, resp.Status))
	resp.Header.Write(headerBuf)
	headerBuf.WriteString("\r\n")
	nHead, err := logInfo.File.Write(headerBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("write chat completion response header: %w", err)
	}
	logInfo.Header.Layout.ResHeaderLen = int64(nHead)

	sniffer := &UsageSniffer{
		Source:   resp.Body,
		File:     logInfo.File,
		Count:    &logInfo.Header.Layout.ResBodyLen,
		Usage:    &logInfo.Header.Usage,
		Pipeline: llm.NewResponsePipeline(logInfo.Header.Meta.Provider, logInfo.Header.Meta.Endpoint, false),
		Events:   &logInfo.Events,
	}
	respBody, readErr := io.ReadAll(sniffer)
	closeErr := sniffer.Close()
	if readErr != nil {
		return respBody, fmt.Errorf("read chat completion response body: %w", readErr)
	}
	if closeErr != nil {
		return respBody, fmt.Errorf("close chat completion response body: %w", closeErr)
	}
	return respBody, nil
}

func chatCompletionResponseError(statusCode int, status string, body []byte) error {
	if statusCode >= 200 && statusCode <= 299 {
		return nil
	}
	errorBody := body
	truncated := len(errorBody) > responsesServerMaxChatErrorBodyBytes
	if truncated {
		errorBody = errorBody[:responsesServerMaxChatErrorBodyBytes]
	}
	return chatclient.HTTPError{
		StatusCode:    statusCode,
		Status:        status,
		Body:          string(errorBody),
		BodyTruncated: truncated,
	}
}

func cloneURL(in *url.URL) *url.URL {
	if in == nil {
		return &url.URL{}
	}
	out := *in
	return &out
}
