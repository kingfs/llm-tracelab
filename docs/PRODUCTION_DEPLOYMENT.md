# Production Deployment

Date: 2026-06-23

This guide describes the production packaging baseline for llm-tracelab as a
Postgres-backed LLM gateway with record/replay. It is operator guidance for the
checked-in Docker/Compose and YAML examples, not a claim that every future
Responses API tool surface is implemented.

## Default Topology

The default production example is:

- `llm-tracelab`: gateway, Monitor, MCP, recorder, local Responses runtime.
- `postgres`: application/auth database for users, tokens, trace index,
  channel/model state, Responses state, and audit tables.
- `searxng`: optional hosted `web_search` provider container, started only
  with the Compose `search` profile. The hosted `web_search` tool itself is
  enabled by default (`LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED` defaults to
  `true` in `docker-compose.yml`); the `search` profile only controls whether
  the searxng provider container is running.

Raw `.http` cassette files remain on the application data volume and are still
the replay/detail source of truth. Postgres stores structured state and indexes;
it does not replace raw cassette replay.

## Required Environment

For the checked-in `docker-compose.yml`, the local defaults start Postgres,
the gateway, Monitor, MCP, and the recorder without requiring any upstream
provider:

```bash
cp .env.example .env
docker compose up -d
```

For a real deployment, override at least the database password/DSN:

```bash
cp .env.example .env
export POSTGRES_PASSWORD='<strong-password>'
export LLM_TRACELAB_DATABASE_DSN='postgres://llm_tracelab:<strong-password>@postgres:5432/llm_tracelab?sslmode=disable'
```

Optionally set `LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL` and
`LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY` to import one initial
OpenAI-compatible provider. Leaving both empty is valid: configure providers,
credentials, and models in Monitor Web after login. The legacy
`LLM_TRACELAB_UPSTREAM_*` variables remain supported for single-upstream
migration, but new deployments should use the Web-managed provider database.

## Migrations And First User

The app starts with `serve`; with `database.auto_migrate: true`, startup applies
the checked-in application Postgres migrations before opening the store. Manual
equivalent:

```bash
docker compose run --rm llm-tracelab -c /app/config/config.yaml db migrate up
docker compose up -d
docker compose exec llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml auth init-user --username admin --password 'change-me-123'
```

Postgres auth migrations currently share the application `schema_migrations`
namespace. This is an audited operational constraint, not an independent auth
migration namespace.

## Optional SearXNG

Start with SearXNG enabled:

```bash
export LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED=true
docker compose --profile search up -d
```

The application reads `tools.web_search.provider=searxng` and
`tools.web_search.base_url=http://searxng:8080`. Hosted `web_search` and
`web_search_preview` are executed server-side only when the provider is enabled
and ready. Unsupported hosted tools are rejected and audited.

## Explicitly Not Implemented As Production Support

- Public multi-tenant relay, billing, subscription, or quota resale platform:
  rejected for this product line.
- Cross-protocol request translation in the proxy hot path: rejected; existing
  non-Responses traffic remains protocol-aware pass-through plus recording.
- Native Responses semantic interposition for every upstream provider:
  future work. The current local Responses runtime uses an OpenAI-compatible
  Chat Completions backend.
- Independent Postgres auth migration namespace: audited gap. Postgres auth
  currently shares the application migration set.
- SQLite versioned application migrations: audited fallback. SQLite remains a
  local startup-schema fallback, not the production migration path.
- file/code/computer-use real hosted tool execution lifecycle: future
  secure executor work (MCP hosted tool execution is implemented and wired).
- Root/container-grade sandbox for `external_command` function executors:
  future secure executor work. The current process policy provides audited
  first-cut checks such as absolute command, allowed command directories,
  working directory, and root rejection.

## Operator Checks

Useful checks after configuration changes:

```bash
docker compose run --rm llm-tracelab -c /app/config/config.yaml config inspect
docker compose run --rm llm-tracelab -c /app/config/config.yaml db migrate status --check-db
docker compose run --rm llm-tracelab -c /app/config/config.yaml doctor --check-db
```

`doctor --probe-providers` performs explicit network probing. Default checks
remain conservative and should not silently contact upstream providers.
