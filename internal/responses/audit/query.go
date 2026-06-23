package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/executionevent"
	"github.com/kingfs/llm-tracelab/ent/dao/predicate"
	"github.com/kingfs/llm-tracelab/ent/dao/requestaudit"
	"github.com/kingfs/llm-tracelab/ent/dao/toolcallaudit"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamexchange"
)

const (
	DefaultAuditQueryLimit = 100
	MaxAuditQueryLimit     = 500
)

var requestAuditStatusValues = []string{"accepted", "completed", "failed", "rejected", "cancelled"}
var requestAuditOperationValues = []string{"create", "compact", "input_items"}

type QueryService struct {
	client *dao.Client
}

func NewQueryService(client *dao.Client) *QueryService {
	return &QueryService{client: client}
}

type ListRequestAuditsParams struct {
	ResponseID      string
	RequestAuditID  string
	ClientRequestID string
	ConversationID  string
	Status          string
	Operation       string
	Limit           int
}

type GetRequestAuditTraceParams struct {
	ResponseID            string
	RequestAuditID        string
	ClientRequestID       string
	ConversationID        string
	EventLimit            int
	UpstreamExchangeLimit int
}

type ListToolCallAuditsParams struct {
	ResponseID     string
	RequestAuditID string
	ConversationID string
	CallID         string
	ToolName       string
	Status         string
	Limit          int
}

type RequestAuditReference struct {
	ResponseID     string
	RequestAuditID string
}

type RequestAuditView struct {
	ID              string
	ResponseID      string
	ConversationID  string
	Method          string
	Path            string
	Operation       string
	ClientRequestID string
	HeaderJSON      map[string]any
	BodyPreview     string
	BodySha256      string
	RedactionJSON   map[string]any
	Status          string
	ErrorText       string
	CreatedAt       time.Time
}

