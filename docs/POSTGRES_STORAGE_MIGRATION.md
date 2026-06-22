# Postgres Storage Migration

Date: 2026-06-22

This note records the Stage 6A migration strategy for Postgres-first
persistence. It is a productionization plan and current-state boundary, not a
claim that Postgres persistence is fully production mature today.

## Current State

- `internal/store.NewWithDatabase` accepts `driver=postgres` and
  `driver=postgresql`, requires an explicit DSN, and binds the application trace
  store ent client with `dialect.Postgres`.
- `internal/auth.OpenDatabase` also accepts `driver=postgres` and
  `driver=postgresql`, but auth schema management is not productionized for
  Postgres.
- With `database.auto_migrate: true`, the auth store keeps SQLite on the
  embedded migration path and uses ent `Schema.Create` for Postgres evaluation
  databases.
- `internal/store.NewWithDatabase` remains the compatibility constructor and
  still initializes schema by default. Command/server paths use
  `NewWithDatabaseOptions(..., AutoMigrate:false)` after running the explicit
  application migrator, so application store startup no longer relies on
  Postgres `Schema.Create`.
- SQLite remains the default for empty driver values and keeps the existing
  local path / `file:` DSN behavior.
- The checked-in `ent/migrations` directory contains SQLite-oriented
  golang-migrate files. The checked-in `ent/postgres-migrations` directory now
  contains the initial Postgres application schema generated from
  `ent/schema/**`, including Responses state and audit/correlation tables.
  `ent/migrate/main.go` is a dialect-aware generator: SQLite remains the
  default and writes to `ent/migrations`; Postgres requires an explicit Atlas
  dev URL and writes to `ent/postgres-migrations`.
- `internal/auth/migrate.go` embeds `ent/migrations` and wires only the SQLite
  golang-migrate driver. For any non-SQLite driver, it returns a clear
  unsupported-driver error.
- `cmd/server/db.go` now has an application-owned `db migrate` command.
  Postgres `db migrate up` applies checked-in SQL from
  `ent/postgres-migrations` through `internal/appdbmigrate` and
  `golang-migrate`; SQLite `db migrate up` continues to use the existing store
  schema initialization path.
- `db migrate down` is intentionally unsupported outside `--dry-run`; ent auto
  migration does not provide a safe rollback plan.
- The Responses runtime has ent-backed persistence for `responses` and
  `response_items`. These tables belong to the application database, not to the
  auth migration domain.

## Command Ownership

Stage 6 splits migration ownership explicitly:

| Command | Intended owner | SQLite status | Postgres status |
| --- | --- | --- | --- |
| `db migrate up|down` | Application database: trace index, routing/channel/model data, Responses state, and future audit tables | `up` uses current application schema initialization; `down` is unsupported except dry-run | `up` applies checked-in SQL from `ent/postgres-migrations` via `golang-migrate`; `down` is unsupported except dry-run |
| `auth migrate up|down` | Auth database: users, tokens, auth-owned schema | Currently supported through embedded SQLite migrations | Separate future gap; not covered by the Stage 6A application migration slice |

`db migrate` is no longer an alias for the auth migrator. Postgres `db migrate
up` is now the versioned application migration path, and command/server store
opening has an explicit no-auto-migrate mode. Production operators should still
account for the remaining auth migration and runtime SQL compatibility gaps
before declaring the whole Postgres deployment model mature.
The top-level `migrate` command remains a cassette rewrite/index rebuild
workflow; when `database.auto_migrate` is enabled, it initializes the
application database schema and does not run the auth migrator.

## Production Deployment Guidance

For SQLite/local development:

- Keep `database.driver` empty or set it to `sqlite`.
- `database.auto_migrate: true` remains appropriate for local startup because
  the embedded migrator is SQLite-only.
- Existing `.http` cassette replay remains independent of the database.

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
- Postgres auth schema handling still uses ent `Schema.Create` when
  `database.auto_migrate: true`. This is an evaluation convenience only.
- Do not run `auth migrate` expecting Postgres migrations; it still uses the
  SQLite-only embedded migrator.
- Do not treat `client.Schema.Create` as a production rollout mechanism. It may
  be useful for disposable evaluation databases, but it has no version history,
  downgrade path, reviewable SQL, or drift policy.
- If `database.auto_migrate: false`, the database schema must already exist.
  Auth commands such as `auth init-user` also require auth tables to exist.
  Checked-in versioned Postgres auth migrations are still incomplete, so the
  current Postgres auth path is not yet a documented production path.

For production-like Postgres trials, run `db migrate up` against a fresh
database first, capture the exact llm-tracelab build and migration version, and
then start the service with application store auto schema creation disabled by
construction. Do not point the remaining auth Postgres auto schema path at a
long-lived production database that needs fully auditable upgrades and
rollbacks.

## Stage 6 Target

Stage 6 should make the application database Postgres-first without removing
SQLite fallback or breaking replay:

- Add a real application migrator behind `db migrate`. Postgres `up` is now
  wired to checked-in SQL; SQLite remains on the schema initialization path.
- Support both SQLite and Postgres application migrations from explicit,
  versioned files.
- Keep Responses state (`responses`, `response_items`) in the application
  migration domain.
- Add future application tables for request audit, execution events, and
  upstream exchange correlation to the application migration domain.
- Keep `auth migrate` scoped to users/tokens and document Postgres auth
  migration as a separate follow-up.
- Preserve `.http` cassette compatibility: `pkg/replay` must not require
  Postgres or any semantic database.

## Versioned Postgres Migration Route

The production route should be additive and reviewable:

1. Define separate migration directories or dialect-aware generation for
   application SQLite and application Postgres migrations.
2. Generate Postgres SQL from ent schema changes, review it, and check it in
   as versioned migration files. Do not depend on runtime `Schema.Create` for
   production.
3. Replace the current `db migrate up` ent auto-schema implementation with a
   versioned application migrator. This is done for Postgres `up`; SQLite still
   uses the compatibility initialization path.
4. Keep SQLite application migrations working for local fallback and tests.
5. Add DSN-gated Postgres integration tests for clean migrate-up, idempotent
   no-change behavior, and a small Responses persistence round trip. The
   migrate-up and idempotent no-change portions now exist under
   `LLM_TRACELAB_TEST_POSTGRES_DSN`; the small Responses persistence round trip
   remains.
6. Audit raw SQL in `internal/store` for placeholder syntax, SQLite functions,
   partial index behavior, time encoding, and transaction assumptions before
   declaring Postgres runtime support complete.
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

- Postgres auth migrations are not versioned; auth startup still uses ent auto
  schema creation when `database.auto_migrate` is enabled for Postgres.
- SQLite application migrations still use schema initialization rather than
  explicit versioned files.
- Request audit, execution events, upstream exchange correlation, Monitor API,
  MCP semantic diagnostics, and Monitor UI trace lookup have a minimal
  Responses path.
- Existing raw SQL paths may still contain SQLite-specific assumptions.
- There is no automatic SQLite-to-Postgres data migration.
