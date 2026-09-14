# Entry/Model Exchange Recording 落地方案

状态：实施蓝图

日期：2026-06-24

## 目标

TraceLab 需要从入口侧统一记录 request/response，同时保留现有上游模型调用 cassette 的回放能力。

这意味着两类 HTTP exchange 都要成为一等观测对象：

- `entry exchange`：客户端调用 TraceLab 看到的 HTTP request/response，例如 Codex 调用本地 `/v1/responses`。
- `model exchange`：TraceLab 为了服务一个入口请求而主动调用上游 LLM 的 HTTP request/response。

二者都可以进入当前审计、观测、分析和 Monitor/MCP 查询流程，但语义不能混淆。Responses server-mode 下，一个入口请求未来可能产生多次、不同目的的 model exchange，例如主回答、工具 follow-up、compact、summary、repair。

## 核心决策

1. 录制入口统一放在流量入口层。

   普通 reverse proxy 请求继续走现有 `internal/proxy` recorder pipeline。本地 Responses server-mode 的 `/v1/responses` 入口应在 `internal/proxy.(*Handler).serveLocalResponsesWithBody` 外层补一层 recording wrapper，而不是把 `.http` cassette 文件格式下沉到 `internal/responses/httpapi` 或 runtime。

2. 用 taxonomy 区分“记录了什么”和“为什么发生”。

   新增可选字段：

   - `exchange_kind`: `entry`, `model`, `tool`, `derived`
   - `exchange_role`: `client_request`, `primary_model_call`, `tool_followup_model_call`, `compact_model_call`, `summary_model_call`, `repair_model_call`, `tool_call`, `derived_summary`, `derived_repair`

   phase one 至少落地 `entry/client_request`、`model/primary_model_call`、`model/tool_followup_model_call`、`model/compact_model_call`。

3. Replay 仍然 cassette-first。

   `pkg/replay` 继续只回放一个 `.http` 文件，不依赖 DB，不自动编排 entry + 多个 model cassettes。entry cassette 回放客户端可见响应；model cassette 回放单次上游模型响应。

4. 短期复用现有表和审计模型。

   不在第一阶段引入通用 `exchanges` 图表。先通过 V3 meta/events、`logs`、`request_audits`、`upstream_exchanges`、`execution_events` 以及 `tool_call_audits` 建立关系。后续查询压力明确后，再把这些索引收敛成通用 `exchanges` read model。

5. 原始 `.http` 文件不做破坏性迁移。

   V2/V3 老 cassette 缺失 `exchange_kind` 时，在查询和 backfill 中推断；backfill 只更新索引，不重写 cassette。

## 目标记录形态

一个 Codex `/v1/responses` 请求的理想关系如下：

```text
request_audit_id=reqaudit_1
entry/client_request exchange_id=exch_entry_1 cassette=entry.http
  model/primary_model_call sequence_index=0 cassette=model_a.http
  tool/tool_call sequence_index=1 call_id=call_search
  model/tool_followup_model_call sequence_index=2 cassette=model_b.http
  model/compact_model_call sequence_index=3 cassette=model_c.http
  derived/derived_summary sequence_index=4 response_id=resp_compact
```

phase one 允许没有真实 `parent_exchange_id`，但必须保留 `request_audit_id` 和稳定排序字段，使 Monitor/MCP 能把一个入口请求下的多个模型调用按执行顺序展示出来。

## 实施阶段

### Phase 1：格式与索引基础

交付：

- `pkg/recordfile.MetaData` 增加可选字段：`exchange_id`、`exchange_kind`、`exchange_role`、`parent_exchange_id`、`sequence_index`、`trace_id`。
- V3 reader 对这些字段可读可忽略；V2 reader 不变。
- recorder prepare/update 路径可以接收 exchange metadata，并写入 V3 meta 或 `# event`。
- `upstream_exchanges` 增加 nullable 字段：`exchange_id`、`exchange_kind`、`exchange_role`、`parent_exchange_id`、`sequence_index`。
- SQLite startup schema 和 Postgres migration 都能在旧库上 additive 初始化。
- 普通 proxy 和旧 cassette 缺失 taxonomy 时默认按 `model/primary_model_call` 推断，replay 不受影响。

验收：

- `pkg/recordfile` 新老 fixture 解析通过。
- `pkg/replay` 对 V2、旧 V3、新 V3 都能继续回放。
- schema migration 可在已有本地 DB 上启动。

### Phase 2：Responses 入口 cassette

交付：

- 在 `internal/proxy.(*Handler).serveLocalResponsesWithBody` 外层增加 entry recording wrapper。
- wrapper 记录客户端原始 request line/path/query/header/body，响应记录 status/header/body。
- streaming SSE 用 write-through tee `ResponseWriter` 录制，不能完整 buffering 后再发给客户端。
- `Flush()` 透传，TTFT 以第一次 body bytes 成功写给客户端为准。
- entry cassette 用 `LLM_PROXY_V3`，标记 `exchange_kind=entry`、`exchange_role=client_request`，并携带 `request_audit_id`。
- 内部上游 model cassette 继续由 `responsesChatCompletionsAdapter` 录制，并标记为 `model/*`。
- entry 和 model 通过 `request_audit_id`、events、`upstream_exchanges` 关联。

验收：

- 本地 Responses server-mode 非流式 `/v1/responses` 产生 entry cassette 和内部 model cassette。
- streaming `/v1/responses` entry cassette 保留 raw SSE bytes，`Content-Type` 和 flush 行为不变。
- runtime error、client cancel、partial stream 都能 best-effort finalize cassette 并留下 error/event。
- entry cassette 可被 `recordfile.Parse` 解析，可被 `pkg/replay` 作为普通 HTTP exchange 回放。

