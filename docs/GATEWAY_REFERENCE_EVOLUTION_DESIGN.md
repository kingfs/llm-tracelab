# 面向网关生态的 TraceLab 演进设计

## 背景

近期对比了 `sub2api`、LiteLLM、Portkey Gateway、Helicone 等 LLM API 代理、聚合网关和观测平台后，需要重新校准 `llm-tracelab` 的产品方向。

结论是：TraceLab 不应该转向成为公网多租户 API 分发、计费和支付平台，但应该吸收成熟网关项目在渠道管理、调度、限流、健康、成本、可观测和治理上的设计，把这些能力服务于 TraceLab 的核心价值：真实记录、可复现、可审计、可回放。

## 参考项目观察

### Sub2API

本地参考仓库：`/data/src/github.com/Wei-Shaw/sub2api`

Sub2API 的定位是 AI API 网关平台，用于分发和管理 AI 产品订阅额度。其核心能力包括：

- 多上游账号管理，支持 OAuth 和 API Key。
- 平台生成 API Key，面向用户分发。
- token 级 usage、余额、订阅、倍率和配额计费。
- 用户级、账号级并发控制。
- 请求/token 速率限制。
- 分组、渠道、账号、模型映射和模型列表展示。
- 账号选择、故障切换、同账号重试、临时摘除、粘性会话。
- 代理池、连接池隔离、h2c、WebSocket、Codex/Gemini/Claude Code 特化兼容。
- 管理后台、运营统计、支付、充值、公告、外部系统集成。

值得 TraceLab 吸收的部分：

- 把上游从静态配置升级为可管理资源：渠道、账号、模型、探测、启停、优先级、权重。
- 调度状态要显式：为什么选中某个渠道，为什么失败，为什么切换。
- 限流与并发要分层：全局、用户/token、渠道、模型、上游账号。
- 粘性会话是 coding agent 场景的重要能力，特别是 Codex/Responses/Claude Code/Gemini CLI。
- 运营面不能只看请求列表，还要看模型、渠道、失败、额度、趋势。

不适合 TraceLab 直接复制的部分：

- 支付、充值、推广码、联盟、SaaS 用户增长体系。
- 公网中转站式多租户分发。
- 为了商业网关兼容而在热路径大量改写请求语义。
- 复杂账号代管、订阅资源转售、第三方风控规避能力。

### LiteLLM

公开定位：Python SDK 和 Proxy Server，面向 100+ LLM API，提供 OpenAI/native 调用格式、成本追踪、guardrails、负载均衡和日志能力。

值得吸收的部分：

- 统一模型入口和 provider adapter 生态。
- virtual key、budget、rate limit、spend tracking 的边界设计。
- fallback、retry、load balancing 作为网关基础能力。
- provider/model 能力矩阵和配置驱动接入。

TraceLab 应该借鉴其“模型网关控制面”，但不应该把“统一所有 provider 的调用 API”作为核心目标。TraceLab 可以对外兼容常见 API，但更重要的是保真记录 provider 差异。

### Portkey Gateway

公开定位：高性能 AI Gateway，强调多模型路由、guardrails、统一 API、治理策略。

值得吸收的部分：

- guardrails 应该在网关层可插拔。
- 路由、策略、fallback、审计应形成一个可解释的决策链。
- 治理不只是在响应后看日志，还应该支持请求前 preflight 和响应后 audit。

TraceLab 的差异化在于 cassette 和 replay：每个 guardrail 决策都应该能被 raw 证据重算或解释，而不是只存在于运行时日志。

### Helicone

公开定位：开源 LLM observability platform，用少量接入成本完成监控、评估和实验。

值得吸收的部分：

- 观测面要以开发者问题为中心：请求、会话、成本、延迟、错误、用户、实验。
- tracing、eval、prompt/version、dataset 可以从调用记录自然演化出来。
- UI 应该支持从列表快速钻取到行为证据，而不只是展示统计卡片。

TraceLab 已经更接近这条线。差异化是本地优先、raw cassette 为事实来源、可离线 replay。

## 重新定位

建议把 TraceLab 的一句话定位调整为：

> TraceLab 是本地优先、可自托管的 LLM API flight recorder 与调试网关：它代理和路由真实 LLM 流量，保留可回放的 raw cassette，解析模型行为，解释上游决策，并把真实调用沉淀为测试、审计和评估资产。

这个定位包含两层：

- 底座：record/replay、raw evidence、cassette、SQLite 派生索引、离线单测。
- 增强：多渠道路由、健康高可用、usage/cost、guardrails、session/eval、MCP agent 查询。

项目不应追求：

- 做公网 API 中转站。
- 做完整支付计费平台。
- 做 provider 协议互转平台。
- 做用户直接编程的新统一 LLM API。

项目应该追求：

