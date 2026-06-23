# Responses Gateway 完成计划

状态：收敛计划
日期：2026-06-23

本文用于把 Responses gateway 重构从“operability 小切片持续补齐”收敛回原始目标：把 `llm-tracelab` 升级为兼容既有 proxy/record/replay 的 production-grade LLM gateway，并吸收 `responses-gateway` 的核心 Responses API server 能力。

事实能力仍以 [当前实现概览](./CURRENT_IMPLEMENTATION.md)、[项目基线](./PROJECT_BASELINE.md) 和 [Responses Server 设计](./RESPONSES_SERVER_DESIGN.md) 为准；差距细节见 [Responses Gateway 差距清单](./RESPONSES_GATEWAY_GAP_ANALYSIS.md)。

## 完成定义

本轮重构完成时，至少应满足以下条件：

1. `responses_server.enabled=true` 时，TraceLab 能稳定作为 OpenAI Responses API semantic server 运行；当上游是 OpenAI-compatible Chat Completions 时，由本地 runtime 编排 model call、tool loop、compact、stream 和 audit，而不是把 `/v1/responses` 透传给上游。
2. 非 Responses 请求继续走现有 protocol-aware proxy、routing、recording、recordfile parser、Monitor/MCP 和 `pkg/replay` 路径；`.http` V2/V3 cassette 兼容性不被破坏。
3. Postgres 是生产部署主路径，应用 schema migration、runtime semantic state、audit/read model 和 operational status 有明确边界；SQLite 保留 local-first fallback，但不能伪装成已完成的生产级 versioned migration。
4. Provider 配置能明确表达 API surface、mode 和 capability；provider detection/onboarding 可以保守补齐缺失信息，但不能覆盖用户显式配置。
5. Hosted/server-side tools 的执行边界明确：已经实现的工具可审计、可查询、失败路径稳定；未实现的 MCP/file/code/computer-use 不伪造执行结果，必须先有安全边界再接入真实执行器。
6. 文档、CLI、doctor/audit/config inspect 只服务上述运行闭环；不再把零散 operability 增强作为独立主线无限延展。

## 当前完成状态

截至 2026-06-23，已完成的主线能力：

- Responses server-mode over Chat Completions upstream 首切。
- `previous_response_id` continuation、Responses runtime store、`/v1/responses/:id/input_items`。
- 显式 `/v1/responses/compact`、item-count auto compact、context-window token-budget auto compact 和 auto compact provenance 首切。
- Compact v2 provenance event-derived read model 首切：manual/auto compact events 引用 source/retained item refs、summary item ids、retained window 和 budget 白名单字段，`QueryService.ListCompactProvenance` 可查询且不输出 raw prompt/summary/tool args/tool output。
- Chat Completions SSE cassette 记录与聚合，Responses streaming 覆盖简单文本、client-owned `function_call_output` continuation、auto compact 后简单文本 continuation、auto compact 后未注册普通 `function` tool arguments、auto compact 后同名 registered executor stream tool loop、auto compact 后 provider 就绪 hosted `web_search` / `web_search_preview` 平凡 `tool_choice` tool loop、普通 function arguments、registered executor、hosted `web_search`、同轮 registered function + hosted `web_search` mixed success，以及同轮 registered function 完成后 hosted `web_search` provider 失败的 started/completed/failed item lifecycle 首切路径。
- Runtime incremental stream fallback contract 首切：不支持的工具组合，以及 auto compact 后未知/未实现 hosted 工具或非平凡 `tool_choice` 的组合，会在写出任何 SSE 或调用上游前返回可识别 fallback error，并带 deferred fallback reason；HTTP handler 可安全转入 deferred envelope 并写 fallback audit event。
- Tool ownership boundary 首切：普通 `function` 默认 client-owned；即使 registry 中存在其它 executor，未注册同名 executor 的 function call 也只返回给客户端，不触发 server-side execution。
- Hosted `web_search`、server-side function executor registry、YAML `static_response` / `external_command` opt-in executor 和轻量 process policy。
- ent-backed Responses store、SQLite fallback raw DDL、Postgres checked-in application migrations、open-vs-migrate 分离。
- `request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`，以及 CLI/Monitor/MCP 查询首切。
- Provider `api_type` / `mode` / capabilities 路由约束，provider probe/report/apply 和 setup validate/apply 首切。
- Native Responses provider mode boundary 首切：server-mode 的内部 Chat Completions backend 只选择 Chat Completions-compatible target，不会把 `responses_native` 且 `capabilities.chat_completions=false` 的 target 当作 `/v1/chat/completions` backend；server-mode 关闭时 `/v1/responses` 仍可选择 native Responses target 透传并录制。
- Codex fixture offline gate、Codex config suggestion、doctor/config inspect/audit operability 首切。
- `models codex-config` 已输出 provider/profile source-boundary diagnostics：runtime profile 事实源为 `responses_server.model_profiles`，catalog/channel 当前为 drift-only，provider/upstream capability 驱动 routing/tokenize。
- `models codex-config` 已输出 provider/channel profile adoption diagnostics：当前采纳阶段为 `observe_only`，冲突策略为 `responses_server.model_profiles_wins`，并已提供机器可读 `profile_adoption_report` dry-run/conflict/gate report。该报告只读展示 channel profile candidate、字段级 diff、显式 runtime profile 保护和 capability false 阻断，不把 catalog/channel profile 接入 runtime；报告内 `adoption_ready=false`，`rollback_plan` 已收敛为 mutation-free 机器可读 rollback contract，`schema_migration` 与 `dsn_gated_tests` 仍明确标记为 `blocking_not_implemented`，避免误读为已经可采纳。

