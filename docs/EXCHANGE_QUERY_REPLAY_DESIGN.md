# Exchange Query, Replay, Monitor/MCP, and Migration Impact Design

Status: design draft
Scope: impact plan for introducing `entry` and `model` exchange records without breaking existing cassette replay.

## Problem Statement

The current storage and audit path treats each recorded `.http` cassette primarily as one upstream trace. The local Responses server and Codex-compatible workflows can produce a different shape:

- one client-facing Codex/Responses request enters llm-tracelab
- the runtime may call one or more upstream LLM models
- the runtime may run hosted tools, MCP tools, compaction, or tool loops between model calls
- each model call can have its own raw HTTP exchange and cassette

The design goal is to make this relationship queryable and visible while preserving the existing record/replay contract:

- raw `.http` cassettes remain the source of truth for replay and detail inspection
- SQLite/Postgres indexes improve query, grouping, and monitor views
- `pkg/replay` continues to work with a single cassette path and does not require a database
- old cassettes without `exchange_kind` continue to parse and replay

## Terms

- `entry exchange`: client-facing HTTP request received by llm-tracelab, for example a Codex `/v1/responses` request handled by the local Responses server. It represents the user-visible API exchange.
- `model exchange`: outbound model-provider HTTP request made while serving an entry exchange. It maps to an upstream LLM call and normally has a raw upstream cassette.
- `exchange_kind`: stable kind stored in cassette metadata and indexed storage. Initial values should be `entry`, `model`, and `legacy`.
- `parent_exchange_id`: optional link from a model exchange to its entry exchange.
- `request_audit_id` / `response_id`: Responses audit correlation keys already used by Monitor and MCP.
- `trace_id`: existing trace/cassette identifier used by the trace index and replay paths.

## Compatibility Contract

### `pkg/replay`

`pkg/replay` must remain cassette-first:

- `replay.NewTransport(filename)` and `replay.ReplayFile(filename, opts)` continue to accept exactly one `.http` file.
- Replay must not open SQLite/Postgres, query `request_audits`, or require `upstream_exchanges`.
- Replay response selection remains based on the HTTP response bytes embedded in the cassette.
- V2 and V3 cassette parsing remains supported through `pkg/recordfile`.

`exchange_kind` should be exposed as metadata, not as a replay dependency:

- add `ExchangeKind` to `recordfile.MetaData` when the storage/format work lands
- leave the zero value meaningful for old cassettes
- make `ReplayFile` summary include the parsed kind if callers need to label the file
- do not make `RoundTrip` reject `entry` cassettes

The expected behavior is:

- replaying a `model` cassette replays the provider response for one model call
- replaying an `entry` cassette replays the client-facing response for one Codex/Responses request, if such a cassette was recorded
- replaying an old cassette works exactly as today and is labeled as inferred `legacy` or `model` by query surfaces, not by the transport

Entry/model relationships are exposed outside `pkg/replay`:

- Monitor/MCP/audit query returns the entry exchange plus a list of child model exchanges
- raw cassette links point to each exchange cassette path
- tests that need a full multi-call scenario should use audit/query fixtures or higher-level Responses runtime fixtures, not ask `pkg/replay.Transport` to orchestrate multiple cassettes

This keeps the package suitable for unit tests that intentionally replay one upstream call without running the llm-tracelab database.

## Observation and Analysis

### Parser Selection

`observeworker` should parse cassettes by `exchange_kind` and protocol hints:

- `model`: parse with the existing provider/protocol registry, using request/response bodies and stream flags as today.
- `entry`: parse with an entry-aware parser only when the entry payload is semantically useful, for example local Responses server input/output, final response status, requested model, and client-visible tool items.
- missing kind: infer using cassette metadata and HTTP path, then choose the parser.

Recommended selection order:

1. explicit `exchange_kind` from cassette prelude metadata
2. indexed exchange kind from DB row, when reparse was started from a DB-backed job
3. request path and metadata inference
4. current default provider parser behavior

