# Responses Gateway 差距清单

本文对比 `responses-gateway` 与当前 `llm-tracelab` 已吸收的 Responses API server 能力。

范围限定：

- Responses API server
- Postgres 状态存储
- request audit
- tool runtime / web search / SearXNG
- Codex compatibility
- migration / doctor / config inspect 等生产运维能力

不覆盖跨协议转换、完整 OpenAI Responses API parity、Monitor 通用功能或 replay cassette 格式。

## 对比基准

`responses-gateway` 定位是 Codex-compatible local Responses API semantic gateway：

- HTTP server：`internal/interfaces/http`
- Responses DTO：`internal/protocol/responses`
- 编排核心：`internal/orchestrator`
- vLLM Chat Completions adapter：`internal/adapter/vllm`
- Postgres/memory store：`internal/orchestrator/ent_store.go`、`store.go`
- request/tool audit：`internal/interfaces/http/request_audit.go`、`internal/orchestrator/ent_store.go`
- web search：`internal/tools/websearch`
- 运维 CLI：`cmd/responses-gateway/{doctor,config_inspect,audit,models,migrate}.go`
- 设计文档：`docs/functional-design.md`、`docs/request-audit-cli.md`、`docs/tool-runtime.md`、`docs/codex-compatibility-profile.md`

`llm-tracelab` 当前定位仍是 record/replay proxy，Responses server-mode 是可选本地语义层：

- 代理分流和内部 Chat Completions 调用：`internal/proxy/responses_server.go`
- Responses HTTP handler：`internal/responses/httpapi`
- Responses runtime：`internal/responses/runtime`
- Responses protocol DTO：`internal/responses/protocol`
- audit：`internal/responses/audit`
- server-side function executor：`internal/responses/functionexec`
- web search：`internal/responses/tools/websearch`
- 应用 store / ent / migrations：`internal/store`、`internal/appdbmigrate`、`ent/postgres-migrations`
- CLI 运维：`cmd/server/{db,auth,provider,schema}.go`
- 当前事实文档：`docs/CURRENT_IMPLEMENTATION.md`、`docs/PROJECT_BASELINE.md`

## 已吸收能力

### Responses server 基本语义

- 已吸收：`POST /v1/responses` 本地 server-mode、`previous_response_id` continuation、function call / function_call_output 往返、`POST /v1/responses/compact` 首切、`/v1/responses/:id/input_items`。
- llm-tracelab 落点：`internal/responses/httpapi/handler.go`、`internal/responses/runtime/runtime.go`、`internal/responses/runtime/ent_store.go`。
- 差异：llm-tracelab 通过现有 proxy/router/recorder 调内部 `/v1/chat/completions`，保留 `.http` cassette；responses-gateway 直接把 vLLM 作为 adapter client。

### Streaming 首切

- 已吸收：简单文本增量、普通 function arguments delta/done、provider 就绪 `web_search` 与已注册 executor 的 stream tool loop 首切。
- llm-tracelab 落点：`internal/responses/runtime/runtime.go`、`internal/proxy/responses_server.go`、`internal/responses/chatclient`。
- responses-gateway 对照：`internal/interfaces/http/router.go`、`internal/orchestrator/runtime.go`。
- 差异：llm-tracelab 对 auto compact 等复杂路径仍 fallback 到 deferred SSE envelope；当前已明确 auto compact 与不支持工具组合的 runtime fallback contract，并能在写出 SSE/调用上游前返回带 reason 的 `ErrIncrementalStreamUnsupported`，但真实 streaming auto compact 仍未完成。responses-gateway 文档强调 typed event lifecycle，但也以 Codex fixture 驱动分阶段落地。

### Postgres 状态存储

- 已吸收：Responses semantic state 使用 ent-backed store，Postgres checked-in migration 可通过 CLI 应用。
- llm-tracelab 落点：`internal/responses/runtime/ent_store.go`、`internal/store`、`internal/appdbmigrate`、`ent/postgres-migrations`、`cmd/server/db.go`。
- responses-gateway 对照：`internal/orchestrator/ent_store.go`、`ent/schema`、`cmd/responses-gateway/migrate.go`。
- 差异：llm-tracelab 同时维护 SQLite startup schema fallback 和 Postgres versioned SQL；responses-gateway 的目标更集中在 Postgres/memory store。

