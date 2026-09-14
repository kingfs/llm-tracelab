# llm-tracelab

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](./LICENSE)

[中文说明](./README.md) | **English**

`llm-tracelab` is a Postgres-first LLM gateway with built-in LLM HTTP record/replay, Responses server-mode, Monitor, and MCP diagnostics. It currently covers OpenAI-compatible, Anthropic Messages, Google GenAI, and Vertex-native protocol families.
The core workflow is simple:

- use Postgres for production users, tokens, trace index, channels/models, Responses state, and audit data
- serve optional `/v1/responses` semantics over an OpenAI-compatible / vLLM upstream
- record real LLM HTTP exchanges as `.http` cassettes and replay them offline in tests

Raw `.http` cassettes remain the source of truth for replay and detail views. Postgres is the production structured-state path; SQLite remains a local fallback and offline test path.

## Current Release Notes

This refactor introduces four major changes:

- `pkg/llm` is now a provider/endpoint adapter layer for requests, responses, stream transcripts, and usage pipelines
- the monitor is now an embedded React UI with async pagination and detail views for timeline / summary / raw protocol
- Postgres application migrations now use checked-in SQL; SQLite is explicitly a startup-schema fallback
- `LLM_PROXY_V3` `# event:` lines now include `llm.*` provider timelines in addition to base request/response events

## Good Fit

- stable unit tests for SDK or application code
- reproducing prompt, tool-call, or streaming issues
- inspecting latency, TTFT, and token usage
- local proxying and debugging of LLM traffic

## Features

- transparent proxy for OpenAI-compatible requests
- persists each exchange as a local `.http` cassette
- `pkg/replay.Transport` for replay-based unit tests
- monitor UI for request detail, unified timeline, and raw protocol views
- Postgres metadata, audit, and Responses state for production; SQLite fallback for local development
- default production Compose topology with app + Postgres and optional SearXNG hosted `web_search`
- backward-compatible readers for legacy V2 record files

## Layout

```text
cmd/server            server entrypoint
internal/proxy        reverse proxy and response interception
internal/recorder     cassette recording and persistence
internal/store        Postgres/SQLite application store and metadata index
internal/monitor      monitor UI and detail parsing
pkg/recordfile        shared V2/V3 record format parser/writer
pkg/replay            replay transport for tests
pkg/llm               cross-provider normalization helpers
```

AI-oriented project guidance lives in [AGENTS.md](./AGENTS.md). The current implemented baseline is summarized in [docs/PROJECT_BASELINE.md](./docs/PROJECT_BASELINE.md). Production deployment guidance is in [docs/PRODUCTION_DEPLOYMENT.md](./docs/PRODUCTION_DEPLOYMENT.md). The user-facing monitor workflow guide is in [docs/MONITOR_GUIDE.md](./docs/MONITOR_GUIDE.md), and authenticated proxy examples are in [docs/PROXY_USAGE_EXAMPLES.md](./docs/PROXY_USAGE_EXAMPLES.md). The maintainer-oriented implementation baseline is in [docs/MAINTAINER_BASELINE.md](./docs/MAINTAINER_BASELINE.md). A short technical summary is in [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md), the upstream compatibility matrix is in [docs/UPSTREAM_PROVIDERS.md](./docs/UPSTREAM_PROVIDERS.md), and the credential routing operator guide is in [docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md](./docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md).

The gateway ecosystem review compares Sub2API, LiteLLM, Portkey, and Helicone. It clarifies that TraceLab should not become a public relay, payment, or SaaS quota-distribution platform; it should absorb channel management, routing, rate limiting, health, cost, and governance ideas only where they strengthen local-first record/replay, debugging, audit, and evaluation workflows.

## Record Format And Index

New recordings use `LLM_PROXY_V3`:

1. a compact metadata prelude instead of a fixed 2KB header block
2. full raw HTTP request/response bytes kept for inspection and replay
3. `# event:` lines now capture normalized provider timeline events such as `llm.output_text.delta`, `llm.reasoning.delta`, `llm.tool_call`, and `llm.usage`
4. metadata, trace ids, session hints, Responses audit, and derived summaries are indexed into the application database; production defaults to Postgres

Production storage layout:

```text
Postgres:
  users / api_tokens / channel_configs / channel_models
  logs / sessions / responses / response_items
  request_audits / execution_events / upstream_exchanges / tool_call_audits

data/traces/
  <upstream-host>/<model>/<yyyy>/<mm>/<dd>/*.http
```

## Quick Start

### 1. Configure Startup Settings

Starting with v1, YAML should be limited to service startup settings: ports, database, trace output directory, auth, MCP, router policy, Responses server, and tool switches. Model channels, provider base URLs, API keys, model enablement, and model profiles should be managed in the Monitor Web UI and persisted to the application database.

[config/config.yaml](./config/config.yaml) is the tracked default Postgres-first startup config. It contains no real secrets and no built-in provider. With no upstream configured, the service still starts and Monitor Web can be used to configure providers later. Production deployments should inject only deployment-local values through environment variables: Postgres DSN/password, exposed ports, and an optional single bootstrap upstream base URL/API key. Local SQLite development can use [config/examples/local-sqlite.yaml](./config/examples/local-sqlite.yaml).

The recommended base config shape is:

```yaml
server:
  port: "8080"

monitor:
  port: "8081"

mcp:
  enabled: true
  path: "/mcp"

database:
  driver: "postgres"
  dsn: ""
  max_open_conns: 16
  max_idle_conns: 8
  auto_migrate: true

auth:
  session_ttl: 24h

trace:
  output_dir: "./data/traces"

responses_server:
  enabled: true
  default_model: ""
  force_store: true
  # Image inputs may be base64 encoded and substantially larger than text requests.
  max_request_body_bytes: 67108864
  path: "/v1/responses"
  auto_compact: true

router:
  model_discovery:
    enabled: true
    refresh_interval: 10m
    startup_policy: "best_effort"
  selection:
    policy: "p2c"
    epsilon: 0.02
    open_window: 15s
    failure_threshold: 3
  fallback:
    on_missing_model: "reject"

debug:
  output_dir: "./data/traces"
  mask_key: true
```

Legacy `upstream` / `upstreams` YAML is still supported, but it is no longer the long-term production configuration entry point. On first startup, setting `LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL` imports one OpenAI-compatible bootstrap provider; leaving it empty starts only the Web and management surface. Imported channels are marked as `bootstrap` in Monitor; edit, probe, enable, and disable models from the Web UI after import.

For an example with two explicit credentials under one upstream, plus sticky route target, credential-safe metadata, and limit scope guidance, see [docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md](./docs/CREDENTIAL_ROUTING_OPERATOR_GUIDE.md). The examples use `$env:...` placeholders only; do not commit real provider secrets in YAML.

If you prefer starting from a ready-made bootstrap config, use one of these examples; long-lived channel configuration should still be managed in Monitor Web:

- [config/examples/openai.yaml](./config/examples/openai.yaml)
- [config/examples/openai-compatible-vllm-postgres.yaml](./config/examples/openai-compatible-vllm-postgres.yaml)
- [config/examples/local-sqlite.yaml](./config/examples/local-sqlite.yaml)
- [config/examples/anthropic.yaml](./config/examples/anthropic.yaml)
- [config/examples/google_genai.yaml](./config/examples/google_genai.yaml)
- [config/examples/azure_openai.yaml](./config/examples/azure_openai.yaml)
- [config/examples/vertex.yaml](./config/examples/vertex.yaml)

Production environment injection should stay small: `LLM_TRACELAB_DATABASE_DSN`, `POSTGRES_PASSWORD`, `LLM_TRACELAB_HOST_SERVER_PORT`, `LLM_TRACELAB_HOST_MONITOR_PORT`, and optional `LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL`, `LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY`, `LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED`. Keep other service behavior in `config/config.yaml`. The legacy `LLM_TRACELAB_UPSTREAM_*` variables remain available for old single-upstream migrations, but new deployments should manage providers in Monitor Web.

