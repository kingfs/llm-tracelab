# Monitor 使用指南

Monitor 是 TraceLab 的本地 Web 工作台。

它面向三个主要场景：

- 查看真实 LLM HTTP 请求。
- 分析 session、模型、渠道、路由和失败。
- 管理 token、渠道、模型启停和重分析任务。

## 登录与 Token

首次部署需要创建用户：

```bash
go run ./cmd/server auth init-user -c config/config.yaml --username admin --password 'change-me-123'
```

登录后在 `Tokens` 页面创建个人 token。

同一个 token 用于：

- 代理 API：`Authorization: Bearer <token>`。
- MCP：`Authorization: Bearer <token>`。

## 数据来源

Monitor 使用两类数据：

- SQLite：列表、过滤、分页、聚合、渠道/模型配置、事件和派生分析。
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

### Requests

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

### Channels

管理上游渠道。

支持：

- 创建渠道。
- 设置 provider preset、base URL、API key、headers、routing 字段。
- 设置 provider API surface：`api_type`、`mode`，以及 Responses、Chat Completions、tool calling、models 等 capability 开关；这些字段会写入 channel store，并在运行时还原为 upstream routing target。
- 创建前可用 Detect provider 做临时探测，不落库返回 API surface 建议；需要采用建议时，使用 Apply suggestions 显式写入表单。
- 创建前需用 Validate setup 调用 provider setup validate：它会组合 base URL、API key、provider preset、model discovery 和 capability 字段做一次探测并把归一化配置写回表单，但不会落库；dialog 会展示 normalized config、probe 和 redacted secret state，字段变更会清空旧验证结果；Create provider 才通过 setup apply 写入 channel store；若 probe 未检测成功，需显式提供 `api_type` 与 `protocol_family`。
- setup validate/apply 响应不会回显 API key；只返回 `api_key_hint`、secret storage mode 和 redacted header 状态。
- 探测模型，并查看 provider detection 建议；需要写回建议时，使用 Apply suggestions 显式更新 channel 配置。
- 启停渠道。
- 启停单个模型。
- 查看渠道用量、token、失败和 probe 结果。

长期渠道配置保存在 application store；Postgres 部署使用版本化迁移，SQLite 仍作为本地 fallback。YAML 只作为启动和首次 bootstrap 输入。

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

返回内容包括：

- `enabled`、`timeout`、`max_result_bytes`。
- `redaction.arguments`、`redaction.output`。
- `supported_types`，当前为 `["static_response", "external_command"]`。
- `executors[]` 中的 `name`、`type`、`enabled`、`available`、`output_configured`、`command_configured` 和 `warnings`。
- `warnings`，例如开启 executor 但未配置任何 binding、没有可用 executor、重复 name、空 name、未知 type 或 `external_command` 缺少 command。

该 API 不返回 `static_response` 的 output 内容，也不返回 `external_command` 的 command 内容。`POST /api/responses/function-executors` 是保守的 Monitor 写配置首切：默认 `validate_only=true`，返回 normalized summary 和 warnings；`validate_only=false` 时只更新当前进程内的 Monitor function executor overlay，不覆写 YAML 文件，也不重新装配 proxy runtime 中已经注册的 executor。写入 payload 只接受 `enabled`、`timeout`、`max_result_bytes`、`redaction.arguments`、`redaction.output`、executor `name` / `type` / `enabled`，以及 `external_command` 的 `process.working_dir` / `process.require_absolute_command` 隔离字段；`output` 和 `command` 等敏感字段不被写接口接受，响应中也不会回显。

`external_command` 必须通过 YAML 显式配置真实 command，运行时不使用 shell，默认不继承环境变量，tool call 输入通过 stdin JSON 传入命令。可选 `process.working_dir` 会要求绝对且已存在的执行目录；可选 `process.require_absolute_command=true` 会拒绝相对 command/PATH 查找，相关危险配置会以 validation warning 形式出现在摘要中。

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
- batch reanalysis。
- usage repair。
- reparse Observation IR。
- rescan findings。

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

- `Repair usage`：从本地响应重新抽取 usage。
- `Reparse`：从 cassette 重建 Observation IR。
- `Rescan`：对已有 Observation IR 重跑 deterministic detectors。
- `Reanalyze`：重建 Observation IR 并重扫 findings。

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
4. Models/Channels 页面确认模型启用和渠道健康。
5. 必要时运行 Reparse/Reanalyze。
