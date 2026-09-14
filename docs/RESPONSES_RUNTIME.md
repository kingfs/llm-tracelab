# 本地 Responses Runtime

本文只记录当前代码事实：`/v1/responses` 落在 TraceLab 本地 Responses runtime 时的选路、请求处理、Codex 兼容、hosted tools、compact 与配置面。仓库级不变量见 `./ARCHITECTURE.md`，路由与凭据模型见 `./ROUTING_AND_CREDENTIALS.md`，审计查询面见 `./OBSERVATION_AND_AUDIT.md`，协议矩阵见 `./protocol-reference/README.md`。字段与行为以 `internal/responses/**`、`internal/proxy/**`、`internal/config/config.go`、`internal/routeplan` 为准。

## 定位与开关

- `/v1/chat/completions`、`/v1/responses`、`/v1/messages` 三个下游入口被无条件受理。`/v1/responses` 在每次请求的选路阶段按模型二选一：命中 native Responses upstream 时走普通代理直通并录制；否则由本地 Responses runtime 接管，把请求编排为一次或多次内部上游 `POST /v1/chat/completions` 调用。
- 本地执行模式**没有开关**。`responses_server.enabled` 字段与 `LLM_TRACELAB_RESPONSES_ENABLED` 环境变量已被彻底删除，`internal/config` 的 `ResponsesServerConfig` 不含 `enabled` 字段；YAML 里写该键不会生效（仅触发未知键告警）。本地 runtime 始终参与 `/v1/responses` 选路。
- 要禁用本地翻译，把应用数据库 `app_settings` 键 `routing.settings` 设为 `{"responses_strategy":"native_only"}`（Monitor 的 Routing 设置，`PATCH /api/settings/routing`）。正确拼写是 **`responses_strategy`**；它是 app_settings 键，**不是** `config.yaml` 键，`config.yaml` 中没有 `routing:` 配置段。
- 本地 runtime 惰性构建：`tools.web_search`、function executor、model profile、tokenize counter 等可选配置出错不会阻塞服务启动，而是在首个真正需要它的本地 Responses 请求上返回 502。构建失败不被缓存，瞬时原因可在后续请求恢复。
- 非 Responses 路径不受影响，继续走既有 protocol-aware 代理、录制与 replay 热路径。`/v1/responses` 命中 native upstream 的直通同样只是普通代理，不做 semantic interposition。

职责切分（本地 runtime 只占其中一块）：

| 上下文 | 职责 | 非职责 |
| --- | --- | --- |
| Gateway Routing | provider/model 选择、endpoint capability 判断、URL 构造、fallback、route decision | 维护 Responses conversation 状态或 tool loop |
| Proxy Recording | 外部 HTTP exchange 拦截、`.http` V3 写入、V2/V3 读取兼容、usage/timeline 派生 | 维护 `previous_response_id` 语义 |
| Responses Runtime | `/v1/responses` semantic server、conversation item、tool orchestration、stream event 归一化、compact | 充当所有协议的通用转换器 |
| Persistence & Audit | semantic state、execution events、request audit、route/upstream correlation、查询索引 | 替代 raw cassette 的 replay 事实源 |

## 路由决策

代码位置：选路策略与约束在 `internal/routeplan`，请求侧决策在 `internal/proxy/handler.go` 的 `responsesRoutingDecision` / `responsesStrategy`，本地 runtime 在 `internal/responses/runtime`，HTTP 面在 `internal/responses/httpapi`，上游调用与录制在 `internal/proxy/responses_server.go`。

`responses_strategy` 由 `internal/routeplan` 定义并校验，合法值五档：

| 策略 | 行为 |
| --- | --- |
| `auto` | 有可用 native Responses upstream 时直通；否则回退本地 runtime。 |
| `prefer_native` | 当前实现与 `auto` 行为相同（native 优先，再回退本地）。 |
| `prefer_local_server` | 有可用 Chat Completions backend 时用本地 runtime；否则回退 native 直通。 |
| `native_only` | 只用 native Responses upstream；没有匹配目标时拒绝本地翻译。 |
| `local_server_only` | 只用本地 runtime；没有 Chat Completions backend 时拒绝 native 直通。 |

