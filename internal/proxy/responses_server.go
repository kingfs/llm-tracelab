package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/recorder"
	responsesaudit "github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/internal/responses/chatclient"
	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const (
	responsesServerChatCompletionsPath      = "/v1/chat/completions"
	responsesServerMaxChatErrorBodyBytes    = 4096
	responsesServerAcceptEncodingNoCompress = "identity"
	responsesEntryMaxCapturedBodyBytes      = 2 << 20
)

type responsesChatCompletionsAdapter struct {
	router        *router.Router
	recorder      *recorder.Recorder
	routingPolicy string
	httpClient    *http.Client
	auditor       responsesaudit.UpstreamExchangeRecorder
	events        responsesaudit.ExecutionEventRecorder
}

var _ runtime.ChatCompletionsClient = (*responsesChatCompletionsAdapter)(nil)
var _ runtime.ChatCompletionsStreamer = (*responsesChatCompletionsAdapter)(nil)

func (a *responsesChatCompletionsAdapter) ChatCompletion(ctx context.Context, chatReq runtime.ChatCompletionRequest) (runtime.ChatCompletionResponse, error) {
	return a.chatCompletion(ctx, chatReq, nil)
}

func (a *responsesChatCompletionsAdapter) ChatCompletionStream(ctx context.Context, chatReq runtime.ChatCompletionRequest, handle runtime.ChatStreamCallback) (runtime.ChatCompletionResponse, error) {
	chatReq.Stream = true
	return a.chatCompletion(ctx, chatReq, handle)
}

