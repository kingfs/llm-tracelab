# Monitor 使用指南

Monitor 是 TraceLab 的本地 Web 工作台。

它面向三个主要场景：

- 查看真实 LLM HTTP 请求。
- 分析 session、模型、渠道、路由和失败。
- 管理 token、渠道、模型启停和重分析任务。

## 登录与 Token

首次部署需要创建用户。`config/config.yaml` 使用 Postgres 且 `database.dsn` 为空，
运行前需导出 `LLM_TRACELAB_DATABASE_DSN`；纯本地运行可改用
`config/examples/local-sqlite.yaml`：

```bash
export LLM_TRACELAB_DATABASE_DSN='postgres://user:pass@host:5432/llm_tracelab?sslmode=disable'
go run ./cmd/server auth init-user -c config/config.yaml --username admin --password 'change-me-123'
```

Monitor UI 使用用户名密码登录，登录态由 monitor-only JWT 承载。JWT 只用于
Monitor API，不用于 SDK、proxy 或 MCP。

登录后可在 `Tokens` 页面创建个人 API token。

个人 API token 用于：

- 代理 API：`Authorization: Bearer <token>`。
- MCP：`Authorization: Bearer <token>`。

## 数据来源

Monitor 使用两类数据：

- application database（生产环境为 Postgres，本地 fallback 为 SQLite）：列表、过滤、分页、聚合、渠道/模型配置、事件和派生分析。
- raw `.http` cassette：trace 详情、raw protocol、replay-safe 检查。

因此列表页快速，详情页仍能回到原始 HTTP 证据。

## 页面

### Overview

用于查看系统健康概览：

- 请求量。
- 失败量。
- token 和 latency 汇总。
- Observation 状态。
- system event 摘要。

### Traces

逐请求 trace 列表。

适合：

- 查单次 HTTP 交换。
- 看 endpoint、model、状态码、duration、TTFT、token。
- 进入 trace detail。
- 跳转到模型、渠道或路由上下文。

### Sessions

按 session 聚合请求。

当前 session 提取顺序：

1. `Session_id`
2. `X-Claude-Code-Session-Id`
3. `X-Codex-Turn-Metadata.session_id`
4. `X-Codex-Window-Id` 中 `:` 前缀
5. 空 session

适合分析 Codex、Claude Code 和其他 agent 一轮任务中多次模型调用的整体行为。

### Models

按模型查看流量。

适合：

- 查看时间窗口内有流量的模型。
- 查看模型覆盖哪些渠道。
- 比较请求数、错误数、token、趋势。
- 定位模型相关失败。

### Providers

管理上游渠道。页面路由是 `/providers`；旧路由 `/channels` 会重定向到 `/providers`。

支持：

- 创建渠道。
- 设置 provider preset、base URL、API key、headers、routing 字段。
- 设置 provider API surface：`api_type`、`mode`，以及 Responses、Chat Completions、tool calling、models 等 capability 开关；这些字段会写入 channel store，并在运行时还原为 upstream routing target。
- 创建前可用 Detect provider 做临时探测，不落库返回 API surface 建议；需要采用建议时，使用 Apply suggestions 显式写入表单。
- 创建前可用 Validate setup 调用 provider setup validate：它会组合 base URL、API key、provider preset、model discovery 和 capability 字段做一次探测并把归一化配置写回表单，但不会落库；dialog 会展示 normalized config、probe 和 redacted secret state，字段变更会清空旧验证结果；Create provider 通过 setup apply 写入 channel store；显式提供协议信息时也允许直接创建，若 probe 未检测成功，需显式提供 `api_type` 与 `protocol_family`。
- setup validate/apply 响应不会回显 API key；只返回 `api_key_hint`、secret storage mode 和 redacted header 状态。
- 探测模型，并查看 provider detection 建议；需要写回建议时，使用 Apply suggestions 显式更新 channel 配置。
- 在 Providers 列表页使用 Batch probe and apply：先运行只读 `POST /api/provider-probe/report` 预览，再通过 `POST /api/provider-probe/report/apply` 对 detected 且有可补字段的 provider 批量应用建议；单个 provider 的探测入口在卡片上。Monitor 不展示或传递 API key。批量应用只填缺失的 `api_type`、`protocol_family` 和未设置 capability，不覆盖显式配置，也不覆盖显式 `false` capability。
- 启停渠道。
- 启停单个模型。
- 查看渠道用量、token、失败和 probe 结果。

