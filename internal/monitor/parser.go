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
	ChatMessages []ChatMessage
	AIContent    string
	AIReasoning  string
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // 当 role=tool 时存在
	Name       string     `json:"name,omitempty"`         // 当 role=tool 时可能是函数名
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

// ParseLogFile 解析 V2/V3 格式的日志文件
func ParseLogFile(content []byte) (*ParsedData, error) {
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return nil, fmt.Errorf("invalid header: %w", err)
	}
	header := parsed.Header
	reqFullBytes, reqBodyBytes, resFullBytes, resBodyBytes := recordfile.ExtractSections(content, parsed)

	// 4. 解析 Request (自适应 Chat / Embedding / Reranker)
	var reqRaw chatRequest
	var messages []ChatMessage
	// 尝试解析 JSON
	if json.Unmarshal(reqBodyBytes, &reqRaw) == nil {
		if len(reqRaw.Messages) > 0 {
			// Case A: 标准 Chat 请求
			messages = reqRaw.Messages
		} else if reqRaw.Input != nil {
			// Case B: Embedding 请求 (转换成伪造的 User Message 以便展示)
			contentStr := formatInput(reqRaw.Input)
			messages = append(messages, ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("🧮 **Embedding Input**:\n%s", contentStr),
			})
		} else if reqRaw.Query != "" {
			// Case C: Reranker 请求
			docList := strings.Join(reqRaw.Documents, "\n- ")
			content := fmt.Sprintf("🔍 **Rerank Query**: %s\n\n📄 **Documents**:\n- %s", reqRaw.Query, docList)
			messages = append(messages, ChatMessage{
				Role:    "user",
				Content: content,
			})
		}
	}
	// ================= 修改点结束 =================

	// 5. 解析 AI Content & Reasoning
	contentStr, reasoningStr := parseAIContent(resBodyBytes, header.Layout.IsStream)

	return &ParsedData{
		Header:       header,
		Events:       parsed.Events,
		ReqFull:      string(reqFullBytes),
		ResFull:      string(resFullBytes),
		ChatMessages: messages,
		AIContent:    contentStr,
		AIReasoning:  reasoningStr,
		// ReqBody:      string(reqBodyBytes), // 也可以加上
		// ResBody:      string(resBodyBytes), // 也可以加上
	}, nil
}

func parseAIContent(data []byte, isStream bool) (string, string) {
	if len(data) == 0 {
		return "", ""
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
			return resp.Choices[0].Message.Content, resp.Choices[0].Message.ReasoningContent
		}
		return "", ""
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
	return contentBuilder.String(), reasoningBuilder.String()
}

// 辅助函数：格式化 Embedding Input (支持 string 和 []string)
func formatInput(input interface{}) string {
	switch v := input.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, "- "+s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		b, _ := json.MarshalIndent(input, "", "  ")
		return string(b)
	}
}