这些能力说明项目已经从“只做代理”进入了“Responses semantic server + proxy/record/replay 并存”的阶段，但还不是完整 production-grade gateway。

## 剩余主线

### Runtime 主线

目标：补齐 Responses semantic runtime 的核心行为，而不是继续增加外围诊断。

必须完成：

- Compact v2/context optimization：summary provenance、source/retained item refs、retention window、artifact-bearing item 策略和可查询 read model。
- Auto compact streaming：简单文本 continuation、未注册普通 `function` tool arguments、同名 registered executor stream tool loop，以及 provider 就绪 hosted `web_search` / `web_search_preview` 且 `tool_choice` 为 nil/空/`none`/`auto` 的 tool loop，已能在自动 compact 后继续真实增量 streaming；未知/未实现 hosted 工具或非平凡 `tool_choice` 的 auto compact 组合仍显式 fallback 并可审计，后续需继续收敛复杂路径。
- Stream/tool lifecycle：client-owned `function_call_output` 的非流式与流式 continuation、同轮多个 registered executor、同轮 registered function + hosted `web_search` mixed success、同轮 registered function 完成后 hosted `web_search` provider 失败的 completed/failed item 顺序已有回归覆盖；仍需补复杂跨轮、auto compact 复杂工具组合、cancel/error 的稳定 event ordering 和 final response 对齐。
- Tool ownership boundary：普通 `function` 默认 client-owned 的首切已有 regression test；server-side executor 必须显式 opt-in；MCP/file/code/computer-use 必须先有安全设计，不直接执行任意外部能力。

近期可并行切片：

- `runtime/compact-v2-provenance`：实现 compact metadata/read model 的 item refs 和 retained window，不输出 raw prompt/summary/tool args。
- `runtime/stream-fallback-contract`：已完成首切，不支持工具组合、以及 auto compact 后未知/未实现 hosted 工具或非平凡 `tool_choice` 的 incremental stream fallback 有 runtime contract tests；client-owned `function_call_output` continuation 的 `stream:true` 路径、auto compact 后简单文本 continuation、未注册普通 `function` tool arguments、同名 registered executor stream tool loop、provider 就绪 hosted `web_search` / `web_search_preview` 平凡 `tool_choice` tool loop、同轮 registered function + hosted `web_search` mixed success，以及同轮 registered function 完成后 hosted `web_search` provider 失败的 completed/failed item lifecycle 已不再 fallback。HTTP handler 既有 fallback audit event 继续覆盖 deferred fallback。后续仍需减少复杂路径 fallback，并补复杂跨轮、复杂 auto compact 工具组合和 cancel lifecycle。

### Storage 主线

目标：让 Postgres-first 成为清晰的生产路径，同时保持 SQLite fallback/replay 兼容。

必须完成：

- Postgres application migration 状态、down/dry-run、required table health 和 schema namespace 的生产说明继续保持机器可读。
- SQLite fallback 策略已定案：本 Storage 边界长期保留 `startup_schema_fallback` 作为 local-first fallback，不在本轮实现 application versioned migrator；`db migrate status --check-db` 只读解释 marker/required table 状态，不承担 destructive repair，生产级 versioned migration 走 Postgres。
- Auth namespace 已定案：当前 Postgres auth 继续复用 application `schema_migrations` namespace；独立 auth namespace 不直接拆。`auth migrate status` / dry-run 已追加设计态机器可读字段，覆盖 adoption 仍未实现、未来 dry-run 必须 mutation-free、status 必须 read-only、当前 rollback scope 仍是 `shared_application_migration_set`、默认测试必须离线且 Postgres 检查 DSN-gated。真正拆分前还必须落地独立 migration source/namespace marker、idempotent adoption、auth-only rollback 和测试门禁。
- 审计更深 raw SQL 兼容：已补 Monitor session/overview、upstream/routing analytics、model catalog/detail analytics、channel usage summary/trends/model usage/recent failures 的 Postgres DSN-gated 覆盖；其余 monitor/analytics 查询中仍依赖 SQLite 方言的路径要继续审计。

