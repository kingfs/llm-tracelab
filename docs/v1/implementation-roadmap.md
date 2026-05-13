# v1 实施路线图

## 总体策略

v1 不做大爆炸重写。

实施策略：

- 保留 `v0.10.0` 的代理、录制、回放、多上游和 Monitor 基础能力。
- 新增 Observation IR，不替换现有 `pkg/llm`。
- 新增异步解析和分析管道，不进入转发热路径。
- 先让 Protocol View 可用，再做 Audit View。
- 每一阶段都必须有 cassette fixture 测试。

## M0. v1 文档基线

状态：

- 当前文档集。

验收：

- `docs/v1/` 下文档全部中文。
- 文档可作为 AI 开发代理的主入口。
- README/AGENTS 指向 v1 文档。

## M1. Observation IR v0

目标：

定义深度协议解析的内部表示。

范围：

- 新增 `pkg/observe`。
- 定义 `TraceObservation`。
- 定义 `SemanticNode`。
- 定义 `Finding`。
- 定义 parser 接口。

验收：

- 有 Go 类型定义。
- 有 JSON marshal/unmarshal 测试。
- 不影响现有 `pkg/llm` 测试。
- 不改 replay 行为。

## M2. OpenAI Deep Parser

目标：

优先解析 OpenAI Chat Completions 和 OpenAI Responses。

范围：

- OpenAI Chat request/response/stream。
- OpenAI Responses request/response/stream。
- tool call/tool result。
- reasoning/refusal。
- usage。
- unknown output item preservation。

验收：

- fixture 覆盖 non-stream、stream、tool、reasoning、refusal、error。
- Protocol nodes 有 provider type、normalized type、path。
- OpenAI Responses 的 `output[]` 能完整展示。

## M3. Claude Deep Parser

目标：

解析 Claude Messages。

范围：

- top-level `system`。
- `messages[].content` string 和 blocks。
- `text`、`thinking`、`tool_use`、`tool_result`。
- server tool result blocks。
- SSE event flow。
- usage。

验收：

- 能还原 Claude content block 顺序。
- 能累积 `input_json_delta`。
- 能区分 client tool 和 server tool。

## M4. Gemini Deep Parser

目标：

解析 Gemini GenerateContent。

范围：

- `systemInstruction`。
- `contents[].parts[]`。
- `candidates[].content.parts[]`。
- `functionCall` / `functionResponse`。
- `executableCode` / `codeExecutionResult`。
- `toolCall` / `toolResponse`。
- `thought` / `thoughtSignature`。
- safetyRatings / promptFeedback。

验收：

- Gemini Part 不再只解析 text。
- function/code/tool/thought 节点可见。
- safety block 可统一显示。

## M5. 异步 Parse Worker

目标：

从 raw cassette 异步生成 Observation IR。

范围：

- parse job 表。
- 新 trace enqueue。
- 手动 reparse。
- parse status。
- parser version。

验收：

- 录制完成后不阻塞请求。
- parse 失败可见。
- reparse 可重建 semantic_nodes。

## M6. Protocol View

目标：

Monitor 增加协议级结构展示。

范围：

- Trace Detail 新增 `Protocol` tab。
- semantic node tree。
- provider type / normalized type / path。
- raw JSON 展开。

验收：

- OpenAI Responses output item sequence 可读。
- Claude content blocks 可读。
- Gemini parts/candidates 可读。
- unknown nodes 可见。

## M7. Audit Findings v0

目标：

增加基础风险检测。

范围：

- dangerous shell detector。
- credential detector。
- PII detector。
- provider safety detector。
- tool error detector。

验收：

- findings 入库。
- Audit tab 可见。
- finding 可跳转到 evidence node。
- 每个 detector 有 fixture 测试。

## M8. Performance View v0

目标：

增强请求和 upstream 性能可观测性。

范围：

- TTFT。
- tokens/s。
- cache ratio。
- routing/fallback annotations。
- session 聚合性能。
- upstream 聚合性能。

验收：

- Trace Detail 有 Performance tab。
- Session Detail 显示性能聚合。
- Upstream Detail 显示健康和性能趋势。

## M9. Session Learning v0

目标：

支持长期对话轨迹与模型横向分析。

范围：

- session summary。
- repeated findings。
- model/provider behavior comparison。
- optional LLM analysis run。

验收：

- analysis run 可追溯。
- 输出引用 trace/node/finding。
- 不依赖 live upstream 才能查看历史分析。

## 测试要求

每个 milestone 必须至少运行：

```bash
task fmt:check
task test
```

涉及转发路径时额外运行：

```bash
task bench:core
```

涉及 UI 时额外运行：

```bash
task build
```

## 提交策略

建议每个 milestone 一个或多个独立 commit：

```text
design: add v1 documentation baseline
feat: add observation ir
feat: add openai deep protocol parser
feat: add claude deep protocol parser
feat: add gemini deep protocol parser
feat: add async semantic parse worker
feat: add protocol monitor view
feat: add audit findings
feat: add performance view
```

## 风险控制

- 不重写 raw cassette。
- 不删除旧 parser。
- 不让 Observation IR 成为 replay 前置依赖。
- 不把 LLM 分析放进默认 hot path。
- 不默认 inline redaction。

## v1 完成定义

v1 可以认为完成，当：

- 四种主流协议能深度解析。
- Protocol View 能准确还原 provider 原生结构。
- Audit View 能识别危险工具调用和敏感信息。
- Performance View 能展示推理服务指标和上游状态。
- 所有语义和分析结果都能从 raw cassette 重算。
- replay 兼容性保持不变。
