# Monitor 使用指南

Monitor 是 TraceLab 的本地 Web 工作台，由 `internal/monitor` 提供 API 与前端静态资源，和代理运行在同一个进程里（`monitor.port`）。它面向三个场景：

- 查看真实 LLM HTTP 请求与原始协议。
- 分析 session、模型、模型服务商、路由和失败。
- 管理模型服务商、模型、路由设置、分析任务与个人 API token。

## 启动与登录

配置 `monitor.port` 后，`serve` 启动时会同时挂载管理端口，浏览器打开 `http://localhost:<monitor.port>` 即可进入 Monitor。

首次部署需要创建用户。tracked 默认配置 `config/config.yaml` 使用 Postgres 且 `database.dsn` 为空，运行前需导出 `LLM_TRACELAB_DATABASE_DSN`；纯本地运行可改用 `config/examples/local-sqlite.yaml`：

```bash
export LLM_TRACELAB_DATABASE_DSN='postgres://user:pass@host:5432/llm_tracelab?sslmode=disable'
go run ./cmd/server auth init-user -c config/config.yaml --username admin --password 'change-me-123'
```

Monitor 使用用户名密码登录（`POST /api/auth/login`），成功后签发仅用于 Monitor 的 JWT：issuer 为 `llm-tracelab-monitor`，audience 为 `llm-tracelab-monitor-ui`，TTL 默认 24 小时，可用 `auth.session_ttl` 调整。前端把 JWT 存在浏览器 localStorage，并以 `Authorization: Bearer` 访问 Monitor API；该 JWT 不用于 SDK、proxy 或 MCP。

右上角账号菜单提供偏好设置（语言、主题，保存在当前浏览器）、修改密码（`POST /api/auth/password`）和退出登录。`serve` 总是挂载 auth store，所以 `/api/auth/status` 返回 `auth_required: true`；只有在没有挂载 auth store 的嵌入式/测试场景下才返回 `false`，此时前端以 local 用户直接进入。

## 个人 API token

「令牌」页面（`/api/auth/tokens`）管理当前用户的个人 API token：创建、撤销、删除，并显示前缀、TTL、过期时间和最后使用时间。token 只在创建时显示一次，之后只保留 prefix。

个人 token 的用途：

- 代理 API：`Authorization: Bearer <token>`，proxy 的每个请求都要通过该 token 鉴权。
- MCP：`Authorization: Bearer <token>`。

Monitor API 本身只接受登录 JWT，不使用个人 token。

## 数据来源

Monitor 同时使用两类数据：

- application database（生产环境为 Postgres，本地 fallback 为 SQLite，默认文件 `{{output_dir}}/llm_tracelab.sqlite3`）：trace 索引、session、列表/过滤/分页/聚合、模型服务商与模型配置、模型别名、路由设置、事件、analysis run/job、Responses 状态和 audit 表。
- raw `.http` cassette：trace 详情、raw protocol、路由事件和 replay-safe 检查的事实来源。

因此列表页只读数据库索引、响应很快，详情页仍能回到原始 HTTP 证据。`GET /api/routing/summary` 是二者的组合：按数据库索引顺序扫描 trace，再读取每个 cassette prelude 中的路由事件做聚合（旧数据或缺少事件的文件单独计数）。

## 页面

左侧主导航固定为 11 项。下面按导航顺序说明路由与用途。

### 概览 `/overview`

时间窗口内的请求量、成功/失败数、Token、延迟、发现项和系统事件概览，以及派生数据健康度（已解析/未解析 trace、解析队列、失败的分析任务）。另有趋势图与主要拆分：端点、上游、路由失败、发现类别。概览每 60 秒自动刷新。

### 事件 `/events`

TraceLab 自身的事件收件箱：

- 来源：`parser`、`analyzer`、`router`、`upstream`。
- 类别：`parse_failure`、`analysis_failure`、`analysis_job_failure`、`routing_failure`、`transport_error`。
- 状态：`unread`、`read`、`resolved`、`ignored`。
- 级别过滤提供 `critical`、`error`、`warning`、`info`。

