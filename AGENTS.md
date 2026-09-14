# AGENTS

## Project Intent

`llm-tracelab` is a local-first LLM API record/replay proxy for OpenAI-compatible and other mainstream LLM APIs.
Its main use case is:

1. route SDK traffic through a proxy during development
2. persist the raw HTTP exchange as a `.http` cassette
3. replay the cassette in unit tests without hitting the upstream model provider

The project optimizes for reliable tests, lower API cost, and fast debugging.

## Current Architecture

- CLI entry point: `cmd/server/main.go` only exits through `run`; command wiring lives in `cmd/server/root.go`
- CLI command files: `cmd/server/serve.go`, `migrate.go`, `auth.go`, `db.go`, `version.go`, `completion.go`
- Management HTTP/MCP wiring shared by serve and tests: `cmd/server/management.go`
- Reverse proxy: `internal/proxy`
- Recording pipeline: `internal/recorder`
- Metadata index: `internal/store` using SQLite at `{{output_dir}}/trace_index.sqlite3`
- Monitor UI: `internal/monitor`
- Replay transport for tests: `pkg/replay`
- Shared record format parser: `pkg/recordfile`
- Cross-provider request/response normalization helpers: `pkg/llm`

Current protocol families are documented in `docs/protocol-reference/implemented-protocols.md`.
The proxy is protocol-aware pass-through plus recording/parsing; it does not currently translate requests between OpenAI, Anthropic, Gemini, and Vertex protocol families in the forwarding hot path.
The single exception is `/v1/responses`: the proxy accepts `/v1/chat/completions`, `/v1/responses` and `/v1/messages` unconditionally, and per-request routing prefers a matching native Responses upstream (pass-through) before falling back to the local Responses runtime, which orchestrates the request as an internal upstream `/v1/chat/completions` call. That local execution mode is always available and has no configuration switch; the legacy `responses_server.enabled` field and `LLM_TRACELAB_RESPONSES_ENABLED` variable were removed (`routing.settings.responses_strategy=native_only` opts out). The local Responses runtime is built lazily, so optional provider configuration must never block startup. The native-vs-local choice is resolved per model, not per channel: an explicit `channel_models.supports_responses` / `supports_chat_completions` value (editable in the monitor UI) overrides the channel-level `api_type`/`capabilities`, and models without a declared value fall back to the channel-level behaviour. `upstream.model_capabilities` expresses the same override in YAML.

## Record File Format

New recordings use `LLM_PROXY_V3`:

1. a short prelude starting with `# llm-tracelab/v3`
2. one `# meta: {...}` JSON line
3. zero or more `# event: {...}` JSON lines
4. one blank line
5. raw HTTP request bytes
6. one separator newline
7. raw HTTP response bytes

Compatibility note:

- readers must continue to support legacy `LLM_PROXY_V2` files with a fixed 2KB JSON header block
- writers should only emit V3 unless a migration task explicitly says otherwise

## Engineering Constraints

- Preserve replay compatibility. `pkg/replay` is a hard requirement.
- Do not make tests depend on network access.
- Keep recorded `.http` payloads human-inspectable.
- Prefer additive evolution over destructive migration of existing cassettes.
- SQLite is the source for monitor list/statistics; raw `.http` files remain the source of truth for replay and detail views.

## Common Workflows

- Format: `task fmt`
- Formatting check without edits: `task fmt:check`
- Lint: `task lint`
- Test: `task test`
- Short check: `task check:quick`
- Full check: `task check:full`
- Race tests: `task test:race`
- Benchmarks: `task bench:core`
- Build backend only: `task build:go`
- Build everything: `task build`
- Run locally: `task run`
- See `docs/DEVELOPMENT_COMMANDS.md` for the command matrix humans and AI agents should use.

## When Changing Storage Or Format

- Update `pkg/recordfile` first, then adapt recorder, monitor, and replay together.
- Keep V2 read compatibility unless the task explicitly allows a breaking change.
- If schema changes in `internal/store`, ensure startup initialization still works on an existing local DB.
- Prefer indexing metadata in SQLite rather than rescanning every file for aggregate stats.

## Documentation Targets

- `README.md` and `README_EN.md`: human-facing overview and quick start
- `AGENTS.md`: AI-oriented project map and invariants
- `docs/README.md`: 中文文档总入口，说明当前事实文档、用户指南、开发指南、v1 设计和归档文档的阅读顺序
- `docs/CURRENT_IMPLEMENTATION.md`: 当前最新代码事实的中文概览
- `docs/PROJECT_BASELINE.md`: current implemented capability baseline for both humans and AI agents
- `docs/protocol-reference/README.md`: current protocol reference entry, implemented protocol matrix, protocol differences, and dated upstream schema snapshots
- `docs/v1/README.md`: v1 中文设计文档入口，区分当前事实、设计背景和历史计划
- `docs/v1/status.md`: v1 能力当前落地状态
- `docs/v1/reference-materials/README.md`: historical v1 protocol parsing reference snapshots; prefer `docs/protocol-reference/README.md` for current implementation work
- `docs/MONITOR_GUIDE.md`: current user-facing monitor capabilities and workflows
- `docs/MCP_GUIDE.md`: current MCP tool surface and usage
- `docs/MAINTAINER_BASELINE.md`: implementation constraints, upgrade expectations, and storage/monitor invariants
- `docs/DEVELOPMENT_COMMANDS.md`: stable test, lint, build, benchmark, and dependency command entry points
- `docs/archive/README.md`: historical plans, completed phase designs, branch notes, and older roadmap material; not a current implementation fact source
- add focused docs under `docs/` only when they clarify architecture or storage decisions
