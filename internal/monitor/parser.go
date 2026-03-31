package monitor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/llm"
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
	RequestTools      []RequestTool
	OpenAITools       []RequestTool
	AnthropicTools    []RequestTool
	AIContent         string
	AIReasoning       string
	AIBlocks          []ContentBlock
	ResponseToolCalls []ToolCall
}

type ChatMessage struct {
	Role          string         `json:"role"`
	MessageType   string         `json:"message_type,omitempty"`
	Content       string         `json:"content"`
	ContentFormat string         `json:"content_format,omitempty"`
	Blocks        []ContentBlock `json:"blocks,omitempty"`
	ToolCalls     []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID    string         `json:"tool_call_id,omitempty"` // 当 role=tool 时存在
	Name          string         `json:"name,omitempty"`         // 当 role=tool 时可能是函数名
	IsError       bool           `json:"is_error,omitempty"`
}

type ContentBlock struct {
	Kind   string `json:"kind"`
	Title  string `json:"title,omitempty"`
	Text   string `json:"text,omitempty"`
	Format string `json:"format,omitempty"`
	Meta   string `json:"meta,omitempty"`
	URL    string `json:"url,omitempty"`
	FileID string `json:"file_id,omitempty"`
}
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON String
	} `json:"function"`
}

type RequestTool struct {
	Source      string `json:"source,omitempty"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  string `json:"parameters,omitempty"`
}
type chatRequest struct {
	// Embedding / Rerank 字段兼容
	Input     interface{} `json:"input"`     // Embedding: string or []string
	Query     string      `json:"query"`     // Reranker
	Documents []string    `json:"documents"` // Reranker
}

type openAIChatRequest struct {
	Messages []openAIChatRequestMessage `json:"messages"`
	Tools    []rawRequestTool           `json:"tools"`
}

type openAIChatRequestMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"`
	ToolCalls  []ToolCall  `json:"tool_calls"`
	ToolCallID string      `json:"tool_call_id"`
	Name       string      `json:"name"`
}

type rawRequestTool struct {
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
	InputSchema interface{} `json:"input_schema"`
	Function    struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Parameters  interface{} `json:"parameters"`
	} `json:"function"`
}

type responsesRequest struct {
	Input interface{} `json:"input"`
}

type anthropicRequest struct {
	System   interface{}               `json:"system"`
	Messages []anthropicRequestMessage `json:"messages"`
}

type anthropicRequestMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text"`
	Thinking  string      `json:"thinking"`
	Data      string      `json:"data"`
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Input     interface{} `json:"input"`
	ToolUseID string      `json:"tool_use_id"`
	Content   interface{} `json:"content"`
	IsError   bool        `json:"is_error"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
}

type anthropicStreamChunk struct {
	Type         string                `json:"type"`
	Index        int                   `json:"index"`
	Delta        anthropicDelta        `json:"delta"`
	ContentBlock anthropicContentBlock `json:"content_block"`
}

type anthropicStreamBlock struct {
	Type  string
	ID    string
	Name  string
	Text  strings.Builder
	Input strings.Builder
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
	endpoint := firstNonEmpty(header.Meta.Endpoint, header.Meta.URL)

	messages := parseRequestMessagesViaAdapter(header.Meta.Provider, endpoint, reqBodyBytes)
	requestTools := parseRequestToolsViaAdapter(header.Meta.Provider, endpoint, reqBodyBytes)
	if len(messages) == 0 {
		messages = parseRequestMessages(header.Meta.URL, reqBodyBytes)
	}
	if len(requestTools) == 0 {
		requestTools = parseRequestTools(reqBodyBytes)
	}
	openAITools, anthropicTools := splitRequestToolsBySource(requestTools)

	contentStr, reasoningStr, aiBlocks, toolCalls := parseResponseViaAdapter(header.Meta.Provider, endpoint, resBodyBytes, header.Layout.IsStream)
	if contentStr == "" && reasoningStr == "" && len(aiBlocks) == 0 && len(toolCalls) == 0 {
		contentStr, reasoningStr, aiBlocks, toolCalls = parseAIContent(header.Meta.URL, resBodyBytes, header.Layout.IsStream)
	}

	return &ParsedData{
		Header:            header,
		Events:            parsed.Events,
		ReqFull:           string(reqFullBytes),
		ResFull:           string(resFullBytes),
		ChatMessages:      messages,
		RequestTools:      requestTools,
		OpenAITools:       openAITools,
		AnthropicTools:    anthropicTools,
		AIContent:         contentStr,
		AIReasoning:       reasoningStr,
		AIBlocks:          aiBlocks,
		ResponseToolCalls: toolCalls,
		// ReqBody:      string(reqBodyBytes), // 也可以加上
		// ResBody:      string(resBodyBytes), // 也可以加上
	}, nil
}

func parseRequestMessagesViaAdapter(provider string, endpoint string, reqBodyBytes []byte) []ChatMessage {
	req, err := llm.ParseRequest(provider, endpoint, reqBodyBytes)
	if err != nil {
		return nil
	}
	return chatMessagesFromLLMRequest(req, endpoint)
}

func parseRequestToolsViaAdapter(provider string, endpoint string, reqBodyBytes []byte) []RequestTool {
	req, err := llm.ParseRequest(provider, endpoint, reqBodyBytes)
	if err != nil || len(req.Tools) == 0 {
		return nil
	}
	source := provider
	if source == "" || source == llm.ProviderUnknown {
		source = llm.ClassifyPath(endpoint, "").Provider
	}
	switch source {
	case llm.ProviderOpenAICompatible:
		source = "openai"
	case llm.ProviderAnthropic:
		source = "anthropic"
	}
	if source == "" || source == llm.ProviderUnknown {
		source = "unknown"
	}
	tools := make([]RequestTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, RequestTool{
			Source:      source,
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  marshalCompact(tool.Parameters),
		})
	}
	return tools
}

func parseResponseViaAdapter(provider string, endpoint string, data []byte, isStream bool) (string, string, []ContentBlock, []ToolCall) {
	var (
		resp llm.LLMResponse
		err  error
	)
	if isStream {
		resp, err = llm.ParseStreamResponse(provider, endpoint, data)
	} else {
		resp, err = llm.ParseResponse(provider, endpoint, data)
	}
	if err != nil {
		return "", "", nil, nil
	}
	return summarizeLLMResponse(resp)
}

func summarizeLLMResponse(resp llm.LLMResponse) (string, string, []ContentBlock, []ToolCall) {
	if len(resp.Candidates) == 0 {
		return "", "", nil, nil
	}
	candidate := resp.Candidates[0]
	var (
		contentParts   []string
		reasoningParts []string
		blocks         []ContentBlock
		toolCalls      []ToolCall
	)

	for _, content := range candidate.Content {
		switch content.Type {
		case "text":
			if strings.TrimSpace(content.Text) != "" {
				contentParts = append(contentParts, content.Text)
			}
		case "thinking":
			if strings.TrimSpace(content.Text) != "" {
				reasoningParts = append(reasoningParts, content.Text)
			}
		case "tool_result":
			rendered := renderResponseOutput(content.ToolResult)
			if rendered != "" {
				blocks = append(blocks, ContentBlock{
					Kind:   "tool_result",
					Title:  firstNonEmpty(content.ToolName, "Tool Result"),
					Text:   rendered,
					Format: detectContentFormat(rendered),
				})
			}
		default:
			if strings.TrimSpace(content.Text) != "" {
				blocks = append(blocks, ContentBlock{
					Kind:   content.Type,
					Title:  humanizeKind(content.Type),
					Text:   content.Text,
					Format: detectContentFormat(content.Text),
				})
			}
		}
	}
	for _, call := range candidate.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:   firstNonEmpty(call.ID, call.Name),
			Type: firstNonEmpty(call.Type, "function"),
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      call.Name,
				Arguments: firstNonEmpty(call.ArgsText, marshalCompact(call.Args)),
			},
		})
	}
	if candidate.Refusal != nil && strings.TrimSpace(candidate.Refusal.Message) != "" {
		blocks = append(blocks, ContentBlock{
			Kind:   "refusal",
			Title:  "Refusal",
			Text:   candidate.Refusal.Message,
			Format: detectContentFormat(candidate.Refusal.Message),
		})
	}
	return strings.Join(contentParts, "\n\n"), strings.Join(reasoningParts, "\n\n"), blocks, toolCalls
}

func humanizeKind(kind string) string {
	if kind == "" {
		return "Structured Content"
	}
	kind = strings.ReplaceAll(kind, "_", " ")
	parts := strings.Fields(kind)
	for i := range parts {
		if len(parts[i]) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func normalizeEndpointLike(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	if idx := strings.Index(endpoint, "?"); idx >= 0 {
		endpoint = endpoint[:idx]
	}
	return endpoint
}

func parseRequestMessages(url string, reqBodyBytes []byte) []ChatMessage {
	if strings.Contains(url, "/v1/responses") {
		if messages := parseResponsesRequest(reqBodyBytes); len(messages) > 0 {
			return messages
		}
	}
	if strings.Contains(url, "/v1/messages") {
		if messages := parseAnthropicRequest(reqBodyBytes); len(messages) > 0 {
			return messages
		}
	}
	if messages := parseOpenAIChatRequest(reqBodyBytes); len(messages) > 0 {
		return messages
	}

	var reqRaw chatRequest
	var messages []ChatMessage
	if json.Unmarshal(reqBodyBytes, &reqRaw) == nil {
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

func chatMessagesFromLLMRequest(req llm.LLMRequest, endpoint string) []ChatMessage {
	messages := make([]ChatMessage, 0, len(req.System)+len(req.Messages))
	toolNames := map[string]string{}
	if len(req.System) > 0 {
		messages = append(messages, llmMessageToChatMessage("system", req.System, endpoint, toolNames))
	}
	for _, message := range req.Messages {
		messages = append(messages, llmMessageToChatMessage(message.Role, message.Content, endpoint, toolNames))
	}
	return compactChatMessages(messages)
}

func llmMessageToChatMessage(role string, contents []llm.LLMContent, endpoint string, toolNames map[string]string) ChatMessage {
	var (
		texts     []string
		blocks    []ContentBlock
		toolCalls []ToolCall
		msgType   = "message"
		toolID    string
		toolName  string
		isError   bool
	)

	for _, content := range contents {
		switch content.Type {
		case "text":
			if strings.TrimSpace(content.Text) != "" {
				texts = append(texts, strings.TrimSpace(content.Text))
			}
		case "thinking":
			if strings.TrimSpace(content.Text) != "" {
				blocks = append(blocks, ContentBlock{
					Kind:   "thinking",
					Title:  "Thinking",
					Text:   content.Text,
					Format: detectContentFormat(content.Text),
				})
			}
		case "tool_use":
			msgType = toolUseMessageType(endpoint)
			if toolID := firstNonEmpty(content.ToolCallID, content.ID); toolID != "" && content.ToolName != "" {
				toolNames[toolID] = content.ToolName
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   firstNonEmpty(content.ToolCallID, content.ID),
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      content.ToolName,
					Arguments: marshalCompact(content.ToolArgs),
				},
			})
		case "tool_result":
			msgType = toolResultMessageType(endpoint)
			toolID = content.ToolCallID
			toolName = firstNonEmpty(content.ToolName, toolNames[content.ToolCallID])
			rendered := strings.TrimSpace(content.Text)
			if rendered == "" {
				rendered = renderToolResult(content.ToolResult)
			}
			if rendered != "" {
				texts = append(texts, rendered)
			}
			if content.Refusal != "" {
				isError = true
			}
			if content.Text == "" && content.ToolResult != nil {
				blocks = append(blocks, ContentBlock{
					Kind:   "attachment",
					Title:  "Tool Result",
					Meta:   marshalPretty(content.ToolResult),
					Format: "json",
				})
			}
		default:
			if strings.TrimSpace(content.Text) != "" {
				blocks = append(blocks, ContentBlock{
					Kind:   content.Type,
					Title:  strings.ReplaceAll(strings.Title(content.Type), "_", " "),
					Text:   content.Text,
					Format: detectContentFormat(content.Text),
				})
			}
		}
	}

	content := strings.Join(texts, "\n\n")
	return ChatMessage{
		Role:          normalizeMessageRole(firstNonEmpty(role, "user"), msgType),
		MessageType:   msgType,
		Content:       content,
		ContentFormat: detectContentFormat(content),
		Blocks:        blocks,
		ToolCalls:     toolCalls,
		ToolCallID:    toolID,
		Name:          toolName,
		IsError:       isError,
	}
}

func normalizeMessageRole(role string, msgType string) string {
	switch msgType {
	case "tool_result", "function_call_output":
		return "tool"
	default:
		return role
	}
}

func toolUseMessageType(endpoint string) string {
	if normalizeEndpointLike(endpoint) == "/v1/responses" {
		return "function_call"
	}
	return "tool_use"
}

func toolResultMessageType(endpoint string) string {
	if normalizeEndpointLike(endpoint) == "/v1/responses" {
		return "function_call_output"
	}
	return "tool_result"
}

func renderToolResult(v interface{}) string {
	switch out := v.(type) {
	case nil:
		return ""
	case map[string]any:
		if value, ok := out["value"].(string); ok && len(out) == 1 {
			return value
		}
		return marshalPretty(out)
	default:
		return renderResponseOutput(out)
	}
}

func compactChatMessages(messages []ChatMessage) []ChatMessage {
	result := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.Content == "" && len(message.Blocks) == 0 && len(message.ToolCalls) == 0 {
			continue
		}
		result = append(result, message)
	}
	return result
}

func parseOpenAIChatRequest(data []byte) []ChatMessage {
	var req openAIChatRequest
	if err := json.Unmarshal(data, &req); err != nil || len(req.Messages) == 0 {
		return nil
	}

	messages := make([]ChatMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := firstNonEmpty(message.Role, "user")
		content, blocks := renderOpenAIChatContent(message.Content)
		if content == "" && len(blocks) == 0 && len(message.ToolCalls) == 0 {
			continue
		}

		msgType := "message"
		if len(message.ToolCalls) > 0 {
			msgType = "tool_use"
		}

		messages = append(messages, ChatMessage{
			Role:          role,
			MessageType:   msgType,
			Content:       content,
			ContentFormat: detectContentFormat(content),
			Blocks:        blocks,
			ToolCalls:     append([]ToolCall(nil), message.ToolCalls...),
			ToolCallID:    message.ToolCallID,
			Name:          message.Name,
		})
	}
	return messages
}

func parseRequestTools(data []byte) []RequestTool {
	var req struct {
		Tools []rawRequestTool `json:"tools"`
	}
	if err := json.Unmarshal(data, &req); err != nil || len(req.Tools) == 0 {
		return nil
	}

	tools := make([]RequestTool, 0, len(req.Tools))
	for _, raw := range req.Tools {
		name := firstNonEmpty(raw.Function.Name, raw.Name)
		description := firstNonEmpty(raw.Function.Description, raw.Description)

		parameters := raw.Function.Parameters
		if parameters == nil {
			parameters = raw.Parameters
		}
		if parameters == nil {
			parameters = raw.InputSchema
		}

		source := "openai"
		if raw.InputSchema != nil || (raw.Function.Name == "" && raw.Name != "") {
			source = "anthropic"
		}

		tools = append(tools, RequestTool{
			Source:      source,
			Type:        firstNonEmpty(raw.Type, "function"),
			Name:        name,
			Description: description,
			Parameters:  marshalNonEmptyAnthropicMeta(parameters),
		})
	}
	return tools
}

func splitRequestToolsBySource(tools []RequestTool) ([]RequestTool, []RequestTool) {
	if len(tools) == 0 {
		return nil, nil
	}

	openAITools := make([]RequestTool, 0, len(tools))
	anthropicTools := make([]RequestTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Source == "anthropic" {
			anthropicTools = append(anthropicTools, tool)
			continue
		}
		openAITools = append(openAITools, tool)
	}
	return openAITools, anthropicTools
}

func parseAnthropicRequest(data []byte) []ChatMessage {
	var req anthropicRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil
	}

	toolNames := map[string]string{}
	var messages []ChatMessage
	messages = append(messages, parseAnthropicSystem(req.System)...)
	for _, message := range req.Messages {
		messages = append(messages, parseAnthropicRequestMessage(message, toolNames)...)
	}
	return messages
}

func parseAnthropicSystem(raw interface{}) []ChatMessage {
	content, blocks := renderAnthropicRichContent(raw)
	if content == "" {
		if len(blocks) == 0 {
			return nil
		}
	}
	return []ChatMessage{{
		Role:          "system",
		MessageType:   "message",
		Content:       content,
		ContentFormat: detectContentFormat(content),
		Blocks:        blocks,
	}}
}

func parseAnthropicRequestMessage(message anthropicRequestMessage, toolNames map[string]string) []ChatMessage {
	role := firstNonEmpty(message.Role, "user")

	switch content := message.Content.(type) {
	case string:
		if strings.TrimSpace(content) == "" {
			return nil
		}
		return []ChatMessage{{
			Role:          role,
			MessageType:   "message",
			Content:       content,
			ContentFormat: detectContentFormat(content),
		}}
	case []interface{}:
		return parseAnthropicBlocks(role, content, toolNames)
	default:
		rendered, blocks := renderAnthropicRichContent(content)
		if rendered == "" && len(blocks) == 0 {
			return nil
		}
		return []ChatMessage{{
			Role:          role,
			MessageType:   "message",
			Content:       rendered,
			ContentFormat: detectContentFormat(rendered),
			Blocks:        blocks,
		}}
	}
}

func parseAnthropicBlocks(role string, rawBlocks []interface{}, toolNames map[string]string) []ChatMessage {
	var (
		messages  []ChatMessage
		textParts []string
		blocks    []ContentBlock
		toolCalls []ToolCall
	)

	flush := func() {
		content := strings.Join(textParts, "\n\n")
		if content == "" && len(toolCalls) == 0 && len(blocks) == 0 {
			return
		}
		msgType := "message"
		if len(toolCalls) > 0 {
			msgType = "tool_use"
		}
		messages = append(messages, ChatMessage{
			Role:          role,
			MessageType:   msgType,
			Content:       content,
			ContentFormat: detectContentFormat(content),
			Blocks:        append([]ContentBlock(nil), blocks...),
			ToolCalls:     append([]ToolCall(nil), toolCalls...),
		})
		textParts = nil
		blocks = nil
		toolCalls = nil
	}

	for _, raw := range rawBlocks {
		blockBytes, err := json.Marshal(raw)
		if err != nil {
			continue
		}

		var block anthropicContentBlock
		if err := json.Unmarshal(blockBytes, &block); err != nil {
			continue
		}

		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				textParts = append(textParts, text)
			}
		case "thinking":
			if thinking := strings.TrimSpace(firstNonEmpty(block.Thinking, block.Text)); thinking != "" {
				blocks = append(blocks, ContentBlock{
					Kind:   "thinking",
					Title:  "Thinking",
					Text:   thinking,
					Format: detectContentFormat(thinking),
				})
			}
		case "tool_use":
			if block.ID != "" && block.Name != "" {
				toolNames[block.ID] = block.Name
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: marshalCompact(block.Input),
				},
			})
		case "tool_result":
			flush()
			content, resultBlocks := renderAnthropicToolResult(block)
			messages = append(messages, ChatMessage{
				Role:          "tool",
				MessageType:   "tool_result",
				ToolCallID:    block.ToolUseID,
				Name:          toolNames[block.ToolUseID],
				Content:       content,
				ContentFormat: detectContentFormat(content),
				Blocks:        resultBlocks,
				IsError:       block.IsError,
			})
		default:
			appendAnthropicBlock(&textParts, &blocks, block)
		}
	}

	flush()
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

func parseAIContent(url string, data []byte, isStream bool) (string, string, []ContentBlock, []ToolCall) {
	if strings.Contains(url, "/v1/responses") {
		return parseResponsesOutput(data, isStream)
	}
	if strings.Contains(url, "/v1/messages") {
		return parseAnthropicOutput(data, isStream)
	}
	content, reasoning, toolCalls := parseChatCompletionsOutput(data, isStream)
	return content, reasoning, nil, toolCalls
}

func parseAnthropicOutput(data []byte, isStream bool) (string, string, []ContentBlock, []ToolCall) {
	if len(data) == 0 {
		return "", "", nil, nil
	}
	if !isStream {
		return parseAnthropicJSONOutput(data)
	}
	return parseAnthropicStreamOutput(data)
}

func parseAnthropicJSONOutput(data []byte) (string, string, []ContentBlock, []ToolCall) {
	var resp struct {
		Content []anthropicContentBlock `json:"content"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", nil, nil
	}

	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		blocks           []ContentBlock
		toolCalls        []ToolCall
	)
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			contentBuilder.WriteString(block.Text)
		case "thinking":
			thinking := firstNonEmpty(block.Thinking, block.Text)
			reasoningBuilder.WriteString(thinking)
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: marshalCompact(block.Input),
				},
			})
		default:
			appendAnthropicBlock(nil, &blocks, block)
		}
	}
	return contentBuilder.String(), reasoningBuilder.String(), blocks, toolCalls
}

