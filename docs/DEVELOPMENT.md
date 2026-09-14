# 开发与测试

本文是 `llm-tracelab` 的开发、验证、构建与集成测试入口。所有命令以仓库根目录的 `Taskfile.yml`、`.github/workflows/ci.yml` 和 `cmd/server/**` 为准；本文只记录当前实际存在、可执行的命令。

相关阅读：[文档入口](./README.md)、[架构说明](./ARCHITECTURE.md)、[协议参考](./protocol-reference/README.md)、[Monitor 使用指南](./MONITOR_GUIDE.md)、[MCP 使用指南](./MCP_GUIDE.md)、[代理使用示例](./PROXY_USAGE_EXAMPLES.md)。

## 命令入口（Taskfile 与直接命令的对应关系）

`task` 是稳定入口，避免手写 Go / 前端命令。常用 task 与底层命令的对应关系：

| task | 实际执行 |
| --- | --- |
| `task fmt` | `gofmt -w ./cmd ./internal ./pkg ./unittest` |
| `task fmt:check` | `gofmt -l` 检查同样的目录，不改文件，有输出即失败 |
| `task lint` | `golangci-lint run ./...` |
| `task lint:vet` | `go vet ./...` |
| `task test:short` | `go test -short ./...` |
| `task test` | `go test ./...` |
| `task test:race` | `go test -race ./...` |
| `task test:cover` | `go test -coverprofile=coverage.out ./...` |
| `task test:e2e` | `go test ./internal/proxy ./internal/monitor ./cmd/server ./unittest` |
| `task test:codex-fixtures` | `env -u GOROOT go test ./internal/responses/httpapi ./internal/responses/runtime -count=1` |
| `task bench` | `go test -bench=. -benchmem ./...` |
| `task bench:core` | `go test -bench=. -benchmem ./internal/proxy ./internal/router ./internal/store ./pkg/llm ./pkg/recordfile ./pkg/replay` |
| `task deps:verify` | `go mod verify` + `go list -mod=readonly ./...` |
| `task deps:tidy` | `go mod tidy` |
| `task build:go` | `go build -trimpath -ldflags=... -o llm-tracelab ./cmd/server` |
| `task build:all` | `task ui:build` 然后 `task build:go`（`task build`、`default` 同此） |
| `task run` | `go run ./cmd/server -c {{.CONFIG}}`，不带子命令即启动 serve |
| `task ui:build` | 在 `web/monitor-ui` 下 `bun install --frozen-lockfile` + `bun run build` |
| `task ui:test` | 在 `web/monitor-ui` 下 `bun install --frozen-lockfile` + `bun run test:ui` |
| `task ui:test:real` | 在 `web/monitor-ui` 下 `bun install --frozen-lockfile` + `bun run test:ui:real` |
| `task migrate` | `go run ./cmd/server migrate -c {{.CONFIG}}`（V2 cassette 重写为 V3 并重建索引） |
| `task migrate:db:up` | `go run ./cmd/server db migrate up -c {{.CONFIG}}` |
| `task auth:init-user` | `go run ./cmd/server auth init-user -c {{.CONFIG}} --username "$USER" --password "$PASSWORD"` |
| `task auth:create-token` | `go run ./cmd/server auth create-token -c {{.CONFIG}} --username "$USER" --name "$NAME"` |
| `task generate:ent` | `go generate ./ent/...` |
| `task migrate:ent:sqlite NAME=x` | `ent/migrate/main.go --dialect sqlite --dir ent/migrations` + `update_hash.go`；Postgres 版为 `migrate:ent:postgres NAME=x DEV_URL=...`，写 `ent/postgres-migrations` |
| `task docker:build` / `docker:up` | `scripts/docker-build-env.sh` + `docker build -t llm-tracelab:local .`；`docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build -d` |
| `task clean` | `rm -f llm-tracelab` |

`CONFIG` 默认是 `config/config.yaml`，可用 `CONFIG=path/to/config.yaml task <task>` 覆盖，所有读取配置的 task 都支持。

## 日常开发流程

```bash
task fmt
task check:quick
task build:go
task run
```

