package llm

// ========== Google Gemini generateContent 映射 ==========

type GeminiPart struct {
	Text string `json:"text,omitempty"`
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
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  JSONSchema `json:"parameters,omitempty"`
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

	for i := range r.Messages {
		m := &r.Messages[i]
		parts := make([]GeminiPart, 0, len(m.Content))
		for j := range m.Content {
			c := &m.Content[j]
			if c.Type == "text" {
				parts = append(parts, GeminiPart{
					Text: c.Text,
				})
			}
		}
		contents = append(contents, GeminiContent{
			Role:  m.Role,
			Parts: parts,
		})
	}

	var sys *GeminiContent
	if len(r.System) > 0 {
		parts := make([]GeminiPart, 0, len(r.System))
		for i := range r.System {
			c := &r.System[i]
			if c.Type == "text" {
				parts = append(parts, GeminiPart{
					Text: c.Text,
				})
			}
		}
		tmp := GeminiContent{
			Role:  "system",
			Parts: parts,
		}
		sys = &tmp
	}

	var genCfg *GeminiGenerationConfig
	if r.Temperature != nil || r.TopP != nil || r.TopK != nil || r.MaxTokens != nil || len(r.StopSeq) > 0 {
		cfg := GeminiGenerationConfig{
			Temperature:     r.Temperature,
			TopP:            r.TopP,
			TopK:            r.TopK,
			MaxOutputTokens: r.MaxTokens,
			StopSequences:   r.StopSeq,
		}
		genCfg = &cfg
	}

	safety := make([]GeminiSafetySetting, 0, len(r.SafetySettings))
	for i := range r.SafetySettings {
		s := &r.SafetySettings[i]
		safety = append(safety, GeminiSafetySetting{
			Category:  s.Category,
			Threshold: s.Threshold,
		})
	}

	tools := make([]GeminiTool, 0, 1)
	if len(r.Tools) > 0 {
		fd := make([]GeminiToolFunctionDeclaration, 0, len(r.Tools))
		for i := range r.Tools {
			t := &r.Tools[i]
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

// ---- GeminiGenerateContentRequest -> LLMRequest ----

func FromGeminiRequest(req GeminiGenerateContentRequest) LLMRequest {
	llmReq := LLMRequest{
		Model: "", // Gemini model name is usually in URL, not body
	}

	// ---- System ----
	if req.SystemInstruction != nil {
		sys := req.SystemInstruction
		llmReq.System = make([]LLMContent, 0, len(sys.Parts))
		for i := range sys.Parts {
			p := &sys.Parts[i]
			llmReq.System = append(llmReq.System, LLMContent{
				Type: "text",
				Text: p.Text,
			})
		}
	}

	// ---- Messages ----
	llmReq.Messages = make([]LLMMessage, 0, len(req.Contents))
	for i := range req.Contents {
		c := &req.Contents[i]

		contents := make([]LLMContent, 0, len(c.Parts))
		for j := range c.Parts {
			p := &c.Parts[j]
			contents = append(contents, LLMContent{
				Type: "text",
				Text: p.Text,
			})
		}

		llmReq.Messages = append(llmReq.Messages, LLMMessage{
			Role:    c.Role,
			Content: contents,
		})
	}

	// ---- GenerationConfig ----
	if req.GenerationConfig != nil {
		cfg := req.GenerationConfig
		llmReq.Temperature = cfg.Temperature
		llmReq.TopP = cfg.TopP
		llmReq.TopK = cfg.TopK
		llmReq.MaxTokens = cfg.MaxOutputTokens
		llmReq.StopSeq = cfg.StopSequences
	}

	// ---- Safety ----
	llmReq.SafetySettings = make([]LLMSafetyConfig, 0, len(req.SafetySettings))
	for i := range req.SafetySettings {
		s := &req.SafetySettings[i]
		llmReq.SafetySettings = append(llmReq.SafetySettings, LLMSafetyConfig{
			Category:  s.Category,
			Threshold: s.Threshold,
		})
	}

	// ---- Tools ----
	llmReq.Tools = make([]LLMTool, 0, len(req.Tools))
	for i := range req.Tools {
		t := &req.Tools[i]
		for j := range t.FunctionDeclarations {
			fd := &t.FunctionDeclarations[j]
			llmReq.Tools = append(llmReq.Tools, LLMTool{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  fd.Parameters,
			})
		}
	}

	return llmReq
}

// ---- GeminiResponse -> LLMResponse ----

func GeminiToLLM(resp GeminiResponse) LLMResponse {
	cands := getCandidateSlice()
	safetyAll := make([]LLMSafetyRating, 0, len(resp.Candidates))

	for i := range resp.Candidates {
		c := &resp.Candidates[i]

		content := getContentSlice()
		for j := range c.Content.Parts {
			p := &c.Content.Parts[j]
			content = append(content, LLMContent{
				Type: "text",
				Text: p.Text,
			})
		}

		safety := make([]LLMSafetyRating, 0, len(c.SafetyRatings))
		for j := range c.SafetyRatings {
			s := &c.SafetyRatings[j]
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
		u := LLMUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  resp.UsageMetadata.TotalTokenCount,
		}
		usage = &u
	}

	if len(cands) == 0 {
		if refusal := geminiPromptFeedbackRefusal(resp.PromptFeedback); refusal != nil {
			cands = append(cands, LLMCandidate{
				Index:   0,
				Role:    "model",
				Refusal: refusal,
			})
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

func geminiPromptFeedbackRefusal(promptFeedback map[string]any) *LLMRefusal {
	if len(promptFeedback) == 0 {
		return nil
	}
	reason := "blocked"
	if blockReason, ok := promptFeedback["blockReason"].(string); ok && blockReason != "" {
		reason = blockReason
	}
	message := marshalCompactString(promptFeedback)
	if message == "" {
		message = reason
	}
	return &LLMRefusal{
		Reason:  reason,
		Message: message,
	}
}

// ---- LLMResponse -> OpenAIChatResponse ----

func (r *LLMResponse) ToGeminiResponse() GeminiResponse {
	cands := make([]GeminiCandidate, 0, len(r.Candidates))

	for _, c := range r.Candidates {
		parts := make([]GeminiPart, 0, len(c.Content))
		for _, cc := range c.Content {
			if cc.Type == "text" {
				parts = append(parts, GeminiPart{Text: cc.Text})
			}
		}

		cands = append(cands, GeminiCandidate{
			Content: GeminiContent{
				Role:  c.Role,
				Parts: parts,
			},
			FinishReason: c.FinishReason,
		})
	}

	var usage *GeminiUsageMetadata
	if r.Usage != nil {
		usage = &GeminiUsageMetadata{
			PromptTokenCount:     r.Usage.InputTokens,
			CandidatesTokenCount: r.Usage.OutputTokens,
			TotalTokenCount:      r.Usage.TotalTokens,
		}
	}

	return GeminiResponse{
		Candidates:    cands,
		UsageMetadata: usage,
	}
}
