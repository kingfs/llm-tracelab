---
name: go-backend-meta-scaffold
description: 用于为 AI 快速搭建或重构生产级 Go 后端元工程。适用于需要基于 protobuf 作为接口唯一事实来源、同时产出 gRPC 与 REST 接口、使用 buf/ent/wire/cobra/viper/go-task/GitLab CI/Docker，并要求仓库结构 AI-friendly、强调 DDD 战略分析但避免僵化套用、明确生成代码不可手改、补齐单测与端到端测试的场景。
---

# Go Backend Meta Scaffold

当用户希望新建或标准化一个专业化、工程化、便于 AI 协作的 Go 后端仓库时，使用此 skill。

## 适用场景

- 从零搭建新的 Go 后端服务仓库
- 将已有仓库重构为更适合 AI 理解与持续协作的元工程
- 需要以 `proto` 为对外接口唯一事实来源，同时生成 gRPC 与 REST 接口
- 需要 `ent`、`wire`、`cobra`、`viper`、`buf`、`go-task`、`gitlab-ci`、`Docker` 的统一工程约束
- 需要用 DDD 做战略分析与核心子域拆分，但允许查询型场景采用更直接的工程模式

## 不适用场景

- 只需要一个极简 demo、一次性 PoC 或单文件服务
- 用户明确要求不用代码生成、不要 `proto`、不要依赖注入或不要数据库抽象
- 纯前端项目、脚本项目、非 Go 项目
- 用户只需要在现有稳定仓库里修一个点状 bug，而不是建立或重塑元工程

## 先读什么

1. 先读取仓库根目录下现有的 `go.mod`、`Taskfile.yml` 或 `Taskfile.yaml`、`buf.yaml`、`docker/`、`.gitlab-ci.yml`
2. 如存在现成 proto、ent schema、wire provider，则以这些业务定义为准，不要重造
3. 如需要具体目录蓝图，读取 `references/repo-blueprint.md`
4. 如从零起盘，优先使用 `scripts/init_scaffold.sh` 和 `assets/templates/`
5. 生成或重构完成后，使用 `checklists/scaffold-delivery-checklist.md` 自检

## Required Inputs

至少收集或从仓库推断出以下信息：

- `module_path`：Go module 路径
- `service_name`：服务名，也是主二进制名
- `bounded_context`：核心限界上下文与子域划分
- `service_type`：偏命令型、交易型、查询型，或混合型
- `proto_package` 与 `proto_version`
- `api_surface`：核心 RPC/API 列表
- `storage`：正式环境数据库，默认按 Postgres 设计
- `test_storage`：单测数据库，优先 SQLite；若不兼容则说明差异
- `delivery_target`：本地、Docker、docker compose、GitLab CI、K8s 等

如果上述信息不完整，优先根据现有仓库推断；只有在关键决策无法安全推断时才向用户追问。

## Workflow

1. 先做最小 DDD 战略分析。
   - 区分核心域、支撑域、通用域。
   - 判断哪些流程值得建聚合、领域服务、仓储接口。
   - 对纯查询、报表、列表检索类需求，不要硬套聚合；可直接走 query service、read model、repo query object。
2. 明确“源文件”和“生成文件”的边界。
   - `proto/**/*.proto` 是接口源。
   - `ent/schema/**`、`ent/mixin/**`、`ent/policy/**` 是数据模型源。
   - `wire.go`、provider set、配置模板、Taskfile、CI、Docker 脚本是工程源。
   - `gen/`、`ent/dao/`、`ent/migrations/`、buf/ent/wire 的生成产物不得手改。
3. 先定仓库骨架，再填业务。
   - 使用 `references/repo-blueprint.md` 的目录建议。
   - 优先保证目录命名、依赖方向、生成链路清晰，再补充具体实现。
   - 如从零开始，先执行 `scripts/init_scaffold.sh phase1` 生成第一阶段骨架，再根据具体业务细化 `proto` 与 `ent/schema`。
   - 当 `ent/schema` 定义完成后，先执行 `go generate ./ent/...` 或 `task generate:ent` 生成 `ent/dao/**`，再执行 `scripts/init_scaffold.sh phase2` 补齐依赖 dao 的运行时代码。
   - 在 `phase2` 完成前，不要直接跑完整 `task generate`，否则 `wire` 与依赖 dao 的代码容易进入错误修复循环。
