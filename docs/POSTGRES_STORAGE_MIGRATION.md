# Postgres Storage Migration

Date: 2026-06-23

This note records the Stage 6A migration strategy for Postgres-first
persistence. It is a productionization plan and current-state boundary, not a
claim that Postgres persistence is fully production mature today.

## Current State

- `internal/store.NewWithDatabase` accepts `driver=postgres` and
  `driver=postgresql`, requires an explicit DSN, and binds the application trace
  store ent client with `dialect.Postgres`.
- `internal/auth.OpenDatabase` also accepts `driver=postgres` and
  `driver=postgresql`. For Postgres, `auth migrate up` now applies the
  checked-in `ent/postgres-migrations` SQL through `internal/appdbmigrate`
  because the current ent/Postgres schema includes both application and auth
  tables.
- With `database.auto_migrate: true`, the auth store runs the explicit auth
  migrator before opening the store. SQLite stays on the embedded SQLite
  migration path; Postgres uses the versioned Postgres SQL path.
- `internal/store.NewWithDatabase` remains the compatibility constructor and
  still initializes schema by default. Command/server paths use
  `NewWithDatabaseOptions(..., AutoMigrate:false)` after running the explicit
  application migrator, so application store startup no longer relies on
  Postgres `Schema.Create`.
- SQLite remains the default for empty driver values and keeps the existing
  local path / `file:` DSN behavior.
- The checked-in `ent/migrations` directory contains SQLite-oriented
  golang-migrate files. The checked-in `ent/postgres-migrations` directory now
  contains versioned Postgres application schema generated from
  `ent/schema/**`, including Responses state, audit/correlation tables, trace
  index tables, observation/finding tables, analysis tables, and system events.
  It also includes `tool_call_audits` for durable hosted tool lifecycle read
  models; runtime web_search/function executor writes, unsupported hosted tool
  rejected writes, and CLI/Monitor/MCP query surfaces are wired.
  `ent/migrate/main.go` is a dialect-aware generator: SQLite remains the
  default and writes to `ent/migrations`; Postgres requires an explicit Atlas
  dev URL and writes to `ent/postgres-migrations`.
- `internal/auth/migrate.go` embeds `ent/migrations` for SQLite and delegates
  Postgres `up` and `down` migrations to `internal/appdbmigrate`. The Postgres
  rollback path is operationally constrained by the shared application/auth
  migration namespace.
- `auth migrate status` and `auth migrate up/down --dry-run` report auth
  migration source, namespace, scope, and whether Postgres is using the shared
  application migration namespace. The machine-readable report also includes
  the auth-owned required table set (`users`, `api_tokens`), table check
  results, `postgres_auth_namespace_strategy:
  shared_application_schema_migrations`, and
  `independent_auth_namespace_status: not_implemented` for Postgres. It now
  also carries design-only adoption fields:
  `auth_namespace_adoption_status: design_required_not_implemented`,
  `auth_namespace_adoption_plan`, `auth_namespace_dry_run_semantics`,
  `auth_namespace_status_semantics`, `auth_namespace_rollback_scope:
  shared_application_migration_set`, and `auth_namespace_test_gate`.
  `auth migrate status --check-db` is an explicit opt-in read-only check:
  Postgres reads the shared `schema_migrations` state and checks the auth-owned
  tables in the current schema, while SQLite reads the configured auth migration
  table and checks the same auth-owned tables.
- `cmd/server/db.go` now has an application-owned `db migrate` command.
  Postgres `db migrate up` applies checked-in SQL from
  `ent/postgres-migrations` through `internal/appdbmigrate` and
  `golang-migrate`; SQLite `db migrate up` continues to use the existing store
  schema initialization path.
- `db migrate status` and `db migrate up/down --dry-run` report the configured
  application migration source without mutating the database: Postgres reports
  `ent/postgres-migrations` checked-in SQL, SQLite reports the startup schema
  fallback with `sqlite_schema_strategy: startup_schema_fallback`,
  `sqlite_versioned_migration_status: not_implemented`, and operational advice
  to use Postgres for versioned production migrations. Auth migrations are
  explicitly marked out of scope for the `db migrate` command. `db migrate
  status --check-db` is an explicit opt-in database check: Postgres reads
  `schema_migrations` version/dirty state, while SQLite opens the application DB
  read-only and reports the lightweight `app_schema_status` marker plus
  required application table presence when available. Missing SQLite DB files
  are not created by status checks. Legacy SQLite DBs without the marker remain
  valid and are reported as compatible startup-schema fallback.
