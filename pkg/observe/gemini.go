package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/llm"
)

const geminiParserVersion = "0.1.0"

type geminiParser struct{}

func NewGeminiParser() Parser {
	return geminiParser{}
}

func (p geminiParser) Name() string {
	return "gemini"
}

func (p geminiParser) Version() string {
	return geminiParserVersion
}

func (p geminiParser) CanParse(input ParseInput) bool {
	return input.Header.Meta.Provider == llm.ProviderGoogleGenAI ||
		input.Header.Meta.Provider == llm.ProviderVertexNative ||
		input.Header.Meta.Operation == llm.OperationGenerateContent ||
		strings.Contains(input.Header.Meta.Endpoint, "generateContent")
}

func (p geminiParser) Parse(ctx context.Context, input ParseInput) (TraceObservation, error) {
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
		return obs, fmt.Errorf("parse gemini request: %w", err)
	}
	if model := stringField(req, "model"); model != "" {
		obs.Model = model
	}
	obs.Request.Config = objectWithout(req, "systemInstruction", "contents", "tools")
	obs.Request.Instructions = parseGeminiSystemInstruction(req["systemInstruction"])
	obs.Request.Messages = parseGeminiContents(req["contents"], "request", "$.contents")
	obs.Request.Tools = parseGeminiTools(req["tools"], "request", "$.tools")
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Instructions...)
	obs.Request.Nodes = append(obs.Request.Nodes, obs.Request.Messages...)
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
	appendGeminiToolObservations(obs.Request.Messages, &obs)

	if input.IsStream {
		parseGeminiStream(input.ResponseBody, &obs)
		return obs, nil
	}

	resp, err := decodeJSONObject(input.ResponseBody)
	if err != nil {
		if providerErr := parseProviderErrorNode(input.ResponseBody, "response", "$"); providerErr.ID != "" {
			obs.Response.Errors = append(obs.Response.Errors, providerErr)
			obs.Response.Nodes = append(obs.Response.Nodes, providerErr)
			return obs, nil
		}
		return obs, fmt.Errorf("parse gemini response: %w", err)
	}
	if providerErr := parseProviderErrorNode(input.ResponseBody, "response", "$"); providerErr.ID != "" {
		obs.Response.Errors = append(obs.Response.Errors, providerErr)
		obs.Response.Nodes = append(obs.Response.Nodes, providerErr)
		return obs, nil
	}
	if model := firstNonEmpty(stringField(resp, "modelVersion"), stringField(resp, "model")); model != "" {
		obs.Model = model
	}
	parseGeminiCandidates(resp["candidates"], &obs)
	if promptFeedback := parseNullableObjectNode(resp["promptFeedback"], "response", "$.promptFeedback", "promptFeedback", NodeSafety); promptFeedback.ID != "" {
		obs.Response.Safety = append(obs.Response.Safety, promptFeedback)
		obs.Response.Nodes = append(obs.Response.Nodes, promptFeedback)
		obs.Safety.Blocked = strings.Contains(strings.ToLower(promptFeedback.Text), "block")
	}
	if usage, ok := parseGeminiUsage(resp["usageMetadata"]); ok {
		obs.Usage = usage
	}
	return obs, nil
}

func parseGeminiSystemInstruction(raw json.RawMessage) []SemanticNode {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	node := parseGeminiContent(raw, "request", "$.systemInstruction", 0)
	node.NormalizedType = NodeInstruction
	node.Role = firstNonEmpty(node.Role, "system")
	return []SemanticNode{node}
}

func parseGeminiContents(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(items))
	for i, item := range items {
		nodes = append(nodes, parseGeminiContent(item, section, fmt.Sprintf("%s[%d]", basePath, i), i))
	}
	return nodes
}

func parseGeminiContent(raw json.RawMessage, section string, path string, index int) SemanticNode {
	obj, _ := decodeJSONObject(raw)
	role := stringField(obj, "role")
	parts := parseGeminiParts(obj["parts"], section, path+".parts", role)
	return SemanticNode{
		ID:             StableNodeID(section, path, "content", index),
		ProviderType:   "content",
		NormalizedType: NodeMessage,
		Role:           role,
		Path:           path,
		Index:          index,
		Text:           firstNodeText(parts),
		Raw:            cloneRaw(raw),
		Metadata:       map[string]any{"role": role},
		Children:       parts,
	}
}

func parseGeminiParts(raw json.RawMessage, section string, basePath string, role string) []SemanticNode {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	nodes := make([]SemanticNode, 0, len(parts))
	for i, part := range parts {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		nodes = append(nodes, parseGeminiPart(part, section, path, i, role))
	}
	return nodes
}

