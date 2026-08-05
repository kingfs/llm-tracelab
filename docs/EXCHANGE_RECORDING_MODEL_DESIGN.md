# Exchange Recording Model Design

Date: 2026-06-24

Status: design draft for implementation planning.

## Problem

`llm-tracelab` currently records each upstream HTTP request/response as a human-readable `.http` cassette and indexes trace metadata in `logs`. Responses server-mode also writes `request_audits`, `execution_events`, `upstream_exchanges`, and `tool_call_audits`.

That is enough to find the raw cassette for a direct proxy request or for the first internal Chat Completions call made by Responses server-mode, but it does not yet give a stable taxonomy for these different things:

- the entry HTTP exchange from an SDK/client into llm-tracelab
- the model exchange from llm-tracelab to an upstream provider
- follow-up model exchanges after hosted/function tool execution
- compact/summary/repair model exchanges that are derived from prior response state
- non-LLM tool calls and derived records that should be linked to the same audit trail without pretending to be upstream LLM HTTP cassettes

This document defines a minimal taxonomy and relationship model that can be added without breaking existing V2/V3 cassette readers or current replay behavior.

## Terms

- **Entry exchange**: the inbound client HTTP exchange accepted by llm-tracelab, such as `POST /v1/responses` or a direct proxied `POST /v1/chat/completions`.
- **Model exchange**: an HTTP exchange from llm-tracelab to an upstream model provider. These are the exchanges replay depends on today.
- **Tool exchange**: an interaction with a hosted/local tool executor that is not itself an upstream LLM HTTP cassette, such as hosted `web_search`, function executor, MCP hosted tool, or local command executor.
- **Derived exchange**: a synthetic lifecycle record created from existing state rather than an external HTTP request/response, such as compaction provenance, summaries, repair attempts, parser reanalysis, or observation artifacts.
- **Cassette**: a `.http` file containing raw HTTP bytes plus a V3 prelude, or a legacy V2 file.

## Minimal Taxonomy

Use two independent fields:

- `exchange_kind`: what physical or logical thing was recorded.
- `exchange_role`: why it happened in the product workflow.

Do not overload endpoint, operation, or event type as the taxonomy. They are useful hints, but they are not stable enough to distinguish a primary model call from a compact call or a tool follow-up.

### `exchange_kind`

Recommended minimal enum:

| Value | Meaning | Cassette expected |
| --- | --- | --- |
| `entry` | inbound client HTTP exchange into llm-tracelab | optional for Responses server-mode until entry recording exists; yes for ordinary proxy entry recordings |
| `model` | outbound LLM provider HTTP exchange | yes |
| `tool` | hosted/local tool execution | no, unless the tool itself uses an HTTP cassette in a future feature |
| `derived` | synthetic or derived lifecycle artifact | no |

`model` is intentionally not named `upstream`: some future tool HTTP calls may also be upstream, but only model calls should participate in LLM replay semantics.

### `exchange_role`

Recommended minimal enum:

| Value | Applies to | Meaning |
| --- | --- | --- |
| `client_request` | `entry` | original client request/response handled by llm-tracelab |
| `primary_model_call` | `model` | first model call used to answer a Responses create request or direct model request |
| `tool_followup_model_call` | `model` | model call after one or more tool outputs have been injected |
| `compact_model_call` | `model` | model call whose output is a compacted summary or context reduction |
| `summary_model_call` | `model` | explicit summary generation not tied to token-budget compaction |
| `repair_model_call` | `model` | retry/repair/model self-correction after invalid output, parser failure, or policy-specific recovery |
| `tool_call` | `tool` | tool execution lifecycle, including hosted web search and function executors |
| `derived_summary` | `derived` | persisted summary/provenance not itself the upstream model call |
| `derived_repair` | `derived` | repair/normalization/reanalysis artifact not itself the upstream model call |

Short-term implementation can ship only these values:

- `client_request`
- `primary_model_call`
- `tool_followup_model_call`
- `compact_model_call`
- `tool_call`
- `derived_summary`

`summary_model_call`, `repair_model_call`, and `derived_repair` should be reserved now so future code does not invent incompatible names.

### Responses Server Classification

For one `POST /v1/responses` request with internal multiple LLM calls:

1. The inbound Responses HTTP request is `entry/client_request` and owns the `request_audit_id`.
2. The first internal Chat Completions call is `model/primary_model_call`.
3. If the model returns tool calls, each hosted/function/MCP execution is `tool/tool_call`.
4. The model call after injecting tool output is `model/tool_followup_model_call`.
5. An auto or explicit compact call is `model/compact_model_call`.
6. The persisted compact response/provenance record is `derived/derived_summary`.
7. If a later model call continues after compact and no tool output caused it, classify by purpose:
   - compact generation itself: `compact_model_call`
   - final answer after compact because the request still needs an answer: `primary_model_call` when it is the first answering model call for this entry; otherwise `tool_followup_model_call` if tool results were injected before it.

