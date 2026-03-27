package monitor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

// ParsedData 提供给 UI 的完整数据结构
type ParsedData struct {
	Header recordfile.RecordHeader
	Events []recordfile.RecordEvent
	// Raw Full Content (Header + Body)
	ReqFull string
	ResFull string

	// Parsed Info
	ChatMessages      []ChatMessage
	AIContent         string
	AIReasoning       string
	ResponseToolCalls []ToolCall
}

type ChatMessage struct {
	Role          string     `json:"role"`
	MessageType   string     `json:"message_type,omitempty"`
	Content       string     `json:"content"`
	ContentFormat string     `json:"content_format,omitempty"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID    string     `json:"tool_call_id,omitempty"` // 当 role=tool 时存在
	Name          string     `json:"name,omitempty"`         // 当 role=tool 时可能是函数名
}
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON String
	} `json:"function"`
}
type chatRequest struct {
	Messages []ChatMessage `json:"messages"`
	// Embedding / Rerank 字段兼容
	Input     interface{} `json:"input"`     // Embedding: string or []string
	Query     string      `json:"query"`     // Reranker
	Documents []string    `json:"documents"` // Reranker
}

type responsesRequest struct {
	Input interface{} `json:"input"`
}

type responsesInputItem struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Role      string                 `json:"role"`
	Content   []responsesContentPart `json:"content"`
	Arguments string                 `json:"arguments"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Output    interface{}            `json:"output"`
}

type responsesContentPart struct {
	Type     string      `json:"type"`
	Text     string      `json:"text"`
	Input    string      `json:"input_text"`
	Output   string      `json:"output_text"`
	Refusal  string      `json:"refusal"`
	ImageURL string      `json:"image_url"`
	FileID   string      `json:"file_id"`
	Data     interface{} `json:"data"`
}

