# Gateway Routing and UI-Managed Model Configuration

状态：目标设计与实施拆分
日期：2026-06-26

本文描述 TraceLab 最终态的入口、内部执行模式、上游能力、模型别名和 Monitor UI 配置体验。它不以当前过渡实现为约束，而是作为后续后端与前端收敛目标。当前实现事实仍以 [当前实现概览](./CURRENT_IMPLEMENTATION.md)、[Responses Server 设计](./RESPONSES_SERVER_DESIGN.md) 和 [模型广场与渠道管理设计](./v1/model-channel-management-design.md) 为准。

## 目标

TraceLab 对下游提供少数稳定入口，对上游接入多个 provider/channel，并在内部自动选择最合适的执行计划。用户主要在 Monitor UI 中管理渠道、模型、别名和策略；YAML 只保留启动、数据库、端口、认证等基础配置，以及首次 bootstrap 兼容能力。

目标体验：

- 下游客户端只需要把 base URL 指向 TraceLab，并继续使用自己选择的 API 入口。
- TraceLab 对外提供 `/v1/chat/completions`、`/v1/responses`、`/v1/messages` 三类主入口。
- 上游可以有多个 provider/channel 支持同一个模型名，TraceLab 按健康、权重、优先级、能力和策略负载均衡。
- 下游访问 `/v1/responses` 时，如果上游有 native Responses 能力则可以直通；如果只有 Chat Completions，TraceLab 自动作为 Responses server，把请求编排为上游 Chat Completions 调用。
- 模型别名由用户在 UI 中手动配置，运行时按实际模型名和别名共同选路。
- 每次请求都能在日志、Monitor 和 MCP 中看到可解释的 route plan，而不是只看到最终 upstream。

## 核心概念

### Client Entrypoint

下游请求进入 TraceLab 的 API 入口。首批固定支持：

- `chat_completions`：`POST /v1/chat/completions`
- `responses`：`POST /v1/responses`
- `anthropic_messages`：`POST /v1/messages`

入口决定客户端期望的响应协议。入口不是 provider，也不是上游能力。

### Execution Mode

TraceLab 对一次请求选择的内部执行方式：

- `proxy_pass`：上下游 API surface 兼容，直接代理、记录和索引。
- `responses_server`：下游是 Responses，上游是 Chat Completions，由 TraceLab 提供 Responses 语义、tool loop、状态、compact 和审计。

后续可以扩展 `messages_adapter` 或 `chat_adapter`，但首期不把所有协议互转泛化为默认行为。

### Upstream Capability

一个 channel 的事实能力，来自 UI 配置、provider probe、模型发现和人工修正：

- API surface：`chat_completions`、`responses`、`anthropic_messages`。
- 模型供应：真实模型名、启用状态、来源、上下文窗口、输出上限。
- 功能能力：stream、tool calling、structured output、embeddings、tokenize 等。

Capability 是路由硬约束。用户可以手动覆盖探测结果，但每次覆盖都应保留审计和 UI 提示。

### Model Alias

模型别名是下游模型名到候选真实模型名的映射。别名参与路由，但不改变下游入口语义。

默认别名是全局模型别名：

```text
abc -> gpt-5.5
```

请求 `model=abc` 时，候选模型集合为 `abc` 和 `gpt-5.5`。如果多个 channel 支持 `gpt-5.5`，它们都可以成为候选，再由路由策略负载均衡。

高级场景允许按 channel 收窄别名：

```text
abc -> openai-main:gpt-5.5
abc -> azure-main:gpt-5.5
```

这用于表达“同一个别名只允许走指定渠道”，避免把不同 provider 的同名模型错误混用。

### Route Plan

Route plan 是一次请求在真正发往上游前生成的可解释计划。它至少包含：

- `client_entrypoint`
- `requested_model`
- `resolved_model_candidates`
- `execution_mode`
- `upstream_endpoint`
- `selected_channel_id`
- `selected_route_target_id`
- `upstream_model`
- `strategy`
- `reason`
- `fallbacks_considered`

Route plan 应写入 recorder event、request audit/upstream exchange，并在 Monitor trace detail 中展示。

## 自动执行规则

### Chat Completions

当下游访问 `/v1/chat/completions`：

1. 解析 `requested_model`。
2. 展开模型别名，得到候选真实模型名。
3. 筛选启用且支持候选模型的 channel。
4. 筛选支持 `chat_completions` 的 channel。
5. 选择 `proxy_pass`。
6. 按 selected candidate 的 alias 规则改写上游 `model`，直通上游 Chat Completions。

如果没有 chat-capable channel，返回明确的 no route 错误，并在 decision trace 中说明模型候选、入口和缺失能力。

### Responses

当下游访问 `/v1/responses`：

