package llm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type UsageSummary = recordfile.UsageInfo

type ResponsePipeline struct {
	provider  string
	endpoint  string
	isStream  bool
	startedAt time.Time

	lineBuf  []byte
	tailBuf  []byte
	usage    UsageSummary
	hasUsage bool
	events   []recordfile.RecordEvent
}

func NewResponsePipeline(provider string, endpoint string, isStream bool) *ResponsePipeline {
	return &ResponsePipeline{
		provider:  provider,
		endpoint:  endpoint,
		isStream:  isStream,
		startedAt: time.Now(),
	}
}

func DetectStreamingResponse(header http.Header) bool {
	if header == nil {
		return false
	}
	if strings.Contains(header.Get("Content-Type"), "text/event-stream") {
		return true
	}
	return strings.EqualFold(header.Get("Transfer-Encoding"), "chunked")
}

func (p *ResponsePipeline) Feed(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if p.isStream {
		p.feedStream(chunk)
		return
	}
	p.feedNonStream(chunk)
}

func (p *ResponsePipeline) Finalize() {
	if p.isStream || p.hasUsage {
		return
	}
	if usage, ok := ExtractUsageFromTail(p.tailBuf); ok {
		p.usage = usage
		p.hasUsage = true
		p.appendUsageEvent(usage)
	}
}

func (p *ResponsePipeline) Usage() (UsageSummary, bool) {
	return p.usage, p.hasUsage
}

func (p *ResponsePipeline) Events() []recordfile.RecordEvent {
	if len(p.events) == 0 {
		return nil
	}
	out := make([]recordfile.RecordEvent, len(p.events))
	copy(out, p.events)
	return out
}

func (p *ResponsePipeline) feedStream(chunk []byte) {
	p.lineBuf = append(p.lineBuf, chunk...)
	if len(p.lineBuf) > 64*1024 {
		copy(p.lineBuf, p.lineBuf[len(p.lineBuf)-64*1024:])
		p.lineBuf = p.lineBuf[:64*1024]
	}

	for {
		idx := bytes.IndexByte(p.lineBuf, '\n')
		if idx == -1 {
			break
		}
		line := p.lineBuf[:idx]
		p.lineBuf = p.lineBuf[idx+1:]
		lineStr := string(line)
		if !strings.HasPrefix(lineStr, "data:") {
			continue
		}
		jsonStr := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		if usage, ok := ExtractUsageFromJSON([]byte(jsonStr)); ok {
			p.usage = usage
			p.hasUsage = true
			p.appendUsageEvent(usage)
		}
		p.appendProviderEvent(jsonStr)
	}
}

func (p *ResponsePipeline) feedNonStream(chunk []byte) {
	const maxBuf = 4096
	if len(p.tailBuf)+len(chunk) > maxBuf {
		combined := append(p.tailBuf, chunk...)
		start := len(combined) - maxBuf
		if start < 0 {
			start = 0
		}
		p.tailBuf = combined[start:]
		return
	}
	p.tailBuf = append(p.tailBuf, chunk...)
}

func (p *ResponsePipeline) appendUsageEvent(usage UsageSummary) {
	p.appendEvent("llm.usage", "", map[string]interface{}{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
	})
}

func (p *ResponsePipeline) appendProviderEvent(jsonStr string) {
	switch NormalizeEndpoint(p.endpoint) {
	case "/v1/chat/completions":
		p.appendOpenAIChatEvent(jsonStr)
	case "/v1/responses":
		p.appendResponsesEvent(jsonStr)
	case "/v1/messages":
		p.appendAnthropicEvent(jsonStr)
	case "/v1beta/models:generateContent", "/v1beta/models:streamGenerateContent":
		p.appendGoogleEvent(jsonStr)
	}
}

