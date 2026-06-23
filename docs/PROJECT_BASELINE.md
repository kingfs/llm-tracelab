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
- 开启 `responses_server.enabled=true` 后，配置的 Responses path 由本地 runtime 处理，当前通过内部上游 `/v1/chat/completions` 调用实现 Responses 响应；该内部调用会受 upstream `api_type` / capabilities 约束，不会选择显式关闭 Chat Completions 能力的 Responses-native target，也不会在请求带 `tools` 时选择 `capabilities.tool_calling: false` 的 target。下游 `stream:true` 时，简单文本输出路径已能边读取内部 Chat Completions SSE、边输出 Responses `response.output_text.delta`；普通 `function` tool 参数分片已能输出 `response.function_call_arguments.delta/done`，并仍记录原始 OpenAI-compatible SSE cassette。已注册 server-side function executor 的 stream 首切会输出 arguments delta/done、执行 executor 前输出 started 态 `response.output_item.added` tool item、成功后输出完成态 `response.output_item.done` tool item、再继续流式输出最终文本，并写带 `stream=true` 的 tool_call started/completed/failed events。同一轮多个已注册 executor call 会按模型 tool call 顺序逐个输出 started/done；若后续 call 执行失败，已完成 call 保留 completed done，当前失败 call best-effort 输出 failed done 并把原错误交回 HTTP 层。provider 就绪的 hosted `web_search` stream 也会输出 arguments delta/done、执行 server-side search 前输出 started 态 `response.output_item.added` `web_search_call` item、成功后输出完成态 `response.output_item.done`、注入结果并继续最终文本 delta，tool_call started/completed/failed events 同样写 `stream=true`。stream tool loop 已在 executor/provider 失败时 best-effort 输出 failed `response.output_item.done`，之后仍让 HTTP 层追加最小 `response.failed` 并记录 failed audit。auto compact 等复杂路径仍会 fallback 到 deferred envelope；更复杂的跨轮/混合工具 failed lifecycle 仍未完成。
- `provider probe` 是当前手动 provider detection 入口，会对配置中的 upstream endpoint 做保守探测并输出建议的 `api_type`、`protocol_family` 和 capability signals；`doctor --probe-providers` 会显式复用同一套 provider probe 并输出脱敏摘要；`provider probe-report` 是同类只读批量报告入口，面向 YAML upstream 列表输出 report，不写配置。默认启动不执行 probe；显式开启 `provider_probe.startup_fill=true` 后，serve 只在内存中填补 YAML upstream 缺失字段，不写回配置，也不覆盖显式配置。`config inspect` 会输出脱敏 effective config，并通过 `sources` 摘要以保守枚举标注主要字段来自 config file、default、effective、empty、derived 或 not_configured。Monitor provider create dialog 提供临时 preview endpoint；`POST /api/provider-setup/validate` 已作为 provider setup wizard 首切，会组合 base URL、API key、provider preset、model discovery 和 capability 字段返回 provider probe、归一化配置和 redacted secret state，不落库且不回显 API key；create dialog 已收敛为 validate -> review normalized config/probe/secret state -> create 的状态流，字段变更会清空旧验证结果；`POST /api/provider-setup/apply` 复用归一化逻辑；当 probe 检测成功，或用户显式提供 `api_type` 与 `protocol_family` 时，才写入 channel store。setup 建议只填补缺失字段或未声明 capability，不覆盖显式 `api_type`、`protocol_family` 或 capability false。`POST /api/provider-probe/report` 会面向 channel 列表返回只读批量 detection report，不写 probe run、model 或 channel 配置；provider detail 的 probe 动作也会返回同类 detection report，并支持用户显式 Apply suggestions 写入表单或 channel 配置。Monitor Providers 列表页提供 Batch probe and apply 首切：先预览只读 report，再调用 `POST /api/provider-probe/report/apply` 批量写入 detected 且可补的非敏感建议；CLI `provider probe-apply` 复用同一 channel service 用例。批量写入口不接收或返回 API key，只填缺失 `api_type`、`protocol_family` 和未设置 capability，不覆盖显式配置或 capability false，Monitor 写入成功后会 reload router。
- `responses_server.model_profiles` 支持按 `name` 或 `pattern` 匹配 model，声明 `context_window_tokens`、`max_output_tokens`、`compact_history_item_threshold` 和 `upstream_model`。当前 runtime 会使用匹配 profile 的 `compact_history_item_threshold` 覆盖全局 item-count 自动 compact 阈值；配置 `upstream_model` 时，内部 Chat Completions 请求使用该上游模型名，但外部 Responses `model` 仍保留客户端请求 model 或默认 model；配置 `max_output_tokens` 时，会在客户端未显式传 `max_output_tokens` 时作为内部 Chat Completions `max_tokens` 默认值；配置 `context_window_tokens` 且开启 auto compact 时，会通过可注入 token estimator 估算 prompt+reserved output，超预算则触发 compact。默认 estimator 已通过 adapter-backed chat prompt counter 包装确定性保守计数器，adapter 失败会 fallback。当 profile 有 `name` 或 `pattern`、配置了 `context_window_tokens`、未显式 `tokenize_counter.enabled=false`，并匹配 upstream/router target 的 `capabilities.tokenize=true` 时，proxy 会自动用该 target 的 base URL、API key 和 headers 构造 HTTP provider `/tokenize` counter；显式 `tokenize_counter.enabled=true` 仍可强制启用，显式 false 可关闭。当前 compact v2 provenance 首切会把安全 lineage/read-model metadata 写入 compact response 的 `metadata._gateway.compact`，覆盖 source/compact response id、source item/window 计数、安全 item refs、retained summary boundary、summary item ids、budget/trigger 和 auto/manual 标记；auto compact event details 引用/摘要该 provenance。完整 context optimization、独立 compact read model schema 和 streaming auto compact 尚未接入。
- Codex 兼容性已有离线 gate、配置建议命令和 doctor drift/HTTP guard/store health 诊断。`task test:codex-fixtures` 运行 focused fixture runner，枚举 `tests/fixtures/codex` 当前 inventory 并校验 JSON/NDJSON、HTTP handler reachability 和最小 runtime/parser 对齐；测试不依赖真实 Codex、真实模型、网络或 Postgres。`models codex-config <model>` 离线输出 JSON envelope 与 Codex TOML 建议；显式传入 `--codex-config <path>` 时会只读解析本地 Codex TOML，对比 `[profiles.<model>]` 与 `[model_providers.llm-tracelab]` 关键字段并输出字段级 drift diagnostics，未传 flag 时标记 `not_configured` 且不读取用户真实文件。`doctor` 的 `responses_server.model_catalog_drift` check 会复用默认模型与 profile match，在本地 SQLite application DB 文件可用时只读检查请求模型是否存在于 `model_catalog` 与 `channel_models`，并输出 catalog/channel drift diagnostics。`doctor --codex-config <path>` 通过 `responses_server.codex_config_drift` 复用同一套本地 Codex TOML drift helper；未传 flag 时 pass 并标注 `not_configured`，missing/unreadable/parse_error/drift 均为 warn 且不会让 doctor 默认失败，输出不包含 TOML 文件内容、API key/token 或未脱敏 URL secret。`responses_server.http_guard` check 会离线输出 path、normalized path、body limit、force_store、auth verifier 配置状态、server/monitor port 摘要，并检查非法 path、过小 body limit 和 MCP/monitor management path 明显冲突。`responses_server.store_health` check 默认离线报告 database driver、auto_migrate、force_store、migration mode、required semantic/audit/settings table set 和是否建议 `--check-db`；显式 `doctor --check-db` 时复用 app DB status check，并检查 `responses`、`response_items`、`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`、`app_settings` 是否存在，缺表或 DB 不可达为 fail。默认 DB 不可用、`:memory:`、非 SQLite 或 Postgres 配置时保持离线回落，输出会脱敏 DSN 和 DB 错误。
- 开启 `tools.web_search.enabled=true` 后，Responses runtime 可执行 hosted `web_search` / `web_search_preview` 首切，provider 支持 `mock` 和 SearXNG；有 ent-backed audit store 时会写 hosted web_search `response.tool_call` started/completed/failed events，`stream:true` 路径会用真实 stream tool loop 执行 hosted search 并写 `stream=true`。强制 `tool_choice` 选择未启用的 `web_search` 或未实现的 `mcp` / `file_search` / `code_interpreter` / `computer_use_preview` 时，runtime 返回稳定 `unsupported_tool` error，并写入不含 raw descriptor/payload 的 `tool_call_audits` rejected 记录。普通 `function` tool 默认仍走客户端回路；runtime 现在提供默认空的 server-side function executor registry。配置 `responses_server.function_executors.enabled=true` 并声明可用的 `static_response` 或 `external_command` executor 后，runtime 才会自动执行同名 function tool，并按 timeout、max-result-bytes 和 audit redaction policy 写 started/completed/failed events。`external_command` 默认不继承环境变量、不使用 shell，通过 stdin JSON 接收 tool call，stdout 作为 tool output，stderr 只进入失败摘要；可选 `process.working_dir` 会把子进程限制到显式绝对工作目录，`process.require_absolute_command=true` 会拒绝相对 command/PATH 查找，`process.allowed_command_dirs` 会要求 command 解析到允许目录内，`process.reject_root=true` 会在当前进程以 root 运行时拒绝执行。Monitor 提供 `/api/responses/function-executors` 配置摘要、validate-only、安全 overlay apply 持久化和 Audit 页面状态/enable 控件；apply 会写入应用库 `app_settings` 并热更新当前进程 runtime executor registry，后续新请求生效。API 不返回 `static_response` output 或 `external_command` command 内容，写接口和持久化 snapshot 也不接受/保存这些敏感可执行字段。
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

