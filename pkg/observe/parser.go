package observe

import (
	"context"
	"fmt"
	"sync"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type ParseInput struct {
	TraceID          string
	CassettePath     string
	Header           recordfile.RecordHeader
	Events           []recordfile.RecordEvent
	RequestBody      []byte
	ResponseBody     []byte
	IsStream         bool
	ExchangeKind     string
	ExchangeRole     string
	ParentExchangeID string
	SequenceIndex    int
	RequestAuditID   string
	ResponseID       string
}

type Parser interface {
	Name() string
	Version() string
	CanParse(input ParseInput) bool
	Parse(ctx context.Context, input ParseInput) (TraceObservation, error)
}

type Registry struct {
	mu      sync.RWMutex
	parsers []Parser
}

func NewRegistry(parsers ...Parser) *Registry {
	r := &Registry{}
	for _, parser := range parsers {
		r.Register(parser)
	}
	return r
}

func NewDefaultRegistry() *Registry {
	return NewRegistry(
		NewEntryParser(),
		NewOpenAIParser(),
		NewAnthropicParser(),
		NewGeminiParser(),
	)
}

func (r *Registry) Register(parser Parser) {
	if parser == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parsers = append(r.parsers, parser)
}

func (r *Registry) Parsers() []Parser {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Parser, len(r.parsers))
	copy(out, r.parsers)
	return out
}

func (r *Registry) Select(input ParseInput) (Parser, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, parser := range r.parsers {
		if parser.CanParse(input) {
			return parser, true
		}
	}
	return nil, false
}

func (r *Registry) Parse(ctx context.Context, input ParseInput) (TraceObservation, error) {
	parser, ok := r.Select(input)
	if !ok {
		return TraceObservation{}, fmt.Errorf("observe: no parser for provider=%q operation=%q endpoint=%q", input.Header.Meta.Provider, input.Header.Meta.Operation, input.Header.Meta.Endpoint)
	}
	return parser.Parse(ctx, input)
}

func applyExchangeMetadata(input ParseInput, obs *TraceObservation) {
	obs.ExchangeKind = firstObservationNonEmpty(input.ExchangeKind, input.Header.Meta.ExchangeKind, obs.ExchangeKind)
	obs.ExchangeRole = firstObservationNonEmpty(input.ExchangeRole, input.Header.Meta.ExchangeRole, obs.ExchangeRole)
	obs.ParentExchangeID = firstObservationNonEmpty(input.ParentExchangeID, input.Header.Meta.ParentExchangeID, obs.ParentExchangeID)
	if input.SequenceIndex != 0 {
		obs.SequenceIndex = input.SequenceIndex
	} else if input.Header.Meta.SequenceIndex != 0 {
		obs.SequenceIndex = input.Header.Meta.SequenceIndex
	}
	obs.RequestAuditID = firstObservationNonEmpty(input.RequestAuditID, input.Header.Meta.RequestAuditID, obs.RequestAuditID)
	obs.ResponseID = firstObservationNonEmpty(input.ResponseID, input.Header.Meta.ResponseID, obs.ResponseID)
	if obs.ExchangeKind == "" {
		obs.ExchangeKind = "model"
	}
	if obs.ExchangeRole == "" {
		if obs.ExchangeKind == "entry" {
			obs.ExchangeRole = "client_request"
		} else {
			obs.ExchangeRole = "primary_model_call"
		}
	}
}

func firstObservationNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
