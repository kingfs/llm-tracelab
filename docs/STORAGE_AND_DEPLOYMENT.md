# 存储与部署

本文说明 llm-tracelab 当前的存储层次与生产部署事实：raw `.http` cassette 是事实源，应用数据库是派生索引；生产使用 Postgres + 版本化迁移，SQLite 仅作本地/dev/test fallback。运维 SQL、备份与恢复细节交给 [Postgres 运维](./POSTGRES_OPERATIONS.md)，本文不重复。

## 存储分层（raw .http cassette 是事实源；应用数据库是派生的索引）

```text
Raw cassette (.http, LLM_PROXY_V3)
  -> trace index (logs)
  -> trace_observations
  -> semantic_nodes / trace_findings
  -> analysis_runs
```

- Raw cassette 保存 raw request、raw response、`# meta:` prelude 和基础 `# event:` timeline，是 replay 与 trace detail 视图的事实源。
- 应用数据库保存结构化状态与索引，只服务于列表、过滤与聚合。数据库不是 replay 依赖：`pkg/replay` 在没有任何数据库的情况下也能回放 cassette。
- 需要扩展时优先扩展 `# event:` 与 `# meta:`，不要破坏已有 parser；reader 必须继续支持 legacy `LLM_PROXY_V2`（固定 2KB JSON header），writer 只写 V3。
- 所有 schema 变更保持 additive。派生表可以清空重建；不会自动重写 cassette。

## 应用数据库（生产 Postgres + 版本化迁移；SQLite 仅本地/dev/test fallback 及启动建表）

生产存储是 Postgres（compose 使用 `postgres:17-alpine`）。`database.driver` 接受 `postgres` / `postgresql`，并且必须提供显式 `database.dsn`：对非 SQLite driver，DSN 为空时 `DatabaseDSN()` 返回空串，迁移会直接报 `postgres application database dsn is required`。

- 版本化迁移 SQL 签入在 `ent/postgres-migrations/`，由 `internal/appdbmigrate` 通过 `golang-migrate` 应用，版本与 dirty 状态记录在 `schema_migrations`。该目录覆盖 trace index、routing/channel/model、Responses 状态、audit/correlation、observation/finding、analysis 与 system events 等表。
- Postgres 生成新迁移使用 ent 生成器（需要临时开发库，禁止生产 DSN）：

  ```bash
  go run -mod=mod ent/migrate/main.go \
    --dialect postgres \
    --dir ent/postgres-migrations \
    --dev-url 'postgres://user:pass@localhost:5432/llm_tracelab_migrate_dev?sslmode=disable' \
    <migration_name>
  go run -mod=mod ent/migrate/update_hash.go ent/postgres-migrations
  ```

  生成后必须复核 SQL，并同时提交迁移文件与 `atlas.sum`；已提交或在共享环境应用过的迁移文件不要手改。`task migrate:ent:postgres NAME=... DEV_URL=...` 是同一流程的封装。
- SQLite 只用于本地、dev、test 与 replay-safe fallback，默认文件为 `{{output_dir}}/llm_tracelab.sqlite3`。SQLite schema 在启动时用 raw DDL 建立，不是版本化迁移；`db migrate status` 会报告 `sqlite_schema_strategy: startup_schema_fallback` 与 `sqlite_versioned_migration_status: not_implemented`。
- SQLite 启动建表会写 `app_schema_status`（namespace `application`）标记；缺少该标记但必需表齐全的旧库仍被视作兼容的 legacy startup-schema 库。
- `internal/store.NewWithDatabase` 是兼容构造器，默认 `AutoMigrate: true`。Postgres 下 `serve` 与命令路径改用 `NewWithDatabaseOptions(..., AutoMigrate:false)`，在显式迁移之后才打开 store；SQLite 没有版本化迁移，`db migrate up` 走 `initializeApplicationDatabase` → `NewWithDatabase`（即 `AutoMigrate: true`）来触发启动建表。