- 策略来源是应用库 `routing.settings`；代理侧读不到或值不在合法集合内时回落为 `auto`，Monitor 的 `PATCH /api/settings/routing` 会直接拒绝非法值（`unsupported responses_strategy %q`）。
- native-vs-local **按模型**解析，不按渠道：显式 `channel_models.supports_responses` / `supports_chat_completions`（Monitor 模型详情页可编辑，YAML 等价项 `upstream.model_capabilities`）优先于渠道级 `api_type` / `capabilities`；未声明的模型回退到渠道级行为。
- 渠道 `mode`（`proxy` / `record_only` / `server` / `responses_server`）只被解析、校验和记录日志，**不参与路由**。
- 本地 runtime 通过内部 `/v1/chat/completions` 调用上游，因此 route target 必须是 OpenAI-compatible Chat Completions backend。显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 target 可以服务 native 直通，但不会被本地 runtime 选中。
- 请求体带 `tools` 时，显式 `capabilities.tool_calling: false` 的 target 不会被选中，候选会标记 `requires_tool_calling`。

## 请求处理流程

HTTP 面（`internal/responses/httpapi/handler.go`）：

- `POST /v1/responses`：create；非 POST 返回 405。
- `POST /v1/responses/compact`：显式 compact。
- `GET /v1/responses/{id}/input_items`：读取已存 response 的 input items。
- 请求体上限由 `responses_server.max_request_body_bytes` 控制，先做 JSON 单对象校验再进入 runtime。

错误形状：`ResponseNotFoundError` → 404；`UnsupportedHostedToolError` → 400，`code` 为 `unsupported_tool`；`context.Canceled` → 499，`code` 为 `cancelled`；其余 → 500 `server_error`。

create 流程：

1. 鉴权与 body limit 之后，写最小 `request_audits` 入站记录。
2. 选路（见上一节）。命中本地时由 runtime 读取 `previous_response_id`、历史 items 与本次 `input`，构造当前 turn 的上下文。
3. 把 `instructions` 映射为内部 Chat Completions system message，把 `input` 映射为 user message / input item，把 `tools`、`tool_choice`、reasoning/metadata 等映射到 Chat Completions 请求。
4. 调用内部上游 `POST /v1/chat/completions`；该外部 exchange 经现有 recorder 写为 `.http` V3 cassette，并在有 ent audit store 时写一条 `upstream_exchanges` 关联（response id、request audit id、recorder request id、cassette path、route target、model、endpoint、status、时间戳）。本地 `/v1/responses` 入站调用本身不作为外部 upstream cassette 录制。
5. 返回 OpenAI Responses 风格 response object，并写入 `responses` / `response_items` semantic state、`execution_events` 和 tool call 相关审计。

continuation 与 input items：

- `previous_response_id` 从 runtime store 加载历史 items 后继续对话，多轮只发增量 `input` 即可；store 复用同一实例时该 continuation 可跨 handler 重启。
- 未配置 server-side executor 的普通 `function` tool 默认 client-owned：function schema 转给内部 Chat Completions，模型返回的 Chat `tool_calls[]` 保存为 Responses `function_call` output item，客户端后续通过 `previous_response_id` + `input[]` 里的 `function_call_output` 继续。
- `store` 语义：`force_store=true` 时始终落库；否则 `store:false` 的请求不落库，未传 `store` 视为落库。

流式：

- `stream:true` 返回 `text/event-stream`，最小事件序列为 `response.created`、`response.in_progress`、`response.output_item.added`、`response.content_part.added`、`response.output_text.delta*`、`response.output_text.done`、`response.content_part.done`、`response.output_item.done`、`response.completed`、`data: [DONE]`。
- 简单文本输出是真实增量转发：边读内部 Chat Completions SSE，边输出 `response.output_text.delta`，完成后存储完整 response。`internal/responses/chatclient` 会把上游 SSE 聚合为内部 `ChatCompletionResponse`（文本 delta、function tool call delta、usage trailer）。普通 function tool 参数分片输出 `response.function_call_arguments.delta/done`；已注册 function executor、provider 就绪的 hosted `web_search` / `web_search_preview` 和 hosted `mcp` 的 stream tool loop 会输出 started 态 `response.output_item.added` 与完成/失败态 `response.output_item.done`，把 tool output 注入下一轮内部 Chat Completions，再继续最终文本 delta；tool_call events 会标记 `stream=true`。
- 未知 hosted 工具、非平凡 `tool_choice`、不支持的组合以及部分 auto compact 组合在写出 SSE 和调用上游之前返回包装的 `ErrIncrementalStreamUnsupported`，HTTP 层记录 `response.stream` fallback event 后转入 deferred SSE envelope。
- 已写出 SSE 之后的 runtime 错误会尽量追加最小 `response.failed` SSE，并保留 `response.stream` failed audit。
- 客户端取消/断连会传播到内部 Chat Completions 调用，并以 `cancelled` 状态写入 request/model_call audit、execution event 和 upstream exchange；对应 HTTP 状态为 499。

