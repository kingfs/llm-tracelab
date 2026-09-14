# Integration Test Runbook

状态：集成测试前诊断指南
日期：2026-06-24

本文用于 TraceLab 在目标测试环境（例如 `http://<your-host>:<port>` 指向的自建实例）上做 Codex + 本地 Responses runtime + hosted tools 集成测试。目标是让自动化脚本在失败时能快速收集足够信息，而不是人工翻日志猜测。

## 默认测试假设

当前测试模板按“默认全开测试”处理：

- `responses_server.codex_compat.enabled=true`
- `responses_server.codex_compat.auto_inject_hosted_tools=["web_search"]`
- `tools.web_search.enabled=true`
- `tools.mcp.enabled=true`
- `mcp.enabled=true`
- `database.driver=postgres`

高风险工具仍不进入集成前默认路径：

- `file_search` 仍是 unsupported/rejected path。
- `code_interpreter` 仍不启用。
- `computer_use_preview` 仍不启用。

## 必填配置

部署前必须确认：

```yaml
database:
  driver: "postgres"
  dsn: "postgres://USER:PASSWORD@HOST:5432/DB?sslmode=disable"

responses_server:
  default_model: "qwen3.6-35b-a3b"

tools:
  web_search:
    enabled: true
    provider: "searxng"
    base_url: "http://searxng:8080"

  mcp:
    enabled: true
    servers:
      - id: tracelab-remote
        label: tracelab-remote
        url: "http://<your-host>:<mcp-port>/mcp"
        bearer_token_env: "DGX_API_KEY"
        enabled_tools: ["search", "fetch"]
        disabled_tools: []
        enabled: true
```

`DGX_API_KEY` 只应通过环境变量注入，不能写入 YAML。

## 预检命令

建议按顺序执行：

```bash
llm-tracelab -c config/config.yaml --format json config inspect
llm-tracelab -c config/config.yaml --format json doctor
llm-tracelab -c config/config.yaml --format json tools status
llm-tracelab -c config/config.yaml --format json models codex-config qwen3.6-35b-a3b
```

判断标准：

- `doctor` 不应出现 blocking failure。
- `tools status` 中 `ready_for_codex_web_search=true`。
- `tools.web_search.readiness=ready`。
- 如果要测试 hosted MCP，`tools.mcp.readiness=ready` 且至少有一个 enabled server。
- `models codex-config` 应输出 `model_context_window`、`model_auto_compact_token_limit`、`tool_output_token_limit`、`model_reasoning_effort`。

## Codex Web Search Smoke Test

命令：

```bash
codex -p dgx "搜索一下今日新闻"
```

期望：

- Codex 请求即使没有显式 tools，TraceLab 也会通过 `responses_server.codex_compat` 注入 `web_search`。
- `execution_events` 中应出现 `response.tool_call` 的 `web_search` started/completed。
- `tool_call_audits` 中应出现 `hosted:web_search` completed。
- 内部 `upstream_exchanges` 应有 TTFT，便于 prefill 速率分析。

## 失败时收集

优先用 `client_request_id`、`conversation_id` 或最新 request 查询。常用命令：

```bash
llm-tracelab -c config/config.yaml --format json audit query \
  --list \
  --status failed \
  --limit 20

llm-tracelab -c config/config.yaml --format json audit query \
  --client-request-id <client_request_id> \
  --include-events \
  --include-exchanges \
  --include-tools \
  --limit 200

llm-tracelab -c config/config.yaml --format json audit tool-calls \
  --latest-by-call \
  --limit 100
```

如果已知 `response_id`：

```bash
llm-tracelab -c config/config.yaml --format json audit query \
  --response-id <response_id> \
  --include-events \
  --include-exchanges \
  --include-tools \
  --limit 200
```

如果已知 `request_audit_id`：

```bash
llm-tracelab -c config/config.yaml --format json audit query \
  --request-audit-id <request_audit_id> \
  --include-events \
  --include-exchanges \
  --include-tools \
  --limit 200
```

## 日志定位字段

服务日志会输出用于关联数据库和请求链路的结构化字段：

- `request_audit_id`
- `response_id`
- `conversation_id`
- `client_request_id`
- `trace_id`
- `cassette_path`
- `upstream_id`
- `model`
- `endpoint`
- `status`
- `status_code`
- `duration_ms`
- `ttft_ms`
- `tool_type`
- `tool_name`
- `executor`
- `call_id`
- `stream`
- `server_id`
- `server_label`

日志不会输出 bearer token、raw authorization 或完整 tool payload。需要 raw payload 调试时只能使用显式高权限的 audit `--include-payloads`，且不建议在共享测试环境默认开启。

## 常见失败判断

- `ready_for_codex_web_search=false`：先看 `tools status` 的 reason 和 warnings。
- `web_search` 未触发：查 `response.request` accepted event 是否包含 `codex_compat.injected_hosted_tools`。
- `web_search` failed：查 `tool_call_audits` 的 `error_text` 和 `metadata.result_count/query`。
- MCP unknown server：确认 descriptor 的 `server_label` / `server_url` 与 `tools.mcp.servers` 完全匹配。
- MCP auth failure：确认 `bearer_token_env` 指向的环境变量存在，日志和 audit 只会显示 env 名，不显示值。
- TTFT 为 0：确认内部 upstream exchange 是否有响应 body；如果请求在连接阶段失败，TTFT 为空是预期。
