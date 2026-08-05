package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

const defaultMaxBodyBytes int64 = 16 << 20
const statusClientClosedRequest = 499

type Runtime interface {
	Create(ctx context.Context, req protocol.CreateResponseRequest) (protocol.Response, error)
	Compact(ctx context.Context, req protocol.CompactResponseRequest) (protocol.Response, error)
	InputItems(ctx context.Context, id string) (protocol.InputItemList, bool, error)
}

type streamingRuntime interface {
	CreateStream(ctx context.Context, req protocol.CreateResponseRequest, sink runtime.ResponseStreamSink) (protocol.Response, error)
}

type Handler struct {
	runtime           Runtime
	maxBodyBytes      int64
	auditor           audit.RequestAuditor
	events            audit.ExecutionEventRecorder
	codexCompat       CodexCompatOptions
	codexCompatActive bool
}

type Option func(*Handler)

type CodexCompatOptions struct {
	Enabled               bool
	InjectWhenToolsAbsent bool
	PreserveClientTools   bool
	AvailableHostedTools  []protocol.Tool
	DefaultToolChoice     any
}

func WithMaxBodyBytes(limit int64) Option {
	return func(h *Handler) {
		if limit > 0 {
			h.maxBodyBytes = limit
		}
	}
}

func WithRequestAuditor(auditor audit.RequestAuditor) Option {
	return func(h *Handler) {
		h.auditor = auditor
	}
}

func WithExecutionEventRecorder(recorder audit.ExecutionEventRecorder) Option {
	return func(h *Handler) {
		h.events = recorder
	}
}

func WithCodexCompat(options CodexCompatOptions) Option {
	return func(h *Handler) {
		h.codexCompat = options
		h.codexCompatActive = true
	}
}