Responses server-mode 当前优先使用 ent-backed runtime store。SQLite raw DDL 已包含 `responses` / `response_items`、`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits` 和 `app_settings`；store 层存在 Postgres 打开路径并能创建 ent client。`ent/postgres-migrations` 已包含 Postgres application schema SQL，并已验证 up/down 可执行；Postgres `db migrate up` 已切到 checked-in SQL migrator，通过 `golang-migrate` 应用嵌入的 `ent/postgres-migrations`。命令/server 打开 application store 时已拆分 migrate 与 open：`database.auto_migrate=true` 先执行应用迁移，再以 no-auto-migrate 模式打开 store；`false` 则只打开已存在 schema。Postgres migration 现在覆盖 `internal/store` SQLite application raw DDL 表集，且 `LLM_TRACELAB_TEST_POSTGRES_DSN` 门控测试已覆盖 ent-backed Responses runtime store 在完整迁移后的 response 与 response_items 写读 round trip，以及 eval dataset/example、eval run、score 写入/finalize/查询的代表路径。`db migrate status` 和 `db migrate up/down --dry-run` 会报告应用库 migration source、namespace、versioned 状态和 auth scope 边界：Postgres 是 checked-in SQL，SQLite 仍是 startup schema fallback，auth migration 不在 `db migrate` 范围内；`db migrate status --check-db` 可显式读取 Postgres `schema_migrations` version/dirty 状态，SQLite 下只读检查 application DB，报告 `app_schema_status` marker version 和核心应用表完整性；旧 SQLite DB 没有 marker 仍按兼容 schema-init fallback 处理。`doctor` 的 `responses_server.store_health` 默认离线报告 Responses store required table set 和 migration mode，`force_store=true` 且 `database.auto_migrate=false` 且未传 `--check-db` 时 warn；显式 `--check-db` 时复用 app DB status check，并检查 `responses`、`response_items`、`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`、`app_settings`，缺表或 DB 不可达为 fail。Postgres `auth migrate up`、`auth migrate down`、`auth migrate status --check-db` 和 auth startup auto-migrate 也复用这套 checked-in Postgres SQL；auth status/dry-run 会显式报告 shared application namespace、migration source 和 scope，避免误读成独立 auth namespace 已完成。独立 auth migration namespace 仍未完成。完整 Postgres 运维生产化仍不是当前基线能力，因为 SQLite 应用迁移仍未版本化，独立 auth migration namespace 未完成，更深 analytics 查询和更广 eval/experiment 查询兼容性仍需持续审计。Stage 9 已准备 `request_audits`、`execution_events`、`upstream_exchanges` schema 骨架；Stage 10A/11A 已接入最小 request audit 写入和内部 Chat Completions upstream exchange correlation，Stage 12A 已接入 request 与内部 model_call 的最小 `execution_events` 写入，Stage 13A 已接入核心 audit 查询服务、Monitor `/api/responses/audit/trace` 和 MCP `responses_audit_trace` 工具，Stage 14A 已接入 hosted `web_search` tool_call started/completed/failed events，`tool_call_audits` 已承接 hosted web_search、configured function executor lifecycle 和强制 unsupported hosted tool rejected lifecycle，并通过 CLI/Monitor/MCP 查询，Stage 17A 已接入普通 function tool 非流式 continuation 和 requested/submitted events，Stage 17B 已接入 `capabilities.tool_calling` 路由硬约束，Stage 18A 已接入 `stream:true` deferred Responses SSE envelope 和 stream lifecycle events，Stage 18B 已接入 Chat Completions SSE 聚合和 Responses server-mode 内部上游 SSE cassette 记录，Stage 18C 已接入 context cancellation 的 `cancelled` request/model_call audit 状态、不随客户端断开的 audit 写入上下文，以及内部 Chat Completions upstream request cancel 传播，Stage 19A 已接入显式 compact API、summary output 存储、compact boundary continuation 和 `response.compact` execution events，Stage 19B 已接入 `responses_server.auto_compact` + `compact_history_item_threshold` 的 item-count 自动 compact 阈值触发和 `auto_triggered` execution event，Stage 21B 已接入 profile max output 默认值、基于 context window 的 token-budget auto compact 触发、可注入 estimator 边界、adapter-backed estimator 层、默认保守计数和 provider `/tokenize` 自动选择首切，Stage 20 已接入简单文本输出、普通 function arguments 和 provider 就绪 hosted `web_search` 的真实增量 Responses streaming 首切，auto-compact stream 会 fallback，Stage 20 stream tool lifecycle 小切片已补充 registered executor 与 hosted `web_search` 的 started 态 `response.output_item.added` SSE、成功/失败态 `response.output_item.done` SSE，并在已写出 SSE 后的 runtime 错误路径尽量追加最小 `response.failed` SSE，Stage 24A 已接入默认空 server-side function executor registry 代码扩展点，Stage 24B 已接入默认关闭的 YAML `static_response` executor 配置、timeout/max-result/redaction policy 和 proxy 装配，Stage 24C 已接入已注册 executor 的 stream tool loop 首切和 `stream=true` tool_call execution event details，Stage 24D 已接入 function executor 配置 validation/Monitor 摘要 warning 和默认关闭的 YAML `external_command` executor 首切，Stage 24E 已接入 `external_command` opt-in process working directory/absolute command/allowed_command_dirs/reject_root 轻量隔离首切，Stage 15A 已接入 upstream API surface 解析校验和 Chat Completions 路由约束，Stage 16A/16B/16C/16D/16E/16F/16G/16H/16I 已接入 Postgres ent migration SQL 生成链路、checked-in migrations、CLI versioned SQL migrator、open-vs-migrate 分离、首轮 store raw SQL 兼容审计、application raw DDL 表覆盖补齐、Postgres auth `up`/`down` 版本化迁移、应用库迁移状态报告和 opt-in database status check，Stage 22 已接入 SQLite application schema marker、只读 status 解释能力和 auth migration status reporting。更完整多工具 failed lifecycle、未来真实 MCP/file/code/computer-use 执行 lifecycle、外部 executor root/container 级沙箱、完整 context optimization 仍不是当前基线能力。

