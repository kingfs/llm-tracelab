# Agent Evolution Roadmap

## Purpose

This document defines a practical roadmap for evolving `llm-tracelab` from a local-first record/replay proxy into an agent-facing trace control plane.

The key requirement is not "more observability" in isolation.
The key requirement is a closed loop that an AI agent can actually use:

1. inspect traces and sessions
2. identify likely failures or regressions
3. replay or re-evaluate the relevant traces
4. compare candidate changes
5. feed the result back into code, prompts, or routing decisions

This roadmap is intentionally feasibility-first.
Every milestone below is constrained by the current architecture, current test guarantees, and the requirement to preserve replay compatibility.

Related documents:

- [Project Baseline](./PROJECT_BASELINE.md)
- [Maintainer Baseline](./MAINTAINER_BASELINE.md)
- [Monitor Guide](./MONITOR_GUIDE.md)
- [Roadmap](./ROADMAP.md)

## Strategic Position

`llm-tracelab` should not try to become a hosted observability platform.

Its strongest differentiators are already local and deterministic:

- raw `.http` cassette as source of truth
- replay-safe inspection without upstream access
- SQLite-backed local query surface
- request, session, and upstream perspectives

The most valuable next step is to expose this local truth to agents in a standard way, then add evaluation and experiment loops on top.

The intended long-term loop is:

`cassette -> indexed trace/session -> MCP query/tool use -> score/eval -> replay experiment -> fix -> compare -> commit`

## Hard Constraints

Any milestone in this document must preserve these invariants:

- `pkg/replay` remains a hard requirement
- raw `.http` cassettes remain human-inspectable
- replay must not depend on network access
- SQLite remains the source for monitor list/statistics/filtering
- raw cassettes remain the source of truth for detail/replay
- schema and format evolution must remain additive

## Feasibility Assessment

### What Is Feasible Now

These items are feasible on top of the current codebase without redesigning core storage:

- exposing monitor-backed read APIs through MCP tools
- exposing trace replay and local dataset creation as MCP tools
- storing additive evaluation metadata in SQLite
- generating comparison reports from replayed traces and stored scores
- exporting additive OTel-style views derived from existing trace data

Why this is feasible now:

- monitor APIs already expose machine-readable `traces`, `sessions`, and `upstreams`
- the codebase already has stable session grouping and trace detail reconstruction
- replay is already isolated from live upstream calls
- Go `1.25` is compatible with modern MCP Go SDKs

### What Is Not Yet Feasible As A Primary Goal

These items are attractive, but should not be treated as immediate commitments:

- fully autonomous prompt optimization across the whole system
- fully autonomous code editing based only on production traces
- broad online judge loops that rely on live model calls for every iteration
- treating OTel GenAI export as the new source of truth

Why not:

- there is no dataset/score model in the project yet
- there is no stable experiment runner yet
- agent-generated fixes need bounded write scopes and deterministic acceptance criteria
- OTel GenAI semantic conventions are valuable, but still evolving and should remain a derived view rather than the storage backbone

### Feasibility Gates

Each milestone should be considered blocked unless its gate is green.

`Gate A: Storage safety`

- no replay regressions
- no implicit cassette rewrites during normal startup
- additive schema only

`Gate B: Machine usability`

- outputs are structured enough for agents to consume without HTML scraping
- tool results are bounded in size and page/filter capable

`Gate C: Deterministic verification`

- new capability has replay-safe tests
- evaluation logic has explicit fixtures or deterministic baselines where possible

`Gate D: Operational containment`

- write-capable agent actions are narrow, auditable, and opt-in
- expensive live-model judging is never the default validation path

## MCP Direction

### Library Decision

For MCP support, the default implementation target is the official Go SDK:

- `github.com/modelcontextprotocol/go-sdk`

This is the default because it is the official SDK, maintained in collaboration with Google, and explicitly documents spec-version compatibility across current MCP revisions.

Approved fallback:

- `github.com/mark3labs/mcp-go`

