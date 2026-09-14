# 凭据路由操作指南

凭据路由用于在一个上游渠道下选择多个 provider 账号或 token。

它的目标是：

- 调试。
- 本地或团队治理。
- 提高可用性。
- 保留可解释的路由证据。

它不是：

- 支付。
- 充值。
- 公网 API relay。
- 订阅转售。
- SaaS 配额分发。

## 一个渠道多个凭据

当一个 provider 渠道有多个账号或 key 时，可以使用显式 credentials。

渠道负责：

- base URL。
- provider preset。
- routing profile。
- priority / weight / capacity。
- 模型覆盖。

credential 负责：

- secret material。
- credential 级别限制。
- route target 身份。

示例：

```yaml
upstreams:
  - id: openai-primary
    enabled: true
    priority: 100
    weight: 1
    capacity_hint: 2
    model_discovery: "static_only"
    static_models:
      - gpt-5
      - gpt-5.1-codex
    upstream:
      base_url: "https://api.openai.com/v1"
      provider_preset: "openai"
      routing_profile: "openai_default"
    credentials:
      - id: primary
        name: "primary local key"
        api_key: "$env:OPENAI_PRIMARY_API_KEY"
        concurrency_limit: 2
      - id: backup
        name: "backup local key"
        api_key: "$env:OPENAI_BACKUP_API_KEY"
        concurrency_limit: 1
```

注意：

- `api_key` 应引用环境变量，不要把真实密钥提交到 YAML。
- `id` 要稳定，会进入 route target identity，并可能出现在 cassette metadata 中。
- `name` 是操作标签，不应包含敏感信息。
- route target identity 通常是 `<channel_id>:<credential_id>`，例如 `openai-primary:primary`。

长期渠道配置建议通过 Monitor Web 管理。YAML 更适合首次 bootstrap、测试和迁移示例。

## 隐式 default credential

旧配置把一个 key 直接写在 upstream/channel 上：

```yaml
upstream:
  base_url: "https://api.openai.com/v1"
  provider_preset: "openai"
  api_key: "$env:OPENAI_API_KEY"
```

当没有显式 `credentials` 时，TraceLab 把 inline key 视为一个隐式 credential：

```text
credential_id = default
route_target_id = <channel_id>:default
```

这保持旧单 key 部署兼容。

如果同一个 upstream 下已经配置显式 credentials，应把显式列表视为事实源，避免同时使用 inline `api_key`。

## Sticky Route Target

sticky routing 绑定的是具体 route target，不只是 provider 名称。

启用 credential routing 后，route target 是 channel + credential。

示例：

```text
sticky key fingerprint -> openai-primary:primary
```

这对 coding agent 很重要，因为 provider 侧 conversation state、prompt cache、file cache 或账号配额可能和具体 credential 相关。

行为：

- sticky hit 会复用同一 route target。
- 如果 credential 不可用，会记录 sticky break，并重新绑定到其他可用 route target。
- Monitor、MCP 和 cassette 事件只暴露 `sticky_key_fingerprint`，不暴露原始 sticky key。

## 安全 metadata

V3 cassette routing events 可以包含以下安全字段：

- `route_target_id`
- `channel_id`
- `credential_id`
- `credential_hint`
- `credential_health_state`
- `credential_filter_reason`
- `sticky_key_fingerprint`

这些字段用于 Monitor、MCP、routing summary 和 failure clustering。

禁止写入 cassette、日志、Monitor JSON 或 MCP 输出：

- API key。
- bearer token。
- OAuth access/refresh token。
- service-account JSON。
- 自定义 auth header 值。
- 完整 raw sticky key。

URL 展示字段写入 routing events 前会做脱敏。`api_key`、`access_token`、`token`、`secret`、`password`、`signature` 等敏感 query 参数会显示为 `REDACTED`。

## Limit Scope

常见 limit scope：

- `global`：整个代理进程（pre-selection）。
- `header`：按 `limits.channel_key_header` 指定的请求头取值分桶（pre-selection）。
- `channel`：一个上游渠道（post-selection）。
- `route_target`：一个编译后的 channel + credential target（post-selection）。
- `credential`：某个渠道下的一个 credential（post-selection）。

`header` 需要同时配置 `limits.channel_key_header`。当 `limits.enabled=true` 时，未在上表列出的 scope 值会在配置加载阶段直接报错（`limits.scope %q is not supported`），缺少 `limits.channel_key_header` 的 `header` scope 同样会报错，因此不会再出现“限流被静默忽略”的情况。`config/config.yaml` 目前没有 `limits` 示例块，实际字段以配置结构体为准。

如果 credential 级别限制拒绝请求，通常会看到类似证据：

```text
limit.concurrency_rejected scope=credential
routing.filtered credential_filter_reason=credential_concurrency_full
```

HTTP 状态含义：

- `429`：调用方或配置策略命中 rate/concurrency limit。
- `503`：上游、渠道、credential 或 route target 暂时不可用。

## Monitor 与 MCP

Monitor 和 MCP 从 cassette events 和 indexed trace metadata 读取凭据路由信息，不需要 raw provider secret。

常用入口：

- Monitor `Routing`：查看 selected route、sticky、failure reason 和 credential-aware grouping。
- Trace detail routing context：查看单次请求的 upstream、route target、channel、credential、candidate 和 sticky break。
- MCP `query_routing_decisions`：查看单条 trace 路由决策。
- MCP `query_sticky_routing`：查看 sticky hit/bind/break。
- MCP `query_failures` / `summarize_failure_clusters`：按 route target、channel、credential 聚类失败。

旧 cassette 仍有效。如果只有 `upstream_id`，Monitor 和 MCP 会退回 upstream 级展示。

## Replay 兼容性

凭据路由不改变 replay 事实：

- 原始 HTTP 请求字节保留。
- 原始 HTTP 响应字节保留。
- V3 routing events 是附加 metadata。
- V2 cassette 仍可读。
- `pkg/replay` 不依赖 channel 或 credential storage。

replay 不会刷新凭据、查询账号、重新执行 sticky binding 或修改健康状态。
