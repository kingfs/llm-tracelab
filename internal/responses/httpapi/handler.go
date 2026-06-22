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

type Runtime interface {
	Create(ctx context.Context, req protocol.CreateResponseRequest) (protocol.Response, error)
	InputItems(ctx context.Context, id string) (protocol.InputItemList, bool, error)
}

type Handler struct {
	runtime      Runtime
	maxBodyBytes int64
	auditor      audit.RequestAuditor
}

type Option func(*Handler)

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
	switch {
	case r.URL.Path == "/v1/responses":
		h.serveResponses(w, r)
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

	auditID := h.auditAccepted(r, body)
	if req.Stream {
		h.auditRejected(r, auditID, "rejected", "streaming responses are not supported by the local responses server")
		writeError(w, http.StatusBadRequest, "streaming responses are not supported by the local responses server", "invalid_request_error", "unsupported_stream")
		return
	}

	ctx := audit.ContextWithRequestAuditID(r.Context(), auditID)
	resp, err := h.runtime.Create(ctx, req)
	if err != nil {
		h.auditRejected(r, auditID, "failed", err.Error())
		writeRuntimeError(w, err)
		return
	}
	h.auditCompleted(r, auditID, resp)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) auditAccepted(r *http.Request, body []byte) string {
	if h.auditor == nil {
		return ""
	}
	id, err := h.auditor.Accepted(r.Context(), audit.NewRequestEntry(r, body))
	if err != nil {
		slog.Error("Failed to write responses request audit", "err", err)
		return ""
	}
	return id
}

func (h *Handler) auditCompleted(r *http.Request, id string, resp protocol.Response) {
	if h.auditor == nil || id == "" {
		return
	}
	if err := h.auditor.Completed(r.Context(), id, audit.CompletionFromResponse(resp)); err != nil {
		slog.Error("Failed to complete responses request audit", "audit_id", id, "err", err)
	}
}

func (h *Handler) auditRejected(r *http.Request, id string, status string, errorText string) {
	if h.auditor == nil || id == "" {
		return
	}
	if err := h.auditor.Rejected(r.Context(), id, audit.Failure{Status: status, ErrorText: errorText}); err != nil {
		slog.Error("Failed to reject responses request audit", "audit_id", id, "err", err)
	}
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
	writeError(w, http.StatusInternalServerError, err.Error(), "server_error", "server_error")
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
