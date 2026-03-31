package llm

// ========== OpenAI Chat Completions 映射 ==========

type OpenAIChatMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type OpenAIToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function OpenAIToolFunctionCall `json:"function"`
}

type OpenAIToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
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
		if len(m.Content) == 0 {
			continue
		}
		msg := OpenAIChatMessage{Role: m.Role}
		if len(m.Content) == 1 && m.Content[0].Type == "text" {
			msg.Content = m.Content[0].Text
		} else {
			msg.Content = toOpenAIMessageContent(m.Content)
		}
		for _, content := range m.Content {
			if content.Type == "tool_result" {
				msg.ToolCallID = content.ToolCallID
				msg.Name = content.ToolName
			}
		}
		msgs = append(msgs, msg)
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
			appendOpenAIContentToSystem(&llmReq, m.Content)
			continue
		}

		msg := LLMMessage{
			Role:    m.Role,
			Content: parseOpenAIMessageContent(m.Content),
		}
		for _, toolCall := range m.ToolCalls {
			msg.Content = append(msg.Content, LLMContent{
				ID:         toolCall.ID,
				Type:       "tool_use",
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.Function.Name,
				ToolArgs:   parseJSONObject(toolCall.Function.Arguments),
			})
		}
		if m.ToolCallID != "" || m.Name != "" {
			msg.Content = append(msg.Content, LLMContent{
				Type:       "tool_result",
				ToolCallID: m.ToolCallID,
				ToolName:   m.Name,
			})
		}
		llmReq.Messages = append(llmReq.Messages, msg)
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
		content = append(content, parseOpenAIMessageContent(ch.Message.Content)...)
		toolCalls := make([]LLMToolCall, 0, len(ch.Message.ToolCalls))
		for _, toolCall := range ch.Message.ToolCalls {
			toolCalls = append(toolCalls, LLMToolCall{
				ID:       toolCall.ID,
				Type:     firstNonEmpty(toolCall.Type, "function"),
				Name:     toolCall.Function.Name,
				Args:     parseJSONObject(toolCall.Function.Arguments),
				ArgsText: toolCall.Function.Arguments,
			})
		}

		cands = append(cands, LLMCandidate{
			Index:        ch.Index,
			Role:         ch.Message.Role,
			Content:      content,
			FinishReason: ch.FinishReason,
			ToolCalls:    toolCalls,
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
		toolCalls := make([]OpenAIToolCall, 0, len(c.ToolCalls))
		for _, toolCall := range c.ToolCalls {
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   toolCall.ID,
				Type: firstNonEmpty(toolCall.Type, "function"),
				Function: OpenAIToolFunctionCall{
					Name:      toolCall.Name,
					Arguments: firstNonEmpty(toolCall.ArgsText, marshalCompactString(toolCall.Args)),
				},
			})
		}

		choices = append(choices, OpenAIChatChoice{
			Index: c.Index,
			Message: OpenAIChatMessage{
				Role:      c.Role,
				Content:   text,
				ToolCalls: toolCalls,
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

func appendOpenAIContentToSystem(req *LLMRequest, raw interface{}) {
	for _, content := range parseOpenAIMessageContent(raw) {
		if content.Type == "text" {
			req.System = append(req.System, content)
		}
	}
}

func parseOpenAIMessageContent(raw interface{}) []LLMContent {
	switch value := raw.(type) {
	case nil:
		return nil
	case string:
		return []LLMContent{{Type: "text", Text: value}}
	case []interface{}:
		result := make([]LLMContent, 0, len(value))
		for _, item := range value {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := itemMap["type"].(string)
			switch partType {
			case "text", "input_text", "output_text":
				text := firstNonEmpty(stringValue(itemMap["text"]), stringValue(itemMap["input_text"]), stringValue(itemMap["output_text"]))
				if text != "" {
					result = append(result, LLMContent{Type: "text", Text: text})
				}
			default:
				result = append(result, LLMContent{
					Type: partType,
					Text: marshalCompactString(itemMap),
				})
			}
		}
		return result
	default:
		return []LLMContent{{Type: "text", Text: marshalCompactString(value)}}
	}
}

func toOpenAIMessageContent(contents []LLMContent) []map[string]any {
	result := make([]map[string]any, 0, len(contents))
	for _, content := range contents {
		switch content.Type {
		case "text":
			result = append(result, map[string]any{
				"type": "text",
				"text": content.Text,
			})
		default:
			if content.Text != "" {
				result = append(result, map[string]any{
					"type": content.Type,
					"text": content.Text,
				})
			}
		}
	}
	return result
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}
