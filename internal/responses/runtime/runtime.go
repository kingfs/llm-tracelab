package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/websearch"
)

var ErrIncrementalStreamUnsupported = errors.New("incremental responses stream unsupported")

type Config struct {
	DefaultModel                string
	ForceStore                  bool
	MaxToolIterations           int
	WebSearchEnabled            bool
	WebSearchMaxResults         int
	AutoCompact                 bool
	CompactHistoryItemThreshold int
	ModelProfiles               []ModelProfile
}

type Runtime struct {
	cfg               Config
	client            ChatCompletionsClient
	store             Store
	tokenEstimator    TokenEstimator
	webSearchProvider websearch.Provider
	functionExecutors map[string]configuredFunctionToolExecutor
	events            audit.ExecutionEventRecorder
}

type Option func(*Runtime)

type TokenEstimator interface {
	EstimateResponsePromptTokens(req protocol.CreateResponseRequest, history []LedgerItem, inputItems []protocol.InputItem, webSearchReady bool) int
}

func WithTokenEstimator(estimator TokenEstimator) Option {
	return func(r *Runtime) {
		if estimator != nil {
			r.tokenEstimator = estimator
		}
	}
}

func WithWebSearchProvider(provider websearch.Provider) Option {
	return func(r *Runtime) {
		r.webSearchProvider = provider
	}
}

func WithExecutionEventRecorder(recorder audit.ExecutionEventRecorder) Option {
	return func(r *Runtime) {
		r.events = recorder
	}
}

func WithFunctionToolExecutor(name string, executor FunctionToolExecutor) Option {
	return WithFunctionToolExecutorPolicy(name, executor, FunctionToolExecutorPolicy{})
}

func WithFunctionToolExecutorPolicy(name string, executor FunctionToolExecutor, policy FunctionToolExecutorPolicy) Option {
	return func(r *Runtime) {
		name = normalizeFunctionToolName(name)
		if name == "" || executor == nil {
			return
		}
		if r.functionExecutors == nil {
			r.functionExecutors = map[string]configuredFunctionToolExecutor{}
		}
		r.functionExecutors[name] = configuredFunctionToolExecutor{
			executor: executor,
			policy:   policy,
		}
	}
}

func New(cfg Config, client ChatCompletionsClient, store Store, opts ...Option) *Runtime {
	if store == nil {
		store = NewMemoryStore()
	}
	if cfg.MaxToolIterations <= 0 {
		cfg.MaxToolIterations = 4
	}
	rt := &Runtime{
		cfg:            cfg,
		client:         client,
		store:          store,
		tokenEstimator: NewAdapterBackedTokenEstimator(ChatTokenCounter{}),
	}
	for _, opt := range opts {
		opt(rt)
	}
	return rt
}

func (r *Runtime) Create(ctx context.Context, req protocol.CreateResponseRequest) (protocol.Response, error) {
	if r.client == nil {
		return protocol.Response{}, fmt.Errorf("chat completions client is required")
	}
	model := req.Model
	if model == "" {
		model = r.cfg.DefaultModel
	}
	if model == "" {
		return protocol.Response{}, fmt.Errorf("model is required")
	}
	inputItems := requestInputItems(req)
	r.recordSubmittedFunctionOutputs(ctx, inputItems)
	history, err := r.loadContinuationHistory(ctx, req.PreviousResponseID)
	if err != nil {
		return protocol.Response{}, err
	}
	modelProfile := r.cfg.ContextBudgetForModel(model)
	budget := modelProfile.Budget
	chatModel := modelProfile.UpstreamModelOr(model)
	webSearchReady := r.webSearchReady()
	compactDecision := r.autoCompactDecision(req, budget, history, inputItems, webSearchReady)
	if compactDecision.ShouldCompact {
		compactResp, err := r.Compact(ctx, protocol.CompactResponseRequest{
			ResponseID: req.PreviousResponseID,
			Model:      model,
			Metadata: map[string]any{
				"_gateway": map[string]any{
					"compact": map[string]any{
						"trigger":                "auto",
						"trigger_reason":         compactDecision.Trigger,
						"history_items":          len(history),
						"history_item_threshold": budget.CompactHistoryItemThreshold,
						"estimated_input_tokens": compactDecision.EstimatedInputTokens,
						"context_window_tokens":  compactDecision.ContextWindowTokens,
						"reserved_output_tokens": compactDecision.ReservedOutputTokens,
					},
				},
			},
		})
		if err != nil {
			return protocol.Response{}, err
		}
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			ResponseID:     compactResp.ID,
			ConversationID: audit.CodexConversationID(compactResp.Metadata),
			EventType:      "response.compact",
			Phase:          "compact",
			Status:         "auto_triggered",
			DetailsJSON: map[string]any{
				"target_response_id":     req.PreviousResponseID,
				"compact_response_id":    compactResp.ID,
				"trigger":                compactDecision.Trigger,
				"history_items":          len(history),
				"history_item_threshold": budget.CompactHistoryItemThreshold,
				"estimated_input_tokens": compactDecision.EstimatedInputTokens,
				"context_window_tokens":  compactDecision.ContextWindowTokens,
				"reserved_output_tokens": compactDecision.ReservedOutputTokens,
			},
		})
		req.PreviousResponseID = compactResp.ID
		history, err = r.loadContinuationHistory(ctx, req.PreviousResponseID)
		if err != nil {
			return protocol.Response{}, err
		}
	}
	if !webSearchReady && forcedWebSearchTool(req.ToolChoice) {
		return protocol.Response{}, UnsupportedHostedToolError{Tool: "web_search", Reason: "web_search is not enabled or no provider is configured"}
	}
	chatReq := chatCompletionRequest(req, chatModel, history, inputItems, webSearchReady, budget)
	resp, err := r.createWithToolLoop(ctx, req, model, chatReq)
	if err != nil {
		return protocol.Response{}, err
	}
	if r.shouldStore(req) {
		if err := r.store.Put(ctx, resp, req, inputItems, resp.Output); err != nil {
			return protocol.Response{}, err
		}
	}
	return resp, nil
}