Inference rules for old cassettes:

- `/v1/responses`, `/responses`, or configured local Responses server path handled by llm-tracelab can be `entry` only when the cassette is known to represent the client-facing local server request.
- OpenAI-compatible `/v1/chat/completions`, upstream `/v1/responses`, Anthropic `/v1/messages`, Gemini `generateContent`, and Vertex model paths default to `model`.
- existing proxy cassettes without request audit linkage default to `model`, because replay compatibility historically means one cassette equals one upstream/provider HTTP exchange.
- ambiguous records should be labeled `legacy` in query responses and parsed with the current default parser instead of being rewritten.

### `observeworker`

The worker should remain job-driven and cassette-local:

- continue to load the cassette path from `store.GetByID(traceID)`
- parse the prelude with `recordfile.ParsePrelude`
- pass `exchange_kind`, `parent_exchange_id`, `request_audit_id`, `response_id`, and protocol hints into `observe.ParseInput` once those fields exist
- persist observations per exchange trace

For entry exchanges, the observation should focus on client-visible semantics:

- request operation: create, compact, input_items, or unknown
- response status and response id
- model requested by the client
- final output text/items when present
- tool call references when present in the entry response
- child model exchange count and ids from the index, if available

For model exchanges, the observation should preserve current model/provider semantics:

- model messages, tool calls, tool results, server tools, safety/refusal signals
- token usage and stream status
- route/upstream metadata

### Analyzer

`internal/analyzer` should not parse cassettes directly. It should continue to consume `observe.TraceObservation`.

Analyzer impact is limited to semantics:

- model exchange observations run the current detectors normally
- entry exchange observations run only detectors that make sense for client-visible data
- findings should include `exchange_kind` once the observation model carries it
- rollups should be able to show "finding on entry" versus "finding on model call"

Avoid duplicate findings:

- if both an entry exchange and child model exchange contain the same tool call text, prefer attaching high-risk command findings to the most specific child model/tool node
- entry-level findings are for client input/output risks, request failures, or missing child model evidence

## Monitor and MCP Query Model

### List Views

Monitor should add an entry-centric view for Responses/Codex requests:

- primary row: one `entry` exchange or one `request_audit`
- columns: time, client request id, response id, conversation id, status, operation, requested model, final status code, duration, model call count, tool call count, compact count, total tokens, error
- expansion: child model exchanges ordered by start time
- each child row: model, provider/upstream, endpoint, stream flag, status code, duration, tokens, cassette link

The existing trace list can remain cassette-centric:

- show `exchange_kind` as a filter and badge
- default existing behavior can keep showing all cassettes
- provide filters: `entry`, `model`, `legacy`, `has_children`, `parent_exchange_id`, `request_audit_id`, `response_id`

This avoids forcing every user into the audit model while giving Codex workflows a natural "one request produced N model calls" view.

### Detail Views

Entry detail should show:

- client-facing request/response summary
- execution event timeline
- tool call lifecycle
- child model exchange table
- raw entry cassette link, when present
- raw model cassette links for each child call

Model exchange detail should show:

- current raw HTTP request/response detail
- parent entry/request audit link, when present
- route/upstream metadata
- observation/analyzer output scoped to that exchange

Raw cassette navigation should be explicit:

- `Open raw entry cassette`
- `Open raw model cassette`
- `Replay this cassette`

Do not hide child model cassettes behind the entry cassette. A multi-model Codex request must make every raw upstream call inspectable.

### MCP Tools

The existing `responses_audit_trace` MCP output already has the right shape:

- request audit
- final response
- execution events
- upstream exchanges
- raw cassettes

It should evolve to include exchange kinds and parent links:

- `entry_exchange`: optional object for the client-facing exchange
- `model_exchanges`: list replacing or aliasing the current `upstream_exchanges`
- each exchange includes `exchange_kind`, `parent_exchange_id`, `trace_id`, `cassette_path`, `model`, `endpoint`, `status_code`, `started_at`, `completed_at`
- `raw_cassettes` includes `exchange_kind` and can include both entry and model cassettes

