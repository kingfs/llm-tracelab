package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMRequest_ToGemini(t *testing.T) {
	temp := 0.5
	maxTokens := 100

	req := LLMRequest{
		Model: "gemini-2.0-pro",
		System: []LLMContent{
			{Type: "text", Text: "System instruction"},
		},
		Messages: []LLMMessage{
			{
				Role: "user",
				Content: []LLMContent{
					{Type: "text", Text: "Hello Gemini"},
				},
			},
		},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}

	gReq := req.ToGemini()

	assert.Equal(t, 1, len(gReq.Contents))
	assert.Equal(t, "user", gReq.Contents[0].Role)
	assert.Equal(t, "Hello Gemini", gReq.Contents[0].Parts[0].Text)

	assert.NotNil(t, gReq.SystemInstruction)
	assert.Equal(t, "System instruction", gReq.SystemInstruction.Parts[0].Text)

	assert.NotNil(t, gReq.GenerationConfig)
	assert.Equal(t, &temp, gReq.GenerationConfig.Temperature)
	assert.Equal(t, &maxTokens, gReq.GenerationConfig.MaxOutputTokens)
}
func TestGeminiToLLM(t *testing.T) {
	resp := GeminiResponse{
		Candidates: []GeminiCandidate{
			{
				Content: GeminiContent{
					Role: "model",
					Parts: []GeminiPart{
						{Text: "Hello from Gemini"},
					},
				},
				FinishReason: "STOP",
				SafetyRatings: []GeminiSafetyRating{
					{Category: "HATE", Probability: "LOW", Blocked: false},
				},
			},
		},
		UsageMetadata: &GeminiUsageMetadata{
			PromptTokenCount:     3,
			CandidatesTokenCount: 7,
			TotalTokenCount:      10,
		},
	}

	llmResp := GeminiToLLM(resp)

	assert.Equal(t, 1, len(llmResp.Candidates))
	assert.Equal(t, "model", llmResp.Candidates[0].Role)
	assert.Equal(t, "Hello from Gemini", llmResp.Candidates[0].Content[0].Text)
	assert.Equal(t, "STOP", llmResp.Candidates[0].FinishReason)

	assert.Equal(t, 3, llmResp.Usage.InputTokens)
	assert.Equal(t, 7, llmResp.Usage.OutputTokens)
	assert.Equal(t, 10, llmResp.Usage.TotalTokens)

	assert.Equal(t, 1, len(llmResp.Safety))
	assert.Equal(t, "HATE", llmResp.Safety[0].Category)
}

func TestGeminiToLLMBlockedPromptFeedback(t *testing.T) {
	resp := GeminiResponse{
		PromptFeedback: map[string]any{
			"blockReason": "SAFETY",
		},
		UsageMetadata: &GeminiUsageMetadata{
			PromptTokenCount:     2,
			CandidatesTokenCount: 0,
			TotalTokenCount:      2,
		},
	}

	llmResp := GeminiToLLM(resp)

	require.Len(t, llmResp.Candidates, 1)
	require.NotNil(t, llmResp.Candidates[0].Refusal)
	assert.Equal(t, "SAFETY", llmResp.Candidates[0].Refusal.Reason)
	assert.Contains(t, llmResp.Candidates[0].Refusal.Message, "blockReason")
	assert.Equal(t, 2, llmResp.Usage.TotalTokens)
}

func TestGeminiStreamResponsePreservesPromptFeedbackAndSafety(t *testing.T) {
	body := []byte(
		"data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"}}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]},\"finishReason\":\"SAFETY\",\"safetyRatings\":[{\"category\":\"HATE\",\"probability\":\"HIGH\",\"blocked\":true}]}]}\n\n",
	)

	resp, err := ParseStreamResponseForPath("/v1beta/models/gemini-2.5-flash:streamGenerateContent", "https://generativelanguage.googleapis.com", body)
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	require.NotNil(t, resp.Candidates[0].Refusal)
	assert.Equal(t, "SAFETY", resp.Candidates[0].Refusal.Reason)
	assert.Equal(t, "model", resp.Candidates[0].Role)
	assert.Equal(t, "SAFETY", resp.Candidates[0].FinishReason)
	assert.Equal(t, "partial", resp.Candidates[0].Content[0].Text)
	assert.NotEmpty(t, resp.Safety)
	assert.Contains(t, resp.Extensions, "prompt_feedback")
	assert.Contains(t, resp.Candidates[0].Extensions, "safety_ratings")
}

func TestVertexStreamResponsePreservesPromptFeedbackAndSafety(t *testing.T) {
	body := []byte(
		"data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"}}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"vertex partial\"}]},\"finishReason\":\"SAFETY\",\"safetyRatings\":[{\"category\":\"HATE\",\"probability\":\"HIGH\",\"blocked\":true}]}]}\n\n",
	)

	resp, err := ParseStreamResponseForPath("/v1/projects/demo/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent", "https://us-central1-aiplatform.googleapis.com", body)
	require.NoError(t, err)
	require.Len(t, resp.Candidates, 1)
	require.NotNil(t, resp.Candidates[0].Refusal)
	assert.Equal(t, "SAFETY", resp.Candidates[0].Refusal.Reason)
	assert.Equal(t, "model", resp.Candidates[0].Role)
	assert.Equal(t, "SAFETY", resp.Candidates[0].FinishReason)
	assert.Equal(t, "vertex partial", resp.Candidates[0].Content[0].Text)
	assert.NotEmpty(t, resp.Safety)
	assert.Contains(t, resp.Extensions, "prompt_feedback")
	assert.Contains(t, resp.Candidates[0].Extensions, "safety_ratings")
}
