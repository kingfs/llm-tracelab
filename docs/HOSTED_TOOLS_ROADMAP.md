# Hosted Tools Roadmap

状态：分阶段实现路线图
日期：2026-06-24

本文定义 TraceLab `/v1/responses` server-mode 对 OpenAI Responses hosted tools 的逐步复现计划。它不是当前实现事实清单；当前事实仍以 [项目基线](./PROJECT_BASELINE.md) 和 [Responses Server 设计](./RESPONSES_SERVER_DESIGN.md) 为准。

## 目标

TraceLab 的目标是提供一个可录制、可审计、可本地/私有化部署的 Responses semantic server。Hosted tools 的目标是让客户端可以按 OpenAI Responses API 的方式传入 `tools`，由 TraceLab 服务端执行受控工具、把工具结果注入模型回路，并输出兼容的 response item、stream event 和审计记录。

优先复现的工具族：

1. `web_search` / `web_search_preview`
2. `mcp`
3. `file_search`
4. `code_interpreter`
5. `computer_use_preview`

非目标：

- 不复刻 Codex CLI 本地 agent 的内部实现。
- 不把所有 Codex 本地工具都变成 TraceLab 内置工具。
- 不让工具执行绕过 TraceLab 的审计、权限和 redaction。
- 不让 `.http` cassette 依赖工具内部状态；raw cassette 仍只作为上游模型 HTTP exchange 的 replay 事实源。
- 不默认启用高风险工具。

## 当前状态

截至 2026-06-24，TraceLab 已有：

- `/v1/responses` server-mode。
- 基于 OpenAI-compatible Chat Completions upstream 的 Responses 编排。
- hosted `web_search` / `web_search_preview` 首切，provider 支持 `mock` 和 SearXNG，并已迁入 hosted tool registry。
- hosted `mcp` 首切：
  - `tools.mcp` YAML/env 配置、`config inspect` 与 `doctor` 诊断。
  - Streamable HTTP MCP `tools/call` executor。
  - bearer token env、enabled/disabled tools、timeout、max result bytes、安全错误与 redaction。
  - Responses runtime 在 `tools.mcp.enabled=true` 且存在 enabled server 时注册 MCP executor。
- 普通 `function` tool 的客户端回路。
- 注册式 server-side function executor，支持 `static_response` 和受限 `external_command`。
- stream tool loop 首切：已注册 function executor 和 hosted web_search 可输出 started/done item，并继续最终文本 delta。
- `tool_call_audits` 与 `execution_events` 已承接 web_search、configured function executor、MCP 和 unsupported hosted tool rejected 记录。
- 对未实现 hosted tools（`file_search`、`code_interpreter`、`computer_use_preview`）已有稳定 `unsupported_tool` 错误和 rejected audit。

主要差距：

- Hosted tool registry 已有首切，但 runtime 内仍存在按工具类型映射 Chat Completions function tool 的局部特判；后续应继续收敛 helper 和 audit contract。
- MCP 已支持 non-stream tool loop；MCP incremental stream、`tools/list` discovery 缓存、resources/prompts、OAuth、approval、long-running calls 仍未实现。
- `file_search`、`code_interpreter`、`computer_use_preview` 均未真实执行。
- Tool lifecycle 还缺少完整跨轮状态、取消、重试、approval、artifact、citation、stream event 细节。
- Monitor 还没有统一的 Hosted Tools 配置/状态/调用审计页面。
- Codex fixtures 主要覆盖已落地路径，未覆盖完整 hosted tool schema 矩阵。

## 服务端与客户端职责边界

TraceLab 是 Responses semantic server。Codex CLI、OpenAI SDK、浏览器前端或其它调用方是客户端。Hosted tools 的核心原则是：**工具执行、审计、权限、redaction、依赖诊断在服务端；用户交互、profile/provider 配置、人工确认体验在客户端或 Monitor 控制面**。

### TraceLab 服务端必须实现

