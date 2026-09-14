# 当前实现概览

本文档用中文概括当前最新代码事实。它不是路线图，也不描述未落地的愿景。

## 产品定位

`llm-tracelab` 是一个 Postgres-first 的 LLM gateway，包含 LLM API 录制、回放、观测、Responses server-mode 和调试能力。

当前核心闭环是：

1. SDK 或 CLI 流量经过 gateway。
2. 代理按协议族选择上游并透传请求。
3. 原始 HTTP 请求/响应写入 `.http` cassette。
4. 生产部署用 Postgres 索引请求元数据、路由信息、会话信息、模型/渠道配置、Responses state、audit 和派生分析结果。
5. Monitor Web 展示请求、会话、模型、渠道、路由、事件、发现和分析任务。
6. MCP 工具向 AI agent 暴露只读排障、trace 查询、失败聚类、系统事件和重分析入口。
7. `pkg/replay` 在测试中基于 cassette 回放响应，不访问上游网络。

SQLite 当前保留为本地开发、离线测试和兼容已有本地 DB 的 fallback，不是生产 versioned migration 主路径。

## 当前协议边界

当前代理是协议族感知的透传代理，不是跨协议转换网关。

已实现协议族：

- OpenAI-compatible：Chat Completions、Responses、Embeddings、Models。
- Anthropic Messages：Claude `/v1/messages`。
- Google GenAI：Gemini `generateContent`、`streamGenerateContent`、模型列表。
- Vertex native：Vertex Gemini `generateContent`、`streamGenerateContent`、模型发现路径。

TraceLab 能解析这些协议并写入统一观测结构，但不会在转发热路径中把 Anthropic Messages 转成 OpenAI-compatible，也不会把 OpenAI Responses 转成 Gemini 或 Claude。

Responses server-mode 始终可用，`/v1/chat/completions`、`/v1/responses`、`/v1/messages` 三个下游入口无条件受理。`/v1/responses` 在选路阶段按模型的上游能力二选一：命中 native Responses upstream 时直接代理透传；否则（模型只在 Chat Completions upstream 上）由本地 Responses runtime 接管，把请求编排为内部上游 `/v1/chat/completions` 调用。`responses_server.enabled` 已被彻底移除（连同 `LLM_TRACELAB_RESPONSES_ENABLED` 环境变量）；如需关闭本地翻译，设置 `routing.settings.responses_strategy=native_only`。本地 Responses runtime 采用惰性构建：`tools.web_search`、function executor、model profile 等可选配置出错不会再阻塞服务启动，而是在首个真正需要它的本地 Responses 请求上以 502 报错。

