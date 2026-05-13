package observe

import (
	"context"
	"fmt"
	"sync"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type ParseInput struct {
	TraceID      string
	CassettePath string
	Header       recordfile.RecordHeader
	Events       []recordfile.RecordEvent
	RequestBody  []byte
	ResponseBody []byte
	IsStream     bool
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
