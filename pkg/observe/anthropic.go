package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const anthropicParserVersion = "0.1.0"

type anthropicParser struct{}

func NewAnthropicParser() Parser {
	return anthropicParser{}
}

func (p anthropicParser) Name() string {
	return "anthropic"
}

func (p anthropicParser) Version() string {
	return anthropicParserVersion
}

func (p anthropicParser) CanParse(input ParseInput) bool {
	return input.Header.Meta.Provider == llm.ProviderAnthropic || input.Header.Meta.Operation == llm.OperationMessages || input.Header.Meta.Endpoint == "/v1/messages"
}

func (p anthropicParser) Parse(ctx context.Context, input ParseInput) (TraceObservation, error) {
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
		Usage: ObservationUsage{
			InputTokens:         input.Header.Usage.PromptTokens,
			OutputTokens:        input.Header.Usage.CompletionTokens,
			TotalTokens:         input.Header.Usage.TotalTokens,
			CacheReadTokens:     cachedTokens(input),
			CacheCreationTokens: 0,
		},
	}
	req, err := decodeJSONObject(input.RequestBody)
	if err != nil {
		return obs, fmt.Errorf("parse anthropic messages request: %w", err)
	}
	if model := stringField(req, "model"); model != "" {
		obs.Model = model
	}
	obs.Request.Config = objectWithout(req, "system", "messages", "tools")
	obs.Request.Instructions = parseAnthropicSystem(req["system"])
	obs.Request.Messages = parseAnthropicMessages(req["messages"], "request", "$.messages")
	obs.Request.Tools = parseAnthropicTools(req["tools"], "request", "$.tools")
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Instructions...)
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Messages...)
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Tools...)
	for _, node := range obs.Request.Tools {
		obs.Tools.Declarations = append(obs.Tools.Declarations, ToolDeclaration{
			ID:           node.ID,
			Name:         metadataString(node.Metadata, "name"),
			Kind:         metadataString(node.Metadata, "kind"),
			Description:  metadataString(node.Metadata, "description"),
			Schema:       rawMessageFromMetadata(node.Metadata, "input_schema"),
			NodeID:       node.ID,
			Path:         node.Path,
			ProviderType: node.ProviderType,
		})
	}
	appendAnthropicToolObservations(obs.Request.Messages, &obs)

	if input.IsStream {
		parseAnthropicStream(input.ResponseBody, &obs)
		return obs, nil
	}

	resp, err := decodeJSONObject(input.ResponseBody)
	if err != nil {
		if providerErr := parseProviderErrorNode(input.ResponseBody, "response", "$"); providerErr.ID != "" {
			obs.Response.Errors = append(obs.Response.Errors, providerErr)
			obs.Response.Nodes = append(obs.Response.Nodes, providerErr)
			return obs, nil
		}
		return obs, fmt.Errorf("parse anthropic messages response: %w", err)
	}
	if model := stringField(resp, "model"); model != "" {
		obs.Model = model
	}
	role := firstNonEmpty(stringField(resp, "role"), "assistant")
	contents := parseAnthropicContentBlocks(resp["content"], "response", "$.content", role)
	message := SemanticNode{
		ID:             StableNodeID("response", "$", "message", 0),
		ProviderType:   "message",
		NormalizedType: NodeMessage,
		Role:           role,
		Path:           "$",
		Text:           firstNodeText(contents),
		Raw:            cloneRaw(input.ResponseBody),
		Metadata: map[string]any{
			"id":          stringField(resp, "id"),
			"stop_reason": stringField(resp, "stop_reason"),
		},
		Children: contents,
	}
	obs.Response.Nodes = append(obs.Response.Nodes, message)
	appendAnthropicResponseNodes(contents, &obs)
	if usage, ok := parseAnthropicUsage(resp["usage"]); ok {
		obs.Usage = usage
	}
	return obs, nil
}

func parseAnthropicSystem(raw json.RawMessage) []SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if text, ok := rawJSONString(raw); ok {
		return []SemanticNode{{
			ID:             StableNodeID("request", "$.system", "system", 0),
			ProviderType:   "system",
			NormalizedType: NodeInstruction,
			Role:           "system",
			Path:           "$.system",
			Text:           text,
			Raw:            cloneRaw(raw),
		}}
	}
	return parseAnthropicContentBlocks(raw, "request", "$.system", "system")
}

