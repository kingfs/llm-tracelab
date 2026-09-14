# 架构与代码地图

## 项目定位

`llm-tracelab` 是本地优先（local-first）的 LLM API record/replay 代理，覆盖 OpenAI-compatible 及 Anthropic Messages、Google Gemini、Vertex native 等主流协议族。典型用法：

1. 开发期让 SDK 流量经过本地代理；
2. 把原始 HTTP 交换持久化为 `.http` cassette；
3. 在单元测试中用 `pkg/replay` 回放 cassette，不访问上游模型。

目标是可靠测试、降低 API 成本、快速排障。生产形态同时是 Postgres-first 网关：承载渠道/模型路由、本地 Responses 运行时、审计与 Monitor 查询。

架构原则：

- 转发路径保持简单可靠，不在热路径做深度协议解析。
- raw cassette 是 replay 与详情页的事实源。
- 派生结果（usage、timeline、Observation IR、findings）可失败、可重试、可从 cassette 重算。
- 测试回放不依赖网络。

文档入口见 [文档总入口](./README.md)，当前能力清单见 [实现状态](./IMPLEMENTATION_STATUS.md)。

## 代码地图

顶层目录与包职责：

- `cmd/server`：CLI 入口与 `serve` 装配。`main.go` 只通过 `run` 退出，命令接线在 `root.go`；命令文件包括 `serve.go`、`migrate.go`、`db.go`、`config.go`、`doctor.go`、`provider.go`、`models.go`、`tools.go`、`audit.go`、`auth.go`、`analyze.go`、`version.go`、`schema.go`、`completion.go`。`management.go` 提供 serve 与测试共用的管理 HTTP/MCP 装配，`provider_startup_probe.go` 提供 provider probe 辅助。
- `internal/proxy`：反向代理与 `/v1/responses` 入口，负责鉴权、转发、响应截获与协议感知透传调整。
- `internal/recorder`：cassette 写入与 metadata finalization。
- `internal/store`：application DB（Postgres/SQLite）schema 初始化、索引、查询与派生状态，并拥有 `ConfigurationTransaction`。
- `internal/upstream`：上游解析、协议族、能力/路由 profile 与鉴权/URL 规则。
- `internal/channel`：渠道（provider）配置与 probe 用例、legacy YAML bootstrap、runtime targets。
- `internal/responses`：本地 Responses 运行时及其子系统——`runtime`（编排与 state store）、`httpapi`（HTTP handler）、`chatclient`（内部 Chat Completions client）、`audit`（审计查询与写入）、`functionexec`（本地函数执行器）、`tools`（hosted tools：`hosted`、`mcp`、`websearch`）、`protocol`（类型定义）、`codexfixtures`（Codex 兼容 fixture）。
- `internal/router`：多上游选择、健康、重试、sticky、决策记录与路由快照发布。
- `internal/routeplan`：入口 endpoint、执行模式与 `responses_strategy` 的路由决策计划。
- `internal/appdbmigrate`：应用 Postgres checked-in migration 的加载与应用。
- `internal/monitor`：Monitor API 与内嵌 React UI（`go:embed ui/dist/*`）。
- `internal/mcpserver`：受限 MCP 工具层。
- `internal/analyzer`：确定性 detector，产出 Observation findings。
- `internal/observeworker`：从 cassette 解析 Observation IR 并落库的后台 worker。
- `internal/reanalysis`：trace/session/batch 重分析任务（service 与 worker）。
- `internal/sessionanalysis`：session 级聚合分析。
- `internal/providerprobe`：provider 能力探测。
- `internal/evals`：基于 replay 的确定性 eval 基线执行（HTTP 状态、TTFT/token 预算、tool-call 校验等）。
- `internal/migrate`：把旧 V2 cassette 转换为 V3，并可选重建索引行。
- `internal/auth`：认证 user/token 与 JWT。
- `internal/config`：YAML/env 配置装载（含 legacy bootstrap 输入）。
- `internal/limit`：请求限流；`internal/redaction`：URL 与敏感参数脱敏；`internal/chaos`：配置驱动的故障注入（延迟/错误）。
- `pkg/recordfile`：V2/V3 cassette 解析与写入。
- `pkg/replay`：测试回放 transport。
- `pkg/llm`：协议识别、adapter、usage 归一化与 stream `ResponsePipeline`。
- `pkg/observe`：Observation IR 定义与各协议 parser。
- `ent/schema` 与 `ent/postgres-migrations`：ent 表结构定义与 checked-in Postgres migration。
- `web/monitor-ui`：Monitor 前端源码（React + Vite，Playwright 测试）。

