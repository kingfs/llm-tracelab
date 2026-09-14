# Responses Gateway 最终收敛计划

状态：最终态收敛总设计
日期：2026-06-23

本文替代此前“小阶段、小切片”的完成计划。后续不再以中间状态为目标，也不再围绕 SQLite fallback 做主线设计；本轮收敛的唯一目标是把 `llm-tracelab` 重构为 Postgres-first、Responses API server mode 可生产部署、且保留既有 proxy/record/replay 优势的 LLM gateway。

事实基线仍以 [当前实现概览](./CURRENT_IMPLEMENTATION.md)、[项目基线](./PROJECT_BASELINE.md)、[Responses Server 设计](./RESPONSES_SERVER_DESIGN.md) 和 [Postgres Storage Migration](./POSTGRES_STORAGE_MIGRATION.md) 为准。但这些文档中凡是把 SQLite local-first fallback 表述为长期主线边界的内容，都应在本轮最终收敛中改写为 legacy/dev 兼容说明，而不是生产目标。

## 最终完成定义

本轮重构完成时，必须同时满足以下条件：

1. TraceLab 是一个生产级 LLM gateway，不再只是 LLM API proxy。
2. 当模型没有可直通的 native Responses upstream 时，TraceLab 以本地 Responses execution mode 稳定作为 OpenAI Responses API semantic server 运行；当上游是 OpenAI-compatible Chat Completions（例如 vLLM）时，由本地 runtime 编排 model call、tool loop、compact、stream、state 和 audit，而不是把 `/v1/responses` 透传给上游。
3. 非 Responses 请求继续走 protocol-aware proxy、routing、recording、recordfile parser、Monitor/MCP 和 `pkg/replay` 路径；`.http` V2/V3 cassette 兼容性不被破坏。
4. Postgres 是唯一生产主路径。application/auth/runtime/audit/read-model schema、migration、health check、doctor、deployment 和测试门禁均以 Postgres 为一等目标。SQLite 只作为 legacy/dev/test 兼容，不再参与生产架构取舍。
5. Provider 配置明确表达 `api_type`、`mode`、`protocol_family`、capabilities 和 model profile；provider detection/onboarding 可保守补齐缺失信息，但不能覆盖用户显式配置或 capability false。
6. Runtime model profile 的事实源、adoption、冲突处理、禁用和回滚有正式管理面，不再停留在 observe-only 诊断。
7. Hosted/server-side tools 的执行边界明确：已实现工具可审计、可查询、失败路径稳定；未实现的 MCP/file/code/computer-use 不伪造执行结果，必须 rejected + audit，真实执行器必须另有安全边界后再接入。
8. `.http` cassette 仍是 replay 和 raw detail 的事实源；Responses semantic state 不能替代 raw cassette。
9. Docker/Compose/README/operator docs 展示的默认部署是 Postgres-backed gateway，可选 SearXNG，OpenAI-compatible upstream，以及 Responses API server mode。

## 当前事实判断

代码已经具备大量首切能力：

- Responses server-mode over Chat Completions upstream。
- `previous_response_id` continuation、runtime store、input_items、compact、auto compact、token-budget compact、compact provenance。
- Chat Completions SSE cassette 记录与聚合，Responses streaming 覆盖简单文本、ordinary function arguments、client-owned `function_call_output` continuation、registered executor、hosted `web_search`、auto compact 后多条真实增量路径，以及 unsupported/fallback contract。
- Hosted `web_search`、server-side function executor registry、YAML `static_response` / `external_command` opt-in executor、轻量 process policy。
- `request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`，以及 CLI/Monitor/MCP 查询首切。
- Provider `api_type` / `mode` / capabilities routing boundary、probe/report/apply、setup validate/apply、native Responses pass-through vs local server-mode boundary。
- Postgres checked-in migrations、`db migrate up/status`、open-vs-migrate、Postgres DSN-gated tests；临时 Postgres 17 容器上已通过 `go test -p 1 ./internal/store ./internal/responses/runtime ./internal/appdbmigrate ./internal/auth ./cmd/server -count=1`。

但项目还没有按最终产品形态收敛，主要问题是代码和文档仍同时表达两个目标：旧的 SQLite local-first record/replay proxy，以及新的 Postgres-first Responses gateway。后续只按后者收敛。

## 五个正交大块任务

### 1. `storage/postgres-production`

目标：把 Postgres 变成生产存储、迁移和运维的唯一主路径。

责任边界：