- LLM 调用证据链最完整。
- 回放和单测最可靠。
- coding agent 行为最容易还原。
- 多上游路由和失败原因最可解释。
- 本地/团队自托管最轻量。

## 产品能力模型

建议将能力分为四层，避免功能膨胀。

### L0：Record/Replay Core

这是不可动摇的核心。

- raw `.http` cassette。
- `LLM_PROXY_V3` 写入和 V2 读取兼容。
- `pkg/replay.Transport` 离线回放。
- provider-aware request/response/stream/usage timeline。
- SQLite metadata index。

所有新能力都不能破坏 L0。

### L1：Observability Workbench

这是 TraceLab 的主要产品体验。

- request、session、upstream、model、channel 多视角。
- timeline、raw protocol、summary、tools、reasoning、refusal、safety、usage。
- failure clustering、system events、reanalysis。
- MCP 查询，让 AI agent 能直接分析 traces/sessions/failures。
- dataset/eval，把真实请求转为回归资产。

L1 是和 Helicone/Langfuse 类项目重叠但差异化最清晰的部分。

### L2：Debug Gateway

这是从 Sub2API/LiteLLM/Portkey 吸收的网关能力，但目标是调试、稳定和可解释，不是商业分发。

- channel/model 管理。
- model discovery 与人工模型启停。
- health-aware routing、P2C、priority、weight、capacity。
- bounded retry、fallback、probation、model-scoped health。
- per-token/per-user/per-channel concurrency 和 rate limit。
- sticky session。
- response/request guardrails。
- route decision trace。

L2 必须把每次决策写成可解释 metadata/event，而不是只改变转发结果。

### L3：Team/Enterprise Governance

这是可选增强，不能反向污染本地开发体验。

- 多用户和 personal token。
- 团队级 retention、redaction、export。
- policy packs。
- audit report。
- role-based access。
- optional Postgres。

L3 不包括支付、充值、推广码和公网售卖能力。

## 从 Sub2API 吸收的具体设计

### 1. 渠道和账号拆分

TraceLab 当前已有 `channel_configs`、`channel_models` 和 upstream runtime projection 的方向。建议进一步明确：

- Channel：一个用户可管理的上游配置，包含 base URL、provider preset、headers、模型发现、权重、优先级。
- Credential：一个 channel 下可选的认证实体，包含 API key、OAuth token 或 service account。
- Route Target：router 热路径使用的内存快照，由 Channel + Credential + Model Enablement 编译得到。

单人本地场景可以让 Channel 直接内联 Credential；团队场景再拆成一对多。

收益：

- 一个 provider 可配多个账号。
- 一个账号可有独立并发、冷却、失败状态。
- 路由决策能解释到“渠道”和“凭证”两个层级。

### 2. 粘性会话

Coding agent 常常依赖 provider-side conversation/session/cache。建议把 sticky session 作为 L2 必做能力：

- sticky key 来源：`Session_id`、`X-Codex-Turn-Metadata.session_id`、`X-Codex-Window-Id`、Responses `previous_response_id`、Anthropic/Gemini CLI 相关 metadata。
- sticky value：绑定到 route target 或 credential。
- TTL：默认 1 小时，可配置。
- 失败策略：同 target 短重试，确定不可用后记录 sticky break event 并切换。

cassette 事件建议：

- `routing.sticky.hit`
- `routing.sticky.miss`
- `routing.sticky.bind`
- `routing.sticky.break`

### 3. 并发和等待队列

Sub2API 对用户和账号并发分别建模，这点适合 TraceLab。

建议层级：

- global proxy wait slots。
- API token concurrency。
- channel concurrency。
- credential concurrency。
- model concurrency override。

本地模式可以默认关闭严格限制，只保留保护性上限。团队模式开启 token/channel/model 限制。

所有拒绝都应结构化：

- HTTP `429`：用户/token 限流。
- HTTP `503`：上游/channel/credential 暂不可用。
- event：`limit.queue_saturated`、`limit.concurrency_rejected`、`limit.rate_rejected`。

### 4. 计量而非支付

Sub2API 的 billing 模型很完整，但 TraceLab 应只吸收“计量”和“预算保护”。

建议实现：

- usage ledger：按 request/session/token/channel/model/day 聚合。
- cost estimate：基于 model price catalog 估算成本。
- budget guard：本地/团队可设置每日或每月预算，只用于提醒或拒绝。
- spend diff：比较 provider 返回 usage 与本地解析 usage。

不实现：

- 充值。
- 余额账户。
- 订阅套餐。
- 支付 webhook。
- 推广码。

### 5. 模型列表响应控制

Sub2API 支持分组自定义 `/v1/models` 展示。TraceLab 可以轻量吸收：

- `/v1/models` 返回当前 enabled model catalog。
- 支持按 token/policy/channel 过滤。
- 模型项附带 route availability、last_seen、capability hints。
- 明确“展示列表不等于协议能力承诺”，真实能力仍以 provider-aware parser 和 channel metadata 为准。