type responsesOutputItem struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Role      string                 `json:"role"`
	Content   []responsesContentPart `json:"content"`
	Arguments string                 `json:"arguments"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Output    interface{}            `json:"output"`
	Status    string                 `json:"status"`
}

// ParseLogFile 解析 V2/V3 格式的日志文件
func ParseLogFile(content []byte) (*ParsedData, error) {
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return nil, fmt.Errorf("invalid header: %w", err)
	}
	header := parsed.Header
	reqFullBytes, reqBodyBytes, resFullBytes, resBodyBytes := recordfile.ExtractSections(content, parsed)

	// 4. 解析 Request (自适应 Chat / Embedding / Reranker)
	messages := parseRequestMessages(header.Meta.URL, reqBodyBytes)

	// 5. 解析 AI Content & Reasoning
	contentStr, reasoningStr, toolCalls := parseAIContent(header.Meta.URL, resBodyBytes, header.Layout.IsStream)

	return &ParsedData{
		Header:            header,
		Events:            parsed.Events,
		ReqFull:           string(reqFullBytes),
		ResFull:           string(resFullBytes),
		ChatMessages:      messages,
		AIContent:         contentStr,
		AIReasoning:       reasoningStr,
		ResponseToolCalls: toolCalls,
		// ReqBody:      string(reqBodyBytes), // 也可以加上
		// ResBody:      string(resBodyBytes), // 也可以加上
	}, nil
}

func parseRequestMessages(url string, reqBodyBytes []byte) []ChatMessage {
	if strings.Contains(url, "/v1/responses") {
		if messages := parseResponsesRequest(reqBodyBytes); len(messages) > 0 {
			return messages
		}
	}

	var reqRaw chatRequest
	var messages []ChatMessage
	if json.Unmarshal(reqBodyBytes, &reqRaw) == nil {
		if len(reqRaw.Messages) > 0 {
			return reqRaw.Messages
		}
		if reqRaw.Input != nil {
			contentStr := formatInput(reqRaw.Input)
			return []ChatMessage{{
				Role:    "user",
				Content: fmt.Sprintf("🧮 **Embedding Input**:\n%s", contentStr),
			}}
		}
		if reqRaw.Query != "" {
			docList := strings.Join(reqRaw.Documents, "\n- ")
			content := fmt.Sprintf("🔍 **Rerank Query**: %s\n\n📄 **Documents**:\n- %s", reqRaw.Query, docList)
			return []ChatMessage{{
				Role:    "user",
				Content: content,
			}}
		}
	}

	return messages
}

func parseResponsesRequest(data []byte) []ChatMessage {
	var req responsesRequest
	if err := json.Unmarshal(data, &req); err != nil || req.Input == nil {
		return nil
	}

	switch v := req.Input.(type) {
	case string:
		return []ChatMessage{{Role: "user", Content: v}}
	case []interface{}:
		var messages []ChatMessage
		for _, item := range v {
			msg, ok := parseResponsesInputMessage(item)
			if ok {
				messages = append(messages, msg)
			}
		}
		return messages
	default:
		return []ChatMessage{{
			Role:    "user",
			Content: formatInput(req.Input),
		}}
	}
}

func parseResponsesInputMessage(raw interface{}) (ChatMessage, bool) {
	itemBytes, err := json.Marshal(raw)
	if err != nil {
		return ChatMessage{}, false
	}

	var item responsesInputItem
	if err := json.Unmarshal(itemBytes, &item); err != nil {
		return ChatMessage{}, false
	}

	switch item.Type {
	case "", "message":
		content := renderResponsesContent(item.Content)
		if content == "" {
			return ChatMessage{}, false
		}
		role := item.Role
		if role == "" {
			role = "user"
		}
		return ChatMessage{
			Role:          role,
			MessageType:   "message",
			Content:       content,
			ContentFormat: detectContentFormat(content),
		}, true
	case "function_call":
		return ChatMessage{
			Role:        "assistant",
			MessageType: "function_call",
			ToolCalls: []ToolCall{{
				ID:   firstNonEmpty(item.CallID, item.ID),
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			}},
		}, true
	case "function_call_output":
		output := renderResponseOutput(item.Output)
		return ChatMessage{
			Role:          "tool",
			MessageType:   "function_call_output",
			ToolCallID:    item.CallID,
			Name:          item.Name,
			Content:       output,
			ContentFormat: detectContentFormat(output),
		}, true
	default:
		content := renderResponseOutput(raw)
		if content == "" {
			return ChatMessage{}, false
		}
		return ChatMessage{
			Role:          firstNonEmpty(item.Role, "user"),
			MessageType:   firstNonEmpty(item.Type, "message"),
			Content:       content,
			ContentFormat: detectContentFormat(content),
		}, true
	}
}

func parseAIContent(url string, data []byte, isStream bool) (string, string, []ToolCall) {
	if strings.Contains(url, "/v1/responses") {
		return parseResponsesOutput(data, isStream)
	}
	return parseChatCompletionsOutput(data, isStream)
}

func parseChatCompletionsOutput(data []byte, isStream bool) (string, string, []ToolCall) {
	if len(data) == 0 {
		return "", "", nil
	}

	if !isStream {
		var resp struct {
			Choices []struct {
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &resp) == nil && len(resp.Choices) > 0 {
			return resp.Choices[0].Message.Content, resp.Choices[0].Message.ReasoningContent, nil
		}
		return "", "", nil
	}

	// Stream Logic
	var contentBuilder, reasoningBuilder strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// 增大 Buffer 防止单行过长
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data:")
		if strings.TrimSpace(jsonStr) == "[DONE]" {
			continue
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          *string `json:"content"`
					ReasoningContent *string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(jsonStr), &chunk) == nil && len(chunk.Choices) > 0 {
			if c := chunk.Choices[0].Delta.Content; c != nil {
				contentBuilder.WriteString(*c)
			}
			if r := chunk.Choices[0].Delta.ReasoningContent; r != nil {
				reasoningBuilder.WriteString(*r)
			}
		}
	}
	return contentBuilder.String(), reasoningBuilder.String(), nil
}

func parseResponsesOutput(data []byte, isStream bool) (string, string, []ToolCall) {
	if len(data) == 0 {
		return "", "", nil
	}
	if !isStream {
		return parseResponsesJSONOutput(data)
	}
	return parseResponsesStreamOutput(data)
}

func parseResponsesJSONOutput(data []byte) (string, string, []ToolCall) {
	var resp struct {
		Output []responsesOutputItem `json:"output"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", nil
	}

	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		toolCalls        []ToolCall
	)
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			text := renderResponsesContent(item.Content)
			if text != "" {
				contentBuilder.WriteString(text)
			}
		case "reasoning":
			text := renderResponsesContent(item.Content)
			if text != "" {
				reasoningBuilder.WriteString(text)
			}
		case "function_call":
			toolCalls = append(toolCalls, ToolCall{
				ID:   firstNonEmpty(item.CallID, item.ID),
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}
	return contentBuilder.String(), reasoningBuilder.String(), toolCalls
}

