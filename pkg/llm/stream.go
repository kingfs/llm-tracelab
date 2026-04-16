package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

type anthropicStreamBlockState struct {
	Type  string
	ID    string
	Name  string
	Text  strings.Builder
	Input strings.Builder
}

func (a openAIChatAdapter) ParseStreamResponse(body []byte) (LLMResponse, error) {
	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		toolCalls        []LLMToolCall
		toolCallByIndex  = map[int]*LLMToolCall{}
		streamError      map[string]any
	)

	scanner := newSSEScanner(body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		if payload, ok := parseOpenAIStreamError(jsonStr); ok {
			streamError = payload
			continue
		}

		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content          *string `json:"content"`
					ReasoningContent *string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				contentBuilder.WriteString(*choice.Delta.Content)
			}
			if choice.Delta.ReasoningContent != nil {
				reasoningBuilder.WriteString(*choice.Delta.ReasoningContent)
			}
			for _, tc := range choice.Delta.ToolCalls {
				call := ensureToolCallByIndex(toolCallByIndex, tc.Index)
				call.ID = firstNonEmpty(call.ID, tc.ID)
				call.Type = firstNonEmpty(tc.Type, "function")
				call.Name = firstNonEmpty(call.Name, tc.Function.Name)
				call.ArgsText += tc.Function.Arguments
			}
		}
	}

	toolCalls = flattenToolCalls(toolCallByIndex)
	return singleCandidateResponse(contentBuilder.String(), reasoningBuilder.String(), toolCalls, streamError), nil
}

func (a anthropicMessagesAdapter) ParseStreamResponse(body []byte) (LLMResponse, error) {
	type anthropicDelta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	}
	type anthropicContentBlock struct {
		Type     string      `json:"type"`
		ID       string      `json:"id"`
		Name     string      `json:"name"`
		Text     string      `json:"text"`
		Thinking string      `json:"thinking"`
		Input    interface{} `json:"input"`
	}
	type anthropicChunk struct {
		Type         string                `json:"type"`
		Index        int                   `json:"index"`
		Delta        anthropicDelta        `json:"delta"`
		ContentBlock anthropicContentBlock `json:"content_block"`
	}
	blockMap := map[int]*anthropicStreamBlockState{}
	var blockOrder []int
	var streamError map[string]any
	scanner := newSSEScanner(body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		if payload, ok := parseAnthropicStreamError(jsonStr); ok {
			streamError = payload
			continue
		}

		var chunk anthropicChunk
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}

		block := ensureAnthropicBlock(blockMap, &blockOrder, chunk.Index)
		switch chunk.Type {
		case "content_block_start":
			block.Type = chunk.ContentBlock.Type
			block.ID = chunk.ContentBlock.ID
			block.Name = chunk.ContentBlock.Name
			if chunk.ContentBlock.Text != "" {
				block.Text.WriteString(chunk.ContentBlock.Text)
			}
			if chunk.ContentBlock.Thinking != "" {
				block.Text.WriteString(chunk.ContentBlock.Thinking)
			}
			if chunk.ContentBlock.Input != nil {
				block.Input.WriteString(marshalCompactString(chunk.ContentBlock.Input))
			}
		case "content_block_delta":
			switch chunk.Delta.Type {
			case "text_delta":
				block.Text.WriteString(chunk.Delta.Text)
			case "thinking_delta":
				block.Text.WriteString(firstNonEmpty(chunk.Delta.Thinking, chunk.Delta.Text))
			case "input_json_delta":
				if block.Input.String() == "{}" {
					block.Input.Reset()
				}
				block.Input.WriteString(chunk.Delta.PartialJSON)
			}
		}
	}

	sort.Ints(blockOrder)
	candidate := LLMCandidate{Index: 0, Role: "assistant"}
	for _, idx := range blockOrder {
		block := blockMap[idx]
		switch block.Type {
		case "text":
			candidate.Content = append(candidate.Content, LLMContent{Type: "text", Text: block.Text.String()})
		case "thinking":
			candidate.Content = append(candidate.Content, LLMContent{Type: "thinking", Text: block.Text.String()})
		case "tool_use":
			candidate.ToolCalls = append(candidate.ToolCalls, LLMToolCall{
				ID:       block.ID,
				Type:     "function",
				Name:     block.Name,
				Args:     parseJSONObject(block.Input.String()),
				ArgsText: block.Input.String(),
			})
		}
	}
	return singleCandidateResponse(candidateText(candidate), candidateReasoning(candidate), candidate.ToolCalls, streamError), nil
}