type ExecutionEventView struct {
	ID             string
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

type UpstreamExchangeView struct {
	ID             string
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

type ToolCallEventReference struct {
	ID         string
	Status     string
	Message    string
	OccurredAt time.Time
}

type ToolCallView struct {
	CallID           string
	ToolName         string
	Executor         string
	StatusesSeen     []string
	LatestStatus     string
	StartedAt        time.Time
	CompletedAt      time.Time
	ErrorText        string
	ArgumentsSummary string
	QuerySummary     string
	OutputSummary    string
	EventCount       int
	Events           []ToolCallEventReference
	RequestAuditID   string
	ResponseID       string
	ConversationID   string
}

type ToolCallAuditView struct {
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

type PendingToolCallDiagnostic struct {
	CallID       string
	ToolName     string
	Executor     string
	LatestStatus string
	StatusesSeen []string
}

type CompactSummaryDiagnostic struct {
	EventCount       int
	AutoTriggered    bool
	LatestEventID    string
	LatestStatus     string
	LatestOccurredAt time.Time
}

type RequestAuditDiagnostics struct {
	EventCount            int
	UpstreamExchangeCount int
	ToolCallCount         int
	LatestStatus          string
	HasCancelled          bool
	HasFailed             bool
	HasStreamEvents       bool
	HasCompactEvents      bool
	PendingToolCallCount  int
	PendingToolCalls      []PendingToolCallDiagnostic
	CompactCandidate      bool
	CompactSummary        *CompactSummaryDiagnostic
}

type RequestAuditTrace struct {
	RequestAudit      RequestAuditView
	ExecutionEvents   []ExecutionEventView
	UpstreamExchanges []UpstreamExchangeView
	ToolCalls         []ToolCallView
	Diagnostics       RequestAuditDiagnostics
}

func (s *QueryService) ListRequestAudits(ctx context.Context, params ListRequestAuditsParams) ([]RequestAuditView, error) {
	if s == nil || s.client == nil {
		return nil, nil
	}
	query := s.client.RequestAudit.Query().
		Order(requestaudit.ByCreatedAt(entsql.OrderDesc()), requestaudit.ByID(entsql.OrderDesc())).
		Limit(normalizeAuditQueryLimit(params.Limit))
	if params.ResponseID != "" {
		query.Where(requestaudit.ResponseIDEQ(params.ResponseID))
	}
	if params.RequestAuditID != "" {
		query.Where(requestaudit.IDEQ(params.RequestAuditID))
	}
	if params.ClientRequestID != "" {
		query.Where(requestaudit.ClientRequestIDEQ(params.ClientRequestID))
	}
	if params.ConversationID != "" {
		query.Where(requestaudit.ConversationIDEQ(params.ConversationID))
	}
	if params.Status != "" {
		query.Where(requestaudit.StatusEQ(params.Status))
	}
	if params.Operation != "" {
		query.Where(requestAuditOperationPredicate(params.Operation))
	}
	records, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RequestAuditView, 0, len(records))
	for _, record := range records {
		out = append(out, requestAuditView(record))
	}
	return out, nil
}

func (s *QueryService) ListToolCallAudits(ctx context.Context, params ListToolCallAuditsParams) ([]ToolCallAuditView, error) {
	if s == nil || s.client == nil {
		return nil, nil
	}
	query := s.client.ToolCallAudit.Query().
		Order(toolcallaudit.ByCreatedAt(), toolcallaudit.ByID()).
		Limit(normalizeAuditQueryLimit(params.Limit))
	if params.ResponseID != "" {
		query.Where(toolcallaudit.ResponseIDEQ(params.ResponseID))
	}
	if params.RequestAuditID != "" {
		query.Where(toolcallaudit.RequestAuditIDEQ(params.RequestAuditID))
	}
	if params.ConversationID != "" {
		query.Where(toolcallaudit.ConversationIDEQ(params.ConversationID))
	}
	if params.CallID != "" {
		query.Where(toolcallaudit.CallIDEQ(params.CallID))
	}
	if params.ToolName != "" {
		query.Where(toolcallaudit.ToolNameEQ(params.ToolName))
	}
	if params.Status != "" {
		query.Where(toolcallaudit.StatusEQ(params.Status))
	}
	records, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ToolCallAuditView, 0, len(records))
	for _, record := range records {
		out = append(out, toolCallAuditView(record))
	}
	return out, nil
}

func (s *QueryService) GetRequestAuditTrace(ctx context.Context, params GetRequestAuditTraceParams) (RequestAuditTrace, bool, error) {
	var trace RequestAuditTrace
	if s == nil || s.client == nil || (params.ResponseID == "" && params.RequestAuditID == "" && params.ClientRequestID == "" && params.ConversationID == "") {
		return trace, false, nil
	}
	audits, err := s.ListRequestAudits(ctx, ListRequestAuditsParams{
		ResponseID:      params.ResponseID,
		RequestAuditID:  params.RequestAuditID,
		ClientRequestID: params.ClientRequestID,
		ConversationID:  params.ConversationID,
		Limit:           1,
	})
	if err != nil {
		return trace, false, err
	}
	if len(audits) == 0 {
		return trace, false, nil
	}
	trace.RequestAudit = audits[0]
	events, err := s.listExecutionEvents(ctx, trace.RequestAudit.ID, trace.RequestAudit.ResponseID, params.EventLimit)
	if err != nil {
		return trace, false, err
	}
	exchanges, err := s.listUpstreamExchanges(ctx, trace.RequestAudit.ID, trace.RequestAudit.ResponseID, params.UpstreamExchangeLimit)
	if err != nil {
		return trace, false, err
	}
	trace.ExecutionEvents = events
	trace.UpstreamExchanges = exchanges
	trace.ToolCalls = deriveToolCalls(events)
	trace.Diagnostics = DeriveRequestAuditDiagnostics(trace.RequestAudit, trace.ExecutionEvents, trace.UpstreamExchanges, trace.ToolCalls)
	return trace, true, nil
}

func DeriveRequestAuditDiagnostics(audit RequestAuditView, events []ExecutionEventView, exchanges []UpstreamExchangeView, toolCalls []ToolCallView) RequestAuditDiagnostics {
	diagnostics := RequestAuditDiagnostics{
		EventCount:            len(events),
		UpstreamExchangeCount: len(exchanges),
		ToolCallCount:         len(toolCalls),
		LatestStatus:          audit.Status,
		PendingToolCalls:      []PendingToolCallDiagnostic{},
	}
	if statusIsCancelled(audit.Status) {
		diagnostics.HasCancelled = true
	}
	if statusIsFailed(audit.Status) || audit.ErrorText != "" {
		diagnostics.HasFailed = true
	}
	for _, exchange := range exchanges {
		if exchange.ErrorText != "" || exchange.StatusCode >= 400 {
			diagnostics.HasFailed = true
		}
	}
	var compactSummary *CompactSummaryDiagnostic
	for _, event := range events {
		if event.Status != "" && audit.Status == "" {
			diagnostics.LatestStatus = event.Status
		}
		if statusIsCancelled(event.Status) || eventContains(event, "cancel") {
			diagnostics.HasCancelled = true
		}
		if statusIsFailed(event.Status) || event.Message != "" && statusIsFailed(event.Message) {
			diagnostics.HasFailed = true
		}
		if isStreamEvent(event) {
			diagnostics.HasStreamEvents = true
		}
		if isCompactEvent(event) {
			diagnostics.HasCompactEvents = true
			diagnostics.CompactCandidate = true
			if compactSummary == nil {
				compactSummary = &CompactSummaryDiagnostic{}
			}
			compactSummary.EventCount++
			compactSummary.LatestEventID = event.ID
			compactSummary.LatestStatus = event.Status
			compactSummary.LatestOccurredAt = event.OccurredAt
			if eventDetailBool(event.DetailsJSON, "auto_triggered") || eventContains(event, "auto_triggered") {
				compactSummary.AutoTriggered = true
			}
		}
	}
	for _, toolCall := range toolCalls {
		if isPendingToolCall(toolCall) {
			diagnostics.PendingToolCalls = append(diagnostics.PendingToolCalls, PendingToolCallDiagnostic{
				CallID:       toolCall.CallID,
				ToolName:     toolCall.ToolName,
				Executor:     toolCall.Executor,
				LatestStatus: toolCall.LatestStatus,
				StatusesSeen: append([]string(nil), toolCall.StatusesSeen...),
			})
		}
	}
	diagnostics.PendingToolCallCount = len(diagnostics.PendingToolCalls)
	diagnostics.CompactSummary = compactSummary
	return diagnostics
}

func (s *QueryService) FindRequestAuditReferenceByTraceID(ctx context.Context, traceID string) (RequestAuditReference, bool, error) {
	var ref RequestAuditReference
	if s == nil || s.client == nil || traceID == "" {
		return ref, false, nil
	}
	exchange, err := s.client.UpstreamExchange.Query().
		Where(upstreamexchange.TraceIDEQ(traceID)).
		Order(upstreamexchange.ByStartedAt(entsql.OrderDesc()), upstreamexchange.ByCompletedAt(entsql.OrderDesc()), upstreamexchange.ByID(entsql.OrderDesc())).
		First(ctx)
	if err != nil {
		if dao.IsNotFound(err) {
			return ref, false, nil
		}
		return ref, false, err
	}
	ref.ResponseID = exchange.ResponseID
	ref.RequestAuditID = exchange.RequestAuditID
	if ref.ResponseID == "" && ref.RequestAuditID != "" {
		audit, err := s.client.RequestAudit.Get(ctx, ref.RequestAuditID)
		if err != nil {
			if dao.IsNotFound(err) {
				return ref, true, nil
			}
			return ref, false, err
		}
		ref.ResponseID = audit.ResponseID
	}
	if ref.RequestAuditID == "" && ref.ResponseID != "" {
		audit, err := s.client.RequestAudit.Query().
			Where(requestaudit.ResponseIDEQ(ref.ResponseID)).
			Order(requestaudit.ByCreatedAt(entsql.OrderDesc()), requestaudit.ByID(entsql.OrderDesc())).
			First(ctx)
		if err != nil {
			if dao.IsNotFound(err) {
				return ref, true, nil
			}
			return ref, false, err
		}
		ref.RequestAuditID = audit.ID
	}
	return ref, ref.ResponseID != "" || ref.RequestAuditID != "", nil
}

func (s *QueryService) listExecutionEvents(ctx context.Context, requestAuditID, responseID string, limit int) ([]ExecutionEventView, error) {
	if requestAuditID == "" && responseID == "" {
		return nil, nil
	}
	query := s.client.ExecutionEvent.Query().
		Order(executionevent.ByOccurredAt(), executionevent.ByID()).
		Limit(normalizeAuditQueryLimit(limit))
	switch {
	case requestAuditID != "" && responseID != "":
		query.Where(executionevent.Or(
			executionevent.RequestAuditIDEQ(requestAuditID),
			executionevent.ResponseIDEQ(responseID),
		))
	case requestAuditID != "":
		query.Where(executionevent.RequestAuditIDEQ(requestAuditID))
	case responseID != "":
		query.Where(executionevent.ResponseIDEQ(responseID))
	}
	records, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExecutionEventView, 0, len(records))
	for _, record := range records {
		out = append(out, executionEventView(record))
	}
	return out, nil
}