### Request audit 基础链路

- 已吸收：入站 Responses request audit、内部 Chat Completions upstream exchange correlation、execution events、Monitor/MCP 查询入口。
- llm-tracelab 落点：`internal/responses/audit`、`internal/mcpserver/responses_audit.go`、`internal/monitor` 的 `/api/responses/audit/trace` 和 `/api/responses/audit/tool-calls`。`tool_call_audits` 独立表、ent schema、SQLite/Postgres migration、QueryService 查询 API、`audit tool-calls` CLI、Monitor/MCP 查询面和 runtime web_search/function executor 双写已落地。
- responses-gateway 对照：`internal/interfaces/http/request_audit.go`、`docs/request-audit-cli.md`、`cmd/responses-gateway/audit.go`。
- 差异：llm-tracelab 当前可按 `response_id` / `request_audit_id` 查询 trace，并可按 client request、conversation、status 和由 method/path 派生的 operation 列表过滤；responses-gateway 的 CLI 仍有更完整的 thread/session/turn 与 Codex-specific 诊断组织。

### Hosted web search / SearXNG

- 已吸收：`mock` 与 `searxng` provider、`tools.web_search` 配置、hosted `web_search` / `web_search_preview` server-side 执行、tool_call audit events。
- llm-tracelab 落点：`internal/responses/tools/websearch/provider.go`、`internal/responses/runtime/runtime.go`、`internal/config/config.go`。
- responses-gateway 对照：`internal/tools/websearch/provider.go`、`internal/orchestrator/server_tools.go`。
- 差异：llm-tracelab provider 已补 `max_results` 查询参数和配置默认值；缺口主要在诊断 CLI 与完整 hosted-tool lifecycle，而不是 provider 本身。

### Server-side function executor

- 已吸收并扩展：默认空 registry、YAML opt-in `static_response` / `external_command` executor、timeout、max result bytes、redaction、轻量 process policy、Monitor 安全 overlay。
- llm-tracelab 落点：`internal/responses/functionexec`、`internal/responses/runtime/function_executor.go`、`cmd/server/serve.go`、`internal/monitor`。
- responses-gateway 对照：`internal/orchestrator/tool_runtime.go` 的 `gateway_builtin` 最小框架和 `gateway_echo`。
- 差异：llm-tracelab 没有采用 `gateway_builtin` descriptor 名称，而是对已注册普通 `function` 做 server-side opt-in 执行；需要在后续文档/协议中继续明确“默认 client-owned，配置命中才 server-owned”的边界。

### Provider API surface 诊断

- 已吸收并扩展：provider probe、probe-report、probe-apply、Monitor provider setup validate/apply、startup in-memory fill。
- llm-tracelab 落点：`cmd/server/provider.go`、`cmd/server/provider_startup_probe.go`、`internal/providerprobe`、`internal/monitor`。
- responses-gateway 对照：`cmd/responses-gateway/doctor.go` 只探测 vLLM `/models`，范围更窄。

### Doctor 启动前诊断首切

