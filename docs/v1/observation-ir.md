# Observation IR 设计

## 背景

当前 `pkg/llm.LLMRequest` / `LLMResponse` 已经能做跨 provider 摘要，但它的目标更接近：

- 基础 normalize。
- 简单互转。
- Monitor 摘要展示。
- usage/timeline 提取。

v1 的深度协议解析需要更保真的中间表示，不能把 OpenAI Responses 的 `OutputItem`、Claude 的 `ContentBlock`、Gemini 的 `Part` 全部压成简单 `text/tool_use/tool_result`。

Observation IR 的目标是：

- 忠实还原 provider 协议。
- 支持统一展示。
- 支持审计和敏感信息检测。
- 支持长期行为分析。
- 支持从 raw cassette 重算。

## 顶层结构

```go
type TraceObservation struct {
    TraceID       string
    Provider      string
    Operation     string
    Endpoint      string
    Model         string
    Parser        string
    ParserVersion string
    Status        ParseStatus
    Warnings      []ParseWarning

    Request  ObservationRequest
    Response ObservationResponse
    Stream   ObservationStream
    Tools    ObservationTools
    Usage    ObservationUsage
    Timings  ObservationTimings
    Safety   ObservationSafety
    Findings []Finding
    RawRefs  RawReferences
}
```

## SemanticNode

`SemanticNode` 是 IR 的核心。它表示任意 provider 原生对象中的一个有意义节点。

```go
type SemanticNode struct {
    ID             string
    ProviderType   string
    NormalizedType string
    Role           string
    Path           string
    Index          int

    Text           string
    JSON           json.RawMessage
    Raw            json.RawMessage
    Metadata       map[string]any

    ParentID       string
    Children       []SemanticNode
}
```

字段含义：

- `ProviderType`: provider 原生类型，例如 `function_call`、`tool_use`、`functionCall`。
- `NormalizedType`: v1 统一类型，例如 `text`、`reasoning`、`tool_call`、`tool_result`。
- `Path`: 原始 JSON path，例如 `$.output[2].arguments`。
- `Raw`: 原始 JSON 节点。
- `JSON`: 规范化后的节点 JSON。
- `Metadata`: provider-specific 元数据。

## NormalizedType 枚举

建议第一版支持：

```text
instruction
message
text
reasoning
refusal
tool_declaration
tool_call
tool_call_delta
tool_result
server_tool_call
server_tool_result
code
code_result
patch
file
image
audio
video
citation
safety
usage
error
unknown
```

## Request IR

```go
type ObservationRequest struct {
    Instructions []SemanticNode
    Messages     []SemanticNode
    Inputs       []SemanticNode
    Tools        []SemanticNode
    Config       map[string]any
    Nodes        []SemanticNode
}
```

需要保留：

- OpenAI Chat `messages[]`
- OpenAI Responses `input` / `instructions`
- Claude `system` / `messages[]`
- Gemini `systemInstruction` / `contents[]`

## Response IR

```go
type ObservationResponse struct {
    Outputs      []SemanticNode
    Candidates   []SemanticNode
    ToolCalls    []SemanticNode
    ToolResults  []SemanticNode
    Reasoning    []SemanticNode
    Refusals     []SemanticNode
    Safety       []SemanticNode
    Errors       []SemanticNode
    Nodes        []SemanticNode
}
```

## Stream IR

```go
type ObservationStream struct {
    Events           []StreamEvent
    AccumulatedText  string
    AccumulatedReasoning string
    AccumulatedToolCalls []SemanticNode
    Errors           []SemanticNode
}
```

```go
type StreamEvent struct {
    Index          int
    EventType      string
    ProviderType   string
    NormalizedType string
    Path           string
    Delta          string
    JSON           json.RawMessage
    At             time.Time
}
```

## Tool IR

```go
type ObservationTools struct {
    Declarations []ToolDeclaration
    Calls        []ToolCallObservation
    Results      []ToolResultObservation
}
```

```go
type ToolCallObservation struct {
    ID        string
    Name      string
    Kind      string
    Owner     ToolOwner
    ArgsText  string
    ArgsJSON  json.RawMessage
    NodeID    string
    Path      string
}
```

```text
ToolOwner:
  model_requested
  client_executed
  provider_executed
  inferred
  unknown
```

## Findings

Findings 不直接写在 parser 里，但 IR 要能承载 findings。

```go
type Finding struct {
    ID              string
    Category        string
    Severity        string
    Confidence      float64
    Title           string
    Description     string
    EvidencePath    string
    EvidenceExcerpt string
    NodeID          string
    Detector        string
    DetectorVersion string
}
```

## RawReferences

```go
type RawReferences struct {
    CassettePath string
    RequestStart int64
    RequestEnd   int64
    ResponseStart int64
    ResponseEnd   int64
}
```

## 设计要求

- 所有 provider 原生对象必须能在 `SemanticNode.Raw` 找到。
- 解析器不能因为未知字段失败。
- 解析器应记录 unsupported node type。
- 展示层可以用 NormalizedType，但 Protocol View 必须显示 ProviderType。
- 审计层优先使用结构化节点，而不是扫描拼接后的全文。

## 与现有 LLMRequest/LLMResponse 的关系

`LLMRequest/LLMResponse` 保留，用于：

- 现有 monitor summary。
- adapter 兼容。
- 简单 usage/timeline。
- 后续可能的轻量互转。

Observation IR 用于：

- v1 Protocol View。
- Audit View。
- 高级分析。
- provider 行为横向比较。

不要把 Observation IR 回填到 hot path。
