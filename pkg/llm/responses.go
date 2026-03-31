package llm

import (
	"encoding/json"
)

type OpenAIResponsesContentPart struct {
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	InputText  string      `json:"input_text,omitempty"`
	OutputText string      `json:"output_text,omitempty"`
	Refusal    string      `json:"refusal,omitempty"`
	Data       interface{} `json:"data,omitempty"`
}

type OpenAIResponsesInputItem struct {
	ID        string                       `json:"id,omitempty"`
	Type      string                       `json:"type"`
	Role      string                       `json:"role,omitempty"`
	Content   []OpenAIResponsesContentPart `json:"content,omitempty"`
	Arguments string                       `json:"arguments,omitempty"`
	CallID    string                       `json:"call_id,omitempty"`
	Name      string                       `json:"name,omitempty"`
	Output    interface{}                  `json:"output,omitempty"`
}

type OpenAIResponsesOutputItem = OpenAIResponsesInputItem

type OpenAIResponsesRequest struct {
	Model           string       `json:"model"`
	Input           interface{}  `json:"input"`
	Tools           []OpenAITool `json:"tools,omitempty"`
	ToolChoice      string       `json:"tool_choice,omitempty"`
	Temperature     *float64     `json:"temperature,omitempty"`
	TopP            *float64     `json:"top_p,omitempty"`
	MaxOutputTokens *int         `json:"max_output_tokens,omitempty"`
	User            string       `json:"user,omitempty"`
}

type OpenAIResponsesUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	TotalTokens              int `json:"total_tokens"`
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type OpenAIResponsesResponse struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object,omitempty"`
	Created int64                       `json:"created_at,omitempty"`
	Model   string                      `json:"model,omitempty"`
	Output  []OpenAIResponsesOutputItem `json:"output,omitempty"`
	Usage   *OpenAIResponsesUsage       `json:"usage,omitempty"`
}

type openAIResponsesAdapter struct {
	semantics TraceSemantics
}

func (a openAIResponsesAdapter) Semantics() TraceSemantics { return a.semantics }
func (a openAIResponsesAdapter) ParseRequest(body []byte) (LLMRequest, error) {
	var req OpenAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return LLMRequest{}, err
	}
	return FromOpenAIResponsesRequest(req), nil
}
func (a openAIResponsesAdapter) ParseResponse(body []byte) (LLMResponse, error) {
	var resp OpenAIResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, err
	}
	return OpenAIResponsesToLLM(resp), nil
}
func (a openAIResponsesAdapter) MarshalRequest(req LLMRequest) ([]byte, error) {
	return json.Marshal(req.ToOpenAIResponses())
}
func (a openAIResponsesAdapter) MarshalResponse(resp LLMResponse) ([]byte, error) {
	return json.Marshal(resp.ToOpenAIResponsesResponse())
}

func FromOpenAIResponsesRequest(req OpenAIResponsesRequest) LLMRequest {
	llmReq := LLMRequest{
		Model:       req.Model,
		ToolChoice:  req.ToolChoice,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxOutputTokens,
		UserID:      req.User,
	}

	llmReq.Tools = make([]LLMTool, 0, len(req.Tools))
	for i := range req.Tools {
		t := &req.Tools[i]
		llmReq.Tools = append(llmReq.Tools, LLMTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	switch input := req.Input.(type) {
	case string:
		llmReq.Messages = append(llmReq.Messages, LLMMessage{
			Role: "user",
			Content: []LLMContent{
				{Type: "text", Text: input},
			},
		})
	case []interface{}:
		for _, raw := range input {
			itemBytes, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var item OpenAIResponsesInputItem
			if err := json.Unmarshal(itemBytes, &item); err != nil {
				continue
			}
			if msg, ok := responsesItemToMessage(item); ok {
				llmReq.Messages = append(llmReq.Messages, msg)
			}
		}
	}

	return llmReq
}

func OpenAIResponsesToLLM(resp OpenAIResponsesResponse) LLMResponse {
	candidate := LLMCandidate{
		Index: 0,
		Role:  "assistant",
	}

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			candidate.Role = firstNonEmpty(item.Role, "assistant")
			for _, part := range item.Content {
				if text := firstNonEmpty(part.OutputText, part.Text, part.InputText); text != "" {
					candidate.Content = append(candidate.Content, LLMContent{
						Type: "text",
						Text: text,
					})
				}
				if part.Refusal != "" {
					candidate.Refusal = &LLMRefusal{
						Reason:  "refusal",
						Message: part.Refusal,
					}
				}
			}
		case "function_call":
			candidate.ToolCalls = append(candidate.ToolCalls, LLMToolCall{
				ID:       item.CallID,
				Type:     "function",
				Name:     item.Name,
				Args:     parseJSONObject(item.Arguments),
				ArgsText: item.Arguments,
			})
		case "function_call_output":
			candidate.Content = append(candidate.Content, LLMContent{
				Type:       "tool_result",
				ID:         item.ID,
				ToolCallID: item.CallID,
				ToolName:   item.Name,
				ToolResult: normalizeToolResult(item.Output),
			})
		}
	}

	result := LLMResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		CreatedAt:  resp.Created,
		Candidates: []LLMCandidate{candidate},
	}
	if resp.Usage != nil {
		result.Usage = &LLMUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			TotalTokens:              resp.Usage.TotalTokens,
			ReasoningTokens:          resp.Usage.ReasoningTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		}
	}
	return result
}

