# Ent Migration Workflow

Control-plane database schema is defined in `ent/schema`.

Standard workflow:

1. Update `ent/schema/**`.
2. Run `go generate ./ent/...`.
3. Run `go run -mod=mod ent/migrate/main.go <migration_name>`.
4. Run `go run -mod=mod ent/migrate/update_hash.go ent/migrations`.
5. Commit generated `ent/dao/**`, `ent/migrations/*.sql`, and `ent/migrations/atlas.sum` together.

Do not manually edit generated migration files after they have been applied in a shared environment.