- `internal/store` has an initial Postgres raw SQL compatibility pass:
  store-owned `?` placeholders are rebound to `$n` for Postgres, transaction
  helpers use the same rebind path, `logs.is_stream` can round-trip as a
  Postgres boolean, and migrated logs/observation/finding/analysis/system-event
  paths are covered by `LLM_TRACELAB_TEST_POSTGRES_DSN` integration tests. A
  representative eval path now also round-trips dataset examples, eval runs,
  score writes, finalize, and score queries against the checked-in Postgres
  migration. Representative Monitor/session/analytics paths now cover Postgres
  compatible session provider aggregation, boolean stream counts, overview
  summary stream/session counts, overview finding categories, high-risk
  findings, analysis summaries, observation summaries, recent parse failures,
  upstream/routing analytics, model catalog/detail analytics, and channel usage
  summary/trends/model usage/recent failures.
- `db migrate down` is intentionally unsupported outside `--dry-run`; ent auto
  migration does not provide a safe rollback plan.
- The Responses runtime has ent-backed persistence for `responses` and
  `response_items`. Request audit, execution events, upstream exchange
  correlation, and `tool_call_audits` also belong to the application database,
  not to the auth migration domain.

## Command Ownership

Stage 6 splits migration ownership explicitly:

| Command | Intended owner | SQLite status | Postgres status |
| --- | --- | --- | --- |
| `db migrate up|down` | Application database: trace index, routing/channel/model data, Responses state, and future audit tables | `up` uses current application schema initialization; `down` is unsupported except dry-run | `up` applies checked-in SQL from `ent/postgres-migrations` via `golang-migrate`; `down` is unsupported except dry-run |
| `auth migrate up|down|status` | Auth database: users, tokens, auth-owned schema | `up` and `down` use embedded SQLite migrations; `status --check-db` reads the auth migration table | `up` applies the shared checked-in `ent/postgres-migrations` SQL; `down` rolls back the same shared migration set; `status --check-db` reads the shared `schema_migrations` table and reports the shared namespace |

`db migrate` is no longer an alias for the auth migrator. Postgres `db migrate
up` is now the versioned application migration path, and command/server store
opening has an explicit no-auto-migrate mode. Production operators should still
account for the shared application/auth Postgres migration namespace, shared
application/auth rollback semantics, missing independent auth namespace, and
remaining runtime SQL compatibility gaps before declaring the whole Postgres
deployment model mature.
The top-level `migrate` command remains a cassette rewrite/index rebuild
workflow; when `database.auto_migrate` is enabled, it initializes the
application database schema and does not run the auth migrator.

## Production Deployment Guidance

For SQLite/local development:

- Keep `database.driver` empty or set it to `sqlite`.
- `database.auto_migrate: true` remains appropriate for local startup because
  the embedded migrator is SQLite-only.
- Existing `.http` cassette replay remains independent of the database.
- Treat SQLite as the long-term local-first fallback for this storage line, not
  as a production versioned migration target. The application schema is still
  initialized through startup raw DDL, and `db migrate status` exposes
  `sqlite_schema_strategy: startup_schema_fallback` plus
  `sqlite_versioned_migration_status: not_implemented`.
- `db migrate status --check-db` is read-only for SQLite. It can explain the
  `app_schema_status` marker and required-table presence, but it does not
  create missing files, repair destructive drift, rebuild cassettes, or rewrite
  user data. Operators should back up the SQLite DB and `.http` directory
  before any manual repair.
- A future SQLite versioned migrator would be a separate compatibility project:
  it must adopt legacy startup-schema databases without data loss, preserve
  replay, support offline tests, and document rollback/repair behavior before
  replacing the current fallback. Until then, Postgres is the only versioned
  migration path for production-like deployments.

For Postgres evaluation:

- Use an explicit `database.driver: postgres` or `postgresql` and a full
  `database.dsn`.
- `ent/postgres-migrations` contains reviewable initial SQL for the current
  application schema. This migration has been generated from ent and verified
  against a fresh Postgres 17 development database.
