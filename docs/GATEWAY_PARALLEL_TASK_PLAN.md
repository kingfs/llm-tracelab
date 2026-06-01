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

## Parallel Batch 2 Plan

Baseline:

```text
6df900e docs: record gateway parallel integration review
```

Coordinator branch:

```text
feature/gateway-next-parallel
/data/src/github.com/kingfs/llm-tracelab-gateway-next-parallel
```

Mainline status:

- Batch 1 was fast-forward merged into `main` at `6df900e`.
- `task check:quick` passes on `main` after merge.
- Batch 2 worker branches were created from coordinator commit `4f0e941`; coordinator assignment commit is `007ef86`.

Batch 2 integration order:

1. MCP sticky/failure drilldown, because it only reads existing cassette events.
2. Monitor routing aggregation, because it should consume existing monitor data and cassette events.
3. Limit event primitives, because it introduces new hot-path rejection events but should not change routing selection.
4. Credential decision-chain design/spec, because it defines the next router/storage boundary and should be reviewed after seeing the read-side needs.

### D. MCP Sticky Drilldown

Branch: `feature/gateway-mcp-sticky-drilldown`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-mcp-sticky-drilldown`
Agent: Sagan (`019e8261-4a51-7331-9a35-22e28f71fa60`)
Status: integrated as `d4dcb6b feat: add mcp sticky routing drilldown`

Owner scope:

- Add an MCP tool that finds traces with `routing.sticky.*` events.
- Support optional filters for sticky status, upstream id, previous upstream id, and sticky fingerprint.
- Return compact rows with trace id, created time if available, status, upstream ids, fingerprint, and cassette path.
- Add deterministic tests using local cassette fixtures or temp V3 record files.

Constraints:

- Read-only over existing store/cassette files.
- No DB schema changes.
- No router/proxy behavior changes.
- Do not expose raw sticky keys; only use `sticky_key_fingerprint`.

Acceptance:

- `routing.sticky.break` traces can be queried without opening raw trace details.
- Missing or malformed cassette files are skipped or reported consistently with existing MCP query behavior.
- `go test ./internal/mcpserver` passes.

### E. Monitor Routing Aggregation

Branch: `feature/gateway-monitor-routing-aggregation`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-monitor-routing-aggregation`
Agent: Heisenberg (`019e8261-703a-7f52-bfeb-caaa11123b4d`)
Status: integrated as `b5d0068 feat: add monitor routing aggregation`

Owner scope:

- Add monitor-facing aggregation for routing/sticky event summaries.
- Prefer an existing monitor API pattern; if adding an endpoint, keep it read-only and cassette-event backed.
- Surface counts by routing failure reason, selected upstream, sticky status, and sticky breaks.
- Add focused backend tests and minimal UI wiring only if the existing Trace/Monitor UI has a natural location.

Constraints:

- No router/proxy hot-path changes.
- No DB schema changes in this batch.
- No broad navigation redesign.
- Preserve existing trace detail behavior.

Acceptance:

- A developer can see whether recent failures are mostly no-support, all-open, all-excluded, retry saturation, or sticky break related.
- Aggregation is derived from V3 prelude events and degrades gracefully for legacy cassettes.
- Relevant Go tests pass; if UI is touched, run the existing frontend check available in this repo.

### F. Limit Event Primitives

Branch: `feature/gateway-limit-events`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-limit-events`
Agent: Curie (`019e8261-8e98-7c90-b044-5706e34238d8`)
Status: integrated as `b441250 feat: add local limit rejection events`

Owner scope:

- Introduce small reusable in-memory concurrency limiter primitives for global/channel-like keys.
- Emit structured rejection events such as `limit.concurrency_rejected` and `limit.queue_saturated` when a configured local limit is exceeded.
- Add conservative disabled-by-default config fields if needed.
- Add unit tests for limiter behavior and proxy event recording.

Constraints:

- Disabled by default; existing behavior must not change without config.
- No persistent quota/billing/payment model.
- No token spend accounting.
- Do not alter sticky routing selection semantics.

Acceptance:

- With limits disabled, current tests and behavior remain unchanged.
- With a tiny configured limit, concurrent requests receive the intended HTTP status and cassette event.
- `task check:quick` passes on the branch.

### G. Credential Decision Chain Spec

Branch: `feature/gateway-credential-decision-spec`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-credential-decision-spec`
Agent: Parfit (`019e824b-227c-73c3-b412-62b324e27785`)
Status: integrated as `aeef840 docs: specify credential routing decision chain`

Owner scope:

- Produce a concrete implementation spec for credential-aware routing.
- Define Channel, Credential, RouteTarget, and event vocabulary boundaries.
- Identify the minimal additive storage/config changes for a later implementation batch.
- Include migration safety and replay compatibility analysis.

Constraints:

- Documentation/spec only unless a tiny type-level sketch is necessary.
- No DB migrations in this branch.
- No router behavior changes.
- Keep payment/recharge/public relay explicitly out of scope.

Acceptance:

- The next implementation batch can split storage, router snapshot, monitor, and MCP work without ambiguity.
- Spec explains how credentials interact with sticky bindings, health, limit events, and route decision traces.
- Documentation is linked from the gateway plan or design entry.

Spec:

- [Credential Routing Decision Chain Spec](./GATEWAY_CREDENTIAL_DECISION_CHAIN_SPEC.md)

## Parallel Batch 3 Plan

Baseline:

```text
b2fb659 test: update management mcp tool count
```

Coordinator branch:

```text
feature/gateway-credential-implementation
/data/src/github.com/kingfs/llm-tracelab-gateway-credential-implementation
```

Batch 3 objective:

Implement the first credential-aware routing slice without breaking existing channel config, cassette replay, or local-first workflows.

Integration order:

1. Config/runtime projection, because router work needs typed credential inputs.
2. Router route-target expansion, because proxy events should consume router decisions.
3. Proxy event/redaction, because it writes additive cassette evidence.
4. Monitor/MCP credential read-side grouping, because it consumes emitted fields.

### H. Credential Config Runtime Projection

Branch: `feature/gateway-credential-config`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-credential-config`
Agent: Sagan (`019e8261-4a51-7331-9a35-22e28f71fa60`)
Status: assigned

Owner scope:

- Add optional credential config under upstream targets.
- Preserve existing single-key behavior by compiling an implicit `default` credential when explicit credentials are absent.
- Add runtime/config tests showing explicit credentials win over inline key and existing configs still load.
- Avoid DB migrations; this is config/runtime projection only.

Constraints:

- No router selection behavior changes.
- No secret values in logs, docs examples beyond env references, events, or tests.
- Existing `upstream` and `upstreams` YAML remain valid.

Acceptance:

- `config.Load` parses optional `credentials`.
- `EffectiveUpstreams` or an adjacent helper exposes enough data for router target expansion.
- `go test ./internal/config ./cmd/server` passes.

### I. Router RouteTarget Expansion

Branch: `feature/gateway-credential-router`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-credential-router`
Agent: Heisenberg (`019e8261-703a-7f52-bfeb-caaa11123b4d`)
Status: assigned

Owner scope:

- Expand each upstream/channel into route targets by credential when credential data is available.
- Add additive `CandidateDecision` / `DecisionTrace` fields: `route_target_id`, `channel_id`, `credential_id`, `credential_hint`.
- Sticky binding should continue using concrete route target IDs.
- Add router tests for implicit default credential, explicit credential expansion, and sticky rebind at route target granularity.

Constraints:

- Depend only on config/runtime structures from task H.
- Do not add DB migrations.
- Do not write cassette events directly.
- Keep requests with no explicit credentials behavior-compatible.

Acceptance:

- Existing router tests pass.
- New tests prove route target IDs remain stable and explainable.
- `go test ./internal/router` passes.

### J. Credential Event Emission And Redaction

Branch: `feature/gateway-credential-events`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-credential-events`
Agent: Curie (`019e8261-8e98-7c90-b044-5706e34238d8`)
Status: assigned

Owner scope:

- Emit additive credential fields in `routing.candidates`, `routing.selected`, `routing.outcome`, and sticky events when router decisions include them.
- Add redaction tests to ensure credential hints are safe and raw auth material never appears in cassette metadata.
- Add proxy e2e assertions for credential event fields.

Constraints:

- Consume router decision fields from task I.
- Do not alter raw HTTP request/response bytes.
- Do not expose API keys, bearer tokens, OAuth tokens, service-account JSON, or raw custom auth headers.

Acceptance:

- Existing V3 cassette parsing remains compatible.
- New credential fields are additive and omitted when absent.
- `go test ./internal/proxy ./internal/redaction` passes.

### K. Credential Read-Side Grouping

Branch: `feature/gateway-credential-readside`
Worktree: `/data/src/github.com/kingfs/llm-tracelab-gateway-credential-readside`
Agent: Parfit (`019e824b-227c-73c3-b412-62b324e27785`)
Status: assigned

Owner scope:

- Extend MCP routing/sticky/failure queries and Monitor routing summary to group by `channel_id`, `credential_id`, and `route_target_id` when present.
- Preserve fallback behavior for old cassettes with only `upstream_id`.
- Add focused tests with mixed old/new event fixtures.

Constraints:

- Read-only over cassette/store.
- No router/proxy changes.
- No DB migrations.

Acceptance:

- MCP and Monitor outputs expose credential grouping without breaking existing fields.
- Old cassette fixtures still pass.
- `go test ./internal/mcpserver ./internal/monitor` passes.