4. 以 `proto` 作为对外接口唯一事实来源。
   - 在 proto 中定义 RPC、消息、错误语义、HTTP 注解、分页/过滤/幂等字段。
   - 使用 `buf` 管理 lint 与 generation。
   - 生成 gRPC 与 REST 所需代码，REST 优先通过 `google.api.http` 注解和 `grpc-gateway` 一类工具链生成，不要在手写 handler 中重复声明接口契约。
5. 对数据库访问采用 `ent`。
   - schema 放在 `ent/schema/`。
   - `entc.go` 默认开启 `gen.AllFeatures`。
   - 生成目标默认放在 `ent/dao/`。
   - migration 生成在 `ent/migrations/`。
   - `ent/helper/tx.go`、`ent/migrate/*`、以及直接 import `ent/dao` 的运行时代码，应在 `ent/dao/**` 已生成后再落盘或启用。
   - migration 的标准生成命令是在项目目录执行 `go run -mod=mod ent/migrate/main.go <migration_name>`。
   - migration 生成后，必须立即执行 `go run -mod=mod ent/migrate/update_hash.go ent/migrations`，重算 `ent/migrations/*.sql` 对应的 `atlas.sum`。
   - 该命令依赖 schema 已定义且已执行 `go generate ./ent/...`，并会临时拉起 Docker 中的 Postgres 来生成 migration。
   - 如果同一个 commit 内因多次调整 schema 产生了多份“尚未提交”的 migration，提交前应合并为一套最终 migration；优先删除本地未提交的旧 migration 后重新生成一次，再更新 `atlas.sum`。不要改写已经进入主干或已被他人依赖的历史 migration。
   - 如仓库提供了 `ent/entx/`、`ent/helper/tx.go`、`ent/migrate/` 等现成资产，优先保留并复用，不要自造第二套流程。
6. 对依赖装配采用 `wire`。
   - 仅修改 provider、injector 输入与业务构造函数，不直接编辑生成的 `wire_gen.go`。
7. 服务入口采用 `cobra` + `viper`。
   - `cmd/<service>/` 下的 root command 与各 sub command 应拆成独立文件，通过 `init()` 注册到 `rootCmd`，不要把全部命令塞进一个 `main.go`。
   - 默认至少提供 `serve`、`migrate up`、`migrate down`。
   - `serve --config /path/to/config.yml` 或 `-c` 为标准启动方式。
   - `migrate up/down` 优先使用 `github.com/golang-migrate/migrate`，并保证 `ent/migrations/**` 会随 Docker 镜像一起进入运行环境。
   - 支持 Docker entrypoint 从模板渲染配置再调用 `serve`。
8. 项目管理采用 `go-task`。
   - 至少包含 `generate`、`generate:proto`、`generate:ent`、`generate:wire`、`lint`、`test`、`test:e2e`、`build`、`package`。
   - 生成类任务必须把 `ent` 与 `wire` 拆分清楚，让 AI 能先只跑 `generate:ent`，再进入第二阶段 scaffold。
   - 不要为了“隔离环境”把 `GOCACHE`、`GOMODCACHE`、`GOPATH` 指到项目目录下的 `.cache/`、`go/` 或其他仓库级缓存目录；默认使用系统 Go 工具链已有缓存位置。
9. 持续集成优先围绕 `Taskfile` 组织。
   - `.gitlab-ci.yml` 的 job 尽量调用 `task`。
   - stages 采用用户要求的固定顺序：`lint`、`test`、`build`、`security`、`coverage`、`package`、`deploy`。
   - 优先使用预构建 builder image，把 `task`、`buf`、`wire`、`golangci-lint` 等常用工具预装进镜像，避免在每个 job 里重复安装。
   - 合理使用 cache、artifacts，避免 package 阶段重复 build。
   - 如 CI 需要缓存 Go 依赖或编译产物，优先使用 runner/home 级路径或 CI 平台托管 cache，不要通过任务脚本在仓库根目录制造 `.cache/go-build`、`.cache/go/pkg/mod` 之类目录。
   - `package` 阶段优先通过 `needs`、`dependencies` 直接消费 `build` job 的 artifact，而不是重复编译或重新生成产物。
10. 补齐 AI 与人类文档。
   - `AGENTS.md`：给 AI 看的工程导航、边界、生成代码禁改规则。
   - `ARCHITECTURE.md`：给工程师和 AI 看的架构说明、分层关系、关键链路。
   - `README.md`：给人类看的启动、开发、测试、发布说明。
11. 最后执行验证。
   - 至少验证生成链、lint、单测、端到端测试、构建链。

## 强约束