func parseAnthropicStreamOutput(data []byte) (string, string, []ContentBlock, []ToolCall) {
	var (
		blockMap   = map[int]*anthropicStreamBlock{}
		blockOrder []int
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

		var chunk anthropicStreamChunk
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}

		block := ensureAnthropicStreamBlock(blockMap, &blockOrder, chunk.Index)
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
				block.Input.WriteString(marshalCompact(chunk.ContentBlock.Input))
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

	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		blocks           []ContentBlock
		toolCalls        []ToolCall
	)
	for _, idx := range blockOrder {
		block := blockMap[idx]
		if block == nil {
			continue
		}
		switch block.Type {
		case "text":
			contentBuilder.WriteString(block.Text.String())
		case "thinking":
			reasoning := block.Text.String()
			reasoningBuilder.WriteString(reasoning)
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: block.Input.String(),
				},
			})
		}
	}
	return contentBuilder.String(), reasoningBuilder.String(), blocks, toolCalls
}

func ensureAnthropicStreamBlock(blockMap map[int]*anthropicStreamBlock, order *[]int, idx int) *anthropicStreamBlock {
	if block, ok := blockMap[idx]; ok {
		return block
	}
	block := &anthropicStreamBlock{}
	blockMap[idx] = block
	*order = append(*order, idx)
	return block
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

func parseResponsesOutput(data []byte, isStream bool) (string, string, []ContentBlock, []ToolCall) {
	if len(data) == 0 {
		return "", "", nil, nil
	}
	if !isStream {
		return parseResponsesJSONOutput(data)
	}
	return parseResponsesStreamOutput(data)
}

func parseResponsesJSONOutput(data []byte) (string, string, []ContentBlock, []ToolCall) {
	var resp struct {
		Output []responsesOutputItem `json:"output"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", nil, nil
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
	return contentBuilder.String(), reasoningBuilder.String(), nil, toolCalls
}

func parseResponsesStreamOutput(data []byte) (string, string, []ContentBlock, []ToolCall) {
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
	return contentBuilder.String(), reasoningBuilder.String(), nil, toolCalls
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

func renderOpenAIChatContent(raw interface{}) (string, []ContentBlock) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	case []interface{}:
		var (
			parts  []string
			blocks []ContentBlock
		)
		for _, item := range v {
			itemBytes, err := json.Marshal(item)
			if err != nil {
				continue
			}

			var part responsesContentPart
			if err := json.Unmarshal(itemBytes, &part); err != nil {
				continue
			}

			text := strings.TrimSpace(firstNonEmpty(part.Text, part.Input, part.Output, part.Refusal))
			if text != "" {
				parts = append(parts, text)
				continue
			}

			switch part.Type {
			case "image_url", "input_image", "output_image":
				meta := marshalNonEmptyAnthropicMeta(item)
				blocks = append(blocks, ContentBlock{
					Kind:   "image",
					Title:  "Image",
					Meta:   meta,
					Format: "json",
				})
			case "file", "input_file", "output_file":
				meta := marshalNonEmptyAnthropicMeta(item)
				blocks = append(blocks, ContentBlock{
					Kind:   "attachment",
					Title:  "Attachment",
					Meta:   meta,
					Format: "json",
				})
			default:
				meta := marshalNonEmptyAnthropicMeta(item)
				if meta != "" {
					blocks = append(blocks, ContentBlock{
						Kind:   "attachment",
						Title:  "Structured Content",
						Meta:   meta,
						Format: "json",
					})
				}
			}
		}
		return strings.Join(parts, "\n\n"), blocks
	default:
		return "", []ContentBlock{{
			Kind:   "attachment",
			Title:  "Structured Content",
			Meta:   marshalPretty(v),
			Format: "json",
		}}
	}
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

func appendAnthropicBlock(textParts *[]string, blocks *[]ContentBlock, block anthropicContentBlock) {
	switch block.Type {
	case "text":
		if textParts != nil {
			if text := strings.TrimSpace(block.Text); text != "" {
				*textParts = append(*textParts, text)
			}
		}
	case "thinking":
		thinking := strings.TrimSpace(firstNonEmpty(block.Thinking, block.Text))
		if thinking != "" && blocks != nil {
			*blocks = append(*blocks, ContentBlock{
				Kind:   "thinking",
				Title:  "Thinking",
				Text:   thinking,
				Format: detectContentFormat(thinking),
			})
		}
	case "input_image", "output_image", "image":
		if blocks != nil {
			*blocks = append(*blocks, ContentBlock{
				Kind:   "image",
				Title:  anthropicBlockTitle(block.Type),
				URL:    strings.TrimSpace(block.Data),
				FileID: block.ID,
				Meta:   marshalNonEmptyAnthropicMeta(block),
			})
		}
	case "document", "input_file", "output_file", "file":
		if blocks != nil {
			*blocks = append(*blocks, ContentBlock{
				Kind:   "attachment",
				Title:  anthropicBlockTitle(block.Type),
				Meta:   marshalNonEmptyAnthropicMeta(block),
				Format: "json",
			})
		}
	case "tool_result":
		// handled by caller
	default:
		renderedText, renderedBlocks := renderAnthropicRichContent(block.Content)
		if textParts != nil && strings.TrimSpace(renderedText) != "" {
			*textParts = append(*textParts, renderedText)
		}
		if blocks != nil && len(renderedBlocks) > 0 {
			*blocks = append(*blocks, renderedBlocks...)
		}
		if blocks != nil && strings.TrimSpace(renderedText) == "" && len(renderedBlocks) == 0 {
			if meta := marshalNonEmptyAnthropicMeta(block); meta != "" {
				*blocks = append(*blocks, ContentBlock{
					Kind:   "attachment",
					Title:  anthropicBlockTitle(block.Type),
					Meta:   meta,
					Format: "json",
				})
			}
		}
	}
}

func renderAnthropicToolResult(block anthropicContentBlock) (string, []ContentBlock) {
	body, blocks := renderAnthropicRichContent(block.Content)
	if body == "" {
		body = strings.TrimSpace(block.Data)
	}
	if !block.IsError {
		return body, blocks
	}
	if body == "" {
		return "Tool result returned an error.", blocks
	}
	return body, blocks
}

func renderAnthropicRichContent(raw interface{}) (string, []ContentBlock) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	case []interface{}:
		var (
			parts  []string
			blocks []ContentBlock
		)
		for _, item := range v {
			itemBytes, err := json.Marshal(item)
			if err != nil {
				continue
			}
			var block anthropicContentBlock
			if err := json.Unmarshal(itemBytes, &block); err != nil {
				continue
			}
			appendAnthropicBlock(&parts, &blocks, block)
		}
		return strings.Join(parts, "\n\n"), blocks
	default:
		return "", []ContentBlock{{
			Kind:   "attachment",
			Title:  "Structured Content",
			Meta:   marshalPretty(v),
			Format: "json",
		}}
	}
}

func anthropicBlockTitle(blockType string) string {
	switch blockType {
	case "input_image":
		return "Input Image"
	case "output_image":
		return "Output Image"
	case "image":
		return "Image"
	case "document", "input_file", "output_file", "file":
		return "Attachment"
	case "thinking":
		return "Thinking"
	default:
		return "Structured Block"
	}
}

func marshalNonEmptyAnthropicMeta(v interface{}) string {
	if v == nil {
		return ""
	}
	raw := marshalPretty(v)
	if raw == "null" || raw == "{}" || raw == "[]" || strings.TrimSpace(raw) == "" {
		return ""
	}
	return raw
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

func marshalCompact(v interface{}) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
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
