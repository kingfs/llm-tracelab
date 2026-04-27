---
name: go-cli-meta-scaffold
description: 用于为 AI 快速搭建或重构生产可用的 Go CLI 元工程。适用于需要 `cobra` 多命令结构、可选 `viper` 配置加载、`go-task` 自动化、`golangci-lint` 质量门禁、`goreleaser` 发布配置、GitLab CI、清晰的 scaffold 与业务边界，以及适合 AI 持续协作的仓库布局的场景。
---

# Go CLI Meta Scaffold

当用户希望新建或标准化一个专业化、可发布、便于 AI 与人类持续协作的 Go CLI 仓库时，使用此 skill。

## 适用场景

- 从零搭建新的 Go CLI 工具仓库
- 将已有脚本型工具或散乱命令集合重构为规范 CLI 工程
- 需要 `cobra` 多命令结构与 `completion`、`version` 等常见 CLI 体验
- 需要 `go-task`、`golangci-lint`、`goreleaser`、GitLab CI 的统一工程约束
- 需要为 AI 明确哪些文件属于 scaffold、哪些文件承载业务实现

## 不适用场景

- 只需要一个几十行的临时脚本或一次性 PoC
- 用户明确不要命令分层、不要发布流程、不要配置体系
- 实际目标是长驻服务、gRPC/HTTP server、数据库驱动后端，而不是 CLI
- 用户只想在现有稳定 CLI 仓库里修一个点状 bug，而不是建立或重塑元工程

## 先读什么

1. 先读取仓库根目录下现有的 `go.mod`、`Taskfile.yml` 或 `Taskfile.yaml`、`.goreleaser.yml`、`.gitlab-ci.yml`
2. 如已有 `cmd/`、`internal/`、`pkg/` 或现成命令定义，则优先复用现有业务命令，不重造平行入口
3. 如需要具体目录蓝图，读取 `references/repo-blueprint.md`
4. 如从零起盘，优先使用 `scripts/init_scaffold.sh`
5. 生成或重构完成后，使用 `checklists/scaffold-delivery-checklist.md` 自检

## Required Inputs

至少收集或从仓库推断出以下信息：

- `module_path`：Go module 路径
- `cli_name`：CLI 二进制名，也是 `cmd/<cli_name>/` 目录名
- `tool_purpose`：该 CLI 的核心用途，例如同步、转换、导出、审计、运维
- `primary_command_model`：以单命令执行为主、子命令集合、或两者混合
- `config_strategy`：是否需要配置文件、环境变量覆盖、仅 flags，或混合
- `output_mode`：人类可读输出、JSON、表格、静默模式，或混合
- `release_target`：本地构建、GitLab CI、goreleaser、多平台发布

如果上述信息不完整，优先根据现有仓库推断；只有在关键决策无法安全推断时才向用户追问。

## Workflow

1. 先区分 scaffold 与业务命令。
   - scaffold 负责目录结构、命令注册方式、配置加载约定、发布与测试链路。
   - 业务层负责具体子命令语义、远端 API、文件格式、领域规则。
2. 先定 CLI 体验，再落代码结构。
   - 根命令负责全局 flags、配置入口、输出模式。
   - `version`、`completion` 作为默认基础命令。
   - 如用户未提供明确业务命令，可先放一个可运行的占位命令，例如 `run`，后续再按业务替换。
3. 明确“源文件”和“生成/构建产物”的边界。
   - `cmd/**`、`internal/**`、`pkg/**`、`Taskfile.yml`、`.goreleaser.yml`、CI 配置是工程源。
   - `dist/`、`coverage/`、发布包、shell completion 输出等属于构建产物，不提交到模板中。
4. 命令组织采用 `cobra` 多文件注册。
   - `cmd/<cli_name>/main.go` 只负责启动。
   - `root.go`、`version.go`、`completion.go` 与业务子命令分文件维护，并通过 `init()` 注册。
   - 不要把所有命令塞进一个 `main.go`。
5. 配置加载按需采用 `viper`。
   - 如 CLI 只依赖 flags，可保持最小实现。
   - 如需要配置文件与环境变量覆盖，则提供统一的 `--config` / `-c` 入口，并固定 env prefix。
   - 不要在业务代码中到处散落配置读取逻辑。
6. 输出策略集中管理。
   - 至少明确普通文本输出与错误输出路径。
   - 若支持 JSON 或 quiet mode，应在根命令或统一 printer 中集中处理。
