package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const openAIParserVersion = "0.1.0"

type openAIParser struct{}

func NewOpenAIParser() Parser {
	return openAIParser{}
}

func (p openAIParser) Name() string {
	return "openai"
}

func (p openAIParser) Version() string {
	return openAIParserVersion
}

func (p openAIParser) CanParse(input ParseInput) bool {
	operation := input.Header.Meta.Operation
	if operation != llm.OperationChatCompletions && operation != llm.OperationResponses {
		return false
	}
	provider := input.Header.Meta.Provider
	return llm.IsOpenAICompatibleProvider(provider) || provider == llm.ProviderUnknown || provider == ""
}

func (p openAIParser) Parse(ctx context.Context, input ParseInput) (TraceObservation, error) {
	select {
	case <-ctx.Done():
		return TraceObservation{}, ctx.Err()
	default:
	}

	obs := TraceObservation{
		TraceID:       input.TraceID,
		Provider:      input.Header.Meta.Provider,
		Operation:     input.Header.Meta.Operation,
		Endpoint:      input.Header.Meta.Endpoint,
		Model:         input.Header.Meta.Model,
		Parser:        p.Name(),
		ParserVersion: p.Version(),
		Status:        ParseStatusParsed,
		RawRefs: RawReferences{
			CassettePath: input.CassettePath,
		},
		Timings: ObservationTimings{
			StartedAt:  input.Header.Meta.Time,
			DurationMs: input.Header.Meta.DurationMs,
			TTFTMs:     input.Header.Meta.TTFTMs,
		},
	}
	obs.Usage = ObservationUsage{
		InputTokens:         input.Header.Usage.PromptTokens,
		OutputTokens:        input.Header.Usage.CompletionTokens,
		TotalTokens:         input.Header.Usage.TotalTokens,
		CacheReadTokens:     cachedTokens(input),
		CacheCreationTokens: 0,
	}

	switch input.Header.Meta.Operation {
	case llm.OperationChatCompletions:
		return parseOpenAIChatObservation(input, obs)
	case llm.OperationResponses:
		return parseOpenAIResponsesObservation(input, obs)
	default:
		return TraceObservation{}, fmt.Errorf("observe: unsupported openai operation %q", input.Header.Meta.Operation)
	}
}

func parseOpenAIChatObservation(input ParseInput, obs TraceObservation) (TraceObservation, error) {
	req, err := decodeJSONObject(input.RequestBody)
	if err != nil {
		return obs, fmt.Errorf("parse openai chat request: %w", err)
	}
	if model := stringField(req, "model"); model != "" {
		obs.Model = model
	}
	obs.Request.Config = objectWithout(req, "messages", "tools")
	obs.Request.Messages = parseOpenAIChatMessages(req["messages"], "request", "$.messages")
	obs.Request.Tools = parseOpenAIChatTools(req["tools"], "request", "$.tools")
	obs.Request.Nodes = append(append([]SemanticNode{}, obs.Request.Messages...), obs.Request.Tools...)
	for _, node := range obs.Request.Tools {
		obs.Tools.Declarations = append(obs.Tools.Declarations, ToolDeclaration{
			ID:           node.ID,
			Name:         metadataString(node.Metadata, "name"),
			Kind:         metadataString(node.Metadata, "kind"),
			Description:  metadataString(node.Metadata, "description"),
			Schema:       rawMessageFromMetadata(node.Metadata, "parameters"),
			NodeID:       node.ID,
			Path:         node.Path,
			ProviderType: node.ProviderType,
		})
	}

	resp, err := decodeJSONObject(input.ResponseBody)
	if err != nil {
		if providerErr := parseProviderErrorNode(input.ResponseBody, "response", "$"); providerErr.ID != "" {
			obs.Response.Errors = append(obs.Response.Errors, providerErr)
			obs.Response.Nodes = append(obs.Response.Nodes, providerErr)
			return obs, nil
		}
		return obs, fmt.Errorf("parse openai chat response: %w", err)
	}
	if model := stringField(resp, "model"); model != "" {
		obs.Model = model
	}
	parseOpenAIChatChoices(resp["choices"], &obs)
	if usage, ok := parseOpenAIUsage(resp["usage"]); ok {
		obs.Usage = usage
	}
	return obs, nil
}

