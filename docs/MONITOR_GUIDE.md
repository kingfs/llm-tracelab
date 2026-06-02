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
- 探测模型。
- 启停渠道。
- 启停单个模型。
- 查看渠道用量、token、失败和 probe 结果。

长期渠道配置保存在 SQLite。YAML 只作为启动和首次 bootstrap 输入。

### Routing

查看 selected route 和路由事件。

适合：

- 确认某个模型请求由哪个渠道处理。
- 排查为什么请求失败或被重试。
- 查看 sticky routing、candidate、failure reason。
- 从路由记录跳到 trace detail。

`GET /api/routing/summary` 会读取 V3 cassette prelude 中的路由事件，并按 failure reason、selected route、sticky 状态等聚合。

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