func NewHandler(rt Runtime, opts ...Option) http.Handler {
	h := &Handler{
		runtime:      rt,
		maxBodyBytes: defaultMaxBodyBytes,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.runtime == nil {
		writeError(w, http.StatusInternalServerError, "runtime is required", "server_error", "server_error")
		return
	}
	r = r.WithContext(audit.ContextWithCorrelationHeaders(r.Context(), r.Header))
	switch {
	case r.URL.Path == "/v1/responses":
		h.serveResponses(w, r)
	case r.URL.Path == "/v1/responses/compact":
		h.serveCompact(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/responses/"):
		h.serveResponseSubresource(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found", "invalid_request_error", "not_found")
	}
}

func (h *Handler) serveResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request body: %v", err), "invalid_request_error", "invalid_json")
		return
	}

	var req protocol.CreateResponseRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request body: %v", err), "invalid_request_error", "invalid_json")
		return
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object", "invalid_request_error", "invalid_json")
		return
	} else if !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object", "invalid_request_error", "invalid_json")
		return
	}

	normalizedBody := body
	normalization := h.normalizeCodexCompatRequest(body, &req)
	if normalization.Changed {
		encoded, err := json.Marshal(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request body: %v", err), "invalid_request_error", "invalid_json")
			return
		}
		normalizedBody = encoded
	}

	auditID := h.auditAccepted(r, normalizedBody)
	eventDetails := map[string]any{
		"request_audit_id": auditID,
		"method":           r.Method,
		"path":             r.URL.Path,
	}
	if len(normalization.InjectedToolTypes) > 0 {
		eventDetails["codex_compat"] = map[string]any{
			"normalized":            true,
			"injected_hosted_tools": normalization.InjectedToolTypes,
			"tool_choice_defaulted": normalization.ToolChoiceDefaulted,
		}
	}
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		EventType:   "response.request",
		Phase:       "request",
		Status:      "accepted",
		DetailsJSON: eventDetails,
	})
	if req.Stream {
		h.serveResponseStream(w, r, auditID, req)
		return
	}

	ctx := audit.ContextWithRequestAuditID(r.Context(), auditID)
	resp, err := h.runtime.Create(ctx, req)
	if err != nil {
		failureStatus := runtimeFailureStatus(err)
		h.auditRejected(r, auditID, failureStatus, err.Error())
		h.recordExecutionEvent(r, audit.ExecutionEvent{
			EventType: "response.request",
			Phase:     "request",
			Status:    failureStatus,
			Message:   err.Error(),
			DetailsJSON: map[string]any{
				"request_audit_id": auditID,
			},
		})
		writeRuntimeError(w, err)
		return
	}
	h.auditCompleted(r, auditID, resp)
	completion := audit.CompletionFromResponse(resp)
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.request",
		Phase:          "request",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"request_audit_id": auditID,
		},
	})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) serveCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request body: %v", err), "invalid_request_error", "invalid_json")
		return
	}

	var req protocol.CompactResponseRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request body: %v", err), "invalid_request_error", "invalid_json")
		return
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object", "invalid_request_error", "invalid_json")
		return
	} else if !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object", "invalid_request_error", "invalid_json")
		return
	}

	auditID := h.auditAccepted(r, body)
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		EventType: "response.compact",
		Phase:     "compact",
		Status:    "started",
		DetailsJSON: map[string]any{
			"request_audit_id":   auditID,
			"target_response_id": req.ResponseID,
			"path":               r.URL.Path,
		},
	})

	ctx := audit.ContextWithRequestAuditID(r.Context(), auditID)
	resp, err := h.runtime.Compact(ctx, req)
	if err != nil {
		failureStatus := runtimeFailureStatus(err)
		h.auditRejected(r, auditID, failureStatus, err.Error())
		h.recordExecutionEvent(r, audit.ExecutionEvent{
			EventType: "response.compact",
			Phase:     "compact",
			Status:    failureStatus,
			Message:   err.Error(),
			DetailsJSON: map[string]any{
				"request_audit_id":   auditID,
				"target_response_id": req.ResponseID,
			},
		})
		writeRuntimeError(w, err)
		return
	}
	h.auditCompleted(r, auditID, resp)
	completion := audit.CompletionFromResponse(resp)
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.compact",
		Phase:          "compact",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"request_audit_id":   auditID,
			"target_response_id": req.ResponseID,
		},
	})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) serveResponseStream(w http.ResponseWriter, r *http.Request, auditID string, req protocol.CreateResponseRequest) {
	if streamer, ok := h.runtime.(streamingRuntime); ok {
		if fallback := h.serveIncrementalResponseStream(w, r, auditID, req, streamer); !fallback {
			return
		}
	}

	ctx := audit.ContextWithRequestAuditID(r.Context(), auditID)
	resp, err := h.runtime.Create(ctx, req)
	if err != nil {
		failureStatus := runtimeFailureStatus(err)
		h.auditRejected(r, auditID, failureStatus, err.Error())
		h.recordExecutionEvent(r, audit.ExecutionEvent{
			EventType: "response.request",
			Phase:     "request",
			Status:    failureStatus,
			Message:   err.Error(),
			DetailsJSON: map[string]any{
				"request_audit_id": auditID,
				"stream":           true,
			},
		})
		writeRuntimeError(w, err)
		return
	}

	h.auditCompleted(r, auditID, resp)
	completion := audit.CompletionFromResponse(resp)
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.stream",
		Phase:          "stream",
		Status:         "started",
		DetailsJSON: map[string]any{
			"request_audit_id": auditID,
			"mode":             "deferred",
		},
	})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	writer := &streamWriter{w: w}
	if err := writer.writeResponse(resp); err != nil {
		h.recordExecutionEvent(r, audit.ExecutionEvent{
			ResponseID:     completion.ResponseID,
			ConversationID: completion.ConversationID,
			EventType:      "response.stream",
			Phase:          "stream",
			Status:         "failed",
			Message:        err.Error(),
			DetailsJSON: map[string]any{
				"request_audit_id": auditID,
				"mode":             "deferred",
			},
		})
		return
	}
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.stream",
		Phase:          "stream",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"request_audit_id": auditID,
			"mode":             "deferred",
		},
	})
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.request",
		Phase:          "request",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"request_audit_id": auditID,
			"stream":           true,
		},
	})
}