## Codex 兼容

配置面 `responses_server.codex_compat`：`enabled`、`auto_inject_hosted_tools`（工具类型列表）、`inject_when_tools_absent`（默认 `true`）、`preserve_client_tools`（默认 `true`）、`default_tool_choice`（默认 `"auto"`）。启用后 handler 会把 `auto_inject_hosted_tools` 中当前真正可用的 hosted tool 注入到没有 `tools` 的请求，并按 `preserve_client_tools` 决定是否保留客户端工具。

最小兼容合约（原独立的 Codex 兼容性文档已并入本节）：

- Text create：接受 `model`、`instructions`、`input`、`store`、`metadata`；`model` 可由请求显式给出，省略时必须配置 `responses_server.default_model`。面向 Codex 建议显式 `store:true`，让 continuation 与审计有稳定状态。
- Streaming text：满足上文事件序列；复杂 compact/工具组合仍可能 fallback 到 deferred SSE。
- Function call and continuation：默认 client-owned，只有 `responses_server.function_executors.enabled=true` 且声明同名 executor 时才变成 server-owned opt-in 执行。
- Ordinary hosted `web_search` descriptor：`{"type":"web_search"}` / `{"type":"web_search_preview"}` 可被解析和审计；provider 未启用时普通 descriptor 不阻断 text create/stream，也不会暴露给上游；provider 就绪时映射为内部 function tool，由模型显式 tool call 后执行。
- Unsupported hosted tools：强制 `tool_choice` 为 `file_search` / `code_interpreter` / `computer_use_preview`（以及未就绪的 `mcp` / `web_search`）时返回稳定 OpenAI-style error envelope，`code` 为 `unsupported_tool`，message 含 `unsupported hosted tool "<tool>"`，并写不含 raw descriptor/payload 的 `tool_call_audits` rejected 记录。

Codex TOML 生成命令 `llm-tracelab models codex-config <model>`：

- 离线运行，不探测上游、不运行真实 Codex、不读取真实 API key；支持全局 `--format text|json` 与可选 `--codex-config <path>`。
- JSON envelope 的 `command` 为 `models.codex_config`；`result.profile` 含 `model_provider`、`model`、`model_context_window`、`model_auto_compact_token_limit`。
- `result.provider.base_url` 由 `server.port` 与 `responses_server.path` 推导，`wire_api` 固定为 `responses`。
- profile 匹配顺序固定：先 `responses_server.model_profiles[].name` 精确匹配，再 `pattern` 通配匹配。未匹配时命令仍成功，context window 与 compact token limit 输出 `0` 并给出 warning；`model_auto_compact_token_limit` 在有 context window 时保守回退为 `context_window_tokens` 的 80%。
- `result.diagnostics` 输出 matched profile、`runtime_profile_source` / `profile_precedence`、context/compact/output/reasoning 各 limit 的 `*_source`、`capability_source`，以及本地 SQLite app DB 可用时的 `model_catalog` / `channel_models` drift。
- 传入 `--codex-config <path>` 时只读解析该 TOML，检查 `[profiles.<model>]`、`[model_providers.llm-tracelab]` 及关键字段漂移；文件缺失/不可读/解析失败只产生 diagnostics 与 warnings。

排障入口：`config inspect --format json` 看脱敏 effective config；`audit query --response-id ... --include-events --include-exchanges` 看 trace；Monitor Audit 页面与 MCP `responses_audit_trace` 复用同一事实源；`provider probe` / `provider probe-report` / `provider probe-apply` 检查与保守补全上游 API surface。离线 fixture 位于 `tests/fixtures/codex/`，`task test:codex-fixtures` 是 focused 离线 gate（校验 fixture inventory、JSON/NDJSON 结构、handler reachability 与最小 runtime/parser 对齐），不是真实 Codex e2e runner。

## Hosted tools

