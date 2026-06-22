# Responses Gateway 重构路线图

本文记录从当前实现继续推进 Responses gateway 化的阶段边界。事实能力以 `CURRENT_IMPLEMENTATION.md` 和 `PROJECT_BASELINE.md` 为准；完整目标设计见 `RESPONSES_SERVER_DESIGN.md`。

## 目标边界

TraceLab 的新定位是 production-grade LLM gateway：

- 继续保留现有 OpenAI-compatible/Anthropic/Gemini/Vertex proxy、record、replay 能力，`.http` cassette 仍是回放和详情的事实源。
- 当下游调用 Responses API 且上游是 OpenAI-compatible Chat Completions 时，由 TraceLab 本地 runtime 承担 Responses API server 语义，而不是透传给上游。
- Postgres 是生产部署主路径；SQLite 保留为 local-first fallback 和测试路径。
- Hosted tools、compact、audit、streaming、model profiles 都属于 Responses runtime 的服务端语义，不应侵入普通代理热路径。

## 当前阶段基线

截至 2026-06-22，已落地：

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
- model profile 驱动的模型专用 tokenizer 与完整 context optimization。
- provider detection 的完整配置/Monitor 工作流；当前已有手动 `provider probe` 诊断建议、只读批量 `provider probe-report` / Monitor report API，以及默认关闭的启动时保守补全开关。
- server-side function executor 的 Monitor/外部 executor 配置化；当前已有默认关闭的 YAML `static_response` executor 首切。
- SQLite 版本化迁移、auth 独立 Postgres namespace/rollback、剩余 raw SQL 方言审计。

## 阶段计划

### Stage 20：Runtime Streaming 首切（已落地）

目标：让 Responses server-mode 在 `stream:true` 时从内部 Chat Completions SSE 增量生成下游 Responses SSE delta，同时完成后仍存完整 response。

依赖：Stage 18B 的 Chat SSE 聚合与 cassette 记录。

验收：

- 本地 httptest upstream 能验证 `response.created`、`response.output_text.delta`、`response.completed` 的真实增量顺序。
- 完成后 `responses` / `response_items` 可查到完整结果。
- 非流式路径、record/replay cassette 和现有 deferred stream 行为不回退。

当前状态：简单文本输出路径已落地；普通 `function` tool call argument 分片会输出 `response.function_call_arguments.delta/done`。内部 Chat Completions upstream cancel 传播已落地，并会记录 cancelled request/model_call/upstream_exchange。已注册 server-side function executor 的 stream 首切会输出 arguments delta/done、执行 executor、把 tool output 注入下一轮模型上下文并继续流式输出最终文本。hosted tools、需要 auto compact 等复杂路径仍会 fallback 到 deferred SSE；完整细粒度 tool lifecycle streaming events 仍未完成。后续工作是 hosted/server-side tool streaming 事件细化和已写出 SSE 后的失败事件细化。

### Stage 21：Model Profile 与 Context Budget（保守估算首切已落地）

目标：把 compact 阈值、上下文窗口、输出上限、上游模型名映射等配置收敛到 model profile。当前已允许 profile 覆盖 item-count compact 阈值、`upstream_model`、默认 `max_output_tokens`，并用 `context_window_tokens` 的保守字符估算触发 auto compact；后续再接模型专用 tokenizer 和更完整的上下文优化。

依赖：Stage 19B 的自动 compact。

验收：

- 全局 compact 阈值继续兼容。
- model profile 匹配当前 model 时覆盖 compact 阈值。
- profile `max_output_tokens` 在客户端未显式传值时作为内部 Chat Completions `max_tokens` 默认值。
- `context_window_tokens` 超预算时写 `response.compact` `auto_triggered` 事件，details 标明 `trigger=context_window_tokens`、估算输入 tokens、窗口和预留输出。
- 文档明确当前 token budgeting 是保守估算，不是模型专用 tokenizer。

### Stage 22：Persistence Operability 收敛

目标：继续补 Postgres-first 的运维可解释性，同时保留 SQLite fallback。

优先切片：

- `db migrate status` / dry-run 明确区分 application DB、auth DB、Postgres checked-in SQL 和 SQLite fallback。
- 或为 SQLite fallback 增加非破坏性的 schema version/status marker。

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

当前状态：已新增 `internal/providerprobe` 和 `provider probe` CLI，可对配置中的 upstream 做 endpoint/capability 诊断并输出建议；`provider probe-report` 是只读批量报告入口，面向 YAML upstream 列表输出 report，不写配置。serve 侧已有默认关闭的 `provider_probe.startup_fill` 首切；开启后只填补 YAML upstream 中缺失的 `api_type`、`protocol_family` 和未声明 capability，不写回配置，也不覆盖显式配置。Monitor provider create dialog 已有临时 preview endpoint，不落库返回同类 report；`POST /api/provider-probe/report` 会面向 SQLite channel 列表返回批量 detection report，不写 probe run、model 或 channel 配置；provider detail 的 probe 动作也已接入 report 展示，用户点击 Apply suggestions 后才会把建议的 `api_type`、`protocol_family` 和 capability bool 写入表单或 channel 配置。后续仍需更完整的 provider setup wizard。

### Stage 24：Tool Execution 扩展（registry 与 YAML static_response 首切已落地）

目标：在 hosted tools 之外，为 server-side function executor 提供受控扩展点。

依赖：function tool requested/submitted 基线和 audit events。

验收：

- executor registry 默认空，不改变普通 function tool 客户端回路。
- 开启 executor 后写 tool_call started/completed/failed events。
- 明确超时、错误、结果大小和敏感信息处理边界。

当前状态：runtime 已新增默认空 server-side function executor registry。调用方显式注册同名 executor 后，非流式 runtime 会自动执行该 function tool、把输出注入下一轮模型上下文，并写 started/completed/failed events；未注册 tool 仍走客户端 `function_call_output` 回路。配置 `responses_server.function_executors.enabled=true` 并声明 `static_response` executor 后，proxy 装配会注册同名受控 executor；首切策略支持 timeout、max-result-bytes、audit arguments/output redaction，默认关闭。已注册 server-side function executor 在 `stream:true` 下已有首切 tool loop：参数分片继续 streaming，executor 执行后进入下一轮内部 Chat Completions，并继续输出最终文本 delta。Monitor 已有只读 API 和 Audit 页面状态面板；Monitor 写配置、外部 executor 隔离策略和完整细粒度 server-side tool streaming lifecycle events 仍未完成。

## 并行开发规则

- Streaming、model profile、migration operability 可以并行；三者写入模块应尽量分离。
- 所有 worker 使用独立 git worktree 和分支提交。
- 已按 operability 小切片 -> model profile -> streaming -> provider probe -> executor registry -> provider probe 启动保守补全 -> function argument streaming 首切 -> 内部 upstream cancel 传播 -> incremental stream fallback audit -> YAML `static_response` executor 配置 -> profile token-budget 保守估算 -> provider detection Monitor preview/report/apply 首切顺序合入。后续优先补 hosted/server-side tool streaming，再做外部 executor/Monitor 配置和更完整的 provider setup wizard。
- 每个阶段合入后必须更新 `CURRENT_IMPLEMENTATION.md`、`PROJECT_BASELINE.md` 和必要的设计文档，不能只改代码。