func parseOpenAIResponsesObservation(input ParseInput, obs TraceObservation) (TraceObservation, error) {
	req, err := decodeJSONObject(input.RequestBody)
	if err != nil {
		return obs, fmt.Errorf("parse openai responses request: %w", err)
	}
	if model := stringField(req, "model"); model != "" {
		obs.Model = model
	}
	obs.Request.Config = objectWithout(req, "input", "instructions", "tools")
	obs.Request.Instructions = parseResponsesInstructions(req["instructions"])
	obs.Request.Inputs = parseResponsesInput(req["input"], "request", "$.input")
	obs.Request.Tools = parseResponsesTools(req["tools"], "request", "$.tools")
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Instructions...)
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Inputs...)
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Tools...)
	for _, node := range obs.Request.Tools {
		obs.Tools.Declarations = append(obs.Tools.Declarations, ToolDeclaration{
			ID:           node.ID,
			Name:         metadataString(node.Metadata, "name"),
			Kind:         metadataString(node.Metadata, "kind"),
			Description:  metadataString(node.Metadata, "description"),
			Schema:       rawMessageFromMetadata(node.Metadata, "parameters"),
			NodeID:       node.ID,
			Path:         node.Path,
			ProviderType: node.ProviderType,
		})
	}

	resp, err := decodeJSONObject(input.ResponseBody)
	if err != nil {
		if providerErr := parseProviderErrorNode(input.ResponseBody, "response", "$"); providerErr.ID != "" {
			obs.Response.Errors = append(obs.Response.Errors, providerErr)
			obs.Response.Nodes = append(obs.Response.Nodes, providerErr)
			return obs, nil
		}
		return obs, fmt.Errorf("parse openai responses response: %w", err)
	}
	if model := stringField(resp, "model"); model != "" {
		obs.Model = model
	}
	if status := stringField(resp, "status"); status == "failed" || status == "incomplete" {
		obs.Warnings = append(obs.Warnings, ParseWarning{
			Code:    "response_status",
			Message: "response status is " + status,
			Path:    "$.status",
		})
	}
	if errNode := parseNullableObjectNode(resp["error"], "response", "$.error", "error", NodeError); errNode.ID != "" {
		obs.Response.Errors = append(obs.Response.Errors, errNode)
		obs.Response.Nodes = append(obs.Response.Nodes, errNode)
	}
	if safetyNode := parseNullableObjectNode(resp["incomplete_details"], "response", "$.incomplete_details", "incomplete_details", NodeSafety); safetyNode.ID != "" {
		obs.Response.Safety = append(obs.Response.Safety, safetyNode)
		obs.Response.Nodes = append(obs.Response.Nodes, safetyNode)
		obs.Safety.Blocked = true
	}
	parseResponsesOutput(resp["output"], &obs)
	if usage, ok := parseResponsesUsage(resp["usage"]); ok {
		obs.Usage = usage
	}
	return obs, nil
}

func parseOpenAIChatMessages(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(item)
		role := stringField(obj, "role")
		node := SemanticNode{
			ID:             StableNodeID(section, path, "chat.message", i),
			ProviderType:   "chat.message",
			NormalizedType: NodeMessage,
			Role:           role,
			Path:           path,
			Index:          i,
			Raw:            cloneRaw(item),
			Metadata:       map[string]any{"role": role},
		}
		if name := stringField(obj, "name"); name != "" {
			node.Metadata["name"] = name
		}
		if toolCallID := stringField(obj, "tool_call_id"); toolCallID != "" {
			node.Metadata["tool_call_id"] = toolCallID
		}
		node.Children = append(node.Children, parseOpenAIContent(obj["content"], section, path+".content", role)...)
		node.Children = append(node.Children, parseChatToolCalls(obj["tool_calls"], section, path+".tool_calls")...)
		if role == "tool" || stringField(obj, "tool_call_id") != "" {
			node.NormalizedType = NodeToolResult
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func parseOpenAIContent(raw json.RawMessage, section string, path string, role string) []SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if text, ok := rawJSONString(raw); ok {
		return []SemanticNode{{
			ID:             StableNodeID(section, path, "content", 0),
			ProviderType:   "content",
			NormalizedType: NodeText,
			Role:           role,
			Path:           path,
			Text:           text,
			Raw:            cloneRaw(raw),
		}}
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return []SemanticNode{unknownNode(section, path, "content", 0, raw)}
	}
	nodes := make([]SemanticNode, 0, len(parts))
	for i, part := range parts {
		partPath := fmt.Sprintf("%s[%d]", path, i)
		obj, _ := decodeJSONObject(part)
		providerType := firstNonEmpty(stringField(obj, "type"), "content_part")
		text := firstNonEmpty(stringField(obj, "text"), stringField(obj, "input_text"), stringField(obj, "output_text"), stringField(obj, "refusal"))
		normalized := normalizedContentType(providerType)
		nodes = append(nodes, SemanticNode{
			ID:             StableNodeID(section, partPath, providerType, i),
			ProviderType:   providerType,
			NormalizedType: normalized,
			Role:           role,
			Path:           partPath,
			Index:          i,
			Text:           text,
			Raw:            cloneRaw(part),
		})
	}
	return nodes
}

func parseOpenAIChatTools(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(item)
		functionObj, _ := decodeJSONObject(obj["function"])
		name := firstNonEmpty(stringField(functionObj, "name"), stringField(obj, "name"))
		metadata := map[string]any{
			"name":        name,
			"kind":        firstNonEmpty(stringField(obj, "type"), "function"),
			"description": stringField(functionObj, "description"),
		}
		if params := functionObj["parameters"]; len(params) > 0 {
			metadata["parameters"] = cloneRaw(params)
		}
		nodes = append(nodes, SemanticNode{
			ID:             StableNodeID(section, path, "tool", i),
			ProviderType:   "tool",
			NormalizedType: NodeToolDeclaration,
			Path:           path,
			Index:          i,
			Text:           name,
			Raw:            cloneRaw(item),
			Metadata:       metadata,
		})
	}
	return nodes
}

