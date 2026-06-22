# Responses Gateway 重构路线图

本文记录从当前实现继续推进 Responses gateway 化的阶段边界。事实能力以 `CURRENT_IMPLEMENTATION.md` 和 `PROJECT_BASELINE.md` 为准；完整目标设计见 `RESPONSES_SERVER_DESIGN.md`。

## 目标边界

TraceLab 的新定位是 production-grade LLM gateway：

- 继续保留现有 OpenAI-compatible/Anthropic/Gemini/Vertex proxy、record、replay 能力，`.http` cassette 仍是回放和详情的事实源。
- 当下游调用 Responses API 且上游是 OpenAI-compatible Chat Completions 时，由 TraceLab 本地 runtime 承担 Responses API server 语义，而不是透传给上游。
- Postgres 是生产部署主路径；SQLite 保留为 local-first fallback 和测试路径。
- Hosted tools、compact、audit、streaming、model profiles 都属于 Responses runtime 的服务端语义，不应侵入普通代理热路径。

## 当前阶段基线

截至 2026-06-23，已落地：

- Responses server-mode over Chat Completions upstream。
- ent-backed Responses runtime store，SQLite fallback 和 Postgres checked-in application migrations。
- request audits、execution events、upstream exchange correlation、Monitor/MCP audit trace 查询。
- hosted `web_search` 非流式 tool loop。
- 普通 function tool 的 requested/submitted continuation。
- Chat Completions SSE cassette 记录与聚合。
- deferred Responses SSE envelope、incremental fallback 审计事件，以及简单文本输出的真实增量 Responses streaming 首切。
- context cancellation audit。
- 显式 `/v1/responses/compact` 和 item-count 自动 compact 阈值。

尚未作为基线能力：

- tool/auto-compact 等复杂场景的 Responses SSE 真实边读边转发。
- model profile 驱动的完整 context optimization；当前已有可注入 estimator 边界、adapter-backed estimator 层、默认确定性保守计数器，以及 HTTP provider `/tokenize` chat prompt counter 的自动选择首切。
- provider detection 的完整配置/Monitor 工作流；当前已有手动 `provider probe` 诊断建议、只读批量 `provider probe-report` / Monitor report API，以及默认关闭的启动时保守补全开关。
- server-side function executor 的更强隔离；当前已有默认关闭的 YAML `static_response` / `external_command` executor 首切、`external_command` opt-in working directory/absolute command/allowed_command_dirs/reject_root 轻量进程隔离首切、Monitor validate-only、安全 overlay 持久化到 `app_settings`，以及当前进程 runtime executor registry 热更新首切。
- provider setup 的完整持久化配置工作流；当前已有手动 `provider probe` 诊断建议、只读批量 `provider probe-report` / Monitor report API、默认关闭的启动时保守补全开关、provider setup validate/apply 首切，以及 Monitor create dialog 的 validate -> review normalized config/probe/secret state -> create 状态编排首切。
- SQLite 版本化迁移、auth 独立 Postgres namespace、剩余 raw SQL 方言审计。

## 阶段计划

### Stage 20：Runtime Streaming 首切（已落地）

目标：让 Responses server-mode 在 `stream:true` 时从内部 Chat Completions SSE 增量生成下游 Responses SSE delta，同时完成后仍存完整 response。

依赖：Stage 18B 的 Chat SSE 聚合与 cassette 记录。

验收：

- 本地 httptest upstream 能验证 `response.created`、`response.output_text.delta`、`response.completed` 的真实增量顺序。
- 完成后 `responses` / `response_items` 可查到完整结果。
- 非流式路径、record/replay cassette 和现有 deferred stream 行为不回退。

当前状态：简单文本输出路径已落地；普通 `function` tool call argument 分片会输出 `response.function_call_arguments.delta/done`。内部 Chat Completions upstream cancel 传播已落地，并会记录 cancelled request/model_call/upstream_exchange。已注册 server-side function executor 的 stream 首切会输出 arguments delta/done、执行 executor 前输出 started 态 `response.output_item.added` tool item、成功后输出完成态 `response.output_item.done`、把 tool output 注入下一轮模型上下文并继续流式输出最终文本，executor tool_call started/completed/failed events 会带 `stream=true`。同一轮多个已注册 executor call 会按模型 tool call 顺序逐个输出 started/done；若后续 call 执行失败，已完成 call 保留 completed done，当前失败 call best-effort 输出 failed done 并返回原错误。provider 就绪的 hosted `web_search` 也已接入 stream tool loop，会输出 arguments delta/done、执行 server-side search 前输出 started 态 `response.output_item.added` `web_search_call` item、成功后输出完成态 `response.output_item.done`、注入结果并继续最终文本 delta，tool_call started/completed/failed events 会带 `stream=true`。stream tool loop 在已输出 started item 后遇到 executor/provider 错误时，会 best-effort 输出 `status=failed` 的 `response.output_item.done`，再返回原错误；已写出 SSE 后的 runtime 错误会尽量追加最小 `response.failed` SSE，并保留现有 audit failed/cancelled 记录；未写出 SSE 的错误仍走原 HTTP error 或 deferred fallback 行为。需要 auto compact 等复杂路径仍会 fallback 到 deferred SSE；更复杂的跨轮/混合工具 failed lifecycle 仍未完成。

