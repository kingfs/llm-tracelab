# Responses Entry Recording Design

本文设计 Responses server 入口侧 request/response cassette 录制方案。目标是让客户端访问 `/v1/responses` 时，无论请求由普通 reverse proxy 转发到上游，还是由本地 Responses server-mode 接管，都能得到一份入口侧 `.http` cassette，用于回放、排障和与内部 model call 关联。

## 背景与缺口

当前纯 proxy 入口已经完整接入 recorder：

- `internal/proxy.Handler.ServeHTTP` 在进入 reverse proxy 前读取并规范化请求体，调用 `recorder.PrepareLogFileWithOptionsAndBody` 写入请求行、请求头和请求体。
- `ReverseProxy.ModifyResponse` 写入响应分隔符、HTTP status/header，并用 `UsageSniffer` 边向客户端转发边把响应体追加到同一个 `.http` 文件。
- 流式响应由 `llm.DetectStreamingResponse` 标记 `Layout.IsStream=true`，响应体按原始 SSE bytes 写入 cassette；`llm.ResponsePipeline` 同步抽取 usage 和事件。
- `UpdateLogFile` 最后写入 `LLM_PROXY_V3` prelude，并把 trace 元数据索引进 SQLite/Postgres store。

Responses server-mode 的外层入口还缺少同等级 cassette。当请求由本地 Responses execution mode 处理时，配置的 Responses path 由 `internal/proxy.(*Handler).serveLocalResponsesWithBody` 重写到内部 `internal/responses/httpapi.Handler`，不再进入 reverse proxy 的 recorder pipeline。当前已有的录制只覆盖 runtime 内部通过 `responsesChatCompletionsAdapter` 发起的上游 `/v1/chat/completions` model exchange；也就是说：

- 客户端真实请求 `/v1/responses` 和 TraceLab 返回给客户端的 Responses object/SSE 没有 `.http` cassette。
- Monitor/audit 能看到 request audit、execution events 和 upstream exchange correlation，但 replay 仍缺少“入口看到的 OpenAI Responses API”原始 HTTP 交换。
- 同一个用户请求可能只有内部 Chat Completions cassette，容易被误认为客户端直接调用了 Chat Completions。

## 设计目标

- 入口 cassette 记录客户端看到的 HTTP exchange：method/path/query/header/body、status/header/body，包括 Responses SSE。
- 普通 proxy `/v1/responses` 保持现状；本地 Responses server-mode 的 `/v1/responses`、`/v1/responses/compact`、`/v1/responses/{id}/input_items` 通过同一 record file 格式补齐。
- 不打断 streaming：录制必须采用 tee/sniffer，不能等完整响应缓冲后再发送。
- 清晰区分 entry exchange 与 model exchange，避免把本地 server 入口 cassette 和内部上游 model cassette 混为一谈。
- 最小一期尽量复用 `internal/recorder` 与现有 `UsageSniffer`/V3 prelude，避免引入第二套文件格式。

## 入口包装方案

推荐在 `internal/proxy.(*Handler).serveLocalResponsesWithBody` 外层包装 `h.responsesHandler`，而不是把录制逻辑放入 `internal/responses/httpapi` 或 runtime：

1. `serveLocalResponsesWithBody` 是本地 server-mode 的统一入口，已经知道客户端原始 path 和重写后的内部 target path。
2. proxy 层已有 `recorder.Recorder`、`Debug.OutputDir`、`MaskKey`、store、auth、limiter、router policy 等上下文。
3. `httpapi.Handler` 可以继续只负责 Responses HTTP contract、audit 和 runtime 调用，避免把 cassette 文件格式泄漏进 Responses runtime 包。

### 请求捕获

实现一个局部 helper，例如 `recordLocalResponsesExchange(w, r, targetPath, next)`：

- 在进入 `httpapi.Handler` 前读取 `r.Body`，受 `responses_server.max_request_body_bytes` 或 recorder 专用上限保护。
- 重建两个 body reader：
  - 一个给 `httpapi.Handler` 使用；
  - 一个给 recorder 写入请求 cassette。
- 录制 URL 应保留客户端入口 path/query，例如配置 path `/responses` 时 cassette request line 是 `POST /responses HTTP/1.1`，同时在事件或 meta 扩展里记录 `responses.target_path=/v1/responses`。
- 复用 `recorder.PrepareLogFileWithOptionsAndBody`，但传入专门的 `PrepareOptions.SiteURL`，建议使用稳定虚拟 site，例如 `llm-tracelab-entry://local-responses` 或 `http://llm-tracelab.local`。这样目录与上游 provider 目录分离。
- 继续执行 `MaskKey`：入口请求头中的 `Authorization`、`api-key`、`x-api-key`、`x-goog-api-key` 要被脱敏；后续应扩展为复用 `internal/redaction` 的 header allow/deny 规则。

请求体记录应优先保存客户端原始 body，而不是 Codex compat 归一化后的 body。`httpapi` 内部为了执行可能会注入 hosted tools 或 default tool choice；这些变化应通过 `# event` 或 audit event 表达，不应改写入口 cassette 的 request bytes。

