package observe

import (
	"context"
	"encoding/json"
	"fmt"
)

const entryParserVersion = "0.1.0"

type entryParser struct{}

func NewEntryParser() Parser {
	return entryParser{}
}

func (p entryParser) Name() string {
	return "entry"
}

func (p entryParser) Version() string {
	return entryParserVersion
}

func (p entryParser) CanParse(input ParseInput) bool {
	return input.ExchangeKind == "entry" || input.Header.Meta.ExchangeKind == "entry"
}

func (p entryParser) Parse(ctx context.Context, input ParseInput) (TraceObservation, error) {
	select {
	case <-ctx.Done():
		return TraceObservation{}, ctx.Err()
	default:
	}

	obs := TraceObservation{
		TraceID:       input.TraceID,
		Provider:      input.Header.Meta.Provider,
		Operation:     input.Header.Meta.Operation,
		Endpoint:      input.Header.Meta.Endpoint,
		Model:         input.Header.Meta.Model,
		Parser:        p.Name(),
		ParserVersion: p.Version(),
		Status:        ParseStatusParsed,
		RawRefs: RawReferences{
			CassettePath: input.CassettePath,
		},
		Timings: ObservationTimings{
			StartedAt:  input.Header.Meta.Time,
			DurationMs: input.Header.Meta.DurationMs,
			TTFTMs:     input.Header.Meta.TTFTMs,
		},
		Usage: ObservationUsage{
			InputTokens:         input.Header.Usage.PromptTokens,
			OutputTokens:        input.Header.Usage.CompletionTokens,
			TotalTokens:         input.Header.Usage.TotalTokens,
			CacheReadTokens:     cachedTokens(input),
			CacheCreationTokens: 0,
		},
	}
	applyExchangeMetadata(input, &obs)

	req, err := decodeJSONObject(input.RequestBody)
	if err != nil && len(input.RequestBody) > 0 {
		return obs, fmt.Errorf("parse entry request: %w", err)
	}
	if model := stringField(req, "model"); model != "" {
		obs.Model = model
	}
	if obs.Operation == "" {
		obs.Operation = stringField(req, "operation")
	}
	obs.Request.Config = objectWithout(req, "input", "messages")
	if len(req) > 0 {
		raw, _ := json.Marshal(req)
		node := SemanticNode{
			ID:             StableNodeID("entry_request", "$", "client_request", 0),
			ProviderType:   "client_request",
			NormalizedType: NodeUnknown,
			Path:           "$",
			Raw:            cloneRaw(raw),
			Metadata: map[string]any{
				"model":          obs.Model,
				"operation":      obs.Operation,
				"exchange_kind":  obs.ExchangeKind,
				"exchange_role":  obs.ExchangeRole,
				"request_audit":  obs.RequestAuditID,
				"sequence_index": obs.SequenceIndex,
			},
		}
		obs.Request.Nodes = append(obs.Request.Nodes, node)
	}

	if appendHTTPErrorResponseIfNonLLM(input, &obs) {
		return obs, nil
	}
	resp, err := decodeJSONObject(input.ResponseBody)
	if err != nil && len(input.ResponseBody) > 0 {
		return obs, fmt.Errorf("parse entry response: %w", err)
	}
	if responseID := stringField(resp, "id"); responseID != "" {
		obs.ResponseID = responseID
	}
	if status := stringField(resp, "status"); status != "" && obs.Operation == "" {
		obs.Operation = status
	}
	if len(resp) > 0 {
		raw, _ := json.Marshal(resp)
		node := SemanticNode{
			ID:             StableNodeID("entry_response", "$", "client_response", 0),
			ProviderType:   "client_response",
			NormalizedType: NodeUnknown,
			Path:           "$",
			Raw:            cloneRaw(raw),
			Metadata: map[string]any{
				"response_id":   obs.ResponseID,
				"status":        stringField(resp, "status"),
				"status_code":   input.Header.Meta.StatusCode,
				"exchange_kind": obs.ExchangeKind,
				"exchange_role": obs.ExchangeRole,
			},
		}
		obs.Response.Outputs = append(obs.Response.Outputs, node)
		obs.Response.Nodes = append(obs.Response.Nodes, node)
	}
	return obs, nil
}
