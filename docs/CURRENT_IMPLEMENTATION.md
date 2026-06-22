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

Responses server-mode 是一个可选功能。默认情况下 `/v1/responses` 仍按 OpenAI-compatible Responses endpoint 代理透传；只有配置 `responses_server.enabled=true` 后，配置的 Responses path 才由本地 Responses runtime 接管。

开启 server-mode 后，当前已支持非流式 Responses 请求经本地 runtime 映射为内部上游 `/v1/chat/completions` 调用；该内部上游 HTTP exchange 会按现有 recorder 写入 `.http` cassette，并在有 ent-backed audit store 时写入一条最小 `upstream_exchanges` correlation。非 Responses 请求仍走现有代理、路由、录制和解析路径。

Upstream 配置已经包含 `api_type`、`mode` 和基础 `capabilities`。`api_type` 默认按协议族推断：OpenAI-compatible 为 `chat_completions`，Anthropic 为 `messages`，Google GenAI / Vertex 为 `gemini_generate_content`。当前路由会把 Chat Completions endpoint 的 API surface 当作约束；显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 target 不会被本地 Responses runtime 的内部 `/v1/chat/completions` 调用选中。

Hosted `web_search` 已有首切实现。配置 `tools.web_search.enabled=true` 后，可选择 `mock` 或 `searxng` provider；非流式 Responses runtime 会把 `web_search` / `web_search_preview` 暴露为上游 Chat Completions function tool，执行 server-side search，并把结果注入下一轮 Chat Completions。默认关闭，不影响普通代理路径。Stage 14A 已为 hosted `web_search` 写入 `response.tool_call` execution events，覆盖 started/completed/failed，details 中包含 tool name、call id、query、iteration、max results 和 result/error 摘要。

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

Responses server-mode 的 semantic state 使用 runtime store。当前装配优先使用 ent-backed store，表为 `responses` 和 `response_items`；SQLite raw DDL 已包含这些表以及 `request_audits`、`execution_events`、`upstream_exchanges`，本地 fallback 可以继续使用 SQLite。store 层也能打开 Postgres 并创建 ent client，但完整 Postgres migration 生产化仍未完成。Stage 10A 已让 server-mode `POST /v1/responses` 写入最小 `request_audits` inbound envelope 和 accepted/completed/failed/rejected 状态；Stage 11A 增加内部 `/v1/chat/completions` cassette 到 `request_audits` 的最小 `upstream_exchanges` 关联，并在 response 完成后回填 `response_id`。字段包括 response id、request audit id、recorder request id、cassette path、upstream id、route target、model、endpoint、status 和时间戳。Stage 12A 已开始写入最小 `execution_events`：Responses request accepted/completed/failed/rejected，以及内部 Chat Completions model_call started/completed/failed。Stage 13A 已新增 `internal/responses/audit.QueryService`、Monitor `/api/responses/audit/trace` 和 MCP `responses_audit_trace` 只读工具，可按 `response_id` 或 `request_audit_id` 查询 request audit、execution events 和 upstream exchanges。Stage 14A 已接入 hosted `web_search` tool_call started/completed/failed events；Stage 15A 已接入 upstream API surface 解析校验和 Chat Completions 路由约束；stream、cancel、compact 等更细粒度事件仍未接入。

Responses audit schema 的职责边界如下：

- `request_audits`：记录入站 Responses request envelope、client request id、headers allowlist、body hash/preview 和完成状态。当前只在 Responses server-mode 写入，并可通过 audit query service / MCP 查询。
- `execution_events`：记录 runtime plan、model/tool/compact/stream/error 生命周期事件。当前已写入 request、内部 model_call 和 hosted `web_search` tool_call 的最小生命周期事件，尚未覆盖 streaming、cancel、compact。
- `upstream_exchanges`：关联 semantic response/request 与 `.http` cassette、trace id、route target。当前只覆盖 Responses server-mode 内部 Chat Completions 调用，并在 response 完成后回填 semantic `response_id`；`trace_id` 暂使用 recorder prelude 的 `meta.request_id`。

后续接入顺序建议先补 Monitor UI 入口，再补通用 function tool lifecycle，最后处理 streaming/cancel/compact events。

当前重要表包括：

- `logs`
- `responses`
- `response_items`
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
- Responses server-mode streaming。
- 完整 Responses function tool lifecycle、streaming tool events 和 compact workflow。
- Responses audit Monitor UI、完整 function-tool/stream/cancel/compact execution event 写入和完整 Postgres migration 生产化；当前仅覆盖 `request_audits`、内部 `upstream_exchanges` correlation、request/model_call/hosted web_search 最小 `execution_events`，以及核心查询服务/Monitor API/MCP 查询。
- provider auto-detect。