- 已吸收首切：`llm-tracelab doctor` 读取同一套配置，输出稳定 JSON envelope，`result` 包含 overall status、summary counts、redacted config summary 和 checks 列表。
- 当前覆盖：`config.load`、`server.port`、monitor/MCP consistency、application database migration mode/report、Responses server backend validation、`responses_server.http_guard` 离线入口边界诊断、`responses_server.default_model`、Responses store driver/auto-migrate readiness、`responses_server.store_health` store/backend 健康诊断、model profile context-window/compact threshold numeric relationships、默认模型与本地 SQLite `model_catalog` / `channel_models` drift 只读诊断、显式 `doctor --codex-config <path>` 本地 Codex TOML drift 只读诊断、web_search provider config validation、provider config basic count、auth migration scope note。
- 安全边界：默认离线，不做真实模型推理、provider 网络请求或远端数据库连接；`responses_server.store_health` 默认只报告 driver、auto_migrate、force_store、migration mode 和 required table set，只有显式 `--check-db` 才读取数据库 migration status 并检查 Responses 关键表；只有显式 `--probe-providers` 才执行受控 provider endpoint probe；未传 `doctor --codex-config` 时不读取用户真实 Codex 文件，传入 path 后 missing/unreadable/parse_error/drift 只产生 warn；输出复用 DSN/URL 脱敏摘要，不输出 API key/header secret 或 Codex TOML 文件内容。
- llm-tracelab 落点：`cmd/server/doctor.go`，复用 `appDBMigrationReport`、`router.ValidateLocalResponsesServerBackendConfig`、`websearch.NewProvider` 和 `config inspect` 的脱敏摘要。
- 剩余缺口：HTTP guard 诊断已覆盖 Responses path、归一化 path、body limit、force_store、auth verifier 配置状态、server/monitor port 摘要以及 MCP/monitor management path 明显冲突。Store/backend 深度健康已通过 `responses_server.store_health` 首切：server-mode 关闭时 pass 并标注 skipped reason；默认离线报告 required semantic/audit/settings table set；`force_store=true` 且 `database.auto_migrate=false` 且未传 `--check-db` 时 warn；显式 `--check-db` 时复用 app DB status check，并检查 `responses`、`response_items`、`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`、`app_settings` 是否存在，缺表或 DB 不可达为 fail。Codex profile 建议和显式 `--codex-config <path>` 本地 TOML drift 检查已在 `models codex-config` 落地；`doctor --codex-config <path>` 复用同一套本地 TOML drift helper；model profile drift 与 catalog/channel 的交叉校验已在 `models codex-config` 与 `doctor` 中落地本地 SQLite 只读诊断；DB 不可用、非 SQLite 或 Postgres 配置在默认模式保持离线回落。
### Codex compatibility profile

- 已吸收首切：新增独立 [Codex Responses 兼容性首切](./CODEX_RESPONSES_COMPATIBILITY.md)，固化 text create、stream text、function call、`function_call_output` continuation、ordinary `web_search` descriptor、unsupported hosted tool 的最小兼容合约。
- fixture 资产：新增 `tests/fixtures/codex/` 离线 examples，覆盖请求、期望 response/event/error 形状；`task test:codex-fixtures` 已接入 focused 离线 runner，会枚举当前 fixture inventory 并校验 JSON/NDJSON 结构、HTTP handler reachability 和最小 runtime/parser 对齐；不依赖真实 Codex、真实模型或网络。
- llm-tracelab 落点：`docs/CODEX_RESPONSES_COMPATIBILITY.md`、`tests/fixtures/codex/*`、`internal/responses/codexfixtures`、`internal/responses/httpapi`、`internal/responses/runtime`，运行时事实仍以 `internal/responses/httpapi`、`internal/responses/runtime`、`internal/proxy/responses_server.go` 为准。
- 剩余缺口：fixture runner 不是完整真实 Codex/e2e runner；Codex TOML/profile 生成已接本地 SQLite catalog/channel drift 诊断，并可在显式传入 `--codex-config <path>` 时检查用户机器上的 Codex 本地配置文件 drift；unsupported hosted tools 已有强制执行时的 stable `unsupported_tool` gate 和 rejected tool-call audit，但尚无真实执行器；Codex-specific audit diagnostics 仍只覆盖 response/request/tool-call 查询首切。

## 部分吸收能力

### Request audit 诊断深度

