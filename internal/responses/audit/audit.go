package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

const DefaultBodyPreviewBytes = 2048

type RequestAuditor interface {
	Accepted(ctx context.Context, entry RequestEntry) (string, error)
	Completed(ctx context.Context, id string, result Completion) error
	Rejected(ctx context.Context, id string, failure Failure) error
}

type UpstreamExchangeRecorder interface {
	RecordUpstreamExchange(ctx context.Context, entry UpstreamExchange) error
}

type ExecutionEventRecorder interface {
	RecordExecutionEvent(ctx context.Context, event ExecutionEvent) error
}

type ToolCallAuditRecorder interface {
	RecordToolCallAudit(ctx context.Context, entry ToolCallAudit) (string, error)
}

type RequestEntry struct {
	Method          string
	Path            string
	ClientRequestID string
	HeaderJSON      map[string]any
	BodyPreview     string
	BodySha256      string
	CreatedAt       time.Time
}

type Completion struct {
	ResponseID     string
	ConversationID string
}

type Failure struct {
	Status    string
	ErrorText string
}

type UpstreamExchange struct {
	ResponseID     string
	RequestAuditID string
	TraceID        string
	CassettePath   string
	UpstreamID     string
	RouteTarget    string
	Model          string
	Endpoint       string
	StatusCode     int
	StartedAt      time.Time
	CompletedAt    time.Time
	ErrorText      string
}

type ExecutionEvent struct {
	ResponseID     string
	RequestAuditID string
	ConversationID string
	EventType      string
	Phase          string
	Status         string
	Message        string
	DetailsJSON    map[string]any
	OccurredAt     time.Time
}

type ToolCallAudit struct {
	ID             string
	ResponseID     string
	RequestAuditID string
	ConversationID string
	CallID         string
	ToolType       string
	ToolName       string
	Executor       string
	Status         string
	Phase          string
	InputJSON      map[string]any
	OutputJSON     map[string]any
	ErrorText      string
	MetadataJSON   map[string]any
	StartedAt      time.Time
	CompletedAt    time.Time
	CreatedAt      time.Time
}

func NewRequestEntry(r *http.Request, body []byte) RequestEntry {
	return RequestEntry{
		Method:          r.Method,
		Path:            r.URL.Path,
		ClientRequestID: r.Header.Get("X-Client-Request-Id"),
		HeaderJSON:      AllowedHeaders(r.Header),
		BodyPreview:     BodyPreview(body, DefaultBodyPreviewBytes),
		BodySha256:      BodySHA256(body),
		CreatedAt:       time.Now(),
	}
}

func CompletionFromResponse(resp protocol.Response) Completion {
	return Completion{
		ResponseID:     resp.ID,
		ConversationID: CodexConversationID(resp.Metadata),
	}
}

func BodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func BodyPreview(body []byte, limit int) string {
	if limit <= 0 || len(body) <= limit {
		return string(body)
	}
	return string(body[:limit])
}

func AllowedHeaders(header http.Header) map[string]any {
	out := map[string]any{}
	for _, item := range []struct {
		key        string
		headerName string
	}{
		{key: "content-type", headerName: "Content-Type"},
		{key: "user-agent", headerName: "User-Agent"},
		{key: "x-client-request-id", headerName: "X-Client-Request-Id"},
		{key: "session_id", headerName: "session_id"},
		{key: "x-codex-window-id", headerName: "X-Codex-Window-Id"},
	} {
		if value := headerValue(header, item.headerName); value != "" {
			out[item.key] = value
		}
	}
	return out
}

func CodexConversationID(metadata map[string]any) string {
	codex := map[string]any{}
	if topLevel, ok := stringAnyMap(metadata["codex"]); ok {
		mergeInto(codex, topLevel)
	}
	if gateway, ok := stringAnyMap(metadata["_gateway"]); ok {
		if gatewayCodex, ok := stringAnyMap(gateway["codex"]); ok {
			mergeInto(codex, gatewayCodex)
		}
	}
	value, _ := codex["thread_id"].(string)
	return value
}

func headerValue(header http.Header, name string) string {
	if value := header.Get(name); value != "" {
		return value
	}
	if values := header.Values(name); len(values) > 0 {
		return values[0]
	}
	if values := header[name]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func mergeInto(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if dstNested, ok := stringAnyMap(dst[key]); ok {
			if srcNested, ok := stringAnyMap(value); ok {
				mergedNested := map[string]any{}
				mergeInto(mergedNested, dstNested)
				mergeInto(mergedNested, srcNested)
				dst[key] = mergedNested
				continue
			}
		}
		dst[key] = value
	}
}

func stringAnyMap(value any) (map[string]any, bool) {
	mapped, ok := value.(map[string]any)
	return mapped, ok
}
