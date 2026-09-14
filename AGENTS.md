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
- CLI command files: `cmd/server/root.go` wires `serve.go`, `migrate.go`, `db.go`, `config.go`, `doctor.go`, `provider.go`, `models.go`, `tools.go`, `audit.go`, `auth.go`, `analyze.go`, `version.go`, `schema.go`, `completion.go` (`provider_startup_probe.go` holds provider-probe helpers)
- Management HTTP/MCP wiring shared by serve and tests: `cmd/server/management.go`
- Reverse proxy: `internal/proxy`
- Recording pipeline: `internal/recorder`
- Application store and metadata index: `internal/store`
- Upstream resolution and capability/protocol-family rules: `internal/upstream`
- Channel (provider) config and probe services: `internal/channel`
- Local Responses runtime, HTTP surface, chat client, and audit queries: `internal/responses` (subpackages `runtime`, `httpapi`, `chatclient`, `audit`, `functionexec`, `tools`, `protocol`, `codexfixtures`)
- Postgres application migrations: `internal/appdbmigrate`
- Monitor UI: `internal/monitor`
- Replay transport for tests: `pkg/replay`
- Shared record format parser: `pkg/recordfile`
- Cross-provider request/response normalization helpers: `pkg/llm`

## Storage

Structured state (trace index, sessions, channel/provider config, upstream targets, observations, findings, analysis jobs, Responses state, and audit tables) lives in the application database.

- Production and the tracked default config use Postgres; the checked-in SQL migrations live in `ent/postgres-migrations/`.
- SQLite is a local/dev/test fallback only, with default file `{{output_dir}}/llm_tracelab.sqlite3`; SQLite schema is applied at startup rather than by versioned migrations.
- Raw `.http` cassettes remain the source of truth for replay and detail views; the database is a derived index for lists, filters, and aggregates.
- YAML channel configuration is a first-bootstrap input only. The first database write stores the application-database `app_settings` key `channels.initialized`; afterwards the database owns routing configuration even when every channel was disabled or deleted. `GET /api/settings/channels` reports the marker and `DELETE /api/settings/channels` clears it, which only re-opens the YAML bootstrap while the database still has no channels. A YAML config with an explicit `credentials` list stays YAML-managed and rejects Monitor channel/model/alias writes with 409.
- All management writes (channels, models, aliases, provider setup and probe apply) run in one `store.ConfigurationTransaction`, which holds the process-wide configuration lock, the upstream write lock, and one SQL transaction; runtime routing is published only after the commit succeeds. Background upstream refresh persists through the same upstream write lock on a best-effort basis.

Current protocol families are documented in `docs/protocol-reference/implemented-protocols.md`.
The proxy is protocol-aware pass-through plus recording/parsing; it does not currently translate requests between OpenAI, Anthropic, Gemini, and Vertex protocol families in the forwarding hot path.
The single exception is `/v1/responses`: the proxy accepts `/v1/chat/completions`, `/v1/responses` and `/v1/messages` unconditionally, and per-request routing prefers a matching native Responses upstream (pass-through) before falling back to the local Responses runtime, which orchestrates the request as an internal upstream `/v1/chat/completions` call. That local execution mode is always available and has no configuration switch; the legacy `responses_server.enabled` field and `LLM_TRACELAB_RESPONSES_ENABLED` variable were removed. To opt out of local translation, set the application-database `app_settings` key `routing.settings` to `{"responses_strategy":"native_only"}` from the Monitor Routing settings (`PATCH /api/settings/routing`); this is not a YAML key. The local Responses runtime is built lazily, so optional provider configuration must never block startup. The native-vs-local choice is resolved per model, not per channel: an explicit `channel_models.supports_responses` / `supports_chat_completions` value (editable in the monitor UI) overrides the channel-level `api_type`/`capabilities`, and models without a declared value fall back to the channel-level behaviour. `upstream.model_capabilities` expresses the same override in YAML.

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
- The application database is the source for monitor list/statistics; raw `.http` files remain the source of truth for replay and detail views.

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
- See `docs/DEVELOPMENT.md` for the command matrix humans and AI agents should use.

## When Changing Storage Or Format

- Update `pkg/recordfile` first, then adapt recorder, monitor, and replay together.
- Keep V2 read compatibility unless the task explicitly allows a breaking change.
- If schema changes in `internal/store`, ensure startup initialization still works on an existing database (Postgres via `internal/appdbmigrate`, SQLite via startup schema).
- Prefer indexing metadata in the application database rather than rescanning every file for aggregate stats.

## Documentation Targets

All documentation under `docs/` is written in Chinese and describes current code facts only. Plans, roadmaps, phase designs, and archives are not kept in this repository.

- `README.md` and `README_EN.md`: human-facing overview and quick start
- `AGENTS.md`: AI-oriented project map and invariants
- `docs/README.md`: 中文文档总入口，列出事实源文档、操作指南、开发文档与协议参考
- `docs/IMPLEMENTATION_STATUS.md`: 当前已实现与未实现能力的事实基线
- `docs/ARCHITECTURE.md`: 代码地图、数据流、存储边界、并发一致性与测试基线
- `docs/PROTOCOLS_AND_PROVIDERS.md`: 协议族、请求入口、provider preset 与能力声明
- `docs/ROUTING_AND_CREDENTIALS.md`: 配置来源与所有权、路由选择、凭据与 limit scope
- `docs/RESPONSES_RUNTIME.md`: 本地 Responses runtime、Codex 兼容面、hosted tools
- `docs/OBSERVATION_AND_AUDIT.md`: 语义解析管道、Observation IR、findings 与重分析
- `docs/STORAGE_AND_DEPLOYMENT.md`: 存储分层、迁移命令、生产部署与派生数据重算
- `docs/MONITOR_GUIDE.md`: Monitor 当前用户可见能力与操作流程
- `docs/MCP_GUIDE.md`: 当前 MCP 工具面与使用方式
- `docs/PROXY_USAGE_EXAMPLES.md`: 把 SDK 与 CLI 接到代理上的示例
- `docs/POSTGRES_OPERATIONS.md`: Postgres 长期运行的基线采集、索引、调优与排障
- `docs/DEVELOPMENT.md`: 测试、lint、构建、基准与依赖的稳定命令入口
- `docs/protocol-reference/README.md`: 协议参考入口、已实现协议矩阵、协议差异与带日期的上游 schema 快照
- add focused docs under `docs/` only when they clarify architecture or storage decisions