- `internal/store`
- `internal/appdbmigrate`
- `internal/auth`
- `cmd/server/db.go`
- `cmd/server/auth.go`
- `cmd/server/doctor.go` 中 store/migration 相关检查
- `ent/schema/**`、`ent/postgres-migrations/**`
- 相关 storage docs

必须交付：

- application/auth/runtime/audit/read-model 全部以 Postgres migration 为生产事实源。
- application migration 与 auth migration namespace 拆清楚；当前 shared namespace 必须被独立 auth namespace 或明确的 application-owned auth schema 合同替代，不能再处于“设计态字段”。
- `database.driver=postgres` 的 fresh DB 能完成 migration、启动、auth bootstrap、Responses runtime、Monitor/API 查询。
- 所有 runtime/Monitor/eval/experiment/routing/audit analytics 查询以 Postgres SQL 为主验证对象。
- SQLite 相关代码保留时只能标注 legacy/dev/test，不得继续驱动生产设计或文档主路径。

验收门禁：

- Fresh Postgres DB：`db migrate up`、`auth migrate up/status`、serve startup、doctor `--check-db`。
- `LLM_TRACELAB_TEST_POSTGRES_DSN=... go test -p 1 ./internal/store ./internal/appdbmigrate ./internal/auth ./cmd/server -count=1`。
- 默认测试仍离线，不要求真实 Postgres。

### 2. `runtime/responses-server-complete`

目标：TraceLab 自身成为稳定的 OpenAI Responses API semantic server。

责任边界：

- `internal/responses/runtime`
- `internal/responses/httpapi`
- `internal/responses/chatclient`
- `internal/proxy/responses_server.go`
- `internal/responses/tools/**`
- `internal/responses/functionexec`
- Responses/Codex fixtures

必须交付：

- `/v1/responses` create/retrieve/input_items/compact/stream 的本地 semantic contract 固化。
- continuation、conversation state、previous_response_id、function_call/function_call_output、tool loop、compact boundary 一致。
- streaming event ordering、已输出 SSE 后的 error/cancel/final response 存储行为稳定。
- auto compact 与 context optimization 作为 runtime 核心能力，而不是 fallback 特例。
- supported hosted/server-side tools 真实执行并审计；unsupported MCP/file/code/computer-use 统一 rejected + audit。
- Native Responses provider 在 proxy mode 可透传；local server-mode 仍走本地 semantic runtime，不混用。

验收门禁：

- `go test ./internal/responses/runtime ./internal/responses/httpapi ./internal/proxy -count=1`。
- `task test:codex-fixtures`。
- Responses server-mode e2e：OpenAI-compatible Chat Completions upstream、streaming、tool loop、compact、audit/upstream exchange/cassette correlation。

### 3. `provider/control-plane`

目标：让 provider/channel/model 配置成为 gateway control plane，而不是 bootstrap YAML 附属物。

责任边界：

- `internal/router`
- `internal/channel`
- `internal/providerprobe`
- provider/channel/model store APIs
- `cmd/server/provider.go`
- `cmd/server/models.go`
- Monitor provider setup/apply API
- provider docs/config examples

必须交付：

- Provider 必须能表达或探测 `api_type`、`mode`、`protocol_family`、capabilities。
- OpenAI-compatible Chat Completions、native Responses、Anthropic Messages、Gemini/Vertex API surface 进入同一 capability registry。
- Probe/setup/apply/onboarding 形成闭环：只填缺失字段，不覆盖显式配置或 capability false，不回显 secret。
- Model profile 与 provider/channel/model catalog 关系定稿：context window、max output、tool capability、upstream model rewrite、tokenize capability 的来源可解释。
- `responses_server.adopt_channel_model_profiles` 从“runtime opt-in + observe-only report”推进为正式管理面：adopt、conflict、disable、rollback、audit。

验收门禁：

- Native Responses pass-through 与 local Responses server-mode routing 不回退。
- capability false 阻断、显式配置优先、profile conflict skip/adopt/rollback 均有测试。
- `models codex-config`、doctor drift、runtime assembly 对 profile source/adoption 表述一致。

### 4. `observability/replay-contract`

目标：保留 TraceLab 的核心差异化能力：可记录、可回放、可审计、可排障。

责任边界：

- `internal/recorder`
- `pkg/recordfile`
- `pkg/replay`
- `internal/responses/audit`
- Monitor/MCP audit read APIs
- trace/session/request/response/upstream/tool correlation

必须交付：

