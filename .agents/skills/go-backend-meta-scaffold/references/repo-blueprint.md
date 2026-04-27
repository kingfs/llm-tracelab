# Go Backend Meta Scaffold Blueprint

本文件用于给 AI 一个稳定、可推断的 Go 后端元工程蓝图。不是所有目录都必须强制存在，但需要保持依赖方向、生成边界和职责边界清晰。

## 推荐目录

```text
.
├── AGENTS.md
├── ARCHITECTURE.md
├── README.md
├── .env.example
├── .gitignore
├── .dockerignore
├── .gitlab-ci.yml
├── Taskfile.yml
├── go.mod
├── go.sum
├── buf.yaml
├── buf.gen.yaml
├── buf.work.yaml                 # 按需
├── proto/
│   └── <domain>/<version>/*.proto
├── gen/                          # buf 或其他生成代码，禁止手改
├── cmd/
│   └── <service-name>/
│       ├── main.go
│       ├── root.go
│       ├── serve.go
│       ├── migrate.go
│       ├── migrate_up.go
│       └── migrate_down.go
├── internal/
│   ├── app/                      # 启动编排、wire injector 入口
│   ├── application/              # 用例服务，事务边界，命令/查询编排
│   ├── domain/
│   │   ├── <bounded-context>/
│   │   │   ├── entity.go
│   │   │   ├── repository.go
│   │   │   ├── service.go
│   │   │   └── errors.go
│   ├── infrastructure/
│   │   ├── config/
│   │   ├── db/
│   │   ├── logging/
│   │   ├── observability/
│   │   ├── persistence/
│   │   └── transport/
│   └── interfaces/
│       ├── grpc/
│       └── http/
├── ent/
│   ├── entc.go
│   ├── generate.go
│   ├── dao/                      # 生成代码，禁止手改
│   ├── entx/                     # 自定义 ent extension
│   ├── helper/
│   ├── migrate/
│   ├── migrations/               # 生成产物，禁止手改
│   ├── mixin/
│   ├── policy/
│   └── schema/
├── docker/
│   ├── Dockerfile
│   ├── entrypoint.sh
│   └── config.template.yaml
├── deployments/
│   └── docker-compose.yml        # 按需
├── scripts/
│   └── ci/
└── tests/
    ├── e2e/
    ├── integration/
    └── testdata/
```

## 依赖方向

- `cmd` 依赖 `internal/app`
- `internal/app` 负责组装，不承载业务规则
- `internal/application` 调用 `internal/domain` 定义的模型与接口
- `internal/infrastructure` 实现仓储、外部网关、配置、日志、传输适配
- `internal/interfaces` 负责协议层适配与 DTO 映射，不承载核心规则
- `ent/schema` 生成 `ent/dao` 与 `ent/migrations`
- `proto` 生成 `gen/` 中的协议代码与网关代码

## DDD 使用原则

适合强化 DDD 的场景：

- 有明确业务不变量
- 有跨实体一致性要求
- 有复杂状态流转、审批、调度、补偿、幂等控制
- 需要区分命令模型与查询模型

不必生硬使用聚合的场景：

- 单表查询、搜索、分页列表
- 仪表盘、报表、统计接口
- 只读 API、外部透传查询

推荐做法：

- 对命令型用例，保留聚合、领域服务、仓储接口
- 对查询型用例，允许直接在 `application/query` 或 `infrastructure/persistence/query` 使用 query object / read model
- 不因为“像 DDD”而引入额外抽象层

## Proto 约束

- `proto` 是对外契约唯一事实来源
- 尽量在 proto 中表达：
  - 资源标识
  - 分页、过滤、排序
  - 幂等键
  - 枚举状态
  - HTTP 注解
  - 字段行为约束
- gRPC 与 REST 共享同一套 proto 定义，不维护平行接口描述
- REST 网关优先通过 `google.api.http` 注解生成，而不是手写第二套 REST 契约
- 生成代码目录必须显式标注为禁止手改

参考你提供的 `a2a.proto` 风格时，重点学习：

- 一个 service 同时承载 gRPC 和 HTTP annotation
- message/enum 具备较高自说明性
- API 契约中体现状态、分页、流式能力和资源路径

## Ent 约束

- 默认把 `ent` 工具链集中到 `ent/`
- `ent/entc.go` 默认开启 `gen.AllFeatures`
- `ent/schema` 是设计源
- `ent/dao` 是生成代码根目录，禁止手改
- `ent/migrations` 是生成 migration 目录，禁止手改
- `ent/entx` 可承载分页模板等扩展
- `ent/helper/tx.go` 可封装事务模板
- 如使用 Atlas checksum，保持 `atlas.sum` 与 migration 一致
- 首次脚手架建议分两阶段：
  - `phase1` 只落 schema、proto、文档与非 dao 依赖骨架
  - 等 `go generate ./ent/...` 生成好 `ent/dao/**` 后，再执行 `phase2` 落 `ent/helper/tx.go`、`ent/migrate/*` 与依赖 dao 的运行时代码

