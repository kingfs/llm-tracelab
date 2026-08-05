# Codex Responses Compatibility Profile

本文固化 `llm-tracelab` Responses server-mode 面向 Codex/OpenAI SDK
客户端的首切兼容合约。它不是完整 OpenAI Responses API parity，也不改变项目
record/replay proxy 的主线：非 Responses 请求仍按现有协议感知代理、录制和 replay
路径工作。

适用范围：

- `responses_server.enabled=true` 的本地 `/v1/responses` server-mode。
- 上游为 OpenAI-compatible Chat Completions API，由 TraceLab 做最小 Responses
  semantic orchestration。
- Codex/OpenAI SDK 常见的 text create、stream text、普通 function call、
  `function_call_output` continuation 和普通 hosted `web_search` descriptor。
- 只读、离线 fixture 位于 `tests/fixtures/codex/`，不依赖真实 Codex、真实模型或网络。

## 最小兼容合约

### Text create

客户端可以发送 `POST /v1/responses`，包含 `model`、`instructions`、`input`、
`store` 和 `metadata`。当前 runtime 会把 `instructions` 映射为内部 Chat
Completions system message，把 string `input` 映射为 user message，并返回
OpenAI Responses 风格 response object。

`model` 可以由请求显式提供；若请求省略，则必须配置 `responses_server.default_model`。
`store` 默认按 runtime 当前策略处理；面向 Codex 建议显式 `store:true`，以便后续
`previous_response_id` continuation 和 audit 查询有稳定状态。

Fixture:

- `tests/fixtures/codex/text_create_request.json`
- `tests/fixtures/codex/text_create_expected_response.json`

### Streaming text

客户端可以在 create request 中设置 `stream:true`。最小事件序列应包含：

```text
response.created
response.in_progress
response.output_item.added
response.content_part.added
response.output_text.delta*
response.output_text.done
response.content_part.done
response.output_item.done
response.completed
data: [DONE]
```

当前实现已支持简单文本输出真实增量转发；auto compact 等复杂路径仍可能 fallback
到 deferred SSE envelope。

Fixture:

- `tests/fixtures/codex/stream_text_request.json`
- `tests/fixtures/codex/stream_text_events.ndjson`

### Function call and continuation

普通 `tools[type=function]` 是 client-owned by default：

- TraceLab 将 function schema 转给内部 Chat Completions。
- 模型返回的 Chat `tool_calls[]` 会保存为 Responses `function_call` output item。
- 未配置 server-side executor 时，TraceLab 不执行客户端函数。
- 客户端后续通过 `previous_response_id` 加 `input[]` 中的
  `function_call_output` 继续对话。

配置 `responses_server.function_executors.enabled=true` 且声明同名
`static_response` 或 `external_command` executor 后，该 function 才变成
server-owned opt-in 执行；这不是 Codex 最小兼容合约的默认行为。

Fixtures:

- `tests/fixtures/codex/function_call_request.json`
- `tests/fixtures/codex/function_call_expected_response.json`
- `tests/fixtures/codex/function_call_output_continuation_request.json`

### Ordinary hosted web_search descriptor

Codex/OpenAI SDK 可能在普通请求中携带 `{"type":"web_search"}` 或
`{"type":"web_search_preview"}` descriptor。首切兼容要求是：

- descriptor 可作为请求输入被解析和审计。
- 当 `tools.web_search.enabled=false` 或没有可用 provider 时，普通 descriptor
  不应破坏 text create/stream text；它不会被暴露给上游 Chat Completions。
- 当 `tools.web_search.enabled=true` 且 provider 就绪时，TraceLab 可把
  `web_search` 映射为内部 function tool，由 model 显式 tool call 后执行
  server-side web search。
- 强制 `tool_choice` 为 `web_search` / `web_search_preview` 但 provider 不可用时，
  当前 runtime 返回 OpenAI-style error envelope，message 中包含
  `unsupported hosted tool "web_search"`，并在可用 audit store 中写入
  `tool_call_audits` rejected read model。

Fixture:

- `tests/fixtures/codex/ordinary_web_search_descriptor_request.json`

### Unsupported hosted tools

`mcp`、`file_search`、`code_interpreter`、`computer_use_preview` 等 hosted tool
runtime 尚未实现。当前首切合约是保守的：

- 不声称会执行这些工具。
- 不伪造 file citations、retrieved chunks、code outputs、MCP tool results 或
  computer-use side effects。