- 解析 OpenAI Responses `tools` / `tool_choice` schema，并把服务端可执行 hosted tool 暴露给上游 Chat Completions 模型。
- 执行受控 hosted tool：
  - `web_search` 调 SearXNG/mock provider。
  - `mcp` 调 configured Streamable HTTP MCP server。
  - 后续 `file_search`、`code_interpreter`、`computer_use_preview` 也由 TraceLab server-side executor 执行。
- 工具 policy：
  - 默认关闭。
  - per-tool enable flag。
  - allowlist/denylist。
  - timeout、max argument bytes、max result bytes。
  - safe error。
  - no raw secret in API response、logs、audit summaries。
- 记录 `execution_events` 与 `tool_call_audits`，并提供 CLI/Monitor/MCP 查询面。
- `doctor` / `config inspect` 输出工具状态、依赖缺失和 redacted 配置。
- 对未启用、未知或暂不支持的 hosted tool 返回稳定 rejected/unsupported 错误，而不是静默忽略。
- 对 stream/non-stream 行为给出明确 contract：
  - 支持 incremental stream 的工具才可在 SSE 中执行并继续输出。
  - 不支持 incremental stream 的工具必须返回 deferred/fallback/unsupported reason，不能误执行。

### Codex/客户端应实现或配置

- 选择是否向 TraceLab `/v1/responses` 发送 `tools` 和 `tool_choice`。TraceLab 不应假设所有 Codex 请求都包含 hosted tools。
- 配置 provider/base_url/wire_api/API key，例如通过 `models codex-config` 生成或手写 Codex provider 配置。
- 在请求层传入符合 OpenAI Responses 语义的 tool descriptor，例如 `web_search_preview`、`mcp`、`file_search`。
- 呈现服务端返回的 tool item、safe error、citation/artifact link、approval required 状态。
- 需要人工确认时，客户端或 Monitor UI 负责交互：
  - 展示服务端给出的 approval request。
  - 收集用户确认/拒绝。
  - 把 approval decision 作为后续 request 或管理 API 调回服务端。
- Codex 本地 agent 的 shell/apply_patch/browser/computer-use 工具不由 TraceLab 复刻；如果要通过 Responses hosted tool 复现，只能走 TraceLab 定义的受控 server-side executor。

### 混合职责

| 能力 | TraceLab 服务端 | Codex/客户端 |
| --- | --- | --- |
| Provider metadata / model profile | 提供 `models codex-config`、doctor、model/profile diagnostics | 读取配置并选择模型 |
| Web search | 执行 SearXNG/mock search，审计结果 | 决定是否请求 `web_search`，展示结果/citations |
| MCP tools | 连接 configured MCP server，执行 allowlisted `tools/call` | 在请求中声明 `mcp` tool，展示失败/approval |
| MCP OAuth | 保存/使用服务端 token、诊断 token 状态 | 发起登录或引导管理员配置 token；Codex `mcp login` 不适用于 TraceLab hosted MCP |
| Approval | 策略判定、生成 approval request、执行 approved call | UI 展示与用户确认 |
| File search | 管理索引、检索、citation、权限 | 上传/选择文件或 vector store，展示 citation |
| Code interpreter | sandbox 执行、artifact 管理、审计 | 展示 stdout/stderr/artifact link，必要时确认高风险执行 |
| Computer use | 隔离 browser/session、screenshot/action loop、artifact | 展示截图、收集确认、处理暂停/继续 |

## 架构原则

### 统一工具注册

所有 hosted tools 应收敛到统一 registry，避免为每个工具在 runtime 主流程里继续扩散特判。

建议核心接口：

```go
type HostedToolRegistry interface {
	List(ctx ToolContext) []HostedToolExecutor
	Resolve(toolType string) (HostedToolExecutor, bool)
}

type HostedToolExecutor interface {
	ToolTypes() []string
	Enabled(ctx ToolContext) bool
	ValidateDescriptor(tool protocol.Tool) error
	ChatTool(ctx ToolContext, tool protocol.Tool) (chatclient.Tool, error)
	Execute(ctx context.Context, call HostedToolCall) (HostedToolResult, error)
}
```

`ToolContext` 建议包含：