- `task fmt`：格式化 Go 代码后再提交。
- `task check:quick`：格式检查 + lint + 短测试，不改写任何文件，是提交前的最小质量门。
- `task build:go`：只重编译后端，适合纯后端改动。
- `task run`：用 `CONFIG` 指定的配置启动代理与 Monitor。

`config/config.yaml` 是 Postgres-first 配置且 `database.dsn` 为空，直接 `task run` 前需要导出 `LLM_TRACELAB_DATABASE_DSN`，或改用 `CONFIG=config/examples/local-sqlite.yaml`。其它示例配置见 `config/examples/`（openai、anthropic、google_genai、vertex、azure_openai、openai-compatible-vllm-postgres）。

## 验证等级（quick / full / race / bench 各自跑什么）

- `task check:quick` = `fmt:check` + `lint` + `test:short`：小范围代码改动的默认验证；`task check` 与它完全相同。
- `task check:full` = `check:quick` + `test` + `test:e2e` + `test:race` + `build:all`：发布前或大范围改动。
- `task test:race`：全套单元测试加 Go race detector；代理、路由、录制、存储或 streaming 改动必须补跑。
- `task test:e2e`：本地端到端测试，覆盖 `internal/proxy`、`internal/monitor`、`cmd/server`、`unittest`，不依赖外部网络。
- `task test:cover`：写 `coverage.out`。
- `task bench`：全部 benchmark；`task bench:core` 只跑热路径包（`internal/proxy`、`internal/router`、`internal/store`、`pkg/llm`、`pkg/recordfile`、`pkg/replay`）。
- `task lint:vet`：单独跑 `go vet ./...`，与 golangci-lint 互补。
- `task test:codex-fixtures`：离线 Codex Responses fixture runner。

所有测试都不应依赖真实 provider 网络或真实 API key。

## 前端开发与 UI 测试

Monitor UI 位于 `web/monitor-ui`，使用 Bun 作为包管理器（`packageManager: bun@1.3.11`）。

- `task ui:build`：构建 UI 并写入 embed 目标目录。
- `cd web/monitor-ui && bun run build`：等价于 `vite build`，`vite.config.js` 把产物写到 `internal/monitor/ui/dist`；`task` 未包装。
- `cd web/monitor-ui && bun run dev`：启动 Vite dev server；Taskfile 中没有对应的 dev task，只有直接命令。

Playwright 有两套套件：

- mock 套件：`bun run test:ui`（`playwright test`，配置 `playwright.config.js`），测试目录 `tests/`，`baseURL=http://127.0.0.1:4173`，由 `bun run build && bunx vite preview --host 127.0.0.1 --port 4173` 提供页面；`reuseExistingServer` 在非 CI 环境为真。项目为 `desktop`（Desktop Chrome）和 `mobile`（Pixel 7）。
- 真实服务套件：`bun run test:ui:real`（`playwright test --config playwright.real.config.js`），测试目录 `tests-real/`，`baseURL` 取自 `MONITOR_REAL_BASE_URL`（默认 `http://127.0.0.1:4183`），`webServer` 用 `MONITOR_REAL_ADDR=<host:port> go run ./test-fixtures/monitor_real_server.go` 启动本地 Go Monitor fixture 与 fake upstream；只有 `desktop-real` 一个项目，且 `reuseExistingServer` 为假。

Chromium 解析方式：两套配置都读取环境变量 `PLAYWRIGHT_CHROMIUM_EXECUTABLE`（`trim()` 后判断）。**只有**该变量非空时才把它设为 `launchOptions.executablePath`，否则不传该字段，使用 Playwright 自己托管的浏览器。CI 通过 `bunx playwright install --with-deps chromium` 安装托管 Chromium，本地无需设置该变量。

UI 或 embed 产物改动后建议执行：

```bash
task ui:build
task ui:test
task ui:test:real
go test ./internal/monitor
task build:go
```

## 构建产物（Go 二进制、UI dist 与 go:embed 的关系）