Access control notes:

- `database` is the unified structured store for users, API tokens, trace index, sessions, upstream metadata, datasets, eval metadata, Responses state, and audit data. Production defaults to Postgres.
- Initialize the first user with `go run ./cmd/server auth init-user -c config/config.yaml --username admin --password 'change-me-123'`.
- The Monitor UI uses username/password login. The web login session uses a monitor-only JWT and does not reuse personal API tokens.
- After login, use the `Tokens` page to generate a personal API token for the current user.
- Personal API tokens work for the LLM proxy API and MCP with `Authorization: Bearer <token>`.
- Channels / Models are managed in Monitor Web and stored in the application database; YAML is no longer the long-lived channel configuration surface.

Recommended compatibility pattern:

- OpenAI / OpenRouter / Fireworks / Together / DeepSeek / Groq and similar OpenAI-compatible services: set `provider_preset` plus `base_url`, and make sure `base_url` already includes the upstream API prefix such as `/v1`, `/api/v1`, `/openai`, or `/openai/v1`
- Azure OpenAI `/openai/v1/...`: set `provider_preset: azure` and optionally `api_version`
- Azure deployment-style routing: set `provider_preset: azure` plus `deployment`
- vLLM OpenAI-compatible server: set `provider_preset: vllm`
- Anthropic Messages API: set `provider_preset: anthropic`; use `headers.anthropic-beta` if you need beta features
- Google GenAI API: set `provider_preset: google_genai`; this round supports the base `generateContent` and `streamGenerateContent` flows
- Vertex AI native API: prefer `provider_preset: vertex`; it infers `vertex_express` or `vertex_project_location` from `base_url`

Support level meanings:

- `verified`: backed by direct behavior tests or cassette-level regression coverage
- `compatible`: expected to work under an existing protocol family, but with lighter direct verification
- `planned`: not yet implemented as a preset or not yet supported

Config validation rules:

- `provider_preset`, `protocol_family`, and `routing_profile` are no longer loose independent knobs
- invalid combinations now fail at startup instead of failing later during proxy traffic
- for example, `provider_preset: anthropic` with `protocol_family: google_genai` is rejected
- likewise, `provider_preset: openrouter` with `routing_profile: azure_openai_v1` is rejected

Current recommended support matrix:

- `provider_preset: openai`
  `support: verified`
  `protocol_family: openai_compatible`
  `routing_profile: openai_default`
- `provider_preset: openrouter | fireworks | together | deepseek | groq | moonshot | cerebras | perplexity`
  `support: openrouter/fireworks/together/groq=verified; deepseek/moonshot/cerebras/perplexity=compatible`
  `protocol_family: openai_compatible`
  `routing_profile: openai_default`
- `provider_preset: azure`
  `support: verified`
  `protocol_family: openai_compatible`
  `routing_profile: azure_openai_v1` or `azure_openai_deployment`
- `provider_preset: vllm`
  `support: verified`
  `protocol_family: openai_compatible`
  `routing_profile: vllm_openai`
- `provider_preset: anthropic`
  `support: verified`
  `protocol_family: anthropic_messages`
  `routing_profile: anthropic_default`
- `provider_preset: google_genai | google | gemini`
  `support: verified`
  `protocol_family: google_genai`
  `routing_profile: google_ai_studio`
- `provider_preset: vertex`
  `support: verified`
  `protocol_family: vertex_native`
  `routing_profile: vertex_express | vertex_project_location`
  `notes: controlled preset; covered by adapter / proxy / cassette regressions`

Anthropic example:

The examples below use YAML to document field meanings for legacy compatibility and first-run bootstrap. For new or long-lived channel configuration, set the same fields in Monitor Web > Channels.