- request id、response id、conversation id、client request id。
- model、provider/channel、route target。
- stream/non-stream。
- auth subject 或 token scope 摘要。
- redaction policy。
- tool policy snapshot。
- audit recorder。
- per-request temp directory 或 artifact root。

`HostedToolCall` 建议包含：

- tool type。
- canonical tool name。
- call id。
- parsed arguments。
- original descriptor summary。
- forced tool choice 标记。
- upstream model call attempt。

`HostedToolResult` 建议包含：

- Responses output item。
- Chat tool result message。
- structured result summary。
- citations/artifacts。
- safe metadata。
- raw payload redaction summary。

### 工具执行生命周期

所有 hosted tools 统一生命周期：

1. `requested`：模型请求 tool call 或客户端强制 tool_choice。
2. `validated`：descriptor 与 arguments 校验通过。
3. `started`：实际执行开始。
4. `completed`：执行成功，结果已准备注入模型。
5. `failed`：执行失败，可安全暴露错误摘要。
6. `cancelled`：请求取消或超时。
7. `rejected`：工具未启用、未授权、descriptor 不允许或策略拒绝。

对应写入：

- `execution_events`：request/model/tool/stream 生命周期。
- `tool_call_audits`：工具调用 read model，支持 Monitor/MCP/CLI 查询。

每条工具审计必须至少包含：

- response id。
- request audit id。
- call id。
- tool type。
- tool name。
- executor id。
- status。
- phase。
- stream flag。
- duration。
- redacted argument summary。
- redacted result summary。
- error text summary。
- policy decision summary。

### Stream contract

Streaming 路径需要明确区分两类模式：

- incremental stream：工具可在模型 SSE 中真实增量执行，输出 `response.output_item.added`、arguments delta/done、tool item done、最终文本 delta。
- deferred stream：复杂组合先返回 Responses SSE envelope，再由后台或简化路径完成，或明确 fallback reason。

短期策略：

- `web_search` 和已注册 function executor 继续支持 incremental stream。
- `mcp` 当前支持 non-stream tool loop；stream 请求先返回明确 fallback/unsupported，不误执行。后续 Phase 2E 再实现 incremental stream 简单路径。
- `file_search` 第一版可支持 non-stream 和 deferred stream。
- `code_interpreter`、`computer_use_preview` 第一版只支持 non-stream 或 deferred stream。

### 安全默认值

默认配置：

- `web_search`: disabled，生产可显式启用 SearXNG。
- `mcp`: disabled。
- `file_search`: disabled。
- `code_interpreter`: disabled。
- `computer_use_preview`: disabled。

所有工具必须支持：

- timeout。
- max result bytes。
- max argument bytes。
- audit redaction。
- per-tool enable flag。
- per-tool allowlist/denylist。
- safe error。
- no raw secret in API response、logs、audit summaries。

高风险工具额外要求：

- `code_interpreter` 必须有 sandbox。
- `computer_use_preview` 必须有 isolated browser/desktop session。
- `mcp` 默认只允许 read-only 或显式 allowlisted tools。
- `file_search` 必须有 file ownership/scope 校验。

## Tool Matrix

| Tool | 当前状态 | 服务端下一目标 | 客户端/Monitor 配合 | 长期目标 | 风险 |
| --- | --- | --- | --- | --- | --- |
| `web_search` | SearXNG/mock，registry 首切，stream/non-stream 可执行 | citations/source action 细节、统一 audit helper | 决定是否传 `web_search`，展示 citations | 更多 provider、cache、prompt-injection guard | prompt injection、版权/引用 |
| `mcp` | 配置化 Streamable HTTP `tools/call`，non-stream tool loop 可执行 | `tools/list` discovery 缓存、incremental stream、OAuth token 状态、approval request | 声明 `mcp` descriptor，展示 safe error/approval；OAuth 登录或 token 配置由客户端/管理员触发 | resources/prompts、approval、streaming results、long-running calls | 外部工具副作用、认证 |
| `file_search` | rejected only | TraceLab-native file/vector store、检索、citation、权限 | 上传/选择文件或 vector store，展示 citation | 对齐 OpenAI vector store/files 语义 | 数据权限、索引成本 |
| `code_interpreter` | rejected only | Docker/rootless sandbox、短任务执行、artifact store | 展示 stdout/stderr/artifact，必要时确认高风险执行 | 多轮 session、package policy、artifact browser | RCE、资源消耗 |
| `computer_use_preview` | rejected only | 先做 threat model 和 browser-use sandbox spike | 展示截图、收集人工确认、处理暂停/继续 | 隔离浏览器/桌面 session，screenshot/action loop | 高风险自动化、环境隔离 |