- `db migrate up` applies the checked-in `ent/postgres-migrations` SQL and
  records progress in `schema_migrations`. It has been manually verified
  against a fresh Postgres 17 development database and covered by a
  DSN-gated integration test.
- `database.auto_migrate: true` runs the application migrator before opening
  the trace store. For Postgres this means checked-in SQL; for SQLite this means
  the existing schema initialization compatibility path.
- `auth migrate up` also applies the checked-in `ent/postgres-migrations` SQL.
  This is currently idempotent with `db migrate up` because both use the same
  `schema_migrations` table and migration directory.
- Do not treat `client.Schema.Create` as a production rollout mechanism. It may
  be useful for disposable evaluation databases, but it has no version history,
  downgrade path, reviewable SQL, or drift policy.
- If `database.auto_migrate: false`, the database schema must already exist.
  Auth commands such as `auth init-user` also require auth tables to exist.
  Postgres auth `up` and `down` are versioned through the shared migration set,
  but a separately owned auth migration namespace is still incomplete.
- Treat the current Postgres auth namespace as shared with the application
  namespace. `auth migrate status` and dry-run output intentionally report
  `postgres_auth_namespace_strategy: shared_application_schema_migrations` and
  `independent_auth_namespace_status: not_implemented`; those fields are the
  operator contract until an independent auth namespace is implemented. The
  companion adoption fields are also report-only: they describe the required
  future behavior and must not be interpreted as an implemented namespace split.

For production-like Postgres trials, run `db migrate up` against a fresh
database first, capture the exact llm-tracelab build and migration version, and
then start the service with application store auto schema creation disabled by
construction. Treat the shared application/auth migration namespace as a known
operational constraint until auth migrations are split or formally documented as
part of the application schema.

## Finalized Storage Boundary

The Storage line is now defined by two explicit decisions:

1. SQLite application storage remains a startup-schema fallback for local-first
   use, tests, and replay-compatible development. It is not being promoted to a
   versioned application migrator in this line. Completion means the fallback is
   visible, read-only status checks are non-destructive, legacy DBs remain
   compatible, and operator advice clearly says to use Postgres for versioned
   production migrations.
2. Postgres auth storage remains in the shared application
   `schema_migrations` namespace for the current productionization boundary.
   The code and CLI must not imply that a separate auth namespace exists. The
   next step is design-gated migration work, not an opportunistic namespace
   split.

An independent Postgres auth namespace can only be implemented behind these
phase gates:

- Add a separate checked-in auth migration source and namespace marker, with
  dry-run/status fields that show source path, namespace, migration version,
  dirty state, shared-vs-independent mode, and auth-owned table health.
- Provide an adoption path for existing deployments where `users` and
  `api_tokens` were created by the shared application migration. Adoption must
  be idempotent and must not roll back or drop application tables.
- Keep `auth migrate status --check-db` read-only and able to report both the
  legacy shared namespace and the new independent namespace during transition.
- Make rollback semantics explicit: shared namespace rollback remains an
  application-wide operation; independent auth rollback must only affect
  auth-owned migrations.
- Cover the transition with offline SQLite tests and DSN-gated Postgres tests.
  Default tests must not require a real Postgres server.

Until those gates are met, production operators should run `db migrate up` as
the canonical Postgres schema setup step and use `auth migrate status
--check-db` only to verify auth-owned table presence and the shared namespace
state.

## Independent Auth Namespace Adoption Design

This design does not change current behavior. Postgres auth migrations still
delegate to `internal/appdbmigrate` and use the application
`ent/postgres-migrations` source plus the shared `schema_migrations` table.

Future independent auth namespace adoption must be explicit and idempotent:

- Detect the current shared state by reading application `schema_migrations`
  and checking the auth-owned required tables (`users`, `api_tokens`) in the
  current schema.
- Refuse adoption if required auth tables are missing, if the shared namespace
  is dirty, or if the target independent auth namespace marker is dirty.
- Initialize only the future auth namespace marker/version state for migrations
  whose effects are already present. Adoption must not create, drop, rewrite,
  or roll back application tables.
- Preserve existing auth data. Adoption is metadata-only unless a future auth
  migration introduces an additive auth-owned table/index that is not already
  present.
- Be safe to rerun: an already adopted database should return an adopted/no-op
  status rather than attempting duplicate DDL.

Dry-run and status semantics:

- `auth migrate up --dry-run` must remain mutation-free. In a future adoption
  implementation it should report whether adoption would be needed, blocked, or
  already complete, plus the shared version, independent auth version, dirty
  flags, and auth table health.
- `auth migrate status` without `--check-db` remains configuration-only.
- `auth migrate status --check-db` remains read-only. During transition it must
  report both namespace states: the legacy shared application
  `schema_migrations` state and the independent auth namespace marker state.
- Output fields should stay additive. Existing fields such as
  `postgres_auth_namespace_strategy` and
  `independent_auth_namespace_status` must not change meaning silently.

Rollback semantics:

- Current Postgres rollback scope is
  `auth_namespace_rollback_scope: shared_application_migration_set`; running
  `auth migrate down` rolls back the shared application/auth migration set and
  is therefore application-wide.
- After a real split, independent auth rollback must only apply migrations from
  the auth-owned migration source and namespace. It must not roll back
  application migrations or drop application tables.
- Adoption itself must not provide a rollback that deletes shared application
  migration history. The safe rollback for a failed adoption attempt is to
  remove or repair only the new auth namespace marker before any new
  auth-owned DDL is applied.

Testing gate:

- Default tests must stay offline and must not require network or a real
  Postgres server.
- Unit tests should cover the report/status/dry-run helper semantics with
  SQLite or config-only Postgres reports.
- Any real Postgres adoption/status checks must be gated by an explicit DSN
  environment variable, follow the existing `LLM_TRACELAB_TEST_POSTGRES_DSN`
  pattern, and use disposable schemas/databases.
- Tests must verify idempotent adoption, dirty shared namespace refusal,
  missing auth table refusal, read-only status behavior, and rollback scope
  boundaries before any namespace split is shipped.

## Stage 6 Target

Stage 6 should make the application database Postgres-first without removing
SQLite fallback or breaking replay:

- Add a real application migrator behind `db migrate`. Postgres `up` is now
  wired to checked-in SQL; SQLite remains on the schema initialization path.
- Support Postgres application migrations from explicit, versioned files.
  SQLite versioned application migrations are intentionally deferred; the
  accepted SQLite target is documented startup-schema fallback with read-only
  status reporting.
- Keep Responses state (`responses`, `response_items`) in the application
  migration domain.
- Add future application tables for request audit, execution events, and
  upstream exchange correlation to the application migration domain.
- Keep `auth migrate` scoped to users/tokens at the command level. Its
  Postgres `up` path currently reuses the shared checked-in ent/Postgres SQL;
  a separate auth migration namespace remains a design-gated follow-up.
- Preserve `.http` cassette compatibility: `pkg/replay` must not require
  Postgres or any semantic database.

## Versioned Postgres Migration Route

The production route should be additive and reviewable:

1. Define separate migration directories or dialect-aware generation for
   application Postgres migrations. SQLite application storage remains on the
   startup-schema fallback unless a future compatibility project explicitly
   introduces versioned SQLite adoption.
2. Generate Postgres SQL from ent schema changes, review it, and check it in
   as versioned migration files. Do not depend on runtime `Schema.Create` for
   production.
3. Replace the current `db migrate up` ent auto-schema implementation with a
   versioned application migrator. This is done for Postgres `up`; SQLite still
   uses the compatibility initialization path.
4. Keep SQLite application migrations working for local fallback and tests.
5. Add DSN-gated Postgres integration tests for clean migrate-up, idempotent
   no-change behavior, core store runtime paths, and a small Responses
   persistence round trip. These now exist under
   `LLM_TRACELAB_TEST_POSTGRES_DSN`, including an ent-backed Responses runtime
   store round trip, representative eval dataset list/detail/example/run/score
   round trips, and representative experiment run read models through checked-in
   Postgres migrations. Representative monitor runtime SQL tests also cover
   session list/detail, overview summary aggregation, overview finding/analysis/
   observation subpanels, upstream/routing analytics, model catalog/detail
   analytics, and channel usage analytics.
6. Audit raw SQL in `internal/store` for placeholder syntax, SQLite functions,
   partial index behavior, time encoding, and transaction assumptions before
   declaring Postgres runtime support complete. The first pass covers
   placeholder rebinding and migrated logs/observation/finding/analysis/system
   event paths plus representative eval dataset list/detail/example/run/score,
   experiment, monitor
   session/overview, upstream/routing, model catalog/detail, and channel usage
   paths; deeper analytics queries still need ongoing Postgres audit as new
   query surfaces are added.
