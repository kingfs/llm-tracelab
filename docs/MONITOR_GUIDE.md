# Monitor Guide

## Purpose

This document explains what users can do with the current monitor and how to move between the two supported observation perspectives:

- `Requests`: one row per recorded HTTP exchange
- `Sessions`: one row per grouped session of related requests
- `Models`: traffic-oriented model marketplace and model detail
- `Channels`: Web-managed upstream channel configuration, probing, model enablement, and channel analytics
- `Routing`: selected route decisions for debugging which channel handled each request
- `Events`: TraceLab runtime and derived-pipeline exceptions

It reflects the current implemented behavior.

## Access Control

The monitor requires username/password login. Initialize the first user before starting a fresh deployment:

```bash
go run ./cmd/server auth init-user -c config/config.yaml --username admin --password 'change-me-123'
```

After login, use the `Tokens` page to generate personal API tokens. The same token is used by the LLM proxy API and MCP with `Authorization: Bearer <token>`.

## What The Monitor Shows

The monitor is backed by two data sources:

- SQLite for list pages, filters, pagination, and aggregate stats
- raw `.http` cassettes for detail reconstruction and replay-safe inspection

This means the monitor is optimized for fast browsing without losing access to the original HTTP payload.

Channels and model enablement are also stored in SQLite. YAML is only the service startup configuration surface and a first-run bootstrap compatibility input for legacy `upstream` / `upstreams` blocks. After bootstrap, manage channels, API keys, provider presets, probing, and model enablement from Monitor Web.

System events are stored in SQLite as operational metadata. They describe TraceLab's own runtime or derived-pipeline health, not ordinary user traffic health.

## Events View

Use `Events` when you need to review TraceLab internal exceptions without mixing them into request failure analytics.

This view is the best fit for:

- parser failures
- analysis failures
- routing selection failures
- upstream transport errors
- future monitor/store/MCP handler failures

The Events navigation item shows an unread badge. The badge is updated from `/api/events/stream` when SSE is available and falls back to periodic summary polling.

Event statuses are:

- `unread`: not yet reviewed
- `read`: reviewed but not resolved
- `resolved`: considered handled
- `ignored`: intentionally not actionable

Repeated events are grouped by fingerprint. If a read or resolved event happens again, it becomes unread again. Ignored events stay ignored.

Overview shows only a compact system event summary and a link to Events. It should not be used as the durable exception inbox.

## Requests View

Use `Requests` when you need to inspect traffic one HTTP exchange at a time.

This view is the best fit for:

- checking a single request/response pair
- inspecting endpoint, model, duration, and token usage per call
- jumping directly into raw protocol details
- validating whether a request failed, streamed, or used tools

Each row corresponds to one recorded trace.

The request list can link into channel, model, and routing views when recorded traces include route metadata.

## Models View

Use `Models` when you want to start from a model name and understand where its traffic is going.

This view is the best fit for:

- seeing models that had traffic in the selected time window
- checking enabled channel coverage for one model
- comparing request, error, token, and today-token totals
- opening model detail to inspect channel coverage and request/token trends

Token totals only sum known usage values. When successful traces have no usage payload, the UI shows `missing usage` next to token totals so that missing provider data is not mistaken for a true zero-token request.

## Channels View

Use `Channels` when you need to manage upstream providers or inspect channel-level health and usage.

This view is the best fit for:

- creating a channel without editing YAML
- setting provider preset, base URL, API key, custom headers, and advanced routing fields
- probing `/models` or provider-specific model discovery
- enabling or disabling a channel with a switch
- enabling or disabling individual models with switches
- comparing channel request and token trends
- reviewing recent probe results and failed traces for one channel

Configuration source is visible in the UI:

- `web-managed`: created or edited through Monitor Web
- `bootstrap`: imported once from legacy YAML when the database had no channel configuration

Model source is also visible:

- `manual`: added from Monitor Web
- `bootstrap static`: imported from legacy YAML `static_models`
- `probe discovered`: discovered by a channel probe
- `seen in trace`: inferred from recorded traffic

## Sessions View

Use `Sessions` when one user workflow produces many related requests and the request list becomes too fragmented.

This view is the best fit for:

- understanding multi-turn coding or agent loops
- reviewing aggregate latency and token patterns for one session
- locating failure clusters inside a larger workflow
- moving from a grouped overview into the specific failed trace

The current session grouping order is:

1. `Session_id`
2. `X-Codex-Turn-Metadata.session_id`
3. `X-Codex-Window-Id` prefix before `:`
4. no session grouping when none of the above exist

The implementation is intentionally provider-extensible. OpenAI-compatible traffic is the first strong use case, but the monitor model is not limited to OpenAI.

## Routing View

Use `Routing` when you need to inspect selected route decisions across recent traces.

This view is the best fit for:

- confirming which channel handled a model request
- filtering route records by model, channel, status, duration, TTFT, or token range
- seeing status code, token usage, duration, and TTFT without opening backend logs
- jumping from a route record into trace detail

Current routing capabilities include:

- time-window filtering
- model and channel substring filtering
- success/error filtering
- duration, TTFT, and token range filtering
- routed request, channel, error, token, and missing-usage summaries
- selected channel tags in request rows

