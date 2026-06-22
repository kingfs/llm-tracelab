package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	responsesaudit "github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type responsesAuditTraceOutput struct {
	Query             responsesAuditTraceQuery      `json:"query"`
	RequestAudit      *responsesRequestAuditView    `json:"request_audit,omitempty"`
	Events            []responsesExecutionEventView `json:"events"`
	UpstreamExchanges []responsesUpstreamExchange   `json:"upstream_exchanges"`
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

type responsesUpstreamExchange struct {
	ID             string    `json:"id"`
	ResponseID     string    `json:"response_id,omitempty"`
	RequestAuditID string    `json:"request_audit_id,omitempty"`
	TraceID        string    `json:"trace_id,omitempty"`
	CassettePath   string    `json:"cassette_path,omitempty"`
	UpstreamID     string    `json:"upstream_id,omitempty"`
	RouteTarget    string    `json:"route_target,omitempty"`
	Model          string    `json:"model,omitempty"`
	Endpoint       string    `json:"endpoint,omitempty"`
	StatusCode     int       `json:"status_code,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	ErrorText      string    `json:"error_text,omitempty"`
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
		UpstreamExchanges: []responsesUpstreamExchange{},
	}
	if !found {
		return nil, out, nil
	}

	out.RequestAudit = responsesRequestAuditFromAudit(trace.RequestAudit)
	out.Query.ResponseID = trace.RequestAudit.ResponseID
	out.Query.RequestAuditID = trace.RequestAudit.ID
	for _, event := range trace.ExecutionEvents {
		out.Events = append(out.Events, responsesExecutionEventFromAudit(event))
	}
	for _, exchange := range trace.UpstreamExchanges {
		out.UpstreamExchanges = append(out.UpstreamExchanges, responsesUpstreamExchangeFromAudit(exchange))
	}
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

func responsesUpstreamExchangeFromAudit(exchange responsesaudit.UpstreamExchangeView) responsesUpstreamExchange {
	return responsesUpstreamExchange{
		ID:             exchange.ID,
		ResponseID:     exchange.ResponseID,
		RequestAuditID: exchange.RequestAuditID,
		TraceID:        exchange.TraceID,
		CassettePath:   exchange.CassettePath,
		UpstreamID:     exchange.UpstreamID,
		RouteTarget:    exchange.RouteTarget,
		Model:          exchange.Model,
		Endpoint:       exchange.Endpoint,
		StatusCode:     exchange.StatusCode,
		StartedAt:      exchange.StartedAt,
		CompletedAt:    exchange.CompletedAt,
		ErrorText:      exchange.ErrorText,
	}
}