### Phase 3：模型调用角色分类

交付：

- Responses runtime 在发起上游调用前决定 `exchange_role`，recorder 只持久化传入 metadata。
- 首次回答模型调用写 `primary_model_call`。
- 注入 tool output 后的模型调用写 `tool_followup_model_call`。
- compact 过程中的模型调用写 `compact_model_call`。
- 后续 summary/repair 场景预留 `summary_model_call`、`repair_model_call`，不临时发明新名字。
- model-call `execution_events.details_json` 写入同一组 exchange 字段。

验收：

- 一个入口请求产生多次模型调用时，`sequence_index` 单调递增。
- audit trace 能区分 primary、tool follow-up、compact。
- 旧路径未传 metadata 时仍能落到兼容默认值。

### Phase 4：查询、Monitor 和 MCP 展示

交付：

- `internal/responses/audit.QueryService` 返回 entry exchange 和 model exchanges。
- 保留现有 `upstream_exchanges` 输出字段，新增字段只做 additive 扩展。
- raw cassette summaries 包含 `exchange_kind`、`exchange_role`、`trace_id`、`cassette_path` 以及读取错误。
- Monitor trace 列表增加 exchange kind filter/badge。
- Responses/Codex 请求详情以 entry/request audit 为主行，展开显示所有 model exchanges、tool events、compact events 和 raw cassette links。
- MCP `responses_audit_trace` 增加 `entry_exchange`、`model_exchanges`、带 kind 的 `raw_cassettes`，同时保留 `upstream_exchanges` alias。

验收：

- 不扫描 cassette 目录也能从索引查出父子关系。
- 旧 MCP client 只读 `upstream_exchanges` 时行为不破坏。
- 一个 Codex 请求下的每个上游模型 cassette 都有独立可打开链接。

### Phase 5：Observation、Analyzer 和 backfill

交付：

- observeworker 选择 parser 的顺序为：V3 meta 显式 kind、DB 索引 kind、path/metadata 推断、现有默认 parser。
- entry observation 只解析客户端可见语义，例如 Responses operation、requested model、final response id、final output、status。
- model observation 保持现有 provider/protocol 解析。
- analyzer findings 携带 exchange kind，避免把 entry 和 child model 上同一风险重复算作两个同级问题。
- 提供可选 backfill：只更新索引，不重写 `.http` 文件；输出 scanned/updated/legacy/conflict/missing counts。

验收：

- 老 cassette 缺失 taxonomy 仍能观察和分析。
- ambiguous 记录标记为 `legacy` 或 unknown，不猜成 entry。
- parse failure 只影响对应 exchange，不阻断同一 request 下其他 model exchange。

## 粗粒度并发开发切片

后续实现可按以下 worktree 并发，依赖拓扑如下：

1. `format-storage-foundation`

   拥有范围：`pkg/recordfile`、`internal/recorder`、store schema/migration。

   交付标准：新字段 additive 写读通过，旧 cassette replay 通过，schema 在旧库上启动。

2. `responses-entry-recording`

   拥有范围：`internal/proxy/responses_server.go` 及其测试。

   依赖：可与 1 并行做 wrapper 原型，但最终合入需要 1 的 recorder metadata API。

   交付标准：非流式、流式、错误、cancel 的 entry cassette 测试通过。

3. `responses-model-role-classification`

   拥有范围：Responses runtime 到 `responsesChatCompletionsAdapter` 的 model-call metadata 传递、audit event details。

   依赖：需要 1 的字段定义，可与 2 正交。

   交付标准：多 model call、tool follow-up、compact 的角色和顺序可查询。

4. `audit-query-monitor-mcp`

   拥有范围：`internal/responses/audit` query、Monitor API/UI、MCP audit trace 输出。

   依赖：需要 1 和 3 的索引字段；可以在 2 之后补 entry cassette 链接。

   交付标准：entry-centric trace 能展示所有 child cassettes，旧 MCP 字段兼容。

5. `observe-analyze-backfill`

   拥有范围：observeworker、observation model、analyzer scope、backfill command/job。

   依赖：需要 1 的 metadata 和 4 的查询形态。

   交付标准：entry/model parser selection、legacy inference、index-only backfill 测试通过。

## 非目标

- 不改变 raw HTTP request/response 在 `.http` 中的可读格式。
- 不让 `pkg/replay` 依赖 DB 或一次回放多个 cassette。
- 不在第一阶段新建完整通用 `exchanges` 图表。
- 不重写旧 V2/V3 cassette 文件。
- 不把 recorder 文件格式泄漏进 Responses runtime/httpapi 的核心协议实现。
- 不在本轮处理 auth failure cassette；最小一期只记录通过 auth 后进入 Responses server-mode 的请求。

## 本轮 Master/Worker 调度结果

本方案由三个正交设计切片并发产出后合并：

- [Exchange Recording Model Design](./EXCHANGE_RECORDING_MODEL_DESIGN.md)：taxonomy、relationship fields、storage 方向。
- [Responses Entry Recording Design](./RESPONSES_ENTRY_RECORDING_DESIGN.md)：入口 wrapper、streaming tee、错误/cancel 和测试策略。
- [Exchange Query Replay Design](./EXCHANGE_QUERY_REPLAY_DESIGN.md)：query、Monitor/MCP、replay 兼容、observation/backfill 影响。

三份子设计均已在独立 `git worktree` 分支提交并合入主分支，worktree 已清理。
