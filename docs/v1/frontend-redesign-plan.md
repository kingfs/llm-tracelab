# v1 Monitor Frontend Redesign Plan

## 背景

当前 Monitor 前端已经不是单个内嵌 HTML，而是位于 `web/monitor-ui` 的 React/Vite 应用。构建产物输出到 `internal/monitor/ui/dist`，再通过 Go `embed.FS` 打包进服务端。

这条链路继续保留：

- 前端源码：`web/monitor-ui/src`
- 前端构建：`task ui:build`
- 服务端嵌入：`internal/monitor/server.go`
- 最终构建：`task build`

后续重构重点不是替换技术栈，而是在后端 v1 API 稳定后，重新设计 Monitor 的信息架构、导航、详情页结构和视觉语言。

## 启动时机

前端重构应在 P0-P12 后启动，并满足以下条件：

1. Observation、Finding、Performance、Analysis 相关 API 已经稳定到可供页面消费。
2. `task fmt:check` 与 `task test` 处于可信状态。
3. 不再通过修改 minified `internal/monitor/ui/dist` 来实现产品逻辑。
4. 每个前端阶段完成后仍然自动提交，并复盘是否偏离 v1 总目标。

当前建议顺序：

1. 后端 v1 闭环完成。
2. 恢复质量门禁。
3. 固化前端重构计划。
4. 开始 Monitor UI 重构。

## 产品定位

重构后的 Monitor 不应继续表现为“日志列表 + 详情页”的简单工具，而应表现为本地优先的 AI 调试工作台。

核心用户任务：

- 观察一段 LLM / agent 工作流的整体状态。
- 快速定位失败请求、危险工具调用、敏感信息泄露和 provider 错误。
- 对比不同 provider、model、session 的行为差异。
- 从高层概览下钻到 raw HTTP evidence。
- 让人类和 AI agent 都能引用同一套 trace、node、finding 证据。

非目标：

- 不做营销型 landing page。
- 不引入公网多租户控制台形态。
- 不在前端重构中改变 raw cassette 或 replay 合约。
- 不为了视觉效果牺牲信息密度和可扫描性。

## 信息架构

建议把主导航从当前的请求/会话/路由/令牌扩展为更贴近 v1 的工作台结构：

```text
Overview
Sessions
Traces
Audit
Upstreams
Analysis
Tokens
```

### Overview

默认首页，聚合当前系统状态：

- 最近请求量、失败率、token、latency、TTFT。
- parse job / finding / analysis 的健康状态。
- 最近失败 trace。
- 高风险 findings。
- 上游健康与 routing failure 摘要。

### Sessions

面向长期工作流：

- session 列表。
- session detail。
- session timeline。
- session performance。
- session findings。
- session analysis runs。

### Traces

面向单次 HTTP exchange：

- trace 列表。
- trace detail。
- Conversation / Protocol / Audit / Performance / Raw tabs。
- provider-specific semantic node tree。
- evidence path deep link。

### Audit

面向风险排查：

- findings 列表。
- severity/category/provider/model/session filters。
- dangerous tool calls。
- sensitive data findings。
- provider safety findings。
- finding 到 trace/node/raw evidence 的跳转。

### Upstreams

面向多 provider 路由：

- upstream health。
- model catalog。
- routing decisions。
- routing failures。
- provider/model latency 与 error 对比。

### Analysis

面向离线分析结果：

- session summary runs。
- repeated findings。
- model/provider behavior comparison。
- 后续可选 LLM analysis runs。

### Tokens

保留当前访问控制和 API token 管理能力，视觉上归入设置型页面。

## 详情页结构

Trace detail 建议固定为以下 tabs：

```text
Conversation
Protocol
Audit
Performance
Raw
```

Session detail 建议固定为以下 tabs：

```text
Timeline
Traces
Audit
Performance
Analysis
```

每个 tab 应满足：

- 页面状态可以通过 URL 表达。
- 列表、筛选、展开节点不依赖后端 session state。
- 找不到派生数据时显示 parse/analyze 状态，而不是隐藏入口。
- Raw 始终可访问。

## 视觉与交互方向

目标风格：现代 AI 调试工作台，强调稳定、可扫描、证据可追溯。

建议原则：