func (h *Handler) serveIncrementalResponseStream(w http.ResponseWriter, r *http.Request, auditID string, req protocol.CreateResponseRequest, streamer streamingRuntime) bool {
	ctx := audit.ContextWithRequestAuditID(r.Context(), auditID)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	writer := &streamWriter{w: w}
	sink := &auditedStreamSink{writer: writer, h: h, r: r, auditID: auditID}

	resp, err := streamer.CreateStream(ctx, req, sink)
	if err != nil {
		if errors.Is(err, runtime.ErrIncrementalStreamUnsupported) && !writer.wrote {
			h.recordExecutionEvent(r, audit.ExecutionEvent{
				EventType: "response.stream",
				Phase:     "stream",
				Status:    "fallback",
				Message:   err.Error(),
				DetailsJSON: map[string]any{
					"request_audit_id": auditID,
					"stream":           true,
					"from_mode":        "incremental",
					"to_mode":          "deferred",
					"reason":           err.Error(),
				},
			})
			return true
		}
		if writer.wrote {
			_ = writer.ResponseFailed(err)
		}
		failureStatus := runtimeFailureStatus(err)
		h.auditRejected(r, auditID, failureStatus, err.Error())
		eventType := "response.request"
		phase := "request"
		if writer.wrote {
			eventType = "response.stream"
			phase = "stream"
		}
		h.recordExecutionEvent(r, audit.ExecutionEvent{
			EventType: eventType,
			Phase:     phase,
			Status:    failureStatus,
			Message:   err.Error(),
			DetailsJSON: map[string]any{
				"request_audit_id": auditID,
				"stream":           true,
			},
		})
		if !writer.wrote {
			writeRuntimeError(w, err)
		}
		return false
	}

	h.auditCompleted(r, auditID, resp)
	completion := audit.CompletionFromResponse(resp)
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.stream",
		Phase:          "stream",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"request_audit_id": auditID,
			"mode":             "incremental",
		},
	})
	h.recordExecutionEvent(r, audit.ExecutionEvent{
		ResponseID:     completion.ResponseID,
		ConversationID: completion.ConversationID,
		EventType:      "response.request",
		Phase:          "request",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"request_audit_id": auditID,
			"stream":           true,
		},
	})
	return false
}

type auditedStreamSink struct {
	writer  *streamWriter
	h       *Handler
	r       *http.Request
	auditID string
	started bool
}

func (s *auditedStreamSink) ResponseCreated(resp protocol.Response) error {
	s.start()
	return s.writer.ResponseCreated(resp)
}

func (s *auditedStreamSink) ResponseInProgress(resp protocol.Response) error {
	s.start()
	return s.writer.ResponseInProgress(resp)
}

func (s *auditedStreamSink) OutputTextDelta(delta runtime.ResponseTextDelta) error {
	s.start()
	return s.writer.OutputTextDelta(delta)
}

func (s *auditedStreamSink) OutputTextDone(done runtime.ResponseTextDone) error {
	s.start()
	return s.writer.OutputTextDone(done)
}

func (s *auditedStreamSink) ContentPartAdded(added runtime.ResponseContentPartAdded) error {
	s.start()
	return s.writer.ContentPartAdded(added)
}

func (s *auditedStreamSink) ContentPartDone(done runtime.ResponseContentPartDone) error {
	s.start()
	return s.writer.ContentPartDone(done)
}

func (s *auditedStreamSink) FunctionCallArgumentsDelta(delta runtime.ResponseFunctionCallArgumentsDelta) error {
	s.start()
	return s.writer.FunctionCallArgumentsDelta(delta)
}

func (s *auditedStreamSink) FunctionCallArgumentsDone(done runtime.ResponseFunctionCallArgumentsDone) error {
	s.start()
	return s.writer.FunctionCallArgumentsDone(done)
}

func (s *auditedStreamSink) OutputItemAdded(added runtime.ResponseOutputItemAdded) error {
	s.start()
	return s.writer.OutputItemAdded(added)
}

func (s *auditedStreamSink) OutputItemDone(done runtime.ResponseOutputItemDone) error {
	s.start()
	return s.writer.OutputItemDone(done)
}

func (s *auditedStreamSink) ResponseCompleted(resp protocol.Response) error {
	s.start()
	return s.writer.ResponseCompleted(resp)
}

func (s *auditedStreamSink) start() {
	if s.started {
		return
	}
	s.started = true
	s.h.recordExecutionEvent(s.r, audit.ExecutionEvent{
		EventType: "response.stream",
		Phase:     "stream",
		Status:    "started",
		DetailsJSON: map[string]any{
			"request_audit_id": s.auditID,
			"mode":             "incremental",
		},
	})
}

type streamWriter struct {
	w     http.ResponseWriter
	wrote bool
}

func (s *streamWriter) writeResponse(resp protocol.Response) error {
	if err := s.write("response.created", protocol.StreamEvent{Type: "response.created", Response: &resp}); err != nil {
		return err
	}
	if err := s.write("response.in_progress", protocol.StreamEvent{Type: "response.in_progress", Response: &resp}); err != nil {
		return err
	}
	for outputIndex, item := range resp.Output {
		if err := s.writeOutputItem(outputIndex, item); err != nil {
			return err
		}
	}
	return s.write("response.completed", protocol.StreamEvent{Type: "response.completed", Response: &resp})
}

