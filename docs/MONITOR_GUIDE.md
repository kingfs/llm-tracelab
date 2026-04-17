# Monitor Guide

## Purpose

This document explains what users can do with the current monitor and how to move between the two supported observation perspectives:

- `Requests`: one row per recorded HTTP exchange
- `Sessions`: one row per grouped session of related requests

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
4. use `Summary`, `Timeline`, or `Raw Protocol` depending on whether you need status context, event flow, or raw bytes

### Investigate a multi-request coding session

1. Start in `Sessions`
2. open the relevant session
3. review counts, timing, and failed-only requests
4. jump into the most relevant trace from the grouped request list
5. use deep links and focus targets to land at the likely failure area

### Compare request-level and grouped perspectives

1. use `Requests` for exact HTTP-level inspection
2. use `Sessions` for workflow-level inspection
3. switch between them depending on whether the problem is isolated or distributed across many turns

## Operational Notes

- The monitor reads aggregate data from SQLite, so large trace directories remain usable without rescanning every cassette on each page load.
- The raw `.http` cassette remains the source of truth for replay and detail views.
- Missing session metadata does not break trace visibility. Those traces remain available in `Requests` even when they cannot be grouped into `Sessions`.
- Session grouping is additive metadata, not a replacement for the original request view.