Backward compatibility:

- keep `upstream_exchanges` as an alias for model exchanges for at least one minor release
- keep existing fields stable for MCP clients
- add fields rather than renaming response keys in the first implementation phase

Recommended MCP query additions:

- `exchange_kind` filter for trace/cassette search
- `include_entry_cassette` and `include_model_cassettes` booleans for large traces
- `cassette_body_limit` remains bounded and defaults to a safe preview size

### Audit Query

`internal/responses/audit.QueryService` should remain the source for entry-centric request traces.

The query service should return:

- request audit row
- final response derived from audit/events/model exchanges
- execution events
- entry exchange, if recorded
- model exchanges, ordered by start time
- raw cassette summaries for selected exchanges
- diagnostics: `model_exchange_count`, `entry_exchange_present`, `missing_model_cassette_count`, `has_stream_model_exchange`, `has_compact_event`

The storage query should not rescan the cassette directory to discover relationships. Relationships must come from indexed fields, with cassette reads limited to requested raw previews.

## Data Migration and Compatibility

### Old Cassettes Without `exchange_kind`

Old cassette files must not be rewritten during normal startup. The system should infer kind at read/query time:

- V2 cassettes: no format rewrite; infer `legacy` or `model`
- V3 cassettes without `exchange_kind`: infer from indexed metadata and path
- existing trace rows: treat missing kind as `model` for proxy records unless linked to a request audit entry record

The default query label should be:

- `model` when the record is a normal provider/upstream exchange
- `entry` only when indexed audit/runtime data proves it is client-facing
- `legacy` when inference is ambiguous

### Backfill

Backfill should be optional, idempotent, and index-only:

- add nullable storage columns first
- on startup, tolerate null values and use inference in query code
- provide a maintenance command or migration job that fills `exchange_kind`, `parent_exchange_id`, `request_audit_id`, and `response_id` where they can be inferred
- do not modify raw `.http` files in the backfill
- report ambiguous rows instead of guessing destructively

Suggested backfill sources:

- `request_audit_id` and `response_id` already in cassette metadata
- `upstream_exchanges.trace_id` and `upstream_exchanges.cassette_path`
- local Responses execution events
- request path and endpoint
- selected upstream metadata

Backfill should produce counts:

- rows scanned
- rows updated as `entry`
- rows updated as `model`
- rows left `legacy` or unknown
- rows with missing cassette files
- rows with conflicting metadata

### Schema Changes

The storage layer should add fields additively:

- `exchange_kind`
- `parent_exchange_id`
- `entry_exchange_id` or equivalent relation key for model exchanges
- `request_audit_id`
- `response_id`
- `cassette_path`
- optional `exchange_sequence` for deterministic child ordering

Existing startup initialization must work on local SQLite databases. Postgres migrations should be versioned, while SQLite can continue using the current startup schema fallback until the project migrates SQLite to versioned migrations.

## Test Matrix

Minimum coverage for implementation:

| Scenario | Record shape | Query expectation | Replay expectation | Observation expectation |
| --- | --- | --- | --- | --- |
| ordinary proxy request | one model cassette, no entry | trace list shows `model` or inferred `model` | single cassette replay unchanged | existing parser/detectors unchanged |
| Responses non-stream | one entry exchange plus one model exchange | request trace shows 1 model call and both raw links when entry cassette exists | entry cassette replays final client response; model cassette replays upstream response | entry parser captures final response; model parser captures model content |
| Responses stream | one entry exchange plus one streaming model exchange | stream flag visible on model child; raw preview bounded | replay returns stored streaming response bytes from selected cassette | stream parser behavior preserved |
| tool loop | one entry exchange plus multiple model exchanges and tool events | timeline interleaves model calls and tool calls; model call count > 1 | each model cassette replays independently | findings attach to specific model/tool nodes where possible |
| compact | entry operation `compact`, optional child model exchange | audit query filters by operation compact; compact provenance visible | cassette replay unchanged | entry observation records compact operation |
| multi model call | one entry exchange with N model children | Monitor/MCP show all child calls ordered by sequence/start time | no multi-cassette orchestration in `pkg/replay` | per-child observations remain separate; entry rollup references children |
| legacy V2 cassette | no `exchange_kind`, old fixed header | query labels inferred `legacy` or `model` | replay unchanged | current parser fallback |
| missing child cassette file | DB exchange row without readable file | query returns exchange plus raw cassette read error | replay fails only if that missing file is selected | observation job records parse failure for that trace |