执行器注册表在 `internal/responses/tools/hosted/contract.go`：`Executor` 接口为 `Type()`、`Enabled(ToolContext)`、`ExecuteHostedTool(ctx, ToolContext, ToolCall)`；`Registry` 提供 `Register` / `Resolve(toolType)` / `List(ctx)`，工具类型常量为 `web_search`、`mcp`、`file_search`、`code_interpreter`、`computer_use_preview`。runtime 在 `New(...)` 时建立 registry，并通过 Option 装配 executor。

当前可执行：

- `web_search` / `web_search_preview`：由 `internal/responses/tools/websearch` 提供，`tools.web_search.provider` 支持 `disabled`、`mock`、`searxng`；`enabled=true` 且 provider 非 disabled 时注册 hosted executor。普通 descriptor 只在模型显式 tool call 后执行；强制 `tool_choice` 但 provider 不可用时返回 `unsupported hosted tool "web_search"` 并写 rejected audit。
- `mcp`：由 `internal/responses/tools/mcp/executor.go` 提供，`tools.mcp.enabled=true` 且存在 enabled server 时经 runtime 的 `executeMCPToolCall` 执行（Streamable HTTP `tools/call`）。支持 `bearer_token_env`、`enabled_tools` / `disabled_tools`、`default_timeout_ms`、`max_result_bytes`、安全错误与 redaction；映射给上游模型的 Chat tool 名为 `mcp_call`。执行结果与 lifecycle 写入 `tool_call_audits`。
- Server-side function executor：`internal/responses/functionexec` 的默认空 registry，YAML opt-in `responses_server.function_executors`；只对已注册的同名普通 `function` 做 server-owned 执行。类型为 `static_response` 与受限 `external_command`（不用 shell，默认不继承环境变量，stdin JSON 传入 tool call，stdout 作为 tool output，stderr 只进失败摘要并受截断上限保护）。

职责边界：TraceLab 服务端负责解析 `tools` / `tool_choice`、执行受控工具、timeout / result 上限 / redaction / safe error、写 `execution_events` 与 `tool_call_audits`，并对未启用或不支持的工具返回稳定 rejected/unsupported 错误而不是静默忽略。客户端（Codex / OpenAI SDK / Monitor）负责决定是否传 `tools`、配置 provider/base_url/wire_api/API key、展示 tool item 与引用/错误，以及任何人工确认交互。

Tool Matrix（仅当前为真的部分）：

| Tool | 当前状态 |
| --- | --- |
| `web_search` / `web_search_preview` | mock / SearXNG provider，non-stream 与 incremental stream tool loop 可执行，写 started/completed/failed audit。 |
| `mcp` | 配置化 Streamable HTTP `tools/call`，non-stream 与 incremental stream tool loop 可执行，写 lifecycle audit。 |
| `function`（server-side） | 默认 client-owned；YAML 声明同名 `static_response` / `external_command` 且启用后变 server-owned。 |
| `file_search` | 无执行器：强制调用返回 `unsupported_tool` 并写 rejected audit。 |
| `code_interpreter` | 无执行器：同上。 |
| `computer_use_preview` | 无执行器：同上。 |

所有工具的配置默认关闭；仓库自带 `config/config.yaml` 是集成测试模板，显式打开了 `codex_compat`、`tools.web_search`、`tools.mcp` 与 `mcp`，生产部署应按需显式关闭或收紧。

## Compact 与上下文优化

