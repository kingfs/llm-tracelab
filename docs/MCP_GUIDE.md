# MCP Guide

## Purpose

This document describes the current `llm-tracelab` MCP server surface.

The current goal is narrow and deliberate:

- expose local trace/session/upstream inspection to AI agents
- reuse existing monitor/store behavior

This is the `M1` slice from [Agent Evolution Roadmap](./AGENT_EVOLUTION_ROADMAP.md).

## Current Status

Current MCP support is:

- transport: streamable HTTP
- implementation library: official `github.com/modelcontextprotocol/go-sdk`
- scope: local inspection, failure-oriented triage, and TraceLab system-event diagnostics

Current MCP support is not:

- a hosted control plane
- a replacement for replay or monitor storage

## Run

Start the main server with the same config file used by the proxy and monitor:

```bash
go run ./cmd/server serve -c config/config.yaml
```

When `mcp.enabled: true`, the server exposes MCP over streamable HTTP on `monitor.port`.

Default endpoint:

- `http://localhost:<monitor.port>/mcp`

Recommended config:

```yaml
monitor:
  port: "8081"

mcp:
  enabled: true
  path: "/mcp"
```

Authentication:

- MCP reuses the same user-backed personal token as the proxy API
- create the first user with `llm-tracelab auth init-user`
- log in to Monitor and create a token from the `Tokens` page, or use `llm-tracelab auth create-token`
- clients must send `Authorization: Bearer <token>`

## Tool Surface

### `list_traces`

List recorded traces with pagination and optional filters:

- `page`
- `page_size`
- `provider`
- `model`
- `q`

### `get_trace`

Get one trace detail by `trace_id`.

Optional input:

- `include_raw`: when true, also return raw HTTP request/response bytes

### `list_sessions`

List grouped sessions with pagination and optional filters:

- `page`
- `page_size`
- `provider`
- `model`
- `q`

### `list_upstreams`

List upstream analytics.

Optional filters:

- `window`: `1h`, `24h`, `7d`, `all`
- `model`

### `query_failures`

Return failed traces from a paginated trace scan.

Inputs match `list_traces`, but the result is filtered to requests with:

- non-2xx `status_code`, or
- non-empty `error`

Important limitation:

- this tool currently filters one paginated `list_traces` result
- it is not yet a dedicated failure index

### `summarize_failure_clusters`

Summarize failed traces from a paginated scan by:

- reason
- status
- model
- provider
- endpoint
- upstream

It also returns bounded top failed traces.

### `list_system_events`

List TraceLab runtime and derived-pipeline events.

Optional filters:

- `page`
- `page_size`
- `status`: `unread`, `read`, `resolved`, `ignored`, `all`
- `severity`: `info`, `warning`, `error`, `critical`
- `source`
- `category`
- `q`
- `window`: `1h`, `24h`, `7d`, `all`

### `get_system_event`

Get one system event by `event_id`.

Optional input:

- `include_details`: when true, include `details_json`

### `summarize_system_events`

Return compact system event counts and newest events for agent triage.

Optional inputs:

- `window`: `1h`, `24h`, `7d`, `all`
- `status`: default `unread` for newest events

### `query_unread_system_events`

Return unread system events ordered by severity and recency.

Optional inputs:

- `limit`
- `min_severity`: default `warning`

## Design Notes

The MCP server intentionally reuses existing monitor JSON APIs in-process rather than adding a parallel query stack.

This keeps the first MCP slice:

- thin
- replay-safe
- low-risk
- focused on inspection and triage rather than local workflow orchestration
- aligned with current monitor semantics

System event MCP tools are read-only. Write-capable event tools such as marking events read or resolved should remain opt-in behind an explicit milestone and configuration gate.

## Next Likely Step

The next MCP-focused step should be:

1. keep comparison local and deterministic
2. keep experiment persistence lightweight and additive
3. add richer evaluators only after current score signals prove actionable

## Current Evaluator Set

Current deterministic evaluator set:

- `http_status_2xx`
- `no_recorded_error`
- `response_has_body`
- `ttft_le_2000ms`
- `total_tokens_le_32000`
- `tool_calls_declared`
- `tool_call_arguments_json`

This set is intentionally objective and cheap.

Default baseline evaluator version is `baseline_v4`.

Current built-in profiles:

- `baseline_v1`: status/error/body checks only
- `baseline_v2`: `baseline_v1` plus TTFT and total-token budgets
- `baseline_v3`: `baseline_v2` plus declared tool-call conformance
- `baseline_v4`: `baseline_v3` plus tool-call argument JSON validation

The latency and token thresholds are currently hard-coded so results stay deterministic and easy to compare across runs.

`tool_calls_declared` checks that every recorded response tool call matches a tool name declared in the request. If no tool call occurred, the check passes.

`tool_call_arguments_json` checks that each recorded response tool call argument payload is valid JSON. Empty argument strings are treated as acceptable.

It is not intended to replace human judgment or model-graded quality review.
