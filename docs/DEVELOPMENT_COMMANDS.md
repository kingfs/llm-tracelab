# 开发命令

本项目使用稳定的 `task` 入口，让人类和 AI agent 不需要猜测 Go、前端或测试命令。

## 日常命令

```bash
task fmt
task check:quick
task build:go
task run
```

- `task fmt`：格式化 Go 代码。
- `task check:quick`：格式检查、lint、短测试，不改写文件。
- `task build:go`：只构建后端。
- `task run`：用 `config/config.yaml` 启动本地服务。可用 `CONFIG=path/to/config.yaml task run` 指定配置。

## 验证等级

按变更风险选择最小足够验证：

```bash
task fmt:check
task lint
task lint:vet
task test:short
task test
task test:e2e
task test:race
task test:cover
task check:quick
task check:full
```

- 小范围代码改动：`task check:quick`。
- 代理、路由、录制、SQLite 或 streaming 改动：补跑 `task test:race`。
- 端到端行为改动：跑 `task test:e2e`。
- 发布前或大范围改动：`task check:full`。

所有测试都不应依赖真实 provider 网络或 API key。

## 前端命令

```bash
task ui:build
task ui:test
task ui:test:real
```

- `task ui:build`：构建 Monitor UI 并生成 Go embed 产物。
- `task ui:test`：用 mock Monitor API 运行 Playwright 冒烟测试。
- `task ui:test:real`：启动本地 Go Monitor fixture 和本地 fake upstream，验证真实嵌入路由。

前端或 embed 产物改动建议执行：

```bash
task ui:build
task ui:test
task ui:test:real
go test ./internal/monitor
task build:go
```

## 构建命令

```bash
task build:go
task build:all
task build
```

- `task build:go`：适合后端-only 改动。
- `task build` / `task build:all`：会先重建嵌入式 Monitor UI，再编译服务端。

## 依赖命令

```bash
task deps:verify
task deps:tidy
```

- `task deps:verify`：检查依赖，不改文件。
- `task deps:tidy`：依赖确实变更时使用，会改 `go.mod` / `go.sum`。

## Benchmark

```bash
task bench
task bench:core
```

以下热路径改动后建议跑 `task bench:core`：

- `internal/proxy`
- `internal/router`
- `internal/store`
- `pkg/llm`
- `pkg/recordfile`
- `pkg/replay`

## 本地密钥命令

```bash
llm-tracelab -c config/config.yaml db secret status
llm-tracelab -c config/config.yaml db secret export --out trace_index.secret.backup
llm-tracelab -c config/config.yaml db secret rotate --yes
llm-tracelab -c config/config.yaml --format json db secret status
```

- `status` 只输出路径、可读性和 fingerprint，不打印密钥。
- `export --out` 以 `0600` 权限写备份文件。
- `rotate --yes` 会备份旧 key、写入新 key，并重加密渠道 API key 和敏感 header。

## Postgres 运维验证

长期运行的 Postgres 优化、回填、灰度读、分区/归档规划见
[PostgreSQL 长期运行优化 Runbook](./POSTGRES_OPERATIONS_RUNBOOK.md)。

生产状态检查入口：

```bash
llm-tracelab -c config/config.yaml db migrate status --check-db
llm-tracelab -c config/config.yaml db migrate optimize-indexes --dry-run
llm-tracelab -c config/config.yaml auth migrate status --check-db
llm-tracelab -c config/config.yaml analyze backfill-exchanges --dry-run
```

- `db migrate status --check-db`：只读检查 application schema migration 状态。
- `db migrate optimize-indexes --dry-run`：预览非事务 PostgreSQL concurrent index 优化语句；确认后去掉 `--dry-run` 执行。
- `auth migrate status --check-db`：只读检查 auth-owned 表和共享 application migration namespace 状态。
- `analyze backfill-exchanges --dry-run`：只报告 exchange metadata 回填扫描/冲突，不更新 DB，不重写 `.http` cassette。
- 新增生产索引、summary、回填或灰度读路径时，先按 runbook 保存 `pg_stat_statements` 基线和 `EXPLAIN (ANALYZE, BUFFERS)`，再选择代码测试命令。

## AI Agent 默认选择

- 文档改动：`git diff --check`，必要时补链接检查。
- Postgres 运维 runbook 或生产 DB 操作说明改动：`git diff --check`；如涉及真实查询路径或 migration SQL，再补 `task check:quick` 和 `go test ./internal/store ./internal/monitor`。
- 小代码改动：`task check:quick`。
- record/replay/协议解析改动：`go test ./pkg/recordfile ./pkg/replay ./pkg/llm ./pkg/observe ./internal/monitor`。
- Monitor UI 改动：`task ui:build && task ui:test && go test ./internal/monitor`。
- proxy/router/store 改动：`go test ./internal/proxy ./internal/router ./internal/store`，必要时 `task test:race`。
- 大范围交付前：`task check:full`。
