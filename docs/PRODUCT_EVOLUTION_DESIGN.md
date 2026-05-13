# 产品演进设计

本文档是早期产品演进设计入口。v1 版本的完整中文设计文档已经拆分到 [`v1/README.md`](./v1/README.md) 及其子文档中。

## v1 设计定位

`llm-tracelab` v1 的目标，是从本地优先的 LLM HTTP 录制与回放代理，演进为本地优先、可自托管的 LLM 行为观测、审计、调试与回放工作台。

v1 设计保留当前项目最有价值的基础能力：

- 原始 LLM HTTP 请求和响应录制。
- 基于 `.http` cassette 的低成本单元测试回放。
- 多 upstream 代理、负载均衡和基础健康观测。
- Monitor UI 中对请求、响应、session、usage 和 timeline 的查看能力。

在此基础上，v1 重点增强：

- 对 OpenAI Chat Completions、OpenAI Responses、Anthropic Claude Messages、Google Gemini/Vertex 等主流协议的深度解析。
- 将 raw cassette 解析为可重算的 Observation IR。
- 对危险命令、敏感信息、工具调用、拒答、安全信号和性能异常进行结构化识别。
- 用 Conversation、Protocol、Audit、Performance、Raw 等视角还原一次 LLM 调用到底发生了什么。
- 为后续长期对话轨迹分析、模型横向比较和 AI 使用方法沉淀提供数据基础。

## 文档入口

建议按以下顺序阅读和开发：

1. [`v1/product-vision.md`](./v1/product-vision.md)：产品定位、命名方向、用户场景与非目标。
2. [`v1/architecture.md`](./v1/architecture.md)：总体架构、五层平面、异步管道与关键边界。
3. [`v1/observation-ir.md`](./v1/observation-ir.md)：深度协议解析输出的内部中间表示。
4. [`v1/protocol-parsers.md`](./v1/protocol-parsers.md)：四类主流 LLM API 协议的解析策略。
5. [`v1/audit-analysis.md`](./v1/audit-analysis.md)：危险操作、敏感内容和行为风险分析。
6. [`v1/monitor-experience.md`](./v1/monitor-experience.md)：面向调试和审计的前端展示体验。
7. [`v1/storage-pipeline.md`](./v1/storage-pipeline.md)：raw cassette、SQLite 派生表、异步队列与重算机制。
8. [`v1/implementation-roadmap.md`](./v1/implementation-roadmap.md)：从 `v0.10.0` 到 v1 的分阶段实现路线。

## 关键原则

- 转发路径保持轻量，不在热路径执行深度协议解析或 LLM 分析。
- raw `.http` cassette 是事实来源，所有语义解析结果都必须可以从 raw 重新计算。
- 转发、录制、解析、分析、展示是分层管道，任一派生层失败都不能破坏代理和回放能力。
- v1 不把跨 provider 协议互转作为核心目标，优先做 provider-aware 的深度识别和还原。
- 对外展示可以统一，但证据必须保真，不能过早抹平各 provider 的协议差异。
