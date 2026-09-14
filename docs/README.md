# TraceLab 文档入口

本目录只保留**当前实现事实**与**当前可用的操作、开发指南**。历史计划、路线图、阶段设计与归档材料不再保留在本仓库文档中。

## 阅读路径

- 第一次接触项目：先读 [当前实现状态](./IMPLEMENTATION_STATUS.md)，再读 [架构与代码地图](./ARCHITECTURE.md)。
- 要接入上游或客户端：读 [协议族与上游 Provider](./PROTOCOLS_AND_PROVIDERS.md) 与 [代理使用示例](./PROXY_USAGE_EXAMPLES.md)。
- 要管理渠道、模型、凭据与限流：读 [路由、渠道与凭据](./ROUTING_AND_CREDENTIALS.md)。
- 要让 Codex 或客户端走本地 Responses：读 [本地 Responses Runtime](./RESPONSES_RUNTIME.md)。
- 要看 Monitor 界面：读 [Monitor 使用指南](./MONITOR_GUIDE.md)。
- 要用 MCP：读 [MCP 使用指南](./MCP_GUIDE.md)。
- 要参与开发：读 [开发与测试](./DEVELOPMENT.md) 与 [架构与代码地图](./ARCHITECTURE.md)。
- 要部署与运维：读 [存储与部署](./STORAGE_AND_DEPLOYMENT.md) 与 [PostgreSQL 运维手册](./POSTGRES_OPERATIONS.md)。
- 要看协议矩阵与协议原文：读 [协议参考](./protocol-reference/README.md)。

## 事实源文档

这些文档描述当前代码事实，是判断“项目现在能做什么”的依据。

| 文档 | 回答的问题 |
| --- | --- |
| [当前实现状态](./IMPLEMENTATION_STATUS.md) | 现在已经实现了什么、没有实现什么 |
| [架构与代码地图](./ARCHITECTURE.md) | 代码怎么组织、数据怎么流动、事实源边界在哪 |
| [协议族与上游 Provider](./PROTOCOLS_AND_PROVIDERS.md) | 支持哪些协议族、有哪些 provider preset、能力怎么声明 |
| [路由、渠道与凭据](./ROUTING_AND_CREDENTIALS.md) | 配置从哪来、怎么写库、路由与限流怎么生效 |
| [本地 Responses Runtime](./RESPONSES_RUNTIME.md) | `/v1/responses` 怎么处理、Codex 兼容面、hosted tools |
| [语义解析、Observation IR 与审计](./OBSERVATION_AND_AUDIT.md) | 录制怎么变成 Observation IR、findings 与重分析怎么工作 |
| [存储与部署](./STORAGE_AND_DEPLOYMENT.md) | 数据放在哪、怎么迁移、怎么部署 |

## 操作指南

| 文档 | 回答的问题 |
| --- | --- |
| [Monitor 使用指南](./MONITOR_GUIDE.md) | 界面有哪些页面、每个页面怎么用 |
| [MCP 使用指南](./MCP_GUIDE.md) | MCP 怎么启动、认证怎么做、有哪些工具 |
| [代理使用示例](./PROXY_USAGE_EXAMPLES.md) | 怎么把 SDK 或 CLI 接到代理上 |
| [PostgreSQL 运维手册](./POSTGRES_OPERATIONS.md) | 长期运行的基线采集、索引、调优与排障 |

## 开发文档

| 文档 | 回答的问题 |
| --- | --- |
| [开发与测试](./DEVELOPMENT.md) | 用什么命令、验证到什么程度、集成测试怎么做、CI 跑什么 |

## 协议参考

| 文档 | 回答的问题 |
| --- | --- |
| [协议参考](./protocol-reference/README.md) | 协议族矩阵、协议差异、上游 schema 快照与取材原则 |

## 维护约定

- 文档描述**当前代码事实**。文档与代码冲突时以代码为准，并修正文档。
- 不再新增历史计划、路线图、阶段设计文档。需要保留设计取舍时，写进对应事实源文档的「设计取舍」小节。
- 只有上游 schema 快照这类外部材料才标注日期。
- 术语统一：渠道（provider）、上游（upstream）、路由（route）、录制（cassette）、本地 Responses runtime。
