# Responses Server 设计

状态：Responses server 演进设计，Stage 3A/3B、Stage 4A/4B、Stage 5A/5B 与 Stage 10A 已部分落地
日期：2026-06-22

本文描述 TraceLab 从本地 proxy/record/replay 工具升级为 LLM gateway + OpenAI Responses API semantic server 的目标架构，并记录截至 2026-06-22 已经落地的 Responses server-mode 事实。当前通用能力仍以 [当前实现概览](./CURRENT_IMPLEMENTATION.md)、[架构说明](./ARCHITECTURE.md) 和 [项目基线](./PROJECT_BASELINE.md) 为准。

## 已落地实现截至 2026-06-22

当前已经落地的范围是可选的 `/v1/responses` 本地 server-mode，不改变默认 proxy 行为：

- 配置：新增 `responses_server` 配置块，字段包括 `enabled`、`default_model`、`force_store`、`max_request_body_bytes` 和 `path`。默认 `enabled: false`，默认 path 为 `/v1/responses`。
- server-mode path：当 `responses_server.enabled=true` 且请求路径等于配置的 Responses path 时，proxy handler 直接进入本地 Responses HTTP handler；非 Responses 请求仍走现有代理热路径。
- Chat Completions adapter：本地 Responses runtime 会把非流式 Responses 请求映射为内部上游 `POST /v1/chat/completions` 调用，由现有 router 选择目标 OpenAI-compatible upstream。
- cassette recording：server-mode 内部发起的上游 Chat Completions exchange 会经过 recorder，写入 `.http` V3 cassette；有 ent-backed audit store 时还会写一条 `upstream_exchanges`，把 response id、request audit、recorder request id、cassette path、route target、model、endpoint、status 和时间戳关联起来；本地 `/v1/responses` 入站调用本身不作为外部 upstream cassette 录制。
- ent-backed state store：新增 ent schema `responses` 和 `response_items`，`runtime.NewEntStore` 保存 response checkpoint、input/output item、`previous_response_id` 链和 `GET /v1/responses/{id}/input_items` 所需数据。handler 重启后，只要复用同一 store，`previous_response_id` continuation 可以跨 handler 重启工作。
- hosted web_search 首切：新增 `tools.web_search` 配置和 `internal/responses/tools/websearch` provider，支持 disabled/mock/SearXNG。开启后，非流式 Responses runtime 可把 `web_search` / `web_search_preview` 暴露为上游 Chat Completions function tool，执行 server-side search，并把 tool result 注入第二轮 Chat Completions；有 ent-backed audit store 时还会写 hosted web_search `response.tool_call` started/completed/failed events。
- SQLite/Postgres 当前状态：SQLite raw DDL 已创建 `responses` / `response_items` 表并支持本地 fallback；store 层可打开 `database.driver=postgres` 并创建 ent client。`ent/postgres-migrations` 已包含从 ent schema 生成并在 Postgres 17 dev database 上验证过的初始应用 schema SQL。Postgres `db migrate up` 已从 auth migrator 中拆出并归入应用库命令，且已通过 `internal/appdbmigrate` 和 `golang-migrate` 应用 checked-in SQL；serve/store startup 的 Postgres `auto_migrate` 仍保留 ent `Schema.Create` 兼容路径。详见 [Postgres Storage Migration](./POSTGRES_STORAGE_MIGRATION.md)。

当前明确未完成：

- streaming Responses server-mode。
- 完整 function tool call/tool result 生命周期。当前只支持非流式 hosted `web_search` 首切及其 started/completed/failed execution events。
- compact workflow。
- Stage 10A 已接入最小 Responses inbound request audit 写入：server-mode `POST /v1/responses` 会写 `request_audits` accepted/completed/failed/rejected 状态。Stage 11A 已接入内部 Chat Completions cassette 的最小 `upstream_exchanges` correlation。Stage 12A 已接入 request 与内部 model_call 的最小 `execution_events` 写入。Stage 13A 已接入核心 audit 查询服务、Monitor `/api/responses/audit/trace` 和 MCP `responses_audit_trace` 工具。Stage 14A 已接入 hosted `web_search` tool_call started/completed/failed events。Stage 15A 已把 upstream `api_type` / `mode` / capabilities 变成解析与路由约束，内部 Chat Completions 不会选择显式 Responses-native 且关闭 chat capability 的 target；Monitor UI 已有最小 Responses audit trace lookup，更完整的 function-tool/stream/cancel/compact events 尚未接入。
- 完整 Postgres migration 生产化。当前已有 checked-in 初始 SQL，且 Postgres `db migrate up` 会应用版本化 SQL；剩余缺口是 serve/store startup 的 Postgres `auto_migrate` 仍走 ent `Schema.Create`、SQLite 应用迁移仍未版本化、Postgres auth migration 未完成。
- provider auto-detect；provider capability 仍需显式配置或由已有渠道/模型数据表达。

