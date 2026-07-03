# TraceLab 文档入口

本目录按“当前事实优先、历史材料归档”的原则整理。

如果你是第一次阅读本项目，建议按下面顺序阅读。

## 快速读取路径

1. [当前实现概览](./CURRENT_IMPLEMENTATION.md)：项目现在已经实现了什么、没有实现什么。
2. [架构说明](./ARCHITECTURE.md)：代理、录制、Postgres-first application DB、Monitor、MCP 与重分析之间的关系。
3. [协议参考](./protocol-reference/README.md)：当前协议族、协议差异和上游 schema 快照。
4. [开发命令](./DEVELOPMENT_COMMANDS.md)：稳定的构建、测试、格式化、检查入口。
5. [维护基线](./MAINTAINER_BASELINE.md)：修改存储、录制、Monitor、MCP、重分析时必须遵守的约束。

如果你在参与 Responses server 演进设计或实现，请先读 [Responses Server 设计](./RESPONSES_SERVER_DESIGN.md)。该文档记录目标设计、截至 2026-06-23 的已落地状态和剩余缺口；入口、自动执行模式、UI 托管渠道和模型别名的最终态任务拆分见 [Gateway Routing and UI-Managed Model Configuration](./GATEWAY_ROUTING_UI_DESIGN.md)；入口侧与上游模型调用统一录制的落地方案见 [Entry/Model Exchange Recording 落地方案](./ENTRY_MODEL_EXCHANGE_RECORDING_PLAN.md)；Hosted tools 的分阶段实现计划见 [Hosted Tools Roadmap](./HOSTED_TOOLS_ROADMAP.md)；最终收敛证据见 [Final Completion Audit](./FINAL_COMPLETION_AUDIT.md)。通用当前事实仍以当前实现概览和项目基线为准。

## 当前事实文档

- [当前实现概览](./CURRENT_IMPLEMENTATION.md)
- [架构说明](./ARCHITECTURE.md)
- [项目基线](./PROJECT_BASELINE.md)
- [维护基线](./MAINTAINER_BASELINE.md)
- [PostgreSQL 长期运行优化 Runbook](./POSTGRES_OPERATIONS_RUNBOOK.md)
- [PostgreSQL Baseline SQL](./POSTGRES_BASELINE_SQL.md)
- [上游 Provider 与协议族](./UPSTREAM_PROVIDERS.md)
- [Provider 协议入口](./PROVIDER_PROTOCOL_ENTRYPOINTS.md)
- [协议参考](./protocol-reference/README.md)
- [Gateway Routing and UI-Managed Model Configuration](./GATEWAY_ROUTING_UI_DESIGN.md)
- [Responses Server 设计](./RESPONSES_SERVER_DESIGN.md)
- [Entry/Model Exchange Recording 落地方案](./ENTRY_MODEL_EXCHANGE_RECORDING_PLAN.md)
- [Exchange Recording Model Design](./EXCHANGE_RECORDING_MODEL_DESIGN.md)
- [Responses Entry Recording Design](./RESPONSES_ENTRY_RECORDING_DESIGN.md)
- [Exchange Query Replay Design](./EXCHANGE_QUERY_REPLAY_DESIGN.md)
- [Hosted Tools Roadmap](./HOSTED_TOOLS_ROADMAP.md)
- [Final Completion Audit](./FINAL_COMPLETION_AUDIT.md)
- [Codex Responses 兼容性首切](./CODEX_RESPONSES_COMPATIBILITY.md)
- [集成测试诊断指南](./INTEGRATION_TEST_RUNBOOK.md)
- [Responses Gateway 完成计划](./RESPONSES_GATEWAY_COMPLETION_PLAN.md)
- [Responses Gateway 重构路线图](./RESPONSES_GATEWAY_ROADMAP.md)
- [Responses Gateway 差距清单](./RESPONSES_GATEWAY_GAP_ANALYSIS.md)

## 用户与运维指南

- [Monitor 使用指南](./MONITOR_GUIDE.md)
- [MCP 使用指南](./MCP_GUIDE.md)
- [代理使用示例](./PROXY_USAGE_EXAMPLES.md)
- [凭据路由操作指南](./CREDENTIAL_ROUTING_OPERATOR_GUIDE.md)
- [PostgreSQL 长期运行优化 Runbook](./POSTGRES_OPERATIONS_RUNBOOK.md)
- [PostgreSQL Baseline SQL](./POSTGRES_BASELINE_SQL.md)
- [Codex MCP 本地配置](./CODEX_MCP_LOCAL_CONFIG.md)
- [集成测试诊断指南](./INTEGRATION_TEST_RUNBOOK.md)

## 开发指南

- [开发命令](./DEVELOPMENT_COMMANDS.md)
- [维护基线](./MAINTAINER_BASELINE.md)
- [PostgreSQL 长期运行优化 Runbook](./POSTGRES_OPERATIONS_RUNBOOK.md)
- [PostgreSQL Baseline SQL](./POSTGRES_BASELINE_SQL.md)
- [协议参考](./protocol-reference/README.md)

## v1 文档

[`v1/`](./v1/README.md) 现在作为“当前 v1 能力与后续演进设计”的中文集合。

其中已经落地的能力应以顶层当前事实文档为准；`v1/` 中保留的设计文档用于解释 Observation IR、协议解析、审计分析和 Monitor 体验的方向。

## 归档文档

历史计划、已完成阶段设计、分支记录和较早路线图已经移到 [`archive/`](./archive/README.md)。

归档文档不再作为当前实现事实源。需要追溯决策背景时再阅读。