func (a *responsesChatCompletionsAdapter) chatCompletion(ctx context.Context, chatReq runtime.ChatCompletionRequest, handle runtime.ChatStreamCallback) (runtime.ChatCompletionResponse, error) {
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
	applyCorrelationHeaders(routeReq.Header, ctx)

	selection, err := a.router.SelectWithBody(routeReq, body)
	if err != nil {
		return runtime.ChatCompletionResponse{}, err
	}
	if rewrittenBody, rewritten := rewriteRequestModelAlias(body, selection); rewritten {
		body = rewrittenBody
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

	prepareOpts := recorder.PrepareOptions{
		SiteURL:                        selection.Target.Upstream.BaseURL,
		SelectedUpstreamID:             selection.Target.ID,
		SelectedUpstreamProviderPreset: selection.Target.Upstream.ProviderPreset,
		RoutingPolicy:                  a.routingPolicy,
		RoutingScore:                   selection.Score,
		RoutingCandidateCount:          selection.CandidateCount,
	}
	if metadata, ok := runtime.ModelCallMetadataFromContext(ctx); ok {
		prepareOpts.ExchangeKind = metadata.ExchangeKind
		prepareOpts.ExchangeRole = metadata.ExchangeRole
		prepareOpts.SequenceIndex = metadata.SequenceIndex
		prepareOpts.ResponseID = metadata.ResponseID
		if metadata.ResponseID != "" {
			prepareOpts.ParentExchangeID = "entry:" + metadata.ResponseID
		}
	}
	logInfo, err := a.recorder.PrepareLogFileWithOptionsAndBody(recordReq, prepareOpts, body)
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
	a.recordModelCallEvent(ctx, logInfo, responsesaudit.ExecutionEvent{
		EventType: "response.model_call",
		Phase:     "model_call",
		Status:    "started",
	})

	httpClient := a.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		logInfo.Header.Meta.Error = "send chat completion request: " + err.Error()
		modelCallStatus := responsesModelCallFailureStatus(err)
		if uErr := a.recorder.UpdateLogFile(logInfo); uErr != nil {
			slog.Error("Failed to update responses chat completion log file", "path", logInfo.Path, "err", uErr)
		}
		a.recordModelCallEvent(ctx, logInfo, responsesaudit.ExecutionEvent{
			EventType: "response.model_call",
			Phase:     "model_call",
			Status:    modelCallStatus,
			Message:   "send chat completion request: " + err.Error(),
		})
		a.recordUpstreamExchange(ctx, logInfo, start, time.Now(), 0)
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

	statusCode := httpResp.StatusCode
	var respBody []byte
	var isStream bool
	var chatResp runtime.ChatCompletionResponse
	if handle != nil && statusCode >= 200 && statusCode < 300 && llm.DetectStreamingResponse(httpResp.Header) {
		chatResp, isStream, err = recordResponsesServerChatStreamResponse(logInfo, httpResp, start, handle)
	} else {
		respBody, isStream, err = recordResponsesServerChatResponse(logInfo, httpResp, start)
	}
	if err != nil {
		logInfo.Header.Meta.Error = err.Error()
		modelCallStatus := responsesModelCallFailureStatus(err)
		if uErr := a.recorder.UpdateLogFile(logInfo); uErr != nil {
			slog.Error("Failed to update responses chat completion log file", "path", logInfo.Path, "err", uErr)
		}
		a.recordModelCallEvent(ctx, logInfo, responsesaudit.ExecutionEvent{
			EventType: "response.model_call",
			Phase:     "model_call",
			Status:    modelCallStatus,
			Message:   err.Error(),
		})
		a.recordUpstreamExchange(ctx, logInfo, start, time.Now(), httpResp.StatusCode)
		a.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: httpResp.StatusCode,
			DurationMs: float64(time.Since(start).Milliseconds()),
			TTFTMs:     float64(logInfo.Header.Meta.TTFTMs),
			Stream:     chatReq.Stream,
		})
		completed = true
		return runtime.ChatCompletionResponse{}, err
	}

	duration := time.Since(start)
	responseErr := chatCompletionResponseError(statusCode, httpResp.Status, respBody)
	if responseErr == nil && len(respBody) > 0 {
		if isStream {
			chatResp, responseErr = chatclient.AggregateChatCompletionStreamWithCallback(bytes.NewReader(respBody), handle)
		} else {
			if err := json.Unmarshal(respBody, &chatResp); err != nil {
				responseErr = fmt.Errorf("decode chat completion response JSON: %w", err)
			}
		}
	}
	if responseErr != nil {
		logInfo.Header.Meta.Error = responseErr.Error()
	}

	logInfo.Header.Meta.DurationMs = duration.Milliseconds()
	logInfo.Header.Meta.StatusCode = statusCode
	logInfo.Header.Meta.ContentLength = logInfo.Header.Layout.ResBodyLen
	logInfo.Header.Layout.IsStream = isStream
	logInfo.Events = append(logInfo.Events, routingOutcomeEvent(selection, statusCode, duration, logInfo.Header.Meta.Error))
	if uErr := a.recorder.UpdateLogFile(logInfo); uErr != nil {
		slog.Error("Failed to update responses chat completion log file", "path", logInfo.Path, "err", uErr)
	}
	modelCallStatus := "completed"
	if responseErr != nil {
		modelCallStatus = "failed"
	}
	a.recordModelCallEvent(ctx, logInfo, responsesaudit.ExecutionEvent{
		EventType: "response.model_call",
		Phase:     "model_call",
		Status:    modelCallStatus,
		Message:   logInfo.Header.Meta.Error,
	})
	a.recordUpstreamExchange(ctx, logInfo, start, start.Add(duration), statusCode)

	a.router.Complete(selection, router.Outcome{
		Success:    responseErr == nil && statusCode >= 200 && statusCode < 300,
		StatusCode: statusCode,
		DurationMs: float64(duration.Milliseconds()),
		TTFTMs:     float64(logInfo.Header.Meta.TTFTMs),
		Stream:     chatReq.Stream,
	})
	completed = true
	if responseErr != nil {
		return runtime.ChatCompletionResponse{}, responseErr
	}
	return chatResp, nil
}