func parseAnthropicMessages(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(item)
		role := stringField(obj, "role")
		children := parseAnthropicContentBlocks(obj["content"], section, path+".content", role)
		node := SemanticNode{
			ID:             StableNodeID(section, path, "message", i),
			ProviderType:   "message",
			NormalizedType: NodeMessage,
			Role:           role,
			Path:           path,
			Index:          i,
			Text:           firstNodeText(children),
			Raw:            cloneRaw(item),
			Metadata:       map[string]any{"role": role},
			Children:       children,
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func parseAnthropicContentBlocks(raw json.RawMessage, section string, basePath string, role string) []SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if text, ok := rawJSONString(raw); ok {
		return []SemanticNode{{
			ID:             StableNodeID(section, basePath, "text", 0),
			ProviderType:   "text",
			NormalizedType: NodeText,
			Role:           role,
			Path:           basePath,
			Text:           text,
			Raw:            cloneRaw(raw),
		}}
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return []SemanticNode{unknownNode(section, basePath, "content", 0, raw)}
	}
	nodes := make([]SemanticNode, 0, len(blocks))
	for i, block := range blocks {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		nodes = append(nodes, parseAnthropicBlock(block, section, path, i, role))
	}
	return nodes
}

func parseAnthropicBlock(raw json.RawMessage, section string, path string, index int, role string) SemanticNode {
	obj, _ := decodeJSONObject(raw)
	providerType := firstNonEmpty(stringField(obj, "type"), "content_block")
	node := SemanticNode{
		ID:             StableNodeID(section, path, providerType, index),
		ProviderType:   providerType,
		NormalizedType: normalizedAnthropicType(providerType),
		Role:           role,
		Path:           path,
		Index:          index,
		Text:           anthropicBlockText(obj, providerType),
		Raw:            cloneRaw(raw),
		Metadata: map[string]any{
			"id":          stringField(obj, "id"),
			"name":        stringField(obj, "name"),
			"tool_use_id": stringField(obj, "tool_use_id"),
		},
	}
	if input := obj["input"]; len(input) > 0 && string(input) != "null" {
		node.Metadata["input"] = cloneRaw(input)
	}
	if content := obj["content"]; len(content) > 0 && string(content) != "null" {
		node.Metadata["content"] = cloneRaw(content)
		node.Children = append(node.Children, parseAnthropicContentBlocks(content, section, path+".content", role)...)
		if node.Text == "" {
			node.Text = firstNodeText(node.Children)
		}
	}
	if isErr := boolField(obj, "is_error"); isErr {
		node.Metadata["is_error"] = "true"
	}
	return node
}

func parseAnthropicTools(raw json.RawMessage, section string, basePath string) []SemanticNode {
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
		if schema := obj["input_schema"]; len(schema) > 0 {
			metadata["input_schema"] = cloneRaw(schema)
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

func parseAnthropicStream(body []byte, obs *TraceObservation) {
	var (
		eventIndex       int
		blockMap         = map[int]*SemanticNode{}
		blockOrder       []int
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
	)
	scanSSEData(body, func(data string) {
		if data == "[DONE]" {
			return
		}
		raw := json.RawMessage(data)
		obj, err := decodeJSONObject(raw)
		if err != nil {
			obs.Warnings = append(obs.Warnings, ParseWarning{Code: "invalid_stream_json", Message: err.Error(), Path: "$"})
			return
		}
		eventType := stringField(obj, "type")
		path := fmt.Sprintf("$.stream.events[%d]", eventIndex)
		obs.Stream.Events = append(obs.Stream.Events, streamEvent(eventIndex, eventType, eventType, normalizedAnthropicStreamEvent(eventType, obj), path, anthropicStreamDelta(obj), raw))
		switch eventType {
		case "content_block_start":
			idx := intField(obj, "index")
			contentObj, _ := decodeJSONObject(obj["content_block"])
			node := parseAnthropicBlock(obj["content_block"], "response", fmt.Sprintf("$.stream.content[%d]", idx), idx, "assistant")
			blockMap[idx] = &node
			blockOrder = append(blockOrder, idx)
			if node.Text != "" {
				switch node.NormalizedType {
				case NodeReasoning:
					reasoningBuilder.WriteString(node.Text)
				case NodeText:
					textBuilder.WriteString(node.Text)
				}
			}
			if stringField(contentObj, "type") == "tool_use" && rawMessageFromMetadata(node.Metadata, "input") == nil {
				node.Metadata["input"] = json.RawMessage(`{}`)
			}
		case "content_block_delta":
			idx := intField(obj, "index")
			node := blockMap[idx]
			if node == nil {
				path := fmt.Sprintf("$.stream.content[%d]", idx)
				tmp := SemanticNode{
					ID:             StableNodeID("response", path, "content_block", idx),
					ProviderType:   "content_block",
					NormalizedType: NodeUnknown,
					Role:           "assistant",
					Path:           path,
					Index:          idx,
					Metadata:       map[string]any{},
				}
				node = &tmp
				blockMap[idx] = node
				blockOrder = append(blockOrder, idx)
			}
			deltaObj, _ := decodeJSONObject(obj["delta"])
			switch stringField(deltaObj, "type") {
			case "text_delta":
				delta := stringField(deltaObj, "text")
				node.Text += delta
				textBuilder.WriteString(delta)
			case "thinking_delta":
				delta := firstNonEmpty(stringField(deltaObj, "thinking"), stringField(deltaObj, "text"))
				node.Text += delta
				reasoningBuilder.WriteString(delta)
			case "input_json_delta":
				partial := stringField(deltaObj, "partial_json")
				node.Metadata["input"] = json.RawMessage(metadataString(node.Metadata, "input") + partial)
			}
		case "message_delta":
			deltaObj, _ := decodeJSONObject(obj["delta"])
			if stopReason := stringField(deltaObj, "stop_reason"); stopReason != "" && strings.Contains(stopReason, "safety") {
				obs.Safety.Blocked = true
			}
			if usage, ok := parseAnthropicUsage(obj["usage"]); ok {
				obs.Usage = usage
			}
		case "error":
			node := SemanticNode{
				ID:             StableNodeID("response", path+".error", "error", eventIndex),
				ProviderType:   "error",
				NormalizedType: NodeError,
				Path:           path + ".error",
				Index:          eventIndex,
				Text:           textFromRaw(obj["error"]),
				Raw:            cloneRaw(raw),
			}
			obs.Stream.Errors = append(obs.Stream.Errors, node)
			obs.Response.Errors = append(obs.Response.Errors, node)
		}
		eventIndex++
	})
	for _, idx := range blockOrder {
		if node := blockMap[idx]; node != nil {
			appendAnthropicResponseNodes([]SemanticNode{*node}, obs)
			if node.NormalizedType == NodeToolCall || node.NormalizedType == NodeServerToolCall {
				obs.Stream.AccumulatedToolCalls = append(obs.Stream.AccumulatedToolCalls, *node)
			}
		}
	}
	obs.Stream.AccumulatedText = textBuilder.String()
	obs.Stream.AccumulatedReasoning = reasoningBuilder.String()
}

func appendAnthropicResponseNodes(nodes []SemanticNode, obs *TraceObservation) {
	for _, node := range nodes {
		switch node.NormalizedType {
		case NodeText, NodeMessage, NodeUnknown, NodeImage, NodeFile, NodeCitation:
			obs.Response.Outputs = append(obs.Response.Outputs, node)
		case NodeReasoning:
			obs.Response.Reasoning = append(obs.Response.Reasoning, node)
		case NodeToolCall, NodeServerToolCall:
			obs.Response.ToolCalls = append(obs.Response.ToolCalls, node)
			obs.Tools.Calls = append(obs.Tools.Calls, anthropicToolCallFromNode(node))
		case NodeToolResult, NodeServerToolResult:
			obs.Response.ToolResults = append(obs.Response.ToolResults, node)
			obs.Tools.Results = append(obs.Tools.Results, anthropicToolResultFromNode(node))
		case NodeError:
			obs.Response.Errors = append(obs.Response.Errors, node)
		}
		for _, child := range node.Children {
			appendAnthropicResponseNodes([]SemanticNode{child}, obs)
		}
	}
}

func appendAnthropicToolObservations(nodes []SemanticNode, obs *TraceObservation) {
	for _, node := range nodes {
		switch node.NormalizedType {
		case NodeToolCall, NodeServerToolCall:
			obs.Tools.Calls = append(obs.Tools.Calls, anthropicToolCallFromNode(node))
		case NodeToolResult, NodeServerToolResult:
			obs.Tools.Results = append(obs.Tools.Results, anthropicToolResultFromNode(node))
		}
		appendAnthropicToolObservations(node.Children, obs)
	}
}

func anthropicToolCallFromNode(node SemanticNode) ToolCallObservation {
	argsJSON := rawMessageFromMetadata(node.Metadata, "input")
	return ToolCallObservation{
		ID:       firstNonEmpty(metadataString(node.Metadata, "id"), node.ID),
		Name:     firstNonEmpty(metadataString(node.Metadata, "name"), node.Text),
		Kind:     node.ProviderType,
		Owner:    anthropicToolOwner(node.ProviderType, true),
		ArgsText: firstNonEmpty(string(argsJSON), metadataString(node.Metadata, "input"), node.Text),
		ArgsJSON: argsJSON,
		NodeID:   node.ID,
		Path:     node.Path,
	}
}

func anthropicToolResultFromNode(node SemanticNode) ToolResultObservation {
	return ToolResultObservation{
		ID:      firstNonEmpty(metadataString(node.Metadata, "tool_use_id"), metadataString(node.Metadata, "id"), node.ID),
		Name:    metadataString(node.Metadata, "name"),
		Kind:    node.ProviderType,
		Owner:   anthropicToolOwner(node.ProviderType, false),
		Text:    node.Text,
		JSON:    rawMessageFromMetadata(node.Metadata, "content"),
		NodeID:  node.ID,
		Path:    node.Path,
		IsError: metadataString(node.Metadata, "is_error") == "true" || strings.Contains(strings.ToLower(node.ProviderType), "error"),
	}
}

func normalizedAnthropicType(providerType string) NormalizedType {
	switch providerType {
	case "text":
		return NodeText
	case "thinking", "redacted_thinking":
		return NodeReasoning
	case "tool_use":
		return NodeToolCall
	case "tool_result":
		return NodeToolResult
	case "server_tool_use":
		return NodeServerToolCall
	case "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result",
		"bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result":
		return NodeServerToolResult
	case "image":
		return NodeImage
	case "document":
		return NodeFile
	case "web_search_result", "citation":
		return NodeCitation
	default:
		if strings.Contains(providerType, "_error") || providerType == "error" {
			return NodeError
		}
		return NodeUnknown
	}
}

func normalizedAnthropicStreamEvent(eventType string, obj map[string]json.RawMessage) NormalizedType {
	switch eventType {
	case "content_block_start":
		contentObj, _ := decodeJSONObject(obj["content_block"])
		return normalizedAnthropicType(stringField(contentObj, "type"))
	case "content_block_delta":
		deltaObj, _ := decodeJSONObject(obj["delta"])
		switch stringField(deltaObj, "type") {
		case "text_delta":
			return NodeText
		case "thinking_delta":
			return NodeReasoning
		case "input_json_delta":
			return NodeToolCallDelta
		}
	case "error":
		return NodeError
	}
	return NodeUnknown
}

func anthropicBlockText(obj map[string]json.RawMessage, providerType string) string {
	switch providerType {
	case "text":
		return stringField(obj, "text")
	case "thinking":
		return stringField(obj, "thinking")
	case "redacted_thinking":
		return "[redacted_thinking]"
	case "tool_use", "server_tool_use":
		return firstNonEmpty(stringField(obj, "name"), textFromRaw(obj["input"]))
	case "tool_result":
		return textFromRaw(obj["content"])
	default:
		return firstNonEmpty(stringField(obj, "text"), textFromRaw(obj["content"]), textFromRaw(obj["input"]))
	}
}

func anthropicToolOwner(providerType string, isCall bool) ToolOwner {
	switch providerType {
	case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result", "code_execution_tool_result",
		"bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result":
		return ToolOwnerProviderExecuted
	case "tool_result":
		return ToolOwnerClientExecuted
	case "tool_use":
		return ToolOwnerModelRequested
	default:
		if isCall {
			return ToolOwnerModelRequested
		}
		return ToolOwnerUnknown
	}
}

func parseAnthropicUsage(raw json.RawMessage) (ObservationUsage, bool) {
	obj, err := decodeJSONObject(raw)
	if err != nil {
		return ObservationUsage{}, false
	}
	usage := ObservationUsage{
		InputTokens:         intField(obj, "input_tokens"),
		OutputTokens:        intField(obj, "output_tokens"),
		CacheCreationTokens: intField(obj, "cache_creation_input_tokens"),
		CacheReadTokens:     intField(obj, "cache_read_input_tokens"),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage, true
}

func anthropicStreamDelta(obj map[string]json.RawMessage) string {
	if deltaObj, err := decodeJSONObject(obj["delta"]); err == nil {
		return firstNonEmpty(stringField(deltaObj, "text"), stringField(deltaObj, "thinking"), stringField(deltaObj, "partial_json"))
	}
	return ""
}

func boolField(obj map[string]json.RawMessage, key string) bool {
	raw, ok := obj[key]
	if !ok {
		return false
	}
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}