func parseGeminiPart(raw json.RawMessage, section string, path string, index int, role string) SemanticNode {
	obj, _ := decodeJSONObject(raw)
	providerType := geminiPartProviderType(obj)
	normalized := normalizedGeminiPart(providerType, obj)
	if boolField(obj, "thought") && normalized == NodeText {
		normalized = NodeReasoning
	}
	node := SemanticNode{
		ID:             StableNodeID(section, path, providerType, index),
		ProviderType:   providerType,
		NormalizedType: normalized,
		Role:           role,
		Path:           path,
		Index:          index,
		Text:           geminiPartText(obj, providerType),
		Raw:            cloneRaw(raw),
		Metadata:       map[string]any{},
	}
	for _, key := range []string{"functionCall", "functionResponse", "executableCode", "codeExecutionResult", "toolCall", "toolResponse", "partMetadata"} {
		if raw := obj[key]; len(raw) > 0 && string(raw) != "null" {
			node.Metadata[key] = cloneRaw(raw)
		}
	}
	if sig := stringField(obj, "thoughtSignature"); sig != "" {
		node.Metadata["thoughtSignature"] = sig
	}
	if name := geminiPartName(obj, providerType); name != "" {
		node.Metadata["name"] = name
	}
	return node
}

func parseGeminiTools(raw json.RawMessage, section string, basePath string) []SemanticNode {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	var nodes []SemanticNode
	for i, item := range items {
		path := fmt.Sprintf("%s[%d]", basePath, i)
		obj, _ := decodeJSONObject(item)
		if fnsRaw := obj["functionDeclarations"]; len(fnsRaw) > 0 {
			var fns []json.RawMessage
			_ = json.Unmarshal(fnsRaw, &fns)
			for j, fnRaw := range fns {
				fnObj, _ := decodeJSONObject(fnRaw)
				fnPath := fmt.Sprintf("%s.functionDeclarations[%d]", path, j)
				metadata := map[string]any{
					"name":        stringField(fnObj, "name"),
					"kind":        "functionDeclaration",
					"description": stringField(fnObj, "description"),
				}
				if params := firstRaw(fnObj["parameters"], fnObj["parametersJsonSchema"]); len(params) > 0 {
					metadata["parameters"] = cloneRaw(params)
				}
				nodes = append(nodes, SemanticNode{
					ID:             StableNodeID(section, fnPath, "functionDeclaration", j),
					ProviderType:   "functionDeclaration",
					NormalizedType: NodeToolDeclaration,
					Path:           fnPath,
					Index:          len(nodes),
					Text:           stringField(fnObj, "name"),
					Raw:            cloneRaw(fnRaw),
					Metadata:       metadata,
				})
			}
		}
		for _, key := range []string{"googleSearchRetrieval", "codeExecution", "googleSearch", "computerUse", "urlContext", "fileSearch", "mcpServers", "googleMaps"} {
			if raw := obj[key]; len(raw) > 0 && string(raw) != "null" {
				nodes = append(nodes, SemanticNode{
					ID:             StableNodeID(section, path+"."+key, key, i),
					ProviderType:   key,
					NormalizedType: NodeToolDeclaration,
					Path:           path + "." + key,
					Index:          len(nodes),
					Text:           key,
					Raw:            cloneRaw(raw),
					Metadata:       map[string]any{"name": key, "kind": key},
				})
			}
		}
	}
	return nodes
}

func parseGeminiCandidates(raw json.RawMessage, obs *TraceObservation) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for i, item := range items {
		path := fmt.Sprintf("$.candidates[%d]", i)
		obj, _ := decodeJSONObject(item)
		content := parseGeminiContent(obj["content"], "response", path+".content", i)
		node := SemanticNode{
			ID:             StableNodeID("response", path, "candidate", i),
			ProviderType:   "candidate",
			NormalizedType: NodeMessage,
			Role:           content.Role,
			Path:           path,
			Index:          i,
			Text:           content.Text,
			Raw:            cloneRaw(item),
			Metadata: map[string]any{
				"finishReason":  stringField(obj, "finishReason"),
				"finishMessage": stringField(obj, "finishMessage"),
			},
			Children: []SemanticNode{content},
		}
		obs.Response.Candidates = append(obs.Response.Candidates, node)
		obs.Response.Nodes = append(obs.Response.Nodes, node)
		appendGeminiResponseNodes(content.Children, obs)
		if safety := parseNullableObjectNode(obj["safetyRatings"], "response", path+".safetyRatings", "safetyRatings", NodeSafety); safety.ID != "" {
			obs.Response.Safety = append(obs.Response.Safety, safety)
			node.Children = append(node.Children, safety)
		}
		finishReason := stringField(obj, "finishReason")
		if geminiFinishReasonBlocked(finishReason) {
			obs.Safety.Blocked = true
		}
	}
}