func (p *ResponsePipeline) appendOpenAIChatEvent(jsonStr string) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          *string `json:"content"`
				ReasoningContent *string `json:"reasoning_content"`
				ToolCalls        []struct {
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
		return
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			p.appendEvent("llm.output_text.delta", *choice.Delta.Content, nil)
		}
		if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
			p.appendEvent("llm.reasoning.delta", *choice.Delta.ReasoningContent, nil)
		}
		for _, tc := range choice.Delta.ToolCalls {
			p.appendEvent("llm.tool_call.delta", "", map[string]interface{}{
				"id":        tc.ID,
				"type":      firstNonEmpty(tc.Type, "function"),
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			})
		}
	}
}

func (p *ResponsePipeline) appendResponsesEvent(jsonStr string) {
	var env struct {
		Type      string `json:"type"`
		Delta     string `json:"delta"`
		Arguments string `json:"arguments"`
		ItemID    string `json:"item_id"`
		Item      struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &env); err != nil {
		return
	}
	switch env.Type {
	case "response.output_text.delta":
		p.appendEvent("llm.output_text.delta", env.Delta, nil)
	case "response.refusal.delta":
		p.appendEvent("llm.output_text.delta", env.Delta, map[string]interface{}{"kind": "refusal"})
	case "response.reasoning_text.delta":
		p.appendEvent("llm.reasoning.delta", env.Delta, nil)
	case "response.reasoning_summary_text.delta":
		p.appendEvent("llm.reasoning.delta", env.Delta, nil)
	case "response.function_call_arguments.delta":
		p.appendEvent("llm.tool_call.delta", "", map[string]interface{}{
			"id":        env.ItemID,
			"arguments": env.Delta,
		})
	case "response.output_item.added", "response.output_item.done":
		if env.Item.Type == "function_call" || strings.HasSuffix(env.Item.Type, "_call") {
			p.appendEvent("llm.tool_call", "", map[string]interface{}{
				"id":   firstNonEmpty(env.Item.CallID, env.Item.ID),
				"name": firstNonEmpty(env.Item.Name, env.Item.Type),
				"type": env.Item.Type,
			})
		}
	}
}

func (p *ResponsePipeline) appendAnthropicEvent(jsonStr string) {
	var chunk struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
		return
	}
	switch chunk.Type {
	case "content_block_delta":
		switch chunk.Delta.Type {
		case "text_delta":
			p.appendEvent("llm.output_text.delta", chunk.Delta.Text, nil)
		case "thinking_delta":
			p.appendEvent("llm.reasoning.delta", firstNonEmpty(chunk.Delta.Thinking, chunk.Delta.Text), nil)
		case "input_json_delta":
			p.appendEvent("llm.tool_call.delta", "", map[string]interface{}{
				"index":     chunk.Index,
				"arguments": chunk.Delta.PartialJSON,
			})
		}
	case "content_block_start":
		if chunk.ContentBlock.Type == "tool_use" {
			p.appendEvent("llm.tool_call", "", map[string]interface{}{
				"id":   chunk.ContentBlock.ID,
				"name": chunk.ContentBlock.Name,
			})
		}
	}
}

func (p *ResponsePipeline) appendGoogleEvent(jsonStr string) {
	var chunk struct {
		Candidates []struct {
			Content struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
		return
	}
	for _, candidate := range chunk.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				p.appendEvent("llm.output_text.delta", part.Text, map[string]interface{}{
					"role": firstNonEmpty(candidate.Content.Role, "model"),
				})
			}
		}
	}
}

func (p *ResponsePipeline) appendEvent(eventType string, message string, attrs map[string]interface{}) {
	p.events = append(p.events, recordfile.RecordEvent{
		Type:       eventType,
		Time:       time.Now(),
		IsStream:   p.isStream,
		Message:    message,
		Attributes: attrs,
	})
}