This is an acceptable fallback only if the official SDK blocks a concrete requirement during implementation, such as a transport or interoperability gap that materially affects `llm-tracelab`.

If fallback is used, the change should include:

- a short decision record explaining why the official SDK was insufficient
- a compatibility note in docs
- a bounded abstraction so server tools are not tightly coupled to one SDK

### MCP Scope Principle

The first MCP milestone should expose existing capabilities, not invent new semantics.

That means the first server should be a thin agent-facing layer over:

- monitor list/detail queries
- replay
- failure search
- upstream health and routing analytics

It should not start with:

- broad mutation tools
- autonomous code editing tools
- opaque prompt execution helpers

## Milestones

## M0. Feasibility And Interface Baseline

Goal:
Establish the contract for agent-facing evolution work without changing storage truth or overcommitting to unstable standards.

Scope:

- document the target loop and architecture boundaries
- choose official MCP SDK as the default implementation path
- define the first MCP tool surface
- define the first score/dataset/experiment entities
- define OTel export as additive and derived

Exit criteria:

- this roadmap is published
- MCP default/fallback policy is documented
- the first server/tool inventory is defined
- score/dataset/experiment work is sequenced after MCP read access, not before

Non-goals:

- implementing the MCP server
- adding schema changes
- adding evaluation code

Feasibility notes:

- complete now
- this milestone is documentation and decision framing only

## M1. Read-Only MCP Control Plane

Goal:
Allow an external AI agent to inspect traces, sessions, failures, and upstream routing state without scraping HTML or reading SQLite files directly.

Scope:

- add an MCP server built with `modelcontextprotocol/go-sdk`
- support `stdio` transport first
- expose read-only tools backed by existing Go services
- keep tool outputs concise, structured, and paginated

Initial tool candidates:

- `list_traces`
- `get_trace`
- `list_sessions`
- `get_session`
- `list_upstreams`
- `get_upstream`
- `query_failures`

Exit criteria:

- an MCP client can list and inspect traces using only MCP calls
- tool outputs map to stable JSON structures, not rendered monitor HTML
- at least one integration test covers MCP server startup and tool invocation
- existing monitor and replay tests remain green

Non-goals:

- write-capable tools
- live upstream execution through MCP
- OTel export

Feasibility notes:

- high feasibility
- current monitor handlers already expose machine-readable data
- main implementation risk is response shaping and pagination discipline, not core architecture

## M2. Replay And Dataset Curation Tools

Goal:
Let an AI agent move from inspection to deterministic verification by replaying traces and curating candidate datasets locally.

Scope:

- expose replay-oriented MCP tools
- add bounded write-safe dataset curation commands
- allow failure-focused selection from traces/sessions into datasets

Initial tool candidates:

- `replay_trace`
- `replay_session`
- `create_dataset_from_traces`
- `append_dataset_examples`
- `list_datasets`
- `get_dataset`

Exit criteria:

- a client can take a failed trace and replay it locally without live upstream access
- datasets can be created from existing traces without mutating cassettes
- dataset metadata is persisted in an additive local store
- replay-backed dataset workflows have regression tests

Non-goals:

- model-graded evaluations by default
- autonomous fix generation
- broad destructive dataset editing APIs

Feasibility notes:

- medium-to-high feasibility
- replay already exists, but dataset storage/schema needs new design work
- write tools must be narrow and auditable

## M3. Score, Eval, And Experiment Layer

Goal:
Turn traces and datasets into measurable quality signals that can drive engineering decisions.

Scope:

- add score storage
- support rule-based evaluators first
- support optional judge-based evaluators second
- add experiment runs that compare baseline and candidate results

Minimum entities:

- `datasets`
- `dataset_examples`
- `scores`
- `eval_runs`
- `experiment_runs`

Initial evaluator classes:

- HTTP/status correctness
- schema/tool-call conformance
- refusal/safety expectations
- latency and token budget checks

Optional later evaluator class:

- LLM-as-judge for answer quality, gated behind explicit configuration

