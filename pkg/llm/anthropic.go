package llm

// ========== Anthropic Messages 映射 ==========

type AnthropicContentBlock struct {
	Type string `json:"type"` // "text", "tool_use", ...
	Text string `json:"text,omitempty"`
	// 其他类型略
}

type AnthropicMessage struct {
	Role    string                  `json:"role"`
	Content []AnthropicContentBlock `json:"content"`
}

type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type AnthropicRequest struct {
	Model         string             `json:"model"`
	System        any                `json:"system,omitempty"` // string 或 blocks
	Messages      []AnthropicMessage `json:"messages"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
	MaxTokens     *int               `json:"max_tokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Metadata      map[string]any     `json:"metadata,omitempty"`
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

	for _, m := range r.Messages {
		blocks := make([]AnthropicContentBlock, 0, len(m.Content))
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				blocks = append(blocks, AnthropicContentBlock{
					Type: "text",
					Text: c.Text,
				})
			default:
				// 多模态 / 工具调用可按需扩展
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
		blocks := make([]AnthropicContentBlock, 0, len(r.System))
		for _, c := range r.System {
			if c.Type == "text" {
				blocks = append(blocks, AnthropicContentBlock{
					Type: "text",
					Text: c.Text,
				})
			}
		}
		system = blocks
	}

	tools := make([]AnthropicTool, 0, len(r.Tools))
	for _, t := range r.Tools {
		tools = append(tools, AnthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
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
		StopSequences: r.StopSequences,
		Metadata:      r.Metadata,
	}
}

// ---- AnthropicResponse -> LLMResponse ----

func AnthropicToLLM(resp AnthropicResponse) LLMResponse {
	content := make([]LLMContent, 0, len(resp.Content))
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			content = append(content, LLMContent{
				Type: "text",
				Text: b.Text,
			})
		default:
			// tool_use / image 等可按需扩展
		}
	}

	var usage *LLMUsage
	if resp.Usage != nil {
		usage = &LLMUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			TotalTokens:              resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		}
	}

	cand := LLMCandidate{
		Index:        0,
		Role:         resp.Role,
		Content:      content,
		FinishReason: resp.StopReason,
	}

	return LLMResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		Candidates: []LLMCandidate{cand},
		Usage:      usage,
	}
}