协议族与 provider 细节见 [协议与 Provider](./PROTOCOLS_AND_PROVIDERS.md)，路由与凭据规则见 [路由与凭据](./ROUTING_AND_CREDENTIALS.md)。

## 数据流

请求转发 → 录制 → 解析 → 索引 → 展示：

1. SDK、CLI 或应用把 LLM API 请求发到本地代理（如 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`）。
2. proxy 归一化入口 endpoint，并按请求路径与上游配置识别协议族。
3. routeplan/router 依据入口、模型、能力与 `responses_strategy` 选择 route target，并记录决策与候选。
4. proxy 做少量透传型调整，例如为 OpenAI-compatible chat stream 注入 `stream_options.include_usage=true`。
5. 请求转发到上游，响应以流式或非流式返回 client。
6. recorder 把原始 HTTP 请求/响应写入 `.http` cassette，并在 prelude 追加 meta 与 event。
7. `pkg/llm.ResponsePipeline` 从响应流中抽取 usage 与 `llm.*` timeline 事件。
8. application DB 写入 trace、路由、usage、session、upstream 等索引字段；生产部署使用 Postgres。
9. observeworker 与 reanalysis 从 raw cassette 解析 Observation IR、findings 和分析任务结果。
10. Monitor 与 MCP 从 application DB 查询列表/聚合，从 cassette 读取详情。
11. 单元测试通过 `pkg/replay.Transport` 从 cassette 回放响应。

Timeline 事件（如 `llm.output_text.delta`、`llm.reasoning.delta`、`llm.tool_call`、`llm.usage`、`routing.selection`、`routing.filtered`、`routing.retry_candidate`、`routing.failure`）写入 cassette prelude，并被 Monitor/MCP 用于 trace 详情、路由排障和失败聚类。

## 协议边界

当前是协议族感知的透传代理，不是跨协议转换网关。已实现协议族与 endpoint 覆盖见 [协议参考](./protocol-reference/README.md)。

- `openai_compatible`、`anthropic_messages`、`google_genai`、`vertex_native` 在转发热路径中互不转换；上游必须支持 client 实际请求的 endpoint（例如 Claude Code 的 `/v1/messages` 需要 Anthropic Messages 兼容上游）。
- `pkg/llm`/`pkg/observe` 可以把不同协议归一化为统一摘要与 Observation IR，但这不等于可以无损转发到另一个协议。
- proxy 无条件接受 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`。

`/v1/responses` 是唯一例外，按请求在 native 与本地 runtime 之间二选一：

- native：匹配到支持 Responses 的上游时直接透传。
- 本地 runtime：把请求编排为一次内部上游 `/v1/chat/completions` 调用（`responses_server` 执行模式）。该模式始终可用且没有配置开关；旧的 `responses_server.enabled` 与 `LLM_TRACELAB_RESPONSES_ENABLED` 已移除。本地 runtime 延迟构建，构建失败只影响触发该次构建的请求，不阻塞启动。
- 策略存于 application DB 的 `app_settings` 键 `routing.settings`，经 `GET`/`PATCH /api/settings/routing` 读写，不是 YAML 键；取值为 `auto`、`prefer_native`、`prefer_local_server`、`native_only`、`local_server_only`。
- native 与 local 的选择按模型解析而非按渠道：`channel_models.supports_responses` / `supports_chat_completions`（Monitor UI 可编辑）覆盖渠道级 `api_type`/`capabilities`，未声明值的模型回落到渠道级行为；YAML 侧对应 `upstream.model_capabilities`。

本地 Responses 运行时的编排、审计与 hosted tools 细节见 [Responses 运行时](./RESPONSES_RUNTIME.md)。

## 录制格式

新录制只写 `LLM_PROXY_V3`：

1. 以 `# llm-tracelab/v3` 开头的短 prelude；
2. 一行 `# meta: {...}` JSON；
3. 零或多行 `# event: {...}` JSON；
4. 一个空行；
5. 原始 HTTP 请求字节；
6. 一个分隔换行；
7. 原始 HTTP 响应字节。

读取端继续支持 legacy `LLM_PROXY_V2`（固定 2KB JSON header block）。cassette 保持人类可读；修改格式时先改 `pkg/recordfile`，再同步 recorder、monitor、replay。

## 存储边界

raw `.http` cassette 是事实源：

- replay 事实源；
- raw protocol 详情源；
- Observation IR / findings / usage repair 的重建来源。

application DB 是结构化查询源：

- trace/session/upstream/model/channel 的列表、聚合、过滤与分页；
- auth user/token、channel/model 配置、system events、analysis jobs、Observation IR、findings、eval；
- Responses semantic state（`responses`、`response_items`）与 Responses audit（`request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`）。

