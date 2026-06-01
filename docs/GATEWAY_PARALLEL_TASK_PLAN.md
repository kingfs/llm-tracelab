# Gateway Evolution Parallel Task Plan

This document records the current parallel implementation split for the gateway
evolution branch. It exists to keep independently developed worktrees aligned
with the product boundary in `GATEWAY_REFERENCE_EVOLUTION_DESIGN.md`.

## Baseline

All parallel branches start from `feature/gateway-evolution` at:

```text
76f2b2f feat: show routing decisions in trace detail
```

Current completed foundation:

- routing decision events are written into V3 cassettes
- routing decisions are exposed through MCP
- Trace Detail shows routing candidates and event payloads
- candidate base URLs are redacted before event recording

## Parallel Workstreams

### A. Shared Redaction Helper

Branch: `feature/gateway-redaction`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-redaction`

Owner scope:

- shared URL/event redaction helper
- proxy routing candidate redaction call site
- redaction tests

Constraints:

- do not change routing semantics
- do not change sticky session behavior
- do not add storage schema

Acceptance:

- URL userinfo passwords are redacted
- sensitive query parameter values are redacted
- non-URL diagnostic strings remain unchanged
- existing proxy routing candidate events use the shared helper

### B. In-Memory Sticky Routing MVP

Branch: `feature/gateway-sticky-session`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-sticky-session`

Owner scope:

- in-memory sticky key extraction and binding
- router selection integration
- sticky routing events
- router/proxy tests

Constraints:

- no database schema
- no credential/account model yet
- no change to requests without sticky keys

Acceptance:

- repeated requests with the same sticky key prefer the same available target
- unavailable or excluded sticky targets produce a break/rebind path
- cassette events explain hit/miss/bind/break

### C. Routing Event Failure Clustering

Branch: `feature/gateway-failure-clustering`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-failure-clustering`

Owner scope:

- MCP failure reason extraction from V3 routing events
- failure clustering tests

Constraints:

- no database schema
- only scan traces already in the requested page
- do not change router selection

Acceptance:

- `routing.filtered.attributes.routing_failure_reason` wins over generic status
- `routing.retry_queue_saturated` remains classified distinctly
- existing failure clustering output remains compatible

## Integration Order

Recommended merge order:

1. Shared redaction helper.
2. Failure clustering.
3. Sticky routing.

Reasoning:

- redaction is the smallest and may be reused by later branches
- failure clustering only consumes existing events
- sticky routing changes selection semantics and should be reviewed last

## Non-Goals For This Parallel Batch

- payment, recharge, or SaaS quota distribution
- credential/account storage model
- persistent sticky state
- new database migrations
- broad Monitor navigation redesign
