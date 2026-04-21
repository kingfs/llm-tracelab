# Maintainer Baseline

## Purpose

This document records the current implementation baseline and the main maintenance constraints for storage, monitor behavior, and session aggregation.

Use it when changing:

- `internal/store`
- `internal/monitor`
- `internal/recorder`
- `pkg/recordfile`
- `pkg/replay`
- `pkg/llm`

## Baseline Architecture

Current responsibility split:

- raw `.http` cassettes are the source of truth for replay and detail reconstruction
- SQLite is the source of truth for list pages, aggregate statistics, filtering, pagination, and session grouping
- monitor detail pages may read raw cassettes on demand
- monitor list pages should not depend on rescanning raw files

This split is deliberate and should remain stable unless there is a strong reason to redesign replay and monitor storage together.

## Record Format Invariants

Active write format:

- `LLM_PROXY_V3`

Read compatibility requirement:

- continue to support legacy `LLM_PROXY_V2`

When changing storage or recorder behavior:

1. update `pkg/recordfile` first
2. then adapt recorder, monitor, and replay together
3. preserve human-inspectable `.http` payloads
4. do not make replay depend on network access

## Session Aggregation Baseline

Session aggregation is already implemented and is not experimental.

Current extraction order:

1. `Session_id`
2. `X-Codex-Turn-Metadata.session_id`
3. `X-Codex-Window-Id` prefix before `:`
4. empty string

Current indexed grouping fields in `logs`:

- `session_id`
- `session_source`
- `window_id`
- `client_request_id`

The first strong use case is OpenAI-compatible and Codex-like traffic, but the storage model is intentionally additive so other providers can attach grouping metadata later without rewriting the monitor around an OpenAI-only concept.

## Monitor Baseline

Stable API surface:

- `GET /api/traces`
- `GET /api/traces/:traceID`
- `GET /api/sessions`
- `GET /api/sessions/:sessionID`
- MCP `list_traces`
- MCP `get_trace`
- MCP `list_sessions`
- MCP `get_session`
- MCP `list_upstreams`
- MCP `get_upstream`
- MCP `query_failures`
- MCP `replay_trace`
- MCP `replay_session`
- MCP `create_dataset_from_traces`
- MCP `create_dataset_from_session`
- MCP `append_dataset_examples`
- MCP `list_datasets`
- MCP `get_dataset`
- MCP `run_eval_on_dataset`
- MCP `run_eval_on_traces`
- MCP `list_eval_runs`
- MCP `get_eval_run`
- MCP `list_scores`
- MCP `compare_eval_runs`
- MCP `create_experiment_from_eval_runs`
- MCP `list_experiment_runs`
- MCP `get_experiment_run`

Stable UI perspectives:

- `Requests`
- `Sessions`

Stable trace detail tabs:

- `Timeline`
- `Summary`
- `Raw Protocol`
- `Declared Tools` when applicable

Stable deep-link parameters:

- `tab`
- `from_session`
- `view`
- `focus`

Stable focus targets:

- `failure`
- `response`
- `timeline`
- `timeline_error`

Changes to these surfaces should be treated as product-facing changes and should be documented explicitly.

## MCP Baseline

Current MCP baseline is intentionally narrow:

- transport is `stdio`
- implementation uses the official Go MCP SDK
- tool surface is read-only
- MCP handlers reuse current monitor/store behavior rather than introducing a second query stack
- eval-run comparison is computed from stored scores
- persisted experiments store linkage and aggregate summary, not duplicated score payloads

Do not:

- make MCP the source of truth for replay or storage
- add broad write-capable mutation tools without an explicit milestone
- fork monitor semantics into a divergent MCP-only query model unless there is a strong reason

## SQLite Upgrade Constraints

Additive schema evolution on existing local databases is a hard requirement.

Important rule:

- all newly required columns must exist before any index or query depends on them

This matters because users may upgrade with an older `trace_index.sqlite3` already present.

Recent regression that is now fixed:

- startup and `migrate` could fail with `no such column: session_id` when indexes referring to session columns were created before `ensureColumn(...)` finished

The expected behavior now is:

1. open existing DB
2. ensure missing columns exist
3. backfill additive metadata if needed
4. create indexes that depend on those columns
5. continue startup successfully without requiring a manual DB reset

Any future schema change must preserve this property.

## Startup And Rebuild Expectations

Current supported flows:

- clean startup on a fresh output directory
- clean startup on an old output directory with an existing SQLite DB
- rebuild from raw cassettes
- explicit migration from V2 to V3 when requested

When modifying store initialization, verify at least:

- fresh DB creation
- startup against an older schema
- backfill paths for newly added metadata
- `migrate` behavior against an older schema

## Replay Compatibility Constraints

`pkg/replay` is a hard requirement and should constrain monitor/storage changes.

Do not:

- make SQLite the replay source of truth
- require normalized metadata that cannot be reconstructed from the raw cassette
- rewrite old cassettes implicitly during normal startup

Prefer:

- additive metadata indexing in SQLite
- explicit migration tooling when file rewrites are needed
- keeping the raw request/response readable for manual debugging

## Testing Expectations

When changing storage or monitor behavior, the baseline expectation is:

- Go tests pass
- monitor frontend builds successfully when frontend assets change
- embedded assets remain in sync with the built frontend

When changing schema upgrade logic, include regression coverage for older DB states where feasible.

## Current Done State

This is considered implemented at the current baseline:

- session metadata extraction and persistence
- dual-view monitor home page
- session list and session detail APIs
- grouped session inspection with failure context
- trace/session deep links and tab selection
- trace focus targets including `timeline_error`
- legacy SQLite schema upgrade fix for session columns
- multi-upstream runtime routing with persisted model catalog state
- per-trace routing metadata and selected-upstream health context
- upstream analytics, drilldown, routing failure analytics, and health-threshold exposure

For maintainers, this implies an important convergence rule:

- do not continue expanding monitor surfaces as a substitute for unfinished routing/runtime work
- once the multi-upstream routing loop is closed, further monitor changes should be justified as focused follow-up work rather than part of the core delivery

The current core delivery is already closed around:

1. multi-upstream config compatibility
2. runtime target selection
3. local model coverage persistence
4. replay-safe routing metadata
5. operator-visible routing diagnostics

## Reasonable Next Refinements

The following work is still reasonable, but is not required to understand the current baseline:

- more stable node-level timeline anchors
- direct linking to tool call or tool response nodes
- richer focus/highlight lifecycle behavior
- more operator-facing troubleshooting documentation