开启 server-mode 后，当前已支持非流式 Responses 请求经本地 runtime 映射为内部上游 `/v1/chat/completions` 调用；该内部上游 HTTP exchange 会按现有 recorder 写入 `.http` cassette，并在有 ent-backed audit store 时写入一条最小 `upstream_exchanges` correlation。`internal/responses/chatclient` 直接调用 OpenAI-compatible Chat Completions 上游时，已能把 `stream:true` SSE 聚合回内部 `ChatCompletionResponse`，覆盖文本增量、function tool call 参数分片和 usage trailer。server-mode 下游请求 `stream:true` 时，简单文本输出路径会边读取内部 Chat Completions SSE、边输出 `response.output_text.delta`；客户端提交普通 `function_call_output` 后通过 `previous_response_id` 继续对话的 `stream:true` 路径已有回归覆盖，会把既有 user/assistant tool call/tool output 历史注入内部 Chat Completions，并存储最终 streamed response。auto compact 后的简单文本 continuation 会先完成 compact、再把后续 streaming response 接到 compact response 后面；普通 `function` tool 参数分片也会边读取上游 SSE、边输出 `response.function_call_arguments.delta/done`，未注册同名 server-side executor 的普通 `function` tool 在 auto compact 后也会继续真实增量输出 arguments，同时把原始上游 SSE 写入 stream cassette 并在完成后存储完整 response。已注册 server-side function executor 的 stream 首切现在会先输出 function arguments delta/done，准备执行 executor 时向下游补充 started 态 `response.output_item.added` tool output item，成功后继续输出完成态 `response.output_item.done`，把 tool output 注入下一轮内部 Chat Completions，并继续输出最终文本 delta；auto compact 后的同名 registered executor 也会先 compact、再执行同一真实增量 stream tool loop；同一轮多个已注册 executor call 会按模型 tool call 顺序逐个输出 started/done 并注入下一轮上下文；最终 response 仍存储为 `function_call_output + final message`。hosted `web_search` 在 provider 就绪时也已接入 stream tool loop：输出 arguments delta/done，准备执行 server-side search 时向下游补充 started 态 `response.output_item.added` `web_search_call` item，成功后继续输出完成态 `response.output_item.done`，把结果注入下一轮内部 Chat Completions，并继续输出最终文本 delta；最终 response 存储为 `web_search_call + final message`。同一轮混合 registered function executor 与 hosted `web_search` 的 stream success path 已有回归覆盖，会按模型 tool call 顺序输出、执行、注入上下文并存储 `function_call_output + web_search_call + final message`；同一轮 registered function 已完成后 hosted `web_search` provider 失败的路径也已有回归覆盖，会保留前者 completed item，并对失败的 `web_search_call` 输出 `status=failed` 的 done item。stream tool loop 在已输出 started item 后遇到 executor/provider 错误时，会 best-effort 输出 `status=failed` 的 `response.output_item.done`，再返回原错误；同轮多个已启动 call 中，已完成的 call 保留 completed done，当前失败 call 输出 failed done。Responses server-mode 内部 Chat Completions 调用已使用客户端 request context，客户端取消会传播到上游，并把 request/model_call/upstream_exchange 记录为 `cancelled`。增量 stream 在已向下游写出 SSE 后遇到 runtime 错误时，会尽量追加最小 `response.failed` SSE，并继续记录现有 failed/cancelled audit；未写出任何 SSE 的错误仍走原 HTTP error 或 deferred fallback 行为。不支持的工具组合，以及 auto compact 后未知/未实现 hosted 工具或非平凡 `tool_choice` 的组合，会在写出任何 SSE 或调用上游前返回可被 `errors.Is(ErrIncrementalStreamUnsupported)` 识别的错误，并带具体 fallback reason，HTTP handler 会记录 fallback event 后转入 deferred Responses SSE envelope。更复杂的 auto compact hosted 工具流、复杂跨轮/cancel lifecycle 仍未完成。非 Responses 请求仍走现有代理、路由、录制和解析路径。

Upstream 配置已经包含 `api_type`、`mode` 和基础 `capabilities`。`api_type` 默认按协议族推断：OpenAI-compatible 为 `chat_completions`，Anthropic 为 `messages`，Google GenAI / Vertex 为 `gemini_generate_content`。当前路由会把 Chat Completions endpoint 的 API surface 当作约束；显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 target 不会被本地 Responses runtime 的内部 `/v1/chat/completions` 调用选中。`capabilities.tool_calling: false` 的 target 在请求体包含 `tools` 时也不会被选中，decision trace 会标记 `unsupported_tools`。当前还提供手动诊断命令 `provider probe`：它会对配置中的 upstream endpoint 做保守探测，输出建议的 `api_type`、`protocol_family` 和 capability signals；`doctor --probe-providers` 会显式复用同一套 provider probe 并输出脱敏摘要；`provider probe-report` 是同类只读批量报告入口，面向 YAML upstream 列表输出 report，不写配置。默认启动不会执行 probe；配置 `provider_probe.startup_fill=true` 后，serve 启动会只在内存中填补 YAML upstream 缺失的 `api_type`、`protocol_family` 和未声明 capability，不写回配置，也不覆盖显式配置。Monitor provider create dialog 已提供临时 `POST /api/provider-probe` preview，不落库返回同类 report；`POST /api/provider-setup/validate` 会把 base URL、API key、provider preset、model discovery 和 capability 字段组合成一次 setup validate，返回 provider probe、归一化配置和 redacted secret state，不落库且不回显 API key；`POST /api/provider-setup/apply` 复用同一归一化逻辑；当 probe 检测成功，或用户显式提供 `api_type` 与 `protocol_family` 时，才写入 channel store。setup 建议只填补缺失字段或未声明 capability，不覆盖显式 `api_type`、`protocol_family` 或 capability false。`config inspect` 会输出脱敏 effective config，并通过 `sources` 摘要以保守枚举标注主要字段来自 config file、default、effective、empty、derived 或 not_configured。`POST /api/provider-probe/report` 会面向 channel 列表返回批量 detection report，不写 probe run、model 或 channel 配置；provider detail 的 probe 动作也会返回 provider detection report。页面会展示建议的 API type、protocol family、capabilities 和 warnings；点击 Apply suggestions 才会显式写入表单或 channel 配置。Monitor Providers 列表页现在提供 Batch probe and apply：必须先调用只读 report 预览，再对 detected 且存在可补字段的 channel 调用 `POST /api/provider-probe/report/apply`；CLI `provider probe-apply` 复用同一 channel service 用例。批量写入口不接收或返回 API key，只填缺失 `api_type`、`protocol_family` 和未设置 capability，不覆盖显式配置或显式 `false` capability，成功写入后会 reload router 或更新后续命令打开的 channel store。

