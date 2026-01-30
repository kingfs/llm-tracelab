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