func (s *QueryService) listUpstreamExchanges(ctx context.Context, requestAuditID, responseID string, limit int) ([]UpstreamExchangeView, error) {
	if requestAuditID == "" && responseID == "" {
		return nil, nil
	}
	query := s.client.UpstreamExchange.Query().
		Order(upstreamexchange.ByStartedAt(), upstreamexchange.ByCompletedAt(), upstreamexchange.ByID()).
		Limit(normalizeAuditQueryLimit(limit))
	switch {
	case requestAuditID != "" && responseID != "":
		query.Where(upstreamexchange.Or(
			upstreamexchange.RequestAuditIDEQ(requestAuditID),
			upstreamexchange.ResponseIDEQ(responseID),
		))
	case requestAuditID != "":
		query.Where(upstreamexchange.RequestAuditIDEQ(requestAuditID))
	case responseID != "":
		query.Where(upstreamexchange.ResponseIDEQ(responseID))
	}
	records, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UpstreamExchangeView, 0, len(records))
	for _, record := range records {
		out = append(out, upstreamExchangeView(record))
	}
	return out, nil
}

func normalizeAuditQueryLimit(limit int) int {
	return NormalizeAuditQueryLimit(limit)
}

func NormalizeAuditQueryLimit(limit int) int {
	if limit <= 0 {
		return DefaultAuditQueryLimit
	}
	if limit > MaxAuditQueryLimit {
		return MaxAuditQueryLimit
	}
	return limit
}

