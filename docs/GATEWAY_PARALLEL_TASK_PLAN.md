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

Integration branch:

```text
feature/gateway-evolution
/data/src/github.com/kingfs/llm-tracelab-gateway-evolution
```

## Parallel Workstreams

### A. Shared Redaction Helper

Branch: `feature/gateway-redaction`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-redaction`
Status: integrated as `28bb173 refactor: share routing redaction helper`

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

Stage review:

- Scope stayed orthogonal: only shared display redaction and proxy call sites changed.
- No router semantics, cassette raw payloads, replay code, or storage schema changed.
- `task check:quick` passed after integration.

### B. In-Memory Sticky Routing MVP

Branch: `feature/gateway-sticky-session`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-sticky-session`
Status: integrated as `e4274a8 feat: add in-memory sticky routing`

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

Stage review:

- Scope stayed in memory: no database schema, credential model, or persistent sticky state.
- Integration changed cassette event attributes to write `sticky_key_fingerprint` instead of raw `sticky_key`.
- Sticky does affect routing only when a sticky key is detected; requests without sticky keys keep the normal selection path.
- A separate deterministic test fix was committed as `6da2d64 test: make fallback routing selection deterministic`.
- `task check:quick` passed after integration.

### C. Routing Event Failure Clustering

Branch: `feature/gateway-failure-clustering`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-failure-clustering`
Status: integrated as `9949d7c feat: cluster failures by routing events`

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

Stage review:

- Scope stayed read-only over current-page traces and V3 cassette prelude events.
- Existing MCP output remains additive: routing event reasons enrich failure rows and cluster labels without changing router behavior.
- `task check:quick` passed after integration.

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

## Final Integration Review

Integrated commits on `feature/gateway-evolution`:

- `28bb173 refactor: share routing redaction helper`
- `9949d7c feat: cluster failures by routing events`
- `e4274a8 feat: add in-memory sticky routing`
- `6da2d64 test: make fallback routing selection deterministic`

Validation:

- `task check:quick` passes on the integration branch.

Destructive analysis:

- No destructive migrations.
- No cassette format rewrite.
- No replay transport behavior change.
- No removal of legacy routing events or V2 read compatibility.
- New sticky routing can alter upstream choice for requests carrying recognized sticky keys, by design.

Existing logic impact:

- Proxy recording remains additive: new `routing.sticky.*` events are prelude metadata, raw request/response bytes remain untouched.
- MCP failure clustering now prefers structured routing failure evidence when present, then falls back to previous generic classification.
- Router selection is unchanged for requests without sticky keys; with sticky keys, available existing bindings are preferred until excluded/unavailable.
- Redaction is centralized for display/event URL fields; raw upstream request URLs are not rewritten.

Closed-loop analysis:

- Route decision capture explains candidates, selection, filtering, sticky affinity, and outcome in cassette events.
- MCP tools can answer both per-trace routing decisions and cross-trace failure clusters from the same cassette evidence.
- Monitor trace detail can inspect routing events without a new storage dependency.
- Tests cover redaction, router sticky selection, proxy sticky event recording, and MCP failure clustering.

Execution-flow analysis:

1. Request enters proxy and router extracts endpoint/model/request features.
2. Router builds candidate decisions and applies health, endpoint, model, exclusion, and sticky constraints.
3. Router returns the selected target plus `DecisionTrace`.
4. Proxy records classified/candidate/selected/sticky/filter events into the V3 prelude and forwards the raw request upstream.
5. Proxy records routing outcome with status/error/duration.
6. Replay still consumes raw cassette bytes; analysis and UI consume derived prelude events.

## Next Orthogonal Workstreams

The next parallel batch should stay orthogonal:

- Credential decision chain: model channel credentials as route-target sub-identities, but keep storage migration and concurrency separate.
- Limit events: add global/token/channel concurrency and queue rejection events without changing sticky routing.
- Monitor routing aggregation: aggregate existing routing and sticky events into UI views, with no hot-path router changes.
- MCP sticky/failure drilldown: query sticky break/rebind traces from cassette events, with no storage schema change.
