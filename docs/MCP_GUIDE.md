# MCP 使用指南

本文档描述 TraceLab 当前与 MCP 相关的两个实现面、启动与认证方式，以及 `internal/mcpserver/server.go` 中真实注册的工具清单。工具数量以代码为准。

## 定位

TraceLab 有两个方向相反的 MCP 实现面，二者都基于官方 `github.com/modelcontextprotocol/go-sdk`（当前 `v1.7.0`），但用途、配置和进程内位置都不同：

- **对外排障 server**：实现在 `internal/mcpserver`，由 `cmd/server/management.go` 装配，挂在 management server 的 MCP endpoint 上。方向是「外部 AI agent → TraceLab」：agent 通过 MCP 查询本地 trace、session、upstream、failures、system events、Responses audit，并触发受控重分析。
- **Responses runtime 内部 tool executor**：实现在 `internal/responses/tools/mcp/executor.go`，是 hosted tools registry 中类型为 `mcp` 的 executor。方向是「TraceLab 本地 Responses runtime → 外部 MCP server」：处理 `/v1/responses` 时，runtime 通过 Streamable HTTP `tools/call` 调用配置好的外部 MCP server，并把结果回灌给模型。它由 `tools.mcp` 配置，映射给上游模型的 Chat tool 名为 `mcp_call`，执行 lifecycle 写入 `tool_call_audits`。

内部 hosted `mcp` executor 已实现并接线；未实现的只有 `file_search`、`code_interpreter`、`computer_use_preview` 三类 hosted tool 的执行器。内部 executor 的完整行为见 [本地 Responses Runtime](./RESPONSES_RUNTIME.md)，本文其余部分只描述对外排障 server。

## 启动与挂载路径

MCP 与 proxy、Monitor 使用同一份配置启动。`config/config.yaml` 使用 Postgres 且 `database.dsn` 为空，运行前需导出 `LLM_TRACELAB_DATABASE_DSN`；纯本地运行可改用 `config/examples/local-sqlite.yaml`：

```bash
export LLM_TRACELAB_DATABASE_DSN='postgres://user:pass@host:5432/llm_tracelab?sslmode=disable'
go run ./cmd/server serve -c config/config.yaml
```

MCP server 相关配置：

```yaml
monitor:
  port: "8081"

mcp:
  enabled: true
  path: "/mcp"
```

- MCP 挂在 management server 上，因此 `monitor.port` 为空时该 HTTP 服务不启动，MCP 也不可用。
- endpoint 默认是 `http://localhost:<monitor.port>/mcp`。`mcp.path` 会被规范化为以 `/` 开头、不以 `/` 结尾；空值回退到 `/mcp`，`/` 是非法值。
- 环境变量覆盖：`LLM_TRACELAB_MCP_ENABLED`、`LLM_TRACELAB_MCP_PATH`。

transport 与协议：

- 使用 streamable HTTP，`Stateless: true`，即 `2026-07-28` 的 sessionless/stateless 请求模型；不创建 `Mcp-Session-Id`，也没有独立 GET、DELETE 和基于 `Last-Event-ID` 的恢复流。
- `2026-07-28` 客户端通过 `server/discover` 发现能力，并在每次请求的 `_meta` 中携带协议版本、客户端信息与能力。
- 同一 endpoint 兼容协商旧协议版本：`2025-11-25`、`2025-06-18`、`2025-03-26`、`2024-11-05`。
- `v1.7.0` 的 SDK 完整支持 `2026-07-28`（标准化 MCP HTTP headers、cacheable list result、统一 subscription stream 与 multi-round-trip request 基础设施）。TraceLab 当前以查询工具为主，没有暴露需要 multi-round-trip input 的工具，也不依赖已弃用的 roots、sampling 或 logging。
- server 实现名为 `llm-tracelab`，版本 `1.0.0`。

## 认证

MCP endpoint 始终包在个人 token 认证中间件里（`auth.Middleware(mcpHandler, "llm-tracelab-mcp", verifier)`）：缺失、格式错误或无效 token 的请求返回 `401 Unauthorized`，并带 `WWW-Authenticate: Bearer realm="llm-tracelab-mcp"`。没有免认证本地模式。

流程：