`audit query` 的 agent-friendly 诊断面已扩展：默认 trace JSON/text 输出包含顶层 diagnostics，基于已取到的 request audit、execution events、upstream exchanges 和 derived tool calls 保守汇总 event/upstream/tool-call count、latest status、cancel/failed/stream/compact signals、pending tool calls 和 compact candidate/summary。`audit query --list` 可按 conversation/client-request/status/operation 等现有 selector 返回 request audit summary 列表，`--status` 支持 `accepted`、`completed`、`failed`、`rejected`、`cancelled`，`--operation` 支持 `create`、`compact`、`input_items` 且只允许和 `--list` 搭配；两者都在 QueryService 层下推过滤，operation 只从 `request_audits.method/path` 派生，不读取 body/header raw。列表只含 id、response_id、conversation_id、client_request_id、status、operation、created_at，不输出 body/header raw。当前仍没有真实 thread/session/turn 字段和对应范围查询。

Stage 9 audit 表职责边界：

- `request_audits`：入站 Responses request envelope、client request id、redaction/body hash；`audit query` 可查询最新 trace、输出顶层 diagnostics，并可用 `--list` 返回 conversation/client-request/status/operation 范围 summary，其中 operation 是 method/path 派生字段。
- `execution_events`：runtime plan、model/tool/compact/stream/error 生命周期。当前写入 request、内部 model_call、hosted web_search、普通 function requested/submitted、registered server-side function started/completed/failed（含 stream tool loop 标记）、incremental stream fallback、deferred/incremental stream started/completed/failed、request/model_call cancellation、explicit compact 和 item-count auto compact trigger 的最小生命周期；auto compact event details 会引用 compact response metadata 中的安全 compact v2 provenance。
- `upstream_exchanges`：semantic response/request 与 `.http` cassette、trace id、route target 的关联。