func (s *streamWriter) ResponseCreated(resp protocol.Response) error {
	return s.write("response.created", protocol.StreamEvent{Type: "response.created", Response: &resp})
}

func (s *streamWriter) ResponseInProgress(resp protocol.Response) error {
	return s.write("response.in_progress", protocol.StreamEvent{Type: "response.in_progress", Response: &resp})
}

func (s *streamWriter) OutputTextDelta(delta runtime.ResponseTextDelta) error {
	outputIndex := delta.OutputIndex
	contentIndex := delta.ContentIndex
	return s.write("response.output_text.delta", protocol.StreamEvent{
		Type:         "response.output_text.delta",
		OutputIndex:  &outputIndex,
		ItemID:       delta.ItemID,
		ContentIndex: &contentIndex,
		Delta:        delta.Delta,
	})
}

func (s *streamWriter) OutputTextDone(done runtime.ResponseTextDone) error {
	outputIndex := done.OutputIndex
	contentIndex := done.ContentIndex
	return s.write("response.output_text.done", protocol.StreamEvent{
		Type:         "response.output_text.done",
		OutputIndex:  &outputIndex,
		ItemID:       done.ItemID,
		ContentIndex: &contentIndex,
		Text:         done.Text,
	})
}

func (s *streamWriter) ContentPartAdded(added runtime.ResponseContentPartAdded) error {
	outputIndex := added.OutputIndex
	contentIndex := added.ContentIndex
	return s.write("response.content_part.added", protocol.StreamEvent{
		Type:         "response.content_part.added",
		OutputIndex:  &outputIndex,
		ItemID:       added.ItemID,
		ContentIndex: &contentIndex,
		Part:         &added.Part,
	})
}

func (s *streamWriter) ContentPartDone(done runtime.ResponseContentPartDone) error {
	outputIndex := done.OutputIndex
	contentIndex := done.ContentIndex
	return s.write("response.content_part.done", protocol.StreamEvent{
		Type:         "response.content_part.done",
		OutputIndex:  &outputIndex,
		ItemID:       done.ItemID,
		ContentIndex: &contentIndex,
		Part:         &done.Part,
	})
}

func (s *streamWriter) FunctionCallArgumentsDelta(delta runtime.ResponseFunctionCallArgumentsDelta) error {
	outputIndex := delta.OutputIndex
	return s.write("response.function_call_arguments.delta", protocol.StreamEvent{
		Type:        "response.function_call_arguments.delta",
		OutputIndex: &outputIndex,
		ItemID:      delta.ItemID,
		Delta:       delta.Delta,
		Arguments:   delta.Arguments,
	})
}

func (s *streamWriter) FunctionCallArgumentsDone(done runtime.ResponseFunctionCallArgumentsDone) error {
	outputIndex := done.OutputIndex
	return s.write("response.function_call_arguments.done", protocol.StreamEvent{
		Type:        "response.function_call_arguments.done",
		OutputIndex: &outputIndex,
		ItemID:      done.ItemID,
		Arguments:   done.Arguments,
	})
}

func (s *streamWriter) OutputItemAdded(added runtime.ResponseOutputItemAdded) error {
	outputIndex := added.OutputIndex
	return s.write("response.output_item.added", protocol.StreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: &outputIndex,
		Item:        &added.Item,
	})
}

func (s *streamWriter) OutputItemDone(done runtime.ResponseOutputItemDone) error {
	outputIndex := done.OutputIndex
	return s.write("response.output_item.done", protocol.StreamEvent{
		Type:        "response.output_item.done",
		OutputIndex: &outputIndex,
		Item:        &done.Item,
	})
}

func (s *streamWriter) ResponseCompleted(resp protocol.Response) error {
	return s.write("response.completed", protocol.StreamEvent{Type: "response.completed", Response: &resp})
}

func (s *streamWriter) ResponseFailed(err error) error {
	body := runtimeErrorBody(err)
	return s.write("response.failed", protocol.StreamEvent{Type: "response.failed", Error: &body})
}

