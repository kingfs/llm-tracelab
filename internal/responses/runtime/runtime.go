package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

type Config struct {
	DefaultModel string
	ForceStore   bool
}

type Runtime struct {
	cfg    Config
	client ChatCompletionsClient
	store  Store
}

func New(cfg Config, client ChatCompletionsClient, store Store) *Runtime {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Runtime{
		cfg:    cfg,
		client: client,
		store:  store,
	}
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
	history, err := r.loadContinuationHistory(ctx, req.PreviousResponseID)
	if err != nil {
		return protocol.Response{}, err
	}
	chatReq := chatCompletionRequest(req, model, history, inputItems)
	chatResp, err := r.client.ChatCompletion(ctx, chatReq)
	if err != nil {
		return protocol.Response{}, err
	}
	resp := chatToResponse(req, chatResp, model)
	if r.shouldStore(req) {
		if err := r.store.Put(ctx, resp, req, inputItems, resp.Output); err != nil {
			return protocol.Response{}, err
		}
	}
	return resp, nil
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

type ResponseNotFoundError struct {
	ID string
}

func (e ResponseNotFoundError) Error() string {
	return fmt.Sprintf("response %q not found", e.ID)
}

func chatCompletionRequest(req protocol.CreateResponseRequest, model string, history []LedgerItem, inputItems []protocol.InputItem) ChatCompletionRequest {
	tools := responseToolsToChatTools(req.Tools)
	return ChatCompletionRequest{
		Model:       model,
		Messages:    responseInputToMessages(req, history, inputItems),
		Tools:       tools,
		ToolChoice:  req.ToolChoice,
		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}
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

func responseToolsToChatTools(tools []protocol.Tool) []ChatTool {
	out := make([]ChatTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		out = append(out, ChatTool{
			Type: "function",
			Function: ChatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return out
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
	input.Output, _ = item["output"].(string)
	input.Content = inputContentParts(item["content"])
	if input.ID == "" {
		input.ID = newInputID()
	}
	if input.Type == "" {
		input.Type = "message"
	}
	return input
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
		return ChatMessage{Role: "tool", ToolCallID: item.CallID, Content: item.Output}
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
			messages = append(messages, ChatMessage{Role: "tool", ToolCallID: item.Output.CallID, Content: item.Output.Output})
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

func chatToResponse(req protocol.CreateResponseRequest, chat ChatCompletionResponse, model string) protocol.Response {
	return protocol.Response{
		ID:                 newResponseID(),
		Object:             "response",
		CreatedAt:          time.Now().Unix(),
		Status:             "completed",
		Model:              model,
		Output:             chatToOutputItems(chat),
		PreviousResponseID: req.PreviousResponseID,
		Usage: protocol.Usage{
			InputTokens:  chat.Usage.PromptTokens,
			OutputTokens: chat.Usage.CompletionTokens,
			TotalTokens:  chat.Usage.TotalTokens,
		},
		Metadata: req.Metadata,
	}
}

func chatToOutputItems(chat ChatCompletionResponse) []protocol.OutputItem {
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
		output = append(output, protocol.OutputItem{
			ID:      "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36),
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
