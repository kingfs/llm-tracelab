package runtime

import "context"

const (
	ModelExchangeKind                 = "model"
	ModelExchangeRolePrimaryModelCall = "primary_model_call"
	ModelExchangeRoleToolFollowup     = "tool_followup_model_call"
	ModelExchangeRoleCompact          = "compact_model_call"
)

type modelCallContextKey struct{}
type modelCallSequenceContextKey struct{}

type ModelCallMetadata struct {
	ExchangeKind  string
	ExchangeRole  string
	SequenceIndex int
	ResponseID    string
}

type modelCallSequence struct {
	next int
}

func withModelCallSequence(ctx context.Context) context.Context {
	if _, ok := modelCallSequenceFromContext(ctx); ok {
		return ctx
	}
	return context.WithValue(ctx, modelCallSequenceContextKey{}, &modelCallSequence{})
}

func modelCallSequenceFromContext(ctx context.Context) (*modelCallSequence, bool) {
	seq, ok := ctx.Value(modelCallSequenceContextKey{}).(*modelCallSequence)
	return seq, ok && seq != nil
}

func withNextModelCallMetadata(ctx context.Context, role string, responseID string) context.Context {
	ctx = withModelCallSequence(ctx)
	seq, _ := modelCallSequenceFromContext(ctx)
	metadata := ModelCallMetadata{
		ExchangeKind:  ModelExchangeKind,
		ExchangeRole:  role,
		SequenceIndex: seq.next,
		ResponseID:    responseID,
	}
	seq.next++
	return context.WithValue(ctx, modelCallContextKey{}, metadata)
}

func ModelCallMetadataFromContext(ctx context.Context) (ModelCallMetadata, bool) {
	metadata, ok := ctx.Value(modelCallContextKey{}).(ModelCallMetadata)
	return metadata, ok
}

type ChatCompletionsClient interface {
	ChatCompletion(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error)
}

type ChatCompletionsStreamer interface {
	ChatCompletionStream(ctx context.Context, req ChatCompletionRequest, handle ChatStreamCallback) (ChatCompletionResponse, error)
}

type ChatStreamCallback func(ChatStreamEvent) error

type ChatStreamEvent struct {
	ChoiceIndex    int
	Role           string
	ContentDelta   string
	ToolCallDeltas []ChatStreamToolCallDelta
	FinishReason   *string
}

type ChatStreamToolCallDelta struct {
	Index          int
	ID             string
	Type           string
	FunctionName   string
	ArgumentsDelta string
}

type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []ChatTool    `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type ChatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
}

type ChatTool struct {
	Type     string       `json:"type"`
	Function ChatFunction `json:"function"`
}

type ChatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ChatToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function ChatToolCallFunction `json:"function"`
}

type ChatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
