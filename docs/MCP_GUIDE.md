# MCP Guide

## Purpose

This document describes the current `llm-tracelab` MCP server surface.

The current goal is narrow and deliberate:

- expose local trace/session/upstream inspection to AI agents
- reuse existing monitor/store behavior
- keep the first MCP surface read-only

This is the `M1` slice from [Agent Evolution Roadmap](./AGENT_EVOLUTION_ROADMAP.md).

## Current Status

Current MCP support is:

- transport: `stdio`
- implementation library: official `github.com/modelcontextprotocol/go-sdk`
- scope: read-only inspection tools

Current MCP support is not:

- a hosted control plane
- a write-capable mutation API
- a replacement for replay or monitor storage

## Run

Start the MCP server with the same config file used by the main proxy:

```bash
go run ./cmd/server mcp -c config/config.yaml
```

The server reads the local `output_dir` and exposes tools over standard input/output.

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

### `get_session`

Get one grouped session by `session_id`.

### `list_upstreams`

List upstream analytics.

Optional filters:

- `window`: `1h`, `24h`, `7d`, `all`
- `model`

### `get_upstream`

Get one upstream drilldown by `upstream_id`.

Optional filters:

- `window`
- `model`

### `query_failures`

Return failed traces from a paginated trace scan.

Inputs match `list_traces`, but the result is filtered to requests with:

- non-2xx `status_code`, or
- non-empty `error`

Important limitation:

- this tool currently filters one paginated `list_traces` result
- it is not yet a dedicated failure index

## Design Notes

The MCP server intentionally reuses existing monitor JSON APIs in-process rather than adding a parallel query stack.

This keeps the first MCP slice:

- thin
- replay-safe
- low-risk
- aligned with current monitor semantics

## Next Likely Step

The next MCP-focused step should be:

1. preserve the current read-only tools
2. add replay-oriented tools
3. add dataset curation only after replay-backed tests are in place
