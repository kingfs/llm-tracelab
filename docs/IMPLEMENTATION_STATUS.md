# 当前实现状态

本文档回答“现在到底实现了什么”，是 `llm-tracelab` 现状的事实源。文中每一条都能在当前 HEAD 的代码中核对；`AGENTS.md` 是项目地图与不变量，代码与来源文档冲突时以代码为准。本文只描述当前代码事实，不承载任何计划性内容。

## 产品定位与边界

`llm-tracelab` 是本地优先的 LLM API 录制/回放代理，同时用生产可用的 Postgres 应用库存放结构化状态与观测数据。当前核心闭环已实现：

1. SDK 或 CLI 流量经 gateway 转发。
2. 代理按协议族选择上游并透传请求（协议感知，不是跨协议转换网关）。
3. 原始 HTTP 请求/响应写入 `.http` cassette。
4. 生产部署用 Postgres 索引请求元数据、路由、会话、渠道/模型配置、Responses state、audit 与派生分析结果。
5. Monitor Web 展示请求、会话、模型、渠道、路由、事件、发现和分析任务。
6. MCP 工具向 AI agent 暴露只读排障、trace 查询、失败聚类、系统事件与受控重分析。
7. `pkg/replay` 在测试中基于 cassette 回放响应，不访问上游网络。

下游入口按协议族受理：OpenAI-compatible 的 `/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`、`/v1/models`，以及 Anthropic 的 `/v1/messages`。除 `/v1/responses` 的本地 runtime 外，转发热路径不做跨协议请求转换。原始 cassette 始终是回放与详情页的事实源，应用数据库只是列表、过滤和聚合的派生索引。

详见 [./README.md](./README.md)、[./ARCHITECTURE.md](./ARCHITECTURE.md)、[./PROTOCOLS_AND_PROVIDERS.md](./PROTOCOLS_AND_PROVIDERS.md)、[./PROXY_USAGE_EXAMPLES.md](./PROXY_USAGE_EXAMPLES.md)。

## 协议覆盖

已实现的协议族（`internal/upstream/resolved.go`）：

- `openai_compatible`：Chat Completions、Responses、Embeddings、Models，以及 `/tokenize`、`/detokenize`。
- `anthropic_messages`：Claude `/v1/messages`。
- `google_genai`：Gemini `generateContent`、`streamGenerateContent`、模型列表。
- `vertex_native`：Vertex Gemini 的 `generateContent`、`streamGenerateContent` 与模型发现路径。

代理可以识别、记录并把这些协议解析为统一观测结构，但不会在转发热路径中把 Anthropic Messages 转成 OpenAI-compatible，也不会把 Responses 转成 Gemini 或 Claude。

能力声明与路由约束：upstream/channel 用 `api_type`、`mode`、`protocol_family` 和 `capabilities`（`responses`、`chat_completions`、`tool_calling`、`embeddings`、`models`、`tokenize`）描述 API surface。`api_type` 默认按协议族推断（OpenAI-compatible → `chat_completions`，Anthropic → `messages`，Google GenAI / Vertex → `gemini_generate_content`）。显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 target 不会被本地 Responses runtime 的内部 Chat Completions 调用选中；`capabilities.tool_calling: false` 的 target 在请求体包含 `tools` 时不被选中，routing decision trace 标记 `unsupported_tools`。

provider detection 属于部分实现：手动 `provider probe`、只读 `provider probe-report`、`provider probe-apply`、`doctor --probe-providers`，以及 Monitor 的 `POST /api/provider-probe`、`POST /api/provider-probe/report`、`POST /api/provider-probe/report/apply`、`POST /api/provider-setup/validate`、`POST /api/provider-setup/apply` 已接入，建议只填补缺失字段，不覆盖显式 `api_type`、`protocol_family` 或 capability false；更完整的批量 onboarding 与自动修复策略尚未实现。

协议矩阵与差异见 [./protocol-reference/README.md](./protocol-reference/README.md)。

## 录制与回放

录制写入格式是 V3（`pkg/recordfile`，前导 `# llm-tracelab/v3`），结构固定为：

1. 以 `# llm-tracelab/v3` 开头的 prelude。
2. 一行 `# meta: {...}` JSON。
3. 零到多行 `# event: {...}` JSON。
4. 一个空行。
5. 原始 HTTP 请求字节。
6. 一个分隔换行。
7. 原始 HTTP 响应字节。

读取端必须继续兼容旧 `LLM_PROXY_V2` 的固定 2KB JSON header block；写入端只产出 V3。`.http` cassette 是 replay 和详情页的事实源，保持人类可读，并优先做增量演进而不是破坏性迁移。