```yaml
upstream:
  base_url: "https://api.anthropic.com"
  api_key: "sk-ant-xxx"
  provider_preset: "anthropic"
  api_version: "2023-06-01"
  headers:
    anthropic-beta: "tools-2024-04-04"
```

Google GenAI example:

```yaml
upstream:
  base_url: "https://generativelanguage.googleapis.com"
  api_key: "AIza..."
  provider_preset: "google_genai"
```

Vertex express example:

```yaml
upstream:
  base_url: "https://aiplatform.googleapis.com"
  api_key: "ya29..."
  provider_preset: "vertex"
  model_resource: "publishers/google/models/gemini-2.5-flash"
```

Vertex project/location example:

```yaml
upstream:
  base_url: "https://us-central1-aiplatform.googleapis.com"
  api_key: "ya29..."
  provider_preset: "vertex"
  project: "demo-project"
  location: "us-central1"
  model_resource: "publishers/google/models/gemini-2.5-flash"
```

If you want to avoid presets entirely, explicit config still works:

- `protocol_family: vertex_native`
- `routing_profile: vertex_express | vertex_project_location`

Azure deployment example:

```yaml
upstream:
  base_url: "https://demo-resource.openai.azure.com"
  api_key: "azure-key"
  provider_preset: "azure"
  deployment: "gpt-4o-mini"
  api_version: "2025-03-01-preview"
```

### 2. Build And Run

Using `go-task` is the recommended workflow:

```bash
task build
task run
task migrate
```

The default config path is the Postgres-first example `config/config.yaml`; local `config inspect` / `doctor` work as-is, while real startup should point at a reachable Postgres database and upstream. For SQLite local development, choose a config explicitly:

```bash
CONFIG=config/examples/local-sqlite.yaml task run
```

Direct run also works:

```bash
export LLM_TRACELAB_DATABASE_DSN='postgres://llm_tracelab:llm_tracelab@localhost:5432/llm_tracelab?sslmode=disable'
export LLM_TRACELAB_RESPONSES_DEFAULT_MODEL=gpt-4o-mini
export LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL=http://localhost:8000/v1
export LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY=local-vllm-placeholder
go run ./cmd/server -c config/config.yaml
```

Point your SDK `base_url` to `http://localhost:8080/v1` and traffic will be recorded through the proxy.
The proxy API requires a personal token. OpenAI-compatible SDKs usually send `api_key` as `Authorization: Bearer <api_key>`, so set the SDK API key to the llm-tracelab token generated from the Monitor `Tokens` page.

curl example:

```bash
export LLM_TRACELAB_TOKEN=llmtl_xxx
curl -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  http://localhost:8080/v1/models | jq
```

More non-streaming, streaming, and SDK examples are in [docs/PROXY_USAGE_EXAMPLES.md](./docs/PROXY_USAGE_EXAMPLES.md).

### 3. Open Monitor

Visit `http://localhost:8081` and sign in with the initialized username and password. Request lists, session views, and trace details require login so recorded LLM payloads are not exposed to unauthenticated users.

The detail page now has three first-class views:

- `Timeline`: consumes normalized `llm.*` cassette events
- `Summary`: conversation / tools / output block projection
- `Raw Protocol`: side-by-side request/response inspection

## Legacy Cassette Migration And Index Rebuild

Use the explicit migration command:

```bash
go run ./cmd/server migrate -c config/config.yaml
```

By default it does both:

- rewrites legacy `LLM_PROXY_V2` `.http` cassettes in place to `LLM_PROXY_V3`
- clears and rebuilds trace index rows in the application database; users and tokens are preserved

Run only one part if needed:

```bash
go run ./cmd/server migrate -c config/config.yaml -rewrite-v2=false
go run ./cmd/server migrate -c config/config.yaml -rebuild-index=false
```

This is intended for bulk upgrades of old cassette directories and for structured trace index recovery. The `.http` cassette remains the replay and detail source of truth.

