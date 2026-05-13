# v1 总体架构

## 架构原则

v1 的架构原则是：

> 转发保可用，录制保事实，解析走异步，分析可重算，展示可追溯。

这意味着：

- LLM 请求转发路径不能因为深度解析变慢。
- raw cassette 永远是事实来源。
- 协议解析、审计扫描、LLM 总结都属于派生过程。
- 派生结果可以失败、重试、升级和重算。

## 五层平面

```text
Client / SDK / AI Tool
  -> Forwarding Plane
    -> Upstream Router
      -> LLM Provider

Forwarding Plane
  -> Raw Capture Plane
    -> Cassette Store
    -> SQLite Lightweight Index

Semantic Parse Plane
  -> Observation IR
  -> Derived Semantic Store

Analysis Plane
  -> Risk Findings
  -> Sensitive Data Findings
  -> Performance Findings
  -> Behavior Summaries

Presentation Plane
  -> Monitor UI
  -> MCP Tools
  -> Reports / Exports
```

## 1. Forwarding Plane

### 目标

保证 LLM 请求高效、稳定地转发到可用上游。

### 职责

- 接收 OpenAI-compatible、OpenAI Responses、Anthropic Messages、Google GenAI、Vertex-native 等请求。
- 根据配置选择 upstream。
- 应用 auth、header、path、query 改写。
- 支持多 upstream 的负载均衡和 fallback。
- 采集轻量运行指标：
  - upstream id
  - routing policy
  - routing score
  - status code
  - duration
  - TTFT
  - stream flag

### 禁止事项

- 不做深度协议解析。
- 不运行敏感信息扫描。
- 不调用 LLM 做分析。
- 不默认改写用户 payload。

### 当前代码基础

- `internal/proxy`
- `internal/router`
- `internal/upstream`

## 2. Raw Capture Plane

### 目标

完整保存 HTTP 请求和响应，使回放、排查和后续分析都有可靠证据。

### 职责

- 写入 `LLM_PROXY_V3` cassette。
- 保留 raw request 和 raw response。
- 保留 stream 原始数据。
- 在 prelude 中写入轻量 `meta` 和 `event`。
- 将 trace list/filter/session/upstream 所需字段索引进 SQLite。

### 不变量

- raw cassette 是 replay 的唯一事实来源。
- SQLite 派生索引不能替代 cassette。
- 语义解析失败不能影响 raw 录制和 replay。

### 当前代码基础

- `internal/recorder`
- `pkg/recordfile`
- `internal/store`

## 3. Semantic Parse Plane

### 目标

从 raw cassette 中异步解析 provider 原生协议，生成保真的 Observation IR。

### 职责

- 识别 provider 和 operation。
- 解析请求中的指令、消息、上下文、工具声明、多模态内容。
- 解析响应中的文本、reasoning/thinking、工具调用、工具结果、拒绝、安全信号。
- 解析 stream event 并累积成语义事件。
- 保留 provider 原生类型、JSON path、unknown fields。

### 当前问题

当前 `pkg/llm.LLMRequest` 和 `pkg/llm.LLMResponse` 更适合摘要和互转，不适合完整协议还原。v1 应新增更保真的 Observation IR，而不是继续把所有 provider 对象压扁到 `LLMContent`。

### 建议包边界

```text
pkg/observe
  ir.go
  parser.go
  openai_chat.go
  openai_responses.go
  anthropic_messages.go
  gemini_generate_content.go
  stream.go
  finding.go
```

## 4. Analysis Plane

### 目标

基于 Observation IR 做安全、合规、性能和行为分析。

### 职责

- 危险命令识别。
- 敏感信息识别。
- 工具调用风险分类。
- provider safety/refusal 统一归类。
- 性能指标计算。
- session/model/provider 横向比较。
- 可选 LLM 总结与经验沉淀。

### 设计要求

- 完全异步。
- 可重跑。
- 分析器有版本。
- findings 有证据路径。
- 不影响转发和 replay。

## 5. Presentation Plane

### 目标

把复杂协议和分析结果以人类、AI agent 都能理解的形式展示出来。

### 主要界面

- Monitor UI。
- MCP 查询工具。
- 报表导出。

### v1 目标视图

- `Conversation`: 人类可读对话还原。
- `Protocol`: provider 原生协议结构树。
- `Audit`: 风险和敏感信息发现。
- `Performance`: 延迟、TTFT、tokens/s、缓存率、路由表现。
- `Raw`: 原始 HTTP/cassette。

## 三条管道

### Forwarding Pipeline

同步热路径：

```text
request -> route -> upstream -> response -> client
```

要求：

- 低延迟。
- 高可用。
- 可流式转发。
- 不等待解析和分析。

### Recording Pipeline

准同步或缓冲管道：

```text
captured bytes -> cassette writer -> lightweight index
```

要求：

- 尽量完整保存。
- 写失败可观测。
- 不破坏 replay 格式。

### Analysis Pipeline

异步管道：

```text
cassette -> parser -> Observation IR -> scanners -> findings -> UI/MCP
```

要求：

- 可失败。
- 可重试。
- 可重算。
- 可按 parser/analyzer version 管理。

## Trace 状态机

建议为每条 trace 增加派生状态：

```text
recorded
indexed
parse_queued
parsed
parse_failed
analysis_queued
analyzed
analysis_failed
```

状态说明：

- `recorded`: raw cassette 已存在。
- `indexed`: SQLite 基础索引已写入。
- `parsed`: Observation IR 已生成。
- `analyzed`: 风险/性能/行为 findings 已生成。

Monitor 应明确展示这些状态，避免用户误以为“无发现”等同于“已分析且安全”。

## 关键不变量

- `pkg/replay` 不能依赖 Observation IR。
- 旧 cassette 必须可继续读。
- v1 新增表必须 additive migration。
- parser 对 unknown fields 必须 tolerant。
- raw path 和 evidence path 必须可追溯。
- 所有生成的分析结果都必须能从 raw cassette 重算。
