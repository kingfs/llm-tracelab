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
- 差异：llm-tracelab 对 auto compact 等复杂路径仍 fallback 到 deferred SSE envelope；responses-gateway 文档强调 typed event lifecycle，但也以 Codex fixture 驱动分阶段落地。

### Postgres 状态存储

- 已吸收：Responses semantic state 使用 ent-backed store，Postgres checked-in migration 可通过 CLI 应用。
- llm-tracelab 落点：`internal/responses/runtime/ent_store.go`、`internal/store`、`internal/appdbmigrate`、`ent/postgres-migrations`、`cmd/server/db.go`。
- responses-gateway 对照：`internal/orchestrator/ent_store.go`、`ent/schema`、`cmd/responses-gateway/migrate.go`。
- 差异：llm-tracelab 同时维护 SQLite startup schema fallback 和 Postgres versioned SQL；responses-gateway 的目标更集中在 Postgres/memory store。

### Request audit 基础链路

- 已吸收：入站 Responses request audit、内部 Chat Completions upstream exchange correlation、execution events、Monitor/MCP 查询入口。
- llm-tracelab 落点：`internal/responses/audit`、`internal/mcpserver/responses_audit.go`、`internal/monitor` 的 `/api/responses/audit/trace`。
- responses-gateway 对照：`internal/interfaces/http/request_audit.go`、`docs/request-audit-cli.md`、`cmd/responses-gateway/audit.go`。
- 差异：llm-tracelab 当前主要按 `response_id` / `request_audit_id` 查询 trace；responses-gateway 的 CLI 已按 thread/session/turn/client request id、compact candidate、status、operation 等维度组织诊断。

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
- 当前覆盖：`config.load`、`server.port`、monitor/MCP consistency、application database migration mode/report、Responses server backend validation、`responses_server.default_model`、Responses store driver/auto-migrate readiness、model profile context-window/compact threshold numeric relationships、web_search provider config validation、provider config basic count、auth migration scope note。
- 安全边界：默认离线，不做真实模型推理或 provider 网络请求；`--check-db` 才读取数据库 migration status；输出复用 DSN/URL 脱敏摘要，不输出 API key/header secret。
- llm-tracelab 落点：`cmd/server/doctor.go`，复用 `appDBMigrationReport`、`router.ValidateLocalResponsesServerBackendConfig`、`websearch.NewProvider` 和 `config inspect` 的脱敏摘要。
- 剩余缺口：`--probe-providers` 仍是离线占位提示，尚未接入受控的 provider probe；store backend 深度健康检查仍需 `--check-db` 或后续更细检查；Codex profile 建议、HTTP guard 深度诊断和 model profile drift 与 catalog/channel 的交叉校验仍待补齐。
### Codex compatibility profile

- 已吸收首切：新增独立 [Codex Responses 兼容性首切](./CODEX_RESPONSES_COMPATIBILITY.md)，固化 text create、stream text、function call、`function_call_output` continuation、ordinary `web_search` descriptor、unsupported hosted tool 的最小兼容合约。
- fixture 资产：新增 `tests/fixtures/codex/` 离线 examples，覆盖请求、期望 response/event/error 形状；不依赖真实 Codex、真实模型或网络。
- llm-tracelab 落点：`docs/CODEX_RESPONSES_COMPATIBILITY.md`、`tests/fixtures/codex/*`，运行时事实仍以 `internal/responses/httpapi`、`internal/responses/runtime`、`internal/proxy/responses_server.go` 为准。
- 剩余缺口：fixture 尚未接自动 Go/e2e runner；Codex TOML/profile 生成已接首切但尚未联动 catalog/channel drift；unsupported hosted tools 还没有 runtime-level stable `unsupported_tool` gate；Codex-specific audit diagnostics 仍只覆盖 response/request 查询首切。

## 部分吸收能力

### Request audit 诊断深度

- 已有：`request_audits`、`execution_events`、`upstream_exchanges`，以及 Monitor/MCP trace 查询；`audit query` CLI 已提供 response/request 维度只读 trace 查询首切。
- 缺口：缺少 thread/session/turn/client-request-id 范围查询、compact candidate summary、pending function call diagnostics、stream/cancel/tool/request feature 顶层诊断。
- llm-tracelab 下一步落点：扩展 `cmd/server/audit.go`，复用并扩展 `internal/responses/audit.QueryService`。

### Compact 与 context optimization

- 已有：显式 compact API、item-count auto compact、基于 context window 的 token-budget auto compact、`/tokenize` counter provider 自动选择首切。
- 缺口：auto compact stream 仍 fallback；summary provenance 和 retained item 窗口信息不如 responses-gateway compact v2 设计完整；完整 context optimization 未接入。
- llm-tracelab 落点：`internal/responses/runtime/runtime.go`、`estimator.go`、`profile.go`、`internal/proxy/responses_tokenize_counter.go`。
- responses-gateway 对照：`docs/functional-design.md` 的 Compact 章节。