- 已有：`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`，以及 Monitor/MCP trace 查询；`audit query` CLI 已提供 response/request/client-request/conversation 维度只读 trace 查询首切，并已在 JSON/text trace 输出中提供顶层 `diagnostics`，汇总 event/upstream/tool-call count、latest status、cancel/failed/stream/compact signals、pending tool call count/list 和保守 compact candidate/summary。`audit query --list` 可按 conversation/client-request/status/operation 等现有 selector 返回 request audit summary 列表，`--status` 支持 `accepted`、`completed`、`failed`、`rejected`、`cancelled`，`--operation` 支持 `create`、`compact`、`input_items` 且只允许和 `--list` 搭配；operation 由 `request_audits.method/path` 派生，并在 QueryService 层下推为 method/path predicate，不读取 raw body/header。列表字段限于 id、response_id、conversation_id、client_request_id、status、operation、created_at。Monitor `/api/responses/audit/tool-calls` 和 MCP `responses_audit_tool_calls` 已提供独立 tool-call audit read model 查询；QueryService 的 `tool_call_audits` 查询已支持 status、tool_type、tool_name、executor、call_id、response/request/conversation selector，并可按 call_id 聚合出 latest lifecycle status、statuses_seen、event_count 和时间戳。当前还可通过 `--include-tools` 从既有 `execution_events.event_type == "response.tool_call"` 派生保守的 tool call diagnostics，汇总 call id、工具名、executor、状态序列、latest status、started/completed 时间、error/output/query/arguments 的脱敏摘要和事件计数。
- 缺口：仍缺少真实 thread/session/turn schema 字段和对应范围查询；`audit query` 的 compact/pending/stream/cancel diagnostics 是基于已取到 request audit、events、exchanges 和 derived tool calls 的保守派生，不输出 raw sensitive payload。`audit query --include-tools` 仍是 derived event 视图，未来真实 MCP/file/code/computer-use runtime lifecycle 还没有统一写入 `tool_call_audits` read model。
- llm-tracelab 下一步落点：扩展 `cmd/server/audit.go`，复用并扩展 `internal/responses/audit.QueryService`。

### Compact 与 context optimization

- 已有：显式 compact API、item-count auto compact、基于 context window 的 token-budget auto compact、`/tokenize` counter provider 自动选择首切；compact v2 provenance 首切已把安全 lineage/read-model metadata 写入 compact response 的 `metadata._gateway.compact`，包含 source/compact response id、source item/window 计数、只含 id/type/status/role/call_id/name 的 source/retained item refs、retained summary boundary、summary item ids、budget/trigger 信息和 auto/manual 标记。auto compact 的 `response.compact` / `auto_triggered` execution event 会引用/摘要该 metadata provenance；这些路径不输出 raw prompt、raw summary、raw tool args 或 tool output。
- 缺口：auto compact stream 仍 fallback；compact provenance 当前仍以内嵌 response metadata 作为最小 read model，尚无独立 schema/query API；artifact-bearing item 策略和完整 context optimization 未接入。
- llm-tracelab 落点：`internal/responses/runtime/runtime.go`、`estimator.go`、`profile.go`、`internal/proxy/responses_tokenize_counter.go`。
- responses-gateway 对照：`docs/functional-design.md` 的 Compact 章节。

### Hosted/server-side tool lifecycle

- 已有：hosted `web_search` 与 registered executor 的 started/completed/failed execution events 和 stream item 首切；强制执行 unsupported hosted tools 时会返回 stable rejection contract 并写 `tool_call_audits` rejected read model；`audit query --include-tools` 已基于 execution events 提供 derived tool call summary。独立 `tool_call_audits` 表、recorder、query API、runtime web_search/function executor 双写、unsupported hosted tool rejected 写入、`audit tool-calls` CLI、Monitor API 和 MCP tool 已落地，SQLite startup schema、SQLite migration、Postgres migration 与 status 检查也已覆盖。`audit tool-calls` CLI 现在可用 `--status started|completed|failed|rejected`、`--tool-type`、`--tool-name`、`--executor` 过滤 durable read model，并可用 `--latest-by-call` 输出按 call_id 聚合的 latest status、event_count、statuses_seen 和 lifecycle timestamps；默认输出限制为 payload summary、redacted error summary、metadata/count/status，只有显式 `--include-payloads` 才输出 raw input/output/metadata JSON。
- 缺口：跨轮/混合工具失败 lifecycle 仍不完整；MCP、file search、code interpreter、computer-use 仍未实现真实执行器，当前只有强制执行时的 stable rejection + audit contract。
- llm-tracelab 下一步落点：`internal/responses/audit`、`internal/responses/runtime`、`internal/responses/functionexec`。
- responses-gateway 对照：`docs/tool-runtime.md` 的 `tool_call_audits` 和 MCP runtime boundary。