- Go 二进制：`task build:go` 在仓库根目录生成 `llm-tracelab`，使用 `-trimpath`，并通过 ldflags 注入 `main.Version`、`main.Commit`、`main.Date`、`main.Branch`（分别来自 `git describe`、`git rev-parse --short=12 HEAD`、UTC 构建时间、当前分支）。
- UI 产物：`internal/monitor/ui/dist/`（`index.html` 与 `assets/`），由 `vite build` 生成，并已提交到仓库。
- 二者关系：`internal/monitor/server.go` 用 `//go:embed ui/dist/*` 把该目录编译进二进制，因此 `go build ./cmd/server` 读取的是编译时刻 `internal/monitor/ui/dist` 的内容。前端改动后必须先 `task ui:build`（或直接用 `task build` / `task build:all`，它们会先跑 `ui:build`）再编译，否则二进制里仍是旧 UI。
- 容器镜像：`task docker:build` 构建本地镜像 `llm-tracelab:local`；`task docker:up` 用 compose 起本地栈。

## 依赖管理

- Go：`task deps:verify` 只校验不改文件（`go mod verify` + `go list -mod=readonly ./...`）；只有依赖确实变化时才用 `task deps:tidy`（会改写 `go.mod` / `go.sum`）。`go.mod` 声明 `go 1.25.0`。
- 前端：依赖锁定在 `web/monitor-ui/bun.lock`，task 与 CI 都使用 `bun install --frozen-lockfile`。
- ent 代码生成：`task generate:ent` 重新生成 `ent` 控制面 DAO；新增 SQLite / Postgres migration 分别用 `task migrate:ent:sqlite NAME=...` 和 `task migrate:ent:postgres NAME=... DEV_URL=...`。

## 数据库与认证初始化

首次启动或重置本地应用库：

```bash
task migrate:db:up
task auth:init-user USER=admin PASSWORD=<password>
task auth:create-token USER=admin NAME=local
```

- `task migrate:db:up`：对 `CONFIG` 指向的应用库执行 `db migrate up`。
- `task auth:init-user`：用 `USER`、`PASSWORD` 创建登录用户（底层 `auth init-user --username --password`）。
- `task auth:create-token`：为 `USER` 创建名为 `NAME` 的 API token（底层 `auth create-token --username --name`，还支持 `--scope`、`--ttl`）。
- Postgres 使用 `ent/postgres-migrations` 的版本化 SQL migration；在 `database.auto_migrate: false` 的生产路径下必须先执行 `db migrate up`，否则启动会报出缺失 migration / table 的错误。Postgres 下 auth 表由应用 migration 集合一并创建。
- SQLite 是本地 / 开发 / 测试回退，schema 在启动时应用而非版本化迁移，默认文件为 `{{output_dir}}/llm_tracelab.sqlite3`。
运维与只读检查入口如下（详见 [PostgreSQL 运维](./POSTGRES_OPERATIONS.md)）：

```bash
llm-tracelab -c config/config.yaml db migrate status --check-db
llm-tracelab -c config/config.yaml db migrate optimize-indexes --dry-run
llm-tracelab -c config/config.yaml db summary rebuild sessions --dry-run
llm-tracelab -c config/config.yaml db summary rebuild sessions --session-id <session_id>
llm-tracelab -c config/config.yaml auth migrate status --check-db
llm-tracelab -c config/config.yaml analyze backfill-exchanges --dry-run
LLM_TRACELAB_DATABASE_DSN='postgres://...' scripts/postgres-baseline.sh /tmp/tracelab-postgres-baseline.txt
```

- `db migrate status --check-db` 与 `auth migrate status --check-db` 从数据库读取状态；不带 `--check-db` 时只看配置。
- `db migrate optimize-indexes --dry-run` 预览非事务 PostgreSQL concurrent index 优化语句，确认后去掉 `--dry-run` 执行。
- `db summary rebuild sessions` 不带 `--session-id` 时重建全部 `session_summaries`，`--dry-run` 只统计不写库。
- `analyze backfill-exchanges --dry-run` 只报告 exchange metadata 回填情况，不改 DB、不重写 `.http` cassette。
- `database.use_session_summary_read: true` 或 `LLM_TRACELAB_DATABASE_USE_SESSION_SUMMARY_READ=true` 才让服务读取 `session_summaries`，默认关闭。

## 本地密钥命令