7. 项目管理采用 `go-task`。
   - 至少包含 `deps`、`lint`、`test`、`build`、`run`、`release:dry`
   - 如提供 shell completion 生成任务，应显式命名，例如 `completion:bash`
   - 初始化新仓库后，先执行一次 `go mod tidy` 或 `task deps`，再进入 lint/test/build
8. 发布与 CI 约束清晰化。
   - 本地发布优先通过 `goreleaser release --snapshot --clean`
   - GitLab CI 尽量调用 `task`
   - package/release 阶段应消费已有构建链，不平行发明第二套脚本
9. 补齐 AI 与人类文档。
   - `AGENTS.md`：给 AI 看的工程导航、边界、构建产物禁改规则
   - `ARCHITECTURE.md`：命令层次、配置流、输出流、发布流
   - `README.md`：启动、开发、测试、构建、发布
10. 最后执行验证。
   - 至少验证命令结构、`task` 任务、脚本初始化、测试与构建入口

## 强约束

- 不要猜测或擅自升级本地 Go 版本；以用户机器上的现有工具链和仓库声明为准。
- 不要在项目目录内创建或要求用户创建 `.cache/go-build`、`.cache/go/pkg/mod`、`.cache/gopath` 等仓库级 Go 缓存目录，也不要通过导出 `GOCACHE`、`GOMODCACHE`、`GOPATH` 把缓存重定向到仓库内。
- 不要把所有命令逻辑塞进 `main.go` 或单个超大文件。
- 不要把发布产物、completion 输出、临时测试文件提交进模板。
- 不要把真实密钥、令牌、`.env`、生产 API 地址写入仓库。
- `.gitignore`、`.env.example`、`.goreleaser.yml`、`.gitlab-ci.yml` 必须存在并符合最小安全实践。

## 目录与边界

详细蓝图见 `references/repo-blueprint.md`。

默认需要明确以下 ownership：

- `cmd/`：CLI 入口与命令注册
- `internal/app/`：应用组装、命令运行时依赖
- `internal/config/`：配置解析、默认值、env 绑定
- `internal/output/`：统一输出策略
- `internal/version/`：构建时注入的版本信息
- `internal/<domain>/`：与具体业务相关的实现
- `pkg/`：只有在需要对外复用时才放公共库，不默认堆放业务
- `tests/`：端到端或命令级行为测试

## Output Contract

完成该 skill 后，产出的具体项目至少应满足：

- 可通过 `scripts/init_scaffold.sh` 初始化出可继续开发的 CLI 工程
- `cmd/<cli_name>/` 采用 root/subcommand 分文件结构，并默认含 `version`、`completion` 与一个业务占位命令
- 可通过 `task lint`、`task test`、`task build`、`task run`、`task release:dry`
- 初始化后可通过 `task deps` 补齐依赖，再进入 `task lint`、`task test`、`task build`
- 仓库包含 `.gitlab-ci.yml`、`Taskfile.yml`、`.goreleaser.yml`、`.env.example`
- 仓库包含 `AGENTS.md`、`ARCHITECTURE.md`、`README.md`
- AI 能根据目录与文档推断“该改哪里、不该改哪里”
- `README` 与 `Taskfile` 不会误导用户把 Go 缓存写入仓库目录

## Verification

1. 检查 `cmd/<cli_name>/` 是否采用 root/subcommand 分文件结构，而不是把命令塞进 `main.go`
2. 检查文档是否明确 `version`、`completion` 与业务命令的职责边界
3. 检查 `Taskfile` 是否提供 `deps`、`lint`、`test`、`build`、`run`、`release:dry`
4. 检查 `.goreleaser.yml` 是否能表达最小的多平台 CLI 发布配置
5. 检查 `.gitlab-ci.yml` 是否围绕 `task` 组织，而不是平行维护第二套构建逻辑
6. 检查 `README.md`、`AGENTS.md`、`ARCHITECTURE.md` 的导航与职责是否一致
7. 检查 `.gitignore`、`.env.example`、发布配置中没有引入真实敏感信息
8. 检查脚手架说明没有把 `GOCACHE`、`GOMODCACHE`、`GOPATH` 指向仓库内 `.cache/` 或其他项目级缓存目录
9. 用 `checklists/scaffold-delivery-checklist.md` 做交付自检

## Resources

- `scripts/init_scaffold.sh`：初始化 CLI 元工程骨架
- `assets/templates/`：可复用项目模板
- `references/repo-blueprint.md`：目录蓝图与依赖边界
- `checklists/scaffold-delivery-checklist.md`：交付自检项