## 背景与目标

TraceLab 当前的核心价值是把真实 LLM HTTP exchange 录制为可检查、可回放的 `.http` cassette，并用 SQLite 建立 Monitor/MCP 所需的索引。后续演进需要在保留这条能力链的基础上，为 Codex、OpenAI Responses API 客户端和本地 OpenAI-compatible Chat Completions 模型提供一个 Responses 语义服务端。

新目标不是把所有 provider 转成同一种协议，也不是替换现有代理热路径。目标是增加一个明确的 server mode：

- 对 `/v1/responses` 提供服务端 Responses 语义，包括状态、conversation item、tool loop、stream event normalization、compact 和审计。
- 当上游只有 OpenAI-compatible `/chat/completions` 时，由 TraceLab 自身作为 Responses server，把 Responses 请求编排成一次或多次上游 Chat Completions 调用。
- 非 Responses 路径继续走现有 protocol-aware proxy、routing、recording 和 replay 机制。
- 所有外部上游 HTTP exchange 仍可被录制为 `.http` cassette；内部执行过程另以 execution events 审计。

## 当前事实与不变量

这些约束在 Responses server 演进期间不能破坏：

- `pkg/replay` 是硬要求。旧 cassette 的单元测试 replay 不依赖网络、不依赖数据库、不要求运行 Responses runtime。
- `.http` raw cassette 仍是 replay 和 trace detail 的事实源。新录制写 `LLM_PROXY_V3`，读取端继续支持 legacy `LLM_PROXY_V2`。
- 现有 proxy 热路径保留。OpenAI Chat Completions、Anthropic Messages、Gemini、Vertex 等非 Responses semantic server 请求继续按当前 protocol-aware pass-through + recording 处理。
- 当前 TraceLab 不在普通代理热路径中做 OpenAI、Anthropic、Gemini、Vertex 之间的跨协议转换。
- SQLite 仍需兼容当前本地开发、Monitor、MCP 和测试基线。即使未来引入 Postgres-first 的 semantic store，也不能让旧 replay 或旧 `.http` 文件依赖 Postgres。
- `.http` payload 必须保持 human-inspectable。新增内部事件不能把 cassette 变成只适合机器消费的 opaque blob。
- 存储 schema 演进应 additive，启动初始化应能处理已有本地 DB。

## 目标架构上下文

目标系统拆成四个上下文，避免把 Responses 语义塞进现有反向代理：

| 上下文 | 职责 | 非职责 |
| --- | --- | --- |
| Gateway Routing | Provider/model 选择、endpoint capability 判断、认证与 URL 构造、fallback 策略、route decision 记录 | 实现 Responses conversation 状态或 tool loop |
| Proxy Recording | 外部 HTTP exchange 拦截、`.http` V3 写入、V2/V3 读取兼容、usage/timeline 派生、旧 replay 保持稳定 | 维护 Responses `previous_response_id` 语义 |
| Responses Runtime | `/v1/responses` semantic server、conversation item、tool orchestration、stream event normalization、compact、Responses 到上游 model client 的编排 | 作为所有协议的通用转换器 |
| Persistence/Audit | semantic state、execution events、request audit、route/upstream audit、Monitor/MCP 查询索引 | 替代 raw cassette 的 replay 事实源 |

### 请求路径分流

