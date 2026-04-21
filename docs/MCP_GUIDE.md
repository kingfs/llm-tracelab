# MCP Guide

## Purpose

This document describes the current `llm-tracelab` MCP server surface.

The current goal is narrow and deliberate:

- expose local trace/session/upstream inspection to AI agents
- reuse existing monitor/store behavior
- keep the first MCP surface read-only

This is the `M1` slice from [Agent Evolution Roadmap](./AGENT_EVOLUTION_ROADMAP.md).

## Current Status

Current MCP support is:

- transport: `stdio`
- implementation library: official `github.com/modelcontextprotocol/go-sdk`
- scope: read-only inspection and replay tools

Current MCP support is not:

- a hosted control plane
- a write-capable mutation API
- a replacement for replay or monitor storage

## Run

Start the MCP server with the same config file used by the main proxy:

```bash
go run ./cmd/server mcp -c config/config.yaml
```

The server reads the local `output_dir` and exposes tools over standard input/output.

## Tool Surface

### `list_traces`

List recorded traces with pagination and optional filters:

- `page`
- `page_size`
- `provider`
- `model`
- `q`

### `get_trace`

Get one trace detail by `trace_id`.

Optional input:

- `include_raw`: when true, also return raw HTTP request/response bytes

### `list_sessions`

List grouped sessions with pagination and optional filters:

- `page`
- `page_size`
- `provider`
- `model`
- `q`

### `get_session`

Get one grouped session by `session_id`.

### `list_upstreams`

List upstream analytics.

Optional filters:

- `window`: `1h`, `24h`, `7d`, `all`
- `model`

### `get_upstream`

Get one upstream drilldown by `upstream_id`.

Optional filters:

- `window`
- `model`

### `query_failures`

Return failed traces from a paginated trace scan.

Inputs match `list_traces`, but the result is filtered to requests with:

- non-2xx `status_code`, or
- non-empty `error`

Important limitation:

- this tool currently filters one paginated `list_traces` result
- it is not yet a dedicated failure index

### `replay_trace`

Replay one recorded trace locally and return a bounded HTTP response summary.

Inputs:

- `trace_id`
- `body_limit`

### `replay_session`

Replay multiple traces from one session locally and return bounded HTTP response summaries.

Inputs:

- `session_id`
- `limit`
- `body_limit`

### `create_dataset_from_traces`

Create a local dataset from explicit trace IDs.

Inputs:

- `name`
- `description`
- `trace_ids`
- `note`

### `create_dataset_from_session`

Create a local dataset from traces in one session.

Inputs:

- `name`
- `description`
- `session_id`
- `limit`
- `note`

### `append_dataset_examples`

Append more trace IDs to an existing dataset.

Inputs:

- `dataset_id`
- `trace_ids`
- `note`

### `list_datasets`

List curated local datasets.

### `get_dataset`

Return one dataset and its ordered examples.

### `run_eval_on_dataset`

Run the deterministic baseline evaluator set on one dataset.

Input:

- `dataset_id`
- `evaluator_set`

### `run_eval_on_traces`

Run the deterministic baseline evaluator set on explicit trace IDs.

Input:

- `trace_ids`
- `evaluator_set`

Notes:

- `evaluator_set` defaults to `baseline_v3`
- built-in profiles are versioned so historical eval runs remain interpretable

### `list_evaluator_profiles`

List built-in evaluator profiles and their thresholds.

### `list_eval_runs`

List recent evaluation runs.

Input:

- `limit`

### `get_eval_run`

Return one evaluation run and its recorded scores.

Input:

- `eval_run_id`

### `list_scores`

List recorded scores with optional filters.

Inputs:

- `trace_id`
- `session_id`
- `dataset_id`
- `eval_run_id`
- `experiment_run_id`
- `limit`

### `compare_eval_runs`

Compare two recorded eval runs and return aggregate pass-rate deltas plus per-trace improvements and regressions.

Inputs:

- `baseline_eval_run_id`
- `candidate_eval_run_id`

Notes:

- comparison is derived from already-recorded `scores`
- matching is keyed by `trace_id + evaluator_key`

### `create_experiment_from_eval_runs`

Persist one experiment run that links a baseline eval run and a candidate eval run.

Inputs:

- `name`
- `description`
- `baseline_eval_run_id`
- `candidate_eval_run_id`

### `list_experiment_runs`

List recent persisted experiment runs.

Input:

- `limit`

### `get_experiment_run`

Return one persisted experiment run plus its derived comparison detail.

Input:

- `experiment_run_id`

Notes:

- the persisted experiment stores only linkage and aggregate summary fields
- detailed evaluator/improvement/regression views are derived from existing scores at read time

## Design Notes

The MCP server intentionally reuses existing monitor JSON APIs in-process rather than adding a parallel query stack.

This keeps the first MCP slice:

- thin
- replay-safe
- low-risk
- aligned with current monitor semantics

## Next Likely Step

The next MCP-focused step should be:

1. keep comparison local and deterministic
2. keep experiment persistence lightweight and additive
3. add richer evaluators only after current score signals prove actionable

## Current Evaluator Set

Current deterministic evaluator set:

- `http_status_2xx`
- `no_recorded_error`
- `response_has_body`
- `ttft_le_2000ms`
- `total_tokens_le_32000`
- `tool_calls_declared`

This set is intentionally objective and cheap.

Default baseline evaluator version is `baseline_v3`.

Current built-in profiles:

- `baseline_v1`: status/error/body checks only
- `baseline_v2`: `baseline_v1` plus TTFT and total-token budgets
- `baseline_v3`: `baseline_v2` plus declared tool-call conformance

The latency and token thresholds are currently hard-coded so results stay deterministic and easy to compare across runs.

`tool_calls_declared` checks that every recorded response tool call matches a tool name declared in the request. If no tool call occurred, the check passes.

It is not intended to replace human judgment or model-graded quality review.
