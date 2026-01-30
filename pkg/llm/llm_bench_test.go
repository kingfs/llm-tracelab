package llm

import (
	"testing"
)

//
// Shared test data
//

var testReq = LLMRequest{
	Model: "test-model",
	System: []LLMContent{
		{Type: "text", Text: "System prompt"},
	},
	Messages: []LLMMessage{
		{
			Role: "user",
			Content: []LLMContent{
				{Type: "text", Text: "Hello world"},
			},
		},
	},
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

var openaiResp = OpenAIChatResponse{
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

var anthropicResp = AnthropicResponse{
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

var geminiResp = GeminiResponse{
	Candidates: []GeminiCandidate{
		{
			Content: GeminiContent{
				Role: "model",
				Parts: []GeminiPart{
					{Text: "Hello from Gemini"},
				},
			},
			FinishReason: "STOP",
		},
	},
	UsageMetadata: &GeminiUsageMetadata{
		PromptTokenCount:     3,
		CandidatesTokenCount: 7,
		TotalTokenCount:      10,
	},
}

//
// Benchmarks: Request → Vendor
//

func BenchmarkToOpenAI(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = testReq.ToOpenAI()
	}
}

func BenchmarkToAnthropic(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = testReq.ToAnthropic()
	}
}

func BenchmarkToGemini(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = testReq.ToGemini()
	}
}

//
// Benchmarks: Vendor → LLMResponse
//

func BenchmarkOpenAIToLLM(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = OpenAIToLLM(openaiResp)
	}
}

func BenchmarkAnthropicToLLM(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = AnthropicToLLM(anthropicResp)
	}
}

func BenchmarkGeminiToLLM(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GeminiToLLM(geminiResp)
	}
}
