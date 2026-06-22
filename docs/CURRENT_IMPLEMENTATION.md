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

开启 server-mode 后，当前已支持非流式 Responses 请求经本地 runtime 映射为内部上游 `/v1/chat/completions` 调用；该内部上游 HTTP exchange 会按现有 recorder 写入 `.http` cassette，并在有 ent-backed audit store 时写入一条最小 `upstream_exchanges` correlation。`internal/responses/chatclient` 直接调用 OpenAI-compatible Chat Completions 上游时，已能把 `stream:true` SSE 聚合回内部 `ChatCompletionResponse`，覆盖文本增量、function tool call 参数分片和 usage trailer。server-mode 下游请求 `stream:true` 时，简单文本输出路径会边读取内部 Chat Completions SSE、边输出 `response.output_text.delta`；普通 `function` tool 参数分片也会边读取上游 SSE、边输出 `response.function_call_arguments.delta/done`，同时把原始上游 SSE 写入 stream cassette 并在完成后存储完整 response。hosted tools、server-side tool execution 和 auto compact 等复杂路径仍 fallback 到 deferred Responses SSE envelope。cancel 传播仍未完成。非 Responses 请求仍走现有代理、路由、录制和解析路径。

Upstream 配置已经包含 `api_type`、`mode` 和基础 `capabilities`。`api_type` 默认按协议族推断：OpenAI-compatible 为 `chat_completions`，Anthropic 为 `messages`，Google GenAI / Vertex 为 `gemini_generate_content`。当前路由会把 Chat Completions endpoint 的 API surface 当作约束；显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 target 不会被本地 Responses runtime 的内部 `/v1/chat/completions` 调用选中。`capabilities.tool_calling: false` 的 target 在请求体包含 `tools` 时也不会被选中，decision trace 会标记 `unsupported_tools`。当前还提供手动诊断命令 `provider probe`：它会对配置中的 upstream endpoint 做保守探测，输出建议的 `api_type`、`protocol_family` 和 capability signals。默认启动不会执行 probe；配置 `provider_probe.startup_fill=true` 后，serve 启动会只在内存中填补 YAML upstream 缺失的 `api_type`、`protocol_family` 和未声明 capability，不写回配置，也不覆盖显式配置。

Hosted `web_search` 已有首切实现。配置 `tools.web_search.enabled=true` 后，可选择 `mock` 或 `searxng` provider；非流式 Responses runtime 会把 `web_search` / `web_search_preview` 暴露为上游 Chat Completions function tool，执行 server-side search，并把结果注入下一轮 Chat Completions。默认关闭，不影响普通代理路径。Stage 14A 已为 hosted `web_search` 写入 `response.tool_call` execution events，覆盖 started/completed/failed，details 中包含 tool name、call id、query、iteration、max results 和 result/error 摘要。普通 `function` tool 默认仍走客户端回路：runtime 会把 tool schema 转给上游 Chat Completions，把模型返回的 function call 保存为 Responses `function_call` output，客户端后续提交 `function_call_output` 时通过 `previous_response_id` 继续对话，并写 requested/submitted tool_call events。当前已新增 server-side function executor registry 代码扩展点；只有调用方显式注册同名 executor 时，runtime 才会自动执行该 function tool、注入下一轮模型上下文，并写 started/completed/failed tool_call events。该 registry 尚未接 YAML/Monitor 配置。

Compact workflow 已有首切实现。server-mode 会把配置 Responses path 下的子路径一起分流到本地 Responses handler；`POST /v1/responses/compact` 接收 `response_id`，读取目标 response 的 continuation history，调用上游 Chat Completions 生成 `summary` output，并把新的 compact response 存入 runtime store。compact response 的 input item 是 `compact_request`，后续 `previous_response_id` 指向该 compact response 时，history 会停在 compact boundary，并把 `summary` 作为 system message 注入下一轮模型上下文。配置 `responses_server.auto_compact=true` 且有效 compact item 阈值大于 0 后，create continuation 会在加载 history item 数超过阈值时先自动 compact，再把本次 response 接到 compact response 后面。有效阈值默认来自 `responses_server.compact_history_item_threshold`，也可由匹配当前 model 的 `responses_server.model_profiles[].compact_history_item_threshold` 覆盖；匹配 profile 且配置 `upstream_model` 时，runtime 的内部 Chat Completions 请求会使用该上游模型名，外部 Responses `model` 仍保留客户端请求 model 或默认 model。`context_window_tokens` 和 `max_output_tokens` 仍是配置骨架；当前自动 compact 仍只按 item-count 判定，不做 token estimator 或完整 context window budgeting。

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

