package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	responsesaudit "github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type responsesAuditTraceOutput struct {
	Query             responsesAuditTraceQuery      `json:"query"`
	RequestAudit      *responsesRequestAuditView    `json:"request_audit,omitempty"`
	FinalResponse     *responsesFinalResponseView   `json:"final_response,omitempty"`
	Events            []responsesExecutionEventView `json:"events"`
	EntryExchange     *responsesExchangeView        `json:"entry_exchange"`
	ModelExchanges    []responsesExchangeView       `json:"model_exchanges"`
	UpstreamExchanges []responsesExchangeView       `json:"upstream_exchanges"`
	RawCassettes      []responsesRawCassetteView    `json:"raw_cassettes"`
}

type responsesAuditToolCallsOutput struct {
	Query           responsesAuditToolCallsQuery `json:"query"`
	Items           []responsesToolCallAuditView `json:"items"`
	Total           int                          `json:"total"`
	IncludePayloads bool                         `json:"include_payloads"`
}

type responsesAuditTraceQuery struct {
	ResponseID     string `json:"response_id,omitempty"`
	RequestAuditID string `json:"request_audit_id,omitempty"`
}

type responsesAuditToolCallsQuery struct {
	ResponseID     string `json:"response_id,omitempty"`
	RequestAuditID string `json:"request_audit_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	Status         string `json:"status,omitempty"`
	Limit          int    `json:"limit"`
}

type responsesRequestAuditView struct {
	ID              string         `json:"id"`
	ResponseID      string         `json:"response_id,omitempty"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	Method          string         `json:"method"`
	Path            string         `json:"path"`
	ClientRequestID string         `json:"client_request_id,omitempty"`
	HeaderJSON      map[string]any `json:"header_json,omitempty"`
	BodyPreview     string         `json:"body_preview,omitempty"`
	BodySHA256      string         `json:"body_sha256,omitempty"`
	Status          string         `json:"status,omitempty"`
	ErrorText       string         `json:"error_text,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type responsesExecutionEventView struct {
	ID             string         `json:"id"`
	ResponseID     string         `json:"response_id,omitempty"`
	RequestAuditID string         `json:"request_audit_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	EventType      string         `json:"event_type"`
	Phase          string         `json:"phase,omitempty"`
	Status         string         `json:"status,omitempty"`
	Message        string         `json:"message,omitempty"`
	DetailsJSON    map[string]any `json:"details_json,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
}

type responsesExchangeView struct {
	ID               string    `json:"id,omitempty"`
	ResponseID       string    `json:"response_id,omitempty"`
	RequestAuditID   string    `json:"request_audit_id,omitempty"`
	ExchangeID       string    `json:"exchange_id,omitempty"`
	ExchangeKind     string    `json:"exchange_kind"`
	ExchangeRole     string    `json:"exchange_role"`
	ParentExchangeID string    `json:"parent_exchange_id,omitempty"`
	SequenceIndex    int       `json:"sequence_index"`
	TraceID          string    `json:"trace_id,omitempty"`
	CassettePath     string    `json:"cassette_path,omitempty"`
	UpstreamID       string    `json:"upstream_id,omitempty"`
	RouteTarget      string    `json:"route_target,omitempty"`
	Model            string    `json:"model,omitempty"`
	Endpoint         string    `json:"endpoint,omitempty"`
	StatusCode       int       `json:"status_code,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	ErrorText        string    `json:"error_text,omitempty"`
}