## 命令归属（哪个命令负责迁移、哪个负责 serve、auto_migrate 语义）

| 命令 | 负责范围 | 关键行为 |
| --- | --- | --- |
| `serve` | 启动 proxy、Monitor、MCP、recorder 与本地 Responses runtime | 先按 `auto_migrate` 跑应用迁移，再以 `AutoMigrate:false` 打开 store；随后启动 in-process 解析 worker 与分析 worker |
| `db migrate up` | 应用数据库 schema | Postgres 应用 `ent/postgres-migrations` 里的签入 SQL 并记录 `schema_migrations`；SQLite 走启动建表路径。支持 `--step N`、`--dry-run` |
| `db migrate down` | 应用数据库回滚 | 非 `--dry-run` 时明确不支持，以 usage 错误码退出（CLI 退出码 `3`；内部 helper 返回 `2`，由 CLI 统一映射为 `3`）；生产约定是前向迁移 + 备份或经过评审的手工回滚方案 |
| `db migrate status` | 迁移可见性 | 默认只读配置（不连库）；加 `--check-db` 才读库：Postgres 读 `schema_migrations` 的 version/dirty，SQLite 以只读方式报告 `app_schema_status` 与必需表是否齐全 |
| `db migrate optimize-indexes` | Postgres 索引优化 | 仅 Postgres 生效，应用非事务性 `CREATE INDEX CONCURRENTLY`；支持 `--dry-run`，SQLite 报 not applicable |
| `db summary rebuild sessions` | 派生汇总重建 | 从 logs 重建 `session_summaries`；支持 `--session-id`、`--dry-run` |
| `db secret status` / `export` / `rotate` | 本地 channel secret 加密密钥 | `rotate` 必须显式 `--yes`；`export` 支持 `--out` |
| `auth migrate up` / `down` / `status` | 认证表 | 见下一节 |
| `migrate`（顶层） | cassette 重写与索引重建 | 默认 `--rewrite-v2 --rebuild-index`，支持 `--dry-run`；打开应用库时同样受 `auto_migrate` 影响，不会运行 auth migrator |
| `analyze ...` | 派生数据重算 | 见“派生数据与重算” |

`auto_migrate` 语义（配置项 `database.auto_migrate`，环境变量 `LLM_TRACELAB_DATABASE_AUTO_MIGRATE`；未设置时默认 `true`）：

- `true`：`serve` 与打开应用库的命令在打开 store 之前先跑应用迁移（Postgres 为签入 SQL，SQLite 为启动建表）。Postgres 的 auth 表由同一迁移集拥有，因此 startup 不再单独运行 auth migrator；SQLite 的 auth startup 仍走内嵌 SQLite auth 迁移。
- `false`：schema 必须已经存在。Postgres 下以 `AutoMigrate:false` 打开 store 时会校验应用迁移是否已应用，否则启动失败；`auth init-user` 等命令也要求 auth 表已存在。
- 生产发布建议显式执行一次 `db migrate up`，记录当时的 build 与迁移版本，再启动服务，而不是只依赖进程内自动迁移。

## 认证命名空间

- Postgres 的 auth 表（`users`、`api_tokens`）属于应用数据库的 `schema_migrations` 命名空间，状态字段为 `postgres_auth_namespace_strategy: shared_application_schema_migrations`、`independent_auth_namespace_status: not_implemented`。
- `auth migrate up` 对 Postgres 委托给同一份 `ent/postgres-migrations`，因此与 `db migrate up` 幂等；CLI 不会暗示存在独立的 auth 命名空间。
- `auth migrate down` 在 Postgres 下被阻止（`ErrPostgresAuthRollbackUnsupported`），以 usage 错误码退出（CLI 退出码 `3`）：auth 命令不能回滚应用表。生产回滚同样依赖备份或经过评审的应用迁移方案。
- `auth migrate status --check-db` 是只读检查：Postgres 读共享 `schema_migrations` 并检查 `users`、`api_tokens` 是否存在；SQLite 读配置的 auth 迁移表并检查同样两张表。不带 `--check-db` 时只报告配置。
- 用户与令牌运维命令：`auth init-user --username <u> --password <p>`、`auth reset-password`、`auth create-token --username --name --scope --ttl`（`--scope` 默认 `auth.DefaultTokenScope`，`--ttl 0` 表示不过期）。