func RequestAuditStatusValues() []string {
	return append([]string(nil), requestAuditStatusValues...)
}

func RequestAuditOperationValues() []string {
	return append([]string(nil), requestAuditOperationValues...)
}

func NormalizeRequestAuditStatus(status string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(status))
	if normalized == "" {
		return "", true
	}
	for _, allowed := range requestAuditStatusValues {
		if normalized == allowed {
			return normalized, true
		}
	}
	return "", false
}

func NormalizeRequestAuditOperation(operation string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(operation))
	if normalized == "" {
		return "", true
	}
	normalized = strings.ReplaceAll(normalized, "-", "_")
	for _, allowed := range requestAuditOperationValues {
		if normalized == allowed {
			return normalized, true
		}
	}
	return "", false
}

func RequestAuditOperation(method, path string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	switch {
	case method == "POST" && path == "/v1/responses":
		return "create"
	case method == "POST" && path == "/v1/responses/compact":
		return "compact"
	case method == "GET" && strings.HasPrefix(path, "/v1/responses/") && strings.HasSuffix(path, "/input_items"):
		return "input_items"
	default:
		return ""
	}
}

func requestAuditOperationPredicate(operation string) predicate.RequestAudit {
	switch operation {
	case "create":
		return requestaudit.And(requestaudit.MethodEQ("POST"), requestaudit.PathEQ("/v1/responses"))
	case "compact":
		return requestaudit.And(requestaudit.MethodEQ("POST"), requestaudit.PathEQ("/v1/responses/compact"))
	case "input_items":
		return requestaudit.And(requestaudit.MethodEQ("GET"), requestaudit.PathHasPrefix("/v1/responses/"), requestaudit.PathHasSuffix("/input_items"))
	default:
		return requestaudit.IDEQ("__llm_tracelab_no_such_request_audit_operation__")
	}
}

func requestAuditView(record *dao.RequestAudit) RequestAuditView {
	if record == nil {
		return RequestAuditView{}
	}
	return RequestAuditView{
		ID:              record.ID,
		ResponseID:      record.ResponseID,
		ConversationID:  record.ConversationID,
		Method:          record.Method,
		Path:            record.Path,
		Operation:       RequestAuditOperation(record.Method, record.Path),
		ClientRequestID: record.ClientRequestID,
		HeaderJSON:      cloneMap(record.HeaderJSON),
		BodyPreview:     record.BodyPreview,
		BodySha256:      record.BodySha256,
		RedactionJSON:   cloneMap(record.RedactionJSON),
		Status:          record.Status,
		ErrorText:       record.ErrorText,
		CreatedAt:       record.CreatedAt,
	}
}