For direct non-Responses proxy traffic, the single recorded cassette can be treated as both the client-visible entry and the model HTTP exchange at the product level. To avoid double rows in phase one, index it as `model/primary_model_call` and leave `entry/client_request` for Responses server-mode and future explicit entry recording.

## Relationship Fields

Add relationship fields in a way that can represent both a flat direct proxy exchange and a Responses tree.

| Field | Required in new records | Meaning |
| --- | --- | --- |
| `exchange_id` | yes for new DB rows/events; optional in cassette meta | stable id for this exchange record, e.g. `exch_...` |
| `exchange_kind` | yes for new DB rows/events; optional in cassette meta | `entry`, `model`, `tool`, or `derived` |
| `exchange_role` | yes for new DB rows/events; optional in cassette meta | purpose enum above |
| `request_audit_id` | required for Responses server-mode descendants | root inbound audit id |
| `response_id` | optional until the semantic response exists; backfilled where possible | semantic Responses id associated with the exchange |
| `parent_exchange_id` | optional | direct parent in the exchange tree |
| `sequence_index` | required among siblings when known | zero-based execution order under the same parent/request audit |
| `trace_id` | required for cassette-backed model exchanges | current cassette trace id, usually V3 `meta.request_id` |
| `cassette_path` | required for cassette-backed exchanges | path to the `.http` cassette |
| `client_request_id` | optional | caller-provided id propagated from entry audit/header |
| `conversation_id` | optional | Responses/Codex conversation/thread correlation when available |

Recommended graph:

```text
request_audit_id=reqaudit_1
entry/client_request exchange_id=exch_entry_1
  model/primary_model_call sequence_index=0 cassette=a.http
  tool/tool_call sequence_index=1 call_id=call_search
  model/tool_followup_model_call sequence_index=2 cassette=b.http
  model/compact_model_call sequence_index=3 cassette=c.http
  derived/derived_summary sequence_index=4 response_id=resp_compact
```

`sequence_index` should be monotonic for records under the same `request_audit_id`, even when `parent_exchange_id` is not known. If exact nesting is unavailable in phase one, leave `parent_exchange_id` empty but still write `sequence_index`.

## V3 Cassette Compatibility

All cassette changes must be additive and optional.

Recommended V3 `RecordHeader.MetaData` optional fields:

```json
{
  "exchange_id": "exch_...",
  "exchange_kind": "model",
  "exchange_role": "primary_model_call",
  "parent_exchange_id": "exch_entry_...",
  "sequence_index": 0,
  "trace_id": "trace_..."
}
```

Notes:

- `trace_id` is optional because existing code treats `meta.request_id` as the trace id. New writers may duplicate it as `trace_id` for clarity, but readers must continue using `request_id` as fallback.
- `request_audit_id` and `response_id` already exist in V3 meta and should keep their current names.
- V3 readers use Go `encoding/json`, so unknown future fields are ignored by older binaries.
- V2 readers remain unchanged. V2 has no exchange taxonomy; imported V2 files should default to `model/primary_model_call` only in DB indexing, not by rewriting the cassette.
- `# event:` lines may include the same fields in `attributes` for event-level correlation, but cassette readers must not require them.
- Replay must ignore the taxonomy. Replay selects raw HTTP request/response bytes and should not fail when taxonomy fields are missing or unknown.

## Database Storage

### Current Tables

- `logs`: trace index for `.http` cassettes. It already contains `trace_id`, path, model/provider/endpoint, status, timing, routing, and session-like fields.
- `request_audits`: inbound Responses request envelope and lifecycle status.
- `upstream_exchanges`: current Responses server-mode model-call correlation to cassette path/trace id/route target.
- `execution_events`: lifecycle events with `details_json`, currently used for model call, compact, stream, and fallback events.
- `tool_call_audits`: structured hosted/function tool lifecycle.

### Short-Term Recommendation

Reuse existing tables. Do not add `entry_exchanges` in the first implementation.

Add nullable columns to `upstream_exchanges`:

- `exchange_id`
- `exchange_kind`
- `exchange_role`
- `parent_exchange_id`
- `sequence_index`

For phase one, every row in `upstream_exchanges` should have `exchange_kind='model'`. This keeps the table name historically imperfect but operationally compatible.

Also add the same correlation keys to `execution_events.details_json` immediately, even before generated Ent fields exist:

- `exchange_id`
- `exchange_kind`
- `exchange_role`
- `parent_exchange_id`
- `sequence_index`
- `cassette_path`
- `trace_id`

Use `request_audits` as the root for `entry/client_request`. The entry exchange does not need a new table in phase one; derive it from `request_audits` and expose it in query/read models as a synthetic root node:

- `exchange_id`: `entry:` + `request_audit_id`, or a stored `entry_exchange_id` later
- `exchange_kind`: `entry`
- `exchange_role`: `client_request`
- `sequence_index`: `-1` or omitted in persisted DB; present as `0` only in rendered trees if children are renumbered

Use `tool_call_audits` for `tool/tool_call`, adding exchange fields later only if query/read-model pressure justifies it. In phase one, tool calls can be linked through `request_audit_id`, `response_id`, `call_id`, `created_at`, and execution event details.

