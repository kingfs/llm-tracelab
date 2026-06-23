# 架构说明

## 目标

`llm-tracelab` 的目标是把真实 LLM HTTP 调用录制成可检查、可回放、可重算的事实，并以 Postgres-first application DB 承载生产级 gateway 状态、配置、审计和查询。

架构原则：

- 转发路径保持简单可靠。
- raw cassette 是 replay 和详情页的事实源。
- Postgres 是生产结构化查询源；SQLite 只作为 legacy/dev/test fallback。
- 协议解析和审计结果可以从 raw cassette 重建。
- 测试 replay 不依赖上游网络。

## 数据流

1. SDK、CLI 或应用把 LLM API 请求发到本地代理。
2. 代理根据请求路径和上游配置识别协议族。
3. router 选择一个支持该协议和模型的 route target。
4. proxy 对请求做少量透传型调整，例如为 OpenAI-compatible chat stream 补 `stream_options.include_usage=true`。
5. 请求转发到上游。
6. recorder 把原始 HTTP 请求/响应写入 `.http` cassette。
7. `pkg/llm.ResponsePipeline` 从响应流中抽取 usage 和 `llm.*` timeline 事件。
8. Application DB 写入 trace、路由、usage、session、upstream 等索引字段；生产部署使用 Postgres。
9. observe/reanalysis 管道从 raw cassette 解析 Observation IR、findings 和分析任务结果。
10. Monitor 和 MCP 从 application DB 查询列表/聚合，从 cassette 读取详情。
11. 单元测试通过 `pkg/replay.Transport` 从 cassette 回放响应。

## 协议边界

当前 TraceLab 是协议族感知的透传代理，不是跨协议转换网关。

已实现协议族和 endpoint 见 [协议参考](./protocol-reference/README.md)。

重要边界：

- OpenAI-compatible、Anthropic Messages、Google Gemini、Vertex native 请求不会在转发热路径中互转。
- `pkg/llm` 可以把不同协议解析到统一摘要/IR，但这不等于可以无损转发到另一个协议。
- 上游必须支持 client 实际请求的 endpoint。例如 Claude Code 的 `/v1/messages` 需要 Anthropic Messages 兼容上游。

## 录制格式

当前写入格式是 `LLM_PROXY_V3`。

V3 prelude：

1. `# llm-tracelab/v3`
2. `# meta: {...}`
3. 多行 `# event: {...}`
4. 空行
5. 原始 HTTP 请求字节
6. 分隔换行
7. 原始 HTTP 响应字节

V2 固定 2KB header block 仍保持读取兼容。

## Timeline 事件

基础 recorder 记录请求/响应生命周期事件。

proxy pipeline 会追加 provider 归一化事件，例如：

- `llm.output_text.delta`
- `llm.reasoning.delta`
- `llm.tool_call`
- `llm.tool_call.delta`
- `llm.usage`
- `routing.selection`
- `routing.filtered`
- `routing.retry_candidate`
- `routing.failure`

这些事件写入 cassette prelude，并被 Monitor/MCP 用于 trace 详情、路由排障和失败聚类。

## Token Usage 归一化

统一 usage 结构：

- `prompt_tokens`
- `completion_tokens`
- `total_tokens`
- `prompt_tokens_details.cached_tokens`

OpenAI-compatible：

- `prompt_tokens = usage.prompt_tokens`
- `completion_tokens = usage.completion_tokens`
- `total_tokens = usage.total_tokens`
- `cached_tokens = usage.prompt_tokens_details.cached_tokens`

Anthropic Messages：

- `prompt_tokens = input_tokens + cache_creation_input_tokens + cache_read_input_tokens`
- `completion_tokens = output_tokens`
- `total_tokens = prompt_tokens + completion_tokens`
- `cached_tokens = cache_read_input_tokens`

Gemini / Vertex：

- 从 `usageMetadata` 映射到统一输入/输出/总 token 视图。

## 存储边界

raw cassette：

- replay 事实源。
- raw protocol 详情源。
- 派生数据重建源。

Application DB：

- trace/session/upstream/model/channel 列表和聚合。
- auth user/token。
- channel/model 配置。
- system events。
- Observation IR 和 findings。
- analysis jobs。
- eval/dataset/score/experiment。

生产 application DB 必须使用 Postgres。SQLite 覆盖同类表集时只作为本地开发、离线测试和既有本地 DB 兼容 fallback。

Monitor 列表页不应依赖扫描文件系统。

## 关键包

- `cmd/server`：CLI 和服务启动。
- `internal/proxy`：反向代理、鉴权、转发、响应截获。
- `internal/router`：多上游选择、健康、重试、sticky、决策记录。
- `internal/upstream`：上游配置解析、协议族、路由 profile、鉴权 header、URL 构造。
- `internal/recorder`：cassette 写入和 metadata finalization。
- `internal/store`：Postgres/SQLite application DB、schema 初始化/迁移、索引、查询和派生状态。
- `internal/monitor`：Monitor API 与嵌入式 React UI。
- `internal/mcpserver`：MCP 工具层。
- `internal/reanalysis`：trace/session/batch 重分析任务。
- `pkg/recordfile`：V2/V3 cassette 解析与写入。
- `pkg/llm`：协议识别、adapter、usage、stream pipeline。
- `pkg/observe`：Observation IR parser。
- `pkg/replay`：测试 replay transport。

## 兼容性要求

- 新录制只写 V3。
- 读取端继续支持 V2。
- 存储 schema 只能 additive 演进。
- 修改 record format 时必须同步 recorder、monitor、replay。
- replay 不能访问网络。