长期渠道配置保存在 application store；Postgres 部署使用版本化迁移，SQLite 仍作为本地 fallback。通常 YAML 只作为启动和首次 bootstrap 输入；首次初始化会在应用库 `app_settings` 写入 `channels.initialized` 标记（`GET /api/settings/channels` 报告，`DELETE /api/settings/channels` 清除），即使后来停用或删除全部渠道，重启也不会重新导入 YAML。清除该标记只是重新放行 YAML bootstrap：只要数据库里仍有渠道配置，数据库依旧是路由配置来源；只有数据库确实为空时，下次启动才重新导入 YAML。使用显式 `credentials` 列表的 YAML 配置仍由 YAML 管理，此模式下 Monitor 的渠道、模型和别名写操作返回 409，避免数据库操作替换 YAML 中的凭据路由。

### 上游和模型的启停语义

- 创建上游只保存连接配置。创建成功后进入详情页，发现或手动添加模型，再选择启用。没有启用模型且未允许未知模型时，不接受命名模型请求。
- 上游启用表示允许参与新请求路由；停用会移除该上游的路由资格，但保留各模型的选择。已在途请求不会因此被取消。
- 模型启用只作用于当前上游，不会启动或停止远端模型进程，也不会改变同名模型在其他上游的配置。
- 模型停用是明确拒绝：自动发现、未知模型放行和 fallback 策略均不能重新放行该上游上的已停用模型。删除模型则是移除配置，不等同于停用；允许未知模型时，删除后的模型可能再次作为未知模型被调用。
- 模型发现默认不启用新模型，保留已有模型的启停选择和能力配置。API 调用方可显式传入 `enable_discovered: true` 启用本次新发现的模型。数据库管理的运行时只从已启用配置建立模型目录，不通过周期刷新扩大准入范围。
- 配置开关与健康状态相互独立。上游停用时，模型行仍保留“已启用”的选择，同时提示路由被上游开关阻止；健康检查和熔断仍可能使已启用模型暂时不可用。
- 仅来自历史请求的模型行不提供启停开关；需要管理时先手动添加模型。发现后停用的模型不再被标为“新模型”。
- 渠道、模型、模型别名及批量修改通过同一数据库事务完成。运行时配置准备成功后才提交并切换路由；校验、批量更新或提交失败时保留原配置和原路由。返回失败的模型探测仍保留诊断记录。


### Connect

应用内连接指引，展示当前部署的 base URL 和各协议入口的 curl 示例：

- OpenAI-compatible Chat Completions：`/v1/chat/completions`。
- OpenAI Responses / Codex：`/responses`。
- Anthropic Messages / Claude Code：`/anthropic/messages`。

适合把客户端接入 TraceLab 时快速复制 base URL、endpoint 和请求示例。

### Routing

查看 selected route 和路由事件。

适合：

- 确认某个模型请求由哪个渠道处理。
- 排查为什么请求失败或被重试。
- 查看 sticky routing、candidate、failure reason。
- 从路由记录跳到 trace detail。

`GET /api/routing/summary` 会读取 V3 cassette prelude 中的路由事件，并按 failure reason、selected route、sticky 状态等聚合。

### Responses Function Executors API

`GET /api/responses/function-executors` 返回当前进程中的 server-side function executor 摘要；Audit 页面会读取该接口并展示状态面板。

Audit 页面也可以按 `response_id` 或 `request_audit_id` 查询本地 Responses runtime 的 request audit、execution events 和 upstream exchanges。Trace detail 如果携带对应 Responses audit id，或后端能通过 `upstream_exchanges.trace_id` 反查到 audit id，会在 Reading guide 中显示 `Responses audit` 入口，直接跳转到 `/audit` 的同一条 request lineage。