func (a openAIResponsesAdapter) ParseStreamResponse(body []byte) (LLMResponse, error) {
	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		refusalBuilder   strings.Builder
		toolCallMap      = map[string]*LLMToolCall{}
		toolCallOrder    []string
		streamError      map[string]any
	)

	scanner := newSSEScanner(body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		if payload, ok := parseOpenAIStreamError(jsonStr); ok {
			streamError = payload
			continue
		}

		var envelope struct {
			Type      string `json:"type"`
			Delta     string `json:"delta"`
			Arguments string `json:"arguments"`
			ItemID    string `json:"item_id"`
			Item      struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Role      string `json:"role"`
				Content   []struct {
					Type       string `json:"type"`
					Text       string `json:"text"`
					InputText  string `json:"input_text"`
					OutputText string `json:"output_text"`
					Refusal    string `json:"refusal"`
				} `json:"content"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "response.output_text.delta":
			contentBuilder.WriteString(envelope.Delta)
		case "response.refusal.delta":
			refusalBuilder.WriteString(envelope.Delta)
		case "response.reasoning_text.delta":
			reasoningBuilder.WriteString(envelope.Delta)
		case "response.reasoning_summary_text.delta":
			reasoningBuilder.WriteString(envelope.Delta)
		case "response.function_call_arguments.delta":
			call := ensureToolCallByID(toolCallMap, &toolCallOrder, envelope.ItemID)
			call.ArgsText += envelope.Delta
		case "response.function_call_arguments.done":
			call := ensureToolCallByID(toolCallMap, &toolCallOrder, envelope.ItemID)
			call.ArgsText = envelope.Arguments
		case "response.output_item.added", "response.output_item.done":
			switch envelope.Item.Type {
			case "function_call":
				call := ensureToolCallByID(toolCallMap, &toolCallOrder, envelope.Item.ID)
				call.ID = firstNonEmpty(envelope.Item.CallID, envelope.Item.ID)
				call.Type = "function"
				call.Name = envelope.Item.Name
				if envelope.Item.Arguments != "" {
					call.ArgsText = envelope.Item.Arguments
				}
			case "web_search_call", "file_search_call", "computer_call", "mcp_call", "custom_tool_call":
				call := ensureToolCallByID(toolCallMap, &toolCallOrder, envelope.Item.ID)
				call.ID = firstNonEmpty(envelope.Item.CallID, envelope.Item.ID)
				call.Type = envelope.Item.Type
				call.Name = firstNonEmpty(envelope.Item.Name, envelope.Item.Type)
				call.ArgsText = marshalCompactString(envelope.Item)
			case "message":
				if contentBuilder.Len() == 0 {
					for _, part := range envelope.Item.Content {
						text := responseContentText(OpenAIResponsesContentPart{
							Type:       part.Type,
							Text:       part.Text,
							InputText:  part.InputText,
							OutputText: part.OutputText,
							Refusal:    part.Refusal,
						})
						if text != "" {
							contentBuilder.WriteString(text)
						}
					}
				}
			case "reasoning":
				if reasoningBuilder.Len() == 0 {
					for _, part := range envelope.Item.Content {
						text := responseContentText(OpenAIResponsesContentPart{
							Type:       part.Type,
							Text:       part.Text,
							InputText:  part.InputText,
							OutputText: part.OutputText,
							Refusal:    part.Refusal,
						})
						if text != "" {
							reasoningBuilder.WriteString(text)
						}
					}
				}
			}
		}
	}

	toolCalls := make([]LLMToolCall, 0, len(toolCallOrder))
	for _, id := range toolCallOrder {
		call := toolCallMap[id]
		call.Args = parseJSONObject(call.ArgsText)
		toolCalls = append(toolCalls, *call)
	}

	candidate := LLMCandidate{
		Index:     0,
		Role:      "assistant",
		ToolCalls: toolCalls,
	}
	if contentBuilder.Len() > 0 {
		candidate.Content = append(candidate.Content, LLMContent{Type: "text", Text: contentBuilder.String()})
	}
	if reasoningBuilder.Len() > 0 {
		candidate.Content = append(candidate.Content, LLMContent{Type: "thinking", Text: reasoningBuilder.String()})
	}
	if refusalBuilder.Len() > 0 {
		candidate.Refusal = &LLMRefusal{
			Reason:  "refusal",
			Message: refusalBuilder.String(),
		}
	}

	resp := LLMResponse{Candidates: []LLMCandidate{candidate}}
	if len(streamError) > 0 {
		resp.Extensions = map[string]any{"error": streamError}
		resp.Candidates[0].FinishReason = "error"
	}
	return resp, nil
}

func (a googleGenerateContentAdapter) ParseStreamResponse(body []byte) (LLMResponse, error) {
	return parseGenerateContentStreamResponse(body, parseGoogleStreamError)
}

func (a vertexGenerateContentAdapter) ParseStreamResponse(body []byte) (LLMResponse, error) {
	return parseGenerateContentStreamResponse(body, parseVertexStreamError)
}

func parseGenerateContentStreamResponse(body []byte, parseStreamError func(string) (map[string]any, bool)) (LLMResponse, error) {
	var (
		contentBuilder strings.Builder
		role           = "model"
		safetyAll      []LLMSafetyRating
		promptFeedback map[string]any
		streamError    map[string]any
	)

	scanner := newSSEScanner(body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		if payload, ok := parseStreamError(jsonStr); ok {
			streamError = payload
			continue
		}

		var chunk GeminiResponse
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}
		for _, candidate := range chunk.Candidates {
			if candidate.Content.Role != "" {
				role = candidate.Content.Role
			}
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					contentBuilder.WriteString(part.Text)
				}
			}
			for _, rating := range candidate.SafetyRatings {
				safetyAll = append(safetyAll, LLMSafetyRating{
					Category:    rating.Category,
					Probability: rating.Probability,
					Blocked:     rating.Blocked,
				})
			}
		}
		if len(chunk.PromptFeedback) > 0 {
			promptFeedback = chunk.PromptFeedback
		}
	}

	resp := singleCandidateResponse(contentBuilder.String(), "", nil, streamError)
	if len(resp.Candidates) > 0 {
		resp.Candidates[0].Role = role
		if len(safetyAll) > 0 {
			resp.Candidates[0].Extensions = map[string]any{
				"safety_ratings": safetyAll,
			}
			resp.Safety = append(resp.Safety, safetyAll...)
		}
	}
	if len(promptFeedback) > 0 {
		resp.Extensions = map[string]any{
			"prompt_feedback": promptFeedback,
		}
		if resp.Candidates[0].Refusal == nil {
			resp.Candidates[0].Refusal = geminiPromptFeedbackRefusal(promptFeedback)
		}
	}
	return resp, nil
}

func newSSEScanner(body []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	return scanner
}

func singleCandidateResponse(content string, reasoning string, toolCalls []LLMToolCall, streamError map[string]any) LLMResponse {
	candidate := LLMCandidate{
		Index:     0,
		Role:      "assistant",
		ToolCalls: toolCalls,
	}
	if content != "" {
		candidate.Content = append(candidate.Content, LLMContent{Type: "text", Text: content})
	}
	if reasoning != "" {
		candidate.Content = append(candidate.Content, LLMContent{Type: "thinking", Text: reasoning})
	}
	resp := LLMResponse{Candidates: []LLMCandidate{candidate}}
	if len(streamError) > 0 {
		resp.Extensions = map[string]any{"error": streamError}
		resp.Candidates[0].FinishReason = "error"
	}
	return resp
}

func candidateText(candidate LLMCandidate) string {
	var parts []string
	for _, content := range candidate.Content {
		if content.Type == "text" && content.Text != "" {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func candidateReasoning(candidate LLMCandidate) string {
	var parts []string
	for _, content := range candidate.Content {
		if content.Type == "thinking" && content.Text != "" {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func ensureToolCallByIndex(toolCalls map[int]*LLMToolCall, idx int) *LLMToolCall {
	if call, ok := toolCalls[idx]; ok {
		return call
	}
	call := &LLMToolCall{}
	toolCalls[idx] = call
	return call
}

func flattenToolCalls(toolCalls map[int]*LLMToolCall) []LLMToolCall {
	indexes := make([]int, 0, len(toolCalls))
	for idx := range toolCalls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	result := make([]LLMToolCall, 0, len(indexes))
	for _, idx := range indexes {
		call := toolCalls[idx]
		call.Args = parseJSONObject(call.ArgsText)
		result = append(result, *call)
	}
	return result
}

func ensureToolCallByID(toolCalls map[string]*LLMToolCall, order *[]string, id string) *LLMToolCall {
	if id == "" {
		id = "toolcall"
	}
	if call, ok := toolCalls[id]; ok {
		return call
	}
	call := &LLMToolCall{}
	toolCalls[id] = call
	*order = append(*order, id)
	return call
}

func ensureAnthropicBlock(blocks map[int]*anthropicStreamBlockState, order *[]int, idx int) *anthropicStreamBlockState {
	if block, ok := blocks[idx]; ok {
		return block
	}
	block := &anthropicStreamBlockState{}
	blocks[idx] = block
	*order = append(*order, idx)
	return block
}