本地 Responses runtime 调用内部上游 `/v1/chat/completions` 时，该上游 HTTP exchange 也按同一 recorder 写入 `.http` cassette；Responses semantic state 不替代 raw cassette。回放由 `pkg/replay` 提供硬性保证，测试回放不访问上游网络。相关背景见 [./ARCHITECTURE.md](./ARCHITECTURE.md)。

## 存储与派生数据

生产环境的结构化状态主路径是 Postgres：checked-in SQL 位于 `ent/postgres-migrations/`，由 `internal/appdbmigrate` 和 `golang-migrate` 应用，入口是 `db migrate up`。命令与 server 打开应用库时已拆分 migrate 与 open：`database.auto_migrate=true` 先执行应用迁移再以 no-auto-migrate 模式打开 store，`false` 只打开已存在的 schema。

SQLite 仅作为本地开发、离线测试和既有本地 DB 兼容的 fallback（`{{output_dir}}/llm_tracelab.sqlite3`），其 schema 由启动时的 raw DDL 应用，不走 versioned migration；它是 `startup_schema_fallback`，不是生产边界。

当前应用库中的主要表：

- trace 与观测：`logs`、`trace_observations`、`semantic_nodes`、`trace_findings`、`analysis_jobs`、`analysis_runs`、`parse_jobs`、`parser_versions`、`system_events`。
- Responses 与审计：`responses`、`response_items`、`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`。
- 渠道与模型：`channel_configs`、`channel_models`、`channel_probe_runs`、`model_catalog`、`model_aliases`、`upstream_targets`、`upstream_models`。
- eval 与实验：`datasets`、`dataset_examples`、`eval_runs`、`scores`、`experiment_runs`。
- 其他：`app_settings`、`session_summaries`、`overview_metric_buckets`、`overview_metric_bucket_members`、`users`、`api_tokens`。

`config inspect`、`db migrate status` 和 `doctor` 会输出 `production_storage_driver=postgres`、`production_ready`、`storage_role`、`storage_contract`。`db migrate status` 与 `db migrate up/down --dry-run` 报告迁移来源：Postgres 为 checked-in SQL，SQLite 为 `internal/store` 启动 DDL fallback；`db migrate status --check-db` 对 SQLite 只读解释 `app_schema_status` marker 与 required table 状态，不创建缺失文件、不做 destructive repair。Postgres 真实检查与代表 runtime SQL 路径由 `LLM_TRACELAB_TEST_POSTGRES_DSN` 门控，默认测试离线。

Postgres 的 auth 表由 application migration set 拥有：`auth migrate up` 复用同一套 checked-in SQL，`auth migrate down` 已被阻止，status/dry-run 报告 `effective_database_namespace=application`、`schema_authority=application_postgres_migration_set`、`storage_contract=postgres_application_schema_owns_auth_tables`、`postgres_auth_namespace_strategy=shared_application_schema_migrations`、`independent_auth_namespace_status=not_implemented`。

存储与部署细节见 [./STORAGE_AND_DEPLOYMENT.md](./STORAGE_AND_DEPLOYMENT.md)、[./POSTGRES_OPERATIONS.md](./POSTGRES_OPERATIONS.md)。

## 本地 Responses runtime

本地 Responses execution mode 始终可用，没有配置开关。历史上用于控制它的 `responses_server.enabled` 字段和 `LLM_TRACELAB_RESPONSES_ENABLED` 环境变量已被删除；`ResponsesServerConfig` 中不存在 `enabled` 字段。本地 runtime 采用惰性构建：`tools.web_search`、function executor、model profile 等可选配置出错不会阻塞服务启动，而是在首个真正需要它的本地 Responses 请求上以 502 报错。

`/v1/responses` 在选路时按模型在上游能力之间二选一：命中 native Responses upstream 时直接代理透传（native Responses target 原样转发并录制为 `/v1/responses` cassette）；否则由本地 runtime 接管，把请求编排为内部上游 `/v1/chat/completions` 调用。选路策略来自应用库 `app_settings` 键 `routing.settings`（不是 YAML 键），由 `PATCH /api/settings/routing` / `GET /api/settings/routing` 管理，`responses_strategy` 有五个取值：

- `auto`（默认）
- `prefer_native`
- `prefer_local_server`
- `native_only`（不做本地翻译）
- `local_server_only`