- 不要猜测或擅自升级本地 Go 版本；以用户机器上的现有工具链和仓库声明为准。
- 不要在项目目录内创建或要求用户创建 `.cache/go-build`、`.cache/go/pkg/mod`、`.cache/gopath` 等仓库级 Go 缓存目录，也不要通过导出 `GOCACHE`、`GOMODCACHE`、`GOPATH` 把缓存重定向到仓库内。
- 不要直接修改任何生成代码来表达业务意图。要回到 `proto`、`ent/schema`、`wire provider`、模板或源配置。
- 不要把 DDD 变成僵化目录仪式。目录与抽象必须服务于复杂业务，而不是增加样板代码。
- 不要把真实密钥、令牌、`.env`、生产库连接串提交到仓库。
- `docker` 目录中的镜像运行约定必须保持稳定：
  - 二进制位于 `/app/bin`
  - 配置模板位于 `/app/config/config.template.yaml`
  - entrypoint 渲染出 `/app/config/config.yml`
  - 由 CLI 的 `serve --config /app/config/config.yml` 启动
- `.gitignore`、`.dockerignore`、`.env.example` 必须存在并符合最小安全实践。

## 目录与边界

详细蓝图见 `references/repo-blueprint.md`。

默认需要明确以下 ownership：

- `proto/`：接口与契约
- `cmd/`：CLI 入口
- `internal/app/`：装配与启动
- `internal/domain/`：领域模型、领域服务、仓储接口
- `internal/application/`：用例编排
- `internal/infrastructure/`：DB、RPC、配置、日志、外部适配
- `internal/interfaces/`：gRPC/HTTP handler、DTO 转换
- `ent/`：schema、mixin、policy、生成代码、migration 工具链
- `docker/`：Dockerfile、entrypoint、配置模板
- `tests/`：单测辅助、e2e、集成测试

## Output Contract

完成该 skill 后，产出的具体项目至少应满足：

- 首次初始化流程明确为 `phase1 -> generate:ent -> phase2 -> generate -> migrate`
- 可以通过 `task generate:ent` 单独生成 `ent/dao/**`
- 可以通过 `task generate` 生成 proto、ent、wire 相关代码
- 可以通过 `task lint`、`task test`、`task test:e2e`、`task build`
- 仓库包含 `.gitlab-ci.yml`、`Taskfile.yml` 或 `Taskfile.yaml`、`docker/`、`.env.example`
- 仓库包含 `AGENTS.md`、`ARCHITECTURE.md`、`README.md`
- 接口契约从 `proto` 即可读懂
- AI 能根据目录与文档推断“该改哪里、不该改哪里”
- migration 工作流明确包含“生成 migration -> 更新 `atlas.sum` -> 提交前检查是否需要合并未提交 migration”
- `cmd/<service>/` 默认采用多文件 `cobra` 注册结构，并含 `serve`、`migrate up`、`migrate down`

## Verification

1. 检查生成源与生成产物边界是否明确且一致。
2. 检查文档与脚本是否明确要求先跑 `phase1`，再跑 `generate:ent`，再进入 `phase2`。
3. 检查 `task generate` 是否覆盖 buf、ent、wire，且 `task generate:ent` 可单独执行。
4. 检查 migration 文档是否明确 `go run -mod=mod ent/migrate/main.go <migration_name>` 与 `go run -mod=mod ent/migrate/update_hash.go ent/migrations` 的执行时机。
5. 检查文档是否明确“同一 commit 内多个未提交 migration 应在提交前合并为一套最终 migration”，且不会误导 AI 改写历史 migration。
6. 检查 `cmd/<service>/` 是否采用 root/sub command 分文件结构，并通过 `init()` 注册至少 `serve`、`migrate up`、`migrate down`。
7. 检查 CI stages 是否与要求一致，且 package 能复用 build 产物。
8. 检查 Docker 运行路径是否满足 `/app/bin` 与 `/app/config` 约定，并能携带 `ent/migrations/**` 进入镜像。
9. 检查测试策略是否覆盖单测与 e2e，并说明 SQLite 与 Postgres 差异。
10. 检查 `Taskfile`、CI、脚本、README 中没有把 `GOCACHE`、`GOMODCACHE`、`GOPATH` 指向仓库内 `.cache/` 或其他项目级缓存目录。
11. 用 `checklists/scaffold-delivery-checklist.md` 做交付自检。

## Resources

- `scripts/init_scaffold.sh`：初始化项目骨架与模板替换
- `assets/templates/`：可复用项目模板
- `references/repo-blueprint.md`：目录蓝图与依赖边界
- `checklists/scaffold-delivery-checklist.md`：交付自检项
