# Session Aggregation Plan

## Goal

Add a second monitor perspective that groups related traces into a higher-level session view while preserving the existing per-request trace view.

The target UX is:

1. `Requests` view: the current raw trace list, one row per recorded request
2. `Sessions` view: grouped rows, one row per session or future grouping key
3. session detail: one summary page plus the ordered trace list inside that group

This feature must remain:

- replay-compatible
- local-first
- additive to existing `.http` cassettes
- efficient for monitor list/statistics queries

## Why This Is Needed

Some clients, especially OpenAI-compatible toolchains such as Codex, emit multiple related requests that belong to one interactive session. Today the monitor only exposes request-level rows, so users lose the higher-level context:

- a single coding session may fan out into many `POST /v1/responses` requests
- latency/token patterns are hard to interpret when split into isolated rows
- debugging multi-turn tool loops is less useful without session-level grouping

The raw request headers already contain enough information to infer grouping keys in many cases, for example:

- `Session_id`
- `X-Codex-Turn-Metadata.session_id`
- `X-Codex-Window-Id`
- `X-Client-Request-Id`

The missing piece is indexing and query support in SQLite plus a monitor UI that can switch perspectives.

## Current State

Current behavior:

- recorder stores the full raw HTTP request and response in `.http`
- cassette prelude stores normalized request-level metadata and timeline events
- SQLite indexes request-level summary fields only
- monitor list API reads request rows from SQLite
- monitor detail pages read raw cassette content on demand

Current limitation:

- session information exists in the raw request header bytes, but is not extracted into the metadata index
- therefore the monitor cannot group efficiently without rescanning raw files

## Non-Goals

This design does not try to:

- redefine replay semantics
- require all providers to expose a universal session identifier
- rewrite existing cassettes to a new format immediately
- hide provider differences behind a fake universal conversation model

## Design Principles

1. Raw `.http` files remain the source of truth.
2. SQLite remains the source for monitor list/statistics/aggregation queries.
3. Grouping must be additive and optional.
4. Missing grouping data must not break existing traces.
5. The grouping design must extend beyond OpenAI-compatible traffic.

## Terminology

- `trace`: one recorded HTTP request/response exchange
- `session`: one logical group of related traces
- `group key`: the canonical indexed value used to aggregate traces
- `group source`: which request header or metadata field produced the group key

For the first implementation, `session` is the primary grouping concept presented in the UI, but the storage design should allow future group types without a schema reset.

## Proposed Architecture

### 1. Add group metadata extraction at index time

When the store indexes a `.http` cassette, it should parse the raw request header section and extract session-related fields.

This happens during:

- live recording upsert
- startup sync
- rebuild/backfill

The extraction should not require any upstream access and should not modify replay behavior.

### 2. Persist extracted group fields in SQLite

The `logs` table should be extended with grouping metadata so list and aggregation queries stay fast.

Recommended new columns:

- `session_id TEXT NOT NULL DEFAULT ''`
- `session_source TEXT NOT NULL DEFAULT ''`
- `window_id TEXT NOT NULL DEFAULT ''`
- `client_request_id TEXT NOT NULL DEFAULT ''`

Optional future-safe fields:

- `group_key TEXT NOT NULL DEFAULT ''`
- `group_type TEXT NOT NULL DEFAULT ''`

For the first implementation, `group_key` may equal `session_id` and `group_type` may equal `session`.

### 3. Add session-level aggregation queries

The store should expose session aggregation methods alongside the existing request-level list methods.

Examples:

- `ListSessionPage(page, pageSize)`
- `GetSession(sessionID)`
- `ListTracesBySession(sessionID, page, pageSize)`
- `SessionStats()`

### 4. Add monitor APIs for grouped views

New API surface:

- `GET /api/sessions`
- `GET /api/sessions/:sessionID`

Existing request APIs stay unchanged:

- `GET /api/traces`
- `GET /api/traces/:traceID`

### 5. Add dual-perspective monitor UI

The monitor home page should expose a view switch:

- `Sessions`
- `Requests`

The request view stays close to current behavior.
The session view shows grouped summaries and drills into a session detail page.

## Group Key Strategy

The grouping logic should be deterministic and ordered by confidence.

### Primary extraction order

1. `Session_id` request header
2. `X-Codex-Turn-Metadata.session_id` parsed from JSON header value
3. `X-Codex-Window-Id` prefix before `:`
4. empty string if none exist

Supporting fields that are useful but should not define the session group by default:

- `X-Client-Request-Id`
- recorder `request_id`

Reasoning:

- `Session_id` is the clearest direct session boundary when present
- `X-Codex-Turn-Metadata.session_id` provides a semantic fallback for Codex traffic
- `X-Codex-Window-Id` can approximate a stable session/workspace window when explicit session fields are missing
- `X-Client-Request-Id` is often request-unique and is therefore a bad primary grouping key

### Normalized metadata shape

The extractor should return something like:

```go
type GroupingInfo struct {
    SessionID       string
    SessionSource   string
    WindowID        string
    ClientRequestID string
}
```

`SessionSource` values should be explicit and stable, for example:

- `header.session_id`
- `header.x_codex_turn_metadata.session_id`
- `header.x_codex_window_id`
- `none`

## Request Header Extraction

### Parsing location

Do not parse session fields in the React monitor or on every detail request.
The extraction belongs in indexing flow because:

- it is needed for list queries
- it should be computed once and cached in SQLite
- startup sync already reads changed cassettes

### Parsing method

Use `recordfile.ParsePrelude` and `recordfile.ExtractSections` to get the request bytes.
Then parse only the request header section.

Requirements:

- tolerate V2 and V3 files
- handle missing or malformed headers safely
- treat header names case-insensitively
- cap JSON parsing to the single header value being interpreted

### Helper placement

Recommended options:

- `internal/store`: if used only for indexing
- `pkg/recordfile`: if we want reusable request header extraction utilities later

Preferred first step:

- keep the session/group extractor in `internal/store` or a nearby internal helper
- move to `pkg/recordfile` only if reuse emerges

## SQLite Schema Evolution

### Phase 1 schema

Extend `logs` with:

- `session_id`
- `session_source`
- `window_id`
- `client_request_id`

Add indexes:

- `CREATE INDEX IF NOT EXISTS idx_logs_session_id_recorded_at ON logs(session_id, recorded_at DESC) WHERE session_id <> '';`
- optional: `CREATE INDEX IF NOT EXISTS idx_logs_window_id_recorded_at ON logs(window_id, recorded_at DESC) WHERE window_id <> '';`

### Migration strategy

Use the existing additive schema pattern:

- `ensureColumn(...)`
- backfill on startup

Backfill process:

1. select rows where session fields are empty
2. read raw cassette
3. extract request headers
4. update new columns

This preserves compatibility with existing local databases and old cassette files.

## Store Query Design

### Request list

Existing request list remains available and should include the indexed session field in its row payload for future UI hints.

Recommended additions to request list response:

- `session_id`
- `session_source`

### Session list

Each session row should summarize one group.

Recommended fields:

- `session_id`
- `session_source`
- `request_count`
- `first_seen`
- `last_seen`
- `last_model`
- `provider_count`
- `providers`
- `success_request`
- `failed_request`
- `success_rate`
- `total_tokens`
- `avg_ttft`
- `total_duration_ms`
- `stream_count`

Sorting:

- default by `last_seen DESC`

Aggregation notes:

- `avg_ttft` should follow the same success-only semantics used by request stats
- `success_rate` should be based on request count inside the session
- `last_model` should come from the latest trace in the session, not arbitrary SQL grouping

### Session detail

The session detail API should return:

- one session summary object
- ordered trace list for that session
- optional small timeline hints in the future

The detail page should not try to merge raw HTTP payloads into a synthetic single conversation in phase 1.

## Monitor API Design

### `GET /api/sessions`

Query params:

- `page`
- `page_size`

Response sketch:

```json
{
  "items": [
    {
      "session_id": "019d9659-34ca-7b03-917e-9f6bb8bc550b",
      "session_source": "header.session_id",
      "request_count": 12,
      "first_seen": "2026-04-16T03:10:00Z",
      "last_seen": "2026-04-16T03:19:00Z",
      "last_model": "gpt-5-codex",
      "providers": ["openai"],
      "success_rate": 100,
      "total_tokens": 38192,
      "avg_ttft": 412
    }
  ],
  "page": 1,
  "page_size": 50,
  "total": 20,
  "total_pages": 1,
  "refreshed_at": "2026-04-16T03:20:00Z"
}
```

### `GET /api/sessions/:sessionID`

Response sketch:

```json
{
  "summary": {
    "session_id": "019d9659-34ca-7b03-917e-9f6bb8bc550b",
    "session_source": "header.session_id",
    "request_count": 12,
    "first_seen": "2026-04-16T03:10:00Z",
    "last_seen": "2026-04-16T03:19:00Z",
    "total_tokens": 38192,
    "avg_ttft": 412,
    "success_rate": 100
  },
  "traces": [
    {
      "id": "trace-id-1",
      "recorded_at": "2026-04-16T03:10:01Z",
      "model": "gpt-5-codex",
      "endpoint": "/v1/responses",
      "status_code": 200,
      "duration_ms": 2410,
      "ttft_ms": 318
    }
  ]
}
```

