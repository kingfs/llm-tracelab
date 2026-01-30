package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLLMRequest_ToAnthropic(t *testing.T) {
	topP := 0.9
	maxTokens := 256

	req := LLMRequest{
		Model: "claude-3-opus",
		System: []LLMContent{
			{Type: "text", Text: "System prompt"},
		},
		Messages: []LLMMessage{
			{
				Role: "user",
				Content: []LLMContent{
					{Type: "text", Text: "Hello Claude"},
				},
			},
		},
		TopP:      &topP,
		MaxTokens: &maxTokens,
	}

	anthReq := req.ToAnthropic()

	assert.Equal(t, "claude-3-opus", anthReq.Model)
	assert.Equal(t, "System prompt", anthReq.System)
	assert.Equal(t, 1, len(anthReq.Messages))
	assert.Equal(t, "user", anthReq.Messages[0].Role)
	assert.Equal(t, "Hello Claude", anthReq.Messages[0].Content[0].Text)
	assert.Equal(t, &maxTokens, anthReq.MaxTokens)
	assert.Equal(t, &topP, anthReq.TopP)
}

func TestAnthropicToLLM(t *testing.T) {
	resp := AnthropicResponse{
		ID:    "msg_123",
		Role:  "assistant",
		Model: "claude-3-opus",
		Content: []AnthropicContentBlock{
			{Type: "text", Text: "Hello from Claude"},
		},
		StopReason: "end_turn",
		Usage: &AnthropicUsage{
			InputTokens:  5,
			OutputTokens: 10,
		},
	}

	llmResp := AnthropicToLLM(resp)

	assert.Equal(t, "msg_123", llmResp.ID)
	assert.Equal(t, "claude-3-opus", llmResp.Model)
	assert.Equal(t, 1, len(llmResp.Candidates))
	assert.Equal(t, "assistant", llmResp.Candidates[0].Role)
	assert.Equal(t, "Hello from Claude", llmResp.Candidates[0].Content[0].Text)
	assert.Equal(t, "end_turn", llmResp.Candidates[0].FinishReason)
	assert.Equal(t, 5, llmResp.Usage.InputTokens)
	assert.Equal(t, 10, llmResp.Usage.OutputTokens)
	assert.Equal(t, 15, llmResp.Usage.TotalTokens)
}