native/local 判定按模型而非按渠道：显式的 `channel_models.supports_responses` / `supports_chat_completions`（Monitor 模型详情页可编辑）优先于渠道级 `api_type` / `capabilities`，未声明的模型沿用渠道级行为；YAML 部署可用 upstream 的 `model_capabilities` 表达同一覆盖。Monitor 的 routing inspect 模拟器使用同一份按模型能力数据。同一渠道因此可以让部分模型走 native 直通、其余走本地 runtime。

已实现的下游与 runtime 能力：

- 入站 `request_audits`、内部 Chat Completions 的 `upstream_exchanges` correlation，以及 request、model_call、tool_call、compact、stream、cancel 等最小 `execution_events`。
- Responses semantic state 存入 runtime store（优先 ent-backed store，表为 `responses` 和 `response_items`）。
- `POST /v1/responses/compact`：读取目标 response 的 continuation history，生成 `summary` output 并存入 runtime store；后续 `previous_response_id` 指向 compact response 时 history 停在 compact boundary，`summary` 作为 system message 注入下一轮。
- `responses_server.auto_compact=true` 时，continuation 在 history item 数超过阈值或估算 prompt + reserved output 超过 `context_window_tokens` 时先自动 compact。
- compact provenance 写入 compact response 的 `metadata._gateway.compact`（只含 id/type/status 等安全字段与计数、budget/trigger、auto/manual 标记），`ListCompactProvenance` 可从 execution events 派生安全 read model。
- hosted `web_search` / `web_search_preview`：`tools.web_search.enabled=true` 时可选 `mock` 或 `searxng` provider；未配置时该能力默认关闭（provider 默认 `disabled`），仓库跟踪的 `config/config.yaml` 启用了 SearXNG。未启用或未实现的 `file_search`、`code_interpreter`、`computer_use_preview` 在强制 `tool_choice` 下返回稳定 `unsupported_tool`，并写 `tool_call_audits` rejected 记录（不含 raw descriptor/payload）。
- server-side function executor registry 默认为空；`responses_server.function_executors.enabled=true` 且声明 `static_response` 或 `external_command` 后才自动执行同名 function tool。`external_command` 默认不继承环境变量、不使用 shell，通过 stdin JSON 接收 call，stdout 作为输出，stderr 只进入截断后的失败摘要。
- MCP hosted tool executor 已实现：`tools.mcp.enabled=true` 且配置 server 后，runtime 可执行 hosted MCP 工具。
- 普通 `function` 默认 client-owned：未注册同名 executor 的 function call 只返回给客户端，由客户端通过 `function_call_output` + `previous_response_id` 继续。

本地 Responses 增量 streaming 属于部分实现：简单文本、普通 function arguments、已注册 server-side executor、以及 provider 就绪且 `tool_choice` 为 nil/空/`none`/`auto` 的 hosted `web_search` 路径支持真实增量输出（`response.output_text.delta`、`response.function_call_arguments.delta/done`、started/completed/failed 的 `response.output_item.added/done`）。未知或未实现的 hosted 工具、非平凡 `tool_choice` 以及不支持的混合工具组合在写出 SSE 或调用上游前返回 `ErrIncrementalStreamUnsupported`，HTTP handler 记录 fallback event 后转入 deferred envelope。

runtime 安全与策略：executor 支持工具级 timeout、最大结果字节数和 audit arguments/output redaction；`external_command` 有 opt-in 的轻量进程隔离（`process.working_dir`、`process.require_absolute_command`、`process.allowed_command_dirs`、`process.reject_root`）。Monitor 提供 `/api/responses/function-executors` 配置摘要、validate-only、安全 overlay apply 持久化到 `app_settings`，并热更新当前进程的 executor registry；API 不返回 `static_response` output 或 `external_command` command 内容。

Codex 兼容性：`tests/fixtures/codex/` 存放离线 fixture，`task test:codex-fixtures` 运行 `internal/responses/httpapi` 与 `internal/responses/runtime` 的离线 gate，不依赖真实 Codex、真实模型、网络或 Postgres。`models codex-config <model>` 离线输出 Codex TOML 建议和 JSON diagnostics，可读取 `responses_server.model_profiles`，或在 `responses_server.adopt_channel_model_profiles=true` 时采用 `profile_adoption_status=adopted` 的 channel model profile；显式 profile 仍最高优先，冲突时保守跳过。

详见 [./RESPONSES_RUNTIME.md](./RESPONSES_RUNTIME.md)。

## 渠道与模型管理