```text
client
  -> TraceLab HTTP server
     -> /v1/responses
        -> Gateway Routing selects provider/model
        -> Responses Runtime
        -> upstream model client, usually /chat/completions
        -> Proxy Recording captures external upstream exchanges
        -> Persistence/Audit stores semantic state and execution events
     -> non-/v1/responses
        -> existing proxy hot path
        -> Gateway Routing
        -> upstream provider endpoint
        -> Proxy Recording
```

`/v1/responses` 的 server mode 是新增能力；它不改变 `/v1/chat/completions`、`/v1/messages`、Gemini/Vertex endpoint 的既有代理语义。

## responses-gateway 吸收映射

参考仓库 `/Users/kingfs/repos/git.yongzhen.wang/llm/responses-gateway` 已经验证了一个面向本地模型的 Responses semantic gateway。TraceLab 应吸收设计与局部实现思想，但需要适配现有 proxy/record/replay 不变量。

| responses-gateway 区域 | TraceLab 目标位置 | 吸收方式 |
| --- | --- | --- |
| `internal/protocol/responses` | `pkg/llm` 或新的 `pkg/responses` protocol 包 | 作为 Responses DTO/事件形状来源。只放协议结构、JSON 编解码和兼容 helper，不放 routing、store 或 tool policy。 |
| `internal/orchestrator` | 新的 `internal/responses` runtime | 吸收 request planning、conversation replay、tool call round-trip、stream normalization、compact workflow。应依赖接口而不是直接依赖某个 provider adapter。 |
| `internal/adapter/vllm` | 泛化为 `internal/modelclient/openai` 或 provider adapter 层 | vLLM 只是 OpenAI-compatible Chat Completions 上游的一种 profile。adapter 应表达 Chat Completions model client 能力，并允许复用现有 `openai_compatible` routing profile。 |
| `internal/tools/websearch` | `internal/responses/tools` 下的 hosted tool runtime | hosted web search 可以作为第一批 server-side tool。必须保留可禁用配置、结构化错误和审计事件。 |
| `ent/schema` store | Persistence/Audit 的 Postgres-first schema 设计输入 | 可以借鉴 Response、Conversation、Item、RequestAudit 等实体，但不能直接把 Ent/Postgres 变成旧 cassette replay 的依赖。SQLite fallback/test schema 需保持轻量兼容。 |

吸收原则：

- DTO 可以较直接迁移；runtime 必须用 TraceLab 的 provider、router、recorder、store 边界重接。
- `adapter/vllm` 不应以 vLLM 命名成为核心概念。核心概念是 OpenAI-compatible Chat Completions model client，vLLM 只是 provider preset/profile。
- hosted tools 的执行必须产出内部 execution events，并能在 Monitor/MCP 中按 request、response、conversation、trace 关联。
- EntStore schema 可作为 Postgres-first 设计参考，但当前 `internal/store` SQLite 不应被一次性替换。

## `/v1/responses` Server Mode 行为

### Provider 支持 Responses 原生 endpoint

当 provider capability 声明支持 native Responses endpoint 时，TraceLab 可以选择 pass-through recording 或 semantic interposition：

- `mode: proxy`：按现有代理路径转发 `/v1/responses` 到上游 `/v1/responses`，只做 routing、recording、解析和索引。
- `mode: responses_server`：由 TraceLab 接管 Responses 语义。即使上游也支持 Responses，也可配置为由本地 runtime 统一处理状态、tools、audit 和 compact。

当前已先落地 `responses_server` + OpenAI-compatible Chat Completions 上游的非流式最小链路；native Responses pass-through 仍沿用普通代理路径，尚未实现完整 semantic interposition。

### 上游只有 Chat Completions

当 client 请求 `/v1/responses`，选中的 provider 是 OpenAI-compatible 且只暴露 `/chat/completions` 时：