7. Define a separate SQLite-to-Postgres data migration/export plan for existing
   installations. This should be explicit operator tooling, not an implicit
   startup side effect.

## Stage 7B/16A Generation Entry

Stage 7B added the minimum CLI surface needed for dialect-aware migration
generation. Stage 16A enables real Postgres SQL generation and checks in the
initial `ent/postgres-migrations` directory. It does not change `ent/dao/**` or
the existing SQLite migration files.

Stage 16B wires the checked-in Postgres SQL into runtime CLI migration:
`cmd/server db migrate up` now reports `migration_mode: versioned-sql` for
Postgres and applies embedded `ent/postgres-migrations` through
`internal/appdbmigrate`.

Stage 16C separates application store opening from schema creation:
`internal/store.NewWithDatabaseOptions` can open without migration, and
serve/top-level migrate/analyze/db command paths run the explicit application
migrator only when `database.auto_migrate` is enabled before reopening the
store with `AutoMigrate:false`.

Stage 16D adds the first runtime SQL compatibility pass: store raw SQL can
rebind positional placeholders for Postgres, transaction helpers share the same
path, and `logs.is_stream` supports Postgres boolean round trips.

Stage 16D follow-up coverage fixes the representative Monitor session raw SQL
path: SQLite `GROUP_CONCAT` session provider aggregation now has a Postgres
`string_agg` equivalent, and session/overview stream counts use a
dialect-appropriate boolean expression. The coverage is DSN-gated by
`LLM_TRACELAB_TEST_POSTGRES_DSN` and skips by default.

Stage 16E promotes the remaining `internal/store` application raw DDL tables
into ent/Postgres migrations: trace observations, semantic nodes, trace
findings, analysis runs/jobs, parse jobs, parser versions, and system events.
The Postgres migration table set now covers the SQLite application raw DDL table
set; auth tables still need an independently owned migration namespace.

Stage 16F routes Postgres auth migration `up` through the same versioned SQL
path. `database.auto_migrate=true` no longer calls auth `Schema.Create` for
Postgres startup; it runs `auth.MigrateDatabaseUp`, which delegates to
`internal/appdbmigrate`. The shared migration set includes the auth tables and
is idempotent with `db migrate up`.

Stage 16G adds configuration-level migration reporting for application DB
operability. `db migrate status` and dry-run output now include
`database_namespace`, `migration_source`, `migration_source_path`,
`schema_versioned`, `rollback_supported`, and `auth_migration_scope`. The status
command intentionally does not connect to Postgres or inspect
`schema_migrations`; it makes the selected migration source and command
ownership visible without requiring a running external database.

Stage 16H adds explicit status verification through `db migrate status
--check-db`. This opt-in path reads Postgres `schema_migrations` and reports
`database_migration_version` plus `database_migration_dirty` when present. For
SQLite it reports the existing schema-init fallback by checking the application
DB read-only. Current SQLite startup initialization writes `app_schema_status`
for the `application` namespace, and `--check-db` reports that marker version
alongside required table completeness. Existing SQLite DBs without the marker
are still treated as compatible legacy fallback DBs.

Stage 22 tightens the SQLite application migration boundary without adding a
versioned SQLite migrator. `db migrate status` and `db migrate up --dry-run`
now expose stable SQLite fields in both JSON and text output:
`sqlite_schema_strategy: startup_schema_fallback`,
`sqlite_versioned_migration_status: not_implemented`, and
`sqlite_migration_advice`. `db migrate status --check-db` remains read-only for
SQLite, does not create a missing DB file, and adds `database_status_advice`
when marker or required-table checks need operator interpretation.

Stage 16I routes Postgres auth migration `down` through the same shared
versioned SQL rollback path in `internal/appdbmigrate`. This closes the
previous Postgres auth rollback command gap, while preserving the explicit
warning that auth and application schemas still share one migration namespace.

