# Multi-Upstream Routing Plan

## Goal

`llm-tracelab` currently assumes exactly one upstream target at startup.

That is sufficient for simple local proxying, but it becomes awkward in real use:

- one model may be offered by multiple providers
- users often want one stable local proxy endpoint instead of editing config and restarting
- provider/model availability changes over time and should not require manual config flipping

This document proposes a multi-upstream design that keeps `llm-tracelab` local-first and replay-safe while making provider selection dynamic.

## Problem Statement

Today the proxy has a hard single-upstream shape:

- `internal/config.Config` contains one `upstream`
- `cmd/server/main.go` resolves and checks one upstream during startup
- `internal/proxy.Handler` binds one `httputil.ReverseProxy` to one fixed target
- recorder metadata stores request semantics, but not which configured upstream was selected at runtime

Operationally this creates friction:

1. user wants `gpt-4.1`, `gpt-5`, or `gemini-2.5-flash` to route to any healthy compatible provider
2. user must stop the proxy
3. user edits `upstream.base_url` and auth fields
4. user restarts the proxy

This is the wrong boundary. Provider selection should move from static config editing to runtime routing.

## Design Principles

The design should preserve these project invariants:

- replay remains cassette-driven and must not depend on live upstream state
- raw `.http` files remain human-inspectable
- storage changes are additive
- SQLite remains the query/index source, not the raw-record source of truth
- provider-specific protocol behavior stays in `internal/upstream` and `pkg/llm`, not spread through the proxy

Additional routing-specific principles:

- model discovery should be cached, not fetched on every request
- no routing decision should require a database roundtrip on the hot path
- startup should tolerate partial upstream failure
- routing decisions should be explainable in logs and monitor metadata
- the first implementation should degrade gracefully when some advanced metrics are unavailable

## Non-Goals

This plan does not turn `llm-tracelab` into a full hosted gateway platform.

Out of scope for the first multi-upstream round:

- billing aggregation
- tenant-specific quotas
- distributed coordination across multiple proxy instances
- provider-native fine-grained capability normalization
- speculative fan-out or hedged requests
- automatic request retry/replay across providers after partial upstream streaming output has already started

## Proposed Configuration Shape

### Target Shape

Replace the single `upstream` block with an additive multi-target structure:

```yaml
server:
  port: "8080"

monitor:
  port: "8081"

router:
  model_discovery:
    enabled: true
    refresh_interval: 10m
    startup_policy: "best_effort"     # strict | best_effort | lazy
  selection:
    policy: "cost_aware_p2c"          # first_available | weighted_random | p2c | cost_aware_p2c
    epsilon: 0.02
    open_window: 15s
    failure_threshold: 3
  fallback:
    on_missing_model: "reject"        # reject | allow_static

upstreams:
  - id: "openai-primary"
    enabled: true
    priority: 100
    weight: 1.0
    capacity_hint: 1.0
    model_discovery: "list_models"    # list_models | static_only | disabled
    static_models: []
    upstream:
      base_url: "https://api.openai.com/v1"
      api_key: "${OPENAI_API_KEY}"
      provider_preset: "openai"

  - id: "openrouter-fallback"
    enabled: true
    priority: 80
    weight: 0.8
    capacity_hint: 1.2
    model_discovery: "list_models"
    static_models:
      - "gpt-5"
      - "gpt-4.1"
    upstream:
      base_url: "https://openrouter.ai/api/v1"
      api_key: "${OPENROUTER_API_KEY}"
      provider_preset: "openrouter"
      headers:
        HTTP-Referer: "http://localhost"

debug:
  output_dir: "./logs"
```

### Compatibility Strategy

The old single `upstream` config should remain valid for at least one transition period.

Recommended startup behavior:

- if `upstreams` is empty and `upstream` is present, synthesize one target with id `default`
- if both are present, fail fast unless an explicit migration override is added later

This keeps current users working while the routing stack evolves.

## Runtime Architecture

Introduce a new orchestration layer between config/upstream resolution and proxy transport.

### New Components

1. `internal/router`
   - owns configured upstream targets
   - owns model catalog cache
   - owns provider health and load state
   - chooses a target per request

2. `internal/router/catalog`
   - refreshes provider model lists
   - merges dynamic discovery with static model declarations
   - persists catalog snapshots to SQLite

3. `internal/router/picker`
   - implements selection policy
   - starts simple, then absorbs the `llmrouterv2` scoring model

4. `internal/router/runtime`
   - records in-flight counts
   - updates EWMA latency, TTFT, timeout, and error rates after responses complete

