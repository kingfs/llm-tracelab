# MCP 使用指南

本文档描述当前 `llm-tracelab` MCP server 的实现范围。

## 定位

MCP 当前用于让 AI agent 查询本地 TraceLab 数据，辅助排障和分析。

当前支持：

- MCP `2026-07-28`，并兼容协商 `2025-11-25`、`2025-06-18`、`2025-03-26` 和 `2024-11-05`。
- streamable HTTP transport。
- `2026-07-28` sessionless/stateless 请求模型和 `server/discover`。
- 复用 Monitor/store 查询逻辑。
- trace、session、upstream 查询。
- 失败聚类。
- TraceLab 系统事件查询。
- 受控 reanalysis job。

当前不支持：

- 托管控制平面。
- 替代 Monitor 或 replay 存储。
- 广泛写入型运维操作。
- 调用上游 provider。

## 启动

使用与代理和 Monitor 相同的配置启动服务：

```bash
go run ./cmd/server serve -c config/config.yaml
```

推荐配置：

```yaml
monitor:
  port: "8081"

mcp:
  enabled: true
  path: "/mcp"
```

默认 endpoint：

```text
http://localhost:<monitor.port>/mcp
```

该 endpoint 使用 stateless Streamable HTTP。`2026-07-28` 客户端通过
`server/discover` 发现能力，并在每次请求的 `_meta` 中携带协议版本、客户端信息和能力；
旧客户端仍可在同一 endpoint 上协商旧协议版本。新协议不创建 `Mcp-Session-Id`，也不支持
独立 GET、DELETE 和基于 `Last-Event-ID` 的恢复流。

项目使用官方 `github.com/modelcontextprotocol/go-sdk`。从 `v1.7.0` 起，该 SDK 完整支持
`2026-07-28`，包括标准化 MCP HTTP headers、cacheable list result、统一 subscription
stream 和 multi-round-trip request 基础设施。TraceLab 当前是以查询工具为主的 server，尚未
暴露需要 multi-round-trip input 的工具，也不依赖已弃用的 roots、sampling 或 logging。

## 认证

MCP 使用和代理 API 相同的个人 token。

流程：

1. 用 `llm-tracelab auth init-user` 初始化首个用户。
2. 登录 Monitor，在 `Tokens` 页面创建 token；或用 `llm-tracelab auth create-token` 创建。
3. MCP client 发送 `Authorization: Bearer <token>`。

## 工具概览

### Trace 与 Session

- `list_traces`：分页列出 trace，支持 provider、model、全文查询和 Observation 状态过滤。
- `get_trace`：获取单条 trace 详情，可选择包含 raw HTTP request/response。
- `list_sessions`：分页列出 session，支持 provider、model、全文查询。
- `list_trace_findings`：列出单条 trace 的审计 findings。

### Upstream 与路由

- `list_upstreams`：查看 upstream 分析，支持时间窗口和模型过滤。
- `query_routing_decisions`：查看单条 trace 的路由决策事件。
- `query_sticky_routing`：查询 sticky routing 事件。
- `query_failures`：从分页 trace 扫描中返回失败请求。
- `summarize_failure_clusters`：按 reason、status、model、provider、endpoint、upstream、route target 聚类失败。

### 系统事件

- `list_system_events`：列出 TraceLab 运行时和派生管道事件。
- `get_system_event`：查看单个事件，可选择包含 details。
- `summarize_system_events`：返回事件计数和最新事件。
- `query_unread_system_events`：按严重程度和时间返回未读事件。

系统事件用于 TraceLab 自身异常：

- parser failure。
- analyzer failure。
- router selection failure。
- upstream transport error。

它们不是普通请求失败列表。

### 重分析

- `reanalyze_trace`：刷新单条 trace 的本地派生分析数据。
- `reanalyze_session`：刷新 session 内 traces 的本地派生分析数据。
- `list_analysis_jobs`：列出分析任务。
- `get_analysis_job`：查看任务详情。

这些操作只读取本地 cassette 并写入 application DB 派生状态；生产部署写入 Postgres，本地 fallback 可写入 SQLite。刷新分析不会访问上游模型，通常只在自动处理异常或结果明显不对时使用。

### 安全相关查询

- `query_dangerous_tool_calls`：查询危险工具调用 findings。
- `query_sensitive_data_findings`：查询凭据或敏感数据 findings。

## Evaluator

当前内置 deterministic evaluator profile：

- `baseline_v1`：HTTP 状态、记录错误、响应 body。
- `baseline_v2`：在 v1 基础上加入 TTFT 和 token 预算。
- `baseline_v3`：在 v2 基础上加入 tool call 声明一致性。
- `baseline_v4`：在 v3 基础上加入 tool call arguments JSON 校验。

默认 baseline 是 `baseline_v4`。

这些 evaluator 是客观、低成本、可复现的信号，不替代人工质量判断或模型评分。

## 设计约束

- MCP handler 复用 Monitor/store 行为，不建立第二套查询语义。
- 只读工具不改变 replay 或 raw cassette。
- 重分析工具必须生成可审计 `analysis_jobs`。
- 不通过 MCP 暴露 raw secret。
- 不把 MCP 当成 TraceLab 的存储事实源。