Exit criteria:

- one trace or dataset example can accumulate multiple scores
- scores are queryable by trace, session, dataset, and experiment run
- experiments can compare baseline vs candidate at aggregate level
- rule-based evaluators have deterministic tests

Non-goals:

- replacing raw trace inspection
- mandatory judge-model dependencies
- global autonomous optimization

Feasibility notes:

- medium feasibility
- storage and API work are straightforward
- the main risk is adding vague scores with no clear actionability
- start with objective checks before subjective grading

## M4. Additive OTel GenAI Export

Goal:
Make `llm-tracelab` interoperate with modern agent observability ecosystems without surrendering local-first storage control.

Scope:

- derive OTel GenAI spans/events from existing trace/session/timeline data
- support export or bridge mode
- keep cassette and SQLite as primary local truth

Export targets may include:

- trace/span JSON export
- OTLP bridge mode
- external observability adapters built on top of the export layer

Exit criteria:

- one recorded trace can be exported into a stable derived OTel view
- export does not mutate original cassettes
- unsupported or evolving semantic fields are clearly versioned or marked experimental

Non-goals:

- replacing current local storage with OTel
- forcing external infra as a dependency

Feasibility notes:

- medium feasibility
- mapping is practical, but semantics should be treated as derived and versioned because the GenAI conventions continue to evolve

## M5. Agent Optimization Loop

Goal:
Enable bounded, measurable agent-driven improvement loops on top of local traces, replay, and scores.

Scope:

- add MCP tools that let agents query scores and experiments
- support candidate evaluation workflows for prompts, routing, or code changes
- integrate optional external optimizers only after local metrics are stable

Candidate workflows:

- routing policy comparison
- prompt revision comparison
- parser/normalizer regression detection
- fixture expansion based on recurring failure clusters

Exit criteria:

- an agent can identify a failure cluster, run a bounded evaluation flow, and compare candidate outcomes using stored metrics
- optimization decisions are tied to explicit acceptance thresholds
- changes remain reviewable and do not bypass normal code review or test gates

Non-goals:

- unrestricted autonomous commits to arbitrary files
- self-modifying production behavior without review

Feasibility notes:

- medium feasibility for bounded loops
- low feasibility for broad autonomous optimization without stronger dataset maturity
- do not start this milestone until M1-M3 are solid

## Recommended Delivery Order

Recommended order:

1. `M0` documentation and interface framing
2. `M1` read-only MCP access
3. `M2` replay and dataset tools
4. `M3` scores/evals/experiments
5. `M4` additive OTel export
6. `M5` bounded optimization loops

Rationale:

- MCP read access unlocks agent usability quickly with low architectural risk
- replay-backed datasets are the minimum bridge from debugging to evaluation
- scoring must exist before optimization is meaningful
- OTel export is valuable, but not on the critical path for the first useful agent loop

## Definition Of Done Per Milestone

Each milestone is only complete when all of the following are true:

- code and docs are updated together
- replay compatibility is preserved
- additive storage changes are migration-safe
- new APIs or tools have regression tests
- the change is small enough to understand in isolation
- the work lands as at least one explicit git commit

## Commit Discipline

The default implementation discipline for this roadmap is:

- complete one milestone or one clearly bounded slice
- run relevant verification
- create a normal git commit immediately after the slice is complete

The intent is to keep the evolution loop auditable and easy for both humans and agents to inspect.

## Current Readiness Snapshot

Current project readiness against this roadmap:

- `M0`: ready and completed by this document
- `M1`: ready to start
- `M2`: partially ready, pending dataset schema design
- `M3`: not ready until dataset and score shapes are agreed
- `M4`: ready only as additive export after `M1`
- `M5`: intentionally deferred until earlier milestones prove useful

## Immediate Next Step

The next concrete engineering step should be:

1. add a small MCP package using the official Go SDK
2. expose read-only tools over existing monitor/store services
3. verify with one end-to-end MCP integration test
4. commit that slice before opening dataset or eval work
