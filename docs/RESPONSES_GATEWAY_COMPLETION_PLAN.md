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
- Chat Completions SSE cassette 记录与聚合，Responses streaming 覆盖简单文本、function arguments、registered executor 和 hosted `web_search` 的首切路径。
- Hosted `web_search`、server-side function executor registry、YAML `static_response` / `external_command` opt-in executor 和轻量 process policy。
- ent-backed Responses store、SQLite fallback raw DDL、Postgres checked-in application migrations、open-vs-migrate 分离。
- `request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`，以及 CLI/Monitor/MCP 查询首切。
- Provider `api_type` / `mode` / capabilities 路由约束，provider probe/report/apply 和 setup validate/apply 首切。
- Codex fixture offline gate、Codex config suggestion、doctor/config inspect/audit operability 首切。

这些能力说明项目已经从“只做代理”进入了“Responses semantic server + proxy/record/replay 并存”的阶段，但还不是完整 production-grade gateway。

## 剩余主线

### Runtime 主线

目标：补齐 Responses semantic runtime 的核心行为，而不是继续增加外围诊断。

必须完成：

- Compact v2/context optimization：summary provenance、source/retained item refs、retention window、artifact-bearing item 策略和可查询 read model。
- Auto compact streaming：避免复杂路径长期 fallback 到 deferred envelope，至少明确哪些路径可真实增量、哪些路径显式 fallback 并可审计。
- Stream/tool lifecycle：补跨轮、混合工具、部分成功后失败、cancel/error 的稳定 event ordering 和 final response 对齐。
- Tool ownership boundary：普通 `function` 默认 client-owned，server-side executor 必须显式 opt-in；MCP/file/code/computer-use 必须先有安全设计，不直接执行任意外部能力。

近期可并行切片：

- `runtime/compact-v2-provenance`：实现 compact metadata/read model 的 item refs 和 retained window，不输出 raw prompt/summary/tool args。
- `runtime/stream-fallback-contract`：为 auto compact/tool loop fallback 增加稳定 contract tests，明确 fallback 条件和 audit event。

### Storage 主线

目标：让 Postgres-first 成为清晰的生产路径，同时保持 SQLite fallback/replay 兼容。

必须完成：

- Postgres application migration 状态、down/dry-run、required table health 和 schema namespace 的生产说明继续保持机器可读。
- SQLite fallback 策略定案：要么实现 versioned migration，要么明确长期只作为 startup schema fallback，并把 repair/upgrade 边界写入 docs/CLI。
- Auth namespace 定案：当前 Postgres auth 复用 application migration namespace；如果要拆独立 namespace，需要迁移方案、dry-run/status、回滚边界和测试。
- 审计更深 raw SQL 兼容：analytics/eval/experiment/monitor 查询中仍依赖 SQLite 方言的路径要继续审计。

近期可并行切片：

- `storage/sqlite-policy-finalization`：将 SQLite fallback 的长期策略、operator advice 和 docs 收敛为明确决策。
- `storage/auth-namespace-design`：产出独立 auth namespace 的迁移设计，只有设计和 dry-run/status 语义通过后再实施。

### Provider 主线

目标：让上游 provider onboarding 支撑 Responses server-mode 的生产使用。

必须完成：

- Provider API surface/capability registry 成为 routing、doctor、setup、models/codex-config 的共同事实来源。
- Probe/setup/apply 形成闭环：只填缺失字段，不覆盖显式配置或 capability false，不回显 secret。
- Model profile 与 provider/channel/model catalog 的关系明确：context window、max output、tool capability、upstream model rewrite、tokenize capability 的来源可解释。
- Native Responses provider 的模式边界明确：proxy pass-through、record-only 和 local semantic interposition 不能混淆。

近期可并行切片：

- `provider/profile-source-unification`：明确 `responses_server.model_profiles`、channel catalog 和 provider capability 的优先级，并补诊断输出。
- `provider/native-responses-mode-boundary`：写清并测试 native Responses target 在 proxy/server mode 下的 routing 行为。

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
2. Storage：SQLite fallback 与 auth namespace 的生产边界定案。

Provider 主线在上述两个任务启动后并行评审，避免 runtime/storage 事实源继续漂移。