渠道密钥的本地加密 key 通过 `db secret` 子命令管理：

```bash
llm-tracelab -c config/config.yaml db secret status
llm-tracelab -c config/config.yaml db secret export --out trace_index.secret.backup
llm-tracelab -c config/config.yaml db secret rotate --yes
llm-tracelab -c config/config.yaml --format json db secret status
```

- `status` 只输出路径、可读性与 fingerprint，不打印密钥；`export --out` 以 `0600` 权限写备份文件，不带 `--out` 时把 key 写到 stdout。
- `rotate --yes` 缺少 `--yes` 会拒绝执行；确认后备份旧 key、写入新 key，并重加密渠道 API key 与敏感 header。

## 集成测试与 smoke test

用于在目标实例（例如 `http://<your-host>:<port>`）上验证 Codex + 本地 Responses runtime + hosted tools 链路。

**前置配置**：按“默认全开”处理，需要 `responses_server.codex_compat.enabled=true`、`responses_server.codex_compat.auto_inject_hosted_tools=["web_search"]`、`tools.web_search.enabled=true`、`tools.mcp.enabled=true`、`mcp.enabled=true`、`database.driver=postgres`，并设置 `responses_server.default_model`。`tools.web_search` 当前示例 provider 为 `searxng`，需要 `base_url`。MCP server 的 `bearer_token_env` 只应指向环境变量（例如 `DGX_API_KEY`），不能把 token 写进 YAML。

高风险工具不在集成默认路径：`file_search` 仍是 unsupported / rejected，`code_interpreter` 与 `computer_use_preview` 不启用。

**预检命令**（按顺序）：

```bash
llm-tracelab -c config/config.yaml --format json config inspect
llm-tracelab -c config/config.yaml --format json doctor
llm-tracelab -c config/config.yaml --format json tools status
llm-tracelab -c config/config.yaml --format json models codex-config <model>
```

判断标准：

- `doctor` 不应出现 blocking failure（可用 `--fail-on-warn` / `--fail-on-fail=false` 调整退出行为）。
- `tools status` 的 `ready_for_codex_web_search=true`，且 `tools.web_search.readiness=ready`。
- 需要 hosted MCP 时 `tools.mcp.readiness=ready` 且至少一个 enabled server。
- `models codex-config` 应输出 `model_context_window`、`model_auto_compact_token_limit`、`tool_output_token_limit`、`model_reasoning_effort`。

**Codex Web Search smoke test**：

```bash
codex -p dgx "搜索一下今日新闻"
```

期望：即使 Codex 请求没有显式 tools，TraceLab 也会按 `responses_server.codex_compat` 注入 `web_search`；`execution_events` 中出现 `response.tool_call` 的 `web_search` started/completed；`tool_call_audits` 中出现 `hosted:web_search` completed；内部 `upstream_exchanges` 带 TTFT，便于 prefill 速率分析。

**失败时收集**：优先用 `client_request_id`、`response_id`、`request_audit_id` 或最新请求查询。

```bash
llm-tracelab -c config/config.yaml --format json audit query --list --status failed --limit 20

llm-tracelab -c config/config.yaml --format json audit query \
  --client-request-id <client_request_id> \
  --include-events --include-exchanges --include-tools --limit 200

llm-tracelab -c config/config.yaml --format json audit query \
  --response-id <response_id> \
  --include-events --include-exchanges --include-tools --limit 200

llm-tracelab -c config/config.yaml --format json audit query \
  --request-audit-id <request_audit_id> \
  --include-events --include-exchanges --include-tools --limit 200

llm-tracelab -c config/config.yaml --format json audit tool-calls --latest-by-call --limit 100
```

**日志定位字段**：服务日志会输出用于关联数据库与请求链路的结构化字段——`request_audit_id`、`response_id`、`conversation_id`、`client_request_id`、`trace_id`、`cassette_path`、`upstream_id`、`model`、`endpoint`、`status`、`status_code`、`duration_ms`、`ttft_ms`、`tool_type`、`tool_name`、`executor`、`call_id`、`stream`、`server_id`、`server_label`。

