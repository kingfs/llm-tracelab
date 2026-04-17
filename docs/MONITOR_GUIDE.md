# Monitor Guide

## Purpose

This document explains what users can do with the current monitor and how to move between the two supported observation perspectives:

- `Requests`: one row per recorded HTTP exchange
- `Sessions`: one row per grouped session of related requests
- `Upstreams`: routing and health analytics across configured upstream targets

It reflects the current implemented behavior.

## What The Monitor Shows

The monitor is backed by two data sources:

- SQLite for list pages, filters, pagination, and aggregate stats
- raw `.http` cassettes for detail reconstruction and replay-safe inspection

This means the monitor is optimized for fast browsing without losing access to the original HTTP payload.

## Requests View

Use `Requests` when you need to inspect traffic one HTTP exchange at a time.

This view is the best fit for:

- checking a single request/response pair
- inspecting endpoint, model, duration, and token usage per call
- jumping directly into raw protocol details
- validating whether a request failed, streamed, or used tools

Each row corresponds to one recorded trace.

The request list page also includes an upstream analytics section so you can inspect routing behavior without leaving the monitor home page.

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

## Upstreams View

Use the upstream analytics section when you need to inspect routing behavior across configured providers and model catalogs.

This view is the best fit for:

- checking which upstreams are healthy right now
- seeing which models are routed to which provider
- spotting one provider accumulating failures
- drilling into a specific upstream without rescanning raw cassettes

Current upstream analytics capabilities include:

- time-window filtering
- model substring filtering
- target health state
- last refresh status
- request / success / failure / token / TTFT aggregates
- recent routed models
- recent failures with deep links into trace detail

Each upstream card also links to a dedicated drilldown page.

## Session Detail

Session detail is a grouped inspection page for one session.

Current capabilities include:

- summary cards for request count, status split, duration, and tokens
- provider/model/endpoint breakdowns
- ordered session timeline
- grouped request list
- failed-only filtering
- failure context windows around failed requests

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

## Trace Routing Context

When a trace was recorded through the multi-upstream router, the trace detail summary also shows routing context for that individual request.

Current routing context includes:

- selected upstream id
- selected upstream provider preset
- selected upstream base URL
- routing policy
- routing score
- routing candidate count

This lets you answer a practical debugging question directly from one trace:

1. which upstream handled this request
2. why the router considered it the chosen target

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

### Investigate one unstable upstream

1. Start in the upstream analytics section on the monitor home page
2. apply a window such as `1h` or `24h`
3. optionally filter by model
4. open the upstream drilldown page
5. review recent failures, model distribution, endpoint distribution, and recent traces
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
- Upstream analytics are also additive. They depend on recorded routing metadata and SQLite indexes, but replay still depends on the raw cassette bytes rather than live provider state.
