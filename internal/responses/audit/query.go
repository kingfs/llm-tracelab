package audit

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/executionevent"
	"github.com/kingfs/llm-tracelab/ent/dao/requestaudit"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamexchange"
)

const (
	DefaultAuditQueryLimit = 100
	MaxAuditQueryLimit     = 500
)

type QueryService struct {
	client *dao.Client
}

func NewQueryService(client *dao.Client) *QueryService {
	return &QueryService{client: client}
}

type ListRequestAuditsParams struct {
	ResponseID     string
	RequestAuditID string
	Limit          int
}

type GetRequestAuditTraceParams struct {
	ResponseID            string
	RequestAuditID        string
	EventLimit            int
	UpstreamExchangeLimit int
}

type RequestAuditView struct {
	ID              string
	ResponseID      string
	ConversationID  string
	Method          string
	Path            string
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

type RequestAuditTrace struct {
	RequestAudit      RequestAuditView
	ExecutionEvents   []ExecutionEventView
	UpstreamExchanges []UpstreamExchangeView
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

func (s *QueryService) GetRequestAuditTrace(ctx context.Context, params GetRequestAuditTraceParams) (RequestAuditTrace, bool, error) {
	var trace RequestAuditTrace
	if s == nil || s.client == nil || (params.ResponseID == "" && params.RequestAuditID == "") {
		return trace, false, nil
	}
	audits, err := s.ListRequestAudits(ctx, ListRequestAuditsParams{
		ResponseID:     params.ResponseID,
		RequestAuditID: params.RequestAuditID,
		Limit:          1,
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
	return trace, true, nil
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
	if limit <= 0 {
		return DefaultAuditQueryLimit
	}
	if limit > MaxAuditQueryLimit {
		return MaxAuditQueryLimit
	}
	return limit
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
