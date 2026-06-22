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
- `internal/store.Store.initSchema` uses `client.Schema.Create` for Postgres.
  This can create the current ent schema on an empty database, but it is not a
  substitute for versioned production migrations.
- SQLite remains the default for empty driver values and keeps the existing
  local path / `file:` DSN behavior.
- The checked-in `ent/migrations` directory contains SQLite-oriented
  golang-migrate files. `ent/migrate/main.go` generates migration diffs with
  `schema.WithDialect(dialect.SQLite)`.
- `internal/auth/migrate.go` embeds `ent/migrations` and wires only the SQLite
  golang-migrate driver. For any non-SQLite driver, it returns a clear
  unsupported-driver error.
- `cmd/server/db.go` now has an application-owned `db migrate` command. `db
  migrate up` initializes the application schema through
  `internal/store.NewWithDatabase`, so it can exercise the existing SQLite
  fallback and Postgres ent `Schema.Create` path. This is still not a
  versioned production migrator.
- `db migrate down` is intentionally unsupported outside `--dry-run`; ent auto
  migration does not provide a safe rollback plan.
- The Responses runtime has ent-backed persistence for `responses` and
  `response_items`. These tables belong to the application database, not to the
  auth migration domain.

## Command Ownership

Stage 6 splits migration ownership explicitly:

| Command | Intended owner | SQLite status | Postgres status |
| --- | --- | --- | --- |
| `db migrate up|down` | Application database: trace index, routing/channel/model data, Responses state, and future audit tables | `up` uses current application schema initialization; `down` is unsupported except dry-run | `up` can initialize the current ent schema with `Schema.Create`; versioned Postgres migrations remain incomplete |
| `auth migrate up|down` | Auth database: users, tokens, auth-owned schema | Currently supported through embedded SQLite migrations | Separate future gap; not covered by the Stage 6A application migration slice |

`db migrate` is no longer an alias for the auth migrator. Operators should
still treat it as an initialization command for disposable or controlled
evaluation databases, not as the final production Postgres migration workflow.

## Production Deployment Guidance

For SQLite/local development:

- Keep `database.driver` empty or set it to `sqlite`.
- `database.auto_migrate: true` remains appropriate for local startup because
  the embedded migrator is SQLite-only.
- Existing `.http` cassette replay remains independent of the database.

For Postgres evaluation:

- Use an explicit `database.driver: postgres` or `postgresql` and a full
  `database.dsn`.
- `db migrate up` can initialize the current application schema using the ent
  `Schema.Create` path. Use this only for disposable evaluation databases or
  controlled trials where recreating the database is acceptable.
- Do not run `auth migrate` expecting Postgres migrations; it still uses the
  SQLite-only embedded migrator.
- Do not treat `client.Schema.Create` as a production rollout mechanism. It may
  be useful for disposable evaluation databases, but it has no version history,
  downgrade path, reviewable SQL, or drift policy.
- If `database.auto_migrate: true`, server startup currently fails before the
  stores open because `openAuthStore` calls the SQLite-only auth migrator. This
  failure is intentional and prevents silent partial migration.
- If `database.auto_migrate: false`, the database schema must already exist.
  Auth commands such as `auth init-user` also require auth tables to exist; the
  current Postgres auth path is not yet a documented production path.

For production-like Postgres trials, use a fresh database, capture the exact
schema creation method outside TraceLab, and be prepared to recreate the
database. Do not point this cut at a long-lived production database that needs
auditable upgrades and rollbacks.

## Stage 6 Target

Stage 6 should make the application database Postgres-first without removing
SQLite fallback or breaking replay:

- Add a real application migrator behind `db migrate`.
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
   versioned application migrator.
4. Keep SQLite application migrations working for local fallback and tests.
5. Add DSN-gated Postgres integration tests for clean migrate-up, idempotent
   no-change behavior, and a small Responses persistence round trip.
6. Audit raw SQL in `internal/store` for placeholder syntax, SQLite functions,
   partial index behavior, time encoding, and transaction assumptions before
   declaring Postgres runtime support complete.
7. Define a separate SQLite-to-Postgres data migration/export plan for existing
   installations. This should be explicit operator tooling, not an implicit
   startup side effect.

## Remaining Gaps

- `db migrate up` is application-owned but still uses ent auto schema creation,
  not checked-in versioned migrations.
- Postgres application migration files are not checked in.
- Postgres auth migrations are not implemented.
- Request audit, execution events, upstream exchange correlation, and Monitor /
  MCP semantic diagnostics are still incomplete.
- Existing raw SQL paths may still contain SQLite-specific assumptions.
- There is no automatic SQLite-to-Postgres data migration.