## 分阶段计划

### Phase 0: Contract And Test Inventory

目标：在不改变行为的前提下固化工具契约和测试矩阵。

实现项：

- 新增 `internal/responses/tools/hosted` 包，定义 registry/executor/result 类型。
- 把当前 unsupported hosted tool 的错误类型和审计字段整理成共享 contract。
- 增加 fixture inventory：
  - `web_search`
  - `web_search_preview`
  - `mcp`
  - `file_search`
  - `code_interpreter`
  - `computer_use_preview`
  - forced `tool_choice`
  - `tool_choice=auto`
  - stream/non-stream
- 增加 docs 中的 capability matrix。

验收：

- 行为不变。
- 现有 `web_search` 测试全部通过。
- 未实现 hosted tools 仍稳定 rejected，audit 不泄露 raw descriptor。
- `task test:codex-fixtures` 覆盖新的 fixture inventory。

### Phase 1: Hosted Tool Registry And Web Search Migration

目标：把现有 web_search 迁入统一 hosted tool registry。

实现项：

- `web_search` executor 实现 `HostedToolExecutor`。
- Runtime 从 registry 获取 Chat Completions function tool 映射。
- Runtime 通过 registry 执行 tool call。
- 统一 `tool_choice` 处理：
  - `auto`
  - `none`
  - string tool name
  - object-style forced tool
- 统一 started/completed/failed/rejected audit helper。
- 保留 SearXNG/mock provider。
- 保留现有 stream tool loop 行为。

验收：

- 现有 `internal/responses/runtime` web_search 测试不降级。
- 新增 registry 单元测试。
- `web_search` disabled 时仍不暴露 chat tool。
- forced web_search disabled 时返回稳定 unsupported/rejected。
- stream 和 non-stream audit 字段一致。

### Phase 2: MCP Hosted Tool

目标：TraceLab 作为 Responses server 连接 configured MCP server，执行 hosted `mcp` tool。

配置建议：

```yaml
tools:
  mcp:
    enabled: false
    default_timeout_ms: 60000
    max_result_bytes: 65536
    servers:
      - id: docs
        url: "https://example.internal/mcp"
        bearer_token_env: "TRACELAB_DOCS_MCP_TOKEN"
        enabled_tools: ["search", "fetch"]
        disabled_tools: []
        enabled: true
```

已落地范围：

- Streamable HTTP MCP。
- Bearer token/env headers。
- `tools/call` execution。
- enabled_tools/disabled_tools。
- result text/json 截断与 redaction。
- `tools.mcp` YAML/env 配置。
- `config inspect` / `doctor` redacted diagnostics。
- Responses runtime non-stream MCP tool loop。

服务端下一步：

- `tools/list` capability discovery 与缓存，用于 doctor/status 和后续自动暴露 tool schema。
- MCP incremental stream 简单路径：输出 tool item lifecycle，并在工具完成后继续最终文本 delta。
- read-only annotation/policy：默认只自动执行只读或 allowlisted tools。
- approval request contract：服务端可以返回 `approval_required` safe item，等待客户端/Monitor 提交 decision。
- MCP server instructions 只进入 internal policy summary，不直接注入用户 prompt，除非显式配置。
- 更细的 audit 字段：server id、server label、tool name、duration、policy decision、argument/output redaction。

客户端/Monitor 配合：

