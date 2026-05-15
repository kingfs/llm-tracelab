# System Events Design

## Purpose

`llm-tracelab` currently shows several failure-like signals on Overview:

- proxied LLM request failures
- routing failures
- parser failures
- analysis failures
- upstream transport errors
- deterministic audit findings

These signals are not the same kind of problem. Request failures describe user traffic. Parser, analyzer, router, store, MCP, and monitor errors describe `llm-tracelab` itself.

This document proposes a dedicated system event center for `llm-tracelab` runtime and derived-pipeline exceptions, with unread/read state, historical browsing, real-time delivery, and MCP tools for agent-facing diagnostics.

## Goals

- Separate "traffic health" from "TraceLab internal health".
- Keep Overview concise: show unread event count and latest severity, then link to Events.
- Persist all important internal exceptions in SQLite.
- Distinguish new/unread events from already reviewed historical events.
- Group repeated events by fingerprint to avoid noisy duplicate rows.
- Expose event summaries through Monitor Web and MCP so AI agents can query failures without scraping UI.
- Build a foundation for replacing periodic refresh with server push.

## Non-Goals

- Do not replace request logs or raw `.http` cassettes.
- Do not make system events the source of truth for replay.
- Do not hide normal request failures from trace/session/upstream views.
- Do not build a full incident-management platform.
- Do not add broad write-capable MCP automation in the first slice.

## Terminology

### Request Failure

A proxied upstream request whose trace has:

- non-2xx HTTP status, or
- non-empty recorded `error_text`.

This remains traffic data and belongs in Traces, Sessions, Models, Routing, and Overview traffic health.

### System Event

A persisted event that describes `llm-tracelab` runtime or derived analysis health.

Examples:

- parser failed to persist Observation IR
- analysis run failed
- router had no available target
- upstream stream copy failed
- database operation failed
- MCP handler failed
- startup/probe failure

### Fingerprint

A stable deduplication key for repeated occurrences of the same problem.

Example fingerprints:

```text
parser:openai:semantic_nodes_unique_constraint
router:gpt-5.5:all_targets_open
upstream:baizhiyun:broken_pipe
mcp:list_traces:store_query_failed
```

When the same fingerprint appears again, the event row is updated instead of creating unbounded duplicates.

## Product Design

## Navigation

Add a primary navigation item:

```text
Events
```

The nav item shows a red badge with unread event count:

```text
Events 12
```

Badge source:

- `GET /api/events/summary`
- later, live updates from `/api/events/stream`

## Overview Boundary

Overview should keep traffic KPIs:

- requests
- success rate
- failed requests
- tokens
- latency
- findings
- routing health summaries

Overview should stop acting as an exception inbox. It should show only a compact system health card:

- unread system events
- critical/error unread count
- latest event timestamp
- link to `/events`

Parser and analysis failures should be summarized as system events, not shown as permanently open rows in Overview.

## Events Page

The Events page is a durable exception inbox.

Top summary cards:

- Unread
- Critical/Error
- Last 24h
- Resolved

Filters:

- status: `unread`, `read`, `resolved`, `ignored`, `all`
- severity: `info`, `warning`, `error`, `critical`
- source: `proxy`, `recorder`, `parser`, `analyzer`, `router`, `monitor`, `store`, `auth`, `mcp`, `upstream`
- category
- text query
- time window

List row fields:

- severity
- title
- source/category
- occurrence count
- first seen
- last seen
- related trace/session/upstream/job links
- actions: `Mark read`, `Resolve`, `Ignore`

Detail panel:

- message
- `details_json`
- related objects
- recent occurrence history if available
- recommended next link, such as trace detail or routing page

## Status Semantics

System event statuses:

- `unread`: event has not been reviewed
- `read`: user reviewed the current occurrence
- `resolved`: user considers the issue handled
- `ignored`: event is intentionally not actionable

Repeated occurrence behavior:

- if an event is `unread`, increment count and update `last_seen_at`
- if an event is `read` and the same fingerprint appears again, move it back to `unread`
- if an event is `resolved` and it appears again, move it back to `unread`
- if an event is `ignored`, keep ignored unless a future policy explicitly expires ignores

This makes "new problem happened again" visible without losing history.

## Storage Design

Add an additive SQLite table:

```sql
CREATE TABLE IF NOT EXISTS system_events (
  id TEXT PRIMARY KEY,
  fingerprint TEXT NOT NULL,
  source TEXT NOT NULL,
  category TEXT NOT NULL,
  severity TEXT NOT NULL,
  status TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  details_json TEXT NOT NULL DEFAULT '{}',
  trace_id TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  job_id TEXT NOT NULL DEFAULT '',
  upstream_id TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  occurrence_count INTEGER NOT NULL DEFAULT 1,
  first_seen_at datetime NOT NULL,
  last_seen_at datetime NOT NULL,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL,
  read_at datetime NULL,
  resolved_at datetime NULL
);
```