type ResponseStreamSink interface {
	ResponseCreated(resp protocol.Response) error
	OutputTextDelta(delta ResponseTextDelta) error
	ResponseCompleted(resp protocol.Response) error
}

type FunctionCallArgumentStreamSink interface {
	FunctionCallArgumentsDelta(delta ResponseFunctionCallArgumentsDelta) error
	FunctionCallArgumentsDone(done ResponseFunctionCallArgumentsDone) error
}

type OutputItemStreamSink interface {
	OutputItemDone(done ResponseOutputItemDone) error
}

type OutputItemAddedStreamSink interface {
	OutputItemAdded(added ResponseOutputItemAdded) error
}

type ResponseTextDelta struct {
	OutputIndex  int
	ItemID       string
	ContentIndex int
	Delta        string
}

type ResponseFunctionCallArgumentsDelta struct {
	OutputIndex int
	ItemID      string
	CallID      string
	Delta       string
	Arguments   string
}

type ResponseFunctionCallArgumentsDone struct {
	OutputIndex int
	ItemID      string
	CallID      string
	Arguments   string
}

type ResponseOutputItemDone struct {
	OutputIndex int
	Item        protocol.OutputItem
}

type ResponseOutputItemAdded struct {
	OutputIndex int
	Item        protocol.OutputItem
}

