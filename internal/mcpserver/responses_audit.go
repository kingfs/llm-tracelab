package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/executionevent"
	"github.com/kingfs/llm-tracelab/ent/dao/requestaudit"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamexchange"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type responsesAuditTraceOutput struct {
	Query             responsesAuditTraceQuery      `json:"query"`
	RequestAudit      *responsesRequestAuditView    `json:"request_audit,omitempty"`
	Events            []responsesExecutionEventView `json:"events"`
	UpstreamExchanges []responsesUpstreamExchange   `json:"upstream_exchanges"`
}

type responsesAuditTraceQuery struct {
	ResponseID     string `json:"response_id,omitempty"`
	RequestAuditID string `json:"request_audit_id,omitempty"`
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

func (a *serverAPI) responsesAuditTrace(ctx context.Context, req *mcp.CallToolRequest, in *responsesAuditTraceInput) (*mcp.CallToolResult, *responsesAuditTraceOutput, error) {
	responseID := strings.TrimSpace(in.ResponseID)
	requestAuditID := strings.TrimSpace(in.RequestAuditID)
	if responseID == "" && requestAuditID == "" {
		return nil, nil, fmt.Errorf("response_id or request_audit_id is required")
	}
	if a.store == nil || a.store.EntClient() == nil {
		return nil, nil, fmt.Errorf("store is not available")
	}

	out := &responsesAuditTraceOutput{
		Query: responsesAuditTraceQuery{
			ResponseID:     responseID,
			RequestAuditID: requestAuditID,
		},
		Events:            []responsesExecutionEventView{},
		UpstreamExchanges: []responsesUpstreamExchange{},
	}

	client := a.store.EntClient()
	audit, err := lookupResponsesRequestAudit(ctx, client, responseID, requestAuditID)
	if err != nil {
		return nil, nil, err
	}
	if audit != nil {
		out.RequestAudit = requestAuditView(audit)
		if responseID == "" {
			responseID = audit.ResponseID
			out.Query.ResponseID = responseID
		}
		if requestAuditID == "" {
			requestAuditID = audit.ID
			out.Query.RequestAuditID = requestAuditID
		}
	}

	exchanges, err := listResponsesUpstreamExchanges(ctx, client, responseID, requestAuditID)
	if err != nil {
		return nil, nil, err
	}
	for _, exchange := range exchanges {
		out.UpstreamExchanges = append(out.UpstreamExchanges, upstreamExchangeView(exchange))
	}

	if responseID != "" {
		events, err := client.ExecutionEvent.Query().
			Where(executionevent.ResponseIDEQ(responseID)).
			Order(executionevent.ByOccurredAt(entsql.OrderAsc())).
			All(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("query responses execution events: %w", err)
		}
		for _, event := range events {
			out.Events = append(out.Events, executionEventView(event))
		}
	}

	return nil, out, nil
}

func lookupResponsesRequestAudit(ctx context.Context, client *dao.Client, responseID string, requestAuditID string) (*dao.RequestAudit, error) {
	if requestAuditID != "" {
		audit, err := client.RequestAudit.Get(ctx, requestAuditID)
		if err != nil {
			if dao.IsNotFound(err) {
				return nil, fmt.Errorf("request audit %q not found", requestAuditID)
			}
			return nil, fmt.Errorf("query responses request audit: %w", err)
		}
		return audit, nil
	}
	audit, err := client.RequestAudit.Query().
		Where(requestaudit.ResponseIDEQ(responseID)).
		Order(requestaudit.ByCreatedAt(entsql.OrderDesc())).
		First(ctx)
	if err != nil {
		if dao.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query responses request audit: %w", err)
	}
	return audit, nil
}

func listResponsesUpstreamExchanges(ctx context.Context, client *dao.Client, responseID string, requestAuditID string) ([]*dao.UpstreamExchange, error) {
	query := client.UpstreamExchange.Query()
	switch {
	case responseID != "" && requestAuditID != "":
		query = query.Where(upstreamexchange.Or(
			upstreamexchange.ResponseIDEQ(responseID),
			upstreamexchange.RequestAuditIDEQ(requestAuditID),
		))
	case responseID != "":
		query = query.Where(upstreamexchange.ResponseIDEQ(responseID))
	case requestAuditID != "":
		query = query.Where(upstreamexchange.RequestAuditIDEQ(requestAuditID))
	default:
		return []*dao.UpstreamExchange{}, nil
	}
	exchanges, err := query.Order(upstreamexchange.ByStartedAt(entsql.OrderAsc())).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query responses upstream exchanges: %w", err)
	}
	return exchanges, nil
}

func requestAuditView(audit *dao.RequestAudit) *responsesRequestAuditView {
	if audit == nil {
		return nil
	}
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

func executionEventView(event *dao.ExecutionEvent) responsesExecutionEventView {
	return responsesExecutionEventView{
		ID:             event.ID,
		ResponseID:     event.ResponseID,
		ConversationID: event.ConversationID,
		EventType:      event.EventType,
		Phase:          event.Phase,
		Status:         event.Status,
		Message:        event.Message,
		DetailsJSON:    event.DetailsJSON,
		OccurredAt:     event.OccurredAt,
	}
}

func upstreamExchangeView(exchange *dao.UpstreamExchange) responsesUpstreamExchange {
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