1. 解析 `requested_model`。
2. 展开模型别名，得到候选真实模型名。
3. 按策略生成候选执行计划。
4. 在可用 plan 中按策略、健康、权重和优先级选择。

Responses 策略是系统级配置，默认建议为 `auto`：

| 策略 | 行为 |
| --- | --- |
| `auto` | 优先 native Responses 直通；没有合适 native plan 时自动使用 Chat Completions backend 进入 `responses_server`。 |
| `prefer_native` | 有 native Responses 时优先直通；无 native 时允许 `responses_server` fallback。 |
| `prefer_local_server` | 有 Chat Completions backend 时优先本地 `responses_server`；无 chat backend 时允许 native Responses 直通。 |
| `native_only` | 只允许 native Responses 直通，不做 Chat Completions 到 Responses 的语义补齐。 |
| `local_server_only` | 只允许本地 `responses_server`，不直通 native Responses。 |

`auto` 的默认顺序：

1. 选支持 `responses` 且模型匹配的 channel，使用 `proxy_pass` 到上游 `/v1/responses`。
2. 如果没有可用 native plan，选支持 `chat_completions` 且模型匹配的 channel，使用 `responses_server`，内部调用上游 `/v1/chat/completions`。
3. 如果两类 plan 都不存在，返回 no route。

如果请求携带 Codex 需要的 hosted tools、compact 或 TraceLab 必须接管的 server-side tool 能力，route planner 可以把 `requires_local_responses_runtime=true` 作为硬约束，使 `auto` 直接选择 `responses_server`。

### Anthropic Messages

当下游访问 `/v1/messages`：

1. 解析 `requested_model`。
2. 展开模型别名。
3. 筛选支持 `anthropic_messages` 的 channel。
4. 选择 `proxy_pass`。

首期不默认把 Anthropic Messages 自动转换到 Chat Completions 或 Responses。后续如需实现，应作为新的 execution mode 和显式策略加入，而不是隐藏在 proxy 路径中。

## UI 配置模型

### Provider Channels

Monitor 中的渠道配置页是上游配置主入口。

页面能力：

- 新增、编辑、禁用、删除 channel。
- 配置 provider preset、base URL、API key、自定义 headers。
- 展示 API key hint，不回显 secret。
- 设置权重、优先级、并发/容量提示、启用状态。
- 点击 probe，自动检测 API surface、模型列表和能力。
- 人工修正 probe 结果，例如声明某 channel 支持 Chat Completions 或关闭 tool calling。
- 保存后触发 router snapshot reload，不要求重启服务。

表单原则：

- 默认只展示常用字段：名称、provider、base URL、API key、启用状态。
- 高级字段折叠：headers、routing profile、API version、deployment、project/location、权重、优先级、capability override。
- Probe 结果以可编辑草稿方式展示，用户确认后写入 DB。

### Model Catalog

模型广场展示系统见过、发现或人工添加的模型。

页面能力：

- 按模型聚合展示 provider 覆盖、启用 channel 数、请求量、错误率、token 用量。
- 模型详情展示每个 channel 上的真实模型名、能力、启用状态、上下文窗口、输出上限。
- 允许人工添加模型到某 channel，适配不能列模型的 provider。
- 允许启用/禁用某 channel 上的模型。
- 展示该模型的别名和被哪些下游 profile 使用。

### Model Aliases

别名配置建议作为模型广场的一等功能，而不是散落在 YAML。

页面能力：

- 新增别名：输入下游模型名，选择一个或多个真实模型。
- 选择别名作用域：全局或限定 channel。
- 预览别名展开后的候选 channel。
- 显示冲突：别名与真实模型同名、别名循环、目标模型不存在、目标 channel 禁用。
- 支持禁用别名但保留配置。
- 展示最近使用该别名的请求和 route plan。

数据校验：

- 禁止别名循环。
- 禁止空目标。
- 允许别名指向当前尚未发现但人工确认的模型，但 UI 必须标记为 manual/unverified。
- 当全局 alias 和 channel-scoped alias 同时存在时，channel-scoped 只收窄候选，不覆盖其他真实模型的存在事实。

### Routing Settings

系统配置页面保留少量全局策略：

- Responses strategy：`auto`、`prefer_native`、`prefer_local_server`、`native_only`、`local_server_only`。
- Selection policy：`p2c`、`first_available`。
- Missing model policy：reject 或允许 fallback 到 unknown model channel。
- Probe 默认行为：手动、启动填补、定时刷新。
- Route plan logging level：normal 或 verbose。

这些是系统策略，不应要求用户在每个 channel 或每个模型上重复配置。

### Route Inspector

新增路由解释视图，用于调试“这个请求为什么走这里”。

入口：