- 使用应用型布局，而不是 landing page 或装饰性大 hero。
- 左侧主导航 + 顶部 context bar，避免当前 sticky chip navigation 扩散。
- 采用中性浅色或深浅可扩展的 design tokens，避免单一米色/复古纸面风格继续主导。
- 使用清晰的状态色区分 success、warning、danger、muted、accent。
- 详情页使用密集但分区明确的信息布局。
- 协议树、finding 列表、raw code block 应有稳定尺寸和可滚动区域。
- 操作按钮优先使用图标加 tooltip；文本按钮保留给明确命令。
- 不使用纯装饰性渐变球、背景斑点或过大的卡片堆叠。

## 技术计划

### F0. 前端重构基线

目标：固化设计方向和工程边界。

范围：

- 新增本文档。
- 确认 `web/monitor-ui` 是唯一前端源码入口。
- 确认 `internal/monitor/ui/dist` 只由构建产物更新。

验收：

- `task fmt:check`
- `task test`

提交：

```text
docs: add monitor frontend redesign plan
```

### F1. 前端 API Client 与类型整理

目标：让页面消费 v1 API 时有统一边界。

范围：

- 整理 `web/monitor-ui/src/lib` API client。
- 为 trace/session/upstream/observation/finding/analysis response 建立轻量 schema 或 adapter。
- 统一 loading/error/empty 状态组件。
- 不改变页面视觉。

验收：

- `task ui:build`
- `task test`
- 关键页面仍可加载。

提交：

```text
refactor: organize monitor ui api client
```

### F2. Shell 与导航重构

目标：建立新的工作台框架。

范围：

- 新增 app shell。
- 左侧导航：Overview、Sessions、Traces、Audit、Upstreams、Analysis、Tokens。
- 顶部 context bar：当前页面、时间窗口、搜索/过滤入口、刷新。
- 保留现有 routes 的兼容跳转。

验收：

- 现有 Requests/Sessions/Upstreams/Tokens 能通过新导航访问。
- mobile 下导航不遮挡内容。
- `task ui:build`
- `task build`

提交：

```text
feat: redesign monitor shell navigation
```

### F3. Trace Detail v1 页面

目标：把 v1 后端 API 的核心价值呈现出来。

范围：

- Conversation tab。
- Protocol tab。
- Audit tab。
- Performance tab。
- Raw tab。
- evidence path 与 semantic node deep link。

验收：

- parse success、parse failed、no observation 三种状态都可读。
- findings 可跳到对应 node 或 raw evidence。
- Raw 不受派生数据状态影响。
- `task ui:build`
- `task build`

提交：

```text
feat: redesign trace detail monitor view
```

### F4. Session Detail v1 页面

目标：支撑长期工作流复盘。

范围：

- Timeline tab。
- Traces tab。
- Audit tab。
- Performance tab。
- Analysis tab。
- session analysis run 展示。

验收：

- session summary 与 trace list 仍可用。
- repeated findings 和 analysis output 有明确 evidence refs。
- `task ui:build`
- `task build`

提交：

```text
feat: redesign session detail monitor view
```

### F5. Overview / Audit / Analysis 工作台页面

目标：把用户常见入口从“先找 trace”升级为“先判断系统状态”。

范围：

- Overview dashboard。
- Audit finding list。
- Analysis run list。
- 跨 session/trace 的筛选和跳转。

验收：

- 高风险 findings 可以从首页进入。
- analysis runs 可以按 session/trace 追溯。
- `task ui:build`
- `task build`

提交：

```text
feat: add monitor overview audit analysis pages
```

### F6. 视觉系统与可用性收尾

目标：统一视觉语言和交互细节。

范围：

- design tokens。
- buttons、tabs、tables、badges、code blocks、empty states。
- responsive layout。
- keyboard focus。
- basic accessibility labels。

验收：

- 桌面和移动宽度下无明显遮挡和文本溢出。
- 所有主要页面 loading/error/empty 状态一致。
- `task ui:build`
- `task build`
- `go test ./internal/monitor`，覆盖 embedded UI 主要 SPA routes 和构建产物 asset。

提交：

```text
style: polish monitor ui system
```

## 复盘规则

每个前端阶段提交后复盘：

1. 是否仍服务 v1 的观测、审计、调试和回放目标。
2. 是否保持 raw cassette 和 replay 合约不变。
3. 是否只修改 `web/monitor-ui/src` 和构建产物需要的文件。
4. 是否存在 API 缺口需要回到后端补齐。
5. 是否因为视觉调整降低了信息密度或 evidence 可追溯性。
