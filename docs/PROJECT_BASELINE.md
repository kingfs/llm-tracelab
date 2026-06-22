# 项目基线

本文档记录当前已经实现的功能基线。它不是路线图。

更短的中文概览见 [当前实现概览](./CURRENT_IMPLEMENTATION.md)。

## 当前产品范围

TraceLab 当前提供：

- 本地 LLM API 代理。
- 原始 HTTP cassette 录制。
- cassette replay。
- SQLite 元数据索引。
- 多上游和模型/渠道管理。
- 可选 Responses server-mode。
- Monitor Web。
- MCP 排障工具。
- Observation IR、findings、reanalysis jobs。

## 当前协议覆盖

已实现协议族：

- `openai_compatible`
- `anthropic_messages`
- `google_genai`
- `vertex_native`

边界：

- 可以识别、记录、解析这些协议。
- 不在代理热路径中做跨协议请求转换。
- OpenAI-compatible provider 只能声明兼容其实际支持的 endpoint。
- Responses server-mode 默认关闭；关闭时 `/v1/responses` 仍按普通 OpenAI-compatible endpoint 代理透传。
- 开启 `responses_server.enabled=true` 后，配置的 Responses path 由本地 runtime 处理，当前通过内部上游 `/v1/chat/completions` 调用实现 Responses 响应；该内部调用会受 upstream `api_type` / capabilities 约束，不会选择显式关闭 Chat Completions 能力的 Responses-native target，也不会在请求带 `tools` 时选择 `capabilities.tool_calling: false` 的 target。下游 `stream:true` 时，内部 Chat Completions model call 已请求并记录 OpenAI-compatible SSE，再聚合为完整 response；下游 Responses SSE 仍是 deferred envelope，不是边读上游边转发。
- 开启 `tools.web_search.enabled=true` 后，非流式 Responses runtime 可执行 hosted `web_search` / `web_search_preview` 首切，provider 支持 `mock` 和 SearXNG；有 ent-backed audit store 时会写 hosted web_search `response.tool_call` started/completed/failed events。
- 非 Responses 请求不进入 Responses runtime，继续走现有代理、路由、录制和解析路径。

详细内容见 [协议参考](./protocol-reference/README.md)。

## 录制与回放基线

- 当前写入格式：`LLM_PROXY_V3`。
- 读取兼容：`LLM_PROXY_V2`。
- `.http` cassette 是 replay 和详情页事实源。
- `pkg/replay` 是硬要求，测试 replay 不访问上游网络。
- Responses server-mode 内部调用上游 Chat Completions 时，该上游 HTTP exchange 也写入 `.http` cassette；Responses semantic state 不替代 raw cassette。

## SQLite 基线

SQLite 当前负责：

- trace 列表、过滤、分页和统计。
- session 聚合。
- upstream、model、channel 分析。
- channel/model 配置。
- auth user/token。
- system events。
- Observation IR。
- trace findings。
- analysis jobs。
- eval、dataset、score、experiment。
- Responses semantic state fallback 表：`responses`、`response_items`。

启动时 schema 升级必须兼容已有本地 DB。

Responses server-mode 当前优先使用 ent-backed runtime store。SQLite raw DDL 已包含 `responses` / `response_items`；store 层存在 Postgres 打开路径并能创建 ent client。`ent/postgres-migrations` 已包含 Postgres application schema SQL，并已验证 up/down 可执行；Postgres `db migrate up` 已切到 checked-in SQL migrator，通过 `golang-migrate` 应用嵌入的 `ent/postgres-migrations`。命令/server 打开 application store 时已拆分 migrate 与 open：`database.auto_migrate=true` 先执行应用迁移，再以 no-auto-migrate 模式打开 store；`false` 则只打开已存在 schema。Postgres migration 现在覆盖 `internal/store` SQLite application raw DDL 表集。Postgres `auth migrate up` 和 auth startup auto-migrate 也复用这套 checked-in Postgres SQL；`auth migrate down` 和独立 auth migration namespace 仍未完成。完整 Postgres 运维生产化仍不是当前基线能力，因为 SQLite 应用迁移仍未版本化，Postgres auth rollback/namespace 未完成，analytics/eval 等 raw SQL 兼容性仍需持续审计。Stage 9 已准备 `request_audits`、`execution_events`、`upstream_exchanges` schema 骨架；Stage 10A/11A 已接入最小 request audit 写入和内部 Chat Completions upstream exchange correlation，Stage 12A 已接入 request 与内部 model_call 的最小 `execution_events` 写入，Stage 13A 已接入核心 audit 查询服务、Monitor `/api/responses/audit/trace` 和 MCP `responses_audit_trace` 工具，Stage 14A 已接入 hosted `web_search` tool_call started/completed/failed events，Stage 17A 已接入普通 function tool 非流式 continuation 和 requested/submitted events，Stage 17B 已接入 `capabilities.tool_calling` 路由硬约束，Stage 18A 已接入 `stream:true` deferred Responses SSE envelope 和 stream lifecycle events，Stage 18B 已接入 Chat Completions SSE 聚合和 Responses server-mode 内部上游 SSE cassette 记录，Stage 15A 已接入 upstream API surface 解析校验和 Chat Completions 路由约束，Stage 16A/16B/16C/16D/16E/16F 已接入 Postgres ent migration SQL 生成链路、checked-in migrations、CLI versioned SQL migrator、open-vs-migrate 分离、首轮 store raw SQL 兼容审计、application raw DDL 表覆盖补齐和 Postgres auth `up` 版本化迁移。完整下游边转发 stream/cancel/compact events 仍不是当前基线能力。