func (r *Runtime) CreateStream(ctx context.Context, req protocol.CreateResponseRequest, sink ResponseStreamSink) (protocol.Response, error) {
	if r.client == nil {
		return protocol.Response{}, fmt.Errorf("chat completions client is required")
	}
	streamer, ok := r.client.(ChatCompletionsStreamer)
	if !ok {
		return protocol.Response{}, ErrIncrementalStreamUnsupported
	}
	if sink == nil {
		return protocol.Response{}, fmt.Errorf("response stream sink is required")
	}
	model := req.Model
	if model == "" {
		model = r.cfg.DefaultModel
	}
	if model == "" {
		return protocol.Response{}, fmt.Errorf("model is required")
	}
	inputItems := requestInputItems(req)
	r.recordSubmittedFunctionOutputs(ctx, inputItems)
	history, err := r.loadContinuationHistory(ctx, req.PreviousResponseID)
	if err != nil {
		return protocol.Response{}, err
	}
	modelProfile := r.cfg.ContextBudgetForModel(model)
	budget := modelProfile.Budget
	chatModel := modelProfile.UpstreamModelOr(model)
	webSearchReady := r.webSearchReady()
	if !incrementalStreamSupportsTools(req.Tools, webSearchReady) {
		return protocol.Response{}, ErrIncrementalStreamUnsupported
	}
	if r.autoCompactDecision(req, budget, history, inputItems, webSearchReady).ShouldCompact {
		return protocol.Response{}, ErrIncrementalStreamUnsupported
	}
	if !webSearchReady && forcedWebSearchTool(req.ToolChoice) {
		return protocol.Response{}, UnsupportedHostedToolError{Tool: "web_search", Reason: "web_search is not enabled or no provider is configured"}
	}
	chatReq := chatCompletionRequest(req, chatModel, history, inputItems, webSearchReady, budget)
	chatReq.Stream = true

	responseID := newResponseID()
	created := protocol.Response{
		ID:                 responseID,
		Object:             "response",
		CreatedAt:          time.Now().Unix(),
		Status:             "in_progress",
		Model:              model,
		PreviousResponseID: req.PreviousResponseID,
		Metadata:           req.Metadata,
	}
	createdSent := false
	sendCreated := func() error {
		if createdSent {
			return nil
		}
		createdSent = true
		return sink.ResponseCreated(created)
	}

	var usage ChatUsage
	output := []protocol.OutputItem{}
	toolIterations := 0
	for {
		messageID := "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36)
		outputOffset := len(output)
		functionStream := newFunctionCallStreamState(sink, outputOffset)
		chatResp, err := streamer.ChatCompletionStream(ctx, chatReq, func(event ChatStreamEvent) error {
			if event.ChoiceIndex != 0 {
				return nil
			}
			if event.ContentDelta != "" {
				if err := sendCreated(); err != nil {
					return err
				}
				if err := sink.OutputTextDelta(ResponseTextDelta{
					OutputIndex:  outputOffset,
					ItemID:       messageID,
					ContentIndex: 0,
					Delta:        event.ContentDelta,
				}); err != nil {
					return err
				}
			}
			for _, toolDelta := range event.ToolCallDeltas {
				functionStream.observe(toolDelta)
				if toolDelta.ArgumentsDelta == "" {
					continue
				}
				if err := sendCreated(); err != nil {
					return err
				}
				if err := functionStream.argumentsDelta(toolDelta); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return protocol.Response{}, err
		}
		usage = addChatUsage(usage, chatResp.Usage)
		if err := sendCreated(); err != nil {
			return protocol.Response{}, err
		}
		outputItems := chatToOutputItemsWithMessageID(chatResp, messageID)
		r.recordRequestedFunctionCalls(ctx, outputItems)
		if err := functionStream.argumentsDone(outputItems); err != nil {
			return protocol.Response{}, err
		}
		calls, hasToolCalls, allExecutable := r.executableToolCalls(chatResp)
		if !hasToolCalls || !allExecutable {
			output = append(output, outputItems...)
			resp := responseFromOutputWithID(responseID, req, model, output, usage)
			if r.shouldStore(req) {
				if err := r.store.Put(ctx, resp, req, inputItems, resp.Output); err != nil {
					return protocol.Response{}, err
				}
			}
			if err := sink.ResponseCompleted(resp); err != nil {
				return protocol.Response{}, err
			}
			return resp, nil
		}
		if len(calls) == 0 {
			return protocol.Response{}, UnsupportedHostedToolError{Tool: "web_search", Reason: "web_search is not enabled or no provider is configured"}
		}
		if toolIterations >= r.cfg.MaxToolIterations {
			return protocol.Response{}, MaxToolIterationsError{Max: r.cfg.MaxToolIterations}
		}
		toolIterations++
		assistantMessage := ChatMessage{Role: "assistant", ToolCalls: make([]ChatToolCall, 0, len(calls))}
		for _, call := range calls {
			assistantMessage.ToolCalls = append(assistantMessage.ToolCalls, call.call)
		}
		chatReq.Messages = append(chatReq.Messages, assistantMessage)
		for _, call := range calls {
			outputIndex := len(output)
			if err := sendCreated(); err != nil {
				return protocol.Response{}, err
			}
			if err := streamOutputItemAdded(sink, outputIndex, startedToolOutputItem(call)); err != nil {
				return protocol.Response{}, err
			}
			outputItem, toolContent, err := r.executeToolCall(ctx, call, toolIterations, toolExecutionContext{Stream: true})
			if err != nil {
				return protocol.Response{}, err
			}
			if err := streamOutputItemDone(sink, outputIndex, outputItem); err != nil {
				return protocol.Response{}, err
			}
			output = append(output, outputItem)
			chatReq.Messages = append(chatReq.Messages, ChatMessage{
				Role:       "tool",
				ToolCallID: call.call.ID,
				Content:    toolContent,
			})
		}
	}
}

func streamOutputItemAdded(sink ResponseStreamSink, outputIndex int, item protocol.OutputItem) error {
	itemSink, ok := sink.(OutputItemAddedStreamSink)
	if !ok {
		return nil
	}
	return itemSink.OutputItemAdded(ResponseOutputItemAdded{
		OutputIndex: outputIndex,
		Item:        item,
	})
}

func streamOutputItemDone(sink ResponseStreamSink, outputIndex int, item protocol.OutputItem) error {
	itemSink, ok := sink.(OutputItemStreamSink)
	if !ok {
		return nil
	}
	return itemSink.OutputItemDone(ResponseOutputItemDone{
		OutputIndex: outputIndex,
		Item:        item,
	})
}

func startedToolOutputItem(call executableToolCall) protocol.OutputItem {
	switch call.kind {
	case executableToolKindWebSearch:
		return protocol.OutputItem{
			ID:     "ws_" + call.call.ID,
			Type:   "web_search_call",
			Status: "in_progress",
			CallID: call.call.ID,
			Action: map[string]any{
				"type":  "search",
				"query": call.query,
			},
		}
	default:
		return protocol.OutputItem{
			ID:     "fco_" + call.call.ID,
			Type:   "function_call_output",
			Status: "in_progress",
			CallID: call.call.ID,
			Name:   call.call.Function.Name,
		}
	}
}

func (r *Runtime) InputItems(ctx context.Context, id string) (protocol.InputItemList, bool, error) {
	items, ok, err := r.store.InputItems(ctx, id)
	if err != nil || !ok {
		return protocol.InputItemList{}, ok, err
	}
	list := protocol.InputItemList{
		Object:  "list",
		Data:    items,
		HasMore: false,
	}
	if len(items) > 0 {
		list.FirstID = items[0].ID
		list.LastID = items[len(items)-1].ID
	}
	return list, true, nil
}

func incrementalStreamSupportsTools(tools []protocol.Tool, webSearchReady bool) bool {
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			continue
		case "web_search", "web_search_preview":
			if webSearchReady {
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

type functionCallStreamState struct {
	sink         FunctionCallArgumentStreamSink
	outputOffset int
	calls        map[int]*functionCallStreamCall
}

type functionCallStreamCall struct {
	index     int
	callID    string
	itemID    string
	arguments strings.Builder
}

func newFunctionCallStreamState(sink ResponseStreamSink, outputOffset int) *functionCallStreamState {
	out := &functionCallStreamState{outputOffset: outputOffset}
	if functionSink, ok := sink.(FunctionCallArgumentStreamSink); ok {
		out.sink = functionSink
	}
	return out
}

func (s *functionCallStreamState) observe(delta ChatStreamToolCallDelta) {
	if s == nil || s.sink == nil {
		return
	}
	s.call(delta)
}

func (s *functionCallStreamState) argumentsDelta(delta ChatStreamToolCallDelta) error {
	if s == nil || s.sink == nil || delta.ArgumentsDelta == "" {
		return nil
	}
	call := s.call(delta)
	call.arguments.WriteString(delta.ArgumentsDelta)
	return s.sink.FunctionCallArgumentsDelta(ResponseFunctionCallArgumentsDelta{
		OutputIndex: call.index,
		ItemID:      call.itemID,
		CallID:      call.callID,
		Delta:       delta.ArgumentsDelta,
		Arguments:   call.arguments.String(),
	})
}

func (s *functionCallStreamState) argumentsDone(outputItems []protocol.OutputItem) error {
	if s == nil || s.sink == nil || len(s.calls) == 0 {
		return nil
	}
	itemsByCallID := map[string]protocol.OutputItem{}
	for _, item := range outputItems {
		if item.Type == "function_call" && item.CallID != "" {
			itemsByCallID[item.CallID] = item
		}
	}
	indexes := make([]int, 0, len(s.calls))
	for index := range s.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := s.calls[index]
		if item, ok := itemsByCallID[call.callID]; ok {
			call.itemID = item.ID
		}
		if err := s.sink.FunctionCallArgumentsDone(ResponseFunctionCallArgumentsDone{
			OutputIndex: call.index,
			ItemID:      call.itemID,
			CallID:      call.callID,
			Arguments:   call.arguments.String(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *functionCallStreamState) call(delta ChatStreamToolCallDelta) *functionCallStreamCall {
	if s.calls == nil {
		s.calls = map[int]*functionCallStreamCall{}
	}
	call := s.calls[delta.Index]
	if call == nil {
		callID := delta.ID
		if callID == "" {
			callID = "call_" + strconv.Itoa(delta.Index)
		}
		call = &functionCallStreamCall{
			index:  s.outputOffset + delta.Index,
			callID: callID,
			itemID: "fc_" + callID,
		}
		s.calls[delta.Index] = call
	}
	if delta.ID != "" && delta.ID != call.callID {
		call.callID = delta.ID
		call.itemID = "fc_" + delta.ID
	}
	return call
}

func (r *Runtime) Compact(ctx context.Context, req protocol.CompactResponseRequest) (protocol.Response, error) {
	if r.client == nil {
		return protocol.Response{}, fmt.Errorf("chat completions client is required")
	}
	if req.ResponseID == "" {
		return protocol.Response{}, fmt.Errorf("response_id is required")
	}
	target, ok, err := r.store.Get(ctx, req.ResponseID)
	if err != nil {
		return protocol.Response{}, err
	}
	if !ok {
		return protocol.Response{}, ResponseNotFoundError{ID: req.ResponseID}
	}
	history, ok, err := r.store.ContinuationItems(ctx, req.ResponseID)
	if err != nil {
		return protocol.Response{}, err
	}
	if !ok {
		return protocol.Response{}, ResponseNotFoundError{ID: req.ResponseID}
	}
	model := req.Model
	if model == "" {
		model = target.Model
	}
	if model == "" {
		model = r.cfg.DefaultModel
	}
	if model == "" {
		return protocol.Response{}, fmt.Errorf("model is required")
	}

	r.recordExecutionEvent(ctx, audit.ExecutionEvent{
		EventType: "response.compact",
		Phase:     "compact",
		Status:    "model_call_started",
		DetailsJSON: map[string]any{
			"target_response_id": req.ResponseID,
			"model":              model,
			"history_items":      len(history),
		},
	})
	chatModel := r.cfg.ContextBudgetForModel(model).UpstreamModelOr(model)
	chatResp, err := r.client.ChatCompletion(ctx, compactChatRequest(chatModel, history))
	if err != nil {
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.compact",
			Phase:     "compact",
			Status:    "failed",
			Message:   err.Error(),
			DetailsJSON: map[string]any{
				"target_response_id": req.ResponseID,
				"model":              model,
			},
		})
		return protocol.Response{}, err
	}
	summary := compactSummaryText(chatResp)
	if summary == "" {
		summary = "No summary was produced."
	}
	metadata := compactResponseMetadata(target.Metadata, req.Metadata, req.ResponseID)
	inputItems := []protocol.InputItem{{
		ID:   "compact_" + req.ResponseID,
		Type: "compact_request",
		Extra: map[string]any{
			"target_response_id": req.ResponseID,
		},
	}}
	outputItems := []protocol.OutputItem{{
		ID:      "summary_" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Type:    "summary",
		Status:  "completed",
		Content: []protocol.ContentPart{{Type: "summary_text", Text: summary}},
	}}
	resp := protocol.Response{
		ID:                 newResponseID(),
		Object:             "response",
		CreatedAt:          time.Now().Unix(),
		Status:             "completed",
		Model:              model,
		Output:             outputItems,
		PreviousResponseID: req.ResponseID,
		Usage: protocol.Usage{
			InputTokens:  chatResp.Usage.PromptTokens,
			OutputTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:  chatResp.Usage.TotalTokens,
		},
		Metadata: metadata,
	}
	if err := r.store.Put(ctx, resp, protocol.CreateResponseRequest{
		Model:              model,
		Input:              []map[string]any{{"type": "compact_request", "response_id": req.ResponseID}},
		PreviousResponseID: req.ResponseID,
		Metadata:           metadata,
	}, inputItems, outputItems); err != nil {
		return protocol.Response{}, err
	}
	r.recordExecutionEvent(ctx, audit.ExecutionEvent{
		ResponseID:     resp.ID,
		ConversationID: audit.CodexConversationID(resp.Metadata),
		EventType:      "response.compact",
		Phase:          "compact",
		Status:         "completed",
		DetailsJSON: map[string]any{
			"target_response_id": req.ResponseID,
			"summary_chars":      len(summary),
		},
	})
	return resp, nil
}

func (r *Runtime) loadContinuationHistory(ctx context.Context, previousResponseID string) ([]LedgerItem, error) {
	if previousResponseID == "" {
		return nil, nil
	}
	items, ok, err := r.store.ContinuationItems(ctx, previousResponseID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ResponseNotFoundError{ID: previousResponseID}
	}
	return items, nil
}

func (r *Runtime) shouldStore(req protocol.CreateResponseRequest) bool {
	if r.cfg.ForceStore {
		return true
	}
	return req.Store == nil || *req.Store
}

type autoCompactDecision struct {
	ShouldCompact        bool
	Trigger              string
	EstimatedInputTokens int
	ContextWindowTokens  int
	ReservedOutputTokens int
}

func (r *Runtime) autoCompactDecision(req protocol.CreateResponseRequest, budget ContextBudget, history []LedgerItem, inputItems []protocol.InputItem, webSearchReady bool) autoCompactDecision {
	decision := autoCompactDecision{}
	if !r.cfg.AutoCompact || req.PreviousResponseID == "" {
		return decision
	}
	threshold := budget.CompactHistoryItemThreshold
	if threshold > 0 && len(history) > threshold {
		decision.ShouldCompact = true
		decision.Trigger = "history_items"
	}
	if budget.ContextWindowTokens <= 0 {
		return decision
	}
	estimatedInputTokens := r.tokenEstimator.EstimateResponsePromptTokens(req, history, inputItems, webSearchReady)
	reservedOutputTokens := effectiveMaxOutputTokens(req, budget)
	if estimatedInputTokens+reservedOutputTokens > budget.ContextWindowTokens {
		decision.ShouldCompact = true
		decision.Trigger = "context_window_tokens"
		decision.EstimatedInputTokens = estimatedInputTokens
		decision.ContextWindowTokens = budget.ContextWindowTokens
		decision.ReservedOutputTokens = reservedOutputTokens
	}
	return decision
}

func effectiveMaxOutputTokens(req protocol.CreateResponseRequest, budget ContextBudget) int {
	if req.MaxOutputTokens > 0 {
		return req.MaxOutputTokens
	}
	return budget.MaxOutputTokens
}

type ResponseNotFoundError struct {
	ID string
}

func (e ResponseNotFoundError) Error() string {
	return fmt.Sprintf("response %q not found", e.ID)
}

type UnsupportedHostedToolError struct {
	Tool   string
	Reason string
}

func (e UnsupportedHostedToolError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("unsupported hosted tool %q: %s", e.Tool, e.Reason)
	}
	return fmt.Sprintf("unsupported hosted tool %q", e.Tool)
}

type MaxToolIterationsError struct {
	Max int
}

func (e MaxToolIterationsError) Error() string {
	return fmt.Sprintf("maximum tool iterations exceeded: %d", e.Max)
}

func (r *Runtime) webSearchReady() bool {
	return r.cfg.WebSearchEnabled && r.webSearchProvider != nil
}

func (r *Runtime) createWithToolLoop(ctx context.Context, req protocol.CreateResponseRequest, model string, chatReq ChatCompletionRequest) (protocol.Response, error) {
	var usage ChatUsage
	output := []protocol.OutputItem{}
	toolIterations := 0

	for {
		chatResp, err := r.client.ChatCompletion(ctx, chatReq)
		if err != nil {
			return protocol.Response{}, err
		}
		usage = addChatUsage(usage, chatResp.Usage)

		calls, hasToolCalls, allExecutable := r.executableToolCalls(chatResp)
		if !hasToolCalls || !allExecutable {
			chatResp.Usage = usage
			outputItems := chatToOutputItems(chatResp)
			r.recordRequestedFunctionCalls(ctx, outputItems)
			output = append(output, outputItems...)
			return responseFromOutput(req, model, output, usage), nil
		}
		if len(calls) == 0 {
			return protocol.Response{}, UnsupportedHostedToolError{Tool: "web_search", Reason: "web_search is not enabled or no provider is configured"}
		}
		if toolIterations >= r.cfg.MaxToolIterations {
			return protocol.Response{}, MaxToolIterationsError{Max: r.cfg.MaxToolIterations}
		}
		toolIterations++

		assistantMessage := ChatMessage{Role: "assistant", ToolCalls: make([]ChatToolCall, 0, len(calls))}
		for _, call := range calls {
			assistantMessage.ToolCalls = append(assistantMessage.ToolCalls, call.call)
		}
		chatReq.Messages = append(chatReq.Messages, assistantMessage)

		for _, call := range calls {
			outputItem, toolContent, err := r.executeToolCall(ctx, call, toolIterations, toolExecutionContext{})
			if err != nil {
				return protocol.Response{}, err
			}
			output = append(output, outputItem)
			chatReq.Messages = append(chatReq.Messages, ChatMessage{
				Role:       "tool",
				ToolCallID: call.call.ID,
				Content:    toolContent,
			})
		}
	}
}

type toolExecutionContext struct {
	Stream bool
}

func (r *Runtime) executeToolCall(ctx context.Context, call executableToolCall, iteration int, exec toolExecutionContext) (protocol.OutputItem, string, error) {
	switch call.kind {
	case executableToolKindWebSearch:
		return r.executeWebSearchToolCall(ctx, call, iteration, exec)
	case executableToolKindFunction:
		return r.executeFunctionToolCall(ctx, call, iteration, exec)
	default:
		return protocol.OutputItem{}, "", fmt.Errorf("unsupported executable tool kind %q", call.kind)
	}
}

func (r *Runtime) executeWebSearchToolCall(ctx context.Context, call executableToolCall, iteration int, exec toolExecutionContext) (protocol.OutputItem, string, error) {
	eventDetails := map[string]any{
		"tool_name":   "web_search",
		"call_id":     call.call.ID,
		"query":       call.query,
		"iteration":   iteration,
		"max_results": r.cfg.WebSearchMaxResults,
		"executor":    "hosted:web_search",
		"stream":      exec.Stream,
	}
	r.recordExecutionEvent(ctx, audit.ExecutionEvent{
		EventType:   "response.tool_call",
		Phase:       "tool_call",
		Status:      "started",
		DetailsJSON: eventDetails,
	})
	result, err := r.webSearchProvider.Search(ctx, websearch.Query{
		Text:       call.query,
		MaxResults: r.cfg.WebSearchMaxResults,
	})
	if err != nil {
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.tool_call",
			Phase:     "tool_call",
			Status:    "failed",
			Message:   err.Error(),
			DetailsJSON: mergeEventDetails(eventDetails, map[string]any{
				"error": err.Error(),
			}),
		})
		return protocol.OutputItem{}, "", err
	}
	toolContent, err := webSearchToolMessageContent(call.query, result)
	if err != nil {
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.tool_call",
			Phase:     "tool_call",
			Status:    "failed",
			Message:   err.Error(),
			DetailsJSON: mergeEventDetails(eventDetails, map[string]any{
				"error":        err.Error(),
				"result_count": len(result.Results),
			}),
		})
		return protocol.OutputItem{}, "", err
	}
	r.recordExecutionEvent(ctx, audit.ExecutionEvent{
		EventType: "response.tool_call",
		Phase:     "tool_call",
		Status:    "completed",
		DetailsJSON: mergeEventDetails(eventDetails, map[string]any{
			"result_count": len(result.Results),
		}),
	})
	return webSearchCallOutput(call.call, call.query, result), toolContent, nil
}