1. TraceLab HTTP server 接收 Responses 请求，进行鉴权和 body limit；Stage 10A 在请求 body 成功 decode 后写入最小 `request_audits` inbound envelope。
2. Gateway Routing 根据 requested model、provider 配置、capabilities、model profile 选择支持 `chat_completions` model client 的 route target。
3. Responses Runtime 读取 `previous_response_id`、conversation item 和 request input，构造当前 turn 的 model context。
4. Runtime 将 Responses input、instructions、tools、tool choice、reasoning/metadata 等映射到 OpenAI-compatible Chat Completions 请求。
5. TraceLab 调用上游 `POST /chat/completions`。这个外部 exchange 进入现有 Proxy Recording 能力，写为 `.http` V3 cassette。
6. 当前 Runtime 输出非流式 OpenAI Responses 兼容 response；streaming events 尚未支持。
7. 当前 Persistence 写入 `responses` 和 `response_items` semantic state；Stage 10A 还会写入 `request_audits` 的 accepted/completed/failed/rejected 状态。Stage 11A 会为内部 Chat Completions cassette 写入最小 `upstream_exchanges` correlation。Stage 12A 会写入 request 与内部 model_call 的最小 `execution_events`。Stage 13A 提供核心 audit 查询服务、Monitor API 和 MCP 查询工具。Stage 14A 写入 hosted `web_search` tool_call events；Monitor UI 后续再接。

关键边界：

- client 看到的是 `/v1/responses` 语义；上游看到的是 `/chat/completions`。
- 上游 Chat Completions exchange 是外部 cassette；tool 执行、conversation mutation、compact 决策未来应成为内部 execution events。
- 非 `/v1/responses` 请求不进入 Responses Runtime。

### 非 Responses 路径

以下路径继续使用现有代理热路径：

- `/v1/chat/completions`
- `/v1/embeddings`
- `/v1/models`
- `/v1/messages` 与 Anthropic aliases
- Gemini / Vertex native endpoint
- management、Monitor、MCP API

这些路径可以共享 Gateway Routing 的 provider capability 数据，但不能被 Responses Runtime 隐式改写。

## Provider API Surface 配置

Provider 配置已经从“协议族 + routing profile”扩展到“协议族 + API 类型 + 运行模式 + 能力 + 模型画像”。当前代码已解析 `api_type`、`mode` 和基础 `capabilities`，并在路由选择时把 Chat Completions endpoint 的 API surface 当作 hard constraint。

当前字段：

```yaml
providers:
  - id: local-vllm
    display_name: Local vLLM
    base_url: http://127.0.0.1:8000/v1
    protocol_family: openai_compatible
    api_type: chat_completions
    mode: responses_server
    routing_profile: vllm_openai
    capabilities:
      chat_completions: true
      responses: false
      models: true
      tokenize: true
```

字段语义：

- `protocol_family`：wire protocol family，例如 `openai_compatible`、`anthropic_messages`、`google_genai`、`vertex_native`。
- `api_type`：provider 对当前 route 暴露的 API surface。当前接受 `chat_completions`、`responses`、`responses_native`、`messages`、`gemini_generate_content`。默认值按协议族推断：OpenAI-compatible 为 `chat_completions`，Anthropic 为 `messages`，Google GenAI / Vertex 为 `gemini_generate_content`。
- `mode`：TraceLab 对该 provider 的处理模式。当前接受空值、`proxy`、`record_only`、`server`、`responses_server`；空值保持历史兼容。
- `capabilities`：当前代码支持 `responses`、`chat_completions`、`embeddings`、`models`、`tokenize` 布尔能力，用于 routing 和 runtime plan，不应从 provider preset 中隐式猜测所有细节。
- `model_profiles`：仍是后续字段，计划用于模型上下文窗口、输出上限、tool 能力、compact 阈值、上游模型名映射和兼容性参数。

配置原则：

- 一个 provider 可以有多个 endpoint profile，但 routing 时必须明确选中与请求路径兼容的 profile。
- 本地 Responses server-mode 当前会通过内部 `/v1/chat/completions` 调用上游，因此需要至少一个可选择的 target 支持 `api_type: chat_completions`，或者显式 `capabilities.chat_completions: true`。
- 显式 `api_type: responses` / `responses_native` 且 `capabilities.chat_completions: false` 的 target 不会被内部 Chat Completions 选中。
- `api_type: chat_completions` 不等于 `responses_native`。由 TraceLab server mode 补齐 Responses 语义。
- 后续 `model_profiles` 会成为 Responses Runtime 构建 context、compact 和 Codex profile 建议的事实源。

## 存储策略

