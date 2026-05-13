# llm-tracelab v1 设计文档入口

## 目标

v1 的目标不是继续把 `llm-tracelab` 做成一个更复杂的日志查看器，而是把它升级为一个本地优先、可自托管的 LLM 行为观测、审计、调试与回放工作台。

这套文档面向两个读者：

- 人类维护者：用于确认产品边界、架构方向、阶段计划与风险。
- AI 开发代理：用于从当前 `v0.10.0` 基线开始，按阶段实现 v1 能力。

## 文档顺序

建议按以下顺序阅读：

1. [`product-vision.md`](./product-vision.md): v1 产品定位、命名方向、用户场景与非目标。
2. [`architecture.md`](./architecture.md): v1 总体架构、五层平面、异步管道与关键边界。
3. [`observation-ir.md`](./observation-ir.md): 深度协议解析需要输出的 Observation IR。
4. [`protocol-parsers.md`](./protocol-parsers.md): OpenAI、Claude、Gemini、Vertex/OpenAI-compatible 等协议解析设计。
5. [`audit-analysis.md`](./audit-analysis.md): 危险操作、敏感信息、合规内容与模型行为分析设计。
6. [`monitor-experience.md`](./monitor-experience.md): Conversation / Protocol / Audit / Performance / Raw 展示体验设计。
7. [`storage-pipeline.md`](./storage-pipeline.md): raw cassette、SQLite 派生表、异步解析队列与重算机制。
8. [`implementation-roadmap.md`](./implementation-roadmap.md): v1 分阶段实施计划、验收条件与测试要求。
9. [`development-plan.md`](./development-plan.md): v1 开发执行计划，明确每阶段任务、验收、提交与复盘机制。
10. [`frontend-redesign-plan.md`](./frontend-redesign-plan.md): 后端 v1 API 稳定后的 Monitor 前端重构计划，覆盖信息架构、导航、详情页和阶段验收。

协议原始材料与抽取 schema 保存在 [`reference-materials/README.md`](./reference-materials/README.md)。实现 parser 前应先读取对应 provider 的快照，避免重复调研或只凭经验补字段。

## 与 v0.10.0 的关系

`v0.10.0` 是 v1 设计之前的稳定基线，已经具备：

- LLM HTTP 透明代理。
- `LLM_PROXY_V3` raw cassette。
- 单元测试回放能力。
- Monitor UI。
- session 聚合。
- 多 upstream 路由与基础健康分析。
- OpenAI-compatible、Anthropic Messages、Google GenAI、Vertex-native 的基础识别与摘要解析。

v1 不推翻这些能力，而是在其上增加：

- 更保真的协议语义解析。
- 可重算的 Observation IR。
- 危险工具/命令和敏感信息审计。
- 更适合排查问题的协议级展示。
- 面向长期对话轨迹和模型横向比较的分析能力。

## 核心原则

- 转发路径保持轻量，不能因为深度解析影响 LLM 可用性。
- raw `.http` cassette 是事实来源。
- 存储、解析、分析是独立管道，分析失败不能影响录制和回放。
- v1 所有语义层都是 additive，可以从 raw cassette 重算。
- 不做 provider 之间的协议互转作为核心目标。
- 不过早抹平 provider 差异；展示统一，但证据保真。
- 上游协议材料按日期快照保存，新增快照不覆盖旧材料。

## 当前推荐代号

仓库名继续保留 `llm-tracelab`。

v1 产品化方向可以暂用以下代号之一：

- `TraceLens`
- `AgentScope`
- `InferenceLens`

本文档不强行决定最终品牌名，但要求 v1 的产品体验不再只呈现为“研发日志工具”。
