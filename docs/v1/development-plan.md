# v1 开发执行计划

## 目标

本文档把 v1 设计拆成可执行、可验收、可提交的开发阶段。

v1 总目标保持不变：在现有 raw cassette、replay、多 upstream、Monitor 基础上，新增可重算的 Observation IR、协议级展示、审计 findings、性能分析和长期 session 分析。

重解析、重审计、usage repair、session/batch reanalysis 的完整执行设计见
[`../REANALYSIS_PIPELINE_DESIGN.md`](../REANALYSIS_PIPELINE_DESIGN.md)。

## 执行原则

- 不做大爆炸重写。
- 每个阶段只交付一个可验证闭环。
- 每个阶段完成后自动提交代码。
- 每次提交后重新 review v1 总目标和下一阶段计划，防止跑偏和需求扩散。
- `pkg/replay` 不能依赖 Observation IR、semantic nodes 或 findings。
- raw `.http` cassette 始终是 replay 和 detail evidence 的事实来源。
- 派生数据必须可以从 raw cassette 重算。
- schema 变更只能 additive，已有本地 SQLite DB 启动升级必须成功。
- parser 实现前必须读取对应 provider 的 reference snapshot。

## Reference Materials 使用规则

协议实现优先使用 [`reference-materials/README.md`](./reference-materials/README.md) 中登记的 2026-05-13 快照材料。

### OpenAI

实现 OpenAI Chat Completions 和 Responses parser 前，先读取：

- [`reference-materials/upstream/openai/schema-index-2026-05-13.md`](./reference-materials/upstream/openai/schema-index-2026-05-13.md)
- [`reference-materials/upstream/openai/chat-completions-core-schemas-2026-05-13.yml`](./reference-materials/upstream/openai/chat-completions-core-schemas-2026-05-13.yml)
- [`reference-materials/upstream/openai/responses-core-schemas-2026-05-13.yml`](./reference-materials/upstream/openai/responses-core-schemas-2026-05-13.yml)

### Anthropic Claude

实现 Claude Messages parser 前，先读取：

- [`reference-materials/upstream/anthropic/schema-index-2026-05-13.md`](./reference-materials/upstream/anthropic/schema-index-2026-05-13.md)
- [`reference-materials/upstream/anthropic/messages-api-2026-05-13.md`](./reference-materials/upstream/anthropic/messages-api-2026-05-13.md)
- [`reference-materials/upstream/anthropic/streaming-messages-2026-05-13.md`](./reference-materials/upstream/anthropic/streaming-messages-2026-05-13.md)
- [`reference-materials/upstream/anthropic/tool-use-overview-2026-05-13.md`](./reference-materials/upstream/anthropic/tool-use-overview-2026-05-13.md)

### Google Gemini

实现 Gemini GenerateContent 和 Vertex-native parser 前，先读取：

- [`reference-materials/upstream/google-gemini/schema-index-2026-05-13.md`](./reference-materials/upstream/google-gemini/schema-index-2026-05-13.md)
- [`reference-materials/upstream/google-gemini/generate-content-core-schemas-2026-05-13.json`](./reference-materials/upstream/google-gemini/generate-content-core-schemas-2026-05-13.json)

### 冲突处理

- 上游 schema 与实际 cassette fixture 冲突时，parser 应保持 tolerant。
- 冲突样本应进入 fixture。
- parser warning 或测试注释应说明冲突原因。
- OpenAI-compatible provider 只能声明兼容某个行为子集，不能默认等价于 OpenAI 官方 API。

## 目标包边界

建议新增或扩展以下模块：

```text
pkg/observe
  ir.go
  parser.go
  jsonpath.go
  stream.go
  openai_chat.go
  openai_responses.go
  anthropic_messages.go
  gemini_generate_content.go
  finding.go

internal/store
  observation and finding persistence APIs

internal/observeworker
  async parse job runner
  reparse orchestration

internal/analyzer
  deterministic detectors

cmd/server/analyze.go
  analyze reparse
  analyze scan

internal/monitor
  observation API
  finding API
  Protocol / Audit / Performance views

internal/mcpserver
  observation and finding query tools
```

`pkg/llm` 保留用于现有摘要、轻量 normalize、adapter 和 monitor 兼容展示。Observation IR 不回填到转发热路径。

## 阶段计划

### P0. 开发计划基线

目标：固化阶段执行计划、reference 使用规则、验收和复盘机制。

范围：

- 新增本文件。
- 在 v1 README 和 AGENTS 文档中链接本文件。
- 不修改运行时代码。

验收：