Codex 兼容性已有 focused 离线 gate。`tests/fixtures/codex/` 存放最小 Responses 兼容 fixture，`internal/responses/codexfixtures` 会枚举当前 inventory 并校验 JSON/NDJSON 结构，`task test:codex-fixtures` 运行 `internal/responses/httpapi` 与 `internal/responses/runtime` 的离线测试，覆盖 fixture schema/contract、HTTP handler reachability 和最小 runtime/parser 对齐；该 gate 不依赖真实 Codex、真实模型、网络或 Postgres。`models codex-config <model>` 会离线输出 Codex TOML 建议和 JSON diagnostics；默认 `runtime_profile_source` 为 `responses_server.model_profiles`，匹配失败时按 `zero_limits_when_unmatched` 输出 0 值，`catalog_profile_role=available_for_runtime_opt_in`，`provider_channel_profile_adoption=report_only`。`profile_adoption_report` 默认 `dry_run=true`、`mutates=false`，并输出 `adoption_ready=true`、`blocking_gate_count=0`；`schema_migration` 是 `implemented_runtime_opt_in`，`dry_run_diff`、`conflict_report`、`rollback_plan`、`dsn_gated_tests` 是非阻塞 implemented contract。配置为 SQLite 且 application DB 文件已存在时，命令会以 `AutoMigrate:false` 打开 store，只读检查请求模型是否存在于 `model_catalog` 与 `channel_models`，输出 catalog/channel 命中、channel model count、source 状态和 drift warnings；channel model candidate 包含 `context_window_tokens`、`max_output_tokens`、`compact_history_item_threshold`、`upstream_model`、`profile_source` 和 `profile_adoption_status`。显式配置 `responses_server.adopt_channel_model_profiles=true` 后，runtime 与 `models codex-config` 会读取 enabled channel、enabled model、`profile_adoption_status=adopted` 且未声明 `supports_chat_completions=false` 的 channel model profile；无显式 profile 且 adopted profile 无冲突时，Codex 建议会使用 `runtime_profile_source=channel_models.profile_adoption` 和 `profile_adoption_report.status=adopted_runtime`。显式 `responses_server.model_profiles` 仍最高优先，多个 adopted profile 对同一 model 产生冲突时会保守跳过。`capability_source=provider_upstream_capabilities` 表示路由和 tokenize 自动选择仍由 provider/upstream capability 驱动。命令还支持显式 `--codex-config <path>`，只读解析本地 Codex TOML，检查 `[profiles.<model>]` 与 `[model_providers.llm-tracelab]` 是否存在，并对比 profile/provider 关键字段；未传 flag 时标记 `not_configured`，不会读取用户真实文件。不存在、不可读或解析失败只产生 diagnostics/warnings，不会 panic；输出不回显真实 API key、token、env var value 或完整敏感文件内容，URL actual 会脱敏。`doctor` 也已把同一套默认模型/profile/catalog/channel drift 诊断纳入 `responses_server.model_catalog_drift` check：Responses server 关闭时 pass，default model 为空时 warn 并标注 skipped reason，SQLite DB 不可用时 pass 回落，有 drift warnings 时 warn。`doctor --codex-config <path>` 还会通过 `responses_server.codex_config_drift` 复用 `models codex-config` 的本地 Codex TOML drift helper；未传 flag 时 pass 并标注 `not_configured` 且不读取真实 Codex 文件，missing/unreadable/parse_error/drift 均为 warn，不会让 doctor 默认失败，也不会输出 TOML 文件内容、API key/token 或未脱敏 URL secret。`responses_server.http_guard` 现在覆盖 Responses server-mode 的离线 HTTP guard/entrypoint 诊断：关闭时 pass 并标注 skipped reason；开启时输出 responses path、normalized path、`max_request_body_bytes`、`force_store`、auth verifier 配置状态、server/monitor port 摘要，以及 MCP path 和 monitor `/api` management path 的明显冲突判断；非法 path fail，过小 body limit warn。`responses_server.store_health` 覆盖 Responses store/backend 健康：默认不连接数据库，只报告 driver、auto_migrate、force_store、migration mode、required semantic/audit/settings table set，以及是否建议显式 `--check-db`；当 `force_store=true` 且 `database.auto_migrate=false` 且未传 `--check-db` 时 warn。显式 `doctor --check-db` 时会复用 app DB status check，并检查 `responses`、`response_items`、`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`、`app_settings` 是否存在；缺表或 DB 不可达为 fail。默认 DB 不存在、`:memory:`、非 SQLite 默认保持离线回落；Postgres 只有显式 `--check-db` 才会只读打开并参与 adoption diagnostics；所有 DB 错误和 DSN 输出都会脱敏，不输出 secret。