- 强制 `tool_choice` 为这些 hosted tool 时，runtime 返回稳定 OpenAI-style
  error envelope，`code` 为 `unsupported_tool`，message 中包含
  `unsupported hosted tool "<tool>"`，并写入不含 raw descriptor/payload 的
  `tool_call_audits` rejected 记录。

Fixture:

- `tests/fixtures/codex/unsupported_hosted_tool_expected_error.json`

## 能力矩阵

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| non-streaming text create | 已支持 | Responses request 映射到内部 Chat Completions，返回 Responses object。 |
| streaming text create | 已支持首切 | 简单 text delta 路径真实增量；复杂 compact path 仍可 fallback。 |
| stored response lookup | 部分支持 | runtime store 支持 response state 与 continuation；尚未声明通用 `GET /v1/responses/{id}` HTTP 合约。 |
| input item lookup | 已支持 | `/v1/responses/:id/input_items` 已接入。 |
| `previous_response_id` continuation | 已支持 | 加载历史 items 后继续对话。 |
| function call output item | 已支持 | Chat tool call 转 Responses `function_call`。 |
| `function_call_output` continuation | 已支持 | 客户端 tool result 转内部 Chat `tool` message。 |
| server-side function executor | 部分支持 | 默认 client-owned；YAML opt-in executor 才 server-owned。 |
| ordinary `web_search` descriptor | 部分支持 | 可解析；provider 就绪时可执行 hosted search；未就绪时不应阻断普通 text path。 |
| forced `web_search` with no provider | 已支持错误和审计 | 返回 server error envelope，message 指出 unsupported hosted tool，并写 rejected tool_call audit。 |
| MCP hosted tool runtime | 稳定拒绝并审计 | 当前 MCP 是对外排障 server，不是 Responses runtime 内部 tool executor；强制执行时返回 `unsupported_tool` 并写 rejected audit。 |
| file search hosted runtime | 稳定拒绝并审计 | 无 vector store/retrieval/citation runtime；强制执行时返回 `unsupported_tool` 并写 rejected audit。 |
| code interpreter hosted runtime | 稳定拒绝并审计 | 无 sandboxed code runtime；强制执行时返回 `unsupported_tool` 并写 rejected audit。 |
| Codex TOML profile generation | 已支持首切 | `models codex-config <model>` 离线读取 `responses_server.model_profiles`，输出 JSON envelope 与 Codex TOML 建议；本地 SQLite app DB 可用时还会只读检查 `model_catalog` / `channel_models` drift；显式传入 `--codex-config <path>` 时会只读检查本地 Codex TOML drift。 |
| Codex fixture runner | 已支持离线 gate | `task test:codex-fixtures` 运行 focused 离线 Go tests，枚举并校验当前 fixture inventory、JSON/NDJSON 结构、HTTP handler reachability 和最小 runtime/parser 对齐；不是完整真实 Codex/e2e runner。 |

## 配置建议

最小本地 Codex/OpenAI SDK 调试配置建议：

```yaml
responses_server:
  enabled: true
  path: /v1/responses
  default_model: local-test-model
  force_store: true

database:
  driver: sqlite
  auto_migrate: true

tools:
  web_search:
    enabled: false
    provider: disabled
```

需要真实 hosted `web_search` 时，显式打开 mock 或 SearXNG provider，并先用
`config inspect` 确认有效配置：

```yaml
tools:
  web_search:
    enabled: true
    provider: searxng
    base_url: http://127.0.0.1:8080
    max_results: 5
```

上游 channel/target 应声明或通过 probe 填补 API surface：

- 本地 Responses server-mode 的内部 model call 需要可用的
  OpenAI-compatible Chat Completions endpoint。
- 带 function tools 的请求需要目标支持 tool calling；显式
  `capabilities.tool_calling:false` 的 target 不会被选择。
- 不要把 Responses-native 且关闭 chat capability 的 target 配成 server-mode
  内部 Chat Completions 后端。

## Codex TOML 生成命令

`llm-tracelab models codex-config <model>` 是离线配置建议生成器，不会探测上游
provider、运行真实 Codex 或读取真实 API key。命令支持全局
`--format text|json`，并支持可选 `--codex-config <path>`：

- JSON 输出使用稳定 envelope，`command` 为 `models.codex_config`。
- `result.profile` 包含 `model_provider`、`model`、`model_context_window` 和
  `model_auto_compact_token_limit`。
- `result.provider.base_url` 根据 `server.port` 和 `responses_server.path` 推导；
  默认 `/v1/responses` 会生成 `http://127.0.0.1:<port>/v1`，`wire_api` 固定为
  `responses`。