func (a *responsesChatCompletionsAdapter) recordModelCallEvent(ctx context.Context, logInfo *recorder.LogInfo, event responsesaudit.ExecutionEvent) {
	if a == nil || a.events == nil || logInfo == nil {
		return
	}
	requestAuditID, ok := responsesaudit.RequestAuditIDFromContext(ctx)
	if !ok {
		return
	}
	details := map[string]any{
		"request_audit_id": requestAuditID,
		"trace_id":         logInfo.Header.Meta.RequestID,
		"cassette_path":    logInfo.Path,
		"upstream_id":      logInfo.Header.Meta.SelectedUpstreamID,
		"route_target":     logInfo.Header.Meta.SelectedUpstreamBaseURL,
		"model":            logInfo.Header.Meta.Model,
		"endpoint":         logInfo.Header.Meta.Endpoint,
	}
	if logInfo.Header.Meta.StatusCode != 0 {
		details["status_code"] = logInfo.Header.Meta.StatusCode
	}
	if metadata, ok := runtime.ModelCallMetadataFromContext(ctx); ok {
		details["exchange_kind"] = metadata.ExchangeKind
		details["exchange_role"] = metadata.ExchangeRole
		details["sequence_index"] = metadata.SequenceIndex
		if metadata.ResponseID != "" {
			details["response_id"] = metadata.ResponseID
			details["parent_exchange_id"] = "entry:" + metadata.ResponseID
			if event.ResponseID == "" {
				event.ResponseID = metadata.ResponseID
			}
		}
	}
	if event.DetailsJSON != nil {
		for key, value := range event.DetailsJSON {
			details[key] = value
		}
	}
	event.DetailsJSON = details
	if err := a.events.RecordExecutionEvent(context.WithoutCancel(ctx), event); err != nil {
		slog.Error("Failed to record responses model call event", "request_audit_id", requestAuditID, "path", logInfo.Path, "err", err)
		return
	}
	slog.Info("Responses model call event recorded",
		"event_type", event.EventType,
		"phase", event.Phase,
		"status", event.Status,
		"request_audit_id", requestAuditID,
		"trace_id", logInfo.Header.Meta.RequestID,
		"cassette_path", logInfo.Path,
		"upstream_id", logInfo.Header.Meta.SelectedUpstreamID,
		"model", logInfo.Header.Meta.Model,
		"endpoint", logInfo.Header.Meta.Endpoint,
		"status_code", logInfo.Header.Meta.StatusCode,
		"duration_ms", logInfo.Header.Meta.DurationMs,
		"ttft_ms", logInfo.Header.Meta.TTFTMs,
	)
}

func (a *responsesChatCompletionsAdapter) recordUpstreamExchange(ctx context.Context, logInfo *recorder.LogInfo, startedAt time.Time, completedAt time.Time, statusCode int) {
	if a == nil || a.auditor == nil || logInfo == nil {
		return
	}
	requestAuditID, ok := responsesaudit.RequestAuditIDFromContext(ctx)
	if !ok {
		return
	}
	entry := responsesaudit.UpstreamExchange{
		RequestAuditID: requestAuditID,
		TraceID:        logInfo.Header.Meta.RequestID,
		CassettePath:   logInfo.Path,
		UpstreamID:     logInfo.Header.Meta.SelectedUpstreamID,
		RouteTarget:    logInfo.Header.Meta.SelectedUpstreamBaseURL,
		Model:          logInfo.Header.Meta.Model,
		Endpoint:       logInfo.Header.Meta.Endpoint,
		StatusCode:     statusCode,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		ErrorText:      logInfo.Header.Meta.Error,
	}
	if metadata, ok := runtime.ModelCallMetadataFromContext(ctx); ok {
		entry.ExchangeKind = metadata.ExchangeKind
		entry.ExchangeRole = metadata.ExchangeRole
		entry.SequenceIndex = metadata.SequenceIndex
		if metadata.ResponseID != "" {
			entry.ResponseID = metadata.ResponseID
			entry.ParentExchangeID = "entry:" + metadata.ResponseID
		}
	}
	if err := a.auditor.RecordUpstreamExchange(context.WithoutCancel(ctx), entry); err != nil {
		slog.Error("Failed to record responses upstream exchange", "request_audit_id", requestAuditID, "path", logInfo.Path, "err", err)
		return
	}
	slog.Info("Responses upstream exchange recorded",
		"request_audit_id", requestAuditID,
		"trace_id", logInfo.Header.Meta.RequestID,
		"cassette_path", logInfo.Path,
		"upstream_id", logInfo.Header.Meta.SelectedUpstreamID,
		"model", logInfo.Header.Meta.Model,
		"endpoint", logInfo.Header.Meta.Endpoint,
		"status_code", statusCode,
		"duration_ms", completedAt.Sub(startedAt).Milliseconds(),
		"ttft_ms", logInfo.Header.Meta.TTFTMs,
	)
}

func (h *Handler) serveLocalResponses(w http.ResponseWriter, r *http.Request) {
	if h.responsesHandler == nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()
	h.serveLocalResponsesWithBody(w, r, body)
}