func parseResponsesTools(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(item)
		providerType := firstNonEmpty(stringField(obj, "type"), "tool")
		name := firstNonEmpty(stringField(obj, "name"), providerType)
		metadata := map[string]any{
			"name":        name,
			"kind":        providerType,
			"description": stringField(obj, "description"),
		}
		if params := obj["parameters"]; len(params) > 0 {
			metadata["parameters"] = cloneRaw(params)
		}
		nodes = append(nodes, SemanticNode{
			ID:             StableNodeID(section, path, providerType, i),
			ProviderType:   providerType,
			NormalizedType: NodeToolDeclaration,
			Path:           path,
			Index:          i,
			Text:           name,
			Raw:            cloneRaw(item),
			Metadata:       metadata,
		})
	}
	return nodes
}

func parseChatToolCalls(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(item)
		functionObj, _ := decodeJSONObject(obj["function"])
		name := stringField(functionObj, "name")
		args := stringField(functionObj, "arguments")
		nodes = append(nodes, SemanticNode{
			ID:             StableNodeID(section, path, "tool_call", i),
			ProviderType:   firstNonEmpty(stringField(obj, "type"), "tool_call"),
			NormalizedType: NodeToolCall,
			Path:           path,
			Index:          i,
			Text:           name,
			Raw:            cloneRaw(item),
			Metadata: map[string]any{
				"id":        stringField(obj, "id"),
				"name":      name,
				"arguments": args,
			},
		})
	}
	return nodes
}

func parseOpenAIChatChoices(raw json.RawMessage, obs *TraceObservation) {
	var choices []json.RawMessage
	if err := json.Unmarshal(raw, &choices); err != nil {
		return
	}
	for i, choiceRaw := range choices {
		path := fmt.Sprintf("$.choices[%d]", i)
		choiceObj, _ := decodeJSONObject(choiceRaw)
		finishReason := stringField(choiceObj, "finish_reason")
		node := SemanticNode{
			ID:             StableNodeID("response", path, "choice", i),
			ProviderType:   "choice",
			NormalizedType: NodeMessage,
			Path:           path,
			Index:          i,
			Raw:            cloneRaw(choiceRaw),
			Metadata: map[string]any{
				"finish_reason": finishReason,
			},
		}
		messageRaw := choiceObj["message"]
		messageObj, _ := decodeJSONObject(messageRaw)
		role := firstNonEmpty(stringField(messageObj, "role"), "assistant")
		messageNode := SemanticNode{
			ID:             StableNodeID("response", path+".message", "chat.message", 0),
			ProviderType:   "chat.message",
			NormalizedType: NodeMessage,
			Role:           role,
			Path:           path + ".message",
			Raw:            cloneRaw(messageRaw),
			Metadata:       map[string]any{"role": role},
		}
		messageNode.Children = append(messageNode.Children, parseOpenAIContent(messageObj["content"], "response", path+".message.content", role)...)
		if refusal := stringField(messageObj, "refusal"); refusal != "" {
			messageNode.Children = append(messageNode.Children, SemanticNode{
				ID:             StableNodeID("response", path+".message.refusal", "refusal", 0),
				ProviderType:   "refusal",
				NormalizedType: NodeRefusal,
				Role:           role,
				Path:           path + ".message.refusal",
				Text:           refusal,
				Raw:            cloneRaw(messageObj["refusal"]),
			})
		}
		toolCalls := parseChatToolCalls(messageObj["tool_calls"], "response", path+".message.tool_calls")
		messageNode.Children = append(messageNode.Children, toolCalls...)
		node.Children = append(node.Children, messageNode)
		obs.Response.Candidates = append(obs.Response.Candidates, node)
		obs.Response.Nodes = append(obs.Response.Nodes, node)
		for _, toolCall := range toolCalls {
			obs.Response.ToolCalls = append(obs.Response.ToolCalls, toolCall)
			obs.Tools.Calls = append(obs.Tools.Calls, toolCallObservationFromNode(toolCall, ToolOwnerModelRequested))
		}
		if finishReason == "content_filter" {
			safetyNode := SemanticNode{
				ID:             StableNodeID("response", path+".finish_reason", "finish_reason", 0),
				ProviderType:   "finish_reason",
				NormalizedType: NodeSafety,
				Path:           path + ".finish_reason",
				Text:           finishReason,
			}
			obs.Response.Safety = append(obs.Response.Safety, safetyNode)
			obs.Response.Nodes = append(obs.Response.Nodes, safetyNode)
			obs.Safety.Blocked = true
		}
	}
}