- Trace detail 页面中的 route plan 面板。
- 模型详情页的“模拟请求”按钮。
- 渠道详情页的“为什么没有被选中”面板。

能力：

- 输入 endpoint、model、是否 stream、是否 tools。
- 输出候选模型、候选 channel、过滤原因、最终 execution mode 和上游模型名。
- 对历史请求展示实际 route plan 和候选排除原因。

## 后端任务拆分

### 1. Route Planner 核心

新增 `internal/routeplan` 或在 `internal/router` 中拆出 planner 层。

任务：

- 定义 `ClientEntrypoint`、`ExecutionMode`、`UpstreamEndpoint`、`RoutePlan`、`PlanCandidate`。
- 从 HTTP path/body 提取 request facts。
- 接入模型别名解析，得到 `resolved_model_candidates`。
- 按 entrypoint 和 strategy 生成 plan candidates。
- 对每个 candidate 记录过滤原因。
- 把最终 plan 交给现有 router selection 做负载均衡。
- 将 plan 写入 decision trace、recorder event 和 audit metadata。

验收：

- 单元测试覆盖 chat->chat、responses->responses、responses->chat server、messages->messages。
- 单元测试覆盖 strategy 差异和无路由错误。
- 单元测试覆盖 tool calling capability 过滤。

### 2. Model Alias Service

新增别名存储和解析服务。

任务：

- 新增 `model_aliases` 表和 migration。
- 支持全局 alias 和 channel-scoped alias。
- 提供 CRUD API。
- 提供 resolver：`requested_model -> []ResolvedModelCandidate`。
- 检测循环、重复、空目标和禁用目标。
- 让 router catalog 同时索引真实模型名和 alias 候选。

建议表结构：

```sql
CREATE TABLE model_aliases (
    id TEXT PRIMARY KEY,
    alias TEXT NOT NULL,
    target_model TEXT NOT NULL,
    channel_id TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    description TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_model_aliases_alias ON model_aliases(alias);
CREATE INDEX idx_model_aliases_target ON model_aliases(target_model);
CREATE INDEX idx_model_aliases_channel ON model_aliases(channel_id);
```

验收：

- 请求 `abc` 可以匹配支持 `abc` 的上游，也可以匹配 alias 指向的 `gpt-5.5`。
- 多 channel 支持同一 alias target 时参与负载均衡。
- channel-scoped alias 只允许指定 channel 参与。
- route plan 展示 alias resolution。

### 3. Channel Capability Store

完善 UI-managed channel 作为长期事实源。

任务：

- 将 channel config、channel models、capabilities、probe runs 作为 router snapshot 输入。
- API key 和 headers 使用 redaction 输出，写入时走 secret 处理。
- Probe apply 只填补缺失字段，不覆盖用户显式 false，除非 UI 明确选择 override。
- 支持手动能力覆盖和审计字段。
- Channel/model 修改后触发 router reload。

验收：

- 无需编辑 YAML 即可新增 provider 并使请求路由生效。
- UI 禁用 channel 或模型后，热路径不再选择它。
- Probe 失败不破坏已有配置。

### 4. Responses Auto Mode

把 `/v1/responses` 从“全局开关进入本地 handler”收敛为 route plan 决策。

任务：

- 新增 responses strategy 配置，存入 application settings。
- Route planner 根据 strategy 选择 native proxy 或 local server。
- Native Responses pass-through 继续用现有 reverse proxy recording。
- Local server plan 调用 Responses Runtime，并把 selected route target 和 upstream model 注入 runtime/model call。
- 当请求要求 hosted tools、compact、server-side tools 时，planner 标记 local runtime required。

验收：

- 同一个下游 `/v1/responses` 请求在 native channel 存在时可直通。
- native 不可用但 chat channel 可用时自动走 `responses_server`。
- `native_only` 下不会自动 chat->responses。
- `local_server_only` 下不会直通 native Responses。

### 5. Request/Body Rewrite Boundary

把模型名改写集中到 route plan 应用阶段。

任务：

- 统一 `client_model` 和 `upstream_model` 字段。
- Chat proxy、Responses native proxy、Responses server internal chat call 都使用同一模型改写 helper。
- Recorder metadata 同时记录 client model 和 upstream model。
- Replay 仍以 raw HTTP cassette 为准，不要求 alias resolver。

验收：

- 下游请求和上游 cassette 中的模型名差异可追踪。
- Monitor detail 能显示“client model abc -> upstream model gpt-5.5”。

### 6. Auditing and Observability

任务：

- 在 `.http` V3 `# event:` 中写入简洁 route plan 摘要。
- 在 application DB 中保存完整 route decision JSON 或结构化字段。
- Monitor trace detail 增加 route plan 面板。
- MCP 增加 route plan 查询字段。
- Selection failure 也记录候选排除原因。

