package llm

// ========== Google Gemini generateContent 映射 ==========

type GeminiPart struct {
	Text string `json:"text,omitempty"`
	// 多模态字段略：inlineData, fileData 等
}

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type GeminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type GeminiToolFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type GeminiTool struct {
	FunctionDeclarations []GeminiToolFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type GeminiGenerateContentRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings    []GeminiSafetySetting   `json:"safetySettings,omitempty"`
	Tools             []GeminiTool            `json:"tools,omitempty"`
}

type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked"`
}

type GeminiCandidate struct {
	Content       GeminiContent        `json:"content"`
	FinishReason  string               `json:"finishReason"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type GeminiResponse struct {
	Candidates     []GeminiCandidate    `json:"candidates"`
	PromptFeedback map[string]any       `json:"promptFeedback,omitempty"`
	UsageMetadata  *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
}

// ---- LLMRequest -> GeminiGenerateContentRequest ----

func (r *LLMRequest) ToGemini() GeminiGenerateContentRequest {
	contents := make([]GeminiContent, 0, len(r.Messages))

	for _, m := range r.Messages {
		parts := make([]GeminiPart, 0, len(m.Content))
		for _, c := range m.Content {
			if c.Type == "text" {
				parts = append(parts, GeminiPart{
					Text: c.Text,
				})
			}
		}
		contents = append(contents, GeminiContent{
			Role:  m.Role, // user / model
			Parts: parts,
		})
	}

	var sys *GeminiContent
	if len(r.System) > 0 {
		parts := make([]GeminiPart, 0, len(r.System))
		for _, c := range r.System {
			if c.Type == "text" {
				parts = append(parts, GeminiPart{
					Text: c.Text,
				})
			}
		}
		sys = &GeminiContent{
			Role:  "system",
			Parts: parts,
		}
	}

	var genCfg *GeminiGenerationConfig
	if r.Temperature != nil || r.TopP != nil || r.TopK != nil || r.MaxTokens != nil || len(r.StopSequences) > 0 {
		genCfg = &GeminiGenerationConfig{
			Temperature:     r.Temperature,
			TopP:            r.TopP,
			TopK:            r.TopK,
			MaxOutputTokens: r.MaxTokens,
			StopSequences:   r.StopSequences,
		}
	}

	safety := make([]GeminiSafetySetting, 0, len(r.SafetySettings))
	for _, s := range r.SafetySettings {
		safety = append(safety, GeminiSafetySetting{
			Category:  s.Category,
			Threshold: s.Threshold,
		})
	}

	tools := make([]GeminiTool, 0)
	if len(r.Tools) > 0 {
		fd := make([]GeminiToolFunctionDeclaration, 0, len(r.Tools))
		for _, t := range r.Tools {
			fd = append(fd, GeminiToolFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		tools = append(tools, GeminiTool{
			FunctionDeclarations: fd,
		})
	}

	return GeminiGenerateContentRequest{
		Contents:          contents,
		SystemInstruction: sys,
		GenerationConfig:  genCfg,
		SafetySettings:    safety,
		Tools:             tools,
	}
}

// ---- GeminiResponse -> LLMResponse ----

func GeminiToLLM(resp GeminiResponse) LLMResponse {
	cands := make([]LLMCandidate, 0, len(resp.Candidates))
	safetyAll := make([]LLMSafetyRating, 0)

	for i, c := range resp.Candidates {
		content := make([]LLMContent, 0, len(c.Content.Parts))
		for _, p := range c.Content.Parts {
			content = append(content, LLMContent{
				Type: "text",
				Text: p.Text,
			})
		}

		safety := make([]LLMSafetyRating, 0, len(c.SafetyRatings))
		for _, s := range c.SafetyRatings {
			sr := LLMSafetyRating{
				Category:    s.Category,
				Probability: s.Probability,
				Blocked:     s.Blocked,
			}
			safety = append(safety, sr)
			safetyAll = append(safetyAll, sr)
		}

		cands = append(cands, LLMCandidate{
			Index:        i,
			Role:         c.Content.Role,
			Content:      content,
			FinishReason: c.FinishReason,
			Extensions: map[string]any{
				"safety_ratings": safety,
			},
		})
	}

	var usage *LLMUsage
	if resp.UsageMetadata != nil {
		usage = &LLMUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  resp.UsageMetadata.TotalTokenCount,
		}
	}

	return LLMResponse{
		Candidates: cands,
		Usage:      usage,
		Safety:     safetyAll,
		Extensions: map[string]any{
			"prompt_feedback": resp.PromptFeedback,
		},
	}
}
