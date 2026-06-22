package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

type ChatPromptTokenCountRequest struct {
	Model      string
	Messages   []ChatMessage
	Tools      []ChatTool
	ToolChoice any
}

type ChatPromptTokenCounter interface {
	CountChatPromptTokens(req ChatPromptTokenCountRequest) (int, error)
}

type adapterBackedTokenEstimator struct {
	counter  ChatPromptTokenCounter
	fallback TokenEstimator
}

func NewAdapterBackedTokenEstimator(counter ChatPromptTokenCounter) TokenEstimator {
	return adapterBackedTokenEstimator{
		counter:  counter,
		fallback: conservativeTokenEstimator{},
	}
}

func (e adapterBackedTokenEstimator) EstimateResponsePromptTokens(req protocol.CreateResponseRequest, history []LedgerItem, inputItems []protocol.InputItem, webSearchReady bool) int {
	tools := responseToolsToChatTools(req.Tools, webSearchReady)
	messages := responseInputToMessages(req, history, inputItems)
	if e.counter == nil {
		return e.fallback.EstimateResponsePromptTokens(req, history, inputItems, webSearchReady)
	}
	tokens, err := e.counter.CountChatPromptTokens(ChatPromptTokenCountRequest{
		Model:      req.Model,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: req.ToolChoice,
	})
	if err != nil || tokens < 0 {
		return e.fallback.EstimateResponsePromptTokens(req, history, inputItems, webSearchReady)
	}
	return tokens
}

type TextTokenCounter interface {
	CountTextTokens(text string) (int, error)
}

type DeterministicTextTokenCounter struct{}

func (DeterministicTextTokenCounter) CountTextTokens(text string) (int, error) {
	return estimateTextTokens(text), nil
}

type ChatTokenCounter struct {
	Text TextTokenCounter
}

func (c ChatTokenCounter) CountChatPromptTokens(req ChatPromptTokenCountRequest) (int, error) {
	text := c.Text
	if text == nil {
		text = DeterministicTextTokenCounter{}
	}
	messages, err := c.countMessages(text, req.Messages)
	if err != nil {
		return 0, err
	}
	tools, err := c.countTools(text, req.Tools)
	if err != nil {
		return 0, err
	}
	toolChoice, err := estimateJSONishTokensWithCounter(req.ToolChoice, text)
	if err != nil {
		return 0, err
	}
	return messages + tools + toolChoice + 8, nil
}

func (c ChatTokenCounter) countMessages(text TextTokenCounter, messages []ChatMessage) (int, error) {
	total := 0
	for _, message := range messages {
		role, err := text.CountTextTokens(message.Role)
		if err != nil {
			return 0, err
		}
		content, err := text.CountTextTokens(chatMessageContentText(message.Content))
		if err != nil {
			return 0, err
		}
		total += 4 + role + content
		for _, call := range message.ToolCalls {
			tokens, err := countTexts(text, call.ID, call.Type, call.Function.Name, call.Function.Arguments)
			if err != nil {
				return 0, err
			}
			total += 4 + tokens
		}
		if message.ToolCallID != "" {
			tokens, err := text.CountTextTokens(message.ToolCallID)
			if err != nil {
				return 0, err
			}
			total += tokens
		}
	}
	return total, nil
}

func (c ChatTokenCounter) countTools(text TextTokenCounter, tools []ChatTool) (int, error) {
	total := 0
	for _, tool := range tools {
		tokens, err := countTexts(text, tool.Type, tool.Function.Name, tool.Function.Description)
		if err != nil {
			return 0, err
		}
		params, err := estimateJSONishTokensWithCounter(tool.Function.Parameters, text)
		if err != nil {
			return 0, err
		}
		total += 4 + tokens + params
	}
	return total, nil
}

type conservativeTokenEstimator struct{}

func (conservativeTokenEstimator) EstimateResponsePromptTokens(req protocol.CreateResponseRequest, history []LedgerItem, inputItems []protocol.InputItem, webSearchReady bool) int {
	tools := responseToolsToChatTools(req.Tools, webSearchReady)
	messages := responseInputToMessages(req, history, inputItems)
	return estimateChatMessagesTokens(messages) + estimateChatToolsTokens(tools) + estimateJSONishTokens(req.ToolChoice) + 8
}

func estimateChatMessagesTokens(messages []ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += 4
		total += estimateTextTokens(message.Role)
		total += estimateTextTokens(chatMessageContentText(message.Content))
		for _, call := range message.ToolCalls {
			total += 4
			total += estimateTextTokens(call.ID)
			total += estimateTextTokens(call.Type)
			total += estimateTextTokens(call.Function.Name)
			total += estimateTextTokens(call.Function.Arguments)
		}
		if message.ToolCallID != "" {
			total += estimateTextTokens(message.ToolCallID)
		}
	}
	return total
}

func estimateChatToolsTokens(tools []ChatTool) int {
	total := 0
	for _, tool := range tools {
		total += 4
		total += estimateTextTokens(tool.Type)
		total += estimateTextTokens(tool.Function.Name)
		total += estimateTextTokens(tool.Function.Description)
		total += estimateJSONishTokens(tool.Function.Parameters)
	}
	return total
}

func estimateJSONishTokens(value any) int {
	tokens, err := estimateJSONishTokensWithCounter(value, DeterministicTextTokenCounter{})
	if err != nil {
		return estimateTextTokens(fmt.Sprint(value))
	}
	return tokens
}

func estimateJSONishTokensWithCounter(value any, counter TextTokenCounter) (int, error) {
	if value == nil {
		return 0, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return counter.CountTextTokens(fmt.Sprint(value))
	}
	return counter.CountTextTokens(string(data))
}

func countTexts(counter TextTokenCounter, values ...string) (int, error) {
	total := 0
	for _, value := range values {
		tokens, err := counter.CountTextTokens(value)
		if err != nil {
			return 0, err
		}
		total += tokens
	}
	return total, nil
}

func estimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return (len([]byte(text)) + 3) / 4
}