Required test levels:

- unit tests for exchange kind inference
- `pkg/replay` regression tests proving no DB dependency
- recordfile tests for optional `exchange_kind` metadata
- query service tests for entry plus multiple model exchanges
- Monitor API/MCP tests for backward-compatible response fields
- observeworker tests for parser selection by explicit and inferred kind
- migration/backfill tests for null, inferred, conflicting, and missing file cases

## Phased Acceptance Checklist

### Phase 1: Metadata and Index Foundations

- [ ] `recordfile.MetaData` can carry optional `exchange_kind` without breaking old V2/V3 reads.
- [ ] Storage schema accepts nullable exchange relationship fields.
- [ ] Recorders write `exchange_kind=model` for normal upstream cassettes.
- [ ] Local Responses entry path can create or index an `entry` exchange when entry recording is enabled.
- [ ] Old cassettes with missing kind continue to parse and replay.

### Phase 2: Query and Audit Shape

- [ ] Query service returns entry exchange and child model exchanges for one request audit.
- [ ] Existing `upstream_exchanges` response remains populated for compatibility.
- [ ] Raw cassette summaries include exchange kind and read errors.
- [ ] Multi-model-call traces are ordered deterministically.
- [ ] Audit query does not scan the cassette directory for relationships.

### Phase 3: Monitor and MCP UX

- [ ] Monitor has an entry-centric request detail that shows one Codex request and all child LLM calls.
- [ ] Trace list can filter by `exchange_kind`.
- [ ] Raw entry and model cassette links are separately visible.
- [ ] MCP `responses_audit_trace` exposes additive exchange fields while preserving existing keys.
- [ ] Large raw previews remain bounded and redacted.

### Phase 4: Observation and Analyzer

- [ ] observeworker selects parser using explicit kind, indexed kind, then inference.
- [ ] Entry observations capture request/final-response semantics without duplicating model parser output.
- [ ] Model observations preserve existing parser behavior.
- [ ] Analyzer findings are scoped by exchange kind and avoid obvious duplicates across parent/child observations.
- [ ] Parse failures remain per cassette/trace and do not block sibling model exchanges.

### Phase 5: Migration and Backfill

- [ ] Startup tolerates null relationship fields on existing DBs.
- [ ] Optional backfill reports counts and conflicts without rewriting cassette files.
- [ ] Backfill is idempotent.
- [ ] Ambiguous rows remain `legacy` or unknown rather than being guessed as entry.
- [ ] Existing local replay fixtures and old `.http` samples still pass.

### Phase 6: End-to-End Verification

- [ ] ordinary proxy recording and replay
- [ ] Responses non-stream with one model call
- [ ] Responses stream with one model call
- [ ] tool loop with multiple model calls
- [ ] compact operation
- [ ] multi model call with raw cassette navigation for every child
- [ ] MCP audit trace output consumed by an older client that only reads `upstream_exchanges`

## Core Decision

The exchange split should be implemented as an additive indexing and observation model. It should not turn replay into a database-backed workflow and should not rewrite old cassettes. Entry exchanges explain the client-facing request; model exchanges preserve the existing provider-call cassette contract. Monitor, MCP, and audit query should present the parent-child relationship, while `pkg/replay` continues to replay exactly one selected `.http` file.