func executionEventView(record *dao.ExecutionEvent) ExecutionEventView {
	if record == nil {
		return ExecutionEventView{}
	}
	return ExecutionEventView{
		ID:             record.ID,
		ResponseID:     record.ResponseID,
		RequestAuditID: record.RequestAuditID,
		ConversationID: record.ConversationID,
		EventType:      record.EventType,
		Phase:          record.Phase,
		Status:         record.Status,
		Message:        record.Message,
		DetailsJSON:    cloneMap(record.DetailsJSON),
		OccurredAt:     record.OccurredAt,
	}
}

func upstreamExchangeView(record *dao.UpstreamExchange) UpstreamExchangeView {
	if record == nil {
		return UpstreamExchangeView{}
	}
	return UpstreamExchangeView{
		ID:             record.ID,
		ResponseID:     record.ResponseID,
		RequestAuditID: record.RequestAuditID,
		TraceID:        record.TraceID,
		CassettePath:   record.CassettePath,
		UpstreamID:     record.UpstreamID,
		RouteTarget:    record.RouteTarget,
		Model:          record.Model,
		Endpoint:       record.Endpoint,
		StatusCode:     record.StatusCode,
		StartedAt:      record.StartedAt,
		CompletedAt:    record.CompletedAt,
		ErrorText:      record.ErrorText,
	}
}

func deriveToolCalls(events []ExecutionEventView) []ToolCallView {
	if len(events) == 0 {
		return nil
	}
	indexByKey := make(map[string]int)
	statusSeen := make([]map[string]struct{}, 0)
	out := make([]ToolCallView, 0)
	for _, event := range events {
		if event.EventType != "response.tool_call" {
			continue
		}
		callID := toolDetailString(event.DetailsJSON, "call_id", "tool_call_id")
		key := callID
		if key == "" {
			key = "event:" + event.ID
		}
		idx, ok := indexByKey[key]
		if !ok {
			indexByKey[key] = len(out)
			statusSeen = append(statusSeen, map[string]struct{}{})
			out = append(out, ToolCallView{
				CallID:         callID,
				ToolName:       toolDetailString(event.DetailsJSON, "tool_name", "name"),
				Executor:       toolDetailString(event.DetailsJSON, "executor"),
				RequestAuditID: event.RequestAuditID,
				ResponseID:     event.ResponseID,
				ConversationID: event.ConversationID,
			})
			idx = len(out) - 1
		}
		call := &out[idx]
		if call.ToolName == "" {
			call.ToolName = toolDetailString(event.DetailsJSON, "tool_name", "name")
		}
		if call.Executor == "" {
			call.Executor = toolDetailString(event.DetailsJSON, "executor")
		}
		if call.RequestAuditID == "" {
			call.RequestAuditID = event.RequestAuditID
		}
		if call.ResponseID == "" {
			call.ResponseID = event.ResponseID
		}
		if call.ConversationID == "" {
			call.ConversationID = event.ConversationID
		}
		if event.Status != "" {
			if _, exists := statusSeen[idx][event.Status]; !exists {
				statusSeen[idx][event.Status] = struct{}{}
				call.StatusesSeen = append(call.StatusesSeen, event.Status)
			}
			call.LatestStatus = event.Status
		}
		if event.Status == "started" && call.StartedAt.IsZero() {
			call.StartedAt = event.OccurredAt
		}
		if event.Status == "completed" || event.Status == "failed" {
			call.CompletedAt = event.OccurredAt
		}
		if summary := summarizeSensitiveToolDetail(event.DetailsJSON["arguments"]); summary != "" {
			call.ArgumentsSummary = summary
		}
		if summary := summarizeSensitiveToolDetail(event.DetailsJSON["query"]); summary != "" {
			call.QuerySummary = summary
		}
		if summary := toolOutputSummary(event.DetailsJSON); summary != "" {
			call.OutputSummary = summary
		}
		if event.Status == "failed" {
			call.ErrorText = summarizeToolError(event)
		}
		call.EventCount++
		call.Events = append(call.Events, ToolCallEventReference{
			ID:         event.ID,
			Status:     event.Status,
			Message:    summarizeToolEventMessage(event.Message),
			OccurredAt: event.OccurredAt,
		})
	}
	return out
}

