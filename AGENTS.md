# AGENTS

## Project Intent

`llm-tracelab` is a local-first LLM API record/replay proxy for OpenAI-compatible APIs.
Its main use case is:

1. route SDK traffic through a proxy during development
2. persist the raw HTTP exchange as a `.http` cassette
3. replay the cassette in unit tests without hitting the upstream model provider

The project optimizes for reliable tests, lower API cost, and fast debugging.

## Current Architecture

- Entry point: `cmd/server/main.go`
- Reverse proxy: `internal/proxy`
- Recording pipeline: `internal/recorder`
- Metadata index: `internal/store` using SQLite at `{{output_dir}}/trace_index.sqlite3`
- Monitor UI: `internal/monitor`
- Replay transport for tests: `pkg/replay`
- Shared record format parser: `pkg/recordfile`
- Cross-provider request/response normalization helpers: `pkg/llm`

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
- Lint: `task lint`
- Test: `task test`
- Build: `task build`
- Run locally: `task run`
- Full check: `task check`

## When Changing Storage Or Format

- Update `pkg/recordfile` first, then adapt recorder, monitor, and replay together.
- Keep V2 read compatibility unless the task explicitly allows a breaking change.
- If schema changes in `internal/store`, ensure startup initialization still works on an existing local DB.
- Prefer indexing metadata in SQLite rather than rescanning every file for aggregate stats.

## Documentation Targets

- `README.md` and `README_EN.md`: human-facing overview and quick start
- `AGENTS.md`: AI-oriented project map and invariants
- add focused docs under `docs/` only when they clarify architecture or storage decisions