Stage 16J adds auth migration status reporting. `auth migrate status` and
`auth migrate up/down --dry-run` now include namespace, source, scope, rollback
support, shared application namespace fields, auth-owned table health fields,
and an explicit independent namespace plan. `auth migrate status --check-db`
reads Postgres `schema_migrations` through the shared application migration
path and reads SQLite auth migration status through the configured auth
database; both dialects check the required auth tables (`users`, `api_tokens`)
read-only. This improves operator visibility but deliberately does not create an
independent Postgres auth migration namespace; Postgres remains
`shared_application_schema_migrations` with independent auth namespace
`not_implemented`.

Stage 16J follow-up adds design-only operator fields for the future independent
auth namespace adoption path. Postgres reports
`auth_namespace_adoption_status: design_required_not_implemented`,
`auth_namespace_dry_run_semantics`, `auth_namespace_status_semantics`,
`auth_namespace_rollback_scope: shared_application_migration_set`, and
`auth_namespace_test_gate`. These are additive output fields, not an
implementation of the split.

Stage 16K adds the durable hosted tool audit read model. `tool_call_audits` is
defined in ent, generated into `ent/dao/**`, included in SQLite startup schema
fallback, covered by a minimal additive SQLite migration, and checked into
`ent/postgres-migrations` with a matching additive Postgres migration. The
table is available through `internal/responses/audit.RecordToolCallAudit` and
`ListToolCallAudits`; runtime now writes hosted `web_search` and configured
function executor started/completed/failed lifecycle into the table, and
unsupported hosted tool choices write rejected records without raw payloads.
`audit tool-calls`, Monitor `/api/responses/audit/tool-calls`, and MCP
`responses_audit_tool_calls` expose query paths with payload summaries by
default. Future MCP/file/code/computer-use runtime execution lifecycle remains
follow-up work.

SQLite compatibility:

```bash
go run -mod=mod ent/migrate/main.go <migration_name>
```

This remains equivalent to:

```bash
go run -mod=mod ent/migrate/main.go \
  --dialect sqlite \
  --dir ent/migrations \
  <migration_name>
```

Future Postgres generation must use a temporary development database, never a
production DSN:

```bash
go run -mod=mod ent/migrate/main.go \
  --dialect postgres \
  --dir ent/postgres-migrations \
  --dev-url 'postgres://user:pass@localhost:5432/llm_tracelab_migrate_dev?sslmode=disable' \
  <migration_name>
```

`LLM_TRACELAB_ENT_MIGRATE_DEV_URL` can supply the dev URL when `--dev-url` is
omitted. The `task` wrapper is:

```bash
task migrate:ent:postgres \
  NAME=<migration_name> \
  DEV_URL='postgres://user:pass@localhost:5432/llm_tracelab_migrate_dev?sslmode=disable'
```

Each generated directory must have its matching checksum refreshed immediately:

```bash
go run -mod=mod ent/migrate/update_hash.go <migration_dir>
```

Review the generated SQL before committing it, commit the migration files and
`atlas.sum` together, and do not hand-edit migration files that have already
been committed or applied in a shared environment.

## Remaining Gaps

- Postgres auth migration `up` and `down` are versioned through the shared
  `ent/postgres-migrations` set, but a separately owned auth migration
  namespace is still missing. Current status/dry-run output documents this as
  `postgres_auth_namespace_strategy: shared_application_schema_migrations` and
  `independent_auth_namespace_status: not_implemented`; the split requires a
  separately versioned auth migration directory/table plan, idempotent adoption
  of existing `users`/`api_tokens` tables, read-only transition status,
  mutation-free dry-run, auth-only rollback boundaries, and DSN-gated Postgres
  tests before command ownership or rollback semantics change.
- SQLite application migrations still use schema initialization rather than
  explicit versioned files. `db migrate status` and dry-run output report
  `sqlite_schema_strategy: startup_schema_fallback` and
  `sqlite_versioned_migration_status: not_implemented`; `db migrate status
  --check-db` reports the lightweight `app_schema_status` marker when present
  while continuing to accept legacy local databases without that marker.
- Request audit, execution events, upstream exchange correlation, Monitor API,
  MCP semantic diagnostics, and Monitor UI trace lookup have a minimal
  Responses path.
- Existing raw SQL paths may still contain SQLite-specific assumptions beyond
  placeholder rebinding and the currently covered monitor/session/overview,
  upstream/routing, model catalog/detail, channel usage, eval, and experiment
  representative paths. New or deeper analytics query surfaces should continue
  to add DSN-gated Postgres coverage.
- There is no automatic SQLite-to-Postgres data migration.