- 后续 AI agent 能从本文件理解阶段目标和边界。
- reference snapshot 的使用规则明确。
- 每阶段提交和复盘规则明确。

检查：

```bash
task fmt:check
task test
```

提交：

```text
docs: add v1 development execution plan
```

### P1. Observation IR v0

目标：建立独立、可序列化、不影响现有行为的 IR 类型层。

范围：

- 新增 `pkg/observe`。
- 定义 `TraceObservation`、`SemanticNode`、`Finding`、usage、timing、safety、raw refs 类型。
- 定义 normalized type、parse status、severity、tool owner 枚举。
- 定义 parser 接口和 parser registry。
- 定义内存 tree 与数据库 flat rows 的转换约定。
- 定义 stable node id 和 evidence path 规则。

验收：

- Go 类型可 JSON marshal/unmarshal。
- 测试覆盖 node tree、warnings、findings、raw refs。
- 不修改 `pkg/replay` 行为。
- 不替换 `pkg/llm`。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: add observation ir
```

### P2. OpenAI Deep Parser v0

目标：完成 OpenAI Chat Completions 和 Responses 非 streaming parser。

范围：

- 读取 OpenAI reference snapshots。
- 解析 Chat request/response。
- 解析 Responses request/response。
- 生成 SemanticNode tree。
- 保留 provider type、normalized type、role、JSON path、raw JSON、unknown fields。
- 解析 tools、tool call、tool result、reasoning、refusal、usage、provider error。

验收：

- fixture 覆盖 non-stream text、tool call、tool result、reasoning、refusal、provider error、unknown output item。
- OpenAI Responses `output[]` 顺序可通过 nodes 还原。
- unknown item 作为 `unknown` node 保留。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: add openai observation parser
```

### P3. OpenAI Streaming Parser

目标：解析 OpenAI Chat 和 Responses SSE，并累积语义事件。

范围：

- 解析 Chat `choices[].delta.*`。
- 解析 Responses `response.*` stream events。
- 累积 text、reasoning、tool arguments。
- 记录 stream events 的 provider type、normalized type、path、delta 和 raw JSON。
- 对无法识别的 stream event 产生 warning 或 unknown event。

验收：

- fixture 覆盖 Chat stream text、Chat stream tool call、Responses text delta、Responses reasoning delta、Responses function arguments delta、stream error。
- accumulated text/reasoning/tool call 与事件流一致。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: parse openai streaming observations
```

### P4. 派生存储与 Reparse CLI

目标：打通 raw cassette 到 Observation IR 再到 SQLite 派生表的手动闭环。

范围：

- 新增 additive tables：
  - `trace_observations`
  - `semantic_nodes`
  - `parse_jobs`
  - `parser_versions`
- 增加 store APIs：
  - save observation
  - get observation summary
  - list semantic nodes by trace
  - clear and rebuild semantic nodes by trace
- 新增 `analyze reparse --trace-id`。
- reparse 从 raw cassette 读取 request/response body，不依赖 live upstream。

验收：

- 单 trace reparse 可生成 observation rows。
- 重复 reparse 幂等。
- parse failed 状态和错误可见。
- 旧 DB 启动升级成功。
- replay 测试不受影响。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: persist trace observations
```

### P5. Protocol API 与 Monitor View

目标：把 Observation IR 的协议级结构展示出来，形成第一个 v1 用户可见闭环。

范围：

- 新增 trace observation API。
- Trace Detail 增加 `Protocol` tab。
- 展示 semantic node tree。
- 展示 provider type、normalized type、role、path、text preview、metadata、raw JSON。
- Raw View 保持可用。

验收：

- OpenAI Responses `output[]` 顺序可读。
- Chat messages、tool calls、usage 可读。
- unknown nodes 可见。
- parse failed 时 Protocol 明确显示失败，Raw 仍可查看。

检查：

```bash
task fmt:check
task test
task build
```

提交：

```text
feat: add protocol observation view
```

### P6. 异步 Parse Worker

目标：录制后自动生成 Observation IR，但不阻塞代理热路径。

范围：

- 录制成功后 enqueue parse job。
- 启动时扫描 pending jobs。
- worker 支持 retry、attempts、last_error。
- 支持手动 reparse 覆盖已解析结果。
- Monitor/API 暴露 parse status。

验收：

- enqueue 失败不导致请求失败。
- parse 失败可见。
- worker 不影响 replay。
- worker 不在转发路径做深度解析。

检查：

```bash
task fmt:check
task test
task bench:core
```

提交：

```text
feat: add async observation parser worker
```