Hosted `web_search` 已有首切实现。配置 `tools.web_search.enabled=true` 后，可选择 `mock` 或 `searxng` provider；Responses runtime 会把 `web_search` / `web_search_preview` 暴露为上游 Chat Completions function tool，执行 server-side search，并把结果注入下一轮 Chat Completions。默认关闭，不影响普通代理路径。Stage 14A 已为 hosted `web_search` 写入 `response.tool_call` execution events，覆盖 started/completed/failed，details 中包含 tool name、call id、query、iteration、max results 和 result/error 摘要；`stream:true` 路径也会用真实 stream tool loop 执行 hosted search，并在 started/completed/failed details 中写 `stream=true`，同时向 Responses SSE 输出 started 态 `response.output_item.added` 和完成态 `response.output_item.done` tool item。强制 `tool_choice` 选择未启用的 `web_search` 或未实现的 `mcp` / `file_search` / `code_interpreter` / `computer_use_preview` 时，runtime 返回稳定 `unsupported_tool` error，并写入 `tool_call_audits` rejected 记录，不保存 raw descriptor/payload。普通 `function` tool 默认仍走客户端回路：runtime 会把 tool schema 转给上游 Chat Completions，把模型返回的 function call 保存为 Responses `function_call` output，客户端后续提交 `function_call_output` 时通过 `previous_response_id` 继续对话，并写 requested/submitted tool_call events。server-side function executor registry 默认仍为空；配置 `responses_server.function_executors.enabled=true` 且声明可用的 `static_response` 或 `external_command` executor 后，runtime 会自动执行同名 function tool、注入下一轮模型上下文，并写 started/completed/failed tool_call events。`external_command` 默认不继承环境变量，不使用 shell，通过 stdin 接收包含 `call_id`、`name`、`arguments` 的 JSON；stdout 作为工具输出，stderr 只进入失败摘要且有截断上限。当前策略支持工具级 timeout、最大结果字节数、audit arguments/output 摘要 redaction，以及 opt-in 的进程隔离首切：`process.working_dir` 显式设置执行目录并要求绝对且已存在，`process.require_absolute_command=true` 时禁止相对 command/PATH 查找，`process.allowed_command_dirs` 会要求 command 解析到允许目录内，`process.reject_root=true` 会在当前进程以 root 运行时拒绝执行 external command。未配置 `process` 时默认行为不变。已注册 executor 的 `stream:true` 首切已能输出 arguments delta/done、执行 executor 前输出 started 态 `response.output_item.added`，执行成功后输出完成态 `response.output_item.done` tool item，并继续流式输出最终文本，tool_call started/completed/failed events 的 details 会带 `stream=true`。Monitor 已提供 `GET /api/responses/function-executors` 配置摘要和 `POST /api/responses/function-executors` 保守写配置入口，Audit 页面也会显示 enabled、timeout、max_result_bytes、redaction、支持类型、executor binding 启用状态、`available`、`output_configured`、`command_configured` 和 validation warnings，并提供 enabled validate/apply 控件。摘要不会返回 `static_response` output 内容，也不会返回 `external_command` command 内容；写接口也不接受 `output`、`command`、`args` 或 `env`，只接受 enabled、timeout、max_result_bytes、redaction、binding name/type/enabled 和 external_command process 隔离字段。`validate_only=false` 会把非敏感 overlay 持久化到应用库 `app_settings`，serve 启动会把该 overlay 合成到 YAML 敏感基线配置上，当前进程也会热更新共享 runtime executor registry，后续新请求生效；真实 command/output/env 仍只能来自 YAML。root/container 级 executor 沙箱（轻量 process policy 已有 allowed_command_dirs/reject_root 首切）和未来真实 MCP/file/code/computer-use 执行器 lifecycle 仍未完成。