支持按状态、级别、来源过滤和搜索 fingerprint / trace / model / 消息，支持单条标记已读、解决、忽略，以及「全部标为已读」。重复事件按 fingerprint 合并。导航上的未读角标由 `GET /api/events/summary` 与 SSE `GET /api/events/stream` 驱动。

### 会话 `/sessions`

按 session 聚合最近 50 个会话，展示健康度、成功率、模型、模型服务商、流式标记与耗时。session ID 的抽取顺序为：

1. `Session-Id` / `Session_id`
2. `X-Claude-Code-Session-Id`
3. `X-Codex-Turn-Metadata` 中的 `session_id`
4. `X-Codex-Window-Id` 的前缀

适合分析 Codex、Claude Code 等 agent 在一轮任务中多次模型调用的整体行为。

### 手动导出会话轨迹

会话详情页提供 `Export trajectory (ATIF)` 按钮。点击后，服务从该会话的客户端可见 cassette 重建轨迹并下载 `session-<id>.atif.jsonl`，不会调用模型、修改 cassette 或自动提交分析任务。

- 格式固定为 **ATIF-v1.7**；每个 JSONL 行是完整 trajectory。单会话下载只有一行，并以换行结束，可拼接成多会话数据集。
- 当前语义重建支持 Codex 使用的 OpenAI Responses generation（`/responses`、`/v1/responses`）及 SSE。其他 endpoint（包括 compact）保留为带原始 body 的扩展记录，并报告 `unsupported_endpoint`；不伪装成已解析的对话。
- 请求按 `recorded_at` 与 trace ID 排序，输出按 Responses `output_index` 排序。会话归组依据保存在 `extra.session_source`，无法从 HTTP 证明全部事件的因果关系或任务已完成。
- 相邻请求的历史上下文按有序重叠合并，不全局删除相同文本。`previous_response_id` 请求按增量输入处理。工具结果按调用 ID 回挂到发起调用的步骤。
- 多模态或未知内容以文本 JSON 与 `extra.native_item` 保留，不生成外部附件。reasoning summary/encrypted content 保留其原始类型，不当作完整可读思维链。
- 每步携带 `trace_id`、origin 与归一化 item 路径（流式输出先重建）；`extra.exchanges` 保留请求配置（含 tools）、usage、HTTP 状态和响应状态。内部 model child exchanges 不重复计入这份客户端视角导出。
- `extra.warnings` 报告文件缺失、无法解析、上下文不连续、缺失或孤立工具结果、流式中断等问题，下载完成后页面显示警告数量。`completion=unknown` 不将 HTTP 成功解释为任务成功。
- 导出以一次查询得到的请求集合为快照，生成期间新增请求不进入本次文件。当前为同步生成并在浏览器下载，极大会话受服务端与浏览器可用内存限制。

接口为 `GET /api/sessions/:sessionID/trajectory`，使用现有 Monitor JWT 登录鉴权，返回 `application/x-ndjson` 和附件下载头。无请求的会话返回 404。导出不依赖异步 Observation 是否已生成，而是从 V2/V3 cassette 重建。

### 追踪 `/traces`

逐请求 trace 列表，支持过滤与分页，展示 endpoint、model、状态码、duration、TTFT、token。可以进入 trace detail，也可以跳到对应的模型、模型服务商或路由上下文。列表数据来自应用库索引。

### 审计 `/audit`

审计页面有两个面板：

- 最近发现项（`GET /api/findings`）：按类别和级别（`critical`、`high`、`medium`、`low`）过滤，可跳转到对应 trace 的审计或协议视图。
- 请求链路：输入 `response_id` 或 `request_audit_id` 加载本地 Responses runtime 的 request audit、execution events 与 upstream exchanges（`GET /api/responses/audit/trace`）；`GET /api/responses/audit/tool-calls` 提供工具调用审计。

页面同时展示当前进程的 server-side function executor 状态面板（见下文）。

### 模型 `/models`

按模型查看时间窗口内的流量：模型覆盖哪些模型服务商，以及请求数、错误数、Token 与趋势。详情路由为 `/models/:model`。

### 模型服务商 `/providers`

