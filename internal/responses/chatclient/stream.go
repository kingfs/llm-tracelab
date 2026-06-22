package chatclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

// AggregateChatCompletionStream folds OpenAI-compatible Chat Completions SSE chunks into a final response.
func AggregateChatCompletionStream(r io.Reader) (runtime.ChatCompletionResponse, error) {
	return AggregateChatCompletionStreamWithCallback(r, nil)
}

// AggregateChatCompletionStreamWithCallback folds OpenAI-compatible Chat Completions SSE chunks into a final response,
// calling handle as content deltas are decoded.
func AggregateChatCompletionStreamWithCallback(r io.Reader, handle runtime.ChatStreamCallback) (runtime.ChatCompletionResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	acc := chatStreamAccumulator{
		choices: make(map[int]*chatStreamChoice),
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk chatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return runtime.ChatCompletionResponse{}, fmt.Errorf("decode chat completion stream chunk JSON: %w", err)
		}
		acc.add(chunk)
		if err := emitChatStreamEvents(chunk, handle); err != nil {
			return runtime.ChatCompletionResponse{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return runtime.ChatCompletionResponse{}, fmt.Errorf("read chat completion stream: %w", err)
	}
	return acc.response(), nil
}

func emitChatStreamEvents(chunk chatCompletionStreamChunk, handle runtime.ChatStreamCallback) error {
	if handle == nil {
		return nil
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content == nil && choice.Delta.Role == "" && choice.FinishReason == nil {
			continue
		}
		event := runtime.ChatStreamEvent{
			ChoiceIndex:  choice.Index,
			Role:         choice.Delta.Role,
			FinishReason: choice.FinishReason,
		}
		if choice.Delta.Content != nil {
			event.ContentDelta = *choice.Delta.Content
		}
		if err := handle(event); err != nil {
			return fmt.Errorf("handle chat completion stream event: %w", err)
		}
	}
	return nil
}

type chatCompletionStreamChunk struct {
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
	Choices []chatStreamChunkChoice `json:"choices"`
	Usage   runtime.ChatUsage       `json:"usage"`
}

type chatStreamChunkChoice struct {
	Index        int             `json:"index"`
	Delta        chatStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type chatStreamDelta struct {
	Role      string               `json:"role"`
	Content   *string              `json:"content"`
	ToolCalls []chatStreamToolCall `json:"tool_calls"`
}

type chatStreamToolCall struct {
	Index    int                        `json:"index"`
	ID       string                     `json:"id"`
	Type     string                     `json:"type"`
	Function chatStreamToolCallFunction `json:"function"`
}

type chatStreamToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatStreamAccumulator struct {
	id      string
	model   string
	usage   runtime.ChatUsage
	choices map[int]*chatStreamChoice
}

type chatStreamChoice struct {
	index        int
	role         string
	content      strings.Builder
	finishReason string
	tools        map[int]*chatStreamTool
}

type chatStreamTool struct {
	id        string
	typ       string
	name      string
	arguments strings.Builder
}

func (a *chatStreamAccumulator) add(chunk chatCompletionStreamChunk) {
	if a.id == "" {
		a.id = chunk.ID
	}
	if a.model == "" {
		a.model = chunk.Model
	}
	if chunk.Usage.TotalTokens != 0 || chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
		a.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		dst := a.choice(choice.Index)
		if choice.Delta.Role != "" {
			dst.role = choice.Delta.Role
		}
		if choice.Delta.Content != nil {
			dst.content.WriteString(*choice.Delta.Content)
		}
		if choice.FinishReason != nil {
			dst.finishReason = *choice.FinishReason
		}
		for _, tool := range choice.Delta.ToolCalls {
			dst.addToolCall(tool)
		}
	}
}

func (a *chatStreamAccumulator) choice(index int) *chatStreamChoice {
	if choice := a.choices[index]; choice != nil {
		return choice
	}
	choice := &chatStreamChoice{
		index: index,
		role:  "assistant",
		tools: make(map[int]*chatStreamTool),
	}
	a.choices[index] = choice
	return choice
}

func (c *chatStreamChoice) addToolCall(delta chatStreamToolCall) {
	tool := c.tools[delta.Index]
	if tool == nil {
		tool = &chatStreamTool{}
		c.tools[delta.Index] = tool
	}
	if delta.ID != "" {
		tool.id = delta.ID
	}
	if delta.Type != "" {
		tool.typ = delta.Type
	}
	if delta.Function.Name != "" {
		tool.name = delta.Function.Name
	}
	if delta.Function.Arguments != "" {
		tool.arguments.WriteString(delta.Function.Arguments)
	}
}

func (a *chatStreamAccumulator) response() runtime.ChatCompletionResponse {
	indexes := make([]int, 0, len(a.choices))
	for index := range a.choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	choices := make([]runtime.ChatChoice, 0, len(indexes))
	for _, index := range indexes {
		choice := a.choices[index]
		choices = append(choices, runtime.ChatChoice{
			Index: choice.index,
			Message: runtime.ChatMessage{
				Role:      firstNonEmpty(choice.role, "assistant"),
				Content:   choice.content.String(),
				ToolCalls: choice.toolCalls(),
			},
			FinishReason: choice.finishReason,
		})
	}

	return runtime.ChatCompletionResponse{
		ID:      a.id,
		Model:   a.model,
		Choices: choices,
		Usage:   a.usage,
	}
}

func (c *chatStreamChoice) toolCalls() []runtime.ChatToolCall {
	if len(c.tools) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(c.tools))
	for index := range c.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	out := make([]runtime.ChatToolCall, 0, len(indexes))
	for _, index := range indexes {
		tool := c.tools[index]
		out = append(out, runtime.ChatToolCall{
			ID:   tool.id,
			Type: firstNonEmpty(tool.typ, "function"),
			Function: runtime.ChatToolCallFunction{
				Name:      tool.name,
				Arguments: tool.arguments.String(),
			},
		})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