1. 用 `llm-tracelab auth init-user` 初始化首个用户。
2. 登录 Monitor，在 `Tokens` 页面创建个人 token；或用 `llm-tracelab auth create-token` 创建。
3. MCP client 每个请求发送 `Authorization: Bearer <token>`。

要点：

- MCP 与 proxy API 复用同一套个人 API token；Monitor 登录态 JWT 只用于 Monitor API，Monitor API 反过来不接受个人 API token。
- server 侧没有 MCP token 环境变量。Codex 示例里的 `LLM_TRACELAB_MCP_TOKEN` 是**客户端**自己声明的环境变量名（`bearer_token_env_var`），不是服务端配置项。
- `internal/responses/tools/mcp` 的 `tools.mcp.servers[].bearer_token_env` 是另一回事：它指定 hosted executor 去哪个环境变量读取访问**外部** MCP server 的 token。

token 管理与 Monitor 登录的完整说明见 [Monitor 使用指南](./MONITOR_GUIDE.md)。

## 工具概览

`internal/mcpserver/server.go` 当前通过 `mcp.AddTool` 注册 **21** 个工具。按用途分组如下。

### Trace、Session 与 Upstream

- `list_traces`：分页列出 trace；支持 `page`、`page_size`（上限 200）、`provider`、`model`、`q` 和 `observation`（`parsed`、`failed`、`queued`、`running`、`unparsed`）过滤。
- `get_trace`：按 `trace_id` 取单条 trace 详情，`include_raw` 时附带原始 HTTP request/response。
- `list_sessions`：分页列出聚合后的 session；支持 `provider`、`model`、`q`。
- `list_upstreams`：返回 upstream 分析；支持 `window`（`today`、`7d`、`30d`、`all`）与 `model`。

### 路由与失败

- `query_routing_decisions`：返回单条 trace 的路由决策事件，含 candidates、selected upstream、outcome 与 failure reason。
- `query_sticky_routing`：查带 `routing.sticky.*` cassette 事件的 trace；支持 `status`（`hit`、`miss`、`bind`、`break`）、`upstream_id`、`previous_upstream_id`、`sticky_key_fingerprint`。
- `query_failures`：从分页 trace 扫描中返回失败请求；过滤项是 `page`、`page_size`、`provider`、`model`、`q`。
- `summarize_failure_clusters`：按 reason、status、model、provider、endpoint、upstream、route target 聚类失败，并给出 top failures；`limit` 控制每组条数（默认 10）。

### Findings

- `list_trace_findings`：列出单条 trace 的 deterministic audit findings；支持 `severity`、`category` 过滤。
- `query_dangerous_tool_calls`：返回单条 trace 的危险命令与不安全工具调用 findings。
- `query_sensitive_data_findings`：返回单条 trace 的凭据与敏感数据 findings。

### 系统事件

- `list_system_events`：分页列出 TraceLab 运行时与派生管道异常事件；支持 `status`、`severity`、`source`、`category`、`q`、`window`。
- `get_system_event`：按 `event_id` 取单个事件；`include_details` 时附带 `details_json`。
- `summarize_system_events`：返回事件计数与最新事件，供 agent 快速 triage；支持 `window`、`status`。
- `query_unread_system_events`：返回未读的 warning/error/critical 事件，按严重度与时间排序；支持 `limit`（默认 20，上限 200）、`min_severity`。

系统事件只覆盖 TraceLab 自身的异常来源，例如 parser failure、analyzer failure、router selection failure、upstream transport error；它不是普通请求失败列表（后者用 `query_failures` 与 `summarize_failure_clusters`）。

### Responses 审计

- `responses_audit_trace`：按 `response_id` 或 `request_audit_id` 返回 Responses request audit、execution events 与 upstream exchange 摘要。
- `responses_audit_tool_calls`：列出持久化的 Responses tool-call audit 记录；支持 `response_id`、`request_audit_id`、`conversation_id`、`call_id`、`tool_name`、`status`、`limit`（默认 100，上限 500）。仅当 `include_payloads=true` 时返回 raw `input_json`、`output_json`、`metadata_json`。

### 重分析