func parseResponsesInstructions(raw json.RawMessage) []SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return []SemanticNode{{
		ID:             StableNodeID("request", "$.instructions", "instructions", 0),
		ProviderType:   "instructions",
		NormalizedType: NodeInstruction,
		Role:           "developer",
		Path:           "$.instructions",
		Text:           textFromRaw(raw),
		Raw:            cloneRaw(raw),
	}}
}

func parseResponsesInput(raw json.RawMessage, section string, basePath string) []SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if text, ok := rawJSONString(raw); ok {
		return []SemanticNode{{
			ID:             StableNodeID(section, basePath, "input_text", 0),
			ProviderType:   "input_text",
			NormalizedType: NodeText,
			Role:           "user",
			Path:           basePath,
			Text:           text,
			Raw:            cloneRaw(raw),
		}}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return []SemanticNode{unknownNode(section, basePath, "input", 0, raw)}
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		nodes = append(nodes, parseResponsesItem(item, section, fmt.Sprintf("%s[%d]", basePath, i), i))
	}
	return nodes
}

func parseResponsesOutput(raw json.RawMessage, obs *TraceObservation) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for i, item := range items {
		node := parseResponsesItem(item, "response", fmt.Sprintf("$.output[%d]", i), i)
		obs.Response.Outputs = append(obs.Response.Outputs, node)
		obs.Response.Nodes = append(obs.Response.Nodes, node)
		switch node.NormalizedType {
		case NodeToolCall, NodeServerToolCall:
			obs.Response.ToolCalls = append(obs.Response.ToolCalls, node)
			obs.Tools.Calls = append(obs.Tools.Calls, toolCallObservationFromNode(node, toolOwnerForResponsesType(node.ProviderType)))
		case NodeToolResult, NodeServerToolResult:
			obs.Response.ToolResults = append(obs.Response.ToolResults, node)
			obs.Tools.Results = append(obs.Tools.Results, toolResultObservationFromNode(node, toolOwnerForResponsesType(node.ProviderType)))
		case NodeReasoning:
			obs.Response.Reasoning = append(obs.Response.Reasoning, node)
		case NodeRefusal:
			obs.Response.Refusals = append(obs.Response.Refusals, node)
			obs.Safety.Refused = true
		case NodeSafety:
			obs.Response.Safety = append(obs.Response.Safety, node)
			obs.Safety.Blocked = true
		case NodeUnknown:
			obs.Warnings = append(obs.Warnings, ParseWarning{
				Code:    "unknown_output_item",
				Message: "preserved unknown responses output item type " + node.ProviderType,
				Path:    node.Path,
			})
		}
	}
}