Stage 9 audit 表职责边界：

- `request_audits`：入站 Responses request envelope、client request id、redaction/body hash。
- `execution_events`：runtime plan、model/tool/compact/stream/error 生命周期。当前写入 request、内部 model_call、hosted web_search、普通 function requested/submitted 和 deferred stream started/completed 的最小生命周期。
- `upstream_exchanges`：semantic response/request 与 `.http` cassette、trace id、route target 的关联。

后续接入顺序建议先补 Responses audit Monitor UI，再补真实上游 streaming/cancel 和 compact events。

## Session 基线

session 聚合已实现。

提取顺序：

1. `Session_id`
2. `X-Claude-Code-Session-Id`
3. `X-Codex-Turn-Metadata.session_id`
4. `X-Codex-Window-Id` 中 `:` 前缀
5. 空 session

Monitor 和 MCP 都可查询 session 列表和详情。

## 多上游与渠道基线

当前支持：

- legacy `upstream`。
- 多 `upstreams`。
- SQLite 中的 `channel_configs` / `channel_models`。
- YAML 首次 bootstrap。
- Monitor Web 管理 channel/model。
- model discovery / probe。
- channel 和 model 启停。
- route target 选择。
- selected route 记录。
- 健康状态、重试和 sticky routing 事件。

长期配置以 SQLite channel/model 记录为准。

## Monitor 基线

Monitor 当前包括：

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

主要 API：

- `/api/overview`
- `/api/traces`
- `/api/sessions`
- `/api/models`
- `/api/channels`
- `/api/routing/summary`
- `/api/events`
- `/api/findings`
- `/api/analysis`
- `/api/upstreams`
- `/api/auth/*`

## MCP 基线

MCP 当前是 streamable HTTP。

工具范围：

- trace/session/upstream 查询。
- failure clustering。
- routing/sticky 查询。
- system event 查询。
- findings 查询。
- 受控 reanalysis。

MCP 不替代 replay、Monitor 或 SQLite 事实源。

## Observation 与 Reanalysis 基线

当前已经实现：

- OpenAI / Anthropic / Gemini parser registry。
- Observation IR 持久化。
- trace findings。
- parser/analyzer 失败写 system events。
- trace/session/batch reanalysis jobs。
- usage repair。

## 当前非目标

- 公网多租户网关。
- 计费/充值/订阅销售。
- 跨协议转换。
- 用派生数据替代 raw cassette。
- 让测试依赖真实 provider。
- 真实上游增量 Responses server-mode streaming 和 cancel。
- 服务端任意 function tool 执行器、streaming tool events 和 compact workflow。
- 完整真实 stream/cancel/compact execution events 和完整 Postgres migration 生产化；当前仅覆盖最小 `request_audits` 写入、内部 Chat Completions `upstream_exchanges` correlation、request/model_call/hosted web_search started/completed/failed、普通 function tool requested/submitted、deferred stream started/completed 最小 `execution_events`，核心查询服务/Monitor API/MCP/UI 查询，Postgres `db migrate up`/`auth migrate up` 的 versioned SQL 应用路径，application store 的 open-vs-migrate 分离，以及 migrated logs/observation/finding/analysis/system-event 路径的首轮 Postgres raw SQL 兼容。
- provider auto-detect。

## 推荐验证

- 文档：`git diff --check`。
- 小改动：`task check:quick`。
- 代理/路由/存储：`go test ./internal/proxy ./internal/router ./internal/store`。
- 协议/record/replay：`go test ./pkg/llm ./pkg/observe ./pkg/recordfile ./pkg/replay`。
- Monitor：`go test ./internal/monitor`，前端改动补 `task ui:build && task ui:test`。