### Long-Term Recommendation

Add a generic `exchanges` table once the read model needs first-class graph queries across entry/model/tool/derived records:

- `id`
- `kind`
- `role`
- `request_audit_id`
- `response_id`
- `conversation_id`
- `parent_exchange_id`
- `sequence_index`
- `trace_id`
- `cassette_path`
- `source_table`
- `source_id`
- `started_at`
- `completed_at`
- `status`
- `metadata_json`

Then either:

- keep `upstream_exchanges` as the model-specific detail table referenced by `exchanges.source_id`, or
- migrate it into `model_exchanges` if a future breaking schema cleanup is explicitly planned.

Avoid creating both `entry_exchanges` and `exchange_links` as a first step. A single `exchanges.parent_exchange_id` adjacency model is simpler and enough for current tree queries. Add `exchange_links` only if the product needs DAG relationships, such as one compact summary derived from many historical responses across different request audits.

## Writer Behavior

For Responses server-mode:

1. Create `request_audit_id` at request acceptance as today.
2. Treat the inbound request as the synthetic root `entry/client_request`.
3. Maintain a per-request `sequence_index` counter in context.
4. Before each internal Chat Completions call, classify role from runtime intent:
   - default first answering call: `primary_model_call`
   - after tool output injection: `tool_followup_model_call`
   - compact path: `compact_model_call`
5. Pass exchange metadata into recorder prepare options so V3 meta receives optional fields.
6. Write `upstream_exchanges` with the same exchange metadata and cassette path.
7. Write started/completed/cancelled/failed `execution_events` with the same exchange metadata in `details_json`.
8. For tool calls, write `tool_call_audits` and execution events with `exchange_kind=tool`, `exchange_role=tool_call`, and a sequence index.
9. For compact provenance/summary artifacts, write execution events with `exchange_kind=derived`, `exchange_role=derived_summary`.

The runtime, not the recorder, should decide `exchange_role`. The recorder only persists supplied metadata.

## Reader And Query Behavior

Readers should apply these fallbacks:

- Missing `exchange_kind` on a cassette-backed row: `model`.
- Missing `exchange_role` on a cassette-backed row: `primary_model_call`.
- Missing `trace_id`: use V3 `meta.request_id` or `logs.trace_id`.
- Missing `exchange_id`: use stable derived id such as `trace:` + `trace_id` for read models only.
- Missing `parent_exchange_id`: attach Responses server-mode model/tool/derived records to synthetic `entry/client_request` root by `request_audit_id`.
- Missing `sequence_index`: order by `started_at`, then `created_at`/`occurred_at`, then id.

Query APIs should return both the raw table ids and normalized exchange fields so agents can reason without knowing which table produced a record.

## Minimal Phase-One Implementation Scope

Phase one should be limited to:

- Add optional V3 meta fields for `exchange_id`, `exchange_kind`, `exchange_role`, `parent_exchange_id`, `sequence_index`, and optional `trace_id` alias.
- Add nullable `exchange_*`, `parent_exchange_id`, and `sequence_index` columns to `upstream_exchanges` for Postgres production migrations and SQLite fallback schema.
- Populate `upstream_exchanges` and model-call `execution_events.details_json` for Responses server-mode internal Chat Completions calls.
- Classify compact calls as `model/compact_model_call`.
- Classify follow-up calls after tool output injection as `model/tool_followup_model_call`.
- Keep direct proxy cassettes and legacy V2 imports working with fallback `model/primary_model_call`.
- Update audit query/read models to render a synthetic `entry/client_request` root per `request_audit_id` and attach model calls by `sequence_index`.
- Add tests that assert missing taxonomy fields in V2/V3 cassettes do not break replay or summary parsing.

## Non-Goals

- Do not change raw `.http` cassette payload layout.
- Do not require replay to understand exchange taxonomy.
- Do not rewrite existing V2 or V3 cassettes just to add taxonomy.
- Do not implement protocol translation between provider families.
- Do not introduce a full generic `exchanges` table in phase one.
- Do not record raw tool inputs/outputs beyond existing redacted audit policy.
- Do not solve cross-request/thread/session range queries beyond existing `request_audit_id`, `response_id`, `conversation_id`, and `client_request_id` correlation.
- Do not make Monitor aggregate stats depend on rescanning `.http` files.

## Core Decision

The smallest durable model is:

- `exchange_kind` answers "what was recorded": `entry`, `model`, `tool`, `derived`.
- `exchange_role` answers "why it happened": `client_request`, `primary_model_call`, `tool_followup_model_call`, `compact_model_call`, `summary_model_call`, `repair_model_call`, `tool_call`, `derived_summary`, `derived_repair`.
- `request_audit_id` is the Responses root correlation id.
- `parent_exchange_id` and `sequence_index` form the execution tree/order.
- `trace_id` plus `cassette_path` link cassette-backed model exchanges to raw replay material.
- Phase one reuses `request_audits`, `upstream_exchanges`, `execution_events`, `tool_call_audits`, and `logs`; a generic `exchanges` graph table is a later read-model optimization.
