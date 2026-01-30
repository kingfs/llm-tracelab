package llm

import (
	"encoding/json"
	"sync"
)

type JSONSchema = json.RawMessage

// ========== 统一内部抽象：请求 ==========

type LLMRequest struct {
	Model string `json:"model"`

	System   []LLMContent `json:"system,omitempty"`
	Messages []LLMMessage `json:"messages,omitempty"`
	Tools    []LLMTool    `json:"tools,omitempty"`

	ToolChoice string   `json:"tool_choice,omitempty"`
	StopSeq    []string `json:"stop_sequences,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`

	SafetySettings []LLMSafetyConfig `json:"safety_settings,omitempty"`

	// 热路径上只用 UserID，避免 map[string]any
	UserID string `json:"user_id,omitempty"`

	// 非热路径扩展字段（不在 benchmark 中使用）
	Extensions map[string]any `json:"extensions,omitempty"`
}

type LLMMessage struct {
	Role    string       `json:"role"` // user, assistant, system, tool, model
	Content []LLMContent `json:"content"`
}

type LLMContent struct {
	Type string `json:"type"` // text, image, audio, video, tool_use, tool_result

	Text string `json:"text,omitempty"`

	ImageData []byte `json:"image_data,omitempty"`
	AudioData []byte `json:"audio_data,omitempty"`
	VideoData []byte `json:"video_data,omitempty"`

	ToolName   string         `json:"tool_name,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ToolResult map[string]any `json:"tool_result,omitempty"`
}

type LLMTool struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  JSONSchema `json:"parameters,omitempty"`
}

type LLMSafetyConfig struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// ========== 统一内部抽象：响应 ==========

type LLMResponse struct {
	ID        string `json:"id,omitempty"`
	Model     string `json:"model,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`

	Candidates []LLMCandidate `json:"candidates"`

	Usage  *LLMUsage         `json:"usage,omitempty"`
	Safety []LLMSafetyRating `json:"safety,omitempty"`

	Extensions map[string]any `json:"extensions,omitempty"`
}

type LLMCandidate struct {
	Index        int          `json:"index,omitempty"`
	Role         string       `json:"role,omitempty"` // assistant / tool / system / model
	Content      []LLMContent `json:"content"`
	FinishReason string       `json:"finish_reason,omitempty"`

	ToolCalls []LLMToolCall `json:"tool_calls,omitempty"`
	Refusal   *LLMRefusal   `json:"refusal,omitempty"`

	Extensions map[string]any `json:"extensions,omitempty"`
}

type LLMToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type LLMRefusal struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type LLMUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	AudioTokens     int `json:"audio_tokens,omitempty"`

	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type LLMSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked"`
}

// ========== sync.Pool 优化（零拷贝 slice 复用） ==========

var contentSlicePool = sync.Pool{
	New: func() any { return make([]LLMContent, 0, 8) },
}

func getContentSlice() []LLMContent {
	return contentSlicePool.Get().([]LLMContent)[:0]
}

func putContentSlice(s []LLMContent) {
	if cap(s) <= 64 {
		contentSlicePool.Put(s[:0])
	}
}

var candidateSlicePool = sync.Pool{
	New: func() any { return make([]LLMCandidate, 0, 4) },
}

func getCandidateSlice() []LLMCandidate {
	return candidateSlicePool.Get().([]LLMCandidate)[:0]
}

func putCandidateSlice(s []LLMCandidate) {
	if cap(s) <= 32 {
		candidateSlicePool.Put(s[:0])
	}
}
