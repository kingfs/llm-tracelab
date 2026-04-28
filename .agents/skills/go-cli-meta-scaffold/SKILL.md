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
- 需要面向 AI agent、CI 和脚本的稳定机器契约，例如 `--format json`、结构化错误、`schema` introspection、stdout/stderr 分离、dry-run
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
- `output_mode`：人类可读输出、JSON/NDJSON、表格、静默模式，或混合；生产级 CLI 必须至少提供稳定 JSON 机器契约
- `side_effect_model`：哪些命令会写文件、写数据库、调用远端 API、删除资源、发送消息或修改权限，并如何 dry-run/confirm
- `release_target`：本地构建、GitLab CI、goreleaser、多平台发布

如果上述信息不完整，优先根据现有仓库推断；只有在关键决策无法安全推断时才向用户追问。

## Workflow

1. 先区分 scaffold 与业务命令。
   - scaffold 负责目录结构、命令注册方式、配置加载约定、发布与测试链路。
   - 业务层负责具体子命令语义、远端 API、文件格式、领域规则。
2. 先定机器契约，再包装人类体验。
   - 明确 stdout 只输出主结果，stderr 输出日志、warning、诊断和失败时的结构化错误。
   - 默认可以保留 text/table 以兼容人类习惯，但必须支持 `--format json` 作为 agent 和脚本的稳定契约。
   - JSON 成功输出应包含 `ok`、`command`、`result`、`warnings`；失败输出应包含 `ok=false` 与结构化 `error`。
   - 错误字段至少包含 `code`、`category`、`message`、`retryable`、`safe_to_retry`，必要时包含 `field`、`required_scopes`、`suggested_commands`。
3. 再定 CLI 体验与代码结构。
   - 根命令负责全局 flags、配置入口、输出模式。
   - `version`、`completion` 作为默认基础命令。
   - 增加 `schema` 或等价 introspection 命令，供 agent 获取命令、flags、默认值、枚举、输出契约和退出码。
   - 如用户未提供明确业务命令，可先放一个可运行的占位命令，例如 `run`，后续再按业务替换。
4. 明确“源文件”和“生成/构建产物”的边界。
   - `cmd/**`、`internal/**`、`pkg/**`、`Taskfile.yml`、`.goreleaser.yml`、CI 配置是工程源。
   - `dist/`、`coverage/`、发布包、shell completion 输出等属于构建产物，不提交到模板中。
5. 命令组织采用 `cobra` 多文件注册。
   - `cmd/<cli_name>/main.go` 只负责启动。
   - `root.go`、`version.go`、`completion.go` 与业务子命令分文件维护，并通过 `init()` 注册。
   - 不要把所有命令塞进一个 `main.go`。
6. 配置加载按需采用 `viper`。
   - 如 CLI 只依赖 flags，可保持最小实现。
   - 如需要配置文件与环境变量覆盖，则提供统一的 `--config` / `-c` 入口，并固定 env prefix。
   - 不要在业务代码中到处散落配置读取逻辑。
7. 输出策略集中管理。
   - 至少提供 `internal/output` 或 CLI 层统一 printer，避免每个命令手写不同 JSON/错误格式。
   - 输出 secrets 时默认脱敏；确实需要返回 token 的命令应在 schema 中标记敏感字段，并避免日志打印。
   - 大列表命令应支持分页、字段选择或 NDJSON，避免一次性输出不可控大 JSON。
8. 副作用命令先支持预演。
   - 写文件、写数据库、调用远端 API、删除、发送、审批、改权限等命令必须支持 `--dry-run` 或等价 preview。
   - dry-run 输出应包含 `dry_run=true`、`mutated=false`、目标资源、最终 resolved config、预计副作用和风险说明。
   - 高风险操作需要 `--confirm <resource-id>` 或明确的人类确认边界；`--yes` 不能绕过权限和策略。
   - 自动化路径必须支持 `--no-input`，非 TTY 或 no-input 下不能进入隐藏 prompt。
9. 项目管理采用 `go-task`。
   - 至少包含 `deps`、`lint`、`test`、`build`、`run`、`release:dry`
   - 如提供 shell completion 生成任务，应显式命名，例如 `completion:bash`
   - 初始化新仓库后，先执行一次 `go mod tidy` 或 `task deps`，再进入 lint/test/build
10. 发布与 CI 约束清晰化。
   - 本地发布优先通过 `goreleaser release --snapshot --clean`
   - GitLab CI 尽量调用 `task`
   - package/release 阶段应消费已有构建链，不平行发明第二套脚本
