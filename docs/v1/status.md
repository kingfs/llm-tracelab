# v1 当前状态

本文档把 v1 设计能力按当前代码事实分组。

## 已落地

### 协议解析与 Observation IR

当前已有：

- `pkg/observe` parser registry。
- OpenAI parser。
- Anthropic parser。
- Gemini/Vertex parser。
- `trace_observations` 持久化。
- trace detail Observation API。
- parser failure 写 system events。

### 审计 Findings

当前已有 deterministic detectors：

- 危险 shell/命令。
- 凭据和敏感信息。
- provider safety signal。
- tool error。

findings 写入 `trace_findings`，可通过 Monitor 和 MCP 查询。

### Reanalysis

当前已有：

- trace reparse。
- trace rescan。
- trace usage repair。
- trace reanalyze。
- session reanalyze。
- batch reanalysis。
- `analysis_jobs` 状态持久化。

### Monitor

当前已有页面/视角：

- Overview。
- Requests。
- Sessions。
- Models。
- Channels。
- Routing。
- Events。
- Tokens。
- Analysis。
- Trace detail。

### 模型与渠道管理

当前已有：

- `channel_configs`。
- `channel_models`。
- `model_catalog`。
- `channel_probe_runs`。
- YAML bootstrap。
- DB 优先 router 配置。
- channel/model 启停。
- probe。
- router reload。
- 本地加密 API key 和敏感 header。

### MCP

当前已有：

- trace/session/upstream 查询。
- failure clustering。
- routing/sticky 查询。
- system event 查询。
- findings 查询。
- reanalysis job。

## 部分落地

### Protocol View

后端 Observation API 已有，Monitor trace detail 已能展示派生协议信息。

仍可继续增强：

- 更细粒度 provider raw node 展示。
- 更好的 stream event 可视化。
- 更强的 unknown field inspection。

### Session 长期分析

session 聚合和 session reanalysis 已有。

仍可继续增强：

- 更丰富的跨 trace 行为摘要。
- 更系统的 session-level findings。

## 仍属设计或后续演进

- 更完整的前端信息架构重设计。
- 更多 provider 协议族。
- 更丰富的 eval profile。
- retention/compaction 策略。
- 更细的预算、ledger、成本治理。
- trace-to-test 自动生成能力。

## 已归档的历史计划

顶层历史计划和完成阶段已经移到 [`../archive/`](../archive/README.md)。

阅读旧计划前，先核对当前事实文档：

- [`../CURRENT_IMPLEMENTATION.md`](../CURRENT_IMPLEMENTATION.md)
- [`../PROJECT_BASELINE.md`](../PROJECT_BASELINE.md)
- [`../MAINTAINER_BASELINE.md`](../MAINTAINER_BASELINE.md)