Tool ownership boundary 已有回归覆盖：普通 `function` 默认 client-owned；即使 registry 中存在其它 executor，未注册同名 executor 的 function call 也只返回给客户端，不触发 server-side execution。

最小配置示例：

```yaml
responses_server:
  enabled: true
  auto_compact: true
  model_profiles:
    - pattern: gpt-4o*
      context_window_tokens: 128000
      upstream_model: gpt-4o-mini
      tokenize_counter:
        enabled: true
        upstream_id: primary
        timeout: 2s
  function_executors:
    enabled: true
    timeout: 2s
    max_result_bytes: 65536
    redaction:
      arguments: true
      output: true
    executors:
      - name: lookup
        type: static_response
        output:
          ok: true
          source: configured
      - name: local_lookup
        type: external_command
        command: /path/to/bin
        args: ["--mode", "lookup"]
        timeout: 2s
        process:
          working_dir: /var/lib/llm-tracelab/executors
          require_absolute_command: true
          allowed_command_dirs:
            - /var/lib/llm-tracelab/executors/bin
          reject_root: true
```

Compact workflow 已有首切实现。server-mode 会把配置 Responses path 下的子路径一起分流到本地 Responses handler；`POST /v1/responses/compact` 接收 `response_id`，读取目标 response 的 continuation history，调用上游 Chat Completions 生成 `summary` output，并把新的 compact response 存入 runtime store。compact response 的 input item 是 `compact_request`，后续 `previous_response_id` 指向该 compact response 时，history 会停在 compact boundary，并把 `summary` 作为 system message 注入下一轮模型上下文。当前 compact v2 provenance 首切会把安全 lineage/read-model metadata 写入 compact response 的 `metadata._gateway.compact`：包含 source/compact response id、source item/window 计数、只含 id/type/status/role/call_id/name 的 source/retained item refs、summary item ids、retained summary boundary、budget/trigger 字段以及 auto/manual 标记；不写 raw prompt、summary 文本、function arguments 或 tool output。manual compact completed event 和 auto compact 的 `response.compact` `auto_triggered` execution event 会引用/摘要同一份 metadata provenance；`internal/responses/audit.QueryService.ListCompactProvenance` 已能从这些 execution events 派生安全 read model，按 response/request/conversation selector 查询 source/retained refs、summary item ids、retained window 和 budget 白名单字段，不返回 raw prompt/summary/tool args/tool output。配置 `responses_server.auto_compact=true` 后，create continuation 会在加载 history item 数超过阈值或估算的 prompt+reserved output 超过 `context_window_tokens` 时先自动 compact，再把本次 response 接到 compact response 后面；`stream:true` 简单文本 continuation、未注册同名 server-side executor 的普通 `function` tool arguments、已注册同名 server-side executor 的普通 `function` stream tool loop、以及 provider 就绪且 `tool_choice` 为 nil/空/`none`/`auto` 的 hosted `web_search` / `web_search_preview` stream tool loop，也会在任何下游 SSE 写出前完成 compact，并把后续 streaming response 接到 compact response 后面。有效 item 阈值默认来自 `responses_server.compact_history_item_threshold`，也可由匹配当前 model 的 `responses_server.model_profiles[].compact_history_item_threshold` 覆盖；匹配 profile 且配置 `upstream_model` 时，runtime 的内部 Chat Completions 请求会使用该上游模型名，外部 Responses `model` 仍保留客户端请求 model 或默认 model。匹配 profile 的 `max_output_tokens` 会在客户端未显式传 `max_output_tokens` 时作为内部 Chat Completions `max_tokens` 默认值。当前 token budgeting 已有可注入 estimator 和 adapter-backed chat prompt counter 边界，默认实现仍是确定性的保守计数器；当 model profile 有 `name` 或 `pattern`、配置了 `context_window_tokens`、未显式 `tokenize_counter.enabled=false`，并且匹配到声明 `capabilities.tokenize=true` 的 upstream/router target 时，proxy 会自动用该 target 的 base URL、API key 和 headers 构造 HTTP provider `/tokenize` counter；显式 `tokenize_counter.enabled=true` 仍可在没有 context window 时强制启用。`/tokenize` counter 支持 `/v1` base URL 归一化、timeout 和 `count`/`token_count`/`tokens`/`token_ids` 等响应形状；adapter 或 provider 失败会 fallback 到 conservative estimator，且不会把 provider 错误 body 作为 token 估算错误向外暴露。完整 context optimization、独立 compact read model schema/HTTP/CLI surface 和更复杂 auto compact hosted 工具流真实增量仍未接入。

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

