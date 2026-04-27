# Go CLI Meta Scaffold Delivery Checklist

交付或验收一个基于 `go-cli-meta-scaffold` 的仓库前，至少检查以下项目：

- `cmd/<cli-name>/` 是否采用 `main.go + root.go + version.go + completion.go + 业务命令文件` 的分文件结构
- `main.go` 是否仅负责启动，不承载具体命令逻辑
- `Taskfile.yml` 是否包含 `deps`、`lint`、`test`、`build`、`run`、`release:dry`
- 初始化说明是否明确要求先执行 `task deps` 或 `go mod tidy`
- `.goreleaser.yml` 是否存在，且最小构建目标、归档规则、版本注入参数明确
- `.gitlab-ci.yml` 是否围绕 `task` 组织，而不是平行维护另一套构建脚本
- `internal/config` 是否集中处理配置文件与环境变量，不在业务命令中散落读取逻辑
- `internal/output` 是否集中管理输出，而不是不同命令各自打印格式
- `README.md`、`AGENTS.md`、`ARCHITECTURE.md` 是否对命令结构、配置流和发布流有一致描述
- `.gitignore` 是否忽略 `dist/`、`coverage/`、release artifacts、completion 临时输出
- `.env.example` 是否只保留占位配置，不包含真实密钥
- 文档和脚本中是否没有把 `GOCACHE`、`GOMODCACHE`、`GOPATH` 指向仓库内目录
- 至少验证过初始化脚本语法，并成功生成一份示例工程结构