近期可并行切片：

- `storage/postgres-runtime-sql-coverage`：继续补 analytics/eval/experiment/monitor 查询的 Postgres DSN-gated 覆盖，默认测试保持离线；已覆盖 session list/detail、overview summary 的 provider aggregation 和 stream-count 布尔聚合、upstream/routing analytics、model catalog/detail analytics，以及 channel usage summary/trends/model usage/recent failures。
- `storage/auth-namespace-adoption-design`：已收敛为文档与 operator 输出合同，不改变当前 shared namespace 行为；后续实施任务必须按 adoption、dry-run/status、rollback 和测试门禁逐项落地。

### Provider 主线

目标：让上游 provider onboarding 支撑 Responses server-mode 的生产使用。

必须完成：

- Provider API surface/capability registry 成为 routing、doctor、setup、models/codex-config 的共同事实来源。
- Probe/setup/apply 形成闭环：只填缺失字段，不覆盖显式配置或 capability false，不回显 secret。
- Model profile 与 provider/channel/model catalog 的关系明确：context window、max output、tool capability、upstream model rewrite、tokenize capability 的来源可解释。
- Native Responses provider 的模式边界已完成首切：默认 proxy/record-only 路径可以把 `/v1/responses` 原样转发并录制到 native Responses upstream；`responses_server.enabled=true` 时，配置的 Responses path 由本地 semantic runtime 接管，runtime 内部模型调用仍只选择 Chat Completions-compatible target，不能把显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 provider 当作 `/v1/chat/completions` upstream 使用。

近期可并行切片：

- `provider/profile-source-unification`：已完成 source-boundary 和 adoption-boundary 首切，`models codex-config` 会输出 `runtime_profile_source`、`profile_precedence`、`catalog_profile_role`、`capability_source`、`provider_channel_profile_adoption`、`profile_adoption_report`、`profile_conflict_strategy` 和 `profile_adoption_required_gates`；当前 report 仅 observe-only dry-run，不覆盖显式配置或 capability false，并在 report 内把 rollback plan 固化为 mutation-free contract，schema migration 与 DSN-gated tests 仍是 blocking gates。后续仍需实现 catalog/channel profile 进入 runtime 前的 migration/test 闭环。
- `provider/native-responses-mode-boundary`：已补 router/proxy e2e 契约，覆盖 native Responses target 在 proxy/server mode 下的 routing 行为；后续 provider 主线聚焦 profile adoption migration/test 闭环。

## 停止发散规则

以下工作暂不作为独立主线继续开任务，除非直接服务 Runtime、Storage 或 Provider 三条主线：

- 新增 doctor check，但不改变 runtime/storage/provider 完成状态。
- 新增 audit CLI 展示字段，但不补真实 runtime lifecycle、storage read model 或 provider boundary。
- 新增 Monitor UI 小控件，但不改变 gateway 运行闭环。
- 新增 Codex/Codex TOML 辅助输出，但不补 Responses server 兼容性或 provider/profile 事实源。
- 新增 config inspect 字段，但不服务生产部署决策。

## 阶段门禁

每个阶段合入前必须满足：

- worker 使用独立 git worktree/branch，负责人 review 后 `--no-ff` 合入并清理 worktree/branch。
- 不编辑 `ent/dao/**`；生成代码只能通过生成链路更新。
- 不破坏 `.http` V2/V3、`pkg/replay`、普通 proxy 热路径。
- 测试默认不依赖网络或真实 Postgres；Postgres 覆盖必须 env-gated。
- 至少运行相关 targeted tests、`rtk git diff --check`，阶段收束时运行 `rtk env -u GOROOT task check:quick`。
- 合入后同步 `CURRENT_IMPLEMENTATION.md`、`PROJECT_BASELINE.md` 和相关设计/路线文档。

## 下一阶段建议

下一阶段不再继续 doctor/audit/config inspect 小切片，优先启动两个正交任务：

1. Runtime：compact v2 provenance/read model 设计与首切实现。
2. Storage：基于已定案的 SQLite fallback/shared auth namespace 边界，继续做 Postgres runtime SQL 覆盖和 auth namespace adoption 设计。

Provider 主线在上述两个任务启动后并行评审，避免 runtime/storage 事实源继续漂移。