### 响应捕获

需要一个 ResponseWriter 包装器，职责是“向客户端原样写，同时把写出的 bytes tee 到 cassette”：

- `Header()` 透传到底层 writer。
- `WriteHeader(code)` 第一次调用时冻结 status/header，并向 cassette 写入分隔符、status line 和响应头。
- `Write(p)` 如果还没写 header，按 net/http 语义先隐式 `WriteHeader(http.StatusOK)`；随后把 `p` 写到底层 writer，再把成功写出的切片写到 cassette。
- `Flush()` 透传 `http.Flusher`，不能因为录制而延迟 SSE flush。
- 可选实现 `http.Hijacker`、`http.Pusher`、`io.ReaderFrom` 时要谨慎；最小一期 Responses HTTP 不需要 hijack，但应确保包装器实现 `http.Flusher`。

响应 status/header 以实际写给客户端的内容为准。`httpapi.writeJSON`、`writeRuntimeError`、`streamWriter.write` 都会通过同一个 wrapper，因此非流式 JSON、错误 JSON、deferred SSE、incremental SSE 会自然进入 cassette。

### Streaming SSE

SSE 不应被完整 buffering。包装器只在每次 `Write` 时追加 bytes；`Flush` 立即透传。完成后：

- 若 `Content-Type` 是 `text/event-stream`，设置 `Layout.IsStream=true`。
- 若已经写出响应头并写出部分 SSE 后 runtime 报错，cassette 应包含已经发送的事件以及 best-effort `response.failed` 事件；meta error 记录运行时错误。
- 若 incremental stream fallback 到 deferred stream，客户端只看到一个最终 SSE exchange，cassette 也只记录入口最终输出；fallback 过程保留在 audit/execution events。
- `TTFTMs` 应在第一次 body bytes 成功写给客户端时记录，而不是 runtime 开始时。

### 错误、panic 与 cancel

- 入口 handler 返回普通 4xx/5xx 时照常记录 status/header/body，并在 `Meta.Error` 写入可用错误摘要。
- `context.Canceled` 或 client disconnect 发生在响应未完成时，cassette 应保留 partial body，`Meta.Error` 标记 `cancelled`，并设置事件 `entry.exchange cancelled`。如果没有真实 HTTP status，保持已写 status；完全未写时可记录 synthetic `499 Client Closed Request` 到 meta/event，但不要向客户端额外写响应。
- 包装层应使用 `defer finalize()` 确保 panic、runtime error、client cancel 都尽量调用 `UpdateLogFile`。panic 不在最小一期吞掉，继续交给上层 net/http 处理；但 finalize 可把 panic 摘要写入 meta。
- 如果 `PrepareLogFile` 失败，不应阻断 Responses server；记录 slog error 后直接调用 `httpapi.Handler`。

## Entry Exchange 与 Model Exchange 的关系

需要在格式和索引上显式区分两类 cassette：

- Entry exchange：客户端到 TraceLab 本地 Responses endpoint 的 HTTP exchange。Provider 建议为 `openai_responses_entry` 或 `local_responses`; operation/endpoint 仍按 `/v1/responses` 分类。它代表 replay 的外部契约。
- Model exchange：Responses runtime 内部调用上游 model provider 的 HTTP exchange。它已经由 `responsesChatCompletionsAdapter` 录制，带真实 selected upstream、routing score、provider preset 和 upstream exchange audit。

建议在 V3 events 中加入稳定关联事件，而不是修改 raw HTTP 格式：

- entry cassette event: `entry.exchange`，属性包含 `exchange_role=entry`、`served_by=responses_server`、`target_path=/v1/responses`、`request_audit_id`。
- model cassette event: 沿用现有 `response.model_call`/`routing.selection`，可追加 `exchange_role=model`。
- entry final event: `entry.model_exchanges`，属性包含同一 `request_audit_id` 下已记录的 model cassette trace IDs 或 cassette paths。最小一期如果 finalize 时还不方便查询 store，可先只依赖 shared `request_audit_id`，Monitor 后续通过 audit store join。

目录也应避免混淆。推荐 entry cassette 落到类似：

```text
<output_dir>/local-responses/<model-or-unknown>/<yyyy>/<mm>/<dd>/*.http
```

内部 model cassette 继续落到真实 upstream host/model 目录。这样人工浏览文件树时不会把 entry 与 upstream 录制混在一起。

## 最小实现切片

一期只改必要模块：

- `internal/proxy/responses_server.go`
  - 替换 `serveLocalResponsesWithBody` 中“直接调用 `h.responsesHandler.ServeHTTP`”为 recorder wrapper。
  - 新增 local Responses response writer/sniffer helper。
  - 生成 entry events、设置 duration/status/content length/stream/error。