func (r *Runtime) executeFunctionToolCall(ctx context.Context, call executableToolCall, iteration int, exec toolExecutionContext) (protocol.OutputItem, string, error) {
	argumentsForEvent := call.call.Function.Arguments
	if call.policy.RedactArguments {
		argumentsForEvent = "<redacted>"
	}
	eventDetails := map[string]any{
		"tool_name": call.call.Function.Name,
		"call_id":   call.call.ID,
		"arguments": argumentsForEvent,
		"iteration": iteration,
		"executor":  "function",
		"stream":    exec.Stream,
	}
	r.recordExecutionEvent(ctx, audit.ExecutionEvent{
		EventType:   "response.tool_call",
		Phase:       "tool_call",
		Status:      "started",
		DetailsJSON: eventDetails,
	})
	execCtx := ctx
	cancel := func() {}
	if call.policy.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, call.policy.Timeout)
	}
	defer cancel()
	result, err := call.executor.ExecuteFunctionTool(execCtx, FunctionToolCall{
		CallID:    call.call.ID,
		Name:      call.call.Function.Name,
		Arguments: call.call.Function.Arguments,
	})
	if err != nil {
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.tool_call",
			Phase:     "tool_call",
			Status:    "failed",
			Message:   err.Error(),
			DetailsJSON: mergeEventDetails(eventDetails, map[string]any{
				"error": err.Error(),
			}),
		})
		return protocol.OutputItem{}, "", err
	}
	toolContent := toolOutputContent(result.Output)
	if call.policy.MaxResultBytes > 0 && len([]byte(toolContent)) > call.policy.MaxResultBytes {
		err := FunctionToolResultTooLargeError{
			Name:           call.call.Function.Name,
			MaxResultBytes: call.policy.MaxResultBytes,
			ResultBytes:    len([]byte(toolContent)),
		}
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.tool_call",
			Phase:     "tool_call",
			Status:    "failed",
			Message:   err.Error(),
			DetailsJSON: mergeEventDetails(eventDetails, map[string]any{
				"error":        err.Error(),
				"result_bytes": err.ResultBytes,
			}),
		})
		return protocol.OutputItem{}, "", err
	}
	outputChars := len(toolContent)
	if call.policy.RedactOutput {
		outputChars = 0
	}
	r.recordExecutionEvent(ctx, audit.ExecutionEvent{
		EventType: "response.tool_call",
		Phase:     "tool_call",
		Status:    "completed",
		DetailsJSON: mergeEventDetails(eventDetails, map[string]any{
			"output_chars": outputChars,
		}),
	})
	return functionToolCallOutput(call.call, result.Output), toolContent, nil
}

