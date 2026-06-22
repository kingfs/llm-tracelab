package runtime

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

type recordingChatPromptTokenCounter struct {
	tokens int
	err    error
	calls  int
	req    ChatPromptTokenCountRequest
}

func (c *recordingChatPromptTokenCounter) CountChatPromptTokens(req ChatPromptTokenCountRequest) (int, error) {
	c.calls++
	c.req = req
	return c.tokens, c.err
}

func TestAdapterBackedTokenEstimatorCallsAdapterWithPromptShape(t *testing.T) {
	counter := &recordingChatPromptTokenCounter{tokens: 42}
	estimator := NewAdapterBackedTokenEstimator(counter)
	history := []LedgerItem{{
		Output: &protocol.OutputItem{
			Type:    "message",
			Role:    "assistant",
			Content: []protocol.ContentPart{{Type: "output_text", Text: "history answer"}},
		},
	}}
	inputItems := []protocol.InputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: []protocol.ContentPart{{Type: "input_text", Text: "current question"}},
		},
		{
			Type:      "function_call",
			CallID:    "call_1",
			Name:      "lookup",
			Arguments: `{"q":"current"}`,
		},
		{
			Type:   "function_call_output",
			CallID: "call_1",
			Output: map[string]any{"answer": "tool result"},
		},
	}
	req := protocol.CreateResponseRequest{
		Model:        "gpt-test",
		Instructions: "system rule",
		Tools: []protocol.Tool{{
			Type:        "function",
			Name:        "lookup",
			Description: "find data",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"q": map[string]any{"type": "string"}},
			},
		}},
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
	}

	got := estimator.EstimateResponsePromptTokens(req, history, inputItems, false)

	if got != 42 {
		t.Fatalf("EstimateResponsePromptTokens() = %d, want adapter tokens", got)
	}
	if counter.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", counter.calls)
	}
	if counter.req.Model != "gpt-test" {
		t.Fatalf("adapter model = %q, want gpt-test", counter.req.Model)
	}
	if len(counter.req.Tools) != 1 || counter.req.Tools[0].Function.Name != "lookup" {
		t.Fatalf("adapter tools = %#v, want lookup tool", counter.req.Tools)
	}
	if !reflect.DeepEqual(counter.req.ToolChoice, req.ToolChoice) {
		t.Fatalf("adapter tool_choice = %#v, want %#v", counter.req.ToolChoice, req.ToolChoice)
	}
	if len(counter.req.Messages) != 5 {
		t.Fatalf("adapter messages len = %d, want system/history/input/function/output messages: %#v", len(counter.req.Messages), counter.req.Messages)
	}
	assertMessageContains(t, counter.req.Messages, "system", "system rule")
	assertMessageContains(t, counter.req.Messages, "assistant", "history answer")
	assertMessageContains(t, counter.req.Messages, "user", "current question")
	if !hasToolCall(counter.req.Messages, "lookup", `{"q":"current"}`) {
		t.Fatalf("adapter messages missing function call: %#v", counter.req.Messages)
	}
	assertMessageContains(t, counter.req.Messages, "tool", "tool result")
}

func TestAdapterBackedTokenEstimatorFallsBackToConservativeOnAdapterError(t *testing.T) {
	counter := &recordingChatPromptTokenCounter{tokens: 1, err: errors.New("tokenizer unavailable")}
	estimator := NewAdapterBackedTokenEstimator(counter)
	req := protocol.CreateResponseRequest{
		Model:        "gpt-test",
		Instructions: "system rule",
		Input:        "current question",
		Tools: []protocol.Tool{{
			Type:        "function",
			Name:        "lookup",
			Description: "find data",
			Parameters:  map[string]any{"type": "object"},
		}},
		ToolChoice: "auto",
	}
	history := []LedgerItem{{
		Input: &protocol.InputItem{
			Type:    "message",
			Role:    "user",
			Content: []protocol.ContentPart{{Type: "input_text", Text: "history question"}},
		},
	}}
	inputItems := requestInputItems(req)
	want := (conservativeTokenEstimator{}).EstimateResponsePromptTokens(req, history, inputItems, false)

	got := estimator.EstimateResponsePromptTokens(req, history, inputItems, false)

	if counter.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", counter.calls)
	}
	if got != want {
		t.Fatalf("EstimateResponsePromptTokens() = %d, want conservative fallback %d", got, want)
	}
}

func TestChatTokenCounterCountsMessagesToolsAndToolChoice(t *testing.T) {
	counter := ChatTokenCounter{}
	base := ChatPromptTokenCountRequest{Model: "gpt-test"}
	withPrompt := ChatPromptTokenCountRequest{
		Model: "gpt-test",
		Messages: []ChatMessage{
			{Role: "system", Content: "system rule"},
			{Role: "user", Content: "current question"},
		},
		Tools: []ChatTool{{
			Type: "function",
			Function: ChatFunction{
				Name:        "lookup",
				Description: "find data",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}},
	}

	baseTokens, err := counter.CountChatPromptTokens(base)
	if err != nil {
		t.Fatalf("CountChatPromptTokens(base) error = %v", err)
	}
	promptTokens, err := counter.CountChatPromptTokens(withPrompt)
	if err != nil {
		t.Fatalf("CountChatPromptTokens(withPrompt) error = %v", err)
	}
	if promptTokens <= baseTokens {
		t.Fatalf("prompt tokens = %d, want greater than base %d when messages/tools/tool_choice are present", promptTokens, baseTokens)
	}
}

func assertMessageContains(t *testing.T, messages []ChatMessage, role string, text string) {
	t.Helper()
	for _, message := range messages {
		if message.Role == role && strings.Contains(chatMessageContentText(message.Content), text) {
			return
		}
	}
	t.Fatalf("messages missing role %q containing %q: %#v", role, text, messages)
}

func hasToolCall(messages []ChatMessage, name string, arguments string) bool {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == name && call.Function.Arguments == arguments {
				return true
			}
		}
	}
	return false
}
