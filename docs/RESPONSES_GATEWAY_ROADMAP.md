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
- deferred Responses SSE envelope，以及简单文本输出的真实增量 Responses streaming 首切。
- context cancellation audit。
- 显式 `/v1/responses/compact` 和 item-count 自动 compact 阈值。

尚未作为基线能力：

- tool/auto-compact 等复杂场景的 Responses SSE 真实边读边转发。
- model profile 驱动的 context window/token budgeting。
- provider detection 的完整配置/Monitor 工作流；当前已有手动 `provider probe` 诊断建议，以及默认关闭的启动时保守补全开关。
- server-side function executor 配置化；当前已有默认空 registry 代码扩展点。
- SQLite 版本化迁移、auth 独立 Postgres namespace/rollback、剩余 raw SQL 方言审计。

## 阶段计划

### Stage 20：Runtime Streaming 首切（已落地）

目标：让 Responses server-mode 在 `stream:true` 时从内部 Chat Completions SSE 增量生成下游 Responses SSE delta，同时完成后仍存完整 response。

依赖：Stage 18B 的 Chat SSE 聚合与 cassette 记录。

验收：

- 本地 httptest upstream 能验证 `response.created`、`response.output_text.delta`、`response.completed` 的真实增量顺序。
- 完成后 `responses` / `response_items` 可查到完整结果。
- 非流式路径、record/replay cassette 和现有 deferred stream 行为不回退。

当前状态：简单文本输出路径已落地；普通 `function` tool call argument 分片会输出 `response.function_call_arguments.delta/done`。内部 Chat Completions upstream cancel 传播已落地，并会记录 cancelled request/model_call/upstream_exchange。hosted tools、server-side tool execution、需要 auto compact 等复杂路径仍 fallback 到 deferred SSE。后续工作是 hosted/server-side tool streaming 和已写出 SSE 后的失败事件细化。

### Stage 21：Model Profile 与 Context Budget 骨架

目标：把 compact 阈值、上下文窗口、输出上限、上游模型名映射等配置收敛到 model profile。首切只允许 profile 覆盖 item-count compact 阈值，不实现 token estimator。

依赖：Stage 19B 的自动 compact。

验收：

- 全局 compact 阈值继续兼容。
- model profile 匹配当前 model 时覆盖 compact 阈值。
- 文档明确 token budgeting 仍未完成。

### Stage 22：Persistence Operability 收敛

目标：继续补 Postgres-first 的运维可解释性，同时保留 SQLite fallback。

优先切片：

- `db migrate status` / dry-run 明确区分 application DB、auth DB、Postgres checked-in SQL 和 SQLite fallback。
- 或为 SQLite fallback 增加非破坏性的 schema version/status marker。

验收：

- 不依赖真实 Postgres 服务。
- 不破坏旧 SQLite DB 启动。
- 文档列清仍未完成的 auth rollback/namespace 与 SQLite versioned migration 缺口。

### Stage 23：Provider Detection 与 Capability Registry（诊断首切已落地）

目标：配置 provider 时，在显式 `api_type` 之外提供保守的探测/建议能力。探测只能辅助，不应覆盖用户显式配置。

依赖：现有 `api_type` / `mode` / capabilities 路由约束。

验收：

- 支持 OpenAI-compatible Chat Completions、Responses-native、Anthropic Messages、Gemini 的最小 endpoint/capability 探测。
- 失败时不阻塞启动，可在诊断命令或 probe report 中暴露。

当前状态：已新增 `internal/providerprobe` 和 `provider probe` CLI，可对配置中的 upstream 做 endpoint/capability 诊断并输出建议。serve 侧已有默认关闭的 `provider_probe.startup_fill` 首切；开启后只填补 YAML upstream 中缺失的 `api_type`、`protocol_family` 和未声明 capability，不写回配置，也不覆盖显式配置。后续可把 probe report 接入 Monitor/provider setup flow。

### Stage 24：Tool Execution 扩展（registry 首切已落地）

目标：在 hosted tools 之外，为 server-side function executor 提供受控扩展点。

依赖：function tool requested/submitted 基线和 audit events。

验收：

- executor registry 默认空，不改变普通 function tool 客户端回路。
- 开启 executor 后写 tool_call started/completed/failed events。
- 明确超时、错误、结果大小和敏感信息处理边界。

当前状态：runtime 已新增默认空 server-side function executor registry。调用方显式注册同名 executor 后，非流式 runtime 会自动执行该 function tool、把输出注入下一轮模型上下文，并写 started/completed/failed events；未注册 tool 仍走客户端 `function_call_output` 回路。普通 function call argument streaming 已有首切；YAML/Monitor 配置、超时/隔离策略和 server-side tool streaming events 仍未完成。

## 并行开发规则

- Streaming、model profile、migration operability 可以并行；三者写入模块应尽量分离。
- 所有 worker 使用独立 git worktree 和分支提交。
- 已按 operability 小切片 -> model profile -> streaming -> provider probe -> executor registry -> provider probe 启动保守补全 -> function argument streaming 首切 -> 内部 upstream cancel 传播顺序合入。后续优先补 hosted/server-side tool streaming，再做 executor 配置化和 provider detection 的 Monitor/setup flow 集成。
- 每个阶段合入后必须更新 `CURRENT_IMPLEMENTATION.md`、`PROJECT_BASELINE.md` 和必要的设计文档，不能只改代码。