推荐最小生成链：

1. 执行 `scripts/init_scaffold.sh phase1 ...`
2. 修改 `ent/schema/**`
3. 运行 ent codegen 更新 `ent/dao/**`
4. 执行 `scripts/init_scaffold.sh phase2 ...`
5. 运行 `go run -mod=mod ent/migrate/main.go <migration_name>` 更新 `ent/migrations/**`
6. 更新或重算 `atlas.sum`
7. 若同一 commit 内出现多份尚未提交 migration，删除本地未提交文件后合并重生成为一套最终 migration

## Wire 约束

- `wire.go`、provider set、构造函数是源文件
- `wire_gen.go` 是生成文件，禁止手改
- 优先把 provider 按基础设施维度拆分，例如 DB、Repo、Service、Server、Config
- 装配逻辑集中于 `internal/app` 或 `cmd/<service>/wire.go`

## CLI 与配置

- `cobra` 负责命令组织
- `viper` 负责文件配置、环境变量覆盖、默认值加载
- `cmd/<service>/` 下的 root command 与各 sub command 推荐分文件，并通过 `init()` 完成注册
- 最少应有：
  - `serve`
  - `migrate up`
  - `migrate down`
  - `version`
- `serve` 支持 `--config` / `-c`
- `migrate up/down` 可优先基于 `github.com/golang-migrate/migrate`，并从镜像内的 `ent/migrations/**` 读取 SQL 文件

## 测试策略

- 单元测试：
  - 领域逻辑、应用服务、mapper、配置解析
  - 数据层优先 SQLite，如与 Postgres 行为存在差异，需要单独说明
- 集成测试：
  - repository、事务、migration、proto adapter
- 端到端测试：
  - 从真实入口发起请求，覆盖核心 happy path 和至少一个失败路径

## Taskfile 约束

至少建议包含：

- `generate`
- `lint`
- `test`
- `test:e2e`
- `build`
- `coverage`
- `package`
- `docker:build` 或并入 `package`

建议：

- 提供 `generate:proto`、`generate:ent`、`generate:wire`
- `generate` 串联 `buf generate`、`go generate ./ent/...`、`wire`
- `lint` 串联 `buf lint`、`go vet`、`golangci-lint`
- `build` 输出到仓库内临时产物目录，例如 `dist/`
- `package` 复用 `build` 产物，而不是重新编译

## GitLab CI 约束

stages 固定为：

```yaml
stages:
  - lint
  - test
  - build
  - security
  - coverage
  - package
  - deploy
```

设计要点：

- 尽量调用 `task <name>`
- 优先使用预构建 builder image，把 `task`、`buf`、`wire`、`golangci-lint` 等工具预装到镜像中，避免每个 job 现场安装
- 对 Go、Task、buf 缓存做合理配置，但不要把 Go 缓存重定向到仓库内 `.cache/`
- Go 相关缓存优先复用工具链默认位置，如 module cache 与 `~/.cache/go-build`，或交给 CI 平台托管 cache
- `build` 输出作为 artifacts 传给 `package`
- `package` 优先通过 `needs` 和 `dependencies` 只消费 `build` job 的 artifact
- `package` 不重新 build，只消费上游产物构建镜像
- `deploy` 只放部署特有逻辑，不把通用构建塞进去

## Docker 约束

运行时契约：

- 二进制放到 `/app/bin/<service-name>`
- 配置模板放到 `/app/config/config.template.yaml`
- entrypoint 根据环境变量渲染 `/app/config/config.yml`
- 最终执行：

```bash
/app/bin/<service-name> serve --config /app/config/config.yml
```

常见做法：

- builder stage 在仓库内输出 `dist/`
- runtime stage 只复制二进制、entrypoint、模板
- `.dockerignore` 排除 `.git`、`dist/`、`tmp/`、测试缓存、大型本地产物

## AI 协作约束

项目仓库内生成以下文档：

- `AGENTS.md`
  - 说明目录职责
  - 说明哪些文件是生成文件，不得直接修改
  - 指导 AI 先读什么，再改什么
- `ARCHITECTURE.md`
  - 解释 bounded context、主链路、关键依赖方向、生成工具链
- `README.md`
  - 面向人类，写运行、开发、测试、发版