## Monitor UI Design

### Home page

Replace the single list-only presentation with a perspective switch.

Suggested controls:

- segmented tabs or pill switch
- URL query parameter such as `?view=sessions` or `?view=requests`

Behavior:

- `Requests`: existing list behavior
- `Sessions`: grouped list behavior

### Session row content

Each row should emphasize:

- session id
- session source
- last activity time
- request count
- total tokens
- success rate
- recent model/provider badges

Optional display:

- a compact sparkline or duration strip later

### Session detail page

Suggested route:

- `/sessions/:sessionID`

Content:

- session summary cards
- chronological trace list
- links to existing trace detail pages

Phase 1 should avoid building a synthetic merged transcript. The detail page is mainly an aggregation shell plus ordered trace navigation.

## Cassette Format Decision

### Phase 1

Do not change the cassette file format.

Reason:

- the required data already exists in raw request headers
- replay does not need session metadata
- additive SQLite indexing is enough for the monitor feature

### Possible Phase 2

Optionally add group metadata to V3 `# meta:` later, for example:

- `session_id`
- `session_source`

This is not required for the first release.
If introduced later, it should remain optional and additive.

## Compatibility

### Replay compatibility

Unaffected.
`pkg/replay` continues to use raw request/response content from the cassette.

### V2/V3 cassette compatibility

Preserved.
Both formats can still be indexed because grouping data comes from raw request headers, not only from the V3 prelude.

### Existing databases

Supported through additive columns plus startup backfill.

## Risks And Mitigations

### 1. Incomplete grouping for some providers

Risk:

- many providers or SDKs may not expose a stable session identifier

Mitigation:

- make grouping optional
- leave traces without `session_id` in request view only
- keep extraction source explicit for debugging

### 2. False grouping from weak fallback fields

Risk:

- using request-unique fields or unstable window IDs may group incorrectly

Mitigation:

- do not use `X-Client-Request-Id` as the primary session key
- keep fallback order narrow and explicit
- make source visible in UI and API

### 3. Backfill cost on large log directories

Risk:

- startup backfill may need to rescan many cassettes once

Mitigation:

- only backfill rows with empty session fields
- rely on existing freshness checks for normal sync
- document `rebuild` when users want a full refresh

### 4. SQL complexity for last-model and provider aggregation

Risk:

- grouped SQL can become hard to maintain

Mitigation:

- keep phase 1 aggregation modest
- compute some display fields in Go after a compact SQL query if needed

## Test Plan

### Store tests

Add tests for:

- extracting `Session_id` from raw request headers
- extracting `session_id` from `X-Codex-Turn-Metadata`
- falling back to `X-Codex-Window-Id`
- session list aggregation correctness
- session detail trace ordering
- startup backfill into new columns

### Monitor API tests

Add tests for:

- `/api/sessions` pagination and payload shape
- `/api/sessions/:id` summary and trace list
- behavior when session id is missing or unknown

### UI tests

At minimum, verify:

- view switch between `Requests` and `Sessions`
- session rows render summary metrics
- session detail links to trace detail

### Regression expectations

- existing `/api/traces` behavior remains stable
- existing trace detail pages remain stable
- no network dependency is introduced

## Implementation Plan

### Phase 1. Group metadata extraction and indexing

Deliverables:

- new SQLite columns
- request header extractor
- live upsert and startup backfill support
- store tests

Exit criteria:

- every newly indexed trace stores `session_id` when available
- old cassettes can be backfilled without manual migration

### Phase 2. Session query APIs

Deliverables:

- store aggregation methods
- `/api/sessions`
- `/api/sessions/:sessionID`
- monitor API tests

Exit criteria:

- grouped session list and detail are queryable from SQLite only

### Phase 3. Monitor dual-view UI

Deliverables:

- `Sessions` and `Requests` switch on the monitor home page
- session list page
- session detail page
- basic UI tests if the repo adds a frontend test harness

Exit criteria:

- users can inspect traffic from either perspective without losing the current request detail flow

### Phase 4. Polish and documentation

Deliverables:

- README updates
- architecture doc update
- edge-case cleanup

Exit criteria:

- feature is documented as a monitor capability and the storage approach is clear

## Recommended First Increment

The first implementation slice should be intentionally narrow:

1. extract and index `session_id`
2. support `Session_id` and `X-Codex-Turn-Metadata.session_id`
3. add session list/detail APIs
4. add monitor view switch

This yields immediate value for the observed Codex/OpenAI traffic while keeping the schema and API ready for broader provider grouping later.
