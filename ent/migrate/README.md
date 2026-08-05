# Ent Migration Workflow

Control-plane database schema is defined in `ent/schema`.

## SQLite Workflow

The default workflow remains SQLite-oriented and writes to `ent/migrations`.
The legacy command is still supported:

1. Update `ent/schema/**`.
2. Run `go generate ./ent/...`.
3. Run `go run -mod=mod ent/migrate/main.go <migration_name>`.
4. Run `go run -mod=mod ent/migrate/update_hash.go ent/migrations`.
5. Commit generated `ent/dao/**`, `ent/migrations/*.sql`, and `ent/migrations/atlas.sum` together.

Equivalent explicit form:

```bash
go run -mod=mod ent/migrate/main.go \
  --dialect sqlite \
  --dir ent/migrations \
  <migration_name>
```

## Postgres Entry Point

`ent/migrate/main.go` also generates checked-in Postgres migration SQL. The
initial Postgres migration directory is `ent/postgres-migrations`.

Use only a temporary development database URL for generation trials. Never use a
production DSN as the Atlas dev database.

```bash
go run -mod=mod ent/migrate/main.go \
  --dialect postgres \
  --dir ent/postgres-migrations \
  --dev-url 'postgres://user:pass@localhost:5432/llm_tracelab_migrate_dev?sslmode=disable' \
  <migration_name>
```

The dev URL can also be supplied with `LLM_TRACELAB_ENT_MIGRATE_DEV_URL`.

Equivalent task entry:

```bash
task migrate:ent:postgres \
  NAME=<migration_name> \
  DEV_URL='postgres://user:pass@localhost:5432/llm_tracelab_migrate_dev?sslmode=disable'
```

The required flow is:

1. Generate SQL from `ent/schema/**` into the dialect-specific migration
   directory.
2. Review the generated SQL before committing it.
3. Run `go run -mod=mod ent/migrate/update_hash.go <migration_dir>` for that
   directory.
4. Commit the SQL files and the matching `atlas.sum` together.

Do not manually edit committed migration files after they have been applied in a
shared environment. If multiple uncommitted migrations are generated during the
same change, regenerate a final reviewed set before commit instead of rewriting
shared history.
