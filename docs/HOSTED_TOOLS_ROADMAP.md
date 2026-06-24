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

截至本文日期，TraceLab 已有：

- `/v1/responses` server-mode。
- 基于 OpenAI-compatible Chat Completions upstream 的 Responses 编排。
- hosted `web_search` / `web_search_preview` 首切，provider 支持 `mock` 和 SearXNG。
- 普通 `function` tool 的客户端回路。
- 注册式 server-side function executor，支持 `static_response` 和受限 `external_command`。
- stream tool loop 首切：已注册 function executor 和 hosted web_search 可输出 started/done item，并继续最终文本 delta。
- `tool_call_audits` 与 `execution_events` 已承接 web_search、configured function executor 和 unsupported hosted tool rejected 记录。
- 对未实现 hosted tools（`mcp`、`file_search`、`code_interpreter`、`computer_use_preview`）已有稳定 `unsupported_tool` 错误和 rejected audit。

主要差距：

- Hosted tool 执行器仍散落在 web_search/function executor 逻辑中，缺少统一 registry。
- `mcp`、`file_search`、`code_interpreter`、`computer_use_preview` 均未真实执行。
- Tool lifecycle 还缺少完整跨轮状态、取消、重试、approval、artifact、citation、stream event 细节。
- Monitor 还没有统一的 Hosted Tools 配置/状态/调用审计页面。
- Codex fixtures 主要覆盖已落地路径，未覆盖完整 hosted tool schema 矩阵。

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
- `mcp` 第一版支持 incremental stream 的简单路径。
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

| Tool | 当前状态 | 第一阶段目标 | 长期目标 | 风险 |
| --- | --- | --- | --- | --- |
| `web_search` | 已有 SearXNG/mock 首切 | 迁入统一 registry，补 Codex fixtures 和 event 对齐 | 支持更多 search provider、citations、source action 细节 | prompt injection、版权/引用 |
| `mcp` | rejected only | 连接 configured MCP server，执行 allowlisted tool | 支持 OAuth、approval、resources/prompts、streaming results | 外部工具副作用、认证 |
| `file_search` | rejected only | 本地文件/vector store 检索，返回片段和 citation | 对齐 OpenAI vector store/files 语义 | 数据权限、索引成本 |
| `code_interpreter` | rejected only | Docker/isolated process sandbox，执行短任务，产出 artifacts | 多轮 session、package policy、artifact browser | RCE、资源消耗 |
| `computer_use_preview` | rejected only | 设计期，不先实现 | 隔离浏览器/桌面 session，screenshot/action loop | 高风险自动化、环境隔离 |

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
        default_approval: "auto_readonly"
```

第一版范围：

- Streamable HTTP MCP。
- Bearer token/env headers。
- `tools/list` capability discovery。
- `tools/call` execution。
- enabled_tools/disabled_tools。
- read-only default policy。
- result text/json 截断与 redaction。
- MCP server instructions 只进入 internal policy summary，不直接注入用户 prompt，除非显式配置。

暂不做：

- OAuth login flow。
- MCP resources/prompts。
- destructive approval UI。
- sampling。
- long-running background tool calls。

Runtime 行为：

- Responses `tools` 中 `type=mcp` 的 descriptor 指向 server/tool。
- Registry 把允许的 MCP tool 暴露为 Chat Completions function tool。
- 模型请求 function call 后，executor 调用 MCP `tools/call`。
- MCP result 转成 Chat tool result message，并追加 Responses `mcp_call` 或兼容 output item。

验收：

- 可用 mock MCP server 做离线测试。
- MCP server 不可达时返回 failed/rejected safe error。
- disabled/denylisted tool 不会暴露给模型。
- audit 记录 server id、tool name、status、duration，不记录 bearer token。
- Monitor/Audit 可查 MCP tool call。

### Phase 3: File Search

目标：提供最小可用的 private file search hosted tool。

建议先实现 TraceLab-native file store，不立即完整复刻 OpenAI vector store API。

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

目标不是让 Codex 动态发现 TraceLab hosted tools。公开 Codex 配置中，自定义 provider 主要声明 base URL、wire API、认证和 headers；Codex 自身 web_search 是 Codex agent 的本地/托管工具开关。

TraceLab 要做的是：

- 当 Codex 或其它 Responses 客户端真的向 TraceLab `/v1/responses` 发送 `tools` 时，尽量兼容 OpenAI Responses hosted tool schema。
- 维护 Codex fixture，记录真实 Codex 可能发送的 Responses 请求形状。
- 对未知/暂未支持工具返回稳定 safe error，而不是静默忽略。
- 输出 `models codex-config` 时继续提供 model/provider/profile 建议；工具支持能力通过文档和 diagnostics 表达，不伪造 Codex 不消费的动态 metadata。

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

1. Phase 0：contract/test inventory。
2. Phase 1：web_search 迁入 registry。
3. Phase 2A：mock MCP executor。
4. Phase 2B：HTTP MCP client executor。
5. Phase 2C：Monitor/CLI tool status。
6. Phase 3A：file store + deterministic lexical search。
7. Phase 3B：embedding + pgvector。
8. Phase 4A：code_interpreter sandbox spike。
9. Phase 4B：artifact store。
10. Phase 5：computer_use design review。

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