func parseGeminiStream(body []byte, obs *TraceObservation) {
	var (
		eventIndex       int
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
	)
	scanSSEData(body, func(data string) {
		raw := json.RawMessage(data)
		obj, err := decodeJSONObject(raw)
		if err != nil {
			obs.Warnings = append(obs.Warnings, ParseWarning{Code: "invalid_stream_json", Message: err.Error(), Path: "$"})
			return
		}
		path := fmt.Sprintf("$.stream.events[%d]", eventIndex)
		obs.Stream.Events = append(obs.Stream.Events, streamEvent(eventIndex, "generateContent.chunk", "GenerateContentResponse", NodeUnknown, path, "", raw))
		parseGeminiCandidates(obj["candidates"], obs)
		for _, node := range obs.Response.ToolCalls {
			if node.Path == "" {
				continue
			}
			if !containsNode(obs.Stream.AccumulatedToolCalls, node.ID) {
				obs.Stream.AccumulatedToolCalls = append(obs.Stream.AccumulatedToolCalls, node)
			}
		}
		for _, node := range allGeminiLatestNodes(obj) {
			switch node.NormalizedType {
			case NodeText:
				textBuilder.WriteString(node.Text)
			case NodeReasoning:
				reasoningBuilder.WriteString(node.Text)
			}
		}
		if promptFeedback := parseNullableObjectNode(obj["promptFeedback"], "response", path+".promptFeedback", "promptFeedback", NodeSafety); promptFeedback.ID != "" {
			obs.Response.Safety = append(obs.Response.Safety, promptFeedback)
			obs.Safety.Blocked = true
		}
		if usage, ok := parseGeminiUsage(obj["usageMetadata"]); ok {
			obs.Usage = usage
		}
		eventIndex++
	})
	obs.Stream.AccumulatedText = textBuilder.String()
	obs.Stream.AccumulatedReasoning = reasoningBuilder.String()
}

func appendGeminiResponseNodes(nodes []SemanticNode, obs *TraceObservation) {
	for _, node := range nodes {
		switch node.NormalizedType {
		case NodeText, NodeImage, NodeFile, NodeCitation, NodeUnknown:
			obs.Response.Outputs = append(obs.Response.Outputs, node)
		case NodeReasoning:
			obs.Response.Reasoning = append(obs.Response.Reasoning, node)
		case NodeToolCall, NodeServerToolCall:
			obs.Response.ToolCalls = append(obs.Response.ToolCalls, node)
			obs.Tools.Calls = append(obs.Tools.Calls, geminiToolCallFromNode(node))
		case NodeToolResult, NodeServerToolResult:
			obs.Response.ToolResults = append(obs.Response.ToolResults, node)
			obs.Tools.Results = append(obs.Tools.Results, geminiToolResultFromNode(node))
		case NodeCode:
			obs.Response.Outputs = append(obs.Response.Outputs, node)
		case NodeCodeResult:
			obs.Response.ToolResults = append(obs.Response.ToolResults, node)
		case NodeSafety:
			obs.Response.Safety = append(obs.Response.Safety, node)
		}
	}
}

func appendGeminiToolObservations(nodes []SemanticNode, obs *TraceObservation) {
	for _, node := range nodes {
		switch node.NormalizedType {
		case NodeToolCall, NodeServerToolCall:
			obs.Tools.Calls = append(obs.Tools.Calls, geminiToolCallFromNode(node))
		case NodeToolResult, NodeServerToolResult:
			obs.Tools.Results = append(obs.Tools.Results, geminiToolResultFromNode(node))
		}
		appendGeminiToolObservations(node.Children, obs)
	}
}

func geminiToolCallFromNode(node SemanticNode) ToolCallObservation {
	args := firstRaw(rawMessageFromMetadata(node.Metadata, "functionCall"), rawMessageFromMetadata(node.Metadata, "toolCall"))
	return ToolCallObservation{
		ID:       firstNonEmpty(metadataString(node.Metadata, "id"), node.ID),
		Name:     firstNonEmpty(metadataString(node.Metadata, "name"), node.Text),
		Kind:     node.ProviderType,
		Owner:    geminiToolOwner(node.ProviderType, true),
		ArgsText: string(args),
		ArgsJSON: args,
		NodeID:   node.ID,
		Path:     node.Path,
	}
}

