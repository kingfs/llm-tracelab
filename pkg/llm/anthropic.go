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

// ---- AnthropicRequest -> LLMRequest ----

func FromAnthropicRequest(req AnthropicRequest) LLMRequest {
	llmReq := LLMRequest{
		Model:          req.Model,
		ToolChoice:     req.ToolChoice,
		Temperature:    req.Temperature,
		TopP:           req.TopP,
		TopK:           req.TopK,
		MaxTokens:      req.MaxTokens,
		StopSeq:        req.StopSequences,
		SafetySettings: nil, // Anthropic doesn't expose safety in request
	}

	// ---- Metadata ----
	if req.Metadata != nil {
		if uid, ok := req.Metadata["user_id"]; ok {
			llmReq.UserID = uid
		}
	}

	// ---- System ----
	switch v := req.System.(type) {
	case string:
		llmReq.System = []LLMContent{{Type: "text", Text: v}}
	case []AnthropicContentBlock:
		llmReq.System = make([]LLMContent, 0, len(v))
		for i := range v {
			if v[i].Type == "text" {
				llmReq.System = append(llmReq.System, LLMContent{
					Type: "text",
					Text: v[i].Text,
				})
			}
		}
	}

	// ---- Messages ----
	llmReq.Messages = make([]LLMMessage, 0, len(req.Messages))
	for i := range req.Messages {
		m := &req.Messages[i]

		contents := make([]LLMContent, 0, len(m.Content))
		for j := range m.Content {
			b := &m.Content[j]
			if b.Type == "text" {
				contents = append(contents, LLMContent{
					Type: "text",
					Text: b.Text,
				})
			}
		}

		llmReq.Messages = append(llmReq.Messages, LLMMessage{
			Role:    m.Role,
			Content: contents,
		})
	}

	// ---- Tools ----
	llmReq.Tools = make([]LLMTool, 0, len(req.Tools))
	for i := range req.Tools {
		t := &req.Tools[i]
		llmReq.Tools = append(llmReq.Tools, LLMTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}

	return llmReq
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

// ---- LLMResponse -> OpenAIChatResponse ----
func (r *LLMResponse) ToAnthropicResponse() AnthropicResponse {
	// Claude 只支持单候选
	var c LLMCandidate
	if len(r.Candidates) > 0 {
		c = r.Candidates[0]
	}

	blocks := make([]AnthropicContentBlock, 0, len(c.Content))
	for _, cc := range c.Content {
		if cc.Type == "text" {
			blocks = append(blocks, AnthropicContentBlock{
				Type: "text",
				Text: cc.Text,
			})
		}
	}

	var usage *AnthropicUsage
	if r.Usage != nil {
		usage = &AnthropicUsage{
			InputTokens:              r.Usage.InputTokens,
			OutputTokens:             r.Usage.OutputTokens,
			CacheCreationInputTokens: r.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     r.Usage.CacheReadInputTokens,
		}
	}

	return AnthropicResponse{
		ID:         r.ID,
		Type:       "message",
		Role:       c.Role,
		Model:      r.Model,
		Content:    blocks,
		StopReason: c.FinishReason,
		Usage:      usage,
	}
}