### Migration 运维

- 已有：Postgres application `db migrate up/status/down --dry-run`、auth migrate、SQLite status 解释、open-vs-migrate 分离；SQLite application report 已稳定输出 `sqlite_schema_strategy: startup_schema_fallback`、`sqlite_versioned_migration_status: not_implemented` 和 migration advice；`auth migrate status` / dry-run 已输出 auth required tables、table health、Postgres shared namespace strategy 和 independent auth namespace `not_implemented` 方案边界。
- 定案边界：SQLite application DB 长期作为 local-first `startup_schema_fallback` 保留；本轮不实现 versioned SQLite migrator，`db migrate status --check-db` 只读解释 marker/required table 状态，不承担 destructive repair、文件创建或数据 rewrite。Postgres auth 继续共享 application `schema_migrations` namespace；独立 auth namespace 未实现，当前通过 status/dry-run 字段明确约束。
- 缺口：Postgres runtime SQL 仍需继续扩大 analytics/eval/experiment/monitor 查询覆盖；独立 auth namespace 只有迁移门禁设计，尚未实现 adoption path、独立 migration source、双 namespace read-only status 和 auth-only rollback 语义。
- llm-tracelab 落点：`cmd/server/db.go`、`cmd/server/auth.go`、`internal/appdbmigrate`、`internal/auth/migrate.go`。
- responses-gateway 对照：`cmd/responses-gateway/migrate.go`、`ent/migrations`。

### Machine-readable CLI schema

- 已有：`schema` 命令输出 CLI machine contract。
- 缺口：schema 不是配置 inspect；它不能展示合成后的有效配置、脱敏 DSN/API key、Responses server/tool/provider 生效状态。
- llm-tracelab 落点：`cmd/server/schema.go`。
- responses-gateway 对照：`cmd/responses-gateway/config_inspect.go`。

## 仍缺能力

### `doctor` 深度诊断

- 已吸收首切：`llm-tracelab doctor` 覆盖离线启动前诊断、稳定 JSON envelope、`--check-db`、`--probe-providers` 受控 provider endpoint probe、`--codex-config <path>` 本地 Codex TOML drift 只读诊断、`--fail-on-warn`、`--fail-on-fail`、Responses default model、HTTP guard、store driver/auto-migrate readiness、`responses_server.store_health` store/backend 深度健康、model profile context-window/compact threshold numeric relationships，以及默认模型与本地 SQLite `model_catalog` / `channel_models` drift 只读诊断。`responses_server.http_guard` 会在 server-mode 关闭时 pass 并标注 skipped reason；开启时离线输出 path、normalized path、body limit、force_store、auth verifier 配置状态、server/monitor port 摘要，并对非法 path、过小 body limit 和 MCP/monitor management path 明显冲突给出 fail/warn。`responses_server.store_health` 默认不连接数据库，只报告 driver、auto_migrate、force_store、migration mode 和 required table set；显式 `--check-db` 时复用 app DB status check 并检查 Responses semantic/audit/settings 关键表，缺表或 DB 不可达为 fail 且输出脱敏。`responses_server.codex_config_drift` 未传 flag 时 pass 并标注 `not_configured`；missing/unreadable/parse_error/drift 均为 warn，不会让 doctor 默认失败。
- 剩余缺口：`models codex-config` 已支持 Codex profile 建议，并可通过显式 `--codex-config <path>` 只读检查本地 Codex 配置 drift；`doctor --codex-config <path>` 复用同一套 TOML parsing、字段比较和 URL 脱敏 helper；`models codex-config` 与 `doctor` 均能在本地 SQLite app DB 可用时检查请求模型的 catalog/channel drift；默认 doctor 不会触发真实网络或远端数据库连接，只有 `--check-db`/`--probe-providers` 才进入相应外部检查。
- responses-gateway 能力：读取同一套配置，输出稳定 JSON，检查 config、HTTP guard、默认模型、vLLM `/models`、model profile context window drift、store backend、web_search 配置。
- llm-tracelab 后续落点：继续扩展 `cmd/server/doctor.go`，受控复用 `internal/providerprobe` 和更细的 responses/profile/store 诊断，但保持默认离线。