- `.http` V3 是新写入事实源，V2 继续可读；Responses semantic state 不能替代 raw cassette。
- 任意 Responses request 能追到 request audit、execution events、tool_call_audits、upstream_exchanges、raw upstream cassette、final response。
- 非 Responses proxy 热路径继续 record/replay 可用。
- Audit read model 面向 Postgres，支持 response/request/conversation/client request/tool/upstream 维度。
- Monitor/MCP/CLI 不另建事实源，只查询统一 read model。

验收门禁：

- `go test ./pkg/recordfile ./pkg/replay ./internal/recorder ./internal/responses/audit ./internal/mcpserver ./internal/monitor -count=1`。
- Responses server-mode 内部 Chat Completions cassette 与 semantic audit correlation e2e。
- 旧 cassette replay 兼容测试不回退。

### 5. `ops/production-packaging`

目标：把最终系统包装为可部署的 Postgres-backed production gateway。

责任边界：

- `Dockerfile`
- `docker-compose.yml`
- `config/config.yaml`
- `config/examples/**`
- `README.md`
- `README_EN.md`
- `docs/CURRENT_IMPLEMENTATION.md`
- `docs/PROJECT_BASELINE.md`
- `docs/POSTGRES_STORAGE_MIGRATION.md`
- `docs/RESPONSES_SERVER_DESIGN.md`
- operator docs

必须交付：

- 默认生产部署示例是 app + Postgres + 可选 SearXNG。
- 配置示例展示 OpenAI-compatible/vLLM upstream + `responses_server` 配置块 + Postgres DSN。
- README/README_EN 不再把项目描述为“只是本地 record/replay proxy”或“SQLite 主路径”；应描述为 Postgres-first LLM gateway with record/replay。
- doctor/config inspect/audit CLI 只服务生产闭环，不再扩散为外围展示功能。
- 未实现能力明确写为 rejected/audited 或 future secure executor，不伪造支持。

验收门禁：

- `task build`。
- compose 启动文档可按 fresh Postgres 路径执行。
- 文档中的 storage/provider/runtime 事实与代码测试一致。

## 合并顺序

负责人按以下顺序合并大块成果：

1. `storage/postgres-production`
2. `provider/control-plane`
3. `runtime/responses-server-complete`
4. `observability/replay-contract`
5. `ops/production-packaging`

理由：

- Storage 先定，否则 runtime/audit/provider 的最终事实源会反复改。
- Provider 在 Runtime 前收敛，否则 runtime backend selection 与 profile source 会继续摇摆。
- Runtime 在 Provider/Storage 之后收敛，避免继续为中间态兼容扩复杂度。
- Observability 在 runtime contract 稳定后统一 correlation 和 read model。
- Ops/docs 最后统一表达最终产品，不再记录过渡状态。

## Agent 执行规则

- 每个大块任务使用独立 git worktree/branch。
- 每个 worker 只拥有自己大块的文件和职责边界；不得修改其它大块的设计决定。
- worker 必须知道仓库中可能存在并行改动，不得 revert 他人修改。
- 大块分支完成后由负责人 review、合并、清理 worktree/branch。
- 不编辑 `ent/dao/**`；schema 变化必须从 `ent/schema/**` 和 migration/generate 流程进入。
- 不破坏 `.http` V2/V3、`pkg/replay`、普通 proxy 热路径。
- 默认测试不依赖真实 provider；真实 Postgres 测试必须 DSN-gated 或由 compose/e2e 明确提供。

## 最终冻结门禁

停止新增功能后，必须一次性运行：

- `rtk env -u GOROOT task check:quick`
- `rtk env -u GOROOT task test`
- `rtk env -u GOROOT task build`
- `rtk env -u GOROOT task test:codex-fixtures`
- `LLM_TRACELAB_TEST_POSTGRES_DSN=... rtk env -u GOROOT go test -p 1 ./internal/store ./internal/responses/runtime ./internal/appdbmigrate ./internal/auth ./cmd/server -count=1`
- Postgres-backed gateway smoke：fresh DB migration、auth bootstrap、serve startup、Responses create/stream/tool/compact over OpenAI-compatible test upstream、non-Responses proxy record/replay。

最终冻结条件：

- 工作区干净。
- README、README_EN、CURRENT_IMPLEMENTATION、PROJECT_BASELINE、RESPONSES_SERVER_DESIGN、POSTGRES_STORAGE_MIGRATION 与本计划一致。
- Postgres 是生产主路径；SQLite 只作为 legacy/dev/test 兼容说明。
- 未实现 MCP/file/code/computer-use 真实执行器仍 rejected + audit，不伪造执行结果。
- `.http` cassette replay 和普通 proxy 热路径通过最终测试。