type responsesFinalResponseView struct {
	ResponseID      string    `json:"response_id,omitempty"`
	RequestAuditID  string    `json:"request_audit_id,omitempty"`
	ConversationID  string    `json:"conversation_id,omitempty"`
	ClientRequestID string    `json:"client_request_id,omitempty"`
	Status          string    `json:"status,omitempty"`
	ErrorText       string    `json:"error_text,omitempty"`
	Model           string    `json:"model,omitempty"`
	Endpoint        string    `json:"endpoint,omitempty"`
	StatusCode      int       `json:"status_code,omitempty"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
}

type responsesRawCassetteView struct {
	ExchangeID       string                         `json:"exchange_id,omitempty"`
	ExchangeKind     string                         `json:"exchange_kind,omitempty"`
	ExchangeRole     string                         `json:"exchange_role,omitempty"`
	ParentExchangeID string                         `json:"parent_exchange_id,omitempty"`
	SequenceIndex    *int                           `json:"sequence_index,omitempty"`
	TraceID          string                         `json:"trace_id,omitempty"`
	CassettePath     string                         `json:"cassette_path,omitempty"`
	ReadError        string                         `json:"read_error,omitempty"`
	Header           recordfile.RecordHeader        `json:"header,omitempty"`
	Events           []recordfile.RecordEvent       `json:"events,omitempty"`
	Request          recordfile.HTTPRequestSummary  `json:"request,omitempty"`
	Response         recordfile.HTTPResponseSummary `json:"response,omitempty"`
}

type responsesToolCallAuditView struct {
	ID              string                        `json:"id"`
	ResponseID      string                        `json:"response_id,omitempty"`
	RequestAuditID  string                        `json:"request_audit_id,omitempty"`
	ConversationID  string                        `json:"conversation_id,omitempty"`
	CallID          string                        `json:"call_id"`
	ToolType        string                        `json:"tool_type,omitempty"`
	ToolName        string                        `json:"tool_name,omitempty"`
	Executor        string                        `json:"executor,omitempty"`
	Status          string                        `json:"status,omitempty"`
	Phase           string                        `json:"phase,omitempty"`
	InputSummary    responsesaudit.PayloadSummary `json:"input_summary"`
	OutputSummary   responsesaudit.PayloadSummary `json:"output_summary"`
	MetadataSummary responsesaudit.PayloadSummary `json:"metadata_summary"`
	InputJSON       map[string]any                `json:"input_json,omitempty"`
	OutputJSON      map[string]any                `json:"output_json,omitempty"`
	MetadataJSON    map[string]any                `json:"metadata_json,omitempty"`
	ErrorText       string                        `json:"error_text,omitempty"`
	StartedAt       time.Time                     `json:"started_at,omitempty"`
	CompletedAt     time.Time                     `json:"completed_at,omitempty"`
	CreatedAt       time.Time                     `json:"created_at"`
}

func (a *serverAPI) responsesAuditTrace(ctx context.Context, req *mcp.CallToolRequest, in *responsesAuditTraceInput) (*mcp.CallToolResult, *responsesAuditTraceOutput, error) {
	responseID := strings.TrimSpace(in.ResponseID)
	requestAuditID := strings.TrimSpace(in.RequestAuditID)
	if responseID == "" && requestAuditID == "" {
		return nil, nil, fmt.Errorf("response_id or request_audit_id is required")
	}
	if a.store == nil || a.store.EntClient() == nil {
		return nil, nil, fmt.Errorf("store is not available")
	}

	trace, found, err := responsesaudit.NewQueryService(a.store.EntClient()).GetRequestAuditTrace(ctx, responsesaudit.GetRequestAuditTraceParams{
		ResponseID:     responseID,
		RequestAuditID: requestAuditID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("query responses audit trace: %w", err)
	}
	out := &responsesAuditTraceOutput{
		Query: responsesAuditTraceQuery{
			ResponseID:     responseID,
			RequestAuditID: requestAuditID,
		},
		Events:            []responsesExecutionEventView{},
		ModelExchanges:    []responsesExchangeView{},
		UpstreamExchanges: []responsesExchangeView{},
		RawCassettes:      []responsesRawCassetteView{},
	}
	if !found {
		return nil, out, nil
	}

	out.RequestAudit = responsesRequestAuditFromAudit(trace.RequestAudit)
	out.FinalResponse = responsesFinalResponseFromAudit(trace.FinalResponse)
	out.Query.ResponseID = trace.RequestAudit.ResponseID
	out.Query.RequestAuditID = trace.RequestAudit.ID
	for _, event := range trace.ExecutionEvents {
		out.Events = append(out.Events, responsesExecutionEventFromAudit(event))
	}
	for _, cassette := range trace.RawCassettes {
		out.RawCassettes = append(out.RawCassettes, responsesRawCassetteFromAudit(cassette))
	}
	for _, exchange := range trace.UpstreamExchanges {
		out.ModelExchanges = append(out.ModelExchanges, responsesModelExchangeFromAudit(exchange, out.RawCassettes, trace.RequestAudit.ID))
	}
	out.UpstreamExchanges = out.ModelExchanges
	out.EntryExchange = responsesEntryExchangeFromAudit(trace.RequestAudit, trace.FinalResponse)
	applyRawCassetteExchangeFallbacks(out.RawCassettes, out.ModelExchanges)
	return nil, out, nil
}

func (a *serverAPI) responsesAuditToolCalls(ctx context.Context, req *mcp.CallToolRequest, in *responsesAuditToolCallsInput) (*mcp.CallToolResult, *responsesAuditToolCallsOutput, error) {
	if a.store == nil || a.store.EntClient() == nil {
		return nil, nil, fmt.Errorf("store is not available")
	}
	params := responsesaudit.ListToolCallAuditsParams{
		ResponseID:     strings.TrimSpace(in.ResponseID),
		RequestAuditID: strings.TrimSpace(in.RequestAuditID),
		ConversationID: strings.TrimSpace(in.ConversationID),
		CallID:         strings.TrimSpace(in.CallID),
		ToolName:       strings.TrimSpace(in.ToolName),
		Status:         strings.TrimSpace(in.Status),
		Limit:          in.Limit,
	}
	records, err := responsesaudit.NewQueryService(a.store.EntClient()).ListToolCallAudits(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("query responses tool call audits: %w", err)
	}
	items := make([]responsesToolCallAuditView, 0, len(records))
	for _, record := range records {
		items = append(items, responsesToolCallAuditFromAudit(record, in.IncludePayloads))
	}
	out := &responsesAuditToolCallsOutput{
		Query: responsesAuditToolCallsQuery{
			ResponseID:     params.ResponseID,
			RequestAuditID: params.RequestAuditID,
			ConversationID: params.ConversationID,
			CallID:         params.CallID,
			ToolName:       params.ToolName,
			Status:         params.Status,
			Limit:          responsesaudit.NormalizeAuditQueryLimit(params.Limit),
		},
		Items:           items,
		Total:           len(items),
		IncludePayloads: in.IncludePayloads,
	}
	return nil, out, nil
}

func responsesRequestAuditFromAudit(audit responsesaudit.RequestAuditView) *responsesRequestAuditView {
	return &responsesRequestAuditView{
		ID:              audit.ID,
		ResponseID:      audit.ResponseID,
		ConversationID:  audit.ConversationID,
		Method:          audit.Method,
		Path:            audit.Path,
		ClientRequestID: audit.ClientRequestID,
		HeaderJSON:      audit.HeaderJSON,
		BodyPreview:     audit.BodyPreview,
		BodySHA256:      audit.BodySha256,
		Status:          audit.Status,
		ErrorText:       audit.ErrorText,
		CreatedAt:       audit.CreatedAt,
	}
}

func responsesToolCallAuditFromAudit(record responsesaudit.ToolCallAuditView, includePayloads bool) responsesToolCallAuditView {
	out := responsesToolCallAuditView{
		ID:              record.ID,
		ResponseID:      record.ResponseID,
		RequestAuditID:  record.RequestAuditID,
		ConversationID:  record.ConversationID,
		CallID:          record.CallID,
		ToolType:        record.ToolType,
		ToolName:        record.ToolName,
		Executor:        record.Executor,
		Status:          record.Status,
		Phase:           record.Phase,
		InputSummary:    responsesaudit.SummarizeJSONPayload(record.InputJSON),
		OutputSummary:   responsesaudit.SummarizeJSONPayload(record.OutputJSON),
		MetadataSummary: responsesaudit.SummarizeJSONPayload(record.MetadataJSON),
		ErrorText:       record.ErrorText,
		StartedAt:       record.StartedAt,
		CompletedAt:     record.CompletedAt,
		CreatedAt:       record.CreatedAt,
	}
	if includePayloads {
		out.InputJSON = record.InputJSON
		out.OutputJSON = record.OutputJSON
		out.MetadataJSON = record.MetadataJSON
	}
	return out
}

func responsesExecutionEventFromAudit(event responsesaudit.ExecutionEventView) responsesExecutionEventView {
	return responsesExecutionEventView{
		ID:             event.ID,
		ResponseID:     event.ResponseID,
		RequestAuditID: event.RequestAuditID,
		ConversationID: event.ConversationID,
		EventType:      event.EventType,
		Phase:          event.Phase,
		Status:         event.Status,
		Message:        event.Message,
		DetailsJSON:    event.DetailsJSON,
		OccurredAt:     event.OccurredAt,
	}
}

func responsesModelExchangeFromAudit(exchange responsesaudit.UpstreamExchangeView, cassettes []responsesRawCassetteView, requestAuditID string) responsesExchangeView {
	header := findRawCassetteHeader(cassettes, exchange)
	out := responsesExchangeView{
		ID:               exchange.ID,
		ResponseID:       exchange.ResponseID,
		RequestAuditID:   exchange.RequestAuditID,
		ExchangeID:       firstNonEmpty(header.Meta.ExchangeID, exchange.ID),
		ExchangeKind:     firstNonEmpty(header.Meta.ExchangeKind, "model"),
		ExchangeRole:     firstNonEmpty(header.Meta.ExchangeRole, "primary_model_call"),
		ParentExchangeID: firstNonEmpty(header.Meta.ParentExchangeID, entryExchangeID(requestAuditID)),
		SequenceIndex:    header.Meta.SequenceIndex,
		TraceID:          firstNonEmpty(exchange.TraceID, header.Meta.TraceID, header.Meta.RequestID),
		CassettePath:     exchange.CassettePath,
		UpstreamID:       exchange.UpstreamID,
		RouteTarget:      exchange.RouteTarget,
		Model:            firstNonEmpty(exchange.Model, header.Meta.Model),
		Endpoint:         firstNonEmpty(exchange.Endpoint, header.Meta.Endpoint),
		StatusCode:       exchange.StatusCode,
		StartedAt:        firstNonZeroTime(exchange.StartedAt, header.Meta.Time),
		CompletedAt:      exchange.CompletedAt,
		ErrorText:        exchange.ErrorText,
	}
	if out.RequestAuditID == "" {
		out.RequestAuditID = header.Meta.RequestAuditID
	}
	if out.ResponseID == "" {
		out.ResponseID = header.Meta.ResponseID
	}
	if out.StatusCode == 0 {
		out.StatusCode = header.Meta.StatusCode
	}
	return out
}

func responsesEntryExchangeFromAudit(request responsesaudit.RequestAuditView, final responsesaudit.FinalResponseView) *responsesExchangeView {
	if request.ID == "" {
		return nil
	}
	return &responsesExchangeView{
		ID:             entryExchangeID(request.ID),
		ResponseID:     firstNonEmpty(request.ResponseID, final.ResponseID),
		RequestAuditID: request.ID,
		ExchangeID:     entryExchangeID(request.ID),
		ExchangeKind:   "entry",
		ExchangeRole:   "client_request",
		SequenceIndex:  0,
		Model:          final.Model,
		Endpoint:       firstNonEmpty(final.Endpoint, request.Path),
		StatusCode:     final.StatusCode,
		StartedAt:      request.CreatedAt,
		CompletedAt:    final.CompletedAt,
	}
}

func responsesFinalResponseFromAudit(response responsesaudit.FinalResponseView) *responsesFinalResponseView {
	if response.ResponseID == "" && response.RequestAuditID == "" && response.Status == "" {
		return nil
	}
	return &responsesFinalResponseView{
		ResponseID:      response.ResponseID,
		RequestAuditID:  response.RequestAuditID,
		ConversationID:  response.ConversationID,
		ClientRequestID: response.ClientRequestID,
		Status:          response.Status,
		ErrorText:       response.ErrorText,
		Model:           response.Model,
		Endpoint:        response.Endpoint,
		StatusCode:      response.StatusCode,
		CompletedAt:     response.CompletedAt,
	}
}

func responsesRawCassetteFromAudit(cassette responsesaudit.RawCassetteView) responsesRawCassetteView {
	out := responsesRawCassetteView{
		ExchangeID:       firstNonEmpty(cassette.Header.Meta.ExchangeID, cassette.ExchangeID),
		ExchangeKind:     cassette.Header.Meta.ExchangeKind,
		ExchangeRole:     cassette.Header.Meta.ExchangeRole,
		ParentExchangeID: cassette.Header.Meta.ParentExchangeID,
		TraceID:          firstNonEmpty(cassette.TraceID, cassette.Header.Meta.TraceID, cassette.Header.Meta.RequestID),
		CassettePath:     cassette.CassettePath,
		ReadError:        cassette.ReadError,
		Header:           cassette.Header,
		Events:           append([]recordfile.RecordEvent(nil), cassette.Events...),
		Request:          cassette.Request,
		Response:         cassette.Response,
	}
	if cassette.Header.Meta.SequenceIndex != 0 {
		out.SequenceIndex = intPtr(cassette.Header.Meta.SequenceIndex)
	}
	return out
}

func applyRawCassetteExchangeFallbacks(cassettes []responsesRawCassetteView, exchanges []responsesExchangeView) {
	for i := range cassettes {
		exchange, ok := findModelExchangeForCassette(exchanges, cassettes[i])
		if !ok {
			continue
		}
		if cassettes[i].ExchangeID == "" {
			cassettes[i].ExchangeID = firstNonEmpty(exchange.ExchangeID, exchange.ID)
		}
		if cassettes[i].ExchangeKind == "" {
			cassettes[i].ExchangeKind = exchange.ExchangeKind
		}
		if cassettes[i].ExchangeRole == "" {
			cassettes[i].ExchangeRole = exchange.ExchangeRole
		}
		if cassettes[i].ParentExchangeID == "" {
			cassettes[i].ParentExchangeID = exchange.ParentExchangeID
		}
		if cassettes[i].SequenceIndex == nil {
			cassettes[i].SequenceIndex = intPtr(exchange.SequenceIndex)
		}
	}
}

func findRawCassetteHeader(cassettes []responsesRawCassetteView, exchange responsesaudit.UpstreamExchangeView) recordfile.RecordHeader {
	for _, cassette := range cassettes {
		if cassette.CassettePath != "" && exchange.CassettePath != "" && cassette.CassettePath == exchange.CassettePath {
			return cassette.Header
		}
		if cassette.TraceID != "" && exchange.TraceID != "" && cassette.TraceID == exchange.TraceID {
			return cassette.Header
		}
	}
	return recordfile.RecordHeader{}
}

func findModelExchangeForCassette(exchanges []responsesExchangeView, cassette responsesRawCassetteView) (responsesExchangeView, bool) {
	for _, exchange := range exchanges {
		if cassette.CassettePath != "" && exchange.CassettePath == cassette.CassettePath {
			return exchange, true
		}
		if cassette.TraceID != "" && exchange.TraceID == cassette.TraceID {
			return exchange, true
		}
	}
	return responsesExchangeView{}, false
}

func entryExchangeID(requestAuditID string) string {
	if requestAuditID == "" {
		return ""
	}
	return "entry:" + requestAuditID
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func intPtr(value int) *int {
	return &value
}