- `reanalyze_trace`：对单条 trace 运行或入队受控重分析；`reparse`、`scan` 默认 true，`repair_usage` 可先修 indexed usage，`async` 决定是否立即执行。
- `reanalyze_session`：对 session 内 traces 运行或入队重分析；`async` 默认 true。
- `list_analysis_jobs`：列出重分析 job；支持 `status`、`target_type`（`trace`、`session`、`batch`）、`target_id`、`limit`（默认 50）。
- `get_analysis_job`：按 `job_id` 取单个重分析 job。

重分析只读本地 cassette 并写 application DB 派生状态（生产为 Postgres，本地 fallback 为 SQLite），生成可审计的 `analysis_jobs`，不访问上游模型。

## Codex 本地配置示例

本仓库约定的 Codex MCP client 本地配置路径是工作区内的 `.codex/config.toml`。`.codex/` 已被 git 忽略，因此该文件不会进入版本库；仍然不要把 token 或敏感 endpoint 写进文件本身。

```toml
[mcp_servers.tracelab-remote]
url = "http://ip:port/mcp"
bearer_token_env_var = "LLM_TRACELAB_MCP_TOKEN"
```

这里的 `LLM_TRACELAB_MCP_TOKEN` 只是客户端从环境变量读取 bearer token 的名字，不是服务端变量。MCP endpoint 始终要求有效的 `Authorization: Bearer <token>`，缺失或无效 token 返回 401，因此启动 Codex 前先导出：

```bash
export LLM_TRACELAB_MCP_TOKEN='...'
```

认证要求见上文「认证」。

## 查看与更新本地配置

只检查仓库本地 MCP 配置，不修改全局 Codex 配置：

```bash
CODEX_HOME="$PWD/.codex" codex mcp list
CODEX_HOME="$PWD/.codex" codex mcp get tracelab-remote
```

更新远端 endpoint：

```bash
CODEX_HOME="$PWD/.codex" codex mcp remove tracelab-remote
CODEX_HOME="$PWD/.codex" codex mcp add tracelab-remote \
  --url http://HOST:PORT/mcp \
  --bearer-token-env-var LLM_TRACELAB_MCP_TOKEN
```

## Evaluator

仓库内置的 deterministic evaluator profile 定义在 `internal/evals`，默认 profile 是 `baseline_v4`：

- `baseline_v1`：HTTP 状态 2xx、记录错误、响应 body 是否存在。
- `baseline_v2`：在 v1 基础上加入 TTFT 与 total token 预算。
- `baseline_v3`：在 v2 基础上加入响应 tool call 必须已在请求中声明。
- `baseline_v4`：在 v3 基础上加入 tool call arguments 必须是合法 JSON。

这些 profile 是客观、低成本、可复现的信号，不替代人工质量判断或模型评分。MCP 工具面本身不包含 evaluator 工具；MCP 侧可查询的是 `list_trace_findings` 等 deterministic audit findings，语义见 [观测与审计](./OBSERVATION_AND_AUDIT.md)。

## 设计约束

- MCP handler 在进程内复用 Monitor HTTP API（通过 `httptest` 直接调用同一个 mux），不为 MCP 建立第二套查询语义。
- 只读工具不改变 replay 行为或 raw cassette。
- 重分析工具必须生成可审计的 `analysis_jobs`。
- 不通过 MCP 暴露 raw secret；`responses_audit_tool_calls` 的 payload 也默认不返回。
- MCP 不是 TraceLab 的存储事实源：列表与聚合来自 application DB，trace 详情来自 raw cassette。
- 修改 MCP 工具面必须同步更新本文档与测试。

## 非目标与未实现

- 不替代 Monitor、replay 或应用数据库事实源，也不提供托管控制平面。
- 对外 MCP server 不调用上游 provider，也没有广泛写入型运维操作；唯一的写入是受控重分析。
- Responses runtime 内部 hosted `mcp` executor 只执行 `tools/call`；`tools/list` discovery 缓存、resources/prompts、OAuth token 管理、approval 与 long-running calls 均未实现。
- `file_search`、`code_interpreter`、`computer_use_preview` 没有执行器：强制调用时返回稳定拒绝与 rejected audit，不伪造 citation、chunk、code output 或 computer-use 副作用。

相关文档：[Monitor 指南](./MONITOR_GUIDE.md)、[本地 Responses Runtime](./RESPONSES_RUNTIME.md)、[观测与审计](./OBSERVATION_AND_AUDIT.md)、[架构与代码地图](./ARCHITECTURE.md)。