func (h *Handler) serveLocalResponsesWithBody(w http.ResponseWriter, r *http.Request, body []byte) {
	if h.responsesHandler == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	localReq := r.Clone(r.Context())
	localReq.URL = cloneURL(r.URL)
	localReq.URL.Path = h.localResponsesTargetPath(r.URL.Path)
	localReq.URL.RawPath = ""
	localReq.Body = io.NopCloser(bytes.NewReader(body))
	localReq.ContentLength = int64(len(body))

	entryRecorder, err := h.prepareLocalResponsesEntryRecording(r, body, localReq.URL.Path)
	if err != nil {
		slog.Error("Failed to prepare local Responses entry recording", "path", r.URL.Path, "err", err)
		h.responsesHandler.ServeHTTP(w, localReq)
		return
	}

	tee := newResponsesEntryRecordingResponseWriter(w, entryRecorder)
	h.responsesHandler.ServeHTTP(tee, localReq)
	tee.finalize()
}

func (h *Handler) localResponsesPath(path string) bool {
	if h == nil || h.responsesPath == "" {
		return false
	}
	base := strings.TrimRight(h.responsesPath, "/")
	return path == base || strings.HasPrefix(path, base+"/")
}

func (h *Handler) localResponsesTargetPath(path string) string {
	base := strings.TrimRight(h.responsesPath, "/")
	suffix := strings.TrimPrefix(path, base)
	return "/v1/responses" + suffix
}

type responsesEntryRecorder struct {
	recorder   *recorder.Recorder
	logInfo    *recorder.LogInfo
	startedAt  time.Time
	targetPath string
	pipeline   *llm.ResponsePipeline
	bodySample bytes.Buffer
	finalized  bool
}

func (h *Handler) prepareLocalResponsesEntryRecording(r *http.Request, body []byte, targetPath string) (*responsesEntryRecorder, error) {
	if h == nil || h.recorder == nil {
		return nil, fmt.Errorf("responses entry recorder is required")
	}
	recordReq := r.Clone(r.Context())
	recordReq.URL = cloneURL(r.URL)
	recordReq.Body = io.NopCloser(bytes.NewReader(body))
	recordReq.ContentLength = int64(len(body))
	recordReq.RequestURI = recordReq.URL.RequestURI()

	startedAt := time.Now()
	logInfo, err := h.recorder.PrepareLogFileWithOptionsAndBody(recordReq, recorder.PrepareOptions{
		SiteURL:      "http://llm-tracelab.local",
		ExchangeKind: "entry",
		ExchangeRole: "client_request",
	}, body)
	if err != nil {
		return nil, err
	}
	logInfo.Events = append(logInfo.Events, recorder.RecordEvent{
		Type:   "responses.entry.target",
		Time:   startedAt,
		Method: r.Method,
		URL:    recordReq.URL.RequestURI(),
		Attributes: map[string]interface{}{
			"target_path": targetPath,
		},
	})
	return &responsesEntryRecorder{
		recorder:   h.recorder,
		logInfo:    logInfo,
		startedAt:  startedAt,
		targetPath: targetPath,
	}, nil
}

func (r *responsesEntryRecorder) writeHeader(statusCode int, header http.Header) {
	if r == nil || r.logInfo == nil || r.logInfo.File == nil {
		return
	}
	if _, err := r.logInfo.File.Write([]byte("\n")); err != nil {
		r.logInfo.Header.Meta.Error = "write responses entry response separator: " + err.Error()
		return
	}
	status := fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode))
	headerBuf := bytes.NewBufferString("HTTP/1.1 " + status + "\r\n")
	header.Write(headerBuf)
	headerBuf.WriteString("\r\n")
	n, err := r.logInfo.File.Write(headerBuf.Bytes())
	if err != nil {
		r.logInfo.Header.Meta.Error = "write responses entry response header: " + err.Error()
		return
	}
	r.logInfo.Header.Layout.ResHeaderLen = int64(n)
	r.logInfo.Header.Meta.StatusCode = statusCode
	isStream := llm.DetectStreamingResponse(header)
	r.logInfo.Header.Layout.IsStream = isStream
	r.pipeline = llm.NewResponsePipeline(r.logInfo.Header.Meta.Provider, r.logInfo.Header.Meta.Endpoint, isStream)
}

