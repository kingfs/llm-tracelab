# Roadmap

## Vision

`llm-tracelab` should become a local-first LLM API record/replay proxy that:

- proxies real upstream traffic during development
- records raw HTTP exchanges as human-inspectable cassettes
- replays those cassettes in tests without network access
- supports multiple mainstream LLM protocol families instead of only one API shape

The project is not trying to be an SDK or a model gateway platform.
Its core value is reliable capture, replay, inspection, and cross-provider normalization.

## Product Goal

The target state is:

1. one proxy process can sit in front of multiple mainstream LLM API styles
2. one cassette format can preserve raw protocol bytes plus normalized timeline semantics
3. one replay layer can drive unit tests without calling upstreams again
4. one monitor can inspect requests, responses, tools, reasoning, refusal, usage, and timing across providers

## Non-Goals

These are explicitly not primary goals:

- building a hosted multi-tenant gateway
- replacing provider SDKs
- inventing a new universal LLM request format for users to write against
- hiding all provider differences at any cost

The project should normalize only what is replay-critical, monitor-critical, or compatibility-critical.

## Current State

The project already has:

- cassette write format `LLM_PROXY_V3`
- V2 read compatibility
- SQLite index for monitor list/statistics
- protocol-family abstraction in `internal/upstream`
- provider/endpoint adapter layer in `pkg/llm`
- working support for:
  - `openai_compatible`
  - `anthropic_messages`
  - `google_genai`
- upstream presets for OpenAI, Azure, vLLM, Anthropic, Google, and a set of OpenAI-compatible providers
- cross-layer regression coverage via cassette matrix tests

This is enough to move from foundational plumbing into structured expansion.

## Planning Principles

All roadmap work should follow these rules:

1. preserve replay compatibility first
2. prefer new protocol families over endless provider-specific special cases
3. add presets only when they map cleanly to an existing protocol family
4. treat raw `.http` cassettes as source of truth
5. make every new capability prove itself through cassette-based regression tests
6. keep provider-specific behavior isolated in routing/auth/config layers whenever possible

## Success Criteria

The roadmap is considered successful when:

- users can proxy and replay the most common upstreams they already use
- new providers usually require config/preset work, not core parser rewrites
- protocol-family additions are additive instead of destabilizing old cassettes
- monitor output stays semantically useful across providers
- test coverage protects recorder, replay, parser, and monitor together

## Workstreams

### 1. Protocol Coverage

Expand first-class support for real upstream API families.

Current families:

- `openai_compatible`
- `anthropic_messages`
- `google_genai`

Future candidates:

- `vertex_native`
- `bedrock_native`
- `realtime_session`

### 2. Provider Compatibility

Continue improving preset-based compatibility for well-known providers without polluting core parsing logic.

Focus:

- routing differences
- auth header differences
- query parameter differences
- deployment/model path differences
- startup diagnostics and config validation

### 3. Cassette Semantics

Improve the normalized event and metadata layer that sits above raw protocol bytes.

Focus:

- tools
- tool results
- reasoning
- refusal / safety block
- usage
- multi-turn history
- stream event consistency

### 4. Monitor Experience

Make recorded traces easier to inspect across protocols.

Focus:

- richer timeline semantics
- better projection of tool loops and multi-turn history
- clearer provider capability differences
- safer debugging of refusal/error flows

### 5. Engineering Guardrails

Keep growth sustainable.

Focus:

- stronger config validation
- clearer support matrix docs
- better end-to-end regression fixtures
- startup and health diagnostics

## Milestones

## M1. Stabilize Current Three Families

Goal:
Make `openai_compatible`, `anthropic_messages`, and `google_genai` solid enough that they feel intentional rather than experimental.

Scope:

- complete higher-value `responses` edge cases
- strengthen Google timeline and response-block semantics
- expand cassette coverage for multi-turn, history, and richer tool flows
- improve docs for provider presets and support status

Exit criteria:

- cassette matrix covers `non_stream`, `stream`, `reasoning`, `tool_call`, `tool_result`, `refusal`, `error`, and `multi_turn`
- high-frequency providers have explicit verified presets
- README and provider docs distinguish stable support vs planned support

## M2. Make Compatibility Predictable

Goal:
Reduce user confusion about whether a provider is supported and how to configure it.

Scope:

- add config validation for incompatible `provider_preset` / `protocol_family` / `routing_profile` combinations
- improve startup diagnostics for upstream resolution and model path behavior
- publish a support matrix with validation level, not just preset names

Exit criteria:

- invalid config combinations fail fast with actionable errors
- startup output clearly shows resolved protocol family and routing profile
- docs classify presets as `verified`, `compatible`, or `planned`

## M3. Strengthen Replay-Critical Semantics

Goal:
Ensure cassettes remain reliable replay and debugging assets as feature coverage expands.

Scope:

- add more fixture capabilities:
  - `multi_turn`
  - `history`
  - `mixed_blocks`
  - `safety`
  - `model_list`
- add more end-to-end behavior tests for provider routing and auth
- keep replay and monitor semantics aligned through cassette-first tests

Exit criteria:

- new features are blocked unless they add cassette-level regression coverage
- core provider flows are tested through recorder/replay/monitor, not only parser unit tests

## M4. Add One New Protocol Family

Goal:
Prove the architecture scales beyond the current three families.

Candidate order:

1. `vertex_native`
2. `bedrock_native`
3. `realtime_session`

Selection rule:

- choose the next family based on ecosystem demand and semantic difference from existing families
- do not add a new family only because a provider is popular if an existing family already fits

Exit criteria:

- new family has isolated routing/auth logic
- request/response/stream parsing is adapter-based
- replay-critical cassette coverage exists
- monitor can render a useful normalized summary

## M5. Operational Polish

Goal:
Make the project easier to run, debug, and trust in real teams.

Scope:

- better migration/index rebuild ergonomics
- clearer health checks
- more explicit error surfaces in monitor
- packaging and example configs for representative providers

Exit criteria:

- a new user can configure at least one provider from each stable family without reading source
- operational failure modes are visible and diagnosable

## Priority Order

Recommended execution order:

1. finish M1
2. finish M2
3. finish M3
4. select and execute M4
5. finish M5

This order matters.
The project should avoid expanding to too many new families before current semantics, docs, and test guardrails are stable.

## Near-Term Plan

The next concrete implementation sequence should be:

1. add `multi_turn` cassette capability and fixtures
2. improve Google timeline richness and normalized blocks
3. add support-status documentation and config validation
4. expand provider verification for high-frequency presets
5. evaluate `vertex_native` as the next protocol family

## Acceptance Checklist Per Feature

Every meaningful roadmap item should satisfy most of this checklist:

- config resolution is explicit
- raw protocol bytes remain human-inspectable
- replay still works without network access
- monitor summary remains useful
- cassette prelude/events are correct
- SQLite indexing remains compatible
- README/docs are updated when user-facing behavior changes
- regression tests cover the behavior end-to-end

## Open Questions

These should be revisited before M4:

- should Vertex share `google_genai` semantics with a different routing profile, or become its own family
- should Bedrock-native support be added directly, or only through provider-compatible surfaces first
- how far should the project go in normalizing reasoning and safety semantics before losing provider truth
- whether model-listing and capability-discovery endpoints should become first-class replay targets

## Decision Rule For Future Contributions

When adding support for something new:

- if only URL/auth/header behavior differs, add a preset or routing profile
- if payload or stream semantics differ, add or extend a protocol family
- if the feature cannot be protected by cassette-based replay tests, the design is probably incomplete