func (s *streamWriter) writeOutputItem(outputIndex int, item protocol.OutputItem) error {
	index := outputIndex
	if err := s.write("response.output_item.added", protocol.StreamEvent{
		Type:        "response.output_item.added",
		OutputIndex: &index,
		Item:        &item,
	}); err != nil {
		return err
	}
	if item.Type == "message" {
		for contentIndex, part := range item.Content {
			idx := contentIndex
			if err := s.write("response.content_part.added", protocol.StreamEvent{
				Type:         "response.content_part.added",
				OutputIndex:  &index,
				ItemID:       item.ID,
				ContentIndex: &idx,
				Part:         &part,
			}); err != nil {
				return err
			}
			if part.Text != "" {
				if err := s.write("response.output_text.delta", protocol.StreamEvent{
					Type:         "response.output_text.delta",
					OutputIndex:  &index,
					ItemID:       item.ID,
					ContentIndex: &idx,
					Delta:        part.Text,
				}); err != nil {
					return err
				}
				if err := s.write("response.output_text.done", protocol.StreamEvent{
					Type:         "response.output_text.done",
					OutputIndex:  &index,
					ItemID:       item.ID,
					ContentIndex: &idx,
					Text:         part.Text,
				}); err != nil {
					return err
				}
			}
			if err := s.write("response.content_part.done", protocol.StreamEvent{
				Type:         "response.content_part.done",
				OutputIndex:  &index,
				ItemID:       item.ID,
				ContentIndex: &idx,
				Part:         &part,
			}); err != nil {
				return err
			}
		}
	} else if item.Type == "function_call" && item.Arguments != "" {
		if err := s.write("response.function_call_arguments.delta", protocol.StreamEvent{
			Type:        "response.function_call_arguments.delta",
			OutputIndex: &index,
			ItemID:      item.ID,
			Delta:       item.Arguments,
			Arguments:   item.Arguments,
		}); err != nil {
			return err
		}
		if err := s.write("response.function_call_arguments.done", protocol.StreamEvent{
			Type:        "response.function_call_arguments.done",
			OutputIndex: &index,
			ItemID:      item.ID,
			Arguments:   item.Arguments,
		}); err != nil {
			return err
		}
	}
	return s.write("response.output_item.done", protocol.StreamEvent{
		Type:        "response.output_item.done",
		OutputIndex: &index,
		Item:        &item,
	})
}

func (s *streamWriter) write(event string, payload protocol.StreamEvent) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(s.w, "event: "+event+"\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(s.w, "data: "+string(data)+"\n\n"); err != nil {
		return err
	}
	if flusher, ok := s.w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.wrote = true
	return nil
}

func (h *Handler) auditAccepted(r *http.Request, body []byte) string {
	if h.auditor == nil {
		return ""
	}
	id, err := h.auditor.Accepted(auditWriteContext(r), audit.NewRequestEntry(r, body))
	if err != nil {
		slog.Error("Failed to write responses request audit", "err", err)
		return ""
	}
	return id
}

type codexCompatNormalization struct {
	Changed             bool
	InjectedToolTypes   []string
	ToolChoiceDefaulted bool
}

func (h *Handler) normalizeCodexCompatRequest(body []byte, req *protocol.CreateResponseRequest) codexCompatNormalization {
	if !h.codexCompatActive || !h.codexCompat.Enabled || !h.codexCompat.InjectWhenToolsAbsent || req == nil || jsonObjectHasField(body, "tools") || len(req.Tools) > 0 {
		return codexCompatNormalization{}
	}

	tool, ok := h.firstAvailableCodexHostedTool()
	if !ok {
		return codexCompatNormalization{}
	}

	req.Tools = []protocol.Tool{tool}
	result := codexCompatNormalization{
		Changed:           true,
		InjectedToolTypes: []string{tool.Type},
	}
	if req.ToolChoice == nil {
		choice := h.codexCompat.DefaultToolChoice
		if choice == nil {
			choice = "auto"
		}
		req.ToolChoice = choice
		result.ToolChoiceDefaulted = true
	}
	return result
}

func (h *Handler) firstAvailableCodexHostedTool() (protocol.Tool, bool) {
	for _, tool := range h.codexCompat.AvailableHostedTools {
		if isCodexCompatHostedToolType(tool.Type) {
			return tool, true
		}
	}
	return protocol.Tool{}, false
}

func isCodexCompatHostedToolType(toolType string) bool {
	switch toolType {
	case "web_search", "web_search_preview":
		return true
	default:
		return false
	}
}

func jsonObjectHasField(body []byte, field string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	_, ok := raw[field]
	return ok
}

func (h *Handler) auditCompleted(r *http.Request, id string, resp protocol.Response) {
	if h.auditor == nil || id == "" {
		return
	}
	if err := h.auditor.Completed(auditWriteContext(r), id, audit.CompletionFromResponse(resp)); err != nil {
		slog.Error("Failed to complete responses request audit", "audit_id", id, "err", err)
	}
}