- `internal/recorder`
  - 尽量不改公共格式。若必须区分 entry role，优先通过 `RecordEvent` 和现有 meta 字段表达。
  - 如需稳定 provider/operation，可小幅扩展 `PrepareOptions` 增加 override 字段；否则先接受 `llm.ClassifyHTTPRequest` 对 `/v1/responses` 的现有分类。
- `pkg/recordfile`
  - 不改格式，仅用现有 V3 event 能力。
- 不改 `internal/responses/runtime`，不改协议类型，不做 replay matcher 大重构。

不建议一期同时做：

- 新建 entry exchange schema 表。
- 改 Monitor 列表 UI。
- 让 replay 自动把 entry cassette 与 model cassette 组合成多段流程。
- 对所有非 Responses 本地 management endpoints 录制。

## 风险与处理

- Streaming buffering：最大风险是包装器先缓冲响应再写文件。必须用 tee write-through，并在测试中验证 SSE chunk 能被 flush。不要用 `httptest.ResponseRecorder` 作为生产实现。
- Body size：入口请求受现有 `responses_server.max_request_body_bytes` 保护；响应体可能很大。最小一期沿用 recorder 行为完整记录，后续可增加 debug 层 `max_recorded_response_bytes`，超限后停止写 body 并在 event 标记 truncated。
- Secret redaction：请求 header 继续依赖 `MaskKey`；响应 header 也可能含敏感 provider/debug 信息，建议最小一期对 `Set-Cookie`、`Authorization`、`api-key` 类响应头也做脱敏后再写 cassette。
- Auth 失败是否记录：如果 token auth 在 `ServeHTTP` 外层已经拒绝，`serveLocalResponsesWithBody` 不会被调用，最小一期不记录 auth 失败。若 auth 是 proxy handler 内部先处理再分发，应只记录通过 auth 后进入 Responses server 的请求。auth failure cassette 可以作为后续 opt-in，因为它更容易记录真实 token 形态。
- 取消和 partial cassette：client cancel 时 raw response 可能没有完整 SSE 终止事件。Replay 应能按已有 raw HTTP 回放 partial bytes，但测试应明确这是调试 cassette，不承诺语义完整。
- Store indexing：entry cassette 会进入同一 trace index，Monitor 可能多出本地 Responses 记录。需要通过 provider/operation/event 区分，避免统计时把一次用户 Responses 请求和内部 model call 都算作同类 upstream traffic。

## 测试建议

必须落到具体包和场景：

- `internal/proxy`
  - server-mode 非流式 `POST /v1/responses`：使用 fake runtime 或 test upstream，断言生成一个 entry cassette，request line 是客户端入口 path，response 是 Responses JSON，`Layout.IsStream=false`，同时内部 model cassette 仍存在且路径/provider 不同。
  - server-mode streaming `POST /v1/responses {"stream":true}`：断言 entry cassette response header 为 `text/event-stream`，body 包含 `response.created`、delta、`response.completed` 或 failed event，`Layout.IsStream=true`。
  - incremental stream 中途 runtime error：断言客户端收到已写 SSE 和 `response.failed`，cassette meta/event 标记 failed，文件可被 `recordfile.Parse` 读取。
  - client cancel：用可取消 request context 和阻塞 runtime/upstream，断言 finalize 后 cassette 存在、meta error 为 cancelled、不会 panic。
  - configured custom Responses path：例如 `/openai/responses` 重写到 `/v1/responses`，断言 cassette raw request 保留 `/openai/responses`，event 记录 target path。
- `internal/responses/httpapi`
  - 不新增 cassette 测试；只保留现有 HTTP contract 测试，确保 handler 不依赖 recorder。
- `pkg/recordfile`
  - 增加包含 `entry.exchange` event、SSE body、partial error meta 的 V3 fixture parse 测试，确保新事件不破坏旧 reader。
- `pkg/replay`
  - 用 entry cassette 回放 `/v1/responses` 非流式响应，断言 SDK/HTTP client 可以按普通 cassette 读取。
  - 用 entry SSE cassette 回放 stream 响应，断言 `Content-Type`、status 和 raw SSE bytes 保持。

回归命令建议：

```bash
task test -- internal/proxy
task test -- internal/responses/httpapi
task test -- pkg/recordfile
task test -- pkg/replay
task check:quick
```

## 交付标准

一期完成时应满足：

- 本地 Responses server-mode 的入口 `/v1/responses` 非流式与流式请求都会生成 entry `.http` cassette。
- cassette 使用 `LLM_PROXY_V3`，可被 `pkg/recordfile` 解析，可被 `pkg/replay` 作为普通 HTTP exchange 回放。
- entry cassette 与内部 model cassette 通过 `request_audit_id` 和 events 可关联，但目录、provider/role/event 能区分。
- Streaming 录制不改变客户端可观察的 chunk/flush 行为。
- 请求 secret 脱敏保持现有 recorder 保障；响应敏感 header 至少覆盖高风险名称。
- Auth 失败是否记录有明确结论：最小一期不记录 handler 外层 auth failure。
- 取消、runtime error、partial stream 都能 finalize 文件并留下可诊断 meta/event。