### `config inspect` 有效配置视图

- 已新增首切：`llm-tracelab config inspect --format json` 读取同一套配置加载链路，输出稳定 envelope、脱敏后的 effective 摘要和保守 `sources` 摘要。
- 当前覆盖：server/monitor/MCP、database driver/DSN/auto_migrate、trace output dir、Responses server、web_search、provider_probe、upstream targets 与 credential/static model 计数；`sources` 区块覆盖主要字段和 upstream targets/credentials，来源枚举采用 `config_file`、`default`、`effective`、`empty`、`derived`、`not_configured`。
- 剩余缺口：尚未做完整逐字段 provenance、CLI flag 与 env 的精确区分，也未做 doctor 式联动校验。
- responses-gateway 能力：明确默认值、config 文件、env、CLI flag 优先级，并支持脱敏输出。
- llm-tracelab 建议落点：`cmd/server/config_inspect.go` 或 `cmd/server/config.go`，复用 `internal/config` 的 default/effective helper 和 `config.RedactDSN`。

### `audit query` CLI

- 已吸收首切：`llm-tracelab audit query`（别名 `audit responses`）提供面向 agent 的只读 Responses audit trace 查询入口，复用当前 config 的 application store 和 `internal/responses/audit.QueryService`。
- 当前用法：`llm-tracelab -c config.yaml --format json audit query --response-id resp_x --include-events --include-exchanges --include-tools --limit 100`，也支持 `--request-audit-id`、`--client-request-id`、`--conversation-id`；多个 selector 同时给出时按 AND 过滤，conversation/client 命中多条时默认返回最新一条 trace；默认输出 request audit envelope 和顶层 diagnostics，events/exchanges/tool calls 需显式打开。`--list` 会返回 matching request audit summary 列表而不是最新 trace，适合 conversation/client-request 范围排查；`--status` 和 `--operation create|compact|input_items` 只在 `--list` 下可用。独立 `audit tool-calls` 可查询 durable `tool_call_audits` read model，支持 response/request/conversation/call/status/tool_type/tool_name/executor 过滤，以及 `--latest-by-call` lifecycle 聚合摘要。
- 安全边界：CLI 输出 request audit 的已存 `body_preview` / hash / redaction metadata，不读取或输出未脱敏 raw request body；`--list` summary 不含 body/header raw，operation 只由 `request_audits.method/path` 派生。当前 `tool_calls` 和 diagnostics 仍是 derived conservative view，不输出 raw arguments/query/output/error，只输出 redacted summary、事件引用或计数/状态。`--include-events` 仍按原行为输出 events 的 `details_json`，需要调用者自行按权限使用。
- responses-gateway 能力：按 response/request/thread/session/turn/client request id 查询，并输出 diagnostics envelope。
- 剩余缺口：尚未支持真实 thread/session/turn 字段和范围查询；compact candidate、pending tool call、stream/cancel/request diagnostics 已有保守顶层派生，但尚不是完整 Codex-specific diagnostics；`audit tool-calls`、Monitor API 和 MCP tool 已提供独立 `tool_call_audits` 查询。MCP、file search、code interpreter、computer-use 的真实 server-side executor 仍未实现，本次只是已写入 lifecycle audit 的查询可操作性增强。

### Codex profile 生成命令

