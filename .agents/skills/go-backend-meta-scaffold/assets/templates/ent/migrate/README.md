# migrate 操作说明

## 生成 migration

```bash
go run -mod=mod ent/migrate/main.go <migration_name>
```

说明：

- 先完成 `ent/schema/**` 定义，并执行 `go generate ./ent/...` 生成 `ent/dao/**`
- 本命令会拉起一个临时 `postgres:17-alpine` Docker 容器，生成完成后自动清理
- migration 统一生成到 `ent/migrations/`
- 不要手动修改 `ent/migrations/` 下的生成文件
- 生成完成后必须立即更新 `atlas.sum`

## 更新 migration hash

```bash
go run -mod=mod ent/migrate/update_hash.go ent/migrations
```

说明：

- 该命令会扫描 `ent/migrations/` 下所有 `.sql` 文件并重新生成 `atlas.sum`
- 每次新增、删除或重生成本地 migration 后，都要重新执行
- 提交代码前，确认 `ent/migrations/*.sql` 与 `ent/migrations/atlas.sum` 一起进入本次变更

## 推荐提交流程

1. 修改 `ent/schema/**`
2. 执行 `go generate ./ent/...`
3. 执行 `go run -mod=mod ent/migrate/main.go <migration_name>`
4. 执行 `go run -mod=mod ent/migrate/update_hash.go ent/migrations`
5. 自检 `ent/migrations/*.sql` 与 `atlas.sum`

## 多个未提交 migration 的处理

- 同一个 commit 中，如果只是同一轮 schema 开发反复调整，最终应只保留一套 migration
- 如果本地已经生成了多份“尚未提交”的 migration，优先删除这些未提交文件后重新生成一套最终 migration，再执行 `update_hash.go`
- 不要改写已经合入主干、已经发布或已被其他环境依赖的历史 migration