- Codex/SDK 请求里继续传 OpenAI Responses `mcp` descriptor。
- Monitor Hosted Tools 页面显示 MCP server readiness、available tools、最近 failed/rejected 调用。
- OAuth 或 bearer token 配置由管理员/客户端触发；TraceLab 不复用 `codex mcp login`，因为 hosted MCP 认证发生在 TraceLab 服务端。
- destructive approval UI 属于客户端/Monitor 交互层；TraceLab 服务端只负责 policy decision、approval request、approved execution。

暂不做：

- Codex 本地 MCP server 管理。
- 复刻 Codex CLI 的 `codex mcp login`。
- sampling。
- long-running background tool calls。
- MCP resources/prompts。

Runtime 行为：

- Responses `tools` 中 `type=mcp` 的 descriptor 指向 server/tool。
- Registry 把允许的 MCP tool 暴露为 Chat Completions function tool。
- 模型请求 function call 后，executor 调用 MCP `tools/call`。
- MCP result 转成 Chat tool result message，并追加 Responses `mcp_call` 或兼容 output item。

验收：

- 可用 mock/httptest MCP server 做离线测试。
- MCP server 不可达时返回 failed/rejected safe error。
- disabled/denylisted tool 不会暴露给模型。
- audit 记录 server id、tool name、status、duration，不记录 bearer token。
- CLI Audit 可查 MCP tool call；Monitor Hosted Tools 页面作为后续验收。

### Phase 3: File Search

目标：提供最小可用的 private file search hosted tool。

建议先实现 TraceLab-native file store，不立即完整复刻 OpenAI vector store API。

服务端职责：

- 文件元数据、chunk、embedding、vector store 的存储与迁移。
- 文件 owner/scope 校验。
- 文件内容提取、chunking、embedding、索引状态机。
- `file_search` hosted executor：按 request 中的 vector store/file scope 检索 top K。
- Responses output item/citation summary。
- audit 只记录 file id/chunk id/hash/score，不记录完整文件内容。

客户端/Monitor 职责：

- 上传或选择文件/vector store。
- 展示 indexing status、citation 和权限错误。
- 不直接执行检索逻辑；检索必须经 TraceLab server-side executor。

数据模型建议：

- `files`
  - id
  - owner/scope
  - filename
  - mime_type
  - size
  - sha256
  - storage_uri
  - created_at
- `file_chunks`
  - id
  - file_id
  - chunk_index
  - text
  - token_count
  - metadata_json
- `file_embeddings`
  - chunk_id
  - embedding provider/model
  - vector
- `vector_stores`
  - id
  - name
  - scope
  - config_json
- `vector_store_files`
  - vector_store_id
  - file_id
  - status

第一版范围：

- Admin/API 导入文件。
- Monitor 文件上传/索引状态只做最小可用 UI，复杂权限 UI 后置。
- 文本/PDF/Markdown/JSON 基础提取。
- chunking。
- embedding provider 可配置。
- Postgres + pgvector 优先；SQLite fallback 可做 lexical/BM25-lite 测试路径。
- `file_search` executor 根据 vector_store ids 检索 top K。
- Responses output item 包含 citation summary。

暂不做：

- 完整 OpenAI Files API 兼容。
- 大文件 async indexing UI。
- 权限继承复杂模型。
- 多租户计费。

验收：

- 离线测试使用 mock embedding deterministic vector。
- 查询能返回稳定 chunk 和 citation。
- 无权限文件不进入检索。
- audit 不记录完整文件内容，只记录 file id/chunk id 摘要。
- Monitor 显示 index status 和最近 file_search 调用。

### Phase 4: Code Interpreter

目标：提供默认关闭的受控代码执行 hosted tool。

服务端职责：

- sandbox 生命周期、resource limits、network policy、artifact directory。
- 执行 code payload 并收集 stdout/stderr/artifact。
- safe error、timeout、oversized output、cleanup。
- artifact metadata 存储与下载鉴权。

客户端/Monitor 职责：

- 展示 stdout/stderr/artifact link。
- 对高风险配置或执行请求展示确认。
- 不在 Codex 本地执行 TraceLab hosted `code_interpreter`；所有执行必须在 TraceLab sandbox 内。

安全前置：

