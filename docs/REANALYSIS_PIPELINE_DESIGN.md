# Reanalysis Pipeline Design

## Purpose

`llm-tracelab` keeps raw `.http` cassettes as the source of truth and stores
SQLite indexes, Observation IR, findings, analysis runs, and monitor summaries
as derived data. When a parser, detector, usage extractor, or monitor view is
fixed, users need a first-class way to rebuild those derived layers without
replaying traffic against an upstream model provider.

This document defines the complete reanalysis capability. The goal is not an
MVP-only button. The goal is a durable operational workflow for single trace,
session, batch, API, UI, CLI, and agent-driven repair.

## Goals

- Rebuild derived data from existing raw cassettes without network access.
- Support single trace reparse, deterministic rescan, usage repair, and full
  "rebuild all derived data for this trace" workflows.
- Support session-level and filtered batch reanalysis.
- Make jobs observable in Monitor, System Events, and MCP.
- Keep all schema changes additive and startup-safe for existing SQLite DBs.
- Preserve replay compatibility. `pkg/replay` must continue to read raw
  cassettes without depending on Observation IR or findings.
- Make reanalysis idempotent enough for users and agents to retry safely.
- Separate raw cassette repair from derived table repair when possible, and
  make cassette rewriting explicit when it is necessary.

## Non-Goals

- Do not call upstream LLM providers while rebuilding derived data.
- Do not replace raw `.http` cassettes as the replay source of truth.
- Do not introduce an external queue in this iteration.
- Do not make Monitor silently rewrite cassettes without an explicit repair
  action.
- Do not make deterministic audit findings depend on generative LLM analysis.

## Terminology

### Reparse

Read one cassette, parse request and response bytes through `pkg/observe`, and
replace that trace's Observation IR rows:

- `trace_observations`
- `semantic_nodes`

### Rescan

Read an existing Observation IR, run deterministic detectors, and replace that
trace's findings:

- `trace_findings`

### Usage Repair

Read one cassette, re-extract usage from the raw response with current
`pkg/llm` logic, then update:

- V3 `# meta` usage in the cassette when explicitly requested
- SQLite `logs.prompt_tokens`
- SQLite `logs.completion_tokens`
- SQLite `logs.total_tokens`
- SQLite `logs.cached_tokens`

Usage repair is needed for historical traces recorded before extractor fixes,
such as large `/v1/responses` streaming events whose usage was not indexed.

### Reanalyze

A composed operation that can run:

```text
repair usage -> reparse -> rescan -> session analysis
```

The exact steps are controlled by the caller and recorded in job metadata.

## Current Baseline

The codebase already has important pieces:

- `cmd/server analyze reparse --trace-id`
- `cmd/server analyze scan --trace-id`
- `cmd/server analyze session --session-id`
- `internal/observeworker.ReparseTrace`
- `internal/analyzer.Runner`
- `internal/store.SaveObservation`
- `internal/store.SaveFindings`
- `parse_jobs` and an `observeworker` loop in `serve`
- system events for parse and analysis failures

Current gaps:

- Monitor API has read-only observation and finding endpoints.
- Queue jobs are parse-specific and cannot represent scan, usage repair, or
  session reanalysis as first-class work.
- CLI reparse and scan are separate operations with no reusable orchestration
  object.
- Usage repair is not available even when raw cassettes contain the missing
  usage.
- UI has no explicit reparse/rescan/repair actions or job status feedback.
- MCP exposes read and diagnostic tools, but not a controlled reanalysis
  workflow.

## Architecture

```text
raw cassette
  -> trace index
  -> reanalysis job
      -> optional usage repair
      -> optional observation reparse
      -> optional deterministic rescan
      -> optional session analysis
  -> derived tables
  -> monitor / MCP / CLI status
  -> system events on failure
```

Reanalysis should be implemented as a small orchestration layer instead of
copying logic between CLI, Monitor, worker, and MCP.

Recommended package:

```text
internal/reanalysis
  service.go       // public orchestration API
  jobs.go          // job payloads, step names, validation
  usage_repair.go  // cassette/index usage repair
  runner.go        // sync execution and async worker helpers
```

The service owns step ordering and shared result summaries. It calls existing
lower-level packages:

- `internal/observeworker` for Observation IR parse
- `internal/analyzer` for deterministic findings
- `internal/sessionanalysis` for session summaries
- `internal/store` for persistence
- `pkg/recordfile` for cassette read/write
- `pkg/llm` for usage extraction

## Job Model

Keep `parse_jobs` for backward compatibility, but introduce a general-purpose
`analysis_jobs` table for all new work.