func toolDetailString(details map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := details[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		default:
			return fmt.Sprint(typed)
		}
	}
	return ""
}

func summarizeSensitiveToolDetail(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return redactedCharsSummary(typed)
	case []byte:
		return fmt.Sprintf("<redacted; bytes=%d>", len(typed))
	default:
		return fmt.Sprintf("<redacted; type=%T>", value)
	}
}

func summarizeToolError(event ExecutionEventView) string {
	if summary := summarizeSensitiveToolDetail(event.DetailsJSON["error"]); summary != "" {
		return summary
	}
	return summarizeToolEventMessage(event.Message)
}

func summarizeToolEventMessage(message string) string {
	if message == "" {
		return ""
	}
	return redactedCharsSummary(message)
}

func redactedCharsSummary(value string) string {
	return fmt.Sprintf("<redacted; chars=%d>", utf8.RuneCountInString(value))
}

func toolOutputSummary(details map[string]any) string {
	if value, ok := details["output_chars"]; ok {
		if n, ok := toolInt(value); ok {
			return "chars=" + strconv.Itoa(n)
		}
	}
	if value, ok := details["result_bytes"]; ok {
		if n, ok := toolInt(value); ok {
			return "bytes=" + strconv.Itoa(n)
		}
	}
	if value, ok := details["result_count"]; ok {
		if n, ok := toolInt(value); ok {
			return "results=" + strconv.Itoa(n)
		}
	}
	return ""
}

func toolInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		n, err := strconv.Atoi(string(typed))
		return n, err == nil
	default:
		return 0, false
	}
}

func statusIsCancelled(status string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(status)), "cancel")
}

func statusIsFailed(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "failed", "failure", "error", "errored", "rejected":
		return true
	default:
		return strings.Contains(normalized, "failed")
	}
}

func isStreamEvent(event ExecutionEventView) bool {
	return eventContains(event, "stream") || eventDetailBool(event.DetailsJSON, "stream")
}

func isCompactEvent(event ExecutionEventView) bool {
	return eventContains(event, "compact") || eventDetailBool(event.DetailsJSON, "auto_triggered")
}

func eventContains(event ExecutionEventView, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	if strings.Contains(strings.ToLower(event.EventType), needle) ||
		strings.Contains(strings.ToLower(event.Phase), needle) ||
		strings.Contains(strings.ToLower(event.Status), needle) ||
		strings.Contains(strings.ToLower(event.Message), needle) {
		return true
	}
	for key, value := range event.DetailsJSON {
		if strings.Contains(strings.ToLower(key), needle) {
			return true
		}
		if text, ok := value.(string); ok && strings.Contains(strings.ToLower(text), needle) {
			return true
		}
	}
	return false
}

func eventDetailBool(details map[string]any, keys ...string) bool {
	if len(details) == 0 {
		return false
	}
	for _, key := range keys {
		value, ok := details[key]
		if !ok {
			continue
		}
		if typed, ok := value.(bool); ok && typed {
			return true
		}
	}
	return false
}

func isPendingToolCall(call ToolCallView) bool {
	seenActive := false
	for _, status := range call.StatusesSeen {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "completed", "failed", "submitted":
			return false
		case "started", "requested":
			seenActive = true
		}
	}
	if !seenActive {
		switch strings.ToLower(strings.TrimSpace(call.LatestStatus)) {
		case "started", "requested":
			seenActive = true
		}
	}
	return seenActive
}

func toolCallAuditView(record *dao.ToolCallAudit) ToolCallAuditView {
	if record == nil {
		return ToolCallAuditView{}
	}
	return ToolCallAuditView{
		ID:             record.ID,
		ResponseID:     record.ResponseID,
		RequestAuditID: record.RequestAuditID,
		ConversationID: record.ConversationID,
		CallID:         record.CallID,
		ToolType:       record.ToolType,
		ToolName:       record.ToolName,
		Executor:       record.Executor,
		Status:         record.Status,
		Phase:          record.Phase,
		InputJSON:      cloneMap(record.InputJSON),
		OutputJSON:     cloneMap(record.OutputJSON),
		ErrorText:      record.ErrorText,
		MetadataJSON:   cloneMap(record.MetadataJSON),
		StartedAt:      record.StartedAt,
		CompletedAt:    record.CompletedAt,
		CreatedAt:      record.CreatedAt,
	}
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