验收：

- 任意失败请求都能解释没有选中上游的原因。
- 任意成功请求都能解释入口、模式、channel、上游 endpoint 和模型改写。

## 前端任务拆分

### 1. Providers 页面重构

任务：

- Channel 列表展示 enabled、provider、base URL、API surfaces、模型数、健康、最近 probe。
- Channel 编辑抽屉支持常用字段和高级字段。
- Probe 结果页展示 discovered capabilities 和 models，并允许用户确认应用。
- Capability override 使用明确的三态控件：auto、on、off。

### 2. Model Catalog 页面

任务：

- 模型列表按请求量、错误率、provider 覆盖、启用状态筛选。
- 模型详情展示 channel coverage 和 per-channel capability。
- 支持启用/禁用 channel model。
- 支持人工添加模型到 channel。

### 3. Alias 管理页面

任务：

- 别名列表展示 alias、targets、scope、enabled、最近使用。
- 创建/编辑别名时从 model catalog 选择目标，也允许输入 manual target。
- 显示候选 channel 预览和冲突警告。
- 支持从模型详情页快速创建 alias。

### 4. Routing Settings 页面

任务：

- 系统设置中增加 Responses strategy、selection policy、missing model policy。
- 每项配置显示当前值、默认值和影响范围。
- 保存后热更新 router/runtime config。

### 5. Route Inspector

任务：

- 新增模拟表单：endpoint、model、stream、tools、structured output。
- 调用后端 dry-run API 返回 route plan。
- 在 trace detail 中复用同一组件展示实际历史 plan。
- 对每个被过滤 candidate 展示原因。

## API 任务拆分

建议新增或稳定以下 Monitor API：

- `GET /api/channels`
- `POST /api/channels`
- `PATCH /api/channels/{id}`
- `POST /api/channels/{id}/probe`
- `POST /api/channels/{id}/probe/apply`
- `GET /api/models`
- `GET /api/models/{model}`
- `POST /api/channels/{id}/models`
- `PATCH /api/channels/{id}/models/{model}`
- `GET /api/model-aliases`
- `POST /api/model-aliases`
- `PATCH /api/model-aliases/{id}`
- `DELETE /api/model-aliases/{id}`
- `GET /api/settings/routing`
- `PATCH /api/settings/routing`
- `POST /api/routing/inspect`

API 返回值必须默认 redacted。涉及 secret 的写入口只接受新值，不回显旧值。

## 数据与迁移任务

任务：

- 增加 `model_aliases` 表。
- 增加 route decision 持久化字段或独立 `route_decisions` 表。
- 将 routing settings 存入 `app_settings` 或专用 settings 表。
- 确保 SQLite startup schema 和 Postgres migration 同步。
- Bootstrap legacy YAML upstreams 到 channel store，但 DB 一旦存在 channel 就以 DB 为准。
- 提供 dry-run migration/doctor 检查：alias 冲突、channel 无模型、capability 不一致。

## 测试计划

后端：

- Route planner table tests：覆盖入口、策略、能力、alias、tools、fallback。
- Router integration tests：多个 channel 同模型负载均衡、健康状态排除、channel-scoped alias。
- Proxy e2e tests：chat proxy、responses native proxy、responses server fallback、messages proxy。
- Store migration tests：SQLite raw DDL、Postgres migration gated tests。
- Audit tests：route plan 出现在 recorder event、request audit、upstream exchange。

前端：

- Component tests：provider form、alias form、route inspector。
- API mock tests：probe apply、alias conflict、settings save。
- Playwright smoke：新增 channel、probe、启用模型、创建 alias、inspect route。

非功能：

- 热路径不得每次访问 DB；router 使用内存 snapshot。
- Secret 不进入日志、trace event、route plan 或前端响应。
- No route 错误必须结构化且可解释。
- 单元测试不得依赖真实网络。

## 实施顺序

1. 定义 route plan 类型和 dry-run planner，不改变现有转发行为。
2. 增加 model alias store、resolver 和后端 CRUD API。
3. 把 router selection 接入 alias resolver，但只影响模型候选，不改变 execution mode。
4. 接入 Responses strategy 和 `/v1/responses` route plan，支持 native proxy 与 local server 自动选择。
5. 把模型改写、route plan event、audit 写入统一到 proxy 和 responses adapter。
6. 完成 UI：Providers、Models、Aliases、Routing Settings、Route Inspector。
7. 将 YAML upstream bootstrap 降级为首次导入路径，Monitor 成为长期配置主入口。
8. 补齐 doctor、MCP 查询和文档。

这个顺序的目标是让每一步都有可测试边界，同时最终形态仍保持“入口简单、配置在 UI、内部自动、路由可解释”。