Postgres 是生产结构化状态主路径：Monitor 列表、统计、过滤、分页、模型/渠道配置、系统事件、Observation IR、findings、分析任务、eval 结果、Responses semantic state 和 audit 表都属于应用数据库。SQLite raw DDL 仍覆盖这些本地 fallback 表，用于本地开发、离线测试和既有 SQLite DB 兼容，不是生产 versioned migration 主路径。

Responses server-mode 的 semantic state 使用 runtime store。当前装配优先使用 ent-backed store，表为 `responses` 和 `response_items`；`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`、`app_settings` 也属于应用库。Postgres `db migrate up` 是生产 schema setup 主路径，通过 `internal/appdbmigrate` 和 `golang-migrate` 应用 checked-in `ent/postgres-migrations` SQL。命令/server 打开 application store 时已拆分 migrate 与 open：`database.auto_migrate=true` 先执行应用迁移，再以 no-auto-migrate 模式打开 store；`false` 则只打开已存在 schema。

`config inspect`、`db migrate status` 和 `doctor` 会输出 `production_storage_driver=postgres`、`production_ready`、`storage_role` 和 `storage_contract`。`db migrate status` 和 `db migrate up/down --dry-run` 报告当前应用库迁移来源：Postgres 为 checked-in `ent/postgres-migrations` SQL，SQLite 为 `internal/store` raw DDL startup fallback，且明确 auth migration 不属于 `db migrate` 范围。`db migrate status --check-db` 可显式读取 Postgres `schema_migrations` version/dirty 状态；SQLite 下只读解释 `app_schema_status` marker 和 required table 状态，不创建缺失文件、不做 destructive repair 或数据 rewrite。

Postgres `auth migrate up` 复用同一套 checked-in SQL，因为当前 ent/Postgres schema 同时包含 auth 表；Postgres `auth migrate down` 已阻止，以免 auth 命令回滚共享 application schema。`auth migrate status` / dry-run 会报告 `effective_database_namespace=application`、`schema_authority=application_postgres_migration_set`、`storage_contract=postgres_application_schema_owns_auth_tables`、`postgres_auth_namespace_strategy=shared_application_schema_migrations`、`independent_auth_namespace_status=not_implemented` 和 `auth_namespace_rollback_scope=unsupported_from_auth_cli_shared_application_migration_set`。