## Docker And Compose

The standardized in-container paths are:

- binary: `/app/bin/llm-tracelab`
- config file: `/app/config/config.yaml`
- trace directory: `/app/data/traces`
- database: Postgres service, configured through `LLM_TRACELAB_DATABASE_DSN`

The repo now includes:

- [Dockerfile](./Dockerfile)
- [docker-compose.yml](./docker-compose.yml)
- [config/config.yaml](./config/config.yaml)

Start it with:

```bash
cp .env.example .env
docker compose up -d
docker compose exec llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml auth init-user --username admin --password 'change-me-123'
```

Then visit `http://localhost:8081`, sign in, configure upstream base URLs, API keys, and models from the `Providers` page, and create a personal token from the `Tokens` page for SDK / MCP traffic.
When SDKs call the proxy, use this token as the SDK API key. For direct curl calls, send `Authorization: Bearer <token>`.

Optional SearXNG hosted `web_search`:

```bash
export LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED=true
docker compose --profile search up -d
```

For local source builds, use the dev override:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

If you only want to use the published Docker Hub image, provide an external Postgres database:

```bash
docker run --rm \
  -p 8080:8080 \
  -p 8081:8081 \
  -e LLM_TRACELAB_DATABASE_DRIVER=postgres \
  -e LLM_TRACELAB_DATABASE_DSN='postgres://llm_tracelab:llm_tracelab@host.docker.internal:5432/llm_tracelab?sslmode=disable' \
  -e LLM_TRACELAB_RESPONSES_FORCE_STORE=true \
  -e LLM_TRACELAB_RESPONSES_DEFAULT_MODEL=gpt-4o-mini \
  -e LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL=http://host.docker.internal:8000/v1 \
  -e LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY=local-vllm-placeholder \
  -e LLM_TRACELAB_OUTPUT_DIR=/app/data/traces \
  -e LLM_TRACELAB_TRACE_OUTPUT_DIR=/app/data/traces \
  -e LLM_TRACELAB_SERVER_PORT=8080 \
  -e LLM_TRACELAB_MONITOR_PORT=8081 \
  -v "$(pwd)/docker-data:/app/data" \
  kingfs/llm-tracelab:latest serve -c /app/config/config.yaml
```

If you prefer `docker compose`, you can also reference the Docker Hub image directly:

```yaml
services:
  llm-tracelab:
    image: kingfs/llm-tracelab:latest
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "8080:8080"
      - "8081:8081"
    environment:
      LLM_TRACELAB_DATABASE_DRIVER: postgres
      LLM_TRACELAB_DATABASE_DSN: postgres://llm_tracelab:llm_tracelab@postgres:5432/llm_tracelab?sslmode=disable
      LLM_TRACELAB_RESPONSES_FORCE_STORE: "true"
      LLM_TRACELAB_RESPONSES_DEFAULT_MODEL: gpt-4o-mini
      LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL: http://host.docker.internal:8000/v1
      LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY: local-vllm-placeholder
      LLM_TRACELAB_OUTPUT_DIR: /app/data/traces
      LLM_TRACELAB_TRACE_OUTPUT_DIR: /app/data/traces
      LLM_TRACELAB_SERVER_PORT: "8080"
      LLM_TRACELAB_MONITOR_PORT: "8081"
    volumes:
      - ./config/config.yaml:/app/config/config.yaml:ro
      - ./docker-data:/app/data
    command: ["serve", "-c", "/app/config/config.yaml"]
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: llm_tracelab
      POSTGRES_USER: llm_tracelab
      POSTGRES_PASSWORD: llm_tracelab
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 20
```

If the default Go module proxy is slow or blocked in your network, pass `GOPROXY` at build time:

```bash
GOPROXY=https://goproxy.cn,direct docker compose build
```