## 生产部署（默认拓扑、必需环境变量、迁移与首个用户创建）

默认拓扑（`docker-compose.yml`）：

- `llm-tracelab`：gateway、Monitor、MCP、recorder、本地 Responses runtime。
- `postgres`：应用/认证数据库，保存用户、令牌、trace index、channel/model 状态、Responses 状态与 audit 表；`llm-tracelab` 通过 `depends_on` 等待其 healthcheck 通过。
- `searxng`：可选 hosted `web_search` provider 容器，只在 Compose `search` profile 下启动。
- 卷：`llm-tracelab-data` 挂到 `/app/data`（cassette 与 SQLite fallback 都在这里），另有 `postgres-data`、`searxng-data`。

compose 中 `llm-tracelab` 的启动命令只有 `serve -c /app/config/config.yaml`，**没有** `db migrate up` 步骤；迁移由进程内 `auto_migrate: true` 完成（签入的 `config/config.yaml` 即为该配置）。

必需的环境变量：

```bash
cp .env.example .env
# 至少覆盖数据库口令/DSN
export POSTGRES_PASSWORD='<strong-password>'
export LLM_TRACELAB_DATABASE_DSN='postgres://llm_tracelab:<strong-password>@postgres:5432/llm_tracelab?sslmode=disable'
docker compose up -d
```

- `LLM_TRACELAB_DATABASE_DSN` 在 Postgres 下必填：签入的 `config/config.yaml` 设置了 `database.driver: postgres` 但 `database.dsn: ""`，本地默认值由 compose 注入。
- Postgres 服务本身读取 `POSTGRES_DB` / `POSTGRES_USER` / `POSTGRES_PASSWORD`；宿主端口由 `LLM_TRACELAB_HOST_SERVER_PORT`、`LLM_TRACELAB_HOST_MONITOR_PORT`、`LLM_TRACELAB_POSTGRES_PORT` 控制。
- 首个 provider 可选：设置 `LLM_TRACELAB_BOOTSTRAP_UPSTREAM_BASE_URL` 与 `LLM_TRACELAB_BOOTSTRAP_UPSTREAM_API_KEY` 可导入一个 OpenAI 兼容 provider；两者留空也合法，登录后在 Monitor Web 配置 provider、凭据与模型。legacy `LLM_TRACELAB_UPSTREAM_*` 仍支持单 upstream 迁移，新部署应使用 Web 管理的 provider 数据库。

迁移与首个用户（手动等价路径）：

```bash
docker compose run --rm llm-tracelab -c /app/config/config.yaml db migrate up
docker compose up -d
docker compose exec llm-tracelab /app/bin/llm-tracelab -c /app/config/config.yaml auth init-user --username admin --password 'change-me-123'
```

## 可选组件（SearXNG 等）

```bash
export LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED=true
docker compose --profile search up -d
```

- hosted `web_search` 工具默认启用（compose 中 `LLM_TRACELAB_TOOLS_WEB_SEARCH_ENABLED` 默认 `true`，`config/config.yaml` 中 `tools.web_search.enabled: true`）；`search` profile 只决定 searxng 容器是否运行。
- 应用读取 `tools.web_search.provider=searxng` 与 `tools.web_search.base_url=http://searxng:8080`。`web_search` 与 `web_search_preview` 只有在 provider 已启用且就绪时才在服务端执行；不支持的 hosted tool 会被拒绝并写入审计。
- MCP server 通过 `tools.mcp` 配置；工具面见 [MCP 指南](./MCP_GUIDE.md)。

