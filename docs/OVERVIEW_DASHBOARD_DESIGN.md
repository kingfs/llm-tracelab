# Overview Dashboard Design

## Goal

Overview is the global dashboard for `llm-tracelab`. It should answer whether the local record/replay proxy is healthy, what changed recently, and where a developer should drill down next.

It is not a second trace list. Trace search, pagination, and raw HTTP inspection stay in the Traces page.

## Product Scope

Overview should aggregate workspace-wide signals from the SQLite trace index and v1 analysis tables:

- request volume, success rate, failed request count, token usage, TTFT, latency, stream ratio, and active session count
- time-window trends for requests, failures, tokens, TTFT, and latency
- top breakdowns by model, provider, endpoint, upstream, routing failure reason, and finding category
- attention queues for recent failures, high-risk findings, routing failures, and slow traces
- analysis and observation health summaries when indexed data exists

The dashboard must remain local-first and evidence-oriented. Every card that signals risk should link to the owning trace, session, audit finding, model, upstream, or filtered trace list.

## Page Responsibilities

### Overview

- global health summary
- short trend charts
- compact top-N breakdowns
- small attention queues
- navigation into deeper work surfaces

### Traces

- latest and historical HTTP exchanges
- search and filtering
- pagination
- trace detail and raw cassette evidence

### Audit

- finding triage
- severity/category filters
- evidence navigation

### Upstreams

- routing health
- provider/model routing diagnostics
- upstream-specific failure timelines

## API Shape

Add:

```text
GET /api/overview?window=24h
```

Supported windows:

- `1h`
- `24h`
- `7d`
- `all`

Initial response shape:

```json
{
  "window": "24h",
  "refreshed_at": "2026-05-14T00:00:00Z",
  "summary": {
    "request_count": 120,
    "success_request": 116,
    "failed_request": 4,
    "success_rate": 96.7,
    "total_tokens": 180000,
    "avg_ttft_ms": 420,
    "avg_duration_ms": 2100,
    "stream_count": 80,
    "session_count": 12
  },
  "timeline": [
    {
      "time": "2026-05-14T00:00:00Z",
      "request_count": 10,
      "failed_request": 1,
      "total_tokens": 10000,
      "avg_ttft_ms": 360,
      "avg_duration_ms": 1800
    }
  ],
  "breakdown": {
    "models": [{ "label": "gpt-5.4", "count": 70 }],
    "providers": [{ "label": "openai", "count": 70 }],
    "endpoints": [{ "label": "/v1/chat/completions", "count": 70 }],
    "upstreams": [{ "label": "openai-primary", "count": 70 }],
    "routing_failure_reasons": [{ "label": "http_5xx", "count": 2 }],
    "finding_categories": [{ "label": "credential_leak", "count": 1 }]
  },
  "attention": {
    "recent_failures": [],
    "high_risk_findings": [],
    "routing_failures": [],
    "slow_traces": []
  },
  "analysis": {
    "total": 5,
    "failed": 1,
    "recent": []
  }
}
```

## Data Source Rules

- SQLite remains the source for dashboard list/statistics.
- Raw `.http` cassettes remain the source of truth for replay and trace detail.
- Overview API must not rescan the filesystem.
- New aggregate queries should read indexed columns from `logs`, `trace_findings`, `trace_observations`, and `analysis_runs`.
- Missing derived data should be represented as zero counts or empty arrays, not hidden UI.

## UI Layout

Initial implementation:

1. Header with window selector and refresh timestamp.
2. KPI grid:
   - Requests
   - Success
   - Failed
   - Tokens
   - Avg TTFT
   - Avg latency
   - Streams
   - Sessions
3. Trend charts:
   - Requests vs failures
   - Tokens
   - TTFT vs latency
4. Breakdown grid:
   - Models
   - Providers
   - Endpoints
   - Upstreams
   - Routing failures
   - Finding categories
5. Attention queues:
   - Recent failures
   - High-risk findings
   - Routing failures
   - Slow traces

`Latest traces` should be removed from Overview. Recent trace rows may appear only when they are actionable failures or slow requests.

## Development Plan

### Phase 1: Design Baseline

- Add this design document.
- Keep scope focused on dashboard aggregation and drilldown.
- Commit documentation separately.

Validation:

- Documentation review against `AGENTS.md` and `docs/v1/frontend-redesign-plan.md`.

Status:

- Completed in `fc8d358 docs: design overview dashboard`.

### Phase 2: Backend Overview API

- Add store-level overview aggregate types and queries.
- Add `/api/overview`.
- Add monitor API tests for summary, timeline, breakdown, and attention queues.
- Ensure the API does not trigger filesystem sync.

Validation:

- `go test ./internal/store ./internal/monitor`
- Commit backend changes.

Status:

- Completed in `1ea8a4f feat: add monitor overview api`.
- Verified with `go test ./internal/store ./internal/monitor`.

### Phase 3: Frontend Overview Dashboard

- Replace current Overview implementation with the new API.
- Reuse existing chart and breakdown components where possible.
- Keep Traces page as the only full trace list page.
- Update embedded UI assets.

Validation:

- `task ui:build`
- `go test ./internal/monitor`
- Commit frontend changes.

Status:

- Completed in `4244901 feat: redesign monitor overview dashboard`.
- Verified with `task ui:build`.
- Verified with `go test ./internal/monitor`.

### Phase 4: Review And Next Slice

- Review this document against the implemented API and UI.
- Record any intentional deferrals.
- Plan the next narrow improvement, such as p95 latency, richer observation health, or deep-link filters.
- Commit documentation updates if needed.

Status:

- This review confirms the implementation stayed within the intended Overview boundary:
  - Overview now consumes `/api/overview`.
  - `Latest traces` was removed from Overview.
  - Trace search, pagination, and raw evidence remain owned by Traces.
  - The first API version uses indexed SQLite data only and does not rescan cassette files.

Next narrow slice:

1. Add filtered drilldown URLs from Overview breakdown rows into Traces, Audit, Models, and Upstreams.
2. Add observation health counts after parse job semantics are stable enough for global display.
3. Add p95 TTFT/latency only if it can be computed from indexed `logs` without a storage migration.

### Phase 5: Breakdown Drilldowns

Goal:

- Make Overview breakdown rows actionable without turning Overview into a list/search page.

Scope:

- Add optional links to reusable breakdown rows.
- Link model rows to model detail.
- Link provider and endpoint rows to filtered Traces.
- Link upstream and routing failure rows to Routing.
- Link finding category rows to filtered Audit.
- Add Audit URL filters for category and severity.

Validation:

- `task ui:build`
- `go test ./internal/monitor`

Status:

- Completed in this phase.
- The next slice remains observation health counts or p95 latency, not additional Overview list features.

## Explicit Deferrals

- Cost estimation is deferred until provider/model pricing configuration exists.
- Percentile latency is deferred unless the first implementation can compute it simply from indexed data without schema changes.
- Long-range historical rollups are deferred; the first version can bucket directly from `logs`.
- No destructive storage migration is required.