- 必须使用隔离 sandbox。
- 必须禁用或显式限制网络。
- 必须限制 CPU、内存、进程数、运行时长、输出大小。
- 必须使用临时工作目录。
- 必须收集 artifacts 并清理临时文件。
- 必须有 deny-by-default 配置。

第一版可选 sandbox：

- Docker container profile。
- rootless Docker 或 containerd profile。
- 后续可评估 Firecracker/wasmtime。

配置建议：

```yaml
tools:
  code_interpreter:
    enabled: false
    sandbox: docker
    image: "llm-tracelab-code-runner:python3.12"
    timeout_ms: 30000
    memory_mb: 512
    cpus: 1
    network: "none"
    max_output_bytes: 65536
    max_artifact_bytes: 10485760
```

第一版范围：

- Python execution。
- stdin code payload。
- stdout/stderr 截断。
- artifact directory。
- session-less execution，每次 tool call 独立容器。
- audit started/completed/failed。

暂不做：

- 多轮持久 kernel。
- pip install。
- GPU。
- 访问宿主文件系统。
- 任意 shell。

验收：

- 默认 disabled。
- 无 Docker 或 sandbox 不可用时清晰诊断。
- 超时/内存超限返回 safe error。
- artifact id 可查，raw artifact 不进入 audit。
- 网络默认不可用。

### Phase 5: Computer Use Preview

目标：在完成其它 hosted tools 后，再评估服务端 computer use。

此阶段风险最高，不建议早期实现。

服务端职责：

- isolated browser/desktop session。
- screenshot/action loop。
- session/artifact retention。
- domain/app allowlist。
- sensitive data masking 与 audit。

客户端/Monitor 职责：

- 展示截图和操作状态。
- 处理 human approval、pause/resume、stop。
- 不把 Codex 本地 computer-use 直接映射成 TraceLab 服务端能力；需要显式 hosted tool descriptor 和服务端 sandbox。

需要先设计：

- isolated browser/desktop session。
- screenshot capture。
- action schema。
- page/OS state persistence。
- allowlisted domains/apps。
- human approval。
- sensitive data masking。
- video/screenshot artifact storage。
- prompt injection guard。

第一版建议只做 browser-use，不做完整桌面：

- Playwright browser context。
- screenshot observation。
- click/type/scroll/navigation actions。
- domain allowlist。
- per-request or per-session isolation。

验收前置：

- 有明确 threat model。
- 有人工 approval/pause 机制。
- 有 artifact retention policy。
- 有 e2e visual verification tests。

## 配置总览

已落地配置：

```yaml
tools:
  web_search:
    enabled: false
    provider: searxng
    base_url: "http://searxng:8080"
    max_results: 5
    timeout_ms: 5000

  mcp:
    enabled: false
    default_timeout_ms: 60000
    max_result_bytes: 65536
    servers:
      - id: docs
        label: docs
        url: "https://example.internal/mcp"
        bearer_token_env: "TRACELAB_DOCS_MCP_TOKEN"
        enabled_tools: ["search", "fetch"]
        disabled_tools: []
        enabled: true
```

建议最终配置结构：

```yaml
tools:
  hosted:
    default_timeout_ms: 60000
    max_argument_bytes: 65536
    max_result_bytes: 65536
    redaction:
      arguments: true
      results: true
      errors: true

  web_search:
    enabled: false
    provider: searxng
    base_url: "http://searxng:8080"
    max_results: 5
    timeout_ms: 5000

  mcp:
    enabled: false
    servers: []

  file_search:
    enabled: false
    embedding_provider: ""
    max_chunks: 8

  code_interpreter:
    enabled: false
    sandbox: docker

  computer_use:
    enabled: false
```

兼容策略：

- 保留现有 `tools.web_search` 字段。
- 新增 `tools.hosted` 作为统一默认值。
- 每个具体工具可覆盖 timeout/result/redaction。
- `config inspect` 和 `doctor` 输出工具启用状态和缺失依赖。

## API 与 Monitor 规划

### Monitor

新增 Hosted Tools 页面或 Audit 子页：

- 工具启用状态。
- provider/backend readiness。
- 最近 tool calls。
- failed/rejected breakdown。
- redaction policy 摘要。
- sandbox readiness。
- MCP server connectivity。
- file index status。