目标是 Postgres-first，但保留 SQLite fallback 和旧 replay 兼容。

截至 2026-06-22，当前实现已有 ent-backed runtime store，覆盖 `responses` 和 `response_items` 两张表。serve 装配时，如果 trace store 提供 ent client，则 Responses runtime 使用 `runtime.NewEntStore`；否则退回 memory store。SQLite raw DDL 已包含这些表以及 `request_audits`、`execution_events`、`upstream_exchanges`。Postgres store 可以通过 `database.driver=postgres` 打开并创建 ent client；`ent/postgres-migrations` 已有初始 schema SQL，Postgres `db migrate up` 已切到版本化 SQL migrator。Stage 10A 已接入 `request_audits` 的最小 inbound runtime 写入；Stage 11A 已接入内部 Chat Completions `upstream_exchanges` correlation；Stage 12A 已接入 request 与内部 model_call 的最小 `execution_events` 写入；Stage 13A 已接入核心 audit 查询服务、Monitor API 和 MCP 查询工具；Stage 14A 已接入 hosted `web_search` tool_call events；Stage 15A 已接入 upstream API surface 解析校验和 Chat Completions 路由约束；Stage 16A/16B 已接入 Postgres ent migration SQL 生成链路、初始 checked-in migration 和 CLI versioned SQL migrator。

Stage 6 的迁移职责需要按领域拆开：`db migrate` 是应用业务库迁移命令，覆盖 trace index、channel/model/routing 数据、Responses state 和后续 audit 表，并同时支持 SQLite fallback 与 Postgres-first 部署；`auth migrate` 继续只负责 users/tokens 等认证 schema。当前 Postgres `db migrate up` 已拆出为应用库版本化 SQL 路径，SQLite `db migrate up` 仍使用应用库初始化路径，`db migrate down` 在非 dry-run 下明确不支持。`internal/auth/migrate.go` 的 embedded migrations 仍只支持 SQLite。Postgres auth migration 是后续独立缺口，不应阻塞把 Responses ent store 归入应用库迁移域。

### Postgres-first semantic store

生产和长会话场景建议使用 Postgres 保存 Responses semantic state：

- `conversations`：thread/session/conversation identity、client metadata、created/updated time。
- `responses`：Responses API response id、status、model、route、usage、previous response link。
- `items`：input/output message、tool call、tool result、reasoning、summary、compact item。
- `request_audits`：入站 Responses request envelope、client request id、headers allowlist、redaction metadata、body hash/preview，用于说明 client 请求进入 runtime 前后的审计边界。
- `execution_events`：runtime plan、model call start/end、tool start/end、compact decision、stream lifecycle、cancel/error 等生命周期事件，用于解释一次 response 如何被编排出来。
- `upstream_exchanges`：semantic response/request 与外部 `.http` cassette、trace id、route target 的关联，用于把 Responses runtime 状态和现有 recorder 事实源连接起来。当前最小实现只覆盖内部 Chat Completions exchange，并在 response 完成后回填 semantic `response_id`；`trace_id` 暂使用 recorder prelude 的 `meta.request_id`。

Stage 10 后续 audit 接入顺序建议：

1. 补齐 Responses audit Monitor UI。
2. 再接通用 function tool lifecycle events、redaction 和 tool result persistence。
3. 最后补 streaming、cancel、compact 事件，因为这些事件对顺序、幂等和部分失败恢复要求更高。

Postgres-first 的原因：

- Responses conversation 和 execution events 需要事务、一致索引、并发更新和长时间查询。
- 未来 Monitor/MCP 需要按 conversation、response、tool、route、compact 维度查询。
- 本地 SQLite 可以继续服务轻量场景，但不应成为复杂 semantic runtime 的唯一设计约束。

### SQLite fallback/test compatibility

SQLite fallback 仍必须存在：

- 本地单机 quickstart 可无 Postgres 启动。
- 现有 Monitor、MCP、channel/model、trace index 基线继续工作。
- 单元测试可用 SQLite 或 memory store 验证 Responses Runtime，不要求外部服务。
- 旧 `.http` cassette replay 完全不依赖 DB；DB 只提供列表、索引、审计和 semantic state。

