# Postgres Storage Migration

Date: 2026-06-22

This note records the Stage 1D Postgres-first storage migration cut. It is a
small compatibility slice, not a complete Postgres migration.

## Current Cut

- `internal/store.NewWithDatabase` accepts `driver=postgres` and
  `driver=postgresql`.
- Postgres trace storage requires an explicit DSN. SQLite keeps the existing
  default path and `file:` DSN handling.
- The trace store binds ent with `dialect.Postgres` on the Postgres path and
  uses `client.Schema.Create` instead of running the hand-written SQLite DDL.
- `internal/auth.OpenDatabase` accepts `driver=postgres` and
  `driver=postgresql`, requires an explicit DSN, and binds ent with
  `dialect.Postgres`.
- SQLite behavior remains the default for empty driver values.

## Not Yet Supported

- Embedded auth migrations are still SQLite-only. `MigrateDatabaseUp` and
  `MigrateDatabaseDown` intentionally return a clear error for Postgres instead
  of pretending that `auto_migrate` is complete.
- There are no checked-in Postgres golang-migrate files yet.
- There is no automatic SQLite-to-Postgres data migration.
- Postgres integration tests are not part of the default test suite.

## Operational Implication

With `database.driver: postgres` or `postgresql`, the server will fail startup
while `database.auto_migrate` is true because auth migrations are incomplete.
That failure is intentional for this cut. A Postgres deployment must currently
set up schema out of band and disable auto-migrate before using the Postgres
open path.

## Remaining Migration Items

- Add dialect-specific migration generation under `ent/migrate` for Postgres.
- Add Postgres auth migration files and wire a Postgres migrate driver.
- Add DSN-gated integration tests for trace store and auth store.
- Audit raw SQL in `internal/store` for SQLite placeholder and function
  assumptions before declaring runtime Postgres support complete.
- Define an explicit data export/import or dual-write plan for existing SQLite
  installations.

## Risks

- `client.Schema.Create` is useful for the first cut, but it is not a substitute
  for versioned production migrations.
- Existing raw SQL paths may still contain SQLite-specific syntax.
- The trace and auth schemas share one ent client package; future migration work
  must keep schema ownership explicit to avoid accidental cross-domain changes.