func (r *LLMRequest) ToOpenAIResponses() OpenAIResponsesRequest {
	input := make([]OpenAIResponsesInputItem, 0, len(r.Messages))
	for _, message := range r.Messages {
		item := OpenAIResponsesInputItem{
			Type: "message",
			Role: message.Role,
		}
		for _, content := range message.Content {
			switch content.Type {
			case "text":
				item.Content = append(item.Content, OpenAIResponsesContentPart{
					Type:      "input_text",
					InputText: content.Text,
				})
			case "tool_use":
				input = append(input, OpenAIResponsesInputItem{
					ID:        content.ID,
					Type:      "function_call",
					CallID:    content.ToolCallID,
					Name:      content.ToolName,
					Arguments: marshalCompactString(content.ToolArgs),
				})
			case "tool_result":
				input = append(input, OpenAIResponsesInputItem{
					ID:     content.ID,
					Type:   "function_call_output",
					CallID: content.ToolCallID,
					Name:   content.ToolName,
					Output: content.ToolResult,
				})
			}
		}
		if len(item.Content) > 0 {
			input = append(input, item)
		}
	}

	tools := make([]OpenAITool, 0, len(r.Tools))
	for _, tool := range r.Tools {
		tools = append(tools, OpenAITool{
			Type: "function",
			Function: OpenAIToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}

	return OpenAIResponsesRequest{
		Model:           r.Model,
		Input:           input,
		Tools:           tools,
		ToolChoice:      r.ToolChoice,
		Temperature:     r.Temperature,
		TopP:            r.TopP,
		MaxOutputTokens: r.MaxTokens,
		User:            r.UserID,
	}
}

func (r *LLMResponse) ToOpenAIResponsesResponse() OpenAIResponsesResponse {
	resp := OpenAIResponsesResponse{
		ID:      r.ID,
		Object:  "response",
		Created: r.CreatedAt,
		Model:   r.Model,
	}
	if len(r.Candidates) > 0 {
		candidate := r.Candidates[0]
		if len(candidate.Content) > 0 {
			item := OpenAIResponsesOutputItem{
				Type: "message",
				Role: firstNonEmpty(candidate.Role, "assistant"),
			}
			for _, content := range candidate.Content {
				switch content.Type {
				case "text":
					item.Content = append(item.Content, OpenAIResponsesContentPart{
						Type:       "output_text",
						OutputText: content.Text,
					})
				case "tool_result":
					resp.Output = append(resp.Output, OpenAIResponsesOutputItem{
						ID:     content.ID,
						Type:   "function_call_output",
						CallID: content.ToolCallID,
						Name:   content.ToolName,
						Output: content.ToolResult,
					})
				}
			}
			if candidate.Refusal != nil {
				item.Content = append(item.Content, OpenAIResponsesContentPart{
					Type:    "refusal",
					Refusal: candidate.Refusal.Message,
				})
			}
			if len(item.Content) > 0 {
				resp.Output = append(resp.Output, item)
			}
		}
		for _, toolCall := range candidate.ToolCalls {
			resp.Output = append(resp.Output, OpenAIResponsesOutputItem{
				ID:        toolCall.ID,
				Type:      "function_call",
				CallID:    toolCall.ID,
				Name:      toolCall.Name,
				Arguments: firstNonEmpty(toolCall.ArgsText, marshalCompactString(toolCall.Args)),
			})
		}
	}
	if r.Usage != nil {
		resp.Usage = &OpenAIResponsesUsage{
			InputTokens:              r.Usage.InputTokens,
			OutputTokens:             r.Usage.OutputTokens,
			TotalTokens:              r.Usage.TotalTokens,
			ReasoningTokens:          r.Usage.ReasoningTokens,
			CacheCreationInputTokens: r.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     r.Usage.CacheReadInputTokens,
		}
	}
	return resp
}

func responsesItemToMessage(item OpenAIResponsesInputItem) (LLMMessage, bool) {
	switch item.Type {
	case "message":
		msg := LLMMessage{Role: firstNonEmpty(item.Role, "user")}
		for _, part := range item.Content {
			if text := firstNonEmpty(part.InputText, part.Text, part.OutputText); text != "" {
				msg.Content = append(msg.Content, LLMContent{
					Type: "text",
					Text: text,
				})
			}
			if part.Refusal != "" {
				msg.Content = append(msg.Content, LLMContent{
					Type:    "text",
					Text:    part.Refusal,
					Refusal: part.Refusal,
				})
			}
		}
		return msg, len(msg.Content) > 0
	case "function_call":
		return LLMMessage{
			Role: "assistant",
			Content: []LLMContent{{
				ID:         item.ID,
				Type:       "tool_use",
				ToolCallID: item.CallID,
				ToolName:   item.Name,
				ToolArgs:   parseJSONObject(item.Arguments),
			}},
		}, true
	case "function_call_output":
		return LLMMessage{
			Role: "tool",
			Content: []LLMContent{{
				ID:         item.ID,
				Type:       "tool_result",
				ToolCallID: item.CallID,
				ToolName:   item.Name,
				ToolResult: normalizeToolResult(item.Output),
			}},
		}, true
	default:
		return LLMMessage{}, false
	}
}

func parseJSONObject(input string) map[string]any {
	if input == "" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(input), &result); err != nil {
		return nil
	}
	return result
}

func normalizeToolResult(v interface{}) map[string]any {
	if v == nil {
		return nil
	}
	if result, ok := v.(map[string]any); ok {
		return result
	}
	return map[string]any{"value": v}
}

func marshalCompactString(v interface{}) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