YAML 的 `upstream` / `upstreams` 只是首次 bootstrap 输入。第一次写库会写入应用库 `app_settings` 键 `channels.initialized`；此后数据库拥有路由配置，即使渠道被全部禁用或删除也仍然如此。`GET /api/settings/channels` 返回该 marker（`{"initialized": bool}`），`DELETE /api/settings/channels` 清除 marker 以重新打开 YAML bootstrap，但在数据库仍存有渠道时数据库继续优先。

YAML 配置包含显式 `credentials` 列表时，渠道保持 YAML 管理：Monitor 的渠道、模型和 alias 写操作返回 409（`upstreams are managed by YAML credentials; edit YAML and restart instead`）。

所有管理写操作（渠道、模型、alias、provider setup 与 probe apply）都在一个 `store.ConfigurationTransaction` 内执行：持有进程级配置锁、upstream 写锁和单个 SQL 事务，runtime 路由只在提交成功后才发布；后台 upstream refresh 通过同一 upstream 写锁做 best-effort 持久化。

已实现的配置能力：

- 渠道的创建/更新、启停，模型的探测、启停，本地加密存储 API key 和敏感 header。
- `POST /api/provider-setup/validate` 组合 base URL、API key、provider preset、model discovery 和 capability 字段，返回 provider probe、归一化配置和脱敏 secret state，不落库、不回显 API key；create dialog 采用 validate → review → create 流程，字段变更清空旧验证结果；只有 probe 检测成功，或用户显式提供 `api_type` 与 `protocol_family` 时才写入 channel store。
- `POST /api/provider-probe/report` 与 `POST /api/provider-probe/report/apply` 提供只读批量 report 与显式批量写入；不接收或返回 API key，只填补缺失的 `api_type`、`protocol_family` 和未声明 capability。Monitor 写入成功后会 reload router。
- `provider probe-apply` 复用同一 channel service 用例。
- `config inspect` 输出脱敏 effective config，并用 `sources` 摘要标注字段来自 config file、default、effective、empty、derived 或 not_configured。
- `provider_probe.startup_fill=true` 时 serve 启动只在内存中填补 YAML upstream 缺失的 `api_type`、`protocol_family` 和未声明 capability，不写回配置、不覆盖显式配置。

路由与凭据细节见 [./ROUTING_AND_CREDENTIALS.md](./ROUTING_AND_CREDENTIALS.md)。

## Monitor

Monitor 是 Go embed 的 React/Vite 前端，当前页面/视角包括：Overview、Events、Sessions、Traces（旧的 `/requests` 入口仍可用）、Audit、Models、Providers（旧的 `/channels` 入口重定向到 `/providers`）、Connect、Routing、Analysis、Tokens，以及 trace／session／provider／model 详情页。Trace detail 的 Reading guide 在 payload 或 `upstream_exchanges.trace_id` 能关联到 `response_id` / `request_audit_id` 时，提供 Responses audit 跳转入口。

主要 HTTP API：`/api/overview`、`/api/traces`、`/api/sessions`、`/api/models`、`/api/channels`、`/api/routing/summary`、`/api/routing/inspect`、`/api/routing/exchanges`、`/api/responses/function-executors`、`/api/responses/audit/trace`、`/api/responses/audit/tool-calls`、`/api/provider-probe/report`、`/api/provider-probe/report/apply`、`/api/provider-setup/*`、`/api/settings/routing`、`/api/settings/channels`、`/api/model-aliases`、`/api/events`、`/api/findings`、`/api/analysis`、`/api/upstreams`、`/api/auth/*`。Monitor 的列表与统计来自应用数据库。

使用说明见 [./MONITOR_GUIDE.md](./MONITOR_GUIDE.md)。

## MCP

MCP 通过 management server 的 streamable HTTP 暴露，定位是只读排障与分析辅助，工具复用 Monitor/store 查询，不另起事实源，也不替代 replay、Monitor 或应用数据库。当前工具共 21 个：

- trace/session/upstream：`list_traces`、`get_trace`、`list_sessions`、`list_upstreams`。
- 路由与失败：`query_routing_decisions`、`query_sticky_routing`、`query_failures`、`summarize_failure_clusters`。
- findings：`list_trace_findings`、`query_dangerous_tool_calls`、`query_sensitive_data_findings`。
- 系统事件：`list_system_events`、`get_system_event`、`summarize_system_events`、`query_unread_system_events`。
- Responses 审计：`responses_audit_trace`、`responses_audit_tool_calls`。
- 重分析：`reanalyze_trace`、`reanalyze_session`、`list_analysis_jobs`、`get_analysis_job`。