Postgres DSN-gated 测试覆盖 migrated Responses/eval/experiment/monitor/session/overview/upstream/routing/model/channel 代表路径；默认测试保持离线。当前已接入最小 request audit、内部 Chat Completions upstream exchange correlation、request/model_call/hosted web_search/function executor execution events、stream lifecycle、compact provenance、Postgres versioned SQL migration、application store open-vs-migrate 分离、SQLite schema marker 和只读 status 解释能力。root/container 级 executor 沙箱、完整 context optimization、新增或更深 analytics 查询和未来真实 MCP/file/code/computer-use 执行 lifecycle 等能力仍需持续审计或另行接入。

`audit query` 现在默认在 JSON/text trace 输出中附带顶层 diagnostics，基于已取到的 request audit、execution events、upstream exchanges 和 derived tool calls 保守派生 event/upstream/tool-call count、latest status、cancel/failed/stream/compact signals、pending tool calls 和 compact candidate/summary；不会输出 raw arguments/query/output/error。`audit query --list` 可按 conversation/client request/status/operation 等现有 selector 返回 request audit summary 列表，`--status` 支持 `accepted`、`completed`、`failed`、`rejected`、`cancelled`，`--operation` 支持 `create`、`compact`、`input_items` 且只允许和 `--list` 搭配，非法值会返回明确 usage error；operation 由 `request_audits.method/path` 派生并在 QueryService 层下推为 method/path predicate，不读取 body/header。summary 只包含 id、response_id、conversation_id、client_request_id、status、operation、created_at。当前 schema 仍没有真实 thread/session/turn 字段，因此尚不支持这些范围查询。

Responses audit schema 的职责边界如下：

- `request_audits`：记录入站 Responses request envelope、client request id、headers allowlist、body hash/preview 和完成状态。当前只在 Responses server-mode 写入，并可通过 audit query service / MCP 查询；CLI 已支持默认顶层 diagnostics 和 `--list` summary 范围查询，可按 status 和由 method/path 派生的 operation 下推过滤，列表不输出 body/header raw。
- `execution_events`：记录 runtime plan、model/tool/compact/stream/error 生命周期事件。当前已写入 request、内部 model_call、hosted `web_search` tool_call、普通 function requested/submitted、registered server-side function started/completed/failed（含 stream tool loop 标记）、incremental stream fallback、deferred/incremental stream started/completed、request/model_call cancellation、explicit compact 和 auto compact threshold trigger 的最小生命周期事件；auto compact event details 会引用/摘要 compact response metadata 中的安全 provenance。
- `upstream_exchanges`：关联 semantic response/request 与 `.http` cassette、trace id、route target。当前只覆盖 Responses server-mode 内部 Chat Completions 调用，并在 response 完成后回填 semantic `response_id`；`trace_id` 暂使用 recorder prelude 的 `meta.request_id`。

后续接入顺序建议继续处理 hosted/server-side streaming lifecycle 细化、compact events 和更完整的 Responses audit 关联入口。

Storage 生产边界已定案：Postgres 是生产存储、迁移和运维唯一主路径；SQLite application DB 只作为 `startup_schema_fallback` 兼容 legacy/dev/test。`db migrate status --check-db` 对 SQLite 只读解释 marker/required table 状态，不做 destructive repair、文件创建或数据 rewrite。Postgres auth 继续共享 application `schema_migrations` namespace；独立 auth namespace 当前未实现。`auth migrate down` 已阻止，当前 rollback scope 为 `unsupported_from_auth_cli_shared_application_migration_set`；真实 Postgres 检查继续 DSN-gated，默认测试保持离线。

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
- Channels：管理上游渠道、执行 provider setup validate/apply、批量 provider probe/apply、探测模型、启停模型。
- Audit：跨 trace findings、Responses request audit trace、function executor 状态与安全 overlay。
- Routing：查看 selected route、sticky、候选和失败聚类。
- Events：系统事件收件箱。
- Tokens：管理当前用户 API token。
- Trace detail：Timeline、Summary、Raw Protocol、Declared Tools、Observation、Findings；如果 trace payload 或 `upstream_exchanges.trace_id` 能关联到 `response_id` / `request_audit_id`，Reading guide 会提供 Responses audit 跳转入口。

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