Responses server-mode 的 semantic state 使用 runtime store。当前装配优先使用 ent-backed store，表为 `responses` 和 `response_items`；SQLite raw DDL 已包含这些表以及 `request_audits`、`execution_events`、`upstream_exchanges`，本地 fallback 可以继续使用 SQLite。store 层也能打开 Postgres 并创建 ent client。`ent/postgres-migrations` 已包含 Postgres application schema SQL，并已在 Postgres 17 dev database 上验证 up/down 可执行；Postgres `db migrate up` 已通过 `internal/appdbmigrate` 和 `golang-migrate` 应用 checked-in SQL。命令/server 打开 application store 时已拆分 migrate 与 open：`database.auto_migrate=true` 先执行应用迁移，再以 no-auto-migrate 模式打开 store；`false` 则只打开已存在 schema。Postgres migration 现在覆盖 `internal/store` SQLite application raw DDL 表集。`db migrate status` 和 `db migrate up/down --dry-run` 报告当前应用库迁移来源：Postgres 为 checked-in `ent/postgres-migrations` SQL，SQLite 为 `internal/store` raw DDL startup fallback，且明确 auth migration 不属于 `db migrate` 范围；`db migrate status --check-db` 可显式读取 Postgres `schema_migrations` version/dirty 状态，SQLite 下仍只报告 schema-init fallback 而不创建版本标记。Postgres `auth migrate up` 和 auth startup auto-migrate 也走同一套 checked-in Postgres SQL，因为当前 ent/Postgres schema 同时包含 auth 表；`auth migrate down` 和独立 auth migration namespace 仍未完成。Stage 10A 已让 server-mode `POST /v1/responses` 写入最小 `request_audits` inbound envelope 和 accepted/completed/failed/rejected/cancelled 状态；Stage 11A 增加内部 `/v1/chat/completions` cassette 到 `request_audits` 的最小 `upstream_exchanges` 关联，并在 response 完成后回填 `response_id`。字段包括 response id、request audit id、recorder request id、cassette path、upstream id、route target、model、endpoint、status 和时间戳。Stage 12A 已开始写入最小 `execution_events`：Responses request accepted/completed/failed/cancelled/rejected，以及内部 Chat Completions model_call started/completed/failed/cancelled。Stage 13A 已新增 `internal/responses/audit.QueryService`、Monitor `/api/responses/audit/trace` 和 MCP `responses_audit_trace` 只读工具，可按 `response_id` 或 `request_audit_id` 查询 request audit、execution events 和 upstream exchanges。Stage 14A 已接入 hosted `web_search` tool_call started/completed/failed events；Stage 17A 已接入普通 function tool 非流式 continuation 和 requested/submitted events；Stage 17B 已接入 `capabilities.tool_calling` 路由硬约束；Stage 18A 已接入 `stream:true` deferred Responses SSE envelope 和 `response.stream` started/completed events；Stage 18B 已接入 Chat Completions SSE 聚合，并在 Responses server-mode 的 stream 请求中记录内部上游 SSE cassette；Stage 18C 已让 context cancellation 以 `cancelled` request/model_call execution events 和 request audit 状态记录，并用不随客户端断开的 audit 写入上下文落库；Stage 19A 已接入显式 compact API、summary output 存储、compact boundary continuation 和 `response.compact` execution events；Stage 19B 已接入配置化自动 compact item-count 阈值触发和 `auto_triggered` execution event；Stage 20 已接入简单文本输出的真实增量 Responses streaming 首切，复杂 tool/auto-compact stream 仍 fallback；Stage 24A 已接入默认空的 server-side function executor registry 代码扩展点和执行审计；Stage 15A 已接入 upstream API surface 解析校验和 Chat Completions 路由约束；Stage 16A/16B/16C/16D/16E/16F/16G/16H 已接入 Postgres ent migration SQL 生成链路、checked-in migrations、CLI versioned SQL migrator、open-vs-migrate 分离、首轮 store raw SQL 兼容审计、application raw DDL 表覆盖补齐、Postgres auth `up` 版本化迁移、应用库迁移状态报告和 opt-in database status check；完整 tool streaming、cancel 传播、function executor 配置化和 model profile token budgeting 等更细粒度能力仍未接入。

Responses audit schema 的职责边界如下：

- `request_audits`：记录入站 Responses request envelope、client request id、headers allowlist、body hash/preview 和完成状态。当前只在 Responses server-mode 写入，并可通过 audit query service / MCP 查询。
- `execution_events`：记录 runtime plan、model/tool/compact/stream/error 生命周期事件。当前已写入 request、内部 model_call、hosted `web_search` tool_call、普通 function requested/submitted、registered server-side function started/completed/failed、deferred/incremental stream started/completed、request/model_call cancellation、explicit compact 和 auto compact threshold trigger 的最小生命周期事件。
- `upstream_exchanges`：关联 semantic response/request 与 `.http` cassette、trace id、route target。当前只覆盖 Responses server-mode 内部 Chat Completions 调用，并在 response 完成后回填 semantic `response_id`；`trace_id` 暂使用 recorder prelude 的 `meta.request_id`。

后续接入顺序建议先补 Monitor UI 入口，再处理真实上游 streaming/cancel 和 compact events。

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
- hosted tool/server-side tool execution、auto-compact 等复杂场景的真实增量 Responses server-mode streaming 和 cancel 传播。
- function executor 的 YAML/Monitor 配置、server-side tool streaming events 和完整 model profile/context window/token budgeting；当前仅有默认空 executor registry、profile 配置骨架和 item-count compact 阈值覆盖，普通 function call argument streaming 已有首切。
- 完整真实 stream/cancel/compact execution event 写入和完整 Postgres migration 生产化；当前仅覆盖 `request_audits`、内部 `upstream_exchanges` correlation、request/model_call/hosted web_search started/completed/failed、普通 function tool requested/submitted、deferred/incremental stream started/completed 最小 `execution_events`，核心查询服务/Monitor API/MCP/UI 查询，Postgres `db migrate up`/`auth migrate up` 的 versioned SQL 应用路径，application store 的 open-vs-migrate 分离，以及 migrated logs/observation/finding/analysis/system-event 路径的首轮 Postgres raw SQL 兼容。
- provider probe 的完整配置/Monitor 工作流；当前只有手动 `provider probe` 诊断建议和默认关闭的启动时保守补全首切。