func (r *Runtime) recordExecutionEvent(ctx context.Context, event audit.ExecutionEvent) {
	if r == nil || r.events == nil {
		return
	}
	if err := r.events.RecordExecutionEvent(ctx, event); err != nil {
		slog.Error("Failed to record responses runtime execution event", "event_type", event.EventType, "status", event.Status, "err", err)
	}
}

func (r *Runtime) recordRequestedFunctionCalls(ctx context.Context, outputItems []protocol.OutputItem) {
	for _, item := range outputItems {
		if item.Type != "function_call" {
			continue
		}
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.tool_call",
			Phase:     "tool_call",
			Status:    "requested",
			DetailsJSON: map[string]any{
				"tool_name": item.Name,
				"call_id":   item.CallID,
				"arguments": item.Arguments,
			},
		})
	}
}

func (r *Runtime) recordSubmittedFunctionOutputs(ctx context.Context, inputItems []protocol.InputItem) {
	for _, item := range inputItems {
		if item.Type != "function_call_output" {
			continue
		}
		r.recordExecutionEvent(ctx, audit.ExecutionEvent{
			EventType: "response.tool_call",
			Phase:     "tool_call",
			Status:    "submitted",
			DetailsJSON: map[string]any{
				"tool_name": item.Name,
				"call_id":   item.CallID,
			},
		})
	}
}