管理上游模型服务商。支持创建与编辑 provider preset、base URL、API key、headers、routing 字段，以及 API surface：`api_type`、`mode`、Responses/Chat Completions/tool calling/models 等 capability，以及模型级 `supports_responses` / `supports_chat_completions` 覆盖（模型级声明优先于 provider 级配置）。

创建前可以先做探测：

- `POST /api/provider-probe` 返回只读的 Detect provider 建议，不落库。
- `POST /api/provider-setup/validate` 组合 base URL、API key、preset、模型发现与 capability 字段做一次探测，并把归一化配置写回表单，但不落库；响应不回显 API key，只返回 `api_key_hint`、secret storage mode 和脱敏 header 状态。表单字段变化会清空旧验证结果。
- `POST /api/provider-setup/apply` 才真正把配置写入 store。
- 列表页的 Batch probe and apply 先跑 `POST /api/provider-probe/report` 预览，再用 `POST /api/provider-probe/report/apply` 对 detected 且存在可补字段的 provider 批量应用；批量应用只补缺失的 `api_type`、`protocol_family` 和未设置的 capability，不覆盖显式配置，也不覆盖显式 `false`。

还可以启停模型服务商、启停单个模型、探测模型、查看用量/Token/失败与 probe 结果。

配置归属：长期配置保存在 application store。YAML 只作为首次 bootstrap 输入，第一次数据库写入会记录 `app_settings` 的 `channels.initialized` 标记（`GET /api/settings/channels` 读取，`DELETE /api/settings/channels` 清除）；此后即使删光所有模型服务商，重启也不会重新导入 YAML。使用显式 `credentials` 列表的 YAML 配置仍由 YAML 管理，此时 Monitor 的模型服务商、模型和别名写操作返回 409。

### 连接 `/connect`

展示当前部署的 base URL 与三种协议入口，并给出可复制的 curl 示例：

- `/v1/chat/completions`（OpenAI-compatible）
- `/responses`（OpenAI Responses / Codex；`/v1/responses` 仍然支持）
- `/anthropic/messages`（Anthropic Messages / Claude Code）

### 路由 `/routing`

工作区包含四个标签页：

- Decisions：最近选中的路由，可按模型、通道/上游、状态、耗时、TTFT、Token 过滤。
- Settings：编辑路由设置（`PATCH /api/settings/routing`），包括 `responses_strategy`、`selection_policy`、`missing_model_policy`。
- Aliases：模型别名的增删改与校验（`/api/model-aliases`、`/api/model-aliases/validate`）。
- Inspector：`POST /api/routing/inspect` 预演某个请求/模型会如何被路由。

摘要面板来自 `GET /api/routing/summary`，按 failure reason、selected route target、credential 和 sticky 状态聚合，并区分有事件、旧数据/缺失事件和解析错误的 trace。

### 分析 `/analysis`

展示已持久化的 analysis run 与 analysis job 队列，并提供两个批量操作：

- 「修复分析数据」：对失败或未解析的 observation 重新解析。
- 「修复 Token 统计」：对缺失 usage 的记录重新抽取 token 统计。

两者都以异步 job 提交（`POST /api/analysis/batch/reanalyze`），不调用上游模型。

### 令牌 `/tokens`

管理当前用户的个人 API token，见上文「个人 API token」。

## 兼容路由与详情路由

- `/` 重定向到 `/overview`。
- `/requests` 与 `/traces` 渲染同一个页面（追踪）。
- `/channels` 重定向到 `/providers`；`/channels/:channelID` 仍渲染模型服务商详情页。
- 详情路由：`/traces/:traceID`、`/sessions/:sessionID`、`/models/:model`、`/providers/:providerID`、`/upstreams/:upstreamID`。

## Trace 详情

Reading guide 提供五个视图：

- Routing & Conversation：路由选择、prompt 消息、最终输出和 timeline 事件。
- Protocol：Observation IR 的语义节点、归一化类型、JSON 路径与原始 payload。
- Audit：确定性 findings、证据路径。
- Performance：延迟、TTFT、Token 吞吐、缓存比例、状态与路由上下文。
- Raw：原始 HTTP 请求/响应字节与 headers。

可执行动作：