Indexes:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS system_events_fingerprint_key
  ON system_events(fingerprint);

CREATE INDEX IF NOT EXISTS idx_system_events_status_last_seen
  ON system_events(status, last_seen_at DESC);

CREATE INDEX IF NOT EXISTS idx_system_events_source_category
  ON system_events(source, category, last_seen_at DESC);

CREATE INDEX IF NOT EXISTS idx_system_events_trace_id
  ON system_events(trace_id, last_seen_at DESC)
  WHERE trace_id <> '';
```

Optional later table for occurrence samples:

```sql
CREATE TABLE IF NOT EXISTS system_event_occurrences (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  event_id TEXT NOT NULL,
  occurred_at datetime NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  details_json TEXT NOT NULL DEFAULT '{}',
  trace_id TEXT NOT NULL DEFAULT '',
  job_id TEXT NOT NULL DEFAULT ''
);
```

The first implementation can skip occurrence rows and keep only aggregate counts.

## Store API

Add store-level types and methods:

```go
type SystemEvent struct {
    ID              string
    Fingerprint     string
    Source          string
    Category        string
    Severity        string
    Status          string
    Title           string
    Message         string
    DetailsJSON     json.RawMessage
    TraceID         string
    SessionID       string
    JobID           string
    UpstreamID      string
    Model           string
    OccurrenceCount int
    FirstSeenAt     time.Time
    LastSeenAt      time.Time
    CreatedAt       time.Time
    UpdatedAt       time.Time
    ReadAt          *time.Time
    ResolvedAt      *time.Time
}

type SystemEventFilter struct {
    Status   string
    Severity string
    Source   string
    Category string
    Query    string
    Since    time.Time
    Page     int
    PageSize int
}