- 显式 `POST /v1/responses/compact`：读取目标 response 的 continuation history，调用上游 Chat Completions 生成 `summary` output，把 compact response 存入 runtime store，并写 `response.compact` started/completed/failed events。后续 continuation 会停在 compact boundary，并把该 summary 作为 system message 注入。
- 自动 compact：`responses_server.auto_compact=true` 且带 `previous_response_id` 时，history item 数超过 `responses_server.compact_history_item_threshold` 会先生成 compact response，并写带 `auto_triggered` 的 `response.compact` event。匹配 `responses_server.model_profiles` 时，profile 的 `compact_history_item_threshold` 覆盖全局阈值。
- Token 预算：profile 的 `context_window_tokens` 与 `max_output_tokens` 参与预算；估算输入 token 加保留输出 token 超过 context window 时触发 `context_window_tokens` 原因的 auto compact。`max_output_tokens` 在客户端未显式传值时作为内部 Chat Completions `max_tokens` 默认值。
- estimator：默认是 `AdapterBackedTokenEstimator`，包装确定性保守计数器，adapter 失败会 fallback。profile 配了 `name` 或 `pattern`、`context_window_tokens`，未显式 `tokenize_counter.enabled=false`，且匹配 target 声明 `capabilities.tokenize=true` 时，HTTP provider `/tokenize` counter 会自动接入；显式 `enabled=true` 仍可强制启用。
- Profile 解析与 rewrite：先按 `name` 精确匹配，再按 `pattern` 通配匹配。`upstream_model` 只改写内部 Chat Completions 请求的 model，外部 Responses `model` 保持客户端请求值或默认 model。
- Provenance：compact response 的 `metadata._gateway.compact` 写入 source/compact response id、source item 与 window 计数、安全 item refs（只含 id/type/status/role/call_id/name）、retained summary boundary、summary item ids、budget/trigger 与 auto/manual 标记，不含 raw prompt、summary 文本、function arguments 或 tool output。manual 与 auto compact 的 event 会引用/摘要该 metadata；`internal/responses/audit.QueryService.ListCompactProvenance` 提供 event 派生的安全 read model。
- Channel profile opt-in：显式 `responses_server.adopt_channel_model_profiles=true` 时，runtime 与 `models codex-config` 才会读取 enabled channel / enabled model / `profile_adoption_status=adopted` 且未声明 `supports_chat_completions=false` 的 channel model profile；显式 `responses_server.model_profiles` 始终最高优先，多 profile 冲突时保守跳过。

## 存储与保留策略

- runtime store：serve 装配时若 trace store 提供 ent client，使用 `runtime.NewEntStore`（表 `responses`、`response_items`）；否则退回 memory store。语义状态包括 response checkpoint、input/output item 与 `previous_response_id` 链，是 `/v1/responses/{id}/input_items` 的数据来源。
- `force_store=true` 时所有 response 都落库；否则遵循请求的 `store` 字段（未传视为落库）。
- 审计表：`request_audits`（入站 envelope，含 accepted/completed/failed/rejected/cancelled 状态）、`execution_events`（runtime plan、model call、tool/stream lifecycle、compact、cancel/error）、`upstream_exchanges`（semantic response 与 `.http` cassette / trace id / route target 的关联）、`tool_call_audits`（hosted/server-side tool 的持久 read model）。`routing.settings` 存于 `app_settings`。
- 事实源边界：Postgres 是生产与长会话的 application DB 主路径，SQLite 只是本地开发/测试与既有本地库的 `startup_schema_fallback`（SQLite 无版本化迁移）。raw `.http` V3 cassette 仍是 replay 与 trace detail 的事实源，数据库只是派生索引；旧 V2 cassette 读取兼容保留，`pkg/replay` 不依赖数据库或 runtime。
- 代码中没有针对 Responses semantic/audit 表的 TTL 或定期清理逻辑，保留策略由底层数据库与部署决定。运维细节见 `./STORAGE_AND_DEPLOYMENT.md` 与 `./POSTGRES_OPERATIONS.md`。

审计查询面（都复用同一个 `internal/responses/audit.QueryService`）：

- CLI：`audit query`（别名 `audit responses`）按 `--response-id` / `--request-audit-id` / `--client-request-id` / `--conversation-id` 查询 trace，`--include-events` / `--include-exchanges` / `--include-tools` 控制返回内容；`--list` 配合 `--status`、`--operation create|compact|input_items` 返回 request audit 摘要列表。`audit tool-calls` 查询 `tool_call_audits` read model。
- Monitor：`/api/responses/audit/trace` 与 `/api/responses/audit/tool-calls`。
- MCP：`responses_audit_trace` 与 `responses_audit_tool_calls` 只读工具。

## 配置项总览

`responses_server` 真实字段（`internal/config/config.go`，附代码内置默认值）：

