# v1 Monitor 体验设计

## 目标

v1 Monitor 不只是 raw HTTP 查看器，而是面向 LLM 行为排查的工作台。

它需要同时服务四类问题：

- 对话发生了什么。
- 协议真实传输了什么。
- 是否存在风险。
- 性能和上游是否异常。

## 信息架构

Trace Detail 建议改为五个主 tab：

```text
Conversation
Protocol
Audit
Performance
Raw
```

Session Detail 和 Upstream Detail 继续保留，但需要能跳转到这五类视图。

## Conversation View

### 目标

用人类可读方式还原一次 LLM 交互。

### 展示内容

- system/developer instruction。
- user messages。
- assistant/model messages。
- reasoning/thinking。
- tool call。
- tool result。
- refusal。
- safety block。

### 展示原则

- 按实际发生顺序展示。
- 工具调用和结果 inline 展示。
- 对危险工具调用打风险标记。
- 对敏感信息显示 finding badge。
- 对 reasoning/thinking 显示来源和可见性状态。

## Protocol View

### 目标

用 provider 原生结构展示协议内容。

### 展示内容

OpenAI Chat：

- `messages[]`
- `choices[]`
- `tool_calls`
- `usage`

OpenAI Responses：

- `input`
- `output[]`
- `ResponseStreamEvent`
- `previous_response_id`
- `tools`

Claude：

- `system`
- `messages[]`
- `content[]`
- SSE event flow

Gemini：

- `systemInstruction`
- `contents[]`
- `parts[]`
- `candidates[]`
- `usageMetadata`

### 节点展示字段

每个节点至少展示：

- provider type。
- normalized type。
- role。
- JSON path。
- text preview。
- metadata。
- raw JSON。

## Audit View

### 目标

集中展示风险和敏感信息。

### 展示内容

- finding 列表。
- severity。
- confidence。
- category。
- evidence excerpt。
- evidence path。
- detector/version。
- 跳转到 Conversation / Protocol 的对应节点。

### 交互

- 按 severity 过滤。
- 按 category 过滤。
- 展开证据。
- 复制 evidence path。

## Performance View

### 目标

展示一次请求的推理服务表现和路由上下文。

### 展示内容

- total latency。
- TTFT。
- tokens/s。
- prompt tokens。
- completion/output tokens。
- total tokens。
- cache read tokens。
- cache hit ratio。
- upstream id。
- routing policy。
- routing score。
- candidate count。
- fallback。
- status code。
- provider error。

### Session 聚合

Session Detail 应展示：

- 总 token。
- 平均 TTFT。
- 平均 tokens/s。
- cache hit ratio。
- 上游分布。
- 失败请求。
- 风险 finding 数量。

### Upstream 聚合

Upstream Detail 应展示：

- 请求量。
- 成功率。
- p50/p95 latency。
- p50/p95 TTFT。
- tokens/s。
- 错误率。
- fallback 次数。
- 模型覆盖。

## Raw View

### 目标

保留事实源。

### 展示内容

- raw cassette prelude。
- raw HTTP request。
- raw HTTP response。
- raw stream event。

### 要求

- 不隐藏 raw。
- 可以从 Protocol node 跳转到 raw path。
- raw 内容可以后续支持 redacted projection，但默认保留原始事实。

## 列表页增强

### Requests

新增列或 filter：

- parse status。
- finding count。
- highest severity。
- selected upstream。
- TTFT。
- tokens/s。
- cache ratio。

### Sessions

新增：

- session finding count。
- highest severity。
- total cost proxy。
- most used tool。
- failed tool calls。
- dominant model/provider。

### Upstreams

新增：

- health timeline。
- parse/analyze coverage。
- safety/finding rate by upstream。

## MCP 展示接口

Monitor 的数据结构应能被 MCP 复用。

建议 MCP tools：

- `get_trace_observation`
- `list_trace_findings`
- `get_session_observation`
- `summarize_session_risks`
- `query_dangerous_tool_calls`
- `query_sensitive_data_findings`
- `compare_models_on_session`

## 可用性要求

- 大 trace 需要分页/折叠。
- 大 raw body 不应一次性渲染所有节点。
- 多模态内容默认显示摘要，不直接展开大 blob。
- finding 和 semantic node 必须互相跳转。
- 解析失败时仍显示 Raw View。
