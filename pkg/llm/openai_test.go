package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLLMRequest_ToOpenAI(t *testing.T) {
	temp := 0.7
	maxTokens := 128

	req := LLMRequest{
		Model: "gpt-4.1",
		System: []LLMContent{
			{Type: "text", Text: "You are a helpful assistant."},
		},
		Messages: []LLMMessage{
			{
				Role: "user",
				Content: []LLMContent{
					{Type: "text", Text: "Hello"},
				},
			},
		},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
		Tools: []LLMTool{
			{
				Name:        "search",
				Description: "Search something",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
		Metadata: map[string]any{
			"user_id": "u123",
		},
	}

	openaiReq := req.ToOpenAI()

	assert.Equal(t, "gpt-4.1", openaiReq.Model)
	assert.Equal(t, 2, len(openaiReq.Messages))
	assert.Equal(t, "system", openaiReq.Messages[0].Role)
	assert.Equal(t, "You are a helpful assistant.", openaiReq.Messages[0].Content)
	assert.Equal(t, "user", openaiReq.Messages[1].Role)
	assert.Equal(t, "Hello", openaiReq.Messages[1].Content)
	assert.Equal(t, "u123", openaiReq.User)
	assert.Equal(t, 1, len(openaiReq.Tools))
	assert.Equal(t, "search", openaiReq.Tools[0].Function.Name)
}

func TestOpenAIToLLM(t *testing.T) {
	resp := OpenAIChatResponse{
		ID:      "chatcmpl-123",
		Model:   "gpt-4.1",
		Created: 123456,
		Choices: []OpenAIChatChoice{
			{
				Index: 0,
				Message: OpenAIChatMessage{
					Role:    "assistant",
					Content: "Hello world",
				},
				FinishReason: "stop",
			},
		},
		Usage: &OpenAIUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}

	llmResp := OpenAIToLLM(resp)

	assert.Equal(t, "chatcmpl-123", llmResp.ID)
	assert.Equal(t, "gpt-4.1", llmResp.Model)
	assert.Equal(t, 1, len(llmResp.Candidates))
	assert.Equal(t, "assistant", llmResp.Candidates[0].Role)
	assert.Equal(t, "Hello world", llmResp.Candidates[0].Content[0].Text)
	assert.Equal(t, "stop", llmResp.Candidates[0].FinishReason)
	assert.Equal(t, 10, llmResp.Usage.InputTokens)
	assert.Equal(t, 20, llmResp.Usage.OutputTokens)
	assert.Equal(t, 30, llmResp.Usage.TotalTokens)
}
