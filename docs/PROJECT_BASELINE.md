# Project Baseline

## Purpose

This document records the current functional baseline of `llm-tracelab` after the session aggregation, multi-upstream routing, and monitor UX work landed.

It is intended for two audiences:

- humans who need a quick project capability overview before making changes
- AI agents that need a stable summary of what already exists, what is considered complete, and what kinds of follow-up work are still reasonable

This is not a roadmap. It describes the current implemented state.

Related documents:

- [Monitor Guide](./MONITOR_GUIDE.md) for user-facing monitor workflows
- [MCP Guide](./MCP_GUIDE.md) for agent-facing MCP workflows
- [Maintainer Baseline](./MAINTAINER_BASELINE.md) for implementation constraints and upgrade expectations

## Product Scope

`llm-tracelab` is a local-first LLM HTTP record/replay proxy.

Its current baseline workflow is:

1. route SDK or application traffic through the proxy
2. persist the raw HTTP exchange as a human-inspectable `.http` cassette
3. index request metadata into SQLite for fast monitor queries
4. replay the cassette in tests without upstream network access
5. inspect traces in the monitor from request-centric, session-centric, and upstream-centric perspectives
6. expose read-only trace/session/upstream inspection to AI agents through MCP over `stdio`

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
- `selected_upstream_id`
- `selected_upstream_base_url`
- `selected_upstream_provider_preset`
- `routing_policy`
- `routing_score`
- `routing_candidate_count`

Additional additive SQLite tables now persist multi-upstream runtime state:

- `upstream_targets`
- `upstream_models`

These tables provide warm-start model catalog state and monitor-facing upstream analytics without coupling replay to live providers.

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

## Multi-Upstream Routing Baseline

Multi-upstream routing is now an implemented feature, not just a plan.

### Configuration And Compatibility

The current baseline supports both:

- legacy single `upstream`
- additive `upstreams` plus `router`

The compatibility path keeps older configs working while allowing one proxy instance to serve multiple providers for the same model family.

### Routing Runtime

The current routing baseline includes:

- request-scoped upstream target selection
- persisted model catalog snapshots per upstream
- health-aware target state
- cost-aware selection derived from the `llmrouterv2` direction
- additive per-trace routing metadata recorded into cassettes and SQLite

### Upstream Monitor APIs

Current upstream monitor API surface includes:

- `GET /api/upstreams`
- `GET /api/upstreams/:upstreamID`

These APIs expose:

- target health summary
- model coverage
- filtered routing analytics by window and model
- recent failures
- per-upstream drilldown with recent traces and breakdowns

## Monitor Baseline

The embedded monitor UI is a React frontend served from Go embed assets.

### Monitor Perspectives

The monitor home page now has stable perspectives for:

- requests
- sessions
- upstream routing analytics

### Request Detail Tabs

Trace detail now supports these stable tabs:

- `Timeline`
- `Summary`
- `Raw Protocol`
- `Declared Tools` when applicable

### Request Routing Context

Trace detail now also exposes the selected upstream and routing decision context when the trace was recorded through the multi-upstream router.

Current routing context includes:

- selected upstream id
- selected upstream provider preset
- selected upstream base URL
- routing policy
- routing score
- routing candidate count
- selected upstream health context when the router is available
- current router health thresholds used to interpret degraded/open state

The trace detail page links directly from an individual request to the upstream drilldown page.

### Cross-View Navigation

The current baseline includes deep links from session pages into trace detail.

## MCP Baseline

Read-only MCP support is now an implemented feature.

Current MCP transport baseline:

- `stdio`

Current implementation baseline:

- official `github.com/modelcontextprotocol/go-sdk`

Current MCP tool surface includes:

- `list_traces`
- `get_trace`
- `list_sessions`
- `get_session`
- `list_upstreams`
- `get_upstream`
- `query_failures`
- `replay_trace`
- `replay_session`
- `create_dataset_from_traces`
- `create_dataset_from_session`
- `append_dataset_examples`
- `list_datasets`
- `get_dataset`
- `run_eval_on_dataset`
- `run_eval_on_traces`
- `list_evaluator_profiles`
- `list_eval_runs`
- `get_eval_run`
- `list_scores`
- `compare_eval_runs`
- `create_experiment_from_eval_runs`
- `list_experiment_runs`
- `get_experiment_run`

Important constraint:

- the MCP server currently reuses the existing monitor/store query behavior
- it does not introduce a second source of truth or a separate query engine
- eval comparison is still derived from existing `eval_runs` and `scores`
- persisted `experiment_runs` store linkage and aggregate summary only
- built-in evaluator profiles are versioned and selectable at eval time, with `baseline_v4` as the current default

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
- inspect active upstream targets on the home page
- filter upstream analytics by time window and model
- drill from an upstream summary card into per-upstream detail
- jump from a session into a specific trace
- jump from a trace into the selected upstream view
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

The implemented multi-upstream routing enhancements must not break:

- replay behavior when routing metadata is absent
- V2 and V3 record parsing
- human readability of raw `.http` cassettes

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
- multi-upstream config compatibility
- upstream target/model catalog persistence
- request-scoped upstream routing
- upstream health and refresh tracking
- upstream analytics list and drilldown pages
- per-trace routing metadata recording
- per-trace selected-upstream health context
- trace-to-upstream navigation
- session detail overview and filtering
- trace/session cross-view navigation
- tab-level deep links
- area-level focus in trace detail
- timeline error-node focus
- legacy SQLite schema upgrade for newly added session columns

This means the first multi-upstream delivery target is now considered complete at the product baseline level:

- one proxy config can keep multiple upstreams online at once
- provider/model availability is remembered locally
- request routing is request-scoped rather than config-scoped
- failures and routing decisions are explainable from monitor views without changing replay semantics

## Reasonable Follow-Up Work

The current baseline does not block release, but these are still reasonable future refinements:

- more stable node-level identifiers inside timeline trees
- linking directly to specific tool call / tool response nodes
- richer focus/highlight lifecycle behavior
- additional documentation for operator workflows and troubleshooting

These are refinements on top of an already functional session-aware monitor baseline, not missing core architecture.