func geminiToolResultFromNode(node SemanticNode) ToolResultObservation {
	raw := firstRaw(rawMessageFromMetadata(node.Metadata, "functionResponse"), rawMessageFromMetadata(node.Metadata, "toolResponse"), rawMessageFromMetadata(node.Metadata, "codeExecutionResult"))
	return ToolResultObservation{
		ID:     firstNonEmpty(metadataString(node.Metadata, "id"), node.ID),
		Name:   firstNonEmpty(metadataString(node.Metadata, "name"), node.Text),
		Kind:   node.ProviderType,
		Owner:  geminiToolOwner(node.ProviderType, false),
		Text:   node.Text,
		JSON:   raw,
		NodeID: node.ID,
		Path:   node.Path,
		IsError: strings.Contains(strings.ToLower(node.ProviderType), "error") ||
			strings.Contains(strings.ToLower(node.Text), "error"),
	}
}

func geminiPartProviderType(obj map[string]json.RawMessage) string {
	for _, key := range []string{"text", "inlineData", "fileData", "functionCall", "functionResponse", "executableCode", "codeExecutionResult", "toolCall", "toolResponse"} {
		if raw := obj[key]; len(raw) > 0 && string(raw) != "null" {
			return key
		}
	}
	if boolField(obj, "thought") {
		return "thought"
	}
	return "part"
}

func normalizedGeminiPart(providerType string, obj map[string]json.RawMessage) NormalizedType {
	switch providerType {
	case "text":
		return NodeText
	case "thought":
		return NodeReasoning
	case "inlineData":
		return NodeImage
	case "fileData":
		return NodeFile
	case "functionCall":
		return NodeToolCall
	case "functionResponse":
		return NodeToolResult
	case "executableCode":
		return NodeCode
	case "codeExecutionResult":
		return NodeCodeResult
	case "toolCall":
		return NodeServerToolCall
	case "toolResponse":
		return NodeServerToolResult
	default:
		return NodeUnknown
	}
}

func geminiPartText(obj map[string]json.RawMessage, providerType string) string {
	switch providerType {
	case "text":
		return stringField(obj, "text")
	case "functionCall", "functionResponse", "executableCode", "codeExecutionResult", "toolCall", "toolResponse":
		return textFromRaw(obj[providerType])
	case "fileData", "inlineData":
		return textFromRaw(obj[providerType])
	case "thought":
		return stringField(obj, "text")
	default:
		return textFromRaw(firstRaw(obj["text"], obj["partMetadata"]))
	}
}

func geminiPartName(obj map[string]json.RawMessage, providerType string) string {
	partObj, err := decodeJSONObject(obj[providerType])
	if err != nil {
		return ""
	}
	return stringField(partObj, "name")
}

func geminiToolOwner(providerType string, isCall bool) ToolOwner {
	switch providerType {
	case "toolCall", "toolResponse", "executableCode", "codeExecutionResult":
		return ToolOwnerProviderExecuted
	case "functionResponse":
		return ToolOwnerClientExecuted
	case "functionCall":
		return ToolOwnerModelRequested
	default:
		if isCall {
			return ToolOwnerModelRequested
		}
		return ToolOwnerUnknown
	}
}

func parseGeminiUsage(raw json.RawMessage) (ObservationUsage, bool) {
	obj, err := decodeJSONObject(raw)
	if err != nil {
		return ObservationUsage{}, false
	}
	return ObservationUsage{
		InputTokens:  intField(obj, "promptTokenCount"),
		OutputTokens: intField(obj, "candidatesTokenCount"),
		TotalTokens:  intField(obj, "totalTokenCount"),
	}, true
}

func geminiFinishReasonBlocked(reason string) bool {
	switch reason {
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MALFORMED_RESPONSE":
		return true
	default:
		return false
	}
}

func allGeminiLatestNodes(obj map[string]json.RawMessage) []SemanticNode {
	var out []SemanticNode
	var candidates []json.RawMessage
	_ = json.Unmarshal(obj["candidates"], &candidates)
	for i, item := range candidates {
		candObj, _ := decodeJSONObject(item)
		contentObj, _ := decodeJSONObject(candObj["content"])
		out = append(out, parseGeminiParts(contentObj["parts"], "response", fmt.Sprintf("$.candidates[%d].content.parts", i), stringField(contentObj, "role"))...)
	}
	return out
}

func containsNode(nodes []SemanticNode, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 && string(value) != "null" {
			return value
		}
	}
	return nil
}