func (h *Handler) auditRejected(r *http.Request, id string, status string, errorText string) {
	if h.auditor == nil || id == "" {
		return
	}
	if err := h.auditor.Rejected(auditWriteContext(r), id, audit.Failure{Status: status, ErrorText: errorText}); err != nil {
		slog.Error("Failed to reject responses request audit", "audit_id", id, "err", err)
	}
}

func (h *Handler) recordExecutionEvent(r *http.Request, event audit.ExecutionEvent) {
	if h.events == nil {
		return
	}
	if err := h.events.RecordExecutionEvent(auditWriteContext(r), event); err != nil {
		slog.Error("Failed to write responses execution event", "event_type", event.EventType, "status", event.Status, "err", err)
		return
	}
	slog.Info("Responses execution event recorded", responsesHTTPEventLogAttrs(event)...)
}

func responsesHTTPEventLogAttrs(event audit.ExecutionEvent) []any {
	attrs := []any{
		"event_type", event.EventType,
		"phase", event.Phase,
		"status", event.Status,
	}
	if event.ResponseID != "" {
		attrs = append(attrs, "response_id", event.ResponseID)
	}
	if event.RequestAuditID != "" {
		attrs = append(attrs, "request_audit_id", event.RequestAuditID)
	}
	if event.ConversationID != "" {
		attrs = append(attrs, "conversation_id", event.ConversationID)
	}
	if event.Message != "" {
		attrs = append(attrs, "message", event.Message)
	}
	for _, key := range []string{
		"request_audit_id",
		"method",
		"path",
		"stream",
		"fallback_reason",
		"target_response_id",
	} {
		if value, ok := event.DetailsJSON[key]; ok && value != nil {
			attrs = append(attrs, key, value)
		}
	}
	if compat, ok := event.DetailsJSON["codex_compat"]; ok {
		attrs = append(attrs, "codex_compat", compat)
	}
	return attrs
}

func (h *Handler) serveResponseSubresource(w http.ResponseWriter, r *http.Request) {
	id, resource, ok := responseSubresource(r.URL.Path)
	if !ok || resource != "input_items" {
		writeError(w, http.StatusNotFound, "not found", "invalid_request_error", "not_found")
		return
	}
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, found, err := h.runtime.InputItems(r.Context(), id)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if !found {
		writeNotFound(w, id)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func responseSubresource(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/v1/responses/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func writeRuntimeError(w http.ResponseWriter, err error) {
	var notFound runtime.ResponseNotFoundError
	if errors.As(err, &notFound) {
		writeNotFound(w, notFound.ID)
		return
	}
	var unsupportedTool runtime.UnsupportedHostedToolError
	if errors.As(err, &unsupportedTool) {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "unsupported_tool")
		return
	}
	if errors.Is(err, context.Canceled) {
		writeError(w, statusClientClosedRequest, "request cancelled", "server_error", "cancelled")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error(), "server_error", "server_error")
}

func runtimeErrorBody(err error) protocol.ErrorBody {
	var notFound runtime.ResponseNotFoundError
	if errors.As(err, &notFound) {
		message := "response not found"
		if notFound.ID != "" {
			message = fmt.Sprintf("response %q not found", notFound.ID)
		}
		return protocol.ErrorBody{
			Message: message,
			Type:    "invalid_request_error",
			Code:    "not_found",
		}
	}
	if errors.Is(err, context.Canceled) {
		return protocol.ErrorBody{
			Message: "request cancelled",
			Type:    "server_error",
			Code:    "cancelled",
		}
	}
	var unsupportedTool runtime.UnsupportedHostedToolError
	if errors.As(err, &unsupportedTool) {
		return protocol.ErrorBody{
			Message: err.Error(),
			Type:    "invalid_request_error",
			Code:    "unsupported_tool",
		}
	}
	return protocol.ErrorBody{
		Message: err.Error(),
		Type:    "server_error",
		Code:    "server_error",
	}
}

func runtimeFailureStatus(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

func auditWriteContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return context.WithoutCancel(r.Context())
}

func writeNotFound(w http.ResponseWriter, id string) {
	message := "response not found"
	if id != "" {
		message = fmt.Sprintf("response %q not found", id)
	}
	writeError(w, http.StatusNotFound, message, "invalid_request_error", "not_found")
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
}

func writeError(w http.ResponseWriter, status int, message, typ, code string) {
	writeJSON(w, status, protocol.ErrorResponse{
		Error: protocol.ErrorBody{
			Message: message,
			Type:    typ,
			Code:    code,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