func parseResponsesStreamOutput(data []byte) (string, string, []ToolCall) {
	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		toolCallMap      = map[string]*ToolCall{}
		toolCallOrder    []string
	)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}

		var envelope struct {
			Type      string              `json:"type"`
			Delta     string              `json:"delta"`
			Arguments string              `json:"arguments"`
			ItemID    string              `json:"item_id"`
			Item      responsesOutputItem `json:"item"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "response.output_text.delta":
			contentBuilder.WriteString(envelope.Delta)
		case "response.reasoning_summary_text.delta":
			reasoningBuilder.WriteString(envelope.Delta)
		case "response.function_call_arguments.delta":
			tc := ensureToolCall(toolCallMap, &toolCallOrder, envelope.ItemID)
			tc.Function.Arguments += envelope.Delta
		case "response.function_call_arguments.done":
			tc := ensureToolCall(toolCallMap, &toolCallOrder, envelope.ItemID)
			tc.Function.Arguments = envelope.Arguments
		case "response.output_item.added", "response.output_item.done":
			switch envelope.Item.Type {
			case "function_call":
				tc := ensureToolCall(toolCallMap, &toolCallOrder, envelope.Item.ID)
				tc.ID = firstNonEmpty(envelope.Item.CallID, envelope.Item.ID)
				tc.Type = "function"
				tc.Function.Name = envelope.Item.Name
				if envelope.Item.Arguments != "" {
					tc.Function.Arguments = envelope.Item.Arguments
				}
			case "message":
				text := renderResponsesContent(envelope.Item.Content)
				if text != "" && contentBuilder.Len() == 0 {
					contentBuilder.WriteString(text)
				}
			case "reasoning":
				text := renderResponsesContent(envelope.Item.Content)
				if text != "" && reasoningBuilder.Len() == 0 {
					reasoningBuilder.WriteString(text)
				}
			}
		}
	}

	toolCalls := make([]ToolCall, 0, len(toolCallOrder))
	for _, id := range toolCallOrder {
		if tc := toolCallMap[id]; tc != nil {
			toolCalls = append(toolCalls, *tc)
		}
	}
	return contentBuilder.String(), reasoningBuilder.String(), toolCalls
}

func ensureToolCall(toolCallMap map[string]*ToolCall, order *[]string, itemID string) *ToolCall {
	id := itemID
	if id == "" {
		id = fmt.Sprintf("toolcall-%d", len(toolCallMap)+1)
	}
	if tc, ok := toolCallMap[id]; ok {
		return tc
	}
	tc := &ToolCall{ID: id, Type: "function"}
	toolCallMap[id] = tc
	*order = append(*order, id)
	return tc
}

func renderResponsesContent(parts []responsesContentPart) string {
	if len(parts) == 0 {
		return ""
	}

	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(firstNonEmpty(part.Text, part.Input, part.Output, part.Refusal))
		if text != "" {
			segments = append(segments, text)
			continue
		}

		switch part.Type {
		case "input_image", "output_image":
			meta := map[string]string{}
			if part.ImageURL != "" {
				meta["image_url"] = part.ImageURL
			}
			if part.FileID != "" {
				meta["file_id"] = part.FileID
			}
			if len(meta) > 0 {
				segments = append(segments, "```json\n"+marshalPretty(meta)+"\n```")
			}
		default:
			if part.Data != nil {
				segments = append(segments, "```json\n"+marshalPretty(part.Data)+"\n```")
			}
		}
	}

	return strings.Join(segments, "\n\n")
}

func renderResponseOutput(v interface{}) string {
	switch out := v.(type) {
	case nil:
		return ""
	case string:
		return out
	default:
		return marshalPretty(out)
	}
}

func detectContentFormat(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		var payload interface{}
		if json.Unmarshal([]byte(trimmed), &payload) == nil {
			return "json"
		}
	}
	return "markdown"
}

func marshalPretty(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func formatInput(input interface{}) string {
	switch v := input.(type) {
	case string:
		return v
	case []interface{}:
		stringParts := make([]string, 0, len(v))
		otherParts := make([]string, 0)
		for _, item := range v {
			if s, ok := item.(string); ok {
				stringParts = append(stringParts, "- "+s)
				continue
			}
			otherParts = append(otherParts, marshalPretty(item))
		}
		parts := append(stringParts, otherParts...)
		return strings.Join(parts, "\n")
	default:
		return marshalPretty(input)
	}
}
