# AI Branch Baseline

## Purpose

This document summarizes the current `feat/ai-better` branch state for humans and AI agents.

Use it when you need a fast answer to these questions:

- what agent-facing work already landed on this branch
- what closed loop is already implemented
- what constraints still bound the current design
- what the next reasonable follow-up work should build on

This document is branch-specific.
It complements, but does not replace:

- [Project Baseline](./PROJECT_BASELINE.md)
- [MCP Guide](./MCP_GUIDE.md)
- [Maintainer Baseline](./MAINTAINER_BASELINE.md)
- [Agent Evolution Roadmap](./AGENT_EVOLUTION_ROADMAP.md)

## Branch Goal

The goal of this branch is to make `llm-tracelab` directly usable by AI agents for local inspection, replay, evaluation, and regression analysis.

The intended loop on this branch is:

`trace/session query -> failure query -> failure clustering -> trace drilldown`

This is a bounded local inspection loop.
It is not a general autonomous code-fixing system.

## Completed Work

The following branch slices are implemented and already committed:

1. `f4d47c7` `docs: add agent evolution roadmap`
2. `6649564` `feat: add read-only mcp server`
3. `e7c8fcc` `feat: add replay mcp tools`
4. `db5bd8f` `feat: add dataset curation tools`
5. `f1a352f` `feat: add baseline eval runs and scores`
6. `c7c7a39` `feat: add eval run comparison tool`
7. `0b7414b` `feat: add persisted experiment runs`
8. `0c2a970` `feat: add deterministic budget evaluators`
9. `fb5a6b6` `feat: add versioned evaluator profiles`
10. `3670dca` `feat: add tool call conformance evaluator`
11. `eba8c6a` `feat: validate tool call arguments json`
12. `b8994c0` `feat: summarize experiment regressions`
13. `57e1ad6` `feat: cluster trace failures for agents`
14. `4de0b99` `feat: explain experiment regressions`
15. `6ac856a` `feat: create datasets from experiment regressions`
16. `e5b3661` `docs: align mcp capability baseline`

## Current Implemented Loop

An AI agent can now do all of the following locally through MCP:

1. inspect traces, sessions, upstreams, and failures
2. query failed traces with the same filters as `list_traces`
3. summarize clustered failures before drilling into trace detail

This is enough to support:

- local regression triage
- agent-guided debugging loops that stay inside the local project boundary

## Current MCP Surface

Implemented MCP tools on this branch:

- `list_traces`
- `get_trace`
- `list_sessions`
- `list_upstreams`
- `query_failures`
- `summarize_failure_clusters`

Implementation constraints:

- transport is streamable HTTP on the management server
- implementation uses the official `github.com/modelcontextprotocol/go-sdk`
- MCP remains a thin control plane over existing monitor/store behavior
- MCP is not a new storage source of truth

## Storage And Design Invariants

This branch does not change the core storage split:

- raw `.http` cassettes remain the source of truth for replay and trace detail
- SQLite remains the source of truth for list pages, filters, aggregate stats, datasets, eval runs, scores, and experiment linkage

This branch also preserves these constraints:

- replay compatibility is non-negotiable
- no network is required for replay-based tests
- schema evolution must stay additive
- no destructive cassette migration is introduced as part of the agent loop
- persisted experiment runs store linkage and aggregate summary, not duplicated detailed score payloads

## What This Branch Does Not Yet Do

This branch is intentionally not:

- an autonomous code editing system
- an always-on hosted observability plane
- a live-model judge loop by default
- an OTel-first storage architecture
- an automatic prompt optimizer

If future work needs any of the above, it should build on the current local inspection loop rather than bypass it.

## Recommended Next Work

The next highest-value follow-up should stay on top of the existing loop rather than expanding sideways.

Good next steps:

1. add explicit acceptance-gate tooling for candidate promotion based on stored metrics
2. improve regression diagnosis quality without creating a second analysis store
3. add bounded experiment/export helpers that keep replay and SQLite as the primary local truth

Less valuable next steps right now:

- broad new mutation APIs with unclear guardrails
- autonomous code changes without explicit acceptance criteria
- replacing local storage semantics with a new telemetry backbone

## Verification Baseline

At the current branch baseline, the expected validation flow is:

- `task fmt`
- `task lint`
- `task test`
- `task check`

Before treating new work as complete, keep these expectations:

- docs and code are updated together
- new MCP tools have regression coverage
- changes preserve replay behavior
- branch slices land as explicit commits