写操作：

- 第一阶段只读。
- 后续可提供 validate-only。
- 高风险工具配置 apply 必须显式确认，并写 `app_settings` overlay。
- Approval UI 属于 Monitor/客户端层，但 approval decision 必须写回服务端并进入 audit。

### CLI

建议新增或扩展：

- `config inspect --include-tools`
- `doctor --check-tools`
- `tools status`
- `tools probe web-search`
- `tools probe mcp <server>`
- `tools file-search index`
- `audit tool-calls --tool <type>`

### MCP

TraceLab 自身 MCP server 可暴露：

- hosted tool status。
- tool call audit query。
- file search index status。
- MCP backend status。

TraceLab 自身 MCP server 不应自动暴露高风险 tool execution；执行仍走 Responses runtime policy。

TraceLab 自身 MCP server 与 hosted `tools.mcp` 是两件事：

- `mcp.enabled/path`：让外部 MCP client 连接 TraceLab，查询 TraceLab 暴露的管理/审计工具。
- `tools.mcp`：让 TraceLab Responses runtime 作为 MCP client，连接外部 MCP server 并替模型执行 allowlisted hosted MCP tool。
- `codex mcp login tracelab-remote` 只适用于 Codex 管理自己的 MCP server 登录能力；TraceLab hosted MCP 的 bearer/OAuth 状态由 TraceLab 服务端配置和诊断负责。

## 存储与迁移

短期不需要为 Phase 0/1 新增表。

Phase 2 MCP：

- 可复用 `app_settings` 保存安全 overlay。
- 可复用 `tool_call_audits` 和 `execution_events`。
- 不保存 bearer token，只保存 env var 名或 key hint。

Phase 3 file_search 需要新增表，Postgres versioned migration 是生产主路径。SQLite 仅保留测试/fallback 最小 schema。

Phase 4 code_interpreter 可能需要：

- `tool_artifacts`
  - id
  - response_id
  - tool_call_id
  - artifact_type
  - filename
  - mime_type
  - size
  - sha256
  - storage_uri
  - created_at

Artifacts 不应放进 `.http` cassette。

## Codex Compatibility

目标不是让 Codex 动态发现 TraceLab hosted tools。公开 Codex 配置中，自定义 provider 主要声明 base URL、wire API、认证和 headers；Codex 自身 web_search/MCP/computer-use 是 Codex agent 的本地或官方托管工具开关，不等同于 TraceLab server-side hosted tools。

TraceLab 要做的是：

- 已落地首切：`responses_server.codex_compat` 可在服务端显式启用，当 Codex/Responses 请求缺少 `tools` 时，TraceLab 可按 allowlist 自动注入当前已启用的 hosted `web_search` descriptor，并把 `tool_choice` 默认成 `auto`。该能力默认关闭，并通过 `config inspect` / `doctor` 暴露配置与诊断。
- 当 Codex 或其它 Responses 客户端真的向 TraceLab `/v1/responses` 发送 `tools` 时，尽量兼容 OpenAI Responses hosted tool schema。
- 维护 Codex fixture，记录真实 Codex 可能发送的 Responses 请求形状。
- 对未知/暂未支持工具返回稳定 safe error，而不是静默忽略。
- 输出 `models codex-config` 时继续提供 model/provider/profile 建议；工具支持能力通过文档和 diagnostics 表达，不伪造 Codex 不消费的动态 metadata。

当前边界：

- 自动注入只覆盖 `web_search` / `web_search_preview`。`mcp` descriptor 需要 server/tool/approval 语义，后续在 MCP descriptor compatibility 阶段补齐，不自动注入裸 `mcp`。
- 默认不覆盖客户端已传入的 `tools`；第一版只处理 tools absent 的 Codex 请求。

Codex/客户端要做的是：