func mergeEventDetails(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func chatCompletionRequest(req protocol.CreateResponseRequest, model string, history []LedgerItem, inputItems []protocol.InputItem, webSearchReady bool, budget ContextBudget) ChatCompletionRequest {
	tools := responseToolsToChatTools(req.Tools, webSearchReady)
	return ChatCompletionRequest{
		Model:       model,
		Messages:    responseInputToMessages(req, history, inputItems),
		Tools:       tools,
		ToolChoice:  chatToolChoice(req.ToolChoice, tools),
		MaxTokens:   effectiveMaxOutputTokens(req, budget),
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
}

func compactChatRequest(model string, history []LedgerItem) ChatCompletionRequest {
	messages := []ChatMessage{{
		Role: "system",
		Content: strings.Join([]string{
			"Summarize the prior conversation for future continuation.",
			"Keep durable user goals, decisions, constraints, tool results, open tasks, and named IDs.",
			"Do not invent facts. Write a concise but complete summary.",
		}, "\n"),
	}}
	messages = append(messages, ledgerToChatMessages(history)...)
	messages = append(messages, ChatMessage{
		Role:    "user",
		Content: "Create the compact conversation summary now.",
	})
	return ChatCompletionRequest{
		Model:    model,
		Messages: normalizeChatMessages(messages),
	}
}

func compactSummaryText(chat ChatCompletionResponse) string {
	if len(chat.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(chatMessageContentText(chat.Choices[0].Message.Content))
}

func compactResponseMetadata(target map[string]any, request map[string]any, targetResponseID string) map[string]any {
	metadata := mergeMetadata(target, request)
	return mergeMetadata(metadata, map[string]any{
		"_gateway": map[string]any{
			"compact": map[string]any{
				"source_response_id": targetResponseID,
			},
		},
	})
}

func responseInputToMessages(req protocol.CreateResponseRequest, history []LedgerItem, inputItems []protocol.InputItem) []ChatMessage {
	messages := make([]ChatMessage, 0, len(history)+len(inputItems)+1)
	if req.Instructions != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: req.Instructions})
	}
	messages = append(messages, ledgerToChatMessages(history)...)
	messages = append(messages, inputItemsToChatMessages(inputItems)...)
	return normalizeChatMessages(messages)
}

func responseToolsToChatTools(tools []protocol.Tool, webSearchReady bool) []ChatTool {
	out := make([]ChatTool, 0, len(tools))
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			out = append(out, ChatTool{
				Type: "function",
				Function: ChatFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
				},
			})
		case "web_search", "web_search_preview":
			if webSearchReady {
				out = append(out, webSearchChatTool())
			}
		}
	}
	return out
}

func webSearchChatTool() ChatTool {
	return ChatTool{
		Type: "function",
		Function: ChatFunction{
			Name:        "web_search",
			Description: "Search the web for current or external information.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The web search query.",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
	}
}

func forcedWebSearchTool(toolChoice any) bool {
	switch value := toolChoice.(type) {
	case string:
		return value == "web_search" || value == "web_search_preview"
	case map[string]any:
		if typ, _ := value["type"].(string); typ == "web_search" || typ == "web_search_preview" {
			return true
		}
		if name, _ := value["name"].(string); name == "web_search" {
			return true
		}
		function, _ := value["function"].(map[string]any)
		name, _ := function["name"].(string)
		return name == "web_search"
	default:
		return false
	}
}

func chatToolChoice(choice any, tools []ChatTool) any {
	if len(tools) == 0 {
		return nil
	}
	if !forcedWebSearchTool(choice) {
		return choice
	}
	if !hasChatTool(tools, "web_search") {
		return nil
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "web_search",
		},
	}
}