### Proxy Change

`internal/proxy.Handler` should no longer own a single pre-bound `ReverseProxy`.

Instead:

- parse request semantics early
- ask router for a selected upstream target
- clone and rewrite the request against that target
- send through a shared `http.Transport`
- feed the response outcome back into router runtime state

This change is necessary because target selection becomes request-scoped instead of process-scoped.

## Request Classification And Model Extraction

The routing layer needs a stable model key before forwarding.

Existing building blocks already help:

- `llm.ParseRequestForPath`
- `llm.ModelFromPath`
- request endpoint normalization in `pkg/llm`

Selection should use this precedence:

1. explicit model parsed from request body
2. model inferred from request path
3. synthetic operation key such as `list_models`

If no model can be extracted, route only through compatible catch-all targets and mark the decision as low-confidence in logs.

## Model Catalog

The user expectation is that the proxy remembers which provider supports which model.

The recommended design is hybrid:

- runtime routing uses in-memory maps for hot-path speed
- SQLite persists the latest known catalog for startup warm state, diagnostics, and future monitor views

### Why Hybrid

In-memory only is fast but loses state at restart.

SQLite only would make every refresh and decision storage-coupled.

The better boundary is:

- SQLite is persistence and introspection
- memory is the active routing cache

### Catalog Sources

Per upstream target, model membership can come from:

1. dynamic discovery via provider model-list endpoint
2. operator-provided `static_models`
3. inferred synthetic models such as deployment-bound Azure or fixed Vertex resources

Dynamic discovery is not equally available everywhere:

- OpenAI-compatible providers usually expose `/models`
- Google GenAI exposes `/v1beta/models`
- Anthropic exposure may be limited or need different semantics
- Azure deployment routing may not expose usable deployment->model discovery
- Vertex fixed model routes may be operator-declared instead of listed

So each target needs an explicit `model_discovery` mode.

### Proposed Storage Additions

Add additive tables instead of overloading `logs`:

```sql
CREATE TABLE IF NOT EXISTS upstream_targets (
    id TEXT PRIMARY KEY,
    provider_preset TEXT NOT NULL,
    base_url TEXT NOT NULL,
    protocol_family TEXT NOT NULL,
    routing_profile TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    priority INTEGER NOT NULL,
    weight REAL NOT NULL,
    capacity_hint REAL NOT NULL,
    last_refresh_at TEXT NOT NULL DEFAULT '',
    last_refresh_status TEXT NOT NULL DEFAULT '',
    last_refresh_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS upstream_models (
    upstream_id TEXT NOT NULL,
    model TEXT NOT NULL,
    source TEXT NOT NULL,            -- dynamic | static | inferred
    seen_at TEXT NOT NULL,
    PRIMARY KEY (upstream_id, model)
);
```

This lets the process restart with a warm catalog and later expose model/provider views in monitor APIs without rescanning live upstreams.

## Selection Algorithm

### Recommendation

Use the `/tmp/llmrouterv2` idea, but do not copy it wholesale as a first pass.

Its strongest ideas fit this project well:

- route only among providers that support the requested model
- track in-flight load separately for streaming and non-streaming requests
- prefer low expected cost instead of simple round-robin
- use a lightweight health state machine: `healthy / degraded / open / probation`
- choose via `P2C` instead of scoring every provider every time

### Why It Fits

`llm-tracelab` already observes most of the needed signals:

- request size and model name at request time
- stream vs non-stream request mode
- response status code
- total duration
- TTFT via `InstrumentedResponseWriter`
- token usage from the recorder pipeline

That is enough to build a practical first routing policy.

### What Does Not Fit Yet

The prototype assumes richer decode-path signals such as:

- mean token delta gap
- p90 delta gap
- jitter
- cache affinity based on prompt hot keys

`llm-tracelab` does not currently expose all of these as stable router metrics.

So the first productionized version should trim the algorithm:

#### Phase 1 Policy

- candidate set: configured targets whose catalog contains the model
- filter out `open` targets unless all are open
- `P2C` between two random candidates
- score based on:
  - in-flight total
  - in-flight streaming
  - TTFT EWMA
  - request latency EWMA
  - error rate EWMA
  - timeout rate EWMA
  - static `weight`
  - `capacity_hint`

#### Phase 2 Policy

Add when routing state and stream parsing are stable:

- decode gap EWMA
- no-first-token rate for streaming failures
- cache affinity using request-body hash
- degraded/probation tuning from live observations

This keeps the implementation aligned with `llmrouterv2` without overpromising unavailable telemetry.