### Hosted/server-side tool lifecycle

- 已有：hosted `web_search` 与 registered executor 的 started/completed/failed execution events 和 stream item 首切。
- 缺口：没有独立 `tool_call_audits` 表；跨轮/混合工具失败 lifecycle 仍不完整；MCP、file search、code interpreter、computer-use 仍未实现或未形成 runtime contract。
- llm-tracelab 下一步落点：`ent/schema`、`internal/responses/audit`、`internal/responses/runtime`、`internal/responses/functionexec`。
- responses-gateway 对照：`docs/tool-runtime.md` 的 `tool_call_audits` 和 MCP runtime boundary。

### Migration 运维

- 已有：Postgres application `db migrate up/status/down --dry-run`、auth migrate、SQLite status 解释、open-vs-migrate 分离。
- 缺口：SQLite 仍是 startup schema fallback，不是 versioned migration；auth 仍共享 application schema namespace，独立 auth migration namespace 未完成。
- llm-tracelab 落点：`cmd/server/db.go`、`cmd/server/auth.go`、`internal/appdbmigrate`、`internal/auth/migrate.go`。
- responses-gateway 对照：`cmd/responses-gateway/migrate.go`、`ent/migrations`。

### Machine-readable CLI schema

- 已有：`schema` 命令输出 CLI machine contract。
- 缺口：schema 不是配置 inspect；它不能展示合成后的有效配置、脱敏 DSN/API key、Responses server/tool/provider 生效状态。
- llm-tracelab 落点：`cmd/server/schema.go`。
- responses-gateway 对照：`cmd/responses-gateway/config_inspect.go`。

## 仍缺能力

### `doctor` 深度诊断

- 已吸收首切：`llm-tracelab doctor` 覆盖离线启动前诊断、稳定 JSON envelope、`--check-db`、`--fail-on-warn`、`--fail-on-fail`、Responses default model、store driver/auto-migrate readiness、model profile context-window/compact threshold numeric relationships。
- 剩余缺口：provider 网络 probe 尚未真正执行；尚未检查 HTTP guard、默认模型是否存在于 catalog/channel、store backend 深度健康、Codex profile 建议与 compact threshold drift。
- responses-gateway 能力：读取同一套配置，输出稳定 JSON，检查 config、HTTP guard、默认模型、vLLM `/models`、model profile context window drift、store backend、web_search 配置。
- llm-tracelab 后续落点：继续扩展 `cmd/server/doctor.go`，受控复用 `internal/providerprobe` 和更细的 responses/profile/store 诊断，但保持默认离线。

### `config inspect` 有效配置视图

- 已新增首切：`llm-tracelab config inspect --format json` 读取同一套配置加载链路，输出稳定 envelope 与脱敏后的 effective 摘要。
- 当前覆盖：server/monitor/MCP、database driver/DSN/auto_migrate、trace output dir、Responses server、web_search、provider_probe、upstream targets 与 credential/static model 计数。
- 剩余缺口：尚未标注每个字段的来源优先级（config 文件、env、CLI flag），也未做 doctor 式联动校验。
- responses-gateway 能力：明确默认值、config 文件、env、CLI flag 优先级，并支持脱敏输出。
- llm-tracelab 建议落点：`cmd/server/config_inspect.go` 或 `cmd/server/config.go`，复用 `internal/config` 的 default/effective helper 和 `config.RedactDSN`。

### `audit query` CLI

- 已吸收首切：`llm-tracelab audit query`（别名 `audit responses`）提供面向 agent 的只读 Responses audit trace 查询入口，复用当前 config 的 application store 和 `internal/responses/audit.QueryService`。
- 当前用法：`llm-tracelab -c config.yaml --format json audit query --response-id resp_x --include-events --include-exchanges --limit 100`，也支持 `--request-audit-id`；默认只输出 request audit envelope，events/exchanges 需显式打开。
- 安全边界：CLI 输出 request audit 的已存 `body_preview` / hash / redaction metadata，不读取或输出未脱敏 raw request body。
- responses-gateway 能力：按 response/request/thread/session/turn/client request id 查询，并输出 diagnostics envelope。
- 剩余缺口：尚未支持 `--client-request-id`、`--conversation-id`、thread/session/turn 范围查询、compact candidate 和 Codex-specific diagnostics。

### Codex profile 生成命令

- 已吸收首切：`llm-tracelab models codex-config <model>` 离线读取 `responses_server.model_profiles`，输出 `models.codex_config` JSON envelope、Codex TOML 建议、provider `base_url` 推导、`wire_api=responses`、profile match/compact threshold diagnostics 和 warnings。
- 安全边界：不连 DB、不探上游网络、不运行真实 Codex、不输出真实 API key/header secret/DSN；无 profile 时不失败，输出 0 值并 warning。
- 剩余缺口：尚未从数据库 catalog/channel model profile 合并能力，也未检查 Codex 本地配置 drift。
- responses-gateway 能力：输出 `model_context_window`、`model_auto_compact_token_limit`、provider `base_url`、`wire_api=responses` 等稳定 JSON/TOML。
- llm-tracelab 建议落点：`cmd/server/models.go` 或 `cmd/server/provider.go` 子命令；数据来源应优先是 `responses_server.model_profiles` 和 channel/model catalog。