SQLite fallback 可以只实现 Stage 1 所需的最小 semantic 表，不要求承载大规模审计历史。

## 记录与回放策略

### 外部 exchange cassette

凡是 TraceLab 对外部 provider 发起的 HTTP model/tool provider 调用，都应按现有 record pipeline 写 `.http` cassette：

- `/v1/responses` server mode 内部调用上游 `/chat/completions` 时，记录上游 Chat Completions request/response。
- native Responses pass-through 时，记录上游 Responses request/response。
- cassette prelude 中追加 semantic correlation metadata，例如 `response_id`、`conversation_id`、`request_audit_id`、`execution_event_id`、`route_target_id`。
- `pkg/replay` 继续按 raw HTTP response 回放，不需要理解 Responses semantic store。

### 内部 execution events

Responses Runtime 的内部语义不适合全部塞进 raw HTTP cassette body，应写入 execution events：

- inbound `/v1/responses` request accepted/rejected。
- conversation load、previous response resolution、context assembly。
- model call plan、tool choice decision、compact trigger。
- hosted tool invocation、result、error、redaction。
- stream event normalization、client disconnect、cancel。
- response/item persistence result。

内部 events 可同步写入 Postgres/SQLite audit store，也可以在必要时把摘要投影到 `.http` V3 `# event:` 行，便于离线阅读和 trace detail 关联。投影必须保持简洁，不应让 cassette 变成完整数据库导出。

### Replay 分层

需要区分两类 replay：

- HTTP cassette replay：现有 `pkg/replay.Transport`，用于重放外部 HTTP response。必须保持兼容旧 V2/V3 文件。
- Semantic execution replay：未来可选能力，用 execution events 和 item store 复现 Responses Runtime 决策。它不能成为旧测试和旧 cassette 的前置条件。

## 分阶段实施计划与当前状态

下面保留原始演进计划，并补充截至 2026-06-22 的状态。未标注已落地的条目仍是目标，不代表当前支持。

### Stage 1A：设计与边界冻结（已落地）

产物：

- 本文档。
- provider 配置字段和 store 边界达成一致。
- 明确 `/v1/responses` server mode 不改变非 Responses proxy 热路径。

验收：

- 文档覆盖现状不变量、目标上下文、responses-gateway 吸收映射、server mode、provider 配置、存储、record/replay 和阶段计划。
- 文档检查通过 `rtk git diff --check`。

### Stage 1B：协议 DTO 与接口骨架（已落地）

产物：

- Responses DTO/stream event 类型。
- Responses Runtime 核心接口：store、model client、tool runtime、recorder hook、clock/id generator。
- Provider capability/model profile 配置结构。

验收：

- DTO round-trip fixture 覆盖非流式、流式、tool call、previous response。
- 不修改 `pkg/replay` 行为。
- 非 `/v1/responses` proxy 测试保持通过。

### Stage 1C：最小 `/v1/responses` 非流式 server（已落地）

产物：

- `POST /v1/responses` 非流式文本。
- OpenAI-compatible Chat Completions model client。
- memory/SQLite fallback semantic store。
- 外部 `/chat/completions` exchange cassette 关联。

验收：

- Codex/OpenAI Responses 风格最小请求可返回 Responses shape。
- 上游 Chat Completions 调用被录制为 `.http` V3。
- 旧 replay fixture 仍通过。

### Stage 1D：状态与 function tool round-trip（部分落地）

产物：

- `previous_response_id` conversation continuation。
- function tool schema 到 Chat Completions tools 的映射。
- tool call/result item 持久化。

验收：

- 多 turn 测试可以只发送增量 input。
- tool call 可被审计并和 response item 关联。
- provider 不支持 tool calling 时返回机器可读错误。

当前状态：`previous_response_id` continuation 和 `input_items` 查询已落地；完整 function tool round-trip、tool audit 和 provider tool capability 错误处理仍未完成。

### Stage 1E：streaming 与 cancel（未完成）

产物：

- Responses streaming event normalization。
- client disconnect/cancel 处理。
- stream lifecycle execution events。

验收：

- Codex 可消费 stream。
- usage、tool call delta、final response event 顺序稳定。
- cancel 不破坏已写 audit/cassette 关联。

