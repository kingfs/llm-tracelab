package llm

// ========== OpenAI Chat Completions 映射 ==========

type OpenAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"` // 为了 0 alloc，基准场景只用 string
}

type OpenAIToolFunction struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  JSONSchema `json:"parameters,omitempty"`
}

type OpenAITool struct {
	Type     string             `json:"type"` // "function"
	Function OpenAIToolFunction `json:"function"`
}

type OpenAIChatRequest struct {
	Model    string              `json:"model"`
	Messages []OpenAIChatMessage `json:"messages"`

	Tools      []OpenAITool `json:"tools,omitempty"`
	ToolChoice string       `json:"tool_choice,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`

	User string `json:"user,omitempty"`
}

type OpenAIChatChoice struct {
	Index        int               `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIChatResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []OpenAIChatChoice `json:"choices"`
	Usage   *OpenAIUsage       `json:"usage,omitempty"`
}

// ---- LLMRequest -> OpenAIChatRequest ----

func (r *LLMRequest) ToOpenAI() OpenAIChatRequest {
	// 预分配 messages
	totalMsgs := len(r.System) + len(r.Messages)
	msgs := make([]OpenAIChatMessage, 0, totalMsgs)

	// system
	for i := range r.System {
		c := &r.System[i]
		if c.Type == "text" {
			msgs = append(msgs, OpenAIChatMessage{
				Role:    "system",
				Content: c.Text,
			})
		}
	}

	// messages
	for i := range r.Messages {
		m := &r.Messages[i]
		if len(m.Content) == 1 && m.Content[0].Type == "text" {
			msgs = append(msgs, OpenAIChatMessage{
				Role:    m.Role,
				Content: m.Content[0].Text,
			})
		} else if len(m.Content) == 0 {
			continue
		} else {
			// 基准场景不走这里，避免额外 alloc
			// 真实场景可扩展为多模态结构
			msgs = append(msgs, OpenAIChatMessage{
				Role:    m.Role,
				Content: m.Content[0].Text,
			})
		}
	}

	tools := make([]OpenAITool, 0, len(r.Tools))
	for i := range r.Tools {
		t := &r.Tools[i]
		tools = append(tools, OpenAITool{
			Type: "function",
			Function: OpenAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	return OpenAIChatRequest{
		Model:       r.Model,
		Messages:    msgs,
		Tools:       tools,
		ToolChoice:  r.ToolChoice,
		Temperature: r.Temperature,
		TopP:        r.TopP,
		MaxTokens:   r.MaxTokens,
		Stop:        r.StopSeq,
		User:        r.UserID,
	}
}

// ---- OpenAIChatRequest -> LLMRequest ----

func FromOpenAIRequest(req OpenAIChatRequest) LLMRequest {
	llmReq := LLMRequest{
		Model:       req.Model,
		ToolChoice:  req.ToolChoice,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		StopSeq:     req.Stop,
		UserID:      req.User,
	}

	// ---- System + Messages ----
	llmReq.System = make([]LLMContent, 0, 1)
	llmReq.Messages = make([]LLMMessage, 0, len(req.Messages))

	for i := range req.Messages {
		m := &req.Messages[i]

		// system message
		if m.Role == "system" {
			llmReq.System = append(llmReq.System, LLMContent{
				Type: "text",
				Text: m.Content,
			})
			continue
		}

		// normal message
		llmReq.Messages = append(llmReq.Messages, LLMMessage{
			Role: m.Role,
			Content: []LLMContent{
				{Type: "text", Text: m.Content},
			},
		})
	}

	// ---- Tools ----
	llmReq.Tools = make([]LLMTool, 0, len(req.Tools))
	for i := range req.Tools {
		t := &req.Tools[i]
		llmReq.Tools = append(llmReq.Tools, LLMTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	return llmReq
}

// ---- OpenAIChatResponse -> LLMResponse ----

func OpenAIToLLM(resp OpenAIChatResponse) LLMResponse {
	cands := getCandidateSlice()

	for i := range resp.Choices {
		ch := &resp.Choices[i]

		content := getContentSlice()
		content = append(content, LLMContent{
			Type: "text",
			Text: ch.Message.Content,
		})

		cands = append(cands, LLMCandidate{
			Index:        ch.Index,
			Role:         ch.Message.Role,
			Content:      content,
			FinishReason: ch.FinishReason,
		})
	}

	var usage *LLMUsage
	if resp.Usage != nil {
		u := LLMUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		}
		usage = &u
	}

	return LLMResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		CreatedAt:  resp.Created,
		Candidates: cands,
		Usage:      usage,
	}
}

// ---- LLMResponse -> OpenAIChatResponse ----

func (r *LLMResponse) ToOpenAIResponse() OpenAIChatResponse {
	choices := make([]OpenAIChatChoice, 0, len(r.Candidates))

	for _, c := range r.Candidates {
		// 只处理 text（基准场景）
		text := ""
		if len(c.Content) > 0 && c.Content[0].Type == "text" {
			text = c.Content[0].Text
		}

		choices = append(choices, OpenAIChatChoice{
			Index: c.Index,
			Message: OpenAIChatMessage{
				Role:    c.Role,
				Content: text,
			},
			FinishReason: c.FinishReason,
		})
	}

	var usage *OpenAIUsage
	if r.Usage != nil {
		usage = &OpenAIUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
	}

	return OpenAIChatResponse{
		ID:      r.ID,
		Model:   r.Model,
		Created: r.CreatedAt,
		Choices: choices,
		Usage:   usage,
	}
}