### Codex fixture/runbook 资产

- 已吸收首切：已有集中 `tests/fixtures/codex` 离线 fixture、`docs/CODEX_RESPONSES_COMPATIBILITY.md` profile 文档，以及 `internal/responses/httpapi` / `internal/responses/runtime` focused offline Go tests。
- 剩余缺口：当前只是 focused fixture/contract tests，不是完整 Codex/e2e runner；没有长任务 compact/cancel/run-report 脚本资产；Codex TOML/profile 生成只有离线首切。
- responses-gateway 能力：`docs/codex-longrun-compact-runbook.md`、`scripts/codex-longrun-*.sh`、Codex fixture profile。
- llm-tracelab 建议落点：在现有离线 fixtures 基础上决定是否引入脚本；不要让测试依赖真实 Codex 或网络。

### MCP hosted tool runtime

- 缺口：MCP 当前是 llm-tracelab 对外排障 server，不是 Responses runtime 内部的 MCP client/tool executor。
- responses-gateway 设计：`type:"mcp"` descriptor 作为 gateway-hosted runtime 请求，当前先明确拒绝并审计。
- llm-tracelab 建议落点：`internal/responses/runtime` 的 hosted tool gate、`internal/responses/audit` 的 tool diagnostics；第一步只做 recognized-and-rejected，不接执行器。

## 建议下一阶段优先级

1. 扩展 `doctor` 深度诊断与 `config inspect` 字段来源。
   - 价值：降低 server-mode/Postgres/web_search/Codex 接入排障成本。
   - 模块：`cmd/server/doctor.go`、`cmd/server/config.go`、`internal/config`、`internal/providerprobe`。
   - 验收：在首切稳定 JSON envelope 基础上补 provider probe、profile/store/default-model 深度诊断；继续默认脱敏、无真实模型推理。

2. 扩展 `audit query` CLI，并复用现有 QueryService。
   - 价值：把 Monitor/MCP 才能看的 Responses audit 变成 agent 可脚本化入口。
   - 模块：`cmd/server/audit.go`、`internal/responses/audit/query.go`。
   - 验收：在首切 response/request 查询基础上补 client-request/conversation 查询；输出 request audit、events、upstream exchanges；不输出未脱敏 raw body。

3. 接入 Codex fixture runner 和 profile 生成。
   - 价值：把已固化的最小兼容合约变成可回归检查，并减少 Codex 本地配置漂移。
   - 模块：`tests/fixtures/codex`、`internal/responses/httpapi`、`runtime` 测试、`cmd/server/provider.go` 或新增 `cmd/server/models.go`。
   - 验收：在已接 focused offline tests 基础上，补更完整 runner 或 e2e harness；输出 JSON + TOML profile 片段。

4. 收敛 hosted tool audit schema。
   - 价值：为 web_search、external_command、未来 MCP/file/code 工具提供统一排障面。
   - 模块：`ent/schema`、`internal/responses/audit`、`internal/responses/runtime`、`internal/responses/functionexec`。
   - 验收：不要手改 `ent/dao/**`；先定义 `tool_call_audits` 或等价表，再接 web_search/executor started/finished。

5. 补 model/Codex profile inspect。
   - 价值：让 Codex 本地配置与 `responses_server.model_profiles`、channel model catalog 保持一致。
   - 模块：`cmd/server/provider.go` 或新增 `cmd/server/models.go`、`internal/config`、`internal/store`。
   - 验收：输出 JSON + TOML 片段；明确 compact threshold 来源和 context window margin。

6. 继续 migration 生产化。
   - 价值：减少 Postgres/SQLite/auth schema 运维歧义。
   - 模块：`internal/appdbmigrate`、`internal/auth/migrate.go`、`cmd/server/db.go`、`cmd/server/auth.go`。
   - 验收：SQLite versioned migration 方案或明确继续 fallback；auth 独立 namespace 拆分方案落文档和 dry-run 状态。

## 不建议直接搬运的点

- 不要把 llm-tracelab 改成纯 semantic gateway；record/replay proxy 和 `.http` cassette 仍是项目主线。
- 不要把 `responses-gateway` 的 vLLM-only 假设写死到 llm-tracelab；现有 router/upstream capability 约束应继续保留。
- 不要绕过现有 Monitor/MCP/store 事实源另建一套 audit 数据。
- 不要为了补工具 runtime 直接执行任意 MCP/code/computer-use；先 recognized-and-rejected、审计和稳定错误码。