## 派生数据与重算（哪些从 cassette 重算，哪些是持久化状态）

管道（recording → parse → analysis）：

```text
proxy 捕获字节
  -> cassette writer
  -> trace index (logs)
  -> enqueue parse_job
parse_job -> 读取 cassette -> provider parser
  -> TraceObservation -> semantic_nodes -> enqueue analysis_job
analysis_job -> detectors -> trace_findings（可选 LLM analysis）
```

- 运行时由 `serve` 内的两个 in-process worker 消费 `parse_jobs` 与 `analysis_jobs`（间隔 5s，批量分别为 10 与 5），没有外部队列。
- enqueue parse job 失败不会让请求失败（只记 warn）；cassette 写入失败会向上返回错误，是可观测的。

| 类别 | 内容 | 说明 |
| --- | --- | --- |
| 可由 cassette 重算 | `logs` 索引、`trace_observations`、`semantic_nodes`、`trace_findings`、`analysis_runs`、`parser_versions`、`session_summaries`、`parse_jobs` / `analysis_jobs` 队列状态 | 派生数据，可清空重建；reparse 结果幂等，`semantic_nodes` 可按 `trace_id` 清理重建 |
| 由写入路径增量维护 | `overview_metric_buckets` / `overview_metric_bucket_members` | 随写入按 path 增量更新；`Store.RebuildOverviewMetricBuckets` 已存在但当前没有 CLI 调用者，因此没有等价的命令行重建入口 |
| 持久化状态（非 cassette 可推导） | channel/upstream 配置与模型目录、`app_settings`（如 `channels.initialized`、`routing.settings`）、`users` / `api_tokens`、`responses` / `response_items`、`request_audits` / `execution_events` / `tool_call_audits`、`datasets` / `eval_runs` / `scores` / `experiment_runs`、本地 channel secret 加密密钥 | 需要独立备份 |
| 可由 cassette 回填的索引字段 | `upstream_exchanges` 的 exchange metadata | `analyze backfill-exchanges` 只回填 DB 索引，不重写 cassette；`logs` 只作为推断输入被读取，不会被写入 |

重算命令（均为当前可用子命令）：

```bash
llm-tracelab analyze reparse --trace-id <id>
llm-tracelab analyze scan --trace-id <id>
llm-tracelab analyze reanalyze --trace-id <id>   # 或 --session-id <id>
llm-tracelab analyze repair-usage --trace-id <id> [--rewrite-cassette]
llm-tracelab analyze backfill-exchanges [--dry-run]
llm-tracelab analyze session --session-id <id>
llm-tracelab analyze batch --all --limit 1000    # 或 --trace-id/--request-id/--session-id/过滤器
llm-tracelab analyze refresh --all
llm-tracelab db summary rebuild sessions [--session-id <id>]
```

`analyze batch` 与 `analyze refresh` 支持 `--repair-usage`、`--reparse`、`--scan`、`--enqueue`、`--rewrite-cassette`、`--workers`、`--limit` 以及 `--provider` / `--model` / `--status` / `--observation` 等过滤器。对历史 cassette 的 usage repair 默认只修 DB 指标，只有显式传 `--rewrite-cassette` 才会重写 V3 prelude。

## 数据体积与归档策略

- raw body 不复制进 `semantic_nodes`：该表存 text preview、必要 JSON 与 `raw_ref`；大 blob 留在 cassette 或 sidecar，多模态数据只索引 metadata。
- 应用库只存索引与聚合：`logs` 存路径、长度与指标，`request_audits` 存 `body_preview`/`body_sha256` 等摘要字段，不存完整 body。
- cassette 是唯一事实源，归档或删除 cassette 会同时失去 replay 与 detail 能力；只要 cassette 还在，DB 索引与派生行可以重建（`migrate --rebuild-index`、`analyze refresh`）。
- SQLite fallback DB 默认位于输出目录内，备份时应把 SQLite DB 与 `.http` 目录一起备份；Postgres 备份与归档策略见 [Postgres 运维](./POSTGRES_OPERATIONS.md)。
- `db migrate status --check-db` 对 SQLite 是只读的：不会创建缺失文件、不会修复 drift、不会重写用户数据。手工修复前先备份 SQLite DB 与 `.http` 目录。

