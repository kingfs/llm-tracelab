# Production Deployment

Date: 2026-06-23

This guide describes the production packaging baseline for llm-tracelab as a
Postgres-backed LLM gateway with record/replay. It is operator guidance for the
checked-in Docker/Compose and YAML examples, not a claim that every future
Responses API tool surface is implemented.

## Default Topology

The default production example is:

- `llm-tracelab`: gateway, Monitor, MCP, recorder, Responses server-mode.
- `postgres`: application/auth database for users, tokens, trace index,
  channel/model state, Responses state, and audit tables.
- `searxng`: optional hosted `web_search` provider, enabled only with the
  Compose `search` profile and `LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED=true`.

Raw `.http` cassette files remain on the application data volume and are still
the replay/detail source of truth. Postgres stores structured state and indexes;
it does not replace raw cassette replay.

## Required Environment

For the checked-in `docker-compose.yml`, the local defaults are usable for an
evaluation stack:

```bash
export LLM_TRACELAB_RESPONSES_DEFAULT_MODEL=gpt-4o-mini
export LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL=http://host.docker.internal:8000/v1
export LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY=local-vllm-placeholder
docker compose up --build
```

For a real deployment, override at least:

```bash
cp .env.example .env
export POSTGRES_PASSWORD='<strong-password>'
export LLM_TRACELAB_DATABASE_DSN='postgres://llm_tracelab:<strong-password>@postgres:5432/llm_tracelab?sslmode=disable'
export LLM_TRACELAB_RESPONSES_DEFAULT_MODEL='<served-model>'
export LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL='https://your-openai-compatible-upstream/v1'
export LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY='<provider-or-vllm-token>'
```

The default config enables `responses_server.enabled=true` and uses an
OpenAI-compatible Chat Completions backend. vLLM is the default preset in
Compose, but any compatible upstream can be used by changing
`LLM_TRACELAB_BOOTSTRAP_UPSTREAM_*` values and, when needed, the routing
profile in a mounted config file. The legacy `LLM_TRACELAB_UPSTREAM_*`
variables are still supported for single-upstream migration, but production
`upstreams` examples avoid them because they also populate the legacy single
`upstream` compatibility block.

## Migrations And First User

The Compose app command runs:

```bash
/app/bin/llm-tracelab -c /app/config/config.yaml db migrate up
/app/bin/llm-tracelab -c /app/config/config.yaml serve
```

Manual equivalent:

```bash
docker compose run --rm llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml db migrate up
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
docker compose --profile search up --build
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
  future work. Current local Responses server-mode uses an OpenAI-compatible
  Chat Completions backend.
- Independent Postgres auth migration namespace: audited gap. Postgres auth
  currently shares the application migration set.
- SQLite versioned application migrations: audited fallback. SQLite remains a
  local startup-schema fallback, not the production migration path.
- MCP/file/code/computer-use real hosted tool execution lifecycle: future
  secure executor work.
- Root/container-grade sandbox for `external_command` function executors:
  future secure executor work. The current process policy provides audited
  first-cut checks such as absolute command, allowed command directories,
  working directory, and root rejection.

## Operator Checks

Useful checks after configuration changes:

```bash
docker compose run --rm llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml config inspect
docker compose run --rm llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml db migrate status --check-db
docker compose run --rm llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml doctor --check-db
```

`doctor --probe-providers` performs explicit network probing. Default checks
remain conservative and should not silently contact upstream providers.
