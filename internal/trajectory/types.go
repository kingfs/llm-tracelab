package trajectory

import "time"

const SchemaVersion = "ATIF-v1.8"

type Trajectory struct {
	SchemaVersion string         `json:"schema_version"`
	SessionID     string         `json:"session_id"`
	Agent         Agent          `json:"agent"`
	Steps         []Step         `json:"steps"`
	FinalMetrics  FinalMetrics   `json:"final_metrics"`
	Extra         map[string]any `json:"extra,omitempty"`
}
type Agent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type Step struct {
	StepID           int            `json:"step_id"`
	Timestamp        string         `json:"timestamp,omitempty"`
	Source           string         `json:"source"`
	Message          string         `json:"message"`
	ModelName        string         `json:"model_name,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ReasoningEffort  string         `json:"reasoning_effort,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	Observation      *Observation   `json:"observation,omitempty"`
	Metrics          *Metrics       `json:"metrics,omitempty"`
	LLMCallCount     *int           `json:"llm_call_count,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}
type Metrics struct {
	PromptTokens     *int64         `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64         `json:"completion_tokens,omitempty"`
	CachedTokens     *int64         `json:"cached_tokens,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}
type FinalMetrics struct {
	TotalSteps            int            `json:"total_steps"`
	TotalPromptTokens     *int64         `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens *int64         `json:"total_completion_tokens,omitempty"`
	TotalCachedTokens     *int64         `json:"total_cached_tokens,omitempty"`
	Extra                 map[string]any `json:"extra,omitempty"`
}
type ToolCall struct {
	ToolCallID   string         `json:"tool_call_id"`
	FunctionName string         `json:"function_name"`
	Arguments    map[string]any `json:"arguments"`
}
type Observation struct {
	Results []Result `json:"results"`
}
type Result struct {
	SourceCallID string         `json:"source_call_id,omitempty"`
	Content      string         `json:"content"`
	Extra        map[string]any `json:"extra,omitempty"`
}
type Warning struct {
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

// Exchange contains only client-visible exchanges, never internal model children.
type Exchange struct {
	TraceID               string
	Time                  time.Time
	DurationMs            int64
	Model, Endpoint       string
	StatusCode            int
	Request, Response     []byte
	Stream                bool
	Error                 string
	ExchangeKind          string
	UserAgent, Originator string
}
type item map[string]any
