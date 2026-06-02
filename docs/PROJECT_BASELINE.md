# 项目基线

本文档记录当前已经实现的功能基线。它不是路线图。

更短的中文概览见 [当前实现概览](./CURRENT_IMPLEMENTATION.md)。

## 当前产品范围

TraceLab 当前提供：

- 本地 LLM API 代理。
- 原始 HTTP cassette 录制。
- cassette replay。
- SQLite 元数据索引。
- 多上游和模型/渠道管理。
- Monitor Web。
- MCP 排障工具。
- Observation IR、findings、reanalysis jobs。

## 当前协议覆盖

已实现协议族：

- `openai_compatible`
- `anthropic_messages`
- `google_genai`
- `vertex_native`

边界：

- 可以识别、记录、解析这些协议。
- 不在代理热路径中做跨协议请求转换。
- OpenAI-compatible provider 只能声明兼容其实际支持的 endpoint。

详细内容见 [协议参考](./protocol-reference/README.md)。

## 录制与回放基线

- 当前写入格式：`LLM_PROXY_V3`。
- 读取兼容：`LLM_PROXY_V2`。
- `.http` cassette 是 replay 和详情页事实源。
- `pkg/replay` 是硬要求，测试 replay 不访问上游网络。

## SQLite 基线

SQLite 当前负责：

- trace 列表、过滤、分页和统计。
- session 聚合。
- upstream、model、channel 分析。
- channel/model 配置。
- auth user/token。
- system events。
- Observation IR。
- trace findings。
- analysis jobs。
- eval、dataset、score、experiment。

启动时 schema 升级必须兼容已有本地 DB。

## Session 基线

session 聚合已实现。

提取顺序：

1. `Session_id`
2. `X-Claude-Code-Session-Id`
3. `X-Codex-Turn-Metadata.session_id`
4. `X-Codex-Window-Id` 中 `:` 前缀
5. 空 session

Monitor 和 MCP 都可查询 session 列表和详情。

## 多上游与渠道基线

当前支持：

- legacy `upstream`。
- 多 `upstreams`。
- SQLite 中的 `channel_configs` / `channel_models`。
- YAML 首次 bootstrap。
- Monitor Web 管理 channel/model。
- model discovery / probe。
- channel 和 model 启停。
- route target 选择。
- selected route 记录。
- 健康状态、重试和 sticky routing 事件。

长期配置以 SQLite channel/model 记录为准。

## Monitor 基线

Monitor 当前包括：

- Overview。
- Requests。
- Sessions。
- Models。
- Channels。
- Routing。
- Events。
- Tokens。
- Analysis。
- Trace detail。

主要 API：

- `/api/overview`
- `/api/traces`
- `/api/sessions`
- `/api/models`
- `/api/channels`
- `/api/routing/summary`
- `/api/events`
- `/api/findings`
- `/api/analysis`
- `/api/upstreams`
- `/api/auth/*`

## MCP 基线

MCP 当前是 streamable HTTP。

工具范围：

- trace/session/upstream 查询。
- failure clustering。
- routing/sticky 查询。
- system event 查询。
- findings 查询。
- 受控 reanalysis。

MCP 不替代 replay、Monitor 或 SQLite 事实源。

## Observation 与 Reanalysis 基线

当前已经实现：

- OpenAI / Anthropic / Gemini parser registry。
- Observation IR 持久化。
- trace findings。
- parser/analyzer 失败写 system events。
- trace/session/batch reanalysis jobs。
- usage repair。

## 当前非目标

- 公网多租户网关。
- 计费/充值/订阅销售。
- 跨协议转换。
- 用派生数据替代 raw cassette。
- 让测试依赖真实 provider。

## 推荐验证

- 文档：`git diff --check`。
- 小改动：`task check:quick`。
- 代理/路由/存储：`go test ./internal/proxy ./internal/router ./internal/store`。
- 协议/record/replay：`go test ./pkg/llm ./pkg/observe ./pkg/recordfile ./pkg/replay`。
- Monitor：`go test ./internal/monitor`，前端改动补 `task ui:build && task ui:test`。