### P7. Audit Findings v0

目标：建立 deterministic audit 闭环。

范围：

- 新增 `trace_findings`。
- 定义 detector 接口。
- 实现：
  - dangerous shell detector
  - credential detector
  - provider safety detector
  - tool error detector
- findings 关联 evidence path 和 node id。

验收：

- 每个 detector 有 fixture。
- findings 可入库并可按 trace 查询。
- evidence excerpt 有长度限制。
- detector version 变化后可重算。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: add audit findings
```

### P8. Audit View 与 MCP 查询

目标：让人类和 AI agent 都能使用 findings。

范围：

- Trace Detail 增加 `Audit` tab。
- 支持 severity/category filter。
- finding 可跳转到 Protocol node。
- MCP 增加：
  - `list_trace_findings`
  - `query_dangerous_tool_calls`
  - `query_sensitive_data_findings`

验收：

- Audit tab 显示 severity、category、confidence、evidence path、detector version。
- MCP 查询不依赖 live upstream。

检查：

```bash
task fmt:check
task test
task build
```

提交：

```text
feat: expose audit findings
```

### P9. Claude Deep Parser

目标：解析 Claude Messages request、response 和 streaming。

范围：

- 读取 Anthropic reference snapshots。
- 解析 top-level `system`。
- 解析 string content 和 content block array。
- 解析 `thinking`、`redacted_thinking`、`tool_use`、`tool_result`、server tool blocks。
- 解析 SSE event flow 和 `input_json_delta`。
- 标注 tool owner。

验收：

- content block 顺序可还原。
- client tool 和 server tool 可区分。
- thinking/redacted thinking 可见。
- usage 可见。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: add claude observation parser
```

### P10. Gemini / Vertex Parser

目标：解析 Gemini GenerateContent 和 Vertex-native 变体。

范围：

- 读取 Google Gemini reference snapshots。
- 解析 `systemInstruction`、`contents[]`、`parts[]`、`candidates[]`。
- 支持 `text`、`inlineData`、`fileData`、`functionCall`、`functionResponse`、`executableCode`、`codeExecutionResult`、`toolCall`、`toolResponse`、`thought`、`thoughtSignature`、`partMetadata`。
- 解析 `safetyRatings`、`promptFeedback`、`usageMetadata`。
- Vertex-native 处理 endpoint path、model resource 和 error shape。

验收：

- Gemini Part 不再只解析 text。
- function/code/tool/thought/safety 节点可见。
- Vertex model resource 可识别。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: add gemini observation parser
```

### P11. Performance View v0

目标：把已有 trace/session/upstream 性能数据产品化。

范围：

- Trace Detail 增加 `Performance` tab。
- 展示 latency、TTFT、tokens/s、cache ratio、status、provider error、routing/fallback。
- Session Detail 增加性能聚合。
- Upstream Detail 增加性能趋势。

验收：

- 使用现有 `logs` 字段即可显示首版 performance。
- 不要求逐 chunk tokens/s 精准计算。
- streaming 细粒度指标可作为后续增强。

检查：

```bash
task fmt:check
task test
task build
```

提交：

```text
feat: add performance monitor view
```

### P12. Session Learning v0

目标：支持长期对话轨迹与模型横向分析。

范围：

- 新增 `analysis_runs`。
- 支持 session summary。
- 支持 repeated findings。
- 支持 model/provider behavior comparison。
- 可选 LLM analysis run。

验收：

- analysis run 记录 analyzer、version、model、input ref、output。
- 输出引用 trace/node/finding。
- 历史分析查看不依赖 live upstream。
- LLM analysis 不进入默认 hot path。

检查：

```bash
task fmt:check
task test
```

提交：

```text
feat: add session analysis runs
```

## 每阶段固定复盘

每次提交后必须复盘：

1. 当前阶段是否仍服务 v1 总目标。
2. 是否破坏 replay、raw cassette 或 SQLite additive migration 约束。
3. 是否引入了不属于本阶段的功能。
4. 是否存在 reference schema 与 fixture 行为不一致的记录。
5. 下一阶段是否仍按计划推进，或者需要调整顺序。

复盘结论应写入后续提交说明、PR 描述或阶段任务记录中。

## 需求扩散控制

以下事项默认不纳入当前阶段，除非阶段计划明确列出：

- 修改 raw cassette 主格式。
- 修改 replay 使用 Observation IR。
- 在转发热路径运行深度 parser 或 analyzer。
- 默认 inline redaction。
- 默认调用 LLM 做分析。
- provider 之间协议互转。
- 公网多租户能力。