### 6. Failover 状态机

TraceLab 已经实现多 upstream 高可用的多个阶段。建议继续向 Sub2API 的可运营形态靠近：

- 同 target retry：网络抖动、429/503/502 可短重试。
- target switch：同模型其他渠道 fallback。
- temporary unschedule：失败 target 进入 cooldown。
- probation：恢复后只放一个探测请求。
- model-scoped health：一个模型失败不拖垮整个渠道。
- reason vocabulary：统一失败分类，供 Monitor/MCP 聚合。

不要做：

- 流式响应已经输出后自动切换 provider。
- 跨 provider 自动重写语义以“强行成功”。

## TraceLab 差异化功能设计

### Route Decision Timeline

每个请求应记录完整路由决策链：

- request model/endpoint/provider family。
- candidate channels。
- filtered reason：disabled、model_missing、unhealthy、rate_limited、concurrency_full、policy_denied。
- selected target。
- retry/fallback attempts。
- final outcome。

这应该成为 `LLM_PROXY_V3` 的事件，而不是只存在 SQLite。

建议事件：

- `routing.classified`
- `routing.candidates`
- `routing.filtered`
- `routing.selected`
- `routing.retry_wait`
- `routing.fallback`
- `routing.outcome`

### Trace-to-Test

这是 TraceLab 相比通用网关最重要的护城河。

建议补齐：

- 从 Monitor 选择 trace/session 生成 replay fixture。
- 生成 Go test snippet，直接使用 `pkg/replay.Transport`。
- session 级 cassette bundle。
- fixture metadata：provider、model、capability tags、risk findings、usage。
- 可选择“冻结响应”或“只保留协议形状”。

### Agent Behavior Audit

TraceLab 应把 coding agent 场景作为重点，而不是泛泛的聊天监控。

重点识别：

- shell 命令和危险参数。
- 文件读写/删除/权限修改。
- 网络访问、下载、执行链。
- secrets 泄露。
- tool call 参数与 tool result 错误。
- model 自主规划和后续行动。

输出：

- request detail 的 Audit tab。
- session detail 的 risk timeline。
- MCP 查询危险调用、敏感信息、失败聚类。

### Replay-Safe Guardrails

guardrails 不能只做在线拦截。每个规则应满足：

- 有 rule id/version。
- 有 evidence span 或 structured evidence。
- 可从 raw cassette 重新计算。
- preflight 拦截和 postflight 审计都写 event。

首批规则：

- API key/secret pattern。
- dangerous shell command。
- destructive file operation。
- external network operation。
- large hidden context/prompt anomaly。
- model refusal/safety block。

### Local Cost And Budget

成本不是为了收费，而是为了调试和控制。

页面：

- 今日/7 日/30 日 token 和估算成本。
- 按模型、渠道、session、API token 分解。
- cache tokens、TTFT、tokens/s。
- provider usage 与解析 usage 不一致提示。

策略：

- soft budget：只告警。
- hard budget：拒绝新请求。
- per-session budget：适合 coding agent 防失控。

## 建议路线图

### Phase A：产品边界固化

目标：把项目叙事从“多 upstream 代理”明确升级为“flight recorder + debug gateway”。

任务：

- 更新 README、README_EN、ROADMAP、v1 product vision。
- 明确非目标：支付、充值、公网中转、多租户售卖。
- 把本文件作为 roadmap 参考入口。

验收：

- 新贡献者能在 10 分钟内理解项目不追 Sub2API 的商业网关方向。

### Phase B：Route Decision Timeline

目标：让每次路由选择可解释、可回放、可聚合。

任务：

- 定义 routing event schema。
- proxy/router 将候选、过滤、选中、retry、fallback、outcome 写入 cassette。
- SQLite 索引 route attempts 摘要。
- Monitor trace detail 展示 route timeline。
- MCP failure clustering 使用 routing event。

验收：

- 任意一次 502/429/模型缺失都能解释为什么没有其他渠道可用。

阶段进度：

- 已实现 router 侧 `DecisionTrace`，包含模型、endpoint、策略、排除列表、候选渠道、过滤原因、可用数量、选中渠道和失败原因。
- 已在 proxy 录制 `routing.classified`、`routing.candidates`、`routing.selected`、`routing.filtered`、`routing.outcome` 事件，并保留旧的 `routing.selection`、`routing.retry_*`、`routing.failure` 事件兼容面。
- 已新增 MCP `query_routing_decisions` 工具，直接从 trace cassette 的 V3 prelude events 提取路由分类、候选、选中、结果和失败原因，方便 AI agent 不读取 raw HTML 或完整 trace 也能解释路由。
- 已在写入 `routing.candidates` 事件前对候选 `base_url` 做安全脱敏，移除 URL userinfo 密码并替换 query 中的 key/token/secret/password/signature 等敏感参数值。
- 已覆盖 router 决策 trace 单测和 proxy e2e cassette 事件断言。
- 当前阶段只把决策写入 cassette 并开放 MCP 查询；Monitor 专用展示和聚合仍是后续工作，避免在事件词汇稳定前过早扩大前端改动。