## 故障处理

- **cassette 写入失败**：recorder 向上返回错误，不会静默吞掉；此时该请求不会出现在索引中。
- **parse failed**：`parse_jobs.last_error` 记录失败；trace 仍出现在列表里，Monitor 显示 Raw 可用、Protocol 不可用、Audit 可能未运行。用 `analyze reparse --trace-id` 重试。
- **analysis failed**：`analysis_jobs.last_error` 记录失败；Monitor 显示 Protocol 可用、Audit 部分可用。用 `analyze reanalyze` / `analyze scan` 重试。
- **enqueue 失败**：只记录 warning，不影响 trace list 与客户端请求。
- **数据库/迁移不可用**：`auto_migrate: true` 时迁移失败会让 `serve` 直接退出；`AutoMigrate:false` 时 Postgres store 打开会校验必需迁移，schema 不完整同样拒绝启动，而不是带病运行。

## 明确不支持的生产能力

- 公共多租户 relay、计费、订阅或配额转售平台：明确不做。
- proxy 热路径的跨协议转换：不做；非 Responses 流量保持 protocol-aware pass-through + recording。
- 对所有 upstream 的 native Responses 语义介入：未实现；当前本地 Responses runtime 以 OpenAI 兼容 Chat Completions 为后端。
- 独立的 Postgres auth 迁移命名空间：未实现；auth 表仍共享应用迁移集，`auth migrate down` 被阻止。
- SQLite 版本化应用迁移：未实现；SQLite 只作本地启动建表 fallback，生产唯一版本化路径是 Postgres。
- `db migrate down` 与 `auth migrate down` 的回滚：`db migrate down` 非 dry-run 直接拒绝，生产为前向迁移。
- SQLite → Postgres 的自动数据迁移：没有内置工具，必须由运维显式处理。
- `file` / `code` / `computer-use` 的真实 hosted tool 执行生命周期：未实现（MCP hosted tool 执行已实现并接入）。
- `external_command` function executor 的 root/容器级沙箱：未实现；当前只有绝对命令、允许目录、工作目录与 root 拒绝等审计化首版约束。
- 外部队列：未实现；解析与分析使用进程内 worker + 数据库队列表。

## 运维检查清单

```bash
docker compose run --rm llm-tracelab -c /app/config/config.yaml config inspect
docker compose run --rm llm-tracelab -c /app/config/config.yaml db migrate status --check-db
docker compose run --rm llm-tracelab -c /app/config/config.yaml auth migrate status --check-db
docker compose run --rm llm-tracelab -c /app/config/config.yaml doctor --check-db
```

- `doctor --probe-providers` 会显式发起网络探测；默认检查保持保守，不应静默访问 upstream provider。
- 升级流程：先对目标库执行 `db migrate up`，记录 build 与迁移版本，再 `serve`；不要用 `client.Schema.Create` 作为生产发布手段（没有版本历史与回滚路径）。
- 生产运维应把 Postgres auth 命名空间视作与应用共享；需要核对时只用 `auth migrate status --check-db` 做只读检查。
- 相关文档：[架构总览](./ARCHITECTURE.md)、[观测与审计](./OBSERVATION_AND_AUDIT.md)、[Responses 运行时](./RESPONSES_RUNTIME.md)、[Monitor 指南](./MONITOR_GUIDE.md)、[MCP 指南](./MCP_GUIDE.md)、[代理使用示例](./PROXY_USAGE_EXAMPLES.md)、[开发指南](./DEVELOPMENT.md)、[实现状态](./IMPLEMENTATION_STATUS.md)、[协议参考](./protocol-reference/README.md)。