长期配置以应用数据库中的 channel/model 记录为准，并通过 Monitor Web 管理；生产部署使用 Postgres，本地 fallback 可使用 SQLite：

- 创建/更新渠道。
- 创建前 provider setup validate 不落库，Create provider 才写入 channel store；Monitor create dialog 已展示 normalized config/probe/redacted secret state，字段变更会清空旧验证结果；若 probe 未检测成功，需显式提供 `api_type` 与 `protocol_family`。
- 在 Providers 列表页先预览只读 provider probe report，再批量应用 detected 且可补的 `api_type`、`protocol_family` 和 capability 建议；显式配置和 capability false 不会被覆盖。
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
- 用结构化数据库替代 raw cassette 作为 replay 事实源。
- auto compact 后未知/未实现 hosted 工具、非平凡 `tool_choice` 或更复杂混合工具等复杂场景的真实增量 Responses server-mode streaming；client-owned `function_call_output` continuation 的 `stream:true` 路径、auto compact 后未注册普通 function arguments、同名 registered executor stream tool loop、provider 就绪 hosted `web_search` / `web_search_preview` 平凡 `tool_choice` tool loop、普通 function、已注册 server-side executor 和 provider 就绪 hosted `web_search` 已有 stream tool loop 首切，内部 Chat Completions upstream cancel 传播已落地。
- function executor root/container 级沙箱、更完整复杂跨轮/cancel lifecycle events、未来真实 MCP/file/code/computer-use 执行 lifecycle 和完整 model profile/context optimization；当前已有默认关闭的 YAML `static_response` / `external_command` executor 首切、`external_command` opt-in working directory/absolute command/allowed_command_dirs/reject_root 轻量进程隔离首切、Monitor 配置摘要 API、validate-only、安全 overlay 持久化到 `app_settings`、runtime executor registry 热更新和 Audit 页面状态/enable 控件、profile max output/token-budget auto compact estimator adapter 扩展边界、provider `/tokenize` 自动选择首切、默认保守计数 fallback、普通 function call argument streaming 首切、client-owned `function_call_output` continuation 的 stream 回归覆盖、已注册 executor 的 stream tool loop started/done/failed item 首切、同一轮 registered function + hosted web_search mixed success stream 回归覆盖、同一轮 registered function 完成后 hosted web_search provider 失败的 completed/failed item 顺序覆盖，以及 unsupported hosted tool rejected audit；同一轮多个已注册 executor call 的 stream started/done 顺序和第二个 call 失败时的 completed/failed done 顺序已有 runtime 单测覆盖。
- 完整真实 stream/compact execution event 写入和所有未来 analytics 查询的 Postgres 兼容审计；当前已覆盖 `request_audits`、内部 `upstream_exchanges` correlation、request/model_call/hosted web_search started/completed/failed/cancelled、普通 function tool requested/submitted、incremental stream fallback、deferred/incremental stream started/completed、stream tool loop `stream=true` 标记等最小 `execution_events`，核心查询服务/Monitor API/MCP/UI 查询，Postgres `db migrate up`/`auth migrate up` 的 versioned SQL 应用路径，application store 的 open-vs-migrate 分离，以及 migrated logs/observation/finding/analysis/system-event、eval dataset list/detail/example/run/score、experiment read model、monitor core list/aggregate、session/overview、upstream/routing、model catalog/detail 和 channel usage analytics 代表路径的 Postgres raw SQL 兼容；新增或更深 analytics 查询和独立 auth migration namespace 仍未完成。SQLite application versioned migration 不是当前生产边界，SQLite 保留为 startup-schema fallback。
- provider probe 的完整配置/Monitor 工作流；当前已有手动 `provider probe` 诊断建议、只读 `provider probe-report` / Monitor `/api/provider-probe/report` 批量报告入口、默认关闭的启动时保守补全首切，以及 Monitor provider create preview/detail report 展示、`/api/provider-setup/validate` + `/api/provider-setup/apply` 首切、显式 Apply suggestions 首切、Providers 列表页 Batch probe and apply 首切和 CLI `provider probe-apply` 首切；尚未完成更完整的批量 provider onboarding、自动修复策略和跨协议消息转换。