生产必须使用 Postgres，checked-in migrations 位于 `ent/postgres-migrations`（`db migrate up`）。SQLite 仅作为本地/开发/测试 fallback，默认文件为 `{{output_dir}}/llm_tracelab.sqlite3`，其 schema 在启动时应用而非版本化迁移。列表页不得依赖扫描文件系统；replay 不得依赖 SQLite 或网络。

部署与迁移细节见 [存储与部署](./STORAGE_AND_DEPLOYMENT.md)，Postgres 长期运行优化见 [Postgres 运维](./POSTGRES_OPERATIONS.md)。

## 并发与一致性

所有管理写入（渠道、模型、别名、provider setup 与 probe apply）共用一个 `store.ConfigurationTransaction`：

- 持进程级配置锁，按 `configMu` -> `upstreamMu` -> `reloadMu` -> `router.mu` 的顺序取锁，并使用单个 SQL 事务。
- 回调必须显式 commit，其它退出路径回滚。
- 只有 commit 成功后才发布新路由快照，运行中的快照绝不描述数据库拒绝的配置。
- 事务内不得再开事务（会返回 `store.ErrNestedTransaction`），也不得新增 `reloadMu` -> `upstreamMu` 的反向获取。

`ConfigurationTransaction` 是 `upstream_targets`/`upstream_models` 的唯一写入方，因此全程持有 upstream 写锁。后台刷新与代理侧 `RefreshNow` 只以 best-effort 方式获取该锁：配置变更持锁时跳过落库但仍更新内存，并在获锁后按当前 live target 集合过滤，避免为已删除的 target 复活行。

渠道配置的 DB 优先规则：YAML `upstream`/`upstreams` 只是首次 bootstrap 输入；首次数据库写入记录 `app_settings` 键 `channels.initialized`，此后 DB 是路由配置来源，即使全部渠道停用或删除也不回退 YAML。`GET /api/settings/channels` 报告该标记，`DELETE /api/settings/channels` 仅清除标记（只有在 DB 中确实没有渠道时，下次启动才会重新导入）。带显式 `credentials` 列表的 YAML 配置保持 YAML 管理，并拒绝 Monitor 的渠道/模型/别名写入（返回 409）。

## 事实源边界

代码是最终事实源，其次是 `AGENTS.md`；本文档只描述“当前为真”的行为，不承载计划、里程碑或历史方案。

- 结构化查询以 application DB 为准；replay 与 raw 详情以 `.http` 为准。
- 不要让列表页扫描 raw 文件，也不要让 replay 依赖数据库或网络。
- 修改 record format、storage schema、Monitor API 或 MCP 工具面时，必须同步更新对应文档与测试。
- 用户面向入口尽量收敛为“刷新分析”和“修复统计”；`analysis_jobs` 的 job 类型是审计与兼容层面的实现细节。

## 兼容性要求

- 新录制只写 V3；读取端继续支持 V2，cassette 保持人类可读。
- `pkg/replay` 是硬性要求，且不能依赖网络或 Observation IR。
- 存储 schema 只做 additive 演进；新列需通过启动时迁移兼容旧 DB（Postgres 走 `internal/appdbmigrate`，SQLite 走启动 schema）。
- 旧本地 SQLite 应用库（如更早的 `trace_index.sqlite3`）与当前默认 `llm_tracelab.sqlite3` 必须可原地升级。
- Observation parser 对 unknown fields 保持 tolerant；所有派生分析结果都必须能从 raw cassette 重算。

## 测试基线

测试分层：

- `pkg/recordfile`、`pkg/replay`、`pkg/llm`、`pkg/observe`：格式、回放、协议归一化与 Observation IR 的单元基线。
- `internal/proxy`、`internal/router`、`internal/store`、`internal/monitor`：转发、路由、存储与 API 的集成基线。
- `web/monitor-ui`：Playwright 前端测试。
- `unittest/` 与 `tests/fixtures/`：cassette 回放与跨协议矩阵 fixture。

常用入口（命令矩阵见 [开发指南](./DEVELOPMENT.md)）：

```bash
task check:quick
go test ./pkg/recordfile ./pkg/replay ./pkg/llm ./pkg/observe
go test ./internal/proxy ./internal/router ./internal/store ./internal/monitor
```

改动 streaming、router 或 store 并发时补：

```bash
task test:race
```

前端改动补：

```bash
task ui:build
task ui:test
go test ./internal/monitor
```

Monitor 与 MCP 的用户面说明见 [Monitor 指南](./MONITOR_GUIDE.md)、[MCP 指南](./MCP_GUIDE.md)，代理接入示例见 [代理使用示例](./PROXY_USAGE_EXAMPLES.md)，观测/审计模型见 [观测与审计](./OBSERVATION_AND_AUDIT.md)。