工具用法见 [./MCP_GUIDE.md](./MCP_GUIDE.md)。

## 语义解析、审计与重分析

语义解析与派生分析层已实现：

- `pkg/observe` 的 parser registry，含 OpenAI、Anthropic、Gemini/Vertex parser；`trace_observations` 持久化并可在 trace detail 展示；parser 失败写入 system events。
- deterministic audit detectors：危险 shell/命令、凭据与敏感信息、provider safety signal、tool error；结果写入 `trace_findings`，可通过 Monitor 和 MCP 查询。
- 重分析 job 类型：`trace_reparse`、`trace_rescan`、`trace_repair_usage`、`trace_reanalyze`、`session_reanalyze`、`batch_reanalyze`，状态持久化在 `analysis_jobs`。
- parser/analyzer/router/upstream 事件写入 system events。
- session 聚合已实现，提取顺序为 `Session_id`、`X-Claude-Code-Session-Id`、`X-Codex-Turn-Metadata.session_id`、`X-Codex-Window-Id` 的 `:` 前缀、空 session；Monitor 与 MCP 都可查询 session 列表和详情。

Responses audit 属于部分实现，职责边界如下：

- `request_audits`：入站 Responses request envelope、client request id、headers allowlist、body hash/preview 与完成状态；目前只在本地 Responses runtime 写入。
- `execution_events`：request、内部 model_call、hosted `web_search`、普通 function requested/submitted、registered server-side function started/completed/failed、incremental stream fallback、deferred/incremental stream started/completed、request/model_call cancellation、explicit compact 与 auto compact trigger 的最小生命周期。
- `upstream_exchanges`：关联 semantic response/request 与 `.http` cassette、trace id、route target；目前只覆盖本地 runtime 的内部 Chat Completions 调用，并在 response 完成后回填 semantic `response_id`，`trace_id` 使用 recorder prelude 的 `meta.request_id`。
- `tool_call_audits`：承接 hosted `web_search`、configured function executor 生命周期与强制 unsupported hosted tool 的 rejected 记录。

`audit query` 默认在 JSON/text trace 中附带顶层 diagnostics，基于 request audit、execution events、upstream exchanges 和 derived tool calls 保守派生出 event/upstream/tool-call 计数、latest status、cancel/failed/stream/compact 信号、pending tool calls 与 compact candidate/summary，不输出 raw arguments/query/output/error。`audit query --list` 可按 conversation/client request/status/operation selector 返回 request audit summary 列表；`--status` 支持 `accepted`、`completed`、`failed`、`rejected`、`cancelled`，`--operation` 支持 `create`、`compact`、`input_items` 且只能与 `--list` 搭配，operation 由 `request_audits.method/path` 派生，不读取 body/header。schema 目前没有真实 thread/session/turn 字段，因此不支持这些范围查询。

详见 [./OBSERVATION_AND_AUDIT.md](./OBSERVATION_AND_AUDIT.md) 与 [./DEVELOPMENT.md](./DEVELOPMENT.md)。

## 非目标与未实现

- 跨协议请求转换网关：转发热路径不做 OpenAI、Anthropic、Gemini、Vertex 之间的请求互转，唯一例外是 `/v1/responses` 本地 runtime 把 Responses 编排为内部 Chat Completions。
- 公网多租户 API 分发平台，以及计费、充值、订阅销售。
- 对上游 Responses provider 的 native semantic interposition：native Responses 只做透传，不做语义改写。
- 用结构化数据库替代 raw cassette 作为 replay/详情事实源；也不让 replay 依赖网络访问，更不让测试依赖真实 provider。
- 完整的 model profile / context optimization，以及复杂组合（未知或未实现 hosted 工具、非平凡 `tool_choice`）在 auto compact 后的真实增量本地 Responses streaming。
- `external_command` executor 的 root/container 级沙箱：当前只有 opt-in 的 working directory、绝对 command、allowed_command_dirs、reject_root 轻量进程隔离。
- 真实 `file_search` / `code_interpreter` / `computer_use_preview` hosted 工具的执行 lifecycle（MCP hosted tool executor 已实现，不在未实现之列）。
- 独立的 Postgres auth migration namespace：auth 表当前由 application migration set 拥有，`auth migrate down` 被阻止。
- SQLite 到 Postgres 的自动数据迁移；SQLite 只保留为 startup-schema fallback。
- 更完整的批量 provider onboarding 与自动修复策略。
- 更深层的 analytics 查询，以及独立 auth namespace 之外的额外迁移能力。