- `result.diagnostics` 标注 matched profile、compact token limit 来源、
  item-count compact threshold 来源、Responses server 是否启用，以及本地 SQLite
  app DB 可用时的 `model_catalog` / `channel_models` 命中和 drift warnings。
- 未传 `--codex-config` 时，`result.diagnostics.codex_config.status` 为
  `not_configured`，命令不会读取用户机器上的真实 Codex 配置文件。
- 传入 `--codex-config <path>` 时，命令只读解析该 TOML，检查
  `[profiles.<model>]` 和 `[model_providers.llm-tracelab]` 是否存在，并对比
  `model_provider`、`model`、`model_context_window`、
  `model_auto_compact_token_limit`、provider `base_url`、`wire_api` 和 `env_key`。
  JSON diagnostics 会输出 path、present/readable/parsed、profile/provider
  present、字段级 expected/actual/matched 状态和 drift warnings；text 输出会给出
  简洁 `codex_config` summary。
- `result.warnings` 会提示 `responses_server.enabled=false`、未匹配 profile、
  profile 缺少 `context_window_tokens` 或 path 无法按 Codex `wire_api=responses`
  习惯推导；当 profile 命中但 catalog/channel 缺少该 model，或 catalog 与
  channel 只有一侧存在该 model，或显式传入的 Codex TOML 存在 drift 时，也会输出
  drift warning。
- text 输出包含可复制 TOML 片段，使用 `env_key = "LLM_TRACELAB_API_KEY"` 占位，
  不输出配置文件中的真实 upstream API key、header secret、数据库 DSN 或 TOML 中的
  API key/token；`base_url` actual 值会先脱敏再输出。

Profile 匹配顺序固定为：先按 `responses_server.model_profiles[].name` exact
匹配，再按 `pattern` wildcard 匹配。无匹配时命令仍成功，模型上下文窗口和 Codex
auto compact token limit 输出为 `0`，并给出 warning。当前首切没有独立的 Codex
compatibility 字段，因此 `model_auto_compact_token_limit` 在有 context window 时
保守回退为 `context_window_tokens` 的 80%。

Catalog/channel drift 诊断只在配置为 SQLite 且 application DB 文件已存在时打开
store，并以 `AutoMigrate:false` 只读查询当前模型是否存在于 `model_catalog` 与
`channel_models`。DB 不存在、`:memory:`、非 SQLite 或打开失败时，命令保持原离线
行为并把 catalog/channel source 标记为 `unavailable`；不会连接 Postgres，也不会输出
DSN 或 secret。

本地 Codex TOML drift 诊断只在显式传入 `--codex-config` 时执行。文件不存在、
不可读或 TOML 解析失败时命令仍成功，`diagnostics.codex_config.status` 分别标记
`missing`、`unreadable` 或 `parse_error`，并通过 warnings 说明问题；不会 panic，
也不会回显完整文件内容或 parser 上下文。

## 排障入口

- `llm-tracelab -c config.yaml config inspect --format json`：查看脱敏后的
  effective config，包括 Responses server、database、web_search、provider_probe
  和 upstream 摘要。
- `llm-tracelab -c config.yaml audit query --response-id resp_x --include-events --include-exchanges --format json`：
  查询 Responses request audit、execution events 和内部 upstream exchange
  correlation。当前主要支持 `--response-id` / `--request-audit-id`。
- Monitor Audit 页面：通过 `/api/responses/audit/trace` 查看同一套 audit trace，
  适合人工检查 runtime 状态、tool events 和 upstream cassette 关联。
- MCP `responses_audit_trace`：给 agent 使用的只读 trace 查询入口，复用 Monitor/store
  事实源。
- `llm-tracelab -c config.yaml provider probe --id <upstream-id> --format json`：
  手动检查上游 API surface；`provider probe-report` 和 `provider probe-apply`
  可用于批量只读报告或保守补全 managed channels。

## Fixture 使用约定

`tests/fixtures/codex/` 是 focused 离线 Go tests 的稳定输入。当前
`task test:codex-fixtures` 会运行 fixture runner，并 pin 当前 fixture inventory；
新增或删除 fixture 时必须同步更新 runner 期望列表与对应契约测试。新增 fixture 时遵守：

- 不依赖真实 Codex、真实模型、真实网络或当前日期。
- 使用占位模型 `local-test-model` 和稳定 id。
- 请求 fixture 保留 Codex/OpenAI SDK 兼容字段，但不要放真实密钥、路径或用户数据。
- expected error fixture 只记录 machine-readable envelope 与稳定 code/message 片段；
  不要求 runtime 今天已经完全按该 fixture 自动回归。