### Stage 21：Model Profile 与 Context Budget（estimator adapter 与可注入 `/tokenize` counter 已落地）

目标：把 compact 阈值、上下文窗口、输出上限、上游模型名映射等配置收敛到 model profile。当前已允许 profile 覆盖 item-count compact 阈值、`upstream_model`、默认 `max_output_tokens`，并用 `context_window_tokens` 的可注入 estimator 触发 auto compact；默认 estimator 现在通过 adapter-backed chat prompt counter 包装确定性保守计数器，adapter 失败会 fallback 到 conservative estimator。HTTP provider `/tokenize` chat prompt counter 已能在 profile 有 `name` 或 `pattern`、配置 `context_window_tokens`、未显式 `tokenize_counter.enabled=false`，且匹配 upstream/router target 声明 `capabilities.tokenize=true` 时自动接入默认 proxy/runtime 装配；显式 `enabled=true` 仍可强制启用，显式 false 可关闭。后续再做更完整的上下文优化。

依赖：Stage 19B 的自动 compact。

验收：

- 全局 compact 阈值继续兼容。
- model profile 匹配当前 model 时覆盖 compact 阈值。
- profile `max_output_tokens` 在客户端未显式传值时作为内部 Chat Completions `max_tokens` 默认值。
- `context_window_tokens` 超预算时写 `response.compact` `auto_triggered` 事件，details 标明 `trigger=context_window_tokens`、估算输入 tokens、窗口和预留输出。
- 文档明确当前 token budgeting 已有 estimator/adapter 扩展边界和可注入 `/tokenize` counter，默认仍是确定性保守计数，不是自动启用的模型专用 tokenizer。

### Stage 22：Persistence Operability 收敛

目标：继续补 Postgres-first 的运维可解释性，同时保留 SQLite fallback。

优先切片：

- `db migrate status` / dry-run 明确区分 application DB、auth DB、Postgres checked-in SQL 和 SQLite fallback。
- `auth migrate status` / dry-run 明确报告 auth migration source、scope、namespace，以及 Postgres 当前复用 application `schema_migrations` / `ent/postgres-migrations` 的 shared namespace 约束。
- 已为 SQLite fallback 增加非破坏性的 `app_schema_status` application schema marker；`db migrate status --check-db` 会只读报告 marker version 和核心应用表完整性，旧 SQLite DB 无 marker 仍兼容。

验收：

- 不依赖真实 Postgres 服务。
- 不破坏旧 SQLite DB 启动。
- 文档列清仍未完成的 auth namespace 与 SQLite versioned migration 缺口。

### Stage 23：Provider Detection 与 Capability Registry（诊断首切已落地）

目标：配置 provider 时，在显式 `api_type` 之外提供保守的探测/建议能力。探测只能辅助，不应覆盖用户显式配置。

依赖：现有 `api_type` / `mode` / capabilities 路由约束。

验收：

- 支持 OpenAI-compatible Chat Completions、Responses-native、Anthropic Messages、Gemini 的最小 endpoint/capability 探测。
- 失败时不阻塞启动，可在诊断命令或 probe report 中暴露。

当前状态：已新增 `internal/providerprobe` 和 `provider probe` CLI，可对配置中的 upstream 做 endpoint/capability 诊断并输出建议；`provider probe-report` 是只读批量报告入口，面向 YAML upstream 列表输出 report，不写配置。serve 侧已有默认关闭的 `provider_probe.startup_fill` 首切；开启后只填补 YAML upstream 中缺失的 `api_type`、`protocol_family` 和未声明 capability，不写回配置，也不覆盖显式配置。Monitor provider create dialog 已有临时 preview endpoint，不落库返回同类 report；`POST /api/provider-setup/validate` 已提供 provider setup wizard 首切，会组合 base URL、API key、provider preset、model discovery 和 capability 字段返回 provider probe、归一化配置和 redacted secret state，不落库且不回显 API key；create dialog 已收敛为 validate -> review normalized config/probe/secret state -> create 的状态流，字段变更会清空旧验证结果，probe detected 或显式 `api_type` + `protocol_family` 时才允许创建。`POST /api/provider-setup/apply` 复用归一化逻辑；当 probe 检测成功，或用户显式提供 `api_type` 与 `protocol_family` 时，才写入 channel store。setup 建议只填补缺失字段或未声明 capability，不覆盖显式 `api_type`、`protocol_family` 或 capability false。`POST /api/provider-probe/report` 会面向 SQLite channel 列表返回批量 detection report，不写 probe run、model 或 channel 配置；provider detail 的 probe 动作也已接入 report 展示，用户点击 Apply suggestions 后才会把建议的 `api_type`、`protocol_family` 和 capability bool 写入表单或 channel 配置。Monitor Providers 列表页已有 Batch probe and apply 入口：必须先调用只读 report 预览，再对 detected 且有可补字段的建议调用 `POST /api/provider-probe/report/apply`；请求体沿用 `{"channel_id":"..."}`，空对象表示全部，不携带 API key，语义是只填缺失 `api_type`、`protocol_family` 和未设置 capability，不覆盖显式配置或 capability false。