```sql
CREATE TABLE IF NOT EXISTS analysis_jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_type TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  status TEXT NOT NULL,
  steps_json TEXT NOT NULL DEFAULT '[]',
  result_json TEXT NOT NULL DEFAULT '{}',
  last_error TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL,
  started_at datetime NULL,
  finished_at datetime NULL
);

CREATE INDEX IF NOT EXISTS idx_analysis_jobs_status_updated
  ON analysis_jobs(status, updated_at ASC);

CREATE INDEX IF NOT EXISTS idx_analysis_jobs_target
  ON analysis_jobs(target_type, target_id, created_at DESC);
```

Job status:

- `queued`
- `running`
- `completed`
- `failed`
- `canceled`

Job types:

- `trace_reparse`
- `trace_rescan`
- `trace_repair_usage`
- `trace_reanalyze`
- `session_reanalyze`
- `batch_reanalyze`

Step names:

- `repair_usage`
- `reparse_observation`
- `scan_findings`
- `analyze_session`

Idempotency rules:

- completed jobs are immutable history
- a new user action creates a new job
- worker selection should avoid running two queued/running jobs with the same
  target and overlapping mutating steps at the same time
- each step replaces only its own derived data

## Trace Operations

### Reparse Trace

Input:

```json
{
  "scan": true,
  "repair_usage": false,
  "rewrite_cassette": false,
  "mode": "sync"
}
```

Behavior:

1. load trace by ID
2. optionally repair usage
3. parse cassette into Observation IR
4. save observation, replacing old semantic nodes
5. optionally scan findings
6. return job/result summary

### Rescan Trace

Input:

```json
{
  "mode": "sync"
}
```

Behavior:

1. load existing observation
2. run deterministic detectors
3. replace findings
4. return finding count and severities

If no observation exists, API should return `409 Conflict` with an actionable
message: run reparse first, or call reanalyze with `reparse=true`.

### Repair Usage

Input:

```json
{
  "rewrite_cassette": true,
  "mode": "sync"
}
```

Behavior:

1. read cassette
2. extract usage from raw response using current extractor
3. compare extracted usage with header/index usage
4. update SQLite index
5. if `rewrite_cassette=true`, rewrite V3 prelude metadata while preserving raw
   request and response bytes exactly

V2 files:

- SQLite index can be repaired.
- Cassette rewrite should be refused unless an explicit migration flow handles
  V2-to-V3 conversion.

## Session And Batch Operations

### Session Reanalyze

Input:

```json
{
  "repair_usage": true,
  "reparse": true,
  "scan": true,
  "session_analysis": true,
  "rewrite_cassettes": false,
  "mode": "async"
}
```

Behavior:

1. enumerate traces by session from SQLite
2. run selected trace steps in deterministic recorded order
3. build a new session analysis run if requested
4. persist aggregate job result

Session reanalysis should default to async because it can touch many traces.

### Batch Reanalyze

Supported filters should match stable trace list filters where possible:

- provider
- model
- endpoint
- selected upstream/channel
- status
- missing usage
- session id
- time window
- parser version
- detector version

Batch jobs are async-only.

## Monitor API

Add write endpoints under existing authenticated Monitor API:

```text
POST /api/traces/{trace_id}/reparse
POST /api/traces/{trace_id}/scan
POST /api/traces/{trace_id}/repair-usage
POST /api/traces/{trace_id}/reanalyze

POST /api/sessions/{session_id}/reanalyze

GET  /api/analysis/jobs
GET  /api/analysis/jobs/{job_id}
POST /api/analysis/jobs/{job_id}/cancel
```

Response shape:

```json
{
  "job": {
    "id": 123,
    "job_type": "trace_reanalyze",
    "target_type": "trace",
    "target_id": "trace-id",
    "status": "completed"
  },
  "result": {
    "usage_repaired": true,
    "observation_status": "parsed",
    "request_nodes": 12,
    "response_nodes": 8,
    "findings": 1
  }
}
```

`mode=sync` may execute immediately and still create a job row for auditability.
`mode=async` creates a queued job and returns `202 Accepted`.

## Monitor UI

Trace detail actions:

- Summary/Performance: `Repair usage`
- Protocol tab: `Reparse`
- Audit tab: `Rescan`
- Overflow/menu action: `Reanalyze trace`

Action behavior:

- show a confirmation modal for cassette rewriting
- show job status toast or inline banner
- refresh affected tabs after completion
- show System Event link when a job fails

Session detail actions:

- `Reanalyze session`
- checkboxes: repair usage, reparse protocol, rescan audit, rebuild session
  analysis

Analysis/Events views:

- Analysis view lists recent reanalysis jobs.
- Events link failed jobs to their trace/session and to the failed job detail.

## CLI

Keep existing commands, but route them through `internal/reanalysis`:

```bash
llm-tracelab analyze reparse --trace-id xxx --scan
llm-tracelab analyze scan --trace-id xxx
llm-tracelab analyze repair-usage --trace-id xxx --rewrite-cassette
llm-tracelab analyze reanalyze --trace-id xxx --repair-usage --reparse --scan
llm-tracelab analyze session --session-id xxx --reparse --scan
```