返回内容包括：

- `enabled`、`timeout`、`max_result_bytes`。
- `redaction.arguments`、`redaction.output`。
- `supported_types`，当前为 `["static_response", "external_command"]`。
- `executors[]` 中的 `name`、`type`、`enabled`、`available`、`process`、`output_configured`、`command_configured` 和 `warnings`。
- `warnings`，例如开启 executor 但未配置任何 binding、没有可用 executor、重复 name、空 name、未知 type 或 `external_command` 缺少 command。

该 API 不返回 `static_response` 的 output 内容，也不返回 `external_command` 的 command 内容。`POST /api/responses/function-executors` 是保守的 Monitor 写配置入口：默认 `validate_only=true`，返回 normalized summary 和 warnings；`validate_only=false` 时会把非敏感 overlay 持久化到应用库 `app_settings`，并热更新当前进程的 Responses runtime executor registry，后续新请求生效。写入 payload 只接受 `enabled`、`timeout`、`max_result_bytes`、`redaction.arguments`、`redaction.output`、executor `name` / `type` / `enabled`，以及 `external_command` 的 `process.working_dir` / `process.require_absolute_command` / `process.allowed_command_dirs` / `process.reject_root` 隔离字段；`output`、`command`、`args`、`env` 等敏感可执行字段不被写接口接受，响应和持久化 snapshot 中也不会回显或保存。

`external_command` 必须通过 YAML 显式配置真实 command，运行时不使用 shell，默认不继承环境变量，tool call 输入通过 stdin JSON 传入命令。Monitor overlay 只能调整开关、全局策略和进程隔离字段，不能创建新的可执行 command。可选 `process.working_dir` 会要求绝对且已存在的执行目录；可选 `process.require_absolute_command=true` 会拒绝相对 command/PATH 查找；可选 `process.allowed_command_dirs` 会要求 command 为绝对路径并解析到允许目录内；可选 `process.reject_root=true` 会在当前进程以 root 运行时拒绝执行 external command。相关危险配置会以 validation warning 形式出现在摘要中。当前这些能力是轻量进程约束，不等同于容器或 root namespace 沙箱。

### Events

TraceLab 自身事件收件箱。

事件类型包括：

- parser failure。
- analysis failure。
- routing selection failure。
- upstream transport error。

状态：

- `unread`
- `read`
- `resolved`
- `ignored`

重复事件按 fingerprint 合并。

### Analysis

查看分析和重分析任务。

支持：

- trace/session analysis runs。
- analysis jobs。
- refresh analysis：重新生成本地派生分析数据。
- repair token stats：从本地响应重新抽取 usage/token 统计。

这些操作不调用上游模型。

### Tokens

管理当前用户 API token。

token 创建后只显示一次，请妥善保存。

## Trace Detail

trace detail 用于查看单条请求的完整上下文。

常用视图：

- Timeline。
- Summary。
- Raw Protocol。
- Declared Tools。
- Observation。
- Findings。

可执行动作：

- `Refresh analysis`：从本地 cassette 重新生成 Observation 和 findings。仅在自动处理异常或结果明显不对时使用。
- `Repair stats`：从本地响应重新抽取 usage/token 统计。仅在 token/cost 统计缺失或错误时使用。

## Deep Link

Trace detail 支持 query 参数定位：

- `tab`
- `from_session`
- `view`
- `focus`

常见 focus：

- `failure`
- `response`
- `timeline`
- `timeline_error`

## 排障建议

请求失败时建议按顺序查看：

1. trace detail 的 Summary 和 Raw Protocol。
2. Routing 页面或 trace 中的 routing context。
3. Events 页面是否有 parser/router/upstream 事件。
4. Models/Providers 页面确认模型启用和渠道健康。
5. 只有在派生结果明显不对时运行 Refresh analysis；token 统计异常时运行 Repair stats。