`task docker:build` and `task docker:up` share the same build variable convention. They prefer `DOCKER_BUILD_*`, then the current shell's `GOPROXY`, `GOSUMDB`, `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` values, including lowercase variants. If `GOPROXY` / `GOSUMDB` are not exported in the shell, the tasks fall back to `go env GOPROXY` / `go env GOSUMDB`. When HTTP(S) proxy points at host `127.0.0.1` or `localhost`, the tasks rewrite it to `host.docker.internal` so Docker build containers can reach it.

```bash
GOPROXY=https://goproxy.cn,direct task docker:build
GOPROXY=https://goproxy.cn,direct task docker:up
```

If you want to override Docker build behavior without changing the shell-wide Go settings, use the `DOCKER_BUILD_*` variables consistently:

```bash
DOCKER_BUILD_GOPROXY=https://goproxy.cn,direct task docker:build
DOCKER_BUILD_GOPROXY=https://goproxy.cn,direct task docker:up
```

Direct `docker compose build` or `docker compose up --build` can only read exported environment variables. Use `task docker:build` or `task docker:up` when you want the automatic `go env` fallback and host loopback proxy rewrite.

Recommended convention:

- Local development: prefer `DOCKER_BUILD_GOPROXY`; `go env GOPROXY` is also honored when no shell `GOPROXY` is exported
- CI / GitHub Actions: leave them unset and use the public default `https://proxy.golang.org,direct`
- If your network also requires system-level proxy settings, prefer `DOCKER_BUILD_HTTP_PROXY` / `DOCKER_BUILD_HTTPS_PROXY` / `DOCKER_BUILD_NO_PROXY`; regular proxy env vars remain supported as fallback, with host loopback addresses rewritten for Docker build

Default mounts:

- `./config/config.yaml -> /app/config/config.yaml:ro`
- `llm-tracelab-data -> /app/data`
- `postgres-data -> /var/lib/postgresql/data`

The runtime image starts as `root` by default. This avoids common bind-mount permission failures when the host directory owner does not match a fixed in-container UID/GID, such as failing to create `/app/data/traces`.

For external configuration, prefer mounted config files and environment variables for service startup settings such as ports, database, and output directories. Manage channels and models in Monitor Web so they are persisted to the application database. Keep `debug.output_dir` on a stable path inside the mounted data volume.

## Developer Commands

```bash
task fmt
task lint
task test
task build
task run
task migrate
task check
task docker:build
task docker:up
```

## Replay In Unit Tests

```go
func TestChat(t *testing.T) {
    tr := replay.NewTransport("testdata/chat.http")

    cfg := openai.DefaultConfig("fake-key")
    cfg.BaseURL = "http://localhost/v1"
    cfg.HTTPClient = &http.Client{Transport: tr}

    client := openai.NewClientWithConfig(cfg)
    resp, err := client.CreateChatCompletion(context.Background(), req)
    _ = resp
    _ = err
}
```

## Design Rules

- raw `.http` cassettes are the source of truth for replay
- Postgres is the production structured-state path; SQLite is a local fallback and does not replace raw files
- new writes use V3, old V2 files remain readable
- prefer readable files, offline tests, and deployable migration paths
- provider semantics, stream transcripts, usage, and event timelines should converge inside `pkg/llm`

Explicit non-support boundaries:

- public multi-tenant relay, billing, recharge, or subscription distribution: rejected
- cross-protocol translation in the proxy hot path: rejected
- independent Postgres auth migration namespace: audited gap; current Postgres auth shares application `schema_migrations`
- SQLite versioned application migrations: audited fallback; current SQLite remains startup-schema fallback
- real MCP/file/code/computer-use execution lifecycle and root/container-grade executor sandboxing: future secure executor work

## Screenshots

- Monitor overview
  ![](./images/traffic_monitor.png)
- Detail page
  ![](./images/message_detail.png)
- Raw SSE stream
  ![](./images/sse_message_raw.png)
- Non-stream response
  ![](./images/message_raw.png)

## License

[MIT](./LICENSE)
