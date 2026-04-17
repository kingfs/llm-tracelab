# Project Baseline

## Purpose

This document records the current functional baseline of `llm-tracelab` after the session aggregation and monitor UX work landed.

It is intended for two audiences:

- humans who need a quick project capability overview before making changes
- AI agents that need a stable summary of what already exists, what is considered complete, and what kinds of follow-up work are still reasonable

This is not a roadmap. It describes the current implemented state.

## Product Scope

`llm-tracelab` is a local-first LLM HTTP record/replay proxy.

Its current baseline workflow is:

1. route SDK or application traffic through the proxy
2. persist the raw HTTP exchange as a human-inspectable `.http` cassette
3. index request metadata into SQLite for fast monitor queries
4. replay the cassette in tests without upstream network access
5. inspect traces in the monitor from either a request-centric or session-centric perspective

## Current Provider Coverage

The codebase currently supports these protocol families at the baseline level:

- OpenAI-compatible
- Anthropic Messages
- Google GenAI
- Vertex native

Provider normalization, usage extraction, stream transcript handling, and monitor parsing are centered in `pkg/llm`.

## Storage Baseline

### Raw Record Files

New recordings are written as `LLM_PROXY_V3`.

The raw `.http` file remains the source of truth for:

- replay
- raw protocol inspection
- trace detail reconstruction

### SQLite Metadata Index

SQLite remains the source for:

- request/session list pages
- aggregate statistics
- session grouping
- monitor filtering and pagination

Current `logs` table baseline includes both request metadata and grouping metadata such as:

- `trace_id`
- `provider`
- `operation`
- `endpoint`
- `session_id`
- `session_source`
- `window_id`
- `client_request_id`

### Schema Upgrade Behavior

The project now expects old SQLite indexes to upgrade in place at startup.

Important invariant:

- column backfill and additive schema upgrades must succeed against an existing local database before dependent indexes are created

This matters because monitor/session features rely on the grouping columns above.

## Session Aggregation Baseline

Session aggregation is now an implemented feature, not just a plan.

### Grouping Inputs

The current grouping extraction order is:

1. `Session_id`
2. `X-Codex-Turn-Metadata.session_id`
3. `X-Codex-Window-Id` prefix before `:`
4. empty string if none exist

Supporting metadata also indexed:

- `window_id`
- `client_request_id`

### Session APIs

Current monitor API surface includes:

- `GET /api/traces`
- `GET /api/traces/:traceID`
- `GET /api/sessions`
- `GET /api/sessions/:sessionID`

### Session UI

The monitor home page now has two stable perspectives:

- `Requests`
- `Sessions`

Session detail currently includes:

- session summary cards
- request count / success / failed / duration / token views
- provider/model/endpoint breakdowns
- ordered session timeline
- grouped request list
- failed-only request filter
- failure context windows around failed requests

## Monitor Baseline

The embedded monitor UI is a React frontend served from Go embed assets.

### Request Detail Tabs

Trace detail now supports these stable tabs:

- `Timeline`
- `Summary`
- `Raw Protocol`
- `Declared Tools` when applicable

### Cross-View Navigation

The current baseline includes deep links from session pages into trace detail.

Supported query parameters now include:

- `tab`
- `from_session`
- `view`
- `focus`

### Focus Targets

Current implemented focus targets are:

- `failure`
  highlights the failure summary card
- `response`
  jumps to and highlights the raw response protocol column
- `timeline`
  jumps to and highlights the timeline panel
- `timeline_error`
  expands the timeline tree and focuses the first `status == "error"` node

### Current UX Capabilities

Users can currently:

- browse raw request-level traces
- switch to grouped session-level inspection
- jump from a session into a specific trace
- land directly in the most relevant trace tab
- focus the failure summary
- focus the raw response payload
- focus the timeline panel
- focus the first timeline error node

## Replay Baseline

`pkg/replay` remains a hard requirement.

The implemented monitor/session enhancements must not break:

- cassette readability
- V2 read compatibility
- replay behavior

## Testing And Quality Baseline

Current expected project workflows:

- `task build`
- `task run`
- `task test`
- `task check`

When changing storage or monitor behavior, the expected baseline validation is:

- Go tests pass
- monitor frontend builds successfully
- embedded UI assets stay in sync when frontend code changes

## What Is Considered Done

The following work is considered functionally complete at the baseline level:

- session metadata extraction
- SQLite grouping/index persistence
- session list/detail APIs
- monitor dual-perspective home page
- session detail overview and filtering
- trace/session cross-view navigation
- tab-level deep links
- area-level focus in trace detail
- timeline error-node focus
- legacy SQLite schema upgrade for newly added session columns

## Reasonable Follow-Up Work

The current baseline does not block release, but these are still reasonable future refinements:

- more stable node-level identifiers inside timeline trees
- linking directly to specific tool call / tool response nodes
- richer focus/highlight lifecycle behavior
- additional documentation for operator workflows and troubleshooting

These are refinements on top of an already functional session-aware monitor baseline, not missing core architecture.