func hasChatTool(tools []ChatTool, name string) bool {
	for _, tool := range tools {
		if tool.Type == "function" && tool.Function.Name == name {
			return true
		}
	}
	return false
}

const (
	executableToolKindWebSearch = "web_search"
	executableToolKindFunction  = "function"
)

type executableToolCall struct {
	kind     string
	call     ChatToolCall
	query    string
	executor FunctionToolExecutor
	policy   FunctionToolExecutorPolicy
}

func (r *Runtime) executableToolCalls(chat ChatCompletionResponse) ([]executableToolCall, bool, bool) {
	if len(chat.Choices) == 0 {
		return nil, false, false
	}
	calls := chat.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		return nil, false, false
	}
	out := make([]executableToolCall, 0, len(calls))
	for i, call := range calls {
		if call.ID == "" {
			call.ID = "call_" + strconv.Itoa(i)
		}
		switch call.Function.Name {
		case "web_search":
			if !r.webSearchReady() {
				return nil, true, true
			}
			query := webSearchQueryFromArguments(call.Function.Arguments)
			out = append(out, executableToolCall{kind: executableToolKindWebSearch, call: call, query: query})
		default:
			configured := r.functionExecutors[normalizeFunctionToolName(call.Function.Name)]
			if configured.executor == nil {
				return nil, true, false
			}
			out = append(out, executableToolCall{kind: executableToolKindFunction, call: call, executor: configured.executor, policy: configured.policy})
		}
	}
	return out, true, true
}

func webSearchQueryFromArguments(arguments string) string {
	var payload struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return strings.TrimSpace(arguments)
	}
	return strings.TrimSpace(payload.Query)
}

func webSearchCallOutput(call ChatToolCall, query string, result websearch.Result) protocol.OutputItem {
	sources := make([]any, 0, len(result.Results))
	for _, item := range result.Results {
		source := map[string]any{}
		if item.Title != "" {
			source["title"] = item.Title
		}
		if item.URL != "" {
			source["url"] = item.URL
		}
		if item.Snippet != "" {
			source["snippet"] = item.Snippet
		}
		sources = append(sources, source)
	}
	return protocol.OutputItem{
		ID:     "ws_" + call.ID,
		Type:   "web_search_call",
		Status: "completed",
		CallID: call.ID,
		Action: map[string]any{
			"type":    "search",
			"query":   query,
			"sources": sources,
		},
	}
}

func functionToolCallOutput(call ChatToolCall, output any) protocol.OutputItem {
	return protocol.OutputItem{
		ID:     "fco_" + call.ID,
		Type:   "function_call_output",
		Status: "completed",
		CallID: call.ID,
		Name:   call.Function.Name,
		Output: output,
	}
}

func webSearchToolMessageContent(query string, result websearch.Result) (string, error) {
	payload := struct {
		Query   string                   `json:"query"`
		Results []websearch.SearchResult `json:"results"`
	}{
		Query:   query,
		Results: result.Results,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal web_search tool output: %w", err)
	}
	return string(data), nil
}

func addChatUsage(left, right ChatUsage) ChatUsage {
	return ChatUsage{
		PromptTokens:     left.PromptTokens + right.PromptTokens,
		CompletionTokens: left.CompletionTokens + right.CompletionTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
	}
}

func requestInputItems(req protocol.CreateResponseRequest) []protocol.InputItem {
	switch input := req.Input.(type) {
	case string:
		return []protocol.InputItem{{
			ID:      newInputID(),
			Type:    "message",
			Role:    "user",
			Content: []protocol.ContentPart{{Type: "input_text", Text: input}},
		}}
	case []any:
		items := make([]protocol.InputItem, 0, len(input))
		for _, raw := range input {
			if item, ok := raw.(map[string]any); ok {
				items = append(items, mapToInputItem(item))
			}
		}
		return items
	default:
		return []protocol.InputItem{{
			ID:      newInputID(),
			Type:    "message",
			Role:    "user",
			Content: []protocol.ContentPart{{Type: "input_text", Text: fmt.Sprint(input)}},
		}}
	}
}

func mapToInputItem(item map[string]any) protocol.InputItem {
	input := protocol.InputItem{}
	input.ID, _ = item["id"].(string)
	input.Type, _ = item["type"].(string)
	input.Role, _ = item["role"].(string)
	input.CallID, _ = item["call_id"].(string)
	if input.CallID == "" {
		input.CallID, _ = item["tool_call_id"].(string)
	}
	input.Name, _ = item["name"].(string)
	input.Arguments, _ = item["arguments"].(string)
	input.Output = item["output"]
	input.Content = inputContentParts(item["content"])
	input.Extra = inputItemExtra(item)
	if input.ID == "" {
		input.ID = newInputID()
	}
	if input.Type == "" {
		input.Type = "message"
	}
	return input
}