type compatibleUsage struct {
	PromptTokens             int                            `json:"prompt_tokens"`
	CompletionTokens         int                            `json:"completion_tokens"`
	TotalTokens              int                            `json:"total_tokens"`
	InputTokens              int                            `json:"input_tokens"`
	OutputTokens             int                            `json:"output_tokens"`
	CacheCreationInputTokens int                            `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                            `json:"cache_read_input_tokens"`
	InputTokenDetails        *recordfile.PromptTokenDetails `json:"input_tokens_details,omitempty"`
	PromptTokenDetails       *recordfile.PromptTokenDetails `json:"prompt_tokens_details,omitempty"`
	PromptTokenCount         int                            `json:"promptTokenCount"`
	CandidatesTokenCount     int                            `json:"candidatesTokenCount"`
	TotalTokenCount          int                            `json:"totalTokenCount"`
}

func (u compatibleUsage) toUsageSummary() (UsageSummary, bool) {
	promptTokens := u.PromptTokens
	completionTokens := u.CompletionTokens
	promptDetails := u.PromptTokenDetails

	if promptTokens == 0 && completionTokens == 0 && (u.InputTokens > 0 || u.OutputTokens > 0) {
		promptTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		completionTokens = u.OutputTokens
		if promptDetails == nil {
			promptDetails = u.InputTokenDetails
		}
		if promptDetails == nil && (u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0) {
			promptDetails = &recordfile.PromptTokenDetails{
				CachedTokens: u.CacheReadInputTokens,
			}
		}
	}
	if promptTokens == 0 && completionTokens == 0 && (u.PromptTokenCount > 0 || u.CandidatesTokenCount > 0) {
		promptTokens = u.PromptTokenCount
		completionTokens = u.CandidatesTokenCount
	}

	totalTokens := u.TotalTokens
	if totalTokens == 0 && u.TotalTokenCount > 0 {
		totalTokens = u.TotalTokenCount
	}
	if totalTokens == 0 && (promptTokens > 0 || completionTokens > 0) {
		totalTokens = promptTokens + completionTokens
	}
	if totalTokens == 0 && promptTokens == 0 && completionTokens == 0 {
		return UsageSummary{}, false
	}

	return UsageSummary{
		PromptTokens:       promptTokens,
		CompletionTokens:   completionTokens,
		TotalTokens:        totalTokens,
		PromptTokenDetails: promptDetails,
	}, true
}

func ExtractUsageFromJSON(data []byte) (UsageSummary, bool) {
	var direct compatibleUsage
	if err := json.Unmarshal(data, &direct); err == nil {
		if usage, ok := direct.toUsageSummary(); ok {
			return usage, true
		}
	}

	var payload struct {
		Usage         *compatibleUsage `json:"usage"`
		UsageMetadata *compatibleUsage `json:"usageMetadata"`
		Response      *struct {
			Usage         *compatibleUsage `json:"usage"`
			UsageMetadata *compatibleUsage `json:"usageMetadata"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return UsageSummary{}, false
	}
	if payload.Usage != nil {
		if usage, ok := payload.Usage.toUsageSummary(); ok {
			return usage, true
		}
	}
	if payload.UsageMetadata != nil {
		if usage, ok := payload.UsageMetadata.toUsageSummary(); ok {
			return usage, true
		}
	}
	if payload.Response != nil && payload.Response.Usage != nil {
		if usage, ok := payload.Response.Usage.toUsageSummary(); ok {
			return usage, true
		}
	}
	if payload.Response != nil && payload.Response.UsageMetadata != nil {
		if usage, ok := payload.Response.UsageMetadata.toUsageSummary(); ok {
			return usage, true
		}
	}
	return UsageSummary{}, false
}

func ExtractUsageFromTail(data []byte) (UsageSummary, bool) {
	if len(data) == 0 {
		return UsageSummary{}, false
	}
	str := string(data)
	idx := strings.LastIndex(str, `"usage"`)
	if usageMetaIdx := strings.LastIndex(str, `"usageMetadata"`); usageMetaIdx > idx {
		idx = usageMetaIdx
	}
	if idx == -1 {
		return UsageSummary{}, false
	}
	segment := str[idx:]
	startBrace := strings.Index(segment, "{")
	if startBrace == -1 {
		return UsageSummary{}, false
	}
	jsonPart := segment[startBrace:]
	depth := 0
	endBrace := -1
	for i, r := range jsonPart {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				endBrace = i + 1
				break
			}
		}
	}
	if endBrace == -1 {
		return UsageSummary{}, false
	}
	return ExtractUsageFromJSON([]byte(jsonPart[:endBrace]))
}
