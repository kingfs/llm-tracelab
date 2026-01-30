package llm

// ========== Anthropic Messages 映射 ==========

type AnthropicContentBlock struct {
	Type string `json:"type"` // "text", "tool_use", ...
	Text string `json:"text,omitempty"`
}

type AnthropicMessage struct {
	Role    string                  `json:"role"`
	Content []AnthropicContentBlock `json:"content"`
}

type AnthropicTool struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	InputSchema JSONSchema `json:"input_schema,omitempty"`
}

type AnthropicRequest struct {
	Model string `json:"model"`

	System   any                `json:"system,omitempty"` // string 或 blocks
	Messages []AnthropicMessage `json:"messages"`

	Tools      []AnthropicTool `json:"tools,omitempty"`
	ToolChoice string          `json:"tool_choice,omitempty"`

	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`

	StopSequences []string `json:"stop_sequences,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`
}

type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []AnthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        *AnthropicUsage         `json:"usage,omitempty"`
}

// ---- LLMRequest -> AnthropicRequest ----

func (r *LLMRequest) ToAnthropic() AnthropicRequest {
	msgs := make([]AnthropicMessage, 0, len(r.Messages))

	for i := range r.Messages {
		m := &r.Messages[i]
		blocks := make([]AnthropicContentBlock, len(m.Content))
		for j := range m.Content {
			c := &m.Content[j]
			if c.Type == "text" {
				blocks[j] = AnthropicContentBlock{
					Type: "text",
					Text: c.Text,
				}
			} else {
				blocks[j] = AnthropicContentBlock{
					Type: "text",
					Text: "",
				}
			}
		}
		msgs = append(msgs, AnthropicMessage{
			Role:    m.Role,
			Content: blocks,
		})
	}

	var system any
	if len(r.System) == 1 && r.System[0].Type == "text" {
		system = r.System[0].Text
	} else if len(r.System) > 0 {
		blocks := make([]AnthropicContentBlock, len(r.System))
		for i := range r.System {
			c := &r.System[i]
			if c.Type == "text" {
				blocks[i] = AnthropicContentBlock{
					Type: "text",
					Text: c.Text,
				}
			}
		}
		system = blocks
	}

	tools := make([]AnthropicTool, 0, len(r.Tools))
	for i := range r.Tools {
		t := &r.Tools[i]
		tools = append(tools, AnthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	meta := map[string]string{}
	if r.UserID != "" {
		meta["user_id"] = r.UserID
	}

	return AnthropicRequest{
		Model:         r.Model,
		System:        system,
		Messages:      msgs,
		Tools:         tools,
		ToolChoice:    r.ToolChoice,
		MaxTokens:     r.MaxTokens,
		Temperature:   r.Temperature,
		TopP:          r.TopP,
		TopK:          r.TopK,
		StopSequences: r.StopSeq,
		Metadata:      meta,
	}
}

// ---- AnthropicResponse -> LLMResponse ----

func AnthropicToLLM(resp AnthropicResponse) LLMResponse {
	content := getContentSlice()
	for i := range resp.Content {
		b := &resp.Content[i]
		if b.Type == "text" {
			content = append(content, LLMContent{
				Type: "text",
				Text: b.Text,
			})
		}
	}

	var usage *LLMUsage
	if resp.Usage != nil {
		u := LLMUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			TotalTokens:              resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		}
		usage = &u
	}

	cand := LLMCandidate{
		Index:        0,
		Role:         resp.Role,
		Content:      content,
		FinishReason: resp.StopReason,
	}

	cands := getCandidateSlice()
	cands = append(cands, cand)

	return LLMResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		Candidates: cands,
		Usage:      usage,
	}
}