func (r *responsesEntryRecorder) writeBody(data []byte) {
	if r == nil || r.logInfo == nil || r.logInfo.File == nil || len(data) == 0 {
		return
	}
	if r.logInfo.Header.Meta.TTFTMs <= 0 && !r.startedAt.IsZero() {
		r.logInfo.Header.Meta.TTFTMs = time.Since(r.startedAt).Milliseconds()
	}
	written, err := r.logInfo.File.Write(data)
	if err != nil {
		r.logInfo.Header.Meta.Error = "write responses entry response body: " + err.Error()
		return
	}
	r.logInfo.Header.Layout.ResBodyLen += int64(written)
	if r.pipeline != nil {
		r.pipeline.Feed(data[:written])
		if usage, ok := r.pipeline.Usage(); ok {
			r.logInfo.Header.Usage = recorder.UsageInfo(usage)
		}
	}
	if r.bodySample.Len() < responsesEntryMaxCapturedBodyBytes {
		remaining := responsesEntryMaxCapturedBodyBytes - r.bodySample.Len()
		if remaining > written {
			remaining = written
		}
		_, _ = r.bodySample.Write(data[:remaining])
	}
}

func (r *responsesEntryRecorder) finalize(statusCode int) {
	if r == nil || r.finalized || r.logInfo == nil {
		return
	}
	r.finalized = true
	if r.pipeline != nil {
		r.pipeline.Finalize()
		if usage, ok := r.pipeline.Usage(); ok {
			r.logInfo.Header.Usage = recorder.UsageInfo(usage)
		}
		r.logInfo.Events = append(r.logInfo.Events, r.pipeline.Events()...)
	}
	duration := time.Since(r.startedAt)
	r.logInfo.Header.Meta.DurationMs = duration.Milliseconds()
	r.logInfo.Header.Meta.ContentLength = r.logInfo.Header.Layout.ResBodyLen
	if r.logInfo.Header.Meta.StatusCode == 0 {
		r.logInfo.Header.Meta.StatusCode = statusCode
	}
	if responseID := extractResponsesEntryResponseID(r.bodySample.Bytes(), r.logInfo.Header.Layout.IsStream); responseID != "" {
		r.logInfo.Header.Meta.ResponseID = responseID
		if r.logInfo.Header.Meta.ExchangeID == "" {
			r.logInfo.Header.Meta.ExchangeID = "entry:" + responseID
		}
	}
	r.logInfo.Events = append(r.logInfo.Events, recorder.RecordEvent{
		Type:       "responses.entry.completed",
		Time:       r.startedAt.Add(duration),
		Method:     r.logInfo.Header.Meta.Method,
		URL:        r.logInfo.Header.Meta.URL,
		StatusCode: r.logInfo.Header.Meta.StatusCode,
		IsStream:   r.logInfo.Header.Layout.IsStream,
		Attributes: map[string]interface{}{
			"target_path": r.targetPath,
		},
	})
	if err := r.recorder.UpdateLogFile(r.logInfo); err != nil {
		slog.Error("Failed to update local Responses entry recording", "path", r.logInfo.Path, "err", err)
	}
}

func extractResponsesEntryResponseID(body []byte, isStream bool) string {
	if len(body) == 0 {
		return ""
	}
	if !isStream {
		var payload struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &payload); err == nil {
			return strings.TrimSpace(payload.ID)
		}
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if id := strings.TrimSpace(event.Response.ID); id != "" {
			return id
		}
		if id := strings.TrimSpace(event.ID); strings.HasPrefix(id, "resp_") {
			return id
		}
	}
	return ""
}

type responsesEntryRecordingResponseWriter struct {
	w           http.ResponseWriter
	recorder    *responsesEntryRecorder
	statusCode  int
	wroteHeader bool
}

func newResponsesEntryRecordingResponseWriter(w http.ResponseWriter, recorder *responsesEntryRecorder) *responsesEntryRecordingResponseWriter {
	return &responsesEntryRecordingResponseWriter{
		w:          w,
		recorder:   recorder,
		statusCode: http.StatusOK,
	}
}

func (w *responsesEntryRecordingResponseWriter) Header() http.Header {
	return w.w.Header()
}

