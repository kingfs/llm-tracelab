# Upstream High Availability Design

## Goal

Improve proxy availability when an upstream provider has short failures, overload,
rate limits, or stale health state.

This design focuses on router/proxy algorithms and tests. Adding more upstream
providers is useful operationally, but it is not the main dependency for this
work.

## Current Failure Mode

Recent production traces showed a concentrated failure pattern:

- model: `gpt-5.5`
- endpoint: `/v1/responses`
- status: `502`
- routing failure: `all_targets_open`

The important code-level lesson is that a single temporary upstream failure can
cascade if routing health, retry timing, and recovery probes are not coordinated.

## Availability Principles

1. Prefer successful delayed completion over immediate `502` for transient
   upstream failures.
2. Do not hide deterministic client errors. 400-class errors other than known
   retryable statuses should pass through.
3. Keep retries bounded by request budget and client cancellation.
4. Avoid coordinated retry spikes by using jitter and refresh coalescing.
5. Make health checks part of the same state machine as request outcomes.
6. Keep replay stable. Retry metadata may be recorded, but raw request/response
   format compatibility must remain intact.

## Implemented Baseline

Already implemented:

- candidate failover for retryable upstream responses
- bounded retry budget for transient all-candidate failures
- background model refresh rebuilding the router catalog
- active `RefreshNow()` used by proxy retry loops
- health refresh success recovering open/degraded targets to probation
- health refresh failures degrading/opening targets after consecutive failures
- coalesced active refresh to avoid per-request refresh storms

## Planned Phases

### Phase 1: Retry Timing And Observability

Status: implemented.

Scope:

- add jitter to retry backoff
- honor upstream `Retry-After` for `429` and `503`
- record retry/backoff/refresh events in trace cassettes
- keep retry budget capped at 30 seconds

Acceptance:

- unit tests cover jitter bounds and `Retry-After` parsing
- e2e tests cover `Retry-After` delaying internal retry
- recorded events expose retry attempt, delay, status, upstream ID, and refresh
  trigger

Review:

- implemented 20% positive jitter on retry backoff
- implemented `Retry-After` parsing for seconds and HTTP date values
- retry wait, candidate retry, and active refresh events are appended to final
  recordings
- fixed event preservation so retry events are not overwritten by response
  pipeline events
- next phase remains probation traffic control; do not start model-scoped health
  until probation burst behavior is bounded

### Phase 2: Probation Traffic Control

Status: implemented.

Scope:

- restrict concurrent traffic sent to `probation` targets
- let only a small number of probe requests enter after recovery
- keep healthy targets preferred over probation targets

Acceptance:

- router tests prove probation concurrency limit
- proxy e2e proves recovered target does not receive a burst of concurrent
  requests

Review:

- router now treats `probation` as a half-open state with one in-flight probe
  allowed per target
- concurrent requests see the target as temporarily unavailable and reuse the
  proxy retry budget instead of bursting into the recovering upstream
- successful probation probes recover the target to `healthy`
- fixed EWMA handling so zero-valued success samples decay error and timeout
  rates instead of leaving stale failure rates permanently high
- next phase should stay on model-scoped health because whole-upstream health is
  now bounded at recovery time

### Phase 3: Model-Scoped Health

Status: implemented.

Scope:

- track health by `(upstream_id, model)` for model-specific upstream failures
- use model-scoped open/degraded state before whole-upstream state when routing
- keep upstream-level state for network and catalog failures

Acceptance:

- one failing model does not make unrelated models unavailable
- failure analytics can distinguish upstream-level and model-level open reasons

Review:

- router now tracks model-level health for retryable HTTP failures with an
  explicit request model
- network errors still update upstream-level health
- model-level open state blocks only the affected model
- successful catalog refresh moves discovered model health back to probation so
  one real request can probe recovery
- existing single-upstream transient recovery e2e remains fast because refresh
  can recover both upstream and model state
- failure analytics still needs explicit model-level reason exposure; keep this
  as monitor/MCP visibility work instead of expanding router behavior now
- next phase is request queue guardrails: bound how many requests may wait while
  all candidates are unavailable

### Phase 4: Request Queue Guardrails

Scope:

- cap concurrent waiters when all candidates are open
- return an explicit overload response when the wait queue is saturated
- expose queue pressure in router snapshots

Acceptance:

- stress tests show bounded goroutines and bounded active waits
- clients get deterministic overload errors instead of connection pileups

### Phase 5: Monitor And MCP Visibility

Scope:

- show retry count, retry delay, recovery probe, and open/probation transitions
- expose high-availability signals through existing monitor and MCP surfaces

Acceptance:

- upstream detail explains why a request waited, retried, or failed
- MCP failure clustering can separate upstream overload, retry exhaustion, and
  queue saturation

## Stage Review Rule

After each phase:

1. run focused tests and `task check:quick`
2. commit code and docs
3. review this document against the implementation
4. update the next phase scope if production evidence or test findings show a
   better priority

Avoid broadening scope without a concrete failure mode or acceptance test.