- 已吸收首切：`llm-tracelab models codex-config <model>` 离线读取 `responses_server.model_profiles`，输出 `models.codex_config` JSON envelope、Codex TOML 建议、provider `base_url` 推导、`wire_api=responses`、profile match/compact threshold diagnostics 和 warnings；diagnostics 已显式输出 `runtime_profile_source=responses_server.model_profiles`、`profile_precedence=[responses_server.model_profiles, zero_limits_when_unmatched]`、`catalog_profile_role=diagnostic_only`、`capability_source=provider_upstream_capabilities`、`provider_channel_profile_adoption=observe_only`、`profile_adoption_report`、`profile_conflict_strategy=responses_server.model_profiles_wins` 和 required gates。本地 SQLite app DB 文件可用时，会以 `AutoMigrate:false` 打开 store 并只读检查 `model_catalog` / `channel_models` 中是否存在请求模型，输出 `catalog_model_present`、`channel_model_present`、`channel_model_count`、source 状态和 drift warnings；`profile_adoption_report` 是机器可读 dry-run/conflict report，只读展示 channel profile candidate、`context_window_tokens` 字段级 diff、显式 `responses_server.model_profiles` 保护、`supports_chat_completions=false` 阻断和 `mutates=false`；显式传入 `--codex-config <path>` 时，还会只读解析本地 Codex TOML，检查 `[profiles.<model>]`、`[model_providers.llm-tracelab]` 和 profile/provider 关键字段 drift。
- 安全边界：不探上游网络、不运行真实 Codex、不输出真实 API key/header secret/DSN；未传 `--codex-config` 时不读取用户真实 Codex 文件；传入 path 后，文件不存在、不可读或解析失败只产生 diagnostics/warnings，不会 panic 或回显完整文件内容，URL actual 会脱敏。DB 不存在、`:memory:`、非 SQLite、打开失败或 Postgres 配置时均保持离线回落。无 profile 时不失败，输出 0 值并 warning。
- 剩余缺口：尚未从数据库 catalog/channel 合并 profile 参数；provider/channel profile 成为 runtime budget/profile 事实源前，还需要实现 schema migration、rollback plan 和 DSN-gated tests，并把 observe-only dry-run/conflict report 推进到可控 adoption。
- responses-gateway 能力：输出 `model_context_window`、`model_auto_compact_token_limit`、provider `base_url`、`wire_api=responses` 等稳定 JSON/TOML。
- llm-tracelab 建议落点：`cmd/server/models.go` 或 `cmd/server/provider.go` 子命令；数据来源应优先是 `responses_server.model_profiles` 和 channel/model catalog。

### Codex fixture/runbook 资产

- 已吸收首切：已有集中 `tests/fixtures/codex` 离线 fixture、`docs/CODEX_RESPONSES_COMPATIBILITY.md` profile 文档、`internal/responses/codexfixtures` runner helper、`task test:codex-fixtures`，以及 `internal/responses/httpapi` / `internal/responses/runtime` focused offline Go tests。
- 剩余缺口：当前是 focused 离线 fixture gate，不是完整真实 Codex/e2e runner；没有长任务 compact/cancel/run-report 脚本资产；Codex TOML/profile 生成已有本地 SQLite catalog/channel drift 首切，并可通过显式 `--codex-config <path>` 检查 Codex 本地配置文件 drift。
- responses-gateway 能力：`docs/codex-longrun-compact-runbook.md`、`scripts/codex-longrun-*.sh`、Codex fixture profile。
- llm-tracelab 建议落点：在现有离线 fixtures 基础上决定是否引入脚本；不要让测试依赖真实 Codex 或网络。

### MCP hosted tool runtime

- 已吸收首切：`mcp`、`file_search`、`code_interpreter`、`computer_use_preview` 在强制 `tool_choice` 执行时返回 OpenAI-style `unsupported_tool` error；普通 descriptor 仍按兼容输入保守解析，不伪造执行结果。
- 缺口：MCP 当前仍是 llm-tracelab 对外排障 server，不是 Responses runtime 内部的 MCP client/tool executor；尚无 file/code/computer-use 执行器；独立 `tool_call_audits` 表已落地并承接 web_search/function executor 与强制 unsupported hosted tool rejection，但尚未承接未来真实 MCP/file/code/computer-use execution lifecycle。
- responses-gateway 设计：`type:"mcp"` descriptor 作为 gateway-hosted runtime 请求，当前先明确拒绝并审计。
- llm-tracelab 下一步落点：`internal/responses/audit` 的 `tool_call_audits` 对未来 MCP/file/code/computer-use execution lifecycle 的扩展；执行器本身需单独设计安全边界。

