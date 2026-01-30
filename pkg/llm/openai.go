package llm

// ========== OpenAI Chat Completions 映射 ==========

type OpenAIChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string 或 结构化
}

type OpenAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type OpenAITool struct {
	Type     string             `json:"type"` // "function"
	Function OpenAIToolFunction `json:"function"`
}

type OpenAIChatRequest struct {
	Model          string              `json:"model"`
	Messages       []OpenAIChatMessage `json:"messages"`
	Tools          []OpenAITool        `json:"tools,omitempty"`
	ToolChoice     any                 `json:"tool_choice,omitempty"`
	Temperature    *float64            `json:"temperature,omitempty"`
	TopP           *float64            `json:"top_p,omitempty"`
	MaxTokens      *int                `json:"max_tokens,omitempty"`
	Stop           []string            `json:"stop,omitempty"`
	User           string              `json:"user,omitempty"`
	ResponseFormat map[string]any      `json:"response_format,omitempty"`
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
	// 细粒度字段略，可按需补充
}

type OpenAIChatResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []OpenAIChatChoice `json:"choices"`
	Usage   *OpenAIUsage       `json:"usage,omitempty"`
	// 其他字段略
}

// ---- LLMRequest -> OpenAIChatRequest ----

func (r *LLMRequest) ToOpenAI() OpenAIChatRequest {
	msgs := make([]OpenAIChatMessage, 0, len(r.System)+len(r.Messages))

	// system
	for _, c := range r.System {
		if c.Type == "text" {
			msgs = append(msgs, OpenAIChatMessage{
				Role:    "system",
				Content: c.Text,
			})
		}
	}

	// messages
	for _, m := range r.Messages {
		// 简化：如果只有一个 text，就直接用 string；否则用结构化
		if len(m.Content) == 1 && m.Content[0].Type == "text" {
			msgs = append(msgs, OpenAIChatMessage{
				Role:    m.Role,
				Content: m.Content[0].Text,
			})
		} else {
			// 这里可以根据需要映射为 OpenAI 的多模态结构
			msgs = append(msgs, OpenAIChatMessage{
				Role:    m.Role,
				Content: m.Content, // 你可以自定义结构
			})
		}
	}

	tools := make([]OpenAITool, 0, len(r.Tools))
	for _, t := range r.Tools {
		tools = append(tools, OpenAITool{
			Type: "function",
			Function: OpenAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	var user string
	if r.Metadata != nil {
		if v, ok := r.Metadata["user_id"].(string); ok {
			user = v
		}
	}

	return OpenAIChatRequest{
		Model:       r.Model,
		Messages:    msgs,
		Tools:       tools,
		ToolChoice:  r.ToolChoice,
		Temperature: r.Temperature,
		TopP:        r.TopP,
		MaxTokens:   r.MaxTokens,
		Stop:        r.StopSequences,
		User:        user,
	}
}

// ---- OpenAIChatResponse -> LLMResponse ----

func OpenAIToLLM(resp OpenAIChatResponse) LLMResponse {
	cands := make([]LLMCandidate, 0, len(resp.Choices))
	for _, ch := range resp.Choices {
		content := []LLMContent{}
		switch v := ch.Message.Content.(type) {
		case string:
			content = append(content, LLMContent{
				Type: "text",
				Text: v,
			})
		default:
			// 如果你在请求中用结构化 content，这里可以做更细致的反序列化
			content = append(content, LLMContent{
				Type: "text",
				Text: "", // 占位
			})
		}

		cands = append(cands, LLMCandidate{
			Index:        ch.Index,
			Role:         ch.Message.Role,
			Content:      content,
			FinishReason: ch.FinishReason,
		})
	}

	var usage *LLMUsage
	if resp.Usage != nil {
		usage = &LLMUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		}
	}

	return LLMResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		CreatedAt:  resp.Created,
		Candidates: cands,
		Usage:      usage,
	}
}