11. 补齐 AI 与人类文档。
   - `AGENTS.md`：给 AI 看的工程导航、边界、构建产物禁改规则
   - `ARCHITECTURE.md`：命令层次、配置流、输出流、发布流
   - `README.md`：启动、开发、测试、构建、发布
   - skill 文档回答“任务应该怎么做”，不要复制完整 README 或动态命令帮助；引导 agent 用 `schema --format json` 按需取命令契约。
12. 最后执行验证。
   - 至少验证命令结构、`task` 任务、脚本初始化、测试与构建入口
   - 增加 JSON 可解析性、stdout/stderr 分离、结构化错误、dry-run 无副作用的测试

## 强约束

- 不要猜测或擅自升级本地 Go 版本；以用户机器上的现有工具链和仓库声明为准。
- 不要在项目目录内创建或要求用户创建 `.cache/go-build`、`.cache/go/pkg/mod`、`.cache/gopath` 等仓库级 Go 缓存目录，也不要通过导出 `GOCACHE`、`GOMODCACHE`、`GOPATH` 把缓存重定向到仓库内。
- 不要把所有命令逻辑塞进 `main.go` 或单个超大文件。
- 不要只提供 table/pretty/prose 输出；生产级 CLI 必须有稳定 JSON 机器输出。
- 不要让日志、spinner、warning 污染 stdout；stdout 只放主结果。
- 不要把错误只写成自然语言；结构化错误必须能表达错误类别、是否可重试和修复建议。
- 不要让自动化路径进入交互式 prompt；缺参数时直接失败并返回结构化 usage/validation 错误。
- 不要给写操作省略 dry-run；如果短期做不到，必须在交付说明中明确缺口和风险。
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
- 支持 `--format json`，成功输出和失败输出都有稳定 JSON envelope
- 提供 `schema` 或等价机器可读 introspection 命令，覆盖命令、flags、默认值、枚举、输出契约和退出码
- 有副作用命令提供 `--dry-run` 或等价 preview，并在 JSON 输出中明确 `dry_run` 与 `mutated`
- stdout/stderr 合同清晰：stdout 只输出主结果，stderr 输出日志、诊断和失败 envelope
- 自动化路径非交互，支持 `--no-input` 或明确声明无 prompt
- 可通过 `task lint`、`task test`、`task build`、`task run`、`task release:dry`
- 初始化后可通过 `task deps` 补齐依赖，再进入 `task lint`、`task test`、`task build`
- 仓库包含 `.gitlab-ci.yml`、`Taskfile.yml`、`.goreleaser.yml`、`.env.example`
- 仓库包含 `AGENTS.md`、`ARCHITECTURE.md`、`README.md`
- AI 能根据目录与文档推断“该改哪里、不该改哪里”
- `README` 与 `Taskfile` 不会误导用户把 Go 缓存写入仓库目录

## Verification

1. 检查 `cmd/<cli_name>/` 是否采用 root/subcommand 分文件结构，而不是把命令塞进 `main.go`
2. 检查 `--format json` 输出是否可被 `jq`/JSON parser 稳定解析，且没有日志混入 stdout
3. 检查结构化错误是否包含 `code`、`category`、`message`、`retryable`、`safe_to_retry`
4. 检查 `schema --format json` 或等价命令是否覆盖命令、flags、默认值、枚举、输出契约和退出码
5. 检查所有写操作、删除操作、发送操作、权限修改操作是否支持 dry-run 或确认边界
6. 检查文档是否明确 `version`、`completion`、`schema` 与业务命令的职责边界
7. 检查 `Taskfile` 是否提供 `deps`、`lint`、`test`、`build`、`run`、`release:dry`
8. 检查 `.goreleaser.yml` 是否能表达最小的多平台 CLI 发布配置
9. 检查 `.gitlab-ci.yml` 是否围绕 `task` 组织，而不是平行维护第二套构建逻辑
10. 检查 `README.md`、`AGENTS.md`、`ARCHITECTURE.md` 的导航与职责是否一致
11. 检查 `.gitignore`、`.env.example`、发布配置中没有引入真实敏感信息
12. 检查脚手架说明没有把 `GOCACHE`、`GOMODCACHE`、`GOPATH` 指向仓库内 `.cache/` 或其他项目级缓存目录
13. 用 `checklists/scaffold-delivery-checklist.md` 做交付自检

## Resources

- `scripts/init_scaffold.sh`：初始化 CLI 元工程骨架
- `assets/templates/`：可复用项目模板
- `references/repo-blueprint.md`：目录蓝图与依赖边界
- `checklists/scaffold-delivery-checklist.md`：交付自检项