## 建议下一阶段优先级

1. 扩展 `doctor` 深度诊断与 `config inspect` 来源联动。
   - 价值：降低 server-mode/Postgres/web_search/Codex 接入排障成本。
   - 模块：`cmd/server/doctor.go`、`cmd/server/config.go`、`internal/config`、`internal/providerprobe`。
   - 验收：在首切稳定 JSON envelope、受控 provider probe 和 config inspect sources 基础上补 profile/store/default-model 深度诊断；继续默认脱敏、无真实模型推理。

2. 扩展 `audit query` CLI，并复用现有 QueryService。
   - 价值：把 Monitor/MCP 才能看的 Responses audit 变成 agent 可脚本化入口。
   - 模块：`cmd/server/audit.go`、`internal/responses/audit/query.go`。
   - 验收：在首切 response/request 查询基础上补 client-request/conversation 查询；输出 request audit、events、upstream exchanges；不输出未脱敏 raw body。

3. 接入 Codex fixture runner 和 profile 生成。
   - 价值：把已固化的最小兼容合约变成可回归检查，并减少 Codex 本地配置漂移。
   - 模块：`tests/fixtures/codex`、`internal/responses/httpapi`、`runtime` 测试、`cmd/server/provider.go` 或新增 `cmd/server/models.go`。
   - 验收：在已接 focused offline tests 基础上，补更完整 runner 或 e2e harness；输出 JSON + TOML profile 片段。

4. 扩展 hosted tool audit 覆盖面。
   - 价值：让 unsupported hosted tools、未来 MCP/file/code 工具共享可索引、可长期演进的排障面。
   - 模块：`internal/responses/runtime`、`internal/responses/functionexec`、`internal/responses/audit`、`cmd/server/audit.go`、Monitor/MCP audit surface。
   - 验收：在已落地的 web_search/executor/rejected hosted tool `tool_call_audits` 写入、CLI、Monitor/MCP 查询入口基础上，补未来执行器 lifecycle；继续保留 derived `--include-tools` 作为 event fallback。

5. 补 model/Codex profile inspect。
   - 价值：让 Codex 本地配置与 `responses_server.model_profiles`、channel model catalog 保持一致。
   - 模块：`cmd/server/provider.go` 或新增 `cmd/server/models.go`、`internal/config`、`internal/store`。
   - 验收：输出 JSON + TOML 片段；明确 compact threshold 来源和 context window margin。

6. 继续 migration 生产化。
   - 价值：减少 Postgres/SQLite/auth schema 运维歧义。
   - 模块：`internal/appdbmigrate`、`internal/auth/migrate.go`、`cmd/server/db.go`、`cmd/server/auth.go`。
   - 当前验收已收敛：SQLite 明确继续 `startup_schema_fallback`，status/dry-run 输出稳定 advice，`--check-db` 只读；Postgres auth 明确 shared application namespace，`auth migrate status --check-db` 能只读报告 auth-owned table presence。
   - 下一步验收：补 Postgres runtime SQL 覆盖；独立 auth namespace 进入 adoption design，必须先定义 dry-run/status 双 namespace 字段、shared deployment adoption、auth-only rollback 语义和测试门禁，再实施。

## 不建议直接搬运的点

- 不要把 llm-tracelab 改成纯 semantic gateway；record/replay proxy 和 `.http` cassette 仍是项目主线。
- 不要把 `responses-gateway` 的 vLLM-only 假设写死到 llm-tracelab；现有 router/upstream capability 约束应继续保留。
- 不要绕过现有 Monitor/MCP/store 事实源另建一套 audit 数据。
- 不要为了补工具 runtime 直接执行任意 MCP/code/computer-use；先 recognized-and-rejected、审计和稳定错误码。