func (w *responsesEntryRecordingResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode
	w.recorder.writeHeader(statusCode, w.w.Header())
	w.w.WriteHeader(statusCode)
}

func (w *responsesEntryRecordingResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.w.Write(data)
	if n > 0 {
		w.recorder.writeBody(data[:n])
	}
	return n, err
}

func (w *responsesEntryRecordingResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responsesEntryRecordingResponseWriter) finalize() {
	if !w.wroteHeader {
		w.recorder.writeHeader(w.statusCode, w.w.Header())
	}
	w.recorder.finalize(w.statusCode)
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
	applyCorrelationHeaders(req.Header, req.Context())
}

func applyCorrelationHeaders(dst http.Header, ctx context.Context) {
	headers, ok := responsesaudit.CorrelationHeadersFromContext(ctx)
	if !ok {
		return
	}
	for key, values := range headers {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func recordResponsesServerChatResponse(logInfo *recorder.LogInfo, resp *http.Response, startedAt time.Time) ([]byte, bool, error) {
	if logInfo == nil || logInfo.File == nil {
		return nil, false, fmt.Errorf("responses chat completions log file is required")
	}
	sniffer, isStream, err := responsesServerChatResponseSniffer(logInfo, resp, startedAt)
	if err != nil {
		return nil, false, err
	}
	respBody, readErr := io.ReadAll(sniffer)
	closeErr := sniffer.Close()
	if readErr != nil {
		return respBody, isStream, fmt.Errorf("read chat completion response body: %w", readErr)
	}
	if closeErr != nil {
		return respBody, isStream, fmt.Errorf("close chat completion response body: %w", closeErr)
	}
	return respBody, isStream, nil
}

func recordResponsesServerChatStreamResponse(logInfo *recorder.LogInfo, resp *http.Response, startedAt time.Time, handle runtime.ChatStreamCallback) (runtime.ChatCompletionResponse, bool, error) {
	if logInfo == nil || logInfo.File == nil {
		return runtime.ChatCompletionResponse{}, false, fmt.Errorf("responses chat completions log file is required")
	}
	sniffer, isStream, err := responsesServerChatResponseSniffer(logInfo, resp, startedAt)
	if err != nil {
		return runtime.ChatCompletionResponse{}, false, err
	}
	chatResp, readErr := chatclient.AggregateChatCompletionStreamWithCallback(sniffer, handle)
	closeErr := sniffer.Close()
	if readErr != nil {
		return runtime.ChatCompletionResponse{}, isStream, readErr
	}
	if closeErr != nil {
		return runtime.ChatCompletionResponse{}, isStream, fmt.Errorf("close chat completion response body: %w", closeErr)
	}
	return chatResp, isStream, nil
}

func responsesServerChatResponseSniffer(logInfo *recorder.LogInfo, resp *http.Response, startedAt time.Time) (*UsageSniffer, bool, error) {
	if _, err := logInfo.File.Write([]byte("\n")); err != nil {
		return nil, false, fmt.Errorf("write chat completion response separator: %w", err)
	}
	headerBuf := bytes.NewBufferString(fmt.Sprintf("%s %s\r\n", resp.Proto, resp.Status))
	resp.Header.Write(headerBuf)
	headerBuf.WriteString("\r\n")
	nHead, err := logInfo.File.Write(headerBuf.Bytes())
	if err != nil {
		return nil, false, fmt.Errorf("write chat completion response header: %w", err)
	}
	logInfo.Header.Layout.ResHeaderLen = int64(nHead)

	isStream := llm.DetectStreamingResponse(resp.Header)
	return &UsageSniffer{
		Source:   resp.Body,
		File:     logInfo.File,
		Count:    &logInfo.Header.Layout.ResBodyLen,
		Usage:    &logInfo.Header.Usage,
		Pipeline: llm.NewResponsePipeline(logInfo.Header.Meta.Provider, logInfo.Header.Meta.Endpoint, isStream),
		Events:   &logInfo.Events,
		Start:    startedAt,
		TTFTMs:   &logInfo.Header.Meta.TTFTMs,
	}, isStream, nil
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

func responsesModelCallFailureStatus(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

func cloneURL(in *url.URL) *url.URL {
	if in == nil {
		return &url.URL{}
	}
	out := *in
	return &out
}
