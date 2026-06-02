# v1 文档入口

`docs/v1` 保存 TraceLab 从录制/回放代理演进到本地 LLM 行为观测工作台的设计文档。

当前代码中，v1 的一部分能力已经落地：

- Observation IR。
- 协议解析。
- deterministic findings。
- reanalysis jobs。
- Models / Channels / Routing / Events 等 Monitor 页面。
- MCP trace/session/upstream/event/reanalysis 工具。

因此阅读时请区分：

- 当前事实：优先看 [`../CURRENT_IMPLEMENTATION.md`](../CURRENT_IMPLEMENTATION.md) 和 [`../PROJECT_BASELINE.md`](../PROJECT_BASELINE.md)。
- 协议事实：优先看 [`../protocol-reference/README.md`](../protocol-reference/README.md)。
- 设计背景：阅读本目录下文档。

## 推荐阅读顺序

1. [`status.md`](./status.md)：v1 能力当前落地状态。
2. [`architecture.md`](./architecture.md)：v1 总体架构原则。
3. [`observation-ir.md`](./observation-ir.md)：Observation IR 设计。
4. [`protocol-parsers.md`](./protocol-parsers.md)：协议解析策略。
5. [`audit-analysis.md`](./audit-analysis.md)：审计和 findings 设计。
6. [`monitor-experience.md`](./monitor-experience.md)：Monitor 体验方向。
7. [`storage-pipeline.md`](./storage-pipeline.md)：存储和重分析管道。
8. [`model-channel-management-design.md`](./model-channel-management-design.md)：模型与渠道管理设计。

## 历史计划文档

以下文档保留用于追溯，不再作为当前执行计划：

- [`development-plan.md`](./development-plan.md)
- [`implementation-roadmap.md`](./implementation-roadmap.md)
- [`frontend-redesign-plan.md`](./frontend-redesign-plan.md)
- [`product-vision.md`](./product-vision.md)

如果这些文档与顶层当前事实文档冲突，以顶层当前事实文档为准。

## 当前非目标

v1 当前仍不把以下内容作为核心目标：

- 公网多租户 API 分发。
- 计费、充值、订阅销售。
- 跨 provider 协议转换。
- 用派生 IR 替代 raw cassette。
