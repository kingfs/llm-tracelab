# 当前实现概览

本文档用中文概括当前最新代码事实。它不是路线图，也不描述未落地的愿景。

## 产品定位

`llm-tracelab` 是一个本地优先的 LLM API 录制、回放、观测和调试代理。

当前核心闭环是：

1. SDK 或 CLI 流量经过本地代理。
2. 代理按协议族选择上游并透传请求。
3. 原始 HTTP 请求/响应写入 `.http` cassette。
4. SQLite 索引请求元数据、路由信息、会话信息、模型/渠道配置和派生分析结果。
5. Monitor Web 展示请求、会话、模型、渠道、路由、事件、发现和分析任务。
6. MCP 工具向 AI agent 暴露只读排障、trace 查询、失败聚类、系统事件和重分析入口。
7. `pkg/replay` 在测试中基于 cassette 回放响应，不访问上游网络。

## 当前协议边界

当前代理是协议族感知的透传代理，不是跨协议转换网关。

已实现协议族：

- OpenAI-compatible：Chat Completions、Responses、Embeddings、Models。
- Anthropic Messages：Claude `/v1/messages`。
- Google GenAI：Gemini `generateContent`、`streamGenerateContent`、模型列表。
- Vertex native：Vertex Gemini `generateContent`、`streamGenerateContent`、模型发现路径。

TraceLab 能解析这些协议并写入统一观测结构，但不会在转发热路径中把 Anthropic Messages 转成 OpenAI-compatible，也不会把 OpenAI Responses 转成 Gemini 或 Claude。

详细协议说明见 [协议参考](./protocol-reference/README.md)。

## 录制格式

当前写入格式是 `LLM_PROXY_V3`。

V3 文件结构：

1. `# llm-tracelab/v3` prelude。
2. 一行 `# meta: {...}`。
3. 零到多行 `# event: {...}`。
4. 一个空行。
5. 原始 HTTP 请求字节。
6. 分隔换行。
7. 原始 HTTP 响应字节。

读取端仍兼容旧 `LLM_PROXY_V2` 固定 2KB header block。

## 存储与派生数据

原始 `.http` cassette 是 replay 和详情页的事实源。

SQLite 是 Monitor 列表、统计、过滤、分页、模型/渠道配置、系统事件、Observation IR、findings、分析任务和 eval 结果的结构化索引。

当前重要表包括：

- `logs`
- `upstream_targets`
- `upstream_models`
- `channel_configs`
- `channel_models`
- `model_catalog`
- `channel_probe_runs`
- `trace_observations`
- `trace_findings`
- `analysis_jobs`
- `system_events`
- eval / dataset / score / experiment 相关表
- auth user / token 相关表

## Monitor 当前能力

Monitor 是 Go embed 的 React/Vite 前端。

当前主要页面/视角：

- Overview：健康概览、请求量、错误、观察状态、系统事件。
- Requests：逐请求 trace 列表。
- Sessions：按 session 聚合的请求视角。
- Models：按模型查看用量、渠道覆盖和失败。
- Channels：管理上游渠道、探测模型、启停模型。
- Routing：查看 selected route、sticky、候选和失败聚类。
- Events：系统事件收件箱。
- Tokens：管理当前用户 API token。
- Trace detail：Timeline、Summary、Raw Protocol、Declared Tools、Observation、Findings。

## MCP 当前能力

MCP 通过 management server 的 streamable HTTP 暴露。

当前定位是只读排障与分析辅助，工具复用 Monitor/store 查询，不另起一套事实源。

典型能力：

- 列出 traces、sessions、upstreams。
- 查看单条 trace，包括 raw request/response。
- 查询失败 trace 和失败聚类。
- 查看路由决策、sticky routing、危险工具调用、敏感数据 findings。
- 查询系统事件和未读事件。
- 触发受控 reanalysis job。

## 重分析与审计

当前已经实现可重算的派生层：

- Observation IR 解析。
- deterministic audit findings。
- trace/session/batch reanalysis jobs。
- parser/analyzer/router/upstream 事件写入 system events。

审计检测器包括危险 shell、凭据/敏感信息、provider safety signal、工具错误等。

## 模型与渠道管理

YAML `upstream` / `upstreams` 仍保留作为兼容启动输入。

长期配置以 SQLite 中的 channel/model 记录为准，并通过 Monitor Web 管理：

- 创建/更新渠道。
- 探测上游模型。
- 启停渠道。
- 启停模型。
- 本地加密存储 API key 和敏感 header。
- 修改后 reload router。

## 非目标

当前代码没有实现：

- 公网多租户 API 分发平台。
- 计费、充值、订阅销售。
- 跨协议请求转换网关。
- 让 replay 依赖网络访问。
- 用 SQLite 替代 raw cassette 作为 replay 事实源。