- `Refresh analysis`（`POST /api/traces/:id/reanalyze`）：从本地 cassette 重新生成 Observation 和 findings，仅在自动处理异常或结果明显不对时使用。
- `Repair stats`（`POST /api/traces/:id/repair-usage`）：从本地响应重新抽取 usage/token 统计，仅在 token/cost 统计缺失或错误时使用。

Deep link 支持 query 参数 `tab`、`from_session`、`view`（`sessions` / `requests`）和 `focus`（`failure`、`timeline`、`timeline_error`、`request`、`response`）。当 trace 携带 Responses audit id，或后端能通过 `upstream_exchanges.trace_id` 反查到 audit id 时，Reading guide 会显示 `Responses audit` 入口，跳转到 `/audit` 中同一条请求链路。

## Responses function executors

`GET /api/responses/function-executors` 返回当前进程的 server-side function executor 摘要：`enabled`、`timeout`、`max_result_bytes`、`redaction.arguments`、`redaction.output`、`supported_types`（当前为 `static_response` 和 `external_command`），以及每个 executor 的 `name`、`type`、`enabled`、`available`、`process`、`output_configured`、`command_configured` 和 `warnings`。接口不返回 `static_response` 的 output，也不返回 `external_command` 的 command。

`POST /api/responses/function-executors` 是 Monitor 的写入口：默认 `validate_only=true`，只返回归一化摘要和 warnings；`validate_only=false` 时把非敏感 overlay 持久化到应用库 `app_settings`，并热更新当前进程的 executor registry，后续新请求生效。payload 只接受 `enabled`、`timeout`、`max_result_bytes`、`redaction.arguments`、`redaction.output`、executor 的 `name`/`type`/`enabled`，以及 `external_command` 的进程隔离字段（`process.working_dir`、`process.require_absolute_command`、`process.allowed_command_dirs`、`process.reject_root`）；`output`、`command`、`args`、`env` 等敏感可执行字段不被接受，也不会出现在响应或持久化 snapshot 中。

## 排障建议

请求失败时按顺序查看：

1. trace detail 的 Routing & Conversation 与 Raw。
2. Routing 页面的 Decisions，或 trace 中的 routing context。
3. Events 页面是否有 parser / analyzer / router / upstream 事件。
4. Models 与 Providers 页面确认模型启用状态和模型服务商健康。
5. 只有在派生结果明显不对时才运行 `Refresh analysis`；Token 统计异常时运行 `Repair stats`。

## 非目标与未实现

- 代理不做协议族之间的转换：转发路径是协议感知透传，唯一的例外是 `/v1/responses` 由本地 Responses runtime 编排为内部 `/v1/chat/completions` 调用。
- Monitor 没有用户管理界面：用户由 `auth init-user`、`auth reset-password`、`auth create-token` 等 CLI 命令创建和维护，界面只支持修改自己的密码。
- Monitor API 不接受个人 API token；个人 token 面向 proxy 与 MCP。
- Monitor 不能修改由 YAML 显式 `credentials` 管理的模型服务商、模型和别名，这类写操作返回 409。
- 写接口不接受 `output`、`command`、`args`、`env` 等敏感可执行字段，Monitor overlay 不能创建新的可执行 command。
- `external_command` 只有轻量进程约束（工作目录、绝对 command、允许目录、拒绝 root），不等同于容器或 namespace 沙箱。

## 相关文档

存储与部署 `./STORAGE_AND_DEPLOYMENT.md`、Postgres 运维 `./POSTGRES_OPERATIONS.md`、路由与凭据 `./ROUTING_AND_CREDENTIALS.md`、观测与审计 `./OBSERVATION_AND_AUDIT.md`、Responses runtime `./RESPONSES_RUNTIME.md`、MCP 使用 `./MCP_GUIDE.md`。

架构 `./ARCHITECTURE.md`、实现状态 `./IMPLEMENTATION_STATUS.md`、协议与模型服务商 `./PROTOCOLS_AND_PROVIDERS.md`、协议参考 `./protocol-reference/README.md`、代理接入示例 `./PROXY_USAGE_EXAMPLES.md`、开发命令 `./DEVELOPMENT.md`、文档总入口 `./README.md`。