| 字段 | 类型 | 说明与默认值 |
| --- | --- | --- |
| `default_model` | string | 请求未带 `model` 时使用；配置与请求都为空则无法解析模型。 |
| `force_store` | bool | 默认 `false`；为 `true` 时忽略请求的 `store:false`。 |
| `max_request_body_bytes` | int64 | `<=0` 时用内置默认 64 MiB（`64<<20`）；仓库模板设为 67108864。 |
| `path` | string | 默认 `/v1/responses`。 |
| `auto_compact` | bool | 默认 `false`；仓库模板设为 `true`。 |
| `compact_history_item_threshold` | int | `<=0` 表示不按 item 数触发；仓库模板设为 80。 |
| `model_profiles[]` | list | 每项：`name`、`pattern`、`context_window_tokens`、`max_output_tokens`、`tool_output_token_limit`、`model_reasoning_effort`、`compact_history_item_threshold`、`upstream_model`、`tokenize_counter{enabled,upstream_id,timeout}`。 |
| `adopt_channel_model_profiles` | bool | 默认 `false`；开启后允许 channel model profile 作为 runtime profile 补充源。 |
| `function_executors` | object | `enabled`（默认 `false`）、`timeout`（默认 `5s`）、`max_result_bytes`（默认 64 KiB）、`redaction.arguments`、`redaction.output`、`executors[]`。 |
| `function_executors.executors[]` | list | 每项：`name`、`type`（`static_response` \| `external_command`）、`enabled`、`output`、`command`、`args`、`timeout`、`env`、`env_allowlist`、`process{working_dir,require_absolute_command,allowed_command_dirs,reject_root}`。 |
| `codex_compat` | object | `enabled`、`auto_inject_hosted_tools`、`inject_when_tools_absent`（默认 `true`）、`preserve_client_tools`（默认 `true`）、`default_tool_choice`（默认 `"auto"`）。 |

相关但不在 `responses_server` 下的配置：`tools.web_search`（`enabled`、`provider`、`max_results`、`base_url`、`timeout_ms`、`user_agent`）、`tools.mcp`（`enabled`、`default_timeout_ms`、`max_result_bytes`、`servers[].{id,label,url,bearer_token_env,enabled_tools,disabled_tools,enabled}`）。可选字段均有对应 `LLM_TRACELAB_RESPONSES_*` 环境变量覆盖（例如 `..._DEFAULT_MODEL`、`..._FORCE_STORE`、`..._MAX_REQUEST_BODY_BYTES`、`..._PATH`、`..._AUTO_COMPACT`、`..._COMPACT_HISTORY_ITEM_THRESHOLD`、`..._FUNCTION_EXECUTORS_*`、`..._CODEX_COMPAT_*`），没有 `LLM_TRACELAB_RESPONSES_ENABLED`。完整配置与部署方式见 `./README.md`、`./PROXY_USAGE_EXAMPLES.md` 与 `./DEVELOPMENT.md`。

## 非目标与未实现

- `file_search`、`code_interpreter`、`computer_use_preview` 没有真实执行器：只做稳定拒绝与 rejected audit，不伪造 citation、chunk、code output 或 computer-use 副作用。
- MCP 只有 `tools/call` 执行：`tools/list` discovery 缓存、resources/prompts、OAuth token 管理、approval、long-running calls 均未实现。
- 复杂跨轮/混合工具/cancel 的完整 stream lifecycle 未完成：auto compact 后未知工具、非平凡 `tool_choice` 和复杂组合仍 fallback 到 deferred SSE envelope。
- 完整 context optimization 未实现：目前只有 item-count 阈值、基于 context window 的 token 预算、estimator 与 `/tokenize` counter。
- 没有通用 `GET /v1/responses/{id}` HTTP 合约：只有 `/v1/responses/{id}/input_items` 子资源。
- native Responses 只做直通代理，不做 semantic interposition；本地 runtime 也不会被用作 native target 的 Chat Completions backend。
- 没有独立的 compact read model schema 与 HTTP/CLI 查询 surface：provenance 目前内嵌在 response metadata 并由 execution events 派生。
- 没有 semantic execution replay：replay 仍只针对 `.http` cassette，不依赖 execution events 或 semantic DB。
- Provider auto-detect 未完成：capability 需显式配置或由渠道/模型数据表达，`provider probe` 只是诊断与保守补全。
- `external_command` 只有轻量 process policy（`working_dir`、`require_absolute_command`、`allowed_command_dirs`、`reject_root`），没有 root/container 级沙箱。
- SQLite 版本化迁移与独立 auth migration namespace 未实现：Postgres auth 与 application schema 共享同一 namespace。
- 本地 `/v1/responses` 入站请求不作为外部 upstream cassette 录制；只有内部上游调用会被录制。
- 本地 runtime 不做 OpenAI、Anthropic、Gemini、Vertex 之间的跨协议转换：普通代理热路径是 protocol-aware pass-through，跨协议翻译只在 `/v1/responses` 的本地执行模式内发生。
- 渠道 `mode: responses_server` 不会被当作行为开关：它只是描述性/校验元数据，native 与本地由按模型的 capability 决定。