### Stage 24：Tool Execution 扩展（registry、YAML static_response 与 external_command 首切已落地）

目标：在 hosted tools 之外，为 server-side function executor 提供受控扩展点。

依赖：function tool requested/submitted 基线和 audit events。

验收：

- executor registry 默认空，不改变普通 function tool 客户端回路。
- 开启 executor 后写 tool_call started/completed/failed events。
- 明确超时、错误、结果大小和敏感信息处理边界。

当前状态：runtime 已新增默认空 server-side function executor registry。调用方显式注册同名 executor 后，非流式 runtime 会自动执行该 function tool、把输出注入下一轮模型上下文，并写 started/completed/failed events；未注册 tool 仍走客户端 `function_call_output` 回路。配置 `responses_server.function_executors.enabled=true` 并声明可用的 `static_response` 或 `external_command` executor 后，proxy 装配会注册同名受控 executor；首切策略支持 timeout、max-result-bytes、audit arguments/output redaction，默认关闭。`external_command` 不使用 shell，通过 stdin JSON 传入 tool call，默认不继承环境变量，仅支持显式 static env / env allowlist，stdout 作为 tool output，stderr 只进入失败摘要并受截断上限保护；现在还支持 opt-in 的 `process.working_dir` 执行目录限制、`process.require_absolute_command` 绝对 command 校验、`process.allowed_command_dirs` command 目录 allowlist 和 `process.reject_root` root 运行拒绝，未配置时保持既有行为。配置规范化会标记 binding `available` 与 validation warnings，Monitor API 会返回支持类型、可用状态和 warning，不返回 `static_response` output 或 `external_command` command；POST 写入口支持 validate-only 和安全 overlay apply，只接受非敏感有限字段，不覆写 YAML 中的 command/output/env，`validate_only=false` 会把 overlay 持久化到 `app_settings` 并热更新当前进程 proxy runtime executor registry，后续新请求生效。已注册 server-side function executor 在 `stream:true` 下已有首切 tool loop：参数分片继续 streaming，executor 执行前输出 started 态 `response.output_item.added` tool item，执行成功后输出完成态 `response.output_item.done` tool item，进入下一轮内部 Chat Completions，并继续输出最终文本 delta；同一轮多个已注册 executor call 会按模型 tool call 顺序输出 started/done；执行失败时会 best-effort 输出 failed `response.output_item.done`，再保留原错误链路。对应 tool_call started/completed/failed events 会标记 `stream=true`。root/container 级 executor 沙箱（轻量 process policy 已有 allowed_command_dirs/reject_root 首切）和更复杂的跨轮/混合工具 failed lifecycle 仍未完成。

## 并行开发规则

- Streaming、model profile、migration operability 可以并行；三者写入模块应尽量分离。
- 所有 worker 使用独立 git worktree 和分支提交。
- 已按 operability 小切片 -> model profile -> streaming -> provider probe -> executor registry -> provider probe 启动保守补全 -> function argument streaming 首切 -> 内部 upstream cancel 传播 -> incremental stream fallback audit -> YAML `static_response` executor 配置 -> profile token-budget 保守估算与 estimator 边界 -> provider detection Monitor preview/report/apply 首切 -> hosted `web_search` stream tool loop 首切 -> 最小 tool output item done SSE 顺序合入 -> 最小 tool output item added started SSE 顺序合入 -> 已写出 SSE 后最小 `response.failed` 顺序合入 -> function executor validation/Monitor 摘要 -> YAML `external_command` executor 首切 -> stream tool failed output item done -> token estimator adapter 层 -> 可注入 provider `/tokenize` counter -> auth migration status reporting -> provider `/tokenize` opt-in runtime 装配 -> `external_command` opt-in process working directory/absolute command/allowed_command_dirs/reject_root 轻量隔离首切 -> Monitor function executor validate-only/当前进程 overlay apply 首切 -> provider setup validate/apply 首切 -> stream multi-tool lifecycle 顺序覆盖首切 -> provider setup wizard 状态编排首切 -> provider `/tokenize` 自动选择首切 -> function executor 安全 overlay 持久化/runtime registry 热更新首切 -> external_command allowed_command_dirs/reject_root 轻量 sandbox policy 首切。后续优先接 root/container 级 executor 沙箱（轻量 process policy 已有 allowed_command_dirs/reject_root 首切）、更完整 provider 配置持久化/批量 setup 工作流、完整 context optimization 和更复杂的 stream tool lifecycle。
- 每个阶段合入后必须更新 `CURRENT_IMPLEMENTATION.md`、`PROJECT_BASELINE.md` 和必要的设计文档，不能只改代码。