### Stage 2 / Stage 6：Postgres-first Persistence/Audit（部分落地）

产物：

- Postgres semantic schema 和 migration。
- request audit / execution events / upstream exchange 查询。
- Monitor/MCP semantic diagnostics。
- `responses_server` 配置和装配默认 `enabled: false`，不改变现有 proxy 热路径。

验收：

- Postgres store 支持长 conversation、多 response 查询和 request correlation。
- SQLite fallback 仍能运行最小 server/test。
- 旧 `.http` replay 不连接 DB。

当前状态：ent schema、SQLite raw DDL、`runtime.NewEntStore`、Postgres 打开路径、Postgres 应用库 `db migrate up` versioned SQL 路径、最小 request audit 写入、内部 Chat Completions cassette 的最小 upstream exchange correlation、request/model_call/hosted web_search 最小 execution events，以及核心 audit 查询服务/Monitor API/MCP/UI 查询工具已落地；完整 Postgres migration 生产化仍未完成。Stage 6A/6B 的边界是先冻结迁移职责并拆出应用库命令：Responses ent store 属于应用库；`auth migrate` 属于认证库迁移命令，当前 embedded migrations 只支持 SQLite，Postgres auth migration 另行处理。

Stage 9 已在此基础上准备 `request_audits`、`execution_events`、`upstream_exchanges` schema 骨架，Stage 10A 接入 `request_audits` 的 inbound request accepted/completed/failed/rejected 写入，Stage 11A 接入内部 Chat Completions cassette 的最小 upstream exchange correlation 并回填 response id，Stage 12A 接入 request 与内部 model_call 的最小 execution events，Stage 13A 接入核心 audit 查询服务、Monitor `/api/responses/audit/trace` 和 MCP `responses_audit_trace` 工具，Stage 14A 接入 hosted `web_search` tool_call events，Stage 15A 接入 upstream API surface 校验与 Chat Completions endpoint capability 路由约束，Stage 16A/16B 接入 Postgres migration 生成和 CLI versioned SQL 应用路径。它不改变 `.http` cassette 作为 replay/detail 事实源的地位。后续 runtime 接入顺序建议先补通用 function tool events，再处理 streaming/cancel/compact events。

### Stage 3：Hosted tools 与 compact（部分落地）

产物：

- hosted `web_search` runtime。
- `/v1/responses/compact` 或 compact internal workflow。
- model profile 驱动的 context budgeting。

验收：

- hosted tool 可禁用、可审计、错误可读。
- compact 产出 summary/item，并保留原始 item lineage。
- Codex 长会话可通过 audit 解释 compact 行为。

当前状态：`tools.web_search` 配置、mock/SearXNG provider、非流式 hosted `web_search` tool loop 及其 started/completed/failed execution events 已落地。通用 function tool audit、streaming tool events、compact workflow 和 model profile 驱动的 context budgeting 仍未完成。

### Stage 4：高级 routing 与多 provider（未完成）

产物：

- model profile 驱动 fallback。
- 多 provider 策略路由。
- native Responses provider interposition/pass-through 策略。

验收：

- routing decision 能解释为什么选择 server mode、native Responses 或 plain proxy。
- fallback 不破坏 conversation consistency。
- Monitor/MCP 能按 provider/model/mode 展示成功率和失败原因。

## 总体验收标准

Responses server 演进完成后，应满足：

- `/v1/responses` 可以在上游只有 OpenAI-compatible Chat Completions 的情况下工作。
- 非 Responses endpoint 的现有 proxy/record/replay 行为不回退。
- 新 `.http` cassette 仍为 V3，旧 V2/V3 cassette 仍可被 `pkg/replay` 使用。
- 外部 HTTP exchange 和内部 semantic execution 有清晰关联，但 replay 不依赖 semantic DB。
- Provider 配置能明确表达 `protocol_family`、`api_type`、`mode`、`capabilities`、`model_profiles`。
- Postgres 可承载完整 Responses semantic state；SQLite 可作为 fallback 和测试兼容层。
- Monitor/MCP 能回答一次 Responses 请求经过了哪些 route、model call、tool call、compact 和错误。