日志不会输出 bearer token、raw authorization 或完整 tool payload。需要 raw payload 时只能用显式高权限的 `audit tool-calls --include-payloads`（会输出 `input_json` / `output_json` / `metadata_json`，可能含敏感数据），不建议在共享测试环境默认开启。审计数据的整体形态见 [观测与审计](./OBSERVATION_AND_AUDIT.md)，Responses 运行时细节见 [Responses 运行时](./RESPONSES_RUNTIME.md)。

**常见失败判断**：

- `ready_for_codex_web_search=false`：先看 `tools status` 的 reason 与 warnings。
- `web_search` 未触发：确认 `response.request` accepted 事件是否包含 `codex_compat.injected_hosted_tools`；failed 时查 `tool_call_audits` 的 `error_text` 与 `metadata.result_count` / `query`。
- MCP unknown server：确认 descriptor 的 `server_label` / `server_url` 与 `tools.mcp.servers` 完全匹配；auth failure 时确认 `bearer_token_env` 指向的环境变量存在（日志与 audit 只显示 env 名，不显示值）。
- TTFT 为 0：确认内部 upstream exchange 是否有响应 body；请求在连接阶段失败时 TTFT 为空是预期行为。

## CI 跑什么

`.github/workflows/ci.yml` 的 `build` job（push 到 `main`、`v*` tag、以及指向 `main` 的 PR）在 `ubuntu-latest` 上依次执行：

1. checkout，`oven-sh/setup-bun@v2`（bun `1.3.11`），`actions/setup-go@v5`（Go `1.25.x`，开启 module cache），`go mod download`。
2. 在 `web/monitor-ui` 执行 `bun install --frozen-lockfile`，再执行 `bun run build` 构建 Monitor UI。
3. `go build -v -o llm-tracelab ./cmd/server`。
4. gofmt 检查：对 `./cmd ./internal ./pkg ./unittest ./web/monitor-ui/test-fixtures` 跑 `gofmt -l`，有未格式化文件即失败。
5. `go vet ./...`。
6. golangci-lint：`golangci/golangci-lint-action@v8`，版本 `v2.11.4`。
7. `go test -v ./...`。
8. 在 `web/monitor-ui` 依次执行 `bunx playwright install --with-deps chromium`、`bun run test:ui`（mock 套件）、`bun run test:ui:real`（真实 Go Monitor fixture 套件）。
9. 上传构建产物 `llm-tracelab-linux-amd64`。

另有 `docker` job，仅在 push 事件运行、依赖 `build` job：登录 Docker Hub 后构建并推送 `linux/amd64` 与 `linux/arm64` 多架构镜像，tag 由 `docker/metadata-action` 生成（`latest`、`sha`、版本 tag），并启用 gha 构建缓存。

CI 与本地 task 的差异：CI 的 gofmt 范围额外包含 `./web/monitor-ui/test-fixtures`，而 `task fmt:check` 只覆盖 `./cmd ./internal ./pkg ./unittest`；CI 跑 `go test -v ./...` 而不跑 `test:race`、`test:e2e` 和 `bench`。

## 给 AI Agent 的默认选择

按改动类型选择最小足够验证，避免无谓的全量运行：

- 文档改动：`git diff --check`，必要时补链接检查；小代码改动：`task check:quick`。
- record / replay / 协议解析改动：`go test ./pkg/recordfile ./pkg/replay ./pkg/llm ./pkg/observe ./internal/monitor`。
- Monitor UI 改动：`task ui:build && task ui:test && go test ./internal/monitor`。
- proxy / router / store 改动：`go test ./internal/proxy ./internal/router ./internal/store`，必要时补 `task test:race`；热路径性能改动补 `task bench:core`。
- 数据库、迁移或生产查询路径改动：`task check:quick` + `go test ./internal/store ./internal/monitor`。
- 大范围交付前：`task check:full`。

约束：不要让测试依赖真实 provider 网络或真实 API key；不要绕过 `task` 入口手写等价命令，除非本文明确说明 Taskfile 未包装（例如 `bun run dev`）。存储与录制格式的改动约束见 [存储与部署](./STORAGE_AND_DEPLOYMENT.md) 和 [架构说明](./ARCHITECTURE.md)。