后续接入顺序建议继续补 Responses audit 更完整关联入口，再补 hosted/server-side streaming lifecycle 细化和 compact events。

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
- Audit。
- Trace detail，且 trace detail API 会按 `upstream_exchanges.trace_id` 补充 Responses audit 关联 ID。

主要 API：

- `/api/overview`
- `/api/traces`
- `/api/sessions`
- `/api/models`
- `/api/channels`
- `/api/routing/summary`
- `/api/responses/function-executors`
- `/api/responses/audit/trace`
- `/api/provider-probe/report`
- `/api/provider-probe/report/apply`
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
- auto-compact 等复杂场景的真实增量 Responses server-mode streaming；内部 Chat Completions upstream cancel 传播已落地，普通 function、已注册 server-side executor 和 provider 就绪的 hosted `web_search` 已有 stream tool loop 首切。
- 外部 executor root/container 级沙箱、更完整的跨轮/混合工具 failed lifecycle events、未来真实 MCP/file/code/computer-use 执行 lifecycle 和完整 model profile/context optimization；当前已有默认关闭的 YAML `static_response` / `external_command` executor 首切、`external_command` opt-in working directory/absolute command/allowed_command_dirs/reject_root 轻量进程隔离首切、Monitor 配置摘要 API、validate-only、安全 overlay 持久化到 `app_settings`、runtime executor registry 热更新和 Audit 页面状态/enable 控件、profile max output/token-budget auto compact estimator adapter 扩展边界、provider `/tokenize` 自动选择首切、默认保守计数 fallback、普通 function call argument streaming 首切、已注册 executor 与 hosted `web_search` 的 stream tool loop 首切、同轮多个已注册 executor call 顺序覆盖、started 态 `response.output_item.added`、成功/失败态 `response.output_item.done` tool item、已写出 SSE 后的最小 `response.failed` 事件、带 `stream=true` 的 tool_call started/completed/failed events，以及 unsupported hosted tool rejected audit。
- 完整真实 stream/compact execution events 和完整 Postgres migration 生产化；当前仅覆盖最小 `request_audits` 写入、内部 Chat Completions `upstream_exchanges` correlation、request/model_call/hosted web_search started/completed/failed/cancelled、普通 function tool requested/submitted、incremental stream fallback、deferred/incremental stream started/completed、stream tool loop `stream=true` 标记等最小 `execution_events`，核心查询服务/Monitor API/MCP/UI 查询，Postgres `db migrate up`/`auth migrate up` 的 versioned SQL 应用路径，application store 的 open-vs-migrate 分离，以及 migrated logs/observation/finding/analysis/system-event 和 eval dataset/run/score 代表路径的 Postgres raw SQL 兼容；更深 analytics 查询、SQLite versioned migration 和独立 auth migration namespace 仍未完成。
- provider probe 的完整配置/Monitor 工作流；当前已有手动 `provider probe` / `doctor --probe-providers` 诊断建议、只读 `provider probe-report` / Monitor `/api/provider-probe/report` 批量报告入口、默认关闭的启动时保守补全首切、`config inspect` sources 摘要，以及 Monitor provider create preview/detail report 展示、provider setup validate/apply 首切、create dialog 状态编排首切、显式 Apply suggestions 首切、Providers 列表页 Batch probe and apply 首切和 CLI `provider probe-apply` 首切；尚未完成更完整的批量 provider onboarding、自动修复策略和跨协议消息转换。

## 推荐验证

- 文档：`git diff --check`。
- 小改动：`task check:quick`。
- 代理/路由/存储：`go test ./internal/proxy ./internal/router ./internal/store`。
- 协议/record/replay：`go test ./pkg/llm ./pkg/observe ./pkg/recordfile ./pkg/replay`。
- Monitor：`go test ./internal/monitor`，前端改动补 `task ui:build && task ui:test`。