func (s *Store) UpsertSystemEvent(event SystemEvent) (SystemEvent, error)
func (s *Store) ListSystemEvents(filter SystemEventFilter) ([]SystemEvent, int, error)
func (s *Store) SystemEventSummary(since time.Time) (SystemEventSummary, error)
func (s *Store) MarkSystemEventRead(id string) error
func (s *Store) MarkAllSystemEventsRead(filter SystemEventFilter) (int, error)
func (s *Store) ResolveSystemEvent(id string) error
func (s *Store) IgnoreSystemEvent(id string) error
```

## Event Producers

Initial producers should reuse already structured points in the codebase.

### Parser

When parse job fails:

- source: `parser`
- category: `parse_failure`
- severity: `error`
- trace_id: job trace ID
- job_id: parse job ID
- message: job last error
- fingerprint: parser category plus normalized error class

Example:

```text
parser:save_observation:semantic_nodes_unique_constraint
```

### Analyzer

When an analysis run fails:

- source: `analyzer`
- category: `analysis_failure`
- severity: `error`
- details: evaluator set, source type, source ID

### Router

When routing fails:

- source: `router`
- category: `routing_failure`
- severity: `warning` or `error`
- trace_id: trace ID when available
- model
- message: routing error
- fingerprint: `router:<model>:<routing_failure_reason>`

### Upstream Transport

When proxying or response copy fails:

- source: `upstream`
- category: `transport_error`
- severity: `warning` for client disconnects, `error` for upstream/network failures
- trace_id
- upstream_id
- model
- fingerprint based on upstream, endpoint, and normalized error class

Client-disconnect cases such as broken pipe should be classified carefully so they do not dominate critical alerts.

### Store / Monitor / MCP

For internal handler or database failures:

- source: `store`, `monitor`, or `mcp`
- category: `db_error`, `handler_error`, `tool_error`
- severity: `error`
- fingerprint: component plus operation plus normalized error class

First slice can implement these only at known high-value call sites instead of wrapping every query.

## Monitor API

Add:

```text
GET  /api/events
GET  /api/events/summary
POST /api/events/{id}/read
POST /api/events/read-all
POST /api/events/{id}/resolve
POST /api/events/{id}/ignore
GET  /api/events/stream
```

`GET /api/events` filters:

- `status`
- `severity`
- `source`
- `category`
- `q`
- `window`
- `page`
- `page_size`

Summary response:

```json
{
  "unread": 12,
  "critical": 1,
  "error": 7,
  "warning": 4,
  "last_seen_at": "2026-05-15T03:30:00Z",
  "by_source": [
    { "label": "parser", "count": 5 }
  ],
  "by_category": [
    { "label": "parse_failure", "count": 4 }
  ]
}
```

## Real-Time Delivery

The target direction is server push for monitor updates.

Recommended sequence:

1. Implement persisted events and HTTP APIs first.
2. Add SSE at `/api/events/stream` for one-way event notifications.
3. Use SSE to update nav badge and Events page incrementally.
4. Later evaluate WebSocket if the monitor needs bidirectional live control.

SSE is enough for:

- unread count changes
- event created/updated notifications
- lightweight overview refresh hints

Event stream payload:

```json
{
  "type": "system_event.updated",
  "event_id": "evt_...",
  "status": "unread",
  "severity": "error",
  "source": "parser",
  "category": "parse_failure",
  "unread": 12,
  "last_seen_at": "2026-05-15T03:30:00Z"
}
```

## MCP Tool Design

System events should be available through MCP so agents can inspect `llm-tracelab` health and suggest fixes without relying on the Web UI.

Initial read-only tools:

### `list_system_events`

List events with pagination and filters.

Inputs:

- `page`
- `page_size`
- `status`
- `severity`
- `source`
- `category`
- `q`
- `window`

Output:

```json
{
  "items": [
    {
      "id": "evt_...",
      "fingerprint": "parser:save_observation:semantic_nodes_unique_constraint",
      "source": "parser",
      "category": "parse_failure",
      "severity": "error",
      "status": "unread",
      "title": "Observation parse job failed",
      "message": "constraint failed: UNIQUE constraint failed...",
      "trace_id": "5da3c4e9-d153-498b-bdc5-a566d8550069",
      "job_id": "36",
      "occurrence_count": 3,
      "first_seen_at": "2026-05-15T01:43:30Z",
      "last_seen_at": "2026-05-15T01:45:10Z"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

### `get_system_event`

Get one event detail.

Inputs:

- `event_id`
- `include_details`: default `false`

Output includes `details_json` when requested.

### `summarize_system_events`

Return compact event counts for agent triage.

Inputs:

- `window`: `1h`, `24h`, `7d`, `all`
- `status`: default `unread`

Output:

- total
- unread
- by severity
- by source
- by category
- newest events

### `query_unread_system_events`

Convenience tool for agent startup checks.

Inputs:

- `limit`
- `min_severity`

Output:

- unread warning/error/critical events ordered by severity and recency

Future write-capable MCP tools, opt-in only:

- `mark_system_event_read`
- `resolve_system_event`

Write-capable event tools should be gated behind an explicit config flag because MCP clients may be automated.

## Security And Privacy

- Do not store raw request/response bodies in system event details.
- Store trace IDs and links instead of duplicating cassette content.
- Truncate long error messages for list views.
- Keep full details bounded and JSON-structured.
- MCP outputs must be paginated and size-bounded.
- Auth behavior should match Monitor and existing MCP token requirements.

## Development Plan

## Phase 1: Documentation And Contract

Scope:

- Add this design document.
- Link future implementation expectations from MCP docs when tools are implemented.

Exit criteria:

- Event model, API, UI, MCP tools, and rollout phases are documented.

Validation:

- Documentation review.

## Phase 2: Storage And Store API

Scope:

- Add `system_events` table in startup schema.
- Add additive migration/column safety for existing SQLite databases.
- Add store types and methods:
  - upsert
  - list
  - summary
  - mark read
  - mark all read
  - resolve
  - ignore
- Add unit tests for deduplication and status transitions.

Exit criteria:

- Repeated fingerprints update one row and increment occurrence count.
- New occurrence of a read/resolved event moves it back to unread.
- Existing local DB startup still works.

Validation:

```bash
go test ./internal/store
```

Status:

- Completed in Phase 2 implementation.
- Added `system_events` schema, store-level event upsert/list/summary/status APIs, fingerprint deduplication, and status transition tests.
- Verified with `go test ./internal/store`.

Review:

- Scope stayed limited to storage and store APIs.
- No event producers, Monitor API, UI, MCP tools, or server push were added in this phase.
- Next phase should only connect existing failure sources to `UpsertSystemEvent` and add focused producer tests.

## Phase 3: Event Producers

Scope:

- Emit events for parse job failures.
- Emit events for analysis failures.
- Emit events for routing failures.
- Emit events for upstream transport errors.
- Add normalization helpers for error fingerprints.

Exit criteria:

- Current Overview parse failure cases are represented as system events.
- Routing failure bursts are grouped by fingerprint.
- Broken pipe/client disconnect is classified lower than upstream/network failures.

Validation:

```bash
go test ./internal/store ./internal/observeworker ./internal/proxy ./cmd/server
```

Status:

- Completed in Phase 3 implementation.
- Parse job failures, failed analysis runs, routing failures, and upstream transport errors now upsert system events from existing store-level write paths.
- Routing failures are grouped by model and routing failure reason.
- Upstream transport errors are grouped by upstream, endpoint, and normalized error class.
- Client disconnect signals such as broken pipe are classified as warning-level `client_disconnect` events.
- Verified with `go test ./internal/store ./internal/observeworker ./internal/proxy ./cmd/server`.

Review:

- Scope stayed limited to event producers and fingerprint/classification helpers.
- No Monitor HTTP API, UI, MCP tools, or server push were added in this phase.
- Routing failures are treated as router events first and are not duplicated as upstream transport events when both `routing_failure_reason` and `error` are present.
- Next phase should expose the persisted event store through Monitor API endpoints only; frontend navigation, badges, and MCP tools remain later phases.

## Phase 4: Monitor Events API

Scope:

- Add `/api/events`.
- Add `/api/events/summary`.
- Add read/resolve/ignore endpoints.
- Add monitor API tests.

Exit criteria:

- UI can fetch unread count independently of Overview.
- Events can be marked read/resolved without deleting history.

Validation:

```bash
go test ./internal/monitor
```

Status:

- Completed in Phase 4 implementation.
- Added Monitor HTTP endpoints for event list, summary, mark read, mark all read, resolve, and ignore.
- Event list supports status, severity, source, category, text query, window, page, and page size filters.
- Summary exposes unread/severity counts plus source/category breakdowns for the future nav badge and Overview health card.
- Verified with `go test ./internal/monitor` and `go test ./internal/store ./internal/monitor`.

Review:

- Scope stayed limited to Monitor API and JSON view mapping.
- No frontend navigation, Events page, MCP tools, or server push were added in this phase.
- Next phase should implement only the monitor UI wiring: Events nav item, unread badge using `/api/events/summary`, Events page list/actions, and compact Overview system health entry.

## Phase 5: Monitor UI

Scope:

- Add `Events` primary nav item.
- Add unread badge.
- Add Events page with filters, list, details, and actions.
- Update Overview to show compact system health summary and link to Events.

Exit criteria:

- User can distinguish unread from historical events.
- Overview no longer acts as the exception inbox.
- Existing Traces, Sessions, Routing, Audit, and Analysis links still work.

Validation:

```bash
task ui:build
go test ./internal/monitor
```

Status:

- Completed in Phase 5 implementation.
- Added an `Events` primary navigation item with an unread-count badge backed by `/api/events/summary`.
- Added an Events page with status/severity/source/search/window filters, event list, detail panel, related links, and read/resolve/ignore actions.
- Updated Overview to show compact system event health and link to Events instead of acting as the parser failure inbox.
- Rebuilt embedded monitor UI assets.
- Verified with `task ui:build` and `go test ./internal/monitor`.

Review:

- Scope stayed limited to Monitor UI and embedded asset rebuild.
- MCP tools and server push were not added in this phase.
- The nav badge still uses periodic summary polling; Phase 7 will replace or supplement that with SSE.
- Next phase should add read-only MCP event tools only: `list_system_events`, `get_system_event`, `summarize_system_events`, and `query_unread_system_events`.

## Phase 6: MCP Event Tools

Scope:

- Add read-only MCP tools:
  - `list_system_events`
  - `get_system_event`
  - `summarize_system_events`
  - `query_unread_system_events`
- Keep outputs concise and paginated.
- Add MCP integration tests.

Exit criteria:

- An agent can query unread TraceLab internal exceptions through MCP.
- Agent can retrieve enough context to recommend code or config fixes.
- Tool results do not include raw cassette bodies or secrets.

Validation:

```bash
go test ./internal/mcpserver
```

Status:

- Completed in Phase 6 implementation.
- Added read-only MCP tools:
  - `list_system_events`
  - `get_system_event`
  - `summarize_system_events`
  - `query_unread_system_events`
- Tool outputs are paginated or bounded and omit `details_json` unless explicitly requested where applicable.
- Verified with `go test ./internal/mcpserver`.

Review:

- Scope stayed limited to read-only MCP diagnostics.
- No write-capable MCP event mutation tools were added.
- No server push implementation was added in this phase.
- Next phase should add an in-process event broadcaster and `/api/events/stream` SSE, then update the nav badge to consume SSE with existing polling as fallback.

## Phase 7: Server Push

Scope:

- Add an in-process event broadcaster.
- Add `/api/events/stream` using SSE.
- Push summary/event updates when new events are upserted or status changes.
- Update nav badge from SSE with polling fallback.

Exit criteria:

- New backend exceptions appear in the nav badge without waiting for the 60s Overview refresh.
- Existing monitor pages still work when SSE is unavailable.

Validation:

```bash
go test ./internal/monitor
task ui:build
```

## Phase 8: Review And Hardening

Scope:

- Review event categories and severity mappings against real deployed data.
- Add retention/compaction policy if the event table grows too large.
- Decide whether write-capable MCP event tools are safe enough to enable.
- Update `MCP_GUIDE.md`, `MONITOR_GUIDE.md`, and `PROJECT_BASELINE.md`.

Exit criteria:

- Event center is documented as part of the stable monitor capability baseline.
- Any MCP mutation tools are explicitly gated and documented.
