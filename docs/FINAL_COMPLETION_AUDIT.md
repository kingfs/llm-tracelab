# Final Completion Audit

Date: 2026-06-23

This audit records the evidence used to close the Responses gateway refactor.
It complements `RESPONSES_GATEWAY_COMPLETION_PLAN.md` and captures the final
state after the storage, provider, runtime, observability, and ops workstreams
were merged.

## Requirement Evidence

| Requirement | Evidence |
| --- | --- |
| TraceLab is a production LLM gateway, not only a proxy | README/README_EN, `docs/CURRENT_IMPLEMENTATION.md`, `docs/PROJECT_BASELINE.md`, CLI help, and `docker-compose.yml` now describe a Postgres-first gateway with Responses server-mode plus record/replay. |
| Responses API server-mode over OpenAI-compatible Chat Completions upstream | Runtime/httpapi/proxy tests pass; compose smoke and local Postgres smoke exercised `/v1/responses` create and stream against a mock `/v1/chat/completions` upstream. |
| Local server-mode does not pass `/v1/responses` through | Smoke showed local Responses JSON/SSE produced while upstream only implemented `/v1/chat/completions`; router logs selected Chat Completions target. |
| Non-Responses requests keep proxy/record/replay behavior | Local smoke called `/v1/chat/completions` through the gateway, recorded a V3 cassette, and replayed it offline via `pkg/replay.NewTransport`. |
| Raw `.http` remains replay/detail source | V3 cassette smoke contained raw request/response, routing events, and audit correlation metadata; `pkg/replay` replayed it without network or database. |
| Postgres is production persistence path | `db migrate up` fresh Postgres smoke succeeded; compose smoke started app + Postgres; DSN-gated tests passed for `internal/store`, `internal/responses/runtime`, `internal/appdbmigrate`, `internal/auth`, and `cmd/server`. |
| SQLite is fallback only | Current README, architecture, project baseline, maintainer baseline, and storage docs describe SQLite as legacy/dev/test fallback or startup-schema fallback. |
| Provider control plane expresses API surface and capabilities | `internal/upstream/capability_registry.go`, provider probe/setup/apply paths, router tests, and `config inspect` sources cover `api_type`, `mode`, `protocol_family`, capabilities, and bootstrap env overrides. |
| Unsupported hosted tools do not fake execution | MCP hosted tool execution is implemented and wired; the still-unsupported file/code/computer-use hosted tools are rejected with `unsupported_tool` and write a `tool_call_audits` row with `status=rejected`. |
| Supported server-side tools are audited | Runtime tests cover hosted web_search and configured function executor started/completed/failed events and tool-call audit paths. |
| Docker/Compose/operator docs show production gateway shape | `docker-compose.yml` starts app + Postgres and optional SearXNG profile; README/README_EN and `docs/PRODUCTION_DEPLOYMENT.md` document Postgres-backed startup and upstream overrides. |

## Verification Run

The final verification set included:

- `task check:quick`
- `task test`
- `task build`
- `task test:codex-fixtures`
- `LLM_TRACELAB_TEST_POSTGRES_DSN=... go test -p 1 ./internal/store ./internal/responses/runtime ./internal/appdbmigrate ./internal/auth ./cmd/server -count=1`
- `docker compose config`
- `docker compose --profile search config`
- Fresh Postgres compose smoke: migration, serve, auth bootstrap, Responses create/stream, audit/upstream exchange/cassette correlation.
- Fresh Postgres local smoke: Responses compact, unsupported hosted tool rejected audit, non-Responses Chat Completions proxy recording, and `pkg/replay` offline replay.

## Explicit Remaining Boundaries

The following are intentionally not claimed as completed product scope:

- Cross-protocol request conversion between OpenAI, Anthropic, Gemini, and Vertex.
- Native Responses semantic interposition for upstream Responses providers.
- Independent Postgres auth migration namespace; auth tables are currently owned
  by the application Postgres migration set and `auth migrate down` is blocked.
- Root/container-grade sandboxing for external command executors.
- Real file/code/computer-use hosted tool execution lifecycle (MCP hosted tool execution is implemented).
- Automatic SQLite-to-Postgres data migration.

These are documented future boundaries, not blockers for the current
Postgres-first Responses gateway refactor.