func inputItemExtra(item map[string]any) map[string]any {
	extra := make(map[string]any, len(item))
	for key, value := range item {
		switch key {
		case "id", "type", "role", "content", "call_id", "tool_call_id", "name", "arguments", "output":
			continue
		default:
			extra[key] = value
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func inputContentParts(content any) []protocol.ContentPart {
	switch value := content.(type) {
	case string:
		return []protocol.ContentPart{{Type: "input_text", Text: value}}
	case []any:
		parts := make([]protocol.ContentPart, 0, len(value))
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := part["type"].(string)
			text, _ := part["text"].(string)
			parts = append(parts, protocol.ContentPart{Type: partType, Text: text})
		}
		return parts
	default:
		if content == nil {
			return nil
		}
		return []protocol.ContentPart{{Type: "input_text", Text: fmt.Sprint(content)}}
	}
}

func inputItemsToChatMessages(items []protocol.InputItem) []ChatMessage {
	messages := make([]ChatMessage, 0, len(items))
	pendingToolCalls := []ChatToolCall{}
	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		messages = append(messages, ChatMessage{Role: "assistant", ToolCalls: pendingToolCalls})
		pendingToolCalls = nil
	}
	for _, item := range items {
		if item.Type == "function_call" {
			pendingToolCalls = append(pendingToolCalls, inputFunctionCallToChatToolCall(item, len(pendingToolCalls)))
			continue
		}
		flushToolCalls()
		messages = append(messages, inputItemToChatMessage(item))
	}
	flushToolCalls()
	return messages
}

func inputItemToChatMessage(item protocol.InputItem) ChatMessage {
	role := item.Role
	if role == "developer" {
		role = "system"
	}
	if role == "" {
		role = "user"
	}
	if item.Type == "function_call_output" {
		return ChatMessage{Role: "tool", ToolCallID: item.CallID, Content: toolOutputContent(item.Output)}
	}
	if item.Type == "function_call" {
		return ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{inputFunctionCallToChatToolCall(item, 0)}}
	}
	return ChatMessage{Role: role, Content: contentPartsText(item.Content)}
}

func inputFunctionCallToChatToolCall(item protocol.InputItem, index int) ChatToolCall {
	callID := item.CallID
	if callID == "" {
		callID = "call_" + strconv.Itoa(index)
	}
	return ChatToolCall{
		ID:   callID,
		Type: "function",
		Function: ChatToolCallFunction{
			Name:      item.Name,
			Arguments: item.Arguments,
		},
	}
}

func ledgerToChatMessages(items []LedgerItem) []ChatMessage {
	messages := make([]ChatMessage, 0, len(items))
	pendingToolCalls := []ChatToolCall{}
	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		messages = append(messages, ChatMessage{Role: "assistant", ToolCalls: pendingToolCalls})
		pendingToolCalls = nil
	}
	for _, item := range items {
		if item.Input != nil {
			if item.Input.Type == "compact_request" {
				continue
			}
			if item.Input.Type == "function_call" {
				pendingToolCalls = append(pendingToolCalls, inputFunctionCallToChatToolCall(*item.Input, len(pendingToolCalls)))
			} else {
				flushToolCalls()
				messages = append(messages, inputItemToChatMessage(*item.Input))
			}
			continue
		}
		if item.Output == nil {
			continue
		}
		switch item.Output.Type {
		case "function_call":
			pendingToolCalls = append(pendingToolCalls, ChatToolCall{
				ID:   item.Output.CallID,
				Type: "function",
				Function: ChatToolCallFunction{
					Name:      item.Output.Name,
					Arguments: item.Output.Arguments,
				},
			})
		case "message":
			flushToolCalls()
			messages = append(messages, ChatMessage{Role: "assistant", Content: contentPartsText(item.Output.Content)})
		case "function_call_output":
			flushToolCalls()
			messages = append(messages, ChatMessage{Role: "tool", ToolCallID: item.Output.CallID, Content: toolOutputContent(item.Output.Output)})
		case "summary":
			flushToolCalls()
			messages = append(messages, ChatMessage{Role: "system", Content: "Previous conversation summary:\n" + contentPartsText(item.Output.Content)})
		}
	}
	flushToolCalls()
	return messages
}

func normalizeChatMessages(messages []ChatMessage) []ChatMessage {
	systemText := []string{}
	rest := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			if text := chatMessageContentText(message.Content); text != "" {
				systemText = append(systemText, text)
			}
			continue
		}
		rest = append(rest, message)
	}
	if len(systemText) == 0 {
		return rest
	}
	out := make([]ChatMessage, 0, len(rest)+1)
	out = append(out, ChatMessage{Role: "system", Content: strings.Join(systemText, "\n\n")})
	out = append(out, rest...)
	return out
}

func responseFromOutput(req protocol.CreateResponseRequest, model string, output []protocol.OutputItem, usage ChatUsage) protocol.Response {
	return responseFromOutputWithID(newResponseID(), req, model, output, usage)
}

func responseFromOutputWithID(id string, req protocol.CreateResponseRequest, model string, output []protocol.OutputItem, usage ChatUsage) protocol.Response {
	return protocol.Response{
		ID:                 id,
		Object:             "response",
		CreatedAt:          time.Now().Unix(),
		Status:             "completed",
		Model:              model,
		Output:             output,
		PreviousResponseID: req.PreviousResponseID,
		Usage: protocol.Usage{
			InputTokens:  usage.PromptTokens,
			OutputTokens: usage.CompletionTokens,
			TotalTokens:  usage.TotalTokens,
		},
		Metadata: req.Metadata,
	}
}

func chatToOutputItems(chat ChatCompletionResponse) []protocol.OutputItem {
	return chatToOutputItemsWithMessageID(chat, "")
}

func chatToOutputItemsWithMessageID(chat ChatCompletionResponse, messageID string) []protocol.OutputItem {
	output := []protocol.OutputItem{}
	if len(chat.Choices) == 0 {
		return output
	}
	msg := chat.Choices[0].Message
	for i, call := range msg.ToolCalls {
		callID := call.ID
		if callID == "" {
			callID = "call_" + strconv.Itoa(i)
		}
		output = append(output, protocol.OutputItem{
			ID:        "fc_" + callID,
			Type:      "function_call",
			Status:    "completed",
			CallID:    callID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	if text := chatMessageContentText(msg.Content); text != "" {
		if messageID == "" {
			messageID = "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		output = append(output, protocol.OutputItem{
			ID:      messageID,
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: text}},
		})
	}
	return output
}

func chatMessageContentText(content any) string {
	if content == nil {
		return ""
	}
	if text, ok := content.(string); ok {
		return text
	}
	return fmt.Sprint(content)
}

func toolOutputContent(output any) string {
	switch value := output.(type) {
	case nil:
		return ""
	case string:
		return value
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func contentPartsText(parts []protocol.ContentPart) string {
	text := ""
	for _, part := range parts {
		text += part.Text
	}
	return text
}

func newResponseID() string {
	return "resp_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func newInputID() string {
	return "in_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