func parseResponsesItem(raw json.RawMessage, section string, path string, index int) SemanticNode {
	obj, _ := decodeJSONObject(raw)
	providerType := firstNonEmpty(stringField(obj, "type"), "unknown")
	role := stringField(obj, "role")
	node := SemanticNode{
		ID:             StableNodeID(section, path, providerType, index),
		ProviderType:   providerType,
		NormalizedType: normalizedResponsesType(providerType),
		Role:           role,
		Path:           path,
		Index:          index,
		Text:           firstNonEmpty(stringField(obj, "name"), stringField(obj, "arguments"), textFromRaw(obj["output"])),
		Raw:            cloneRaw(raw),
		Metadata: map[string]any{
			"id":      stringField(obj, "id"),
			"call_id": stringField(obj, "call_id"),
			"name":    stringField(obj, "name"),
			"status":  stringField(obj, "status"),
		},
	}
	if args := stringField(obj, "arguments"); args != "" {
		node.Metadata["arguments"] = args
	}
	if len(obj["output"]) > 0 && string(obj["output"]) != "null" {
		node.Metadata["output"] = cloneRaw(obj["output"])
	}
	if content := parseResponsesContent(obj["content"], section, path+".content"); len(content) > 0 {
		node.Children = append(node.Children, content...)
		if node.Text == "" {
			node.Text = firstNodeText(content)
		}
	}
	return node
}

func parseResponsesContent(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(parts))
	for i, part := range parts {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(part)
		providerType := firstNonEmpty(stringField(obj, "type"), "content_part")
		node := SemanticNode{
			ID:             StableNodeID(section, path, providerType, i),
			ProviderType:   providerType,
			NormalizedType: normalizedContentType(providerType),
			Path:           path,
			Index:          i,
			Text:           firstNonEmpty(stringField(obj, "text"), stringField(obj, "input_text"), stringField(obj, "output_text"), stringField(obj, "refusal")),
			Raw:            cloneRaw(part),
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func normalizedResponsesType(providerType string) NormalizedType {
	switch providerType {
	case "message":
		return NodeMessage
	case "reasoning":
		return NodeReasoning
	case "function_call", "custom_tool_call", "local_shell_call", "apply_patch":
		return NodeToolCall
	case "web_search_call", "file_search_call", "computer_call", "code_interpreter_call", "mcp_call":
		return NodeServerToolCall
	case "function_call_output", "custom_tool_call_output":
		return NodeToolResult
	case "mcp_call_output", "web_search_call_output", "file_search_call_output", "computer_call_output", "code_interpreter_call_output":
		return NodeServerToolResult
	case "refusal":
		return NodeRefusal
	case "error":
		return NodeError
	default:
		return NodeUnknown
	}
}

func normalizedContentType(providerType string) NormalizedType {
	switch providerType {
	case "input_text", "output_text", "text":
		return NodeText
	case "refusal":
		return NodeRefusal
	case "reasoning", "summary_text", "reasoning_text":
		return NodeReasoning
	case "input_image", "image_url":
		return NodeImage
	case "input_file", "file":
		return NodeFile
	default:
		return NodeUnknown
	}
}

func toolOwnerForResponsesType(providerType string) ToolOwner {
	switch providerType {
	case "web_search_call", "file_search_call", "computer_call", "code_interpreter_call", "mcp_call",
		"mcp_call_output", "web_search_call_output", "file_search_call_output", "computer_call_output", "code_interpreter_call_output":
		return ToolOwnerProviderExecuted
	case "function_call", "custom_tool_call", "local_shell_call", "apply_patch":
		return ToolOwnerModelRequested
	case "function_call_output", "custom_tool_call_output":
		return ToolOwnerClientExecuted
	default:
		return ToolOwnerUnknown
	}
}

func toolCallObservationFromNode(node SemanticNode, owner ToolOwner) ToolCallObservation {
	return ToolCallObservation{
		ID:       firstNonEmpty(metadataString(node.Metadata, "call_id"), metadataString(node.Metadata, "id"), node.ID),
		Name:     firstNonEmpty(metadataString(node.Metadata, "name"), node.Text),
		Kind:     node.ProviderType,
		Owner:    owner,
		ArgsText: metadataString(node.Metadata, "arguments"),
		NodeID:   node.ID,
		Path:     node.Path,
	}
}

func toolResultObservationFromNode(node SemanticNode, owner ToolOwner) ToolResultObservation {
	return ToolResultObservation{
		ID:      firstNonEmpty(metadataString(node.Metadata, "call_id"), metadataString(node.Metadata, "id"), node.ID),
		Name:    firstNonEmpty(metadataString(node.Metadata, "name"), node.Text),
		Kind:    node.ProviderType,
		Owner:   owner,
		Text:    node.Text,
		JSON:    rawMessageFromMetadata(node.Metadata, "output"),
		NodeID:  node.ID,
		Path:    node.Path,
		IsError: strings.Contains(strings.ToLower(metadataString(node.Metadata, "status")), "error"),
	}
}

func parseOpenAIUsage(raw json.RawMessage) (ObservationUsage, bool) {
	obj, err := decodeJSONObject(raw)
	if err != nil {
		return ObservationUsage{}, false
	}
	usage := ObservationUsage{
		InputTokens:  intField(obj, "prompt_tokens"),
		OutputTokens: intField(obj, "completion_tokens"),
		TotalTokens:  intField(obj, "total_tokens"),
	}
	if details, err := decodeJSONObject(obj["prompt_tokens_details"]); err == nil {
		usage.CacheReadTokens = intField(details, "cached_tokens")
	}
	if details, err := decodeJSONObject(obj["completion_tokens_details"]); err == nil {
		usage.ReasoningTokens = intField(details, "reasoning_tokens")
	}
	return usage, true
}

func parseResponsesUsage(raw json.RawMessage) (ObservationUsage, bool) {
	obj, err := decodeJSONObject(raw)
	if err != nil {
		return ObservationUsage{}, false
	}
	usage := ObservationUsage{
		InputTokens:  intField(obj, "input_tokens"),
		OutputTokens: intField(obj, "output_tokens"),
		TotalTokens:  intField(obj, "total_tokens"),
	}
	if details, err := decodeJSONObject(obj["input_tokens_details"]); err == nil {
		usage.CacheReadTokens = intField(details, "cached_tokens")
	}
	if details, err := decodeJSONObject(obj["output_tokens_details"]); err == nil {
		usage.ReasoningTokens = intField(details, "reasoning_tokens")
	}
	return usage, true
}

func parseProviderErrorNode(raw json.RawMessage, section string, path string) SemanticNode {
	obj, err := decodeJSONObject(raw)
	if err != nil {
		return SemanticNode{}
	}
	if _, ok := obj["error"]; !ok {
		return SemanticNode{}
	}
	return SemanticNode{
		ID:             StableNodeID(section, path, "error", 0),
		ProviderType:   "error",
		NormalizedType: NodeError,
		Path:           path,
		Text:           textFromRaw(obj["error"]),
		Raw:            cloneRaw(raw),
	}
}

func parseNullableObjectNode(raw json.RawMessage, section string, path string, providerType string, normalized NormalizedType) SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return SemanticNode{}
	}
	return SemanticNode{
		ID:             StableNodeID(section, path, providerType, 0),
		ProviderType:   providerType,
		NormalizedType: normalized,
		Path:           path,
		Text:           textFromRaw(raw),
		Raw:            cloneRaw(raw),
	}
}

func decodeJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty json")
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func objectWithout(obj map[string]json.RawMessage, omitted ...string) map[string]any {
	omit := make(map[string]struct{}, len(omitted))
	for _, key := range omitted {
		omit[key] = struct{}{}
	}
	out := make(map[string]any)
	for key, raw := range obj {
		if _, skip := omit[key]; skip {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err == nil {
			out[key] = value
		}
	}
	return out
}

func stringField(obj map[string]json.RawMessage, key string) string {
	raw, ok := obj[key]
	if !ok {
		return ""
	}
	text, _ := rawJSONString(raw)
	return text
}

func intField(obj map[string]json.RawMessage, key string) int {
	raw, ok := obj[key]
	if !ok {
		return 0
	}
	var n int
	_ = json.Unmarshal(raw, &n)
	return n
}

func rawJSONString(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}

func textFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if text, ok := rawJSONString(raw); ok {
		return text
	}
	return string(raw)
}

func unknownNode(section string, path string, providerType string, index int, raw json.RawMessage) SemanticNode {
	return SemanticNode{
		ID:             StableNodeID(section, path, providerType, index),
		ProviderType:   providerType,
		NormalizedType: NodeUnknown,
		Path:           path,
		Index:          index,
		Raw:            cloneRaw(raw),
	}
}

func firstNodeText(nodes []SemanticNode) string {
	for _, node := range nodes {
		if node.Text != "" {
			return node.Text
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func rawMessageFromMetadata(metadata map[string]any, key string) json.RawMessage {
	if metadata == nil {
		return nil
	}
	switch value := metadata[key].(type) {
	case json.RawMessage:
		return cloneRaw(value)
	case []byte:
		return cloneRaw(value)
	default:
		return nil
	}
}

func cachedTokens(input ParseInput) int {
	if input.Header.Usage.PromptTokenDetails == nil {
		return 0
	}
	return input.Header.Usage.PromptTokenDetails.CachedTokens
}