Existing command names should remain compatible.

## MCP

Expose controlled MCP tools after Monitor API exists:

- `reparse_trace`
- `rescan_trace`
- `repair_trace_usage`
- `reanalyze_session`
- `list_analysis_jobs`
- `get_analysis_job`

MCP write tools must require the same auth model as Monitor/API and should
return job IDs instead of streaming raw cassette content.

## System Events

Create or update system events for:

- job execution failure
- usage extraction failed for a successful trace
- cassette rewrite refused or failed
- observation parse failed
- deterministic scan failed
- session analysis failed

Fingerprint examples:

```text
analysis_job:trace_reanalyze:trace-id:parse_error_hash
usage_repair:trace-id:missing_usage
session_reanalyze:session-id:scan_failed
```

Failures should remain operational metadata and should not modify raw cassettes.

## Safety And Compatibility

- No network calls during reanalysis.
- Raw request/response bytes must not be altered by usage repair.
- V3 prelude rewrite must preserve `# event:` lines unless explicitly replacing
  derived events.
- V2 read compatibility remains required.
- `pkg/replay` must not depend on analysis tables.
- Failed async jobs must not leave partial transaction state inside one step.
- Reanalysis must tolerate missing raw files and report clear errors.

## Phased Development Plan

### P0. Stream Usage Baseline

Status: completed.

Scope:

- Fix long SSE usage extraction.
- Add regression test.

Commit:

```text
fix: parse usage from long stream events
```

### P1. Design Baseline

Scope:

- Add this design document.
- Link it from project baseline and v1 storage/development docs.
- No runtime code changes.

Acceptance:

- Complete end-state design is documented.
- Phases cover trace, session, job, UI, CLI, MCP, and usage repair.

Suggested checks:

```bash
task fmt:check
```

Commit:

```text
docs: design reanalysis pipeline
```

### P2. Backend Job Foundation

Scope:

- Add `analysis_jobs` schema and store APIs.
- Add `internal/reanalysis` service with sync trace operations.
- Implement trace reparse, rescan, and trace reanalyze orchestration.
- Keep existing CLI behavior, rerouted through service where practical.

Acceptance:

- Unit tests cover job lifecycle and trace reanalyze result.
- Existing `analyze reparse` and `analyze scan` still pass.

Suggested checks:

```bash
go test ./internal/store ./internal/reanalysis ./cmd/server -run 'AnalysisJob|Analyze|Reanalysis' -count=1
task check:quick
```

Commit:

```text
feat: add reanalysis job foundation
```

### P3. Usage Repair

Scope:

- Extract usage from existing cassettes.
- Repair SQLite index.
- Rewrite V3 prelude usage when requested.
- Refuse V2 rewrite with clear error.

Acceptance:

- Regression fixture for historical long SSE trace shape.
- Index-only repair and V3 rewrite repair both tested.

Commit:

```text
feat: repair usage from recorded cassettes
```

### P4. Monitor API

Scope:

- Add trace reparse/scan/repair/reanalyze endpoints.
- Add session reanalyze endpoint.
- Add job list/detail endpoints.
- Surface failures as system events.

Acceptance:

- API tests cover sync trace actions and async session job creation.
- Auth wrapper applies to all write endpoints.

Commit:

```text
feat: expose reanalysis monitor api
```

### P5. Async Worker And Session/Batch Execution

Scope:

- Add worker for `analysis_jobs`.
- Implement session reanalysis.
- Implement filtered batch reanalysis if filters are stable enough.
- Add cancellation status support where safe.

Acceptance:

- Worker processes queued jobs.
- Session reanalysis rebuilds selected trace derived data and writes a new
  session analysis run.

Commit:

```text
feat: run async reanalysis jobs
```

### P6. Monitor UI

Scope:

- Add trace action buttons and confirmation flows.
- Add session reanalysis modal.
- Add job status list/detail.
- Refresh affected tabs after completion.

Acceptance:

- Users can reparse, rescan, repair usage, and reanalyze from Monitor.
- Failed jobs link to Events.

Commit:

```text
feat: add reanalysis controls to monitor
```

### P7. CLI/MCP Completion And Docs

Scope:

- Add CLI usage repair and composed reanalyze commands.
- Add MCP write tools for controlled reanalysis.
- Update Monitor Guide, MCP Guide, Project Baseline, and Maintainer Baseline.

Acceptance:

- CLI, Monitor API, UI, and MCP share one service.
- Documentation describes operational workflows and caveats.

Commit:

```text
docs: document reanalysis workflows
```

## Review Checklist After Each Phase

After every phase:

1. Confirm raw cassette replay compatibility is unchanged.
2. Confirm no new network dependency was introduced.
3. Confirm schema changes are additive and startup-safe.
4. Confirm job failures create actionable status or system events.
5. Confirm the next phase still matches this design and has not expanded into
   unrelated monitor or parser refactors.