The legacy `Upstreams` pages remain available as runtime diagnostics for existing selected-upstream metadata, but the primary v1 workflow is `Channels` for configuration and analytics, `Models` for model-centric usage, and `Routing` for selected-route debugging.

## Session Detail

Session detail is a grouped inspection page for one session.

Current capabilities include:

- summary cards for request count, status split, duration, and tokens
- provider/model/endpoint breakdowns
- ordered session timeline
- grouped request list
- failed-only filtering
- failure context windows around failed requests
- async session reanalysis, which can rebuild trace observations/findings and
  persist a fresh session analysis run

This page is meant to answer two questions quickly:

1. what happened across the full session
2. which individual request should I open next

## Trace Detail Tabs

Each trace detail page currently supports these tabs:

- `Timeline`
- `Summary`
- `Raw Protocol`
- `Declared Tools` when the request declares tools

These tabs are stable navigation targets and are used by session-to-trace deep links.

Trace detail also exposes controlled reanalysis actions:

- `Repair usage`: re-extract usage from the recorded response and repair SQLite
  token metrics
- `Reparse`: rebuild Observation IR from the raw cassette
- `Rescan`: rerun deterministic audit detectors against existing Observation IR
- `Reanalyze`: rebuild Observation IR and rerun deterministic scan

These actions do not call the upstream provider. They read local cassettes and
write auditable rows in `analysis_jobs`.

## Analysis View

Use `Analysis` to inspect persisted session analysis runs and reanalysis jobs.

Current capabilities include:

- recent `analysis_runs`
- recent `analysis_jobs`
- job status, target, steps, request, result, and last error
- a batch action for repairing successful traces with missing usage

Batch reanalysis expands a stable filter selection into per-trace child jobs.
The child jobs perform the actual trace work so failures remain attributable to
specific trace IDs.

## Trace Routing Context

When a trace was recorded through the multi-upstream router, the trace detail summary also shows routing context for that individual request.

Current routing context includes:

- selected upstream id
- selected upstream provider preset
- selected upstream base URL
- routing policy
- routing score
- routing candidate count
- selected upstream health state when the router is attached
- current health-threshold interpretation for error/timeout/TTFT signals

This lets you answer a practical debugging question directly from one trace:

1. which upstream handled this request
2. why the router considered it the chosen target
3. whether that upstream is currently healthy, degraded, or open from the router's point of view

## Deep Links And Focus

The monitor currently supports query-driven navigation so that session pages can open the most relevant part of a trace detail page.

Supported query parameters:

- `tab`
- `from_session`
- `view`
- `focus`

Current focus targets:

- `failure`: highlight the failure summary card
- `response`: jump to and highlight the raw response area
- `timeline`: jump to and highlight the timeline panel
- `timeline_error`: expand the timeline tree and focus the first error node

These links are useful when the session page already knows the user likely wants the failed trace, the raw response body, or the timeline error.

## Typical Workflows

### Investigate a single failed request

1. Start in `Requests`
2. filter or scan for the failed row
3. open the trace detail page
4. use the routing context in `Summary` to confirm which upstream handled it
5. use `Timeline` or `Raw Protocol` depending on whether you need event flow or raw bytes

### Investigate a multi-request coding session

1. Start in `Sessions`
2. open the relevant session
3. review counts, timing, and failed-only requests
4. jump into the most relevant trace from the grouped request list
5. use deep links and focus targets to land at the likely failure area

### Add or change a channel

1. Start in `Channels`
2. create or edit a channel from the modal form
3. choose the provider preset and base URL, then enter API key or headers
4. use advanced options only when protocol family, routing profile, deployment, project, location, or model resource needs to be explicit
5. probe the channel to discover models
6. enable the models you want routed
7. send traffic through the proxy and inspect the channel/model statistics

### Investigate one unstable channel

1. Start in `Channels`
2. choose a time window such as `24h`, `7d`, or `30d`
3. open the channel detail page
4. review request/token trends, model usage, recent probes, and recent failed traces
5. use `Routing` if you need to filter selected-route records by status, latency, TTFT, or token range
6. jump from a failed request into trace detail if you need raw protocol or timeline context

### Compare request-level and grouped perspectives

1. use `Requests` for exact HTTP-level inspection
2. use `Sessions` for workflow-level inspection
3. switch between them depending on whether the problem is isolated or distributed across many turns

## Operational Notes

- The monitor reads aggregate data from SQLite, so large trace directories remain usable without rescanning every cassette on each page load.
- The raw `.http` cassette remains the source of truth for replay and detail views.
- Missing session metadata does not break trace visibility. Those traces remain available in `Requests` even when they cannot be grouped into `Sessions`.
- Session grouping is additive metadata, not a replacement for the original request view.
- Channel/model/routing analytics are additive. They depend on recorded routing metadata and SQLite indexes, but replay still depends on the raw cassette bytes rather than live provider state.
- YAML `upstream` / `upstreams` blocks are compatibility bootstrap input. Long-lived channel and model state should be edited in Monitor Web.
