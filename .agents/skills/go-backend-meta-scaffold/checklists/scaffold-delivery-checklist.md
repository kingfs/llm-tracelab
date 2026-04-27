# Scaffold Delivery Checklist

在用此 skill 生成或重构具体项目后，至少检查以下项目。

## 输入与边界

- 已明确 `module_path`、`service_name`、`proto_package`、`proto_version`
- 已明确 bounded context、核心域/支撑域/通用域
- 已判断哪些用例适合 DDD 聚合，哪些只需要 query service

## 生成链

- `proto` 是对外接口唯一事实来源
- `buf.yaml`、`buf.gen.yaml` 存在且可执行
- `ent/schema`、`ent/dao`、`ent/migrations` 边界清晰
- 首次初始化顺序明确为 `phase1 -> generate:ent -> phase2 -> generate -> migrate`
- migration 流程明确要求“生成 migration 后更新 `atlas.sum`”
- 明确约束“同一个 commit 内多个未提交 migration 需要在提交前合并为一套”
- `wire` 的源文件与生成文件边界清晰
- 文档明确声明不得手改生成代码

## 工程骨架

- 存在 `cmd/`、`internal/`、`proto/`、`ent/`、`docker/`、`tests/`
- 存在 `Taskfile.yml` 或 `Taskfile.yaml`
- 存在 `.gitlab-ci.yml`
- 存在 `.gitignore`、`.dockerignore`、`.env.example`
- 不提交真实 `.env`

## 运行与配置

- CLI 使用 `cobra`
- `cmd/<service>/` 采用 root/sub command 分文件结构
- 配置管理使用 `viper`
- 存在 `serve --config` 或 `-c`
- 存在 `migrate up` 与 `migrate down`
- Docker runtime 满足 `/app/bin` 与 `/app/config` 约定

## 质量保障

- 有单元测试
- 有 e2e 测试
- 说明了 SQLite 与 Postgres 的差异或兼容策略
- `task generate`
- `task lint`
- `task test`
- `task test:e2e`
- `task build`

## 文档

- 存在项目级 `AGENTS.md`
- 存在 `ARCHITECTURE.md`
- 存在 `README.md`
- `AGENTS.md` 明确告诉 AI 应先读哪些文件、不要改哪些文件

## CI/CD

- `.gitlab-ci.yml` stages 顺序正确
- `build` 产物通过 artifacts 传给 `package`
- `package` 不重复 build
- 对 Go 和 Task 相关缓存做了合理配置
- `Taskfile`、CI、脚本、文档没有把 `GOCACHE`、`GOMODCACHE`、`GOPATH` 指到仓库内 `.cache/`、`go/` 或类似项目级缓存目录