- 选择是否在 request 中发送 hosted tool descriptor。
- 呈现 TraceLab 返回的 tool output、failed/rejected、approval required、artifact/citation link。
- 对 approval/OAuth 等交互能力提供用户入口；TraceLab 服务端负责策略和执行，客户端负责交互。
- 不依赖 `codex mcp login` 来登录 TraceLab hosted MCP backend；那条命令面向 Codex 自己的 MCP server 配置，不是 TraceLab `tools.mcp`。

建议 fixture 分类：

- Codex 无工具普通请求。
- Codex function tool 请求。
- Codex web_search 请求。
- forced tool choice。
- stream web_search。
- unknown hosted tool。
- large tool output。
- cancelled stream tool call。

## 风险与缓解

### Prompt Injection

风险：web_search、file_search、MCP、computer_use 都可能读取不可信内容。

缓解：

- 工具结果作为 untrusted context 注入。
- 结果摘要加来源标记。
- 默认截断。
- 不把工具结果写入长期 memory。
- Monitor 标记外部来源。

### Secret Leakage

风险：MCP headers、API keys、file contents、code output 泄露。

缓解：

- token 只从 env 读取。
- audit 只写 hint/hash。
- raw payload 默认 redacted。
- CLI/Monitor 不展示 secret。

### Remote Code Execution

风险：code_interpreter 和 external_command。

缓解：

- 默认关闭。
- sandbox 强制。
- no shell by default。
- resource limits。
- no network by default。
- no host mount。

### Tool Side Effects

风险：MCP tool 可能写外部系统。

缓解：

- allowlist。
- read-only annotation 优先。
- destructive tool 默认拒绝。
- 后续引入 approval policy。

### Replay Drift

风险：工具结果不可复现导致测试 replay 不稳定。

缓解：

- `.http` cassette 继续只作为上游 HTTP exchange 事实源。
- 工具执行事件单独 audit。
- 测试使用 mock tool provider。
- 可选工具结果 cassette 后续单独设计，不混入现有 recordfile 格式。

## 验证策略

默认测试不依赖网络、Docker、Postgres 或真实 MCP server。

基础验证：

- `go test ./internal/responses/...`
- `go test ./internal/monitor`
- `go test ./cmd/server`
- `task test:codex-fixtures`

DSN-gated：

- Postgres migration。
- pgvector/file_search 查询。

Integration-gated：

- SearXNG web_search。
- MCP HTTP server。
- Docker code_interpreter。
- Browser computer_use。

每个工具必须有：

- disabled path。
- enabled happy path。
- backend unavailable。
- invalid descriptor。
- invalid arguments。
- timeout。
- oversized result。
- stream path（如支持）。
- audit redaction assertion。
- cancellation assertion（如支持）。

## 推荐开发顺序

1. Phase 0：contract/test inventory。已完成。
2. Phase 1：web_search 迁入 registry。已完成。
3. Phase 2A：mock MCP executor。已完成。
4. Phase 2B：HTTP MCP `tools/call` executor。已完成。
5. Phase 2C：`tools.mcp` config/doctor/runtime assembly。已完成。
6. Phase 2D：服务端 MCP `tools/list` discovery/status + Monitor/CLI tool status。
7. Phase 2E：MCP incremental stream 简单路径。
8. Phase 2F：approval request/decision contract；Monitor/客户端 UI 后续接入。
9. Phase 3A：file store + deterministic lexical search。
10. Phase 3B：embedding + pgvector。
11. Phase 3C：客户端/Monitor file upload、index status、citation 展示。
12. Phase 4A：code_interpreter sandbox spike。
13. Phase 4B：artifact store。
14. Phase 4C：客户端/Monitor artifact 展示与高风险确认。
15. Phase 5A：computer_use threat model/design review。
16. Phase 5B：browser-use sandbox spike。

## Done Definition

某个 hosted tool 标记为 production-ready 前必须满足：

- 默认关闭，显式启用。
- 文档写明配置和风险。
- `doctor` 能检查依赖。
- Monitor 能看到状态和最近失败。
- 有 offline unit tests。
- 有 integration-gated tests。
- 有 stream/non-stream 行为说明。
- 有 audit redaction 测试。
- 有 safe error contract。
- 不破坏 replay 兼容。
- 不要求测试访问真实外部服务。