### Fallback Rules

If the requested model is unknown:

- default behavior should be `reject` with a clear proxy error
- optional `allow_static` mode can route to operator-approved generic targets

If only one candidate exists:

- route directly

If all candidates are in `open` state:

- either fail closed
- or temporarily pick the least-bad target, controlled by policy

The default should fail closed for predictability.

## Health State Machine

Adopt the same four-state model from the prototype:

- `healthy`
- `degraded`
- `open`
- `probation`

Suggested transitions:

- repeated failures or high timeout/error EWMA -> `open`
- `open` target remains excluded until `open_window` expires
- first requests after cooldown enter `probation`
- successful probation requests return target to `healthy`
- elevated but not critical latency/error ratios mark `degraded`

This is operationally simple and easy to explain in logs.

## Recording And Replay Impact

Replay compatibility must remain untouched.

The cassette should still represent exactly one real HTTP exchange.

### Required Metadata Additions

Extend recorded metadata additively with runtime routing facts:

- `selected_upstream_id`
- `selected_upstream_base_url`
- `selected_upstream_provider_preset`
- `routing_policy`
- `routing_score`
- `routing_candidates`

These fields should be stored in:

- cassette metadata/events for per-trace debugging
- SQLite `logs` table for monitor filtering later

None of these fields should be required for replay.

Replay only needs the raw request/response bytes as it does today.

## Startup And Refresh Behavior

Startup model refresh should support three modes:

- `strict`: all enabled targets must pass connectivity and model refresh
- `best_effort`: start if at least one target is usable
- `lazy`: start immediately and refresh in background

Recommended default: `best_effort`.

Reason:

- a multi-upstream proxy should not fail completely because one fallback provider is down
- but it also should not silently start with zero usable targets

Background refresh should:

- periodically refresh provider model lists
- preserve the last known catalog on temporary failures
- update target status and refresh timestamps in SQLite

## Monitoring And Diagnostics

The monitor should eventually expose routing context, but that can be incremental.

First useful additions:

- selected upstream on trace detail
- routing candidate count
- routing failure reason when no provider matched
- upstream target health summary API
- last model-catalog refresh status

Later additions:

- target health dashboard
- model -> providers lookup page
- routing distribution over time

## Migration Plan

### Phase 0: Design And Compatibility

- add this design note
- keep old single-upstream config valid

### Phase 1: Structural Support

- add `upstreams` and `router` config
- resolve multiple upstream targets at startup
- add additive SQLite tables for targets and model catalog
- add a router service with `first_available` or weighted policy
- record selected upstream metadata

### Phase 2: Discovery And Health

- add periodic model discovery
- persist latest target/model snapshots
- add EWMA health tracking and `healthy/degraded/open/probation`
- reject requests whose model has no eligible provider

### Phase 3: Cost-Aware Selection

- replace simple selection with `P2C`
- add cost terms from request size, TTFT, inflight counts, and error history
- add structured routing decision logs

### Phase 4: Richer Signals

- optional cache affinity
- optional stream decode-gap metrics
- monitor pages for provider/model health

## Testing Strategy

The routing work should be test-first at the package level.

### Unit Tests

- config compatibility: `upstream` vs `upstreams`
- model catalog merge rules
- target selection with fixed candidate sets
- health-state transitions
- fallback rules on unknown model / all-open providers

### Integration Tests

- proxy routes the same model to different fake upstreams depending on health
- recorder stores selected upstream metadata
- SQLite startup upgrade works on existing DBs
- background refresh updates model catalog without affecting replay

### Replay Regression Tests

- existing cassette replay tests stay unchanged
- new routing metadata in cassette prelude must not break V2/V3 readers

## Recommended Initial Implementation Decision

The first merge should not attempt the full `llmrouterv2` feature set.

Recommended cut:

1. add multi-upstream config and startup resolution
2. add model catalog with SQLite persistence
3. add request-scoped target selection
4. add simple health-aware `P2C`
5. record routing metadata

This already solves the user problem:

- one config file
- multiple providers online at once
- provider/model availability remembered locally
- no restart just to switch one model to another provider

## Summary

The right architecture change is not "allow many base URLs" in config.

It is:

- multi-target upstream resolution
- a local model catalog
- request-scoped target selection
- runtime health tracking
- additive routing metadata in storage and cassettes

`/tmp/llmrouterv2` is a strong reference for the picker design, especially its `cost-aware P2C` and health-state model.
It should be adapted in phases, with a smaller production-safe metric set first and richer routing signals later.