阶段复盘：

- 这一步强化的是 TraceLab 的 replay/debug/audit 核心，不改变请求转发选择算法，不改变 raw payload，不影响 `pkg/replay`。
- 下一步应优先在事件词汇稳定后做 sticky session / credential 决策链，或者把现有 routing events 暴露到 Monitor/MCP；不应跳到支付、充值或公网分发能力。

### Phase C：Credential 和 Sticky Session

目标：补齐 coding agent 场景下的账号级调度能力。

任务：

- 在 channel 之下引入 optional credential model。
- 支持 per-credential concurrency、health、cooldown。
- sticky session store。
- sticky break event。
- UI 显示 sticky 命中和切换。

验收：

- 同一 Codex/Claude Code/Gemini CLI session 能稳定绑定到同一 target。
- 目标失败后能解释何时打破 sticky。

### Phase D：Usage Ledger 和 Budget Guard

目标：吸收 billing 的计量价值，避免走向支付平台。

任务：

- 增加 usage ledger 派生表或 rollup。
- 引入 model price catalog，可本地内置并支持手动覆盖。
- Monitor 增加 cost estimate。
- soft/hard budget policy。
- budget event 写 cassette 和 system events。

验收：

- 用户能知道某个 session 花了多少 token/估算成本。
- budget 拒绝不会破坏 replay，且原因可审计。

### Phase E：Replay-Safe Guardrails

目标：把安全策略做成可重算审计资产。

任务：

- 规则引擎接口。
- 首批 deterministic rules。
- preflight/postflight event。
- Audit tab。
- MCP query guardrail findings。

验收：

- 同一 raw cassette 重分析能得到同一批 deterministic findings。

### Phase F：Trace-to-Test 和 Dataset

目标：把真实调用沉淀为测试和评估资产。

任务：

- Monitor 中选择 trace/session 导出 fixture。
- 生成 Go replay test snippet。
- dataset grouping。
- eval run 复用 cassette 输入和期望输出。

验收：

- 从一次真实失败请求到新增离线回归测试的路径不超过 3 个操作。

## 架构边界调整

建议逐步形成这些稳定边界：

- `internal/channel`：管理 channel、credential、model enablement、probe。
- `internal/router`：只消费内存快照，负责选择、健康、并发、sticky。
- `internal/policy`：budget、rate limit、guardrails 的规则和决策。
- `internal/ledger`：usage/cost 聚合，不处理支付。
- `internal/analyzer`：从 cassette 重算 observation、audit、risk。
- `pkg/recordfile`：继续只关心格式兼容。
- `pkg/replay`：不依赖 SQLite、不依赖 router、不依赖 policy。

关键约束：

- 热路径不能每次请求查数据库。
- policy 决策必须写入事件。
- analyzer 失败不能影响 proxy/replay。
- raw cassette 永远是事实来源。

## UI 信息架构建议

Monitor 应从“请求列表工具”升级成“LLM 调试工作台”：

- Overview：请求、错误、token、成本、风险、上游健康。
- Requests：单请求列表。
- Sessions：coding agent/task 视角。
- Models：模型广场、模型趋势、可用渠道。
- Channels：渠道配置、探测、健康、模型启停。
- Routing：路由决策、fallback、open/probation、queue pressure。
- Audit：风险 findings、敏感信息、危险工具调用。
- Datasets：trace/session 到测试/eval。
- System Events：TraceLab 自身异常。

避免做：

- 充值中心。
- 订单管理。
- 套餐售卖。
- 推广和联盟。

## 决策原则

后续判断一个网关功能是否该进 TraceLab，可以用这组问题：

1. 它是否提升 replay、debug、audit、eval 中至少一个核心价值？
2. 它的行为是否能写入 cassette 或从 cassette 重算？
3. 它是否保持本地优先和自托管轻量？
4. 它是否避免把项目推向公网多租户商业中转？
5. 它是否能解释 provider 差异，而不是强行抹平差异？

如果答案是否定的，应放弃或作为外部集成，而不是进入核心。

## 参考链接

- Sub2API 本地仓库：`/data/src/github.com/Wei-Shaw/sub2api`
- Sub2API GitHub：https://github.com/Wei-Shaw/sub2api
- LiteLLM GitHub：https://github.com/BerriAI/litellm
- Portkey Gateway GitHub：https://github.com/Portkey-AI/gateway
- Helicone GitHub：https://github.com/Helicone/helicone
