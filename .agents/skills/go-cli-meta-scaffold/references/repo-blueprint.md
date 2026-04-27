# Go CLI Meta Scaffold Blueprint

本文件用于给 AI 一个稳定、可推断的 Go CLI 元工程蓝图。不是所有目录都必须强制存在，但需要保持命令层级、配置入口、输出策略与发布链路清晰。

## 推荐目录

```text
.
├── AGENTS.md
├── ARCHITECTURE.md
├── README.md
├── .env.example
├── .gitignore
├── .gitlab-ci.yml
├── .goreleaser.yml
├── Taskfile.yml
├── go.mod
├── go.sum
├── cmd/
│   └── <cli-name>/
│       ├── main.go
│       ├── root.go
│       ├── version.go
│       ├── completion.go
│       └── <business-command>.go
├── internal/
│   ├── app/
│   │   └── app.go
│   ├── config/
│   │   └── config.go
│   ├── output/
│   │   └── printer.go
│   ├── version/
│   │   └── version.go
│   └── <domain>/
│       └── runner.go
├── pkg/                          # 按需；仅放可复用公共库
├── scripts/
│   └── ci/
├── dist/                         # 构建产物，不提交
└── tests/
    ├── e2e/
    └── testdata/
```

## 依赖方向

- `cmd` 只负责命令行入口、flags 注册、错误返回
- `internal/app` 负责初始化配置、输出器、业务 runner
- `internal/config` 负责配置默认值、配置文件解析、环境变量覆盖
- `internal/output` 负责统一文本/JSON/quiet 等输出策略
- `internal/<domain>` 负责具体业务命令逻辑
- `pkg` 只有在其他仓库也要 import 时才启用；不要把本项目私有逻辑塞进去

## 命令组织约束

- `main.go` 只负责执行根命令
- `root.go` 持有全局 flags 与配置入口
- `version.go` 输出版本、提交 SHA、构建时间
- `completion.go` 生成 shell completion
- 业务命令单独分文件维护，并通过 `init()` 注册到 root command
- 用户未给出具体业务命令时，可先用 `run` 作为占位命令，但交付时应明确这是待替换的业务入口

## 配置与输出约束

- 如果需要配置文件，统一使用 `--config` / `-c`
- 环境变量应有稳定前缀，例如 `FOO_BAR_`
- 配置读取集中在 `internal/config`，不要在各命令文件中零散读取环境变量
- 普通输出和错误输出应通过统一 printer 或 output helper 处理
- 若支持 JSON 或 quiet mode，应在根命令层统一控制，而不是业务层各自实现一套

## Taskfile 约束

至少建议包含：

- `lint`
- `test`
- `build`
- `run`
- `release:dry`
- `deps`
- `completion:bash`

建议：

- `lint` 串联 `go vet` 与 `golangci-lint`
- `deps` 执行 `go mod tidy`
- `build` 输出到 `dist/bin/<cli-name>`
- `run` 直接执行 `go run ./cmd/<cli-name> -- --help` 或用户传入参数
- `release:dry` 使用 `goreleaser release --snapshot --clean`

## 发布与 CI 约束

- `.goreleaser.yml` 用于最小多平台二进制发布
- GitLab CI 优先通过 `task` 执行公共任务
- stages 至少覆盖 `lint`、`test`、`build`、`release`
- `release` 阶段不应重复定义另一套复杂构建逻辑

## 测试策略

- 单元测试覆盖配置解析、输出格式、核心业务 runner
- 端到端测试至少覆盖：
  - `version`
  - 业务占位命令的 happy path
  - 至少一个错误路径
- 如果 CLI 依赖外部服务，应为 e2e 提供可替换的假实现或显式说明不在 scaffold 层内覆盖

## AI 协作约束

项目仓库内应生成以下文档：

- `AGENTS.md`
  - 说明目录职责
  - 说明 `dist/`、release artifacts、completion 输出属于构建产物，不直接修改
  - 指导 AI 先读什么，再改什么
- `ARCHITECTURE.md`
  - 解释命令组织、配置流、输出流、发布流
- `README.md`
  - 面向人类，写运行、开发、测试、发布
