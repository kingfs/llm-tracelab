# 路由、渠道与凭据

本文描述当前代码事实下的渠道（provider）配置、模型与别名管理、路由选择、凭据路由与 limit scope。
渠道配置的长期归属是应用数据库，YAML 只承担启动配置与首次 bootstrap。
相关主题见 [ARCHITECTURE.md](./ARCHITECTURE.md)、[PROTOCOLS_AND_PROVIDERS.md](./PROTOCOLS_AND_PROVIDERS.md)、[RESPONSES_RUNTIME.md](./RESPONSES_RUNTIME.md)、[MONITOR_GUIDE.md](./MONITOR_GUIDE.md)、[MCP_GUIDE.md](./MCP_GUIDE.md) 与 [STORAGE_AND_DEPLOYMENT.md](./STORAGE_AND_DEPLOYMENT.md)。

## 配置来源与所有权

- YAML 的 `upstreams`（或 legacy 单 `upstream`）只在数据库尚无渠道时导入。
  `channel.Service.BootstrapFromConfig` 把 target 写入 `channel_configs` / `channel_models`，导入渠道的 `source` 记为 `bootstrap`；Monitor 中创建的渠道 `source` 为 `manual`。
- 导入使用稳定 ID：YAML 显式 `id` 优先；legacy 单 upstream 使用 `default`；没有 ID 时由 provider preset 与 base URL 生成 slug，发生冲突再追加序号。
- 第一次数据库写入（bootstrap 导入或任意管理写）会在应用库 `app_settings` 写入 `channels.initialized`。
  此后数据库拥有路由配置，即使所有渠道都被禁用或删除。
- `GET /api/settings/channels` 返回 `{"initialized": <bool>}`；`DELETE /api/settings/channels` 在 `ConfigurationTransaction` 内清除该标记。
  仅当数据库仍然没有任何渠道时，下一次启动才会重新执行 YAML bootstrap。
- 带显式 `credentials` 列表的 YAML 渠道保持 YAML 管理：`cmd/server` 检测到显式 credentials 时以 `WithReadOnly(true)` 构造 channel service，所有 Monitor 渠道/模型/别名写路径返回 `409` 与 `upstreams are managed by YAML credentials; edit YAML and restart instead`。
  `BootstrapFromConfig` 也会跳过含显式 credentials 的配置，不把其导入数据库。
- 环境变量只用于启动与本地部署覆盖，不是长期的渠道/模型管理面。

## 数据模型

- `channel_configs`（渠道主配置）：`id`、`name`、`description`、`source`、`base_url`、`provider_preset`、`api_type`、`mode`、`capabilities_json`、`protocol_family`、`routing_profile`、`api_version`、`deployment`、`project`、`location`、`model_resource`、`api_key_ciphertext`、`api_key_hint`、`headers_json`、`enabled`、`priority`、`weight`、`capacity_hint`、`model_discovery`、`allow_unknown_models`、`created_at`、`updated_at`、`last_probe_at`、`last_probe_status`、`last_probe_error`。
- `channel_models`（渠道与模型的供应/启用关系）：唯一键 `(channel_id, model)`；字段包括 `display_name`、`source`（`discovered` / `static` / `manual` / `inferred` / `trace`）、`enabled`、`supports_responses` / `supports_chat_completions` / `supports_embeddings`（三态 `INTEGER NULL`，`NULL` 表示继承渠道级能力）、`context_window`、`max_output_tokens`、`compact_history_item_threshold`、`upstream_model`、`profile_source`、`profile_adoption_status`、输入/输出模态 JSON、`raw_model_json` 与 `first_seen_at` / `last_seen_at` / `last_probe_at`。
- `model_aliases`：`id`、`alias`、`target_model`、`channel_id`（空表示全局别名）、`enabled`、`description`、`source`、时间戳。
  `channel_id` 非空时表示该别名只允许在该渠道展开；写入时校验禁止直接别名循环、禁止同一 `alias + channel` 重复激活目标，冲突返回 `model alias conflict`。
- `model_catalog`：按模型保存展示元信息（`display_name`、`family`、`vendor`、`description`、标签、首末出现时间），它不是路由事实来源；可路由性由 `channel_models.enabled` 与 router 内存快照决定。
- `channel_probe_runs`：探测历史（状态、耗时、发现/启用数量、endpoint、错误文本、`request_meta_json`）。
- `upstream_targets` / `upstream_models`：旧的运行时快照与兼容投影，由配置事务和后台 refresh 写入。
- 凭据：YAML `credentials[]` 的字段是 `id`、`name`、`enabled`、`api_key`、`headers`、`concurrency_limit`。
  未配置显式 credentials 时，inline `upstream.api_key` 被视为一个隐式 credential（见下）。

SQLite 走启动期 schema 初始化，Postgres 走 `ent/postgres-migrations/` 中的版本化迁移（例如 `20260629192000_add_model_aliases`）。

## 管理写路径的一致性

所有管理写（渠道、渠道模型、模型别名、provider setup、probe apply）都经过 `store.ConfigurationTransaction`：

- 它在整个回调期间持有进程级配置锁 `configMu` 与 upstream 写锁 `upstreamMu`，并开启一个 SQL 事务；原始 SQL 与 ent 操作共享该事务。
- 回调拿到 `tx *store.Store` 与 `commit func() error`，必须先准备好运行时快照再显式 `commit`；其它退出路径回滚。回调结束仍未 commit 则报 `configuration transaction was not committed`。
- Monitor 侧由 `configurationAPIHandler` 统一包装：GET/HEAD 与 `/validate` 直接执行；其它方法先检查 `svc.ReadOnly()`，只读时返回 409；否则在事务内以 `txService`/`txStore` 执行原 handler，把响应缓冲到 `configurationResponse`。
- handler 返回错误状态时事务回滚；唯一例外是 `/probe` 返回 `502` 且结果为 `failed`，此时提交事务以保留诊断记录，但不会改动路由。
- 写成功后依次执行 `MarkConfigurationInitialized`、`txService.RuntimeTargets()`，再以 `rtr.ReloadWithCommit(targets, tx, commit)` 在提交前构建新快照。提交成功后才对外发布路由；失败则保留旧快照。
- `RuntimeTargets` 只包含 `enabled = true` 的渠道：`StaticModels` = 该渠道已启用模型加上可在该渠道展开的启用别名，`DisabledModels` 记录被禁用的模型，`ConfiguredModelsOnly` 为 `true`；headers、capabilities、`model_capabilities` 与 API key 只在内存中交给 router/proxy。
- reload 按 target ID 继承既有运行时状态（EWMA/health），锁顺序固定为 `configMu → upstreamMu → reloadMu → r.mu`。
- 后台 upstream refresh 以 best-effort 方式持久化：`LockUpstreamWrites` 阻塞等待，`TryLockUpstreamWrites` 在配置变更持锁时直接跳过写库。

## 模型与渠道管理 API

以下路径由 `internal/monitor/server.go` 真实注册，全部经过 `monitorAuthRequired`。

渠道与渠道模型：

- `GET /api/channels`、`POST /api/channels`
- `GET /api/channels/{id}`、`PATCH /api/channels/{id}`、`DELETE /api/channels/{id}`
- `POST /api/channels/{id}/probe`
- `GET /api/channels/{id}/models`、`POST /api/channels/{id}/models`
- `PATCH /api/channels/{id}/models/batch`
- `PATCH /api/channels/{id}/models/{model}`、`DELETE /api/channels/{id}/models/{model}`

`GET /api/channels/{id}` 返回配置摘要、模型数量、用量摘要、趋势、模型用量、最近失败与最近探测记录。
渠道启停通过 `PATCH /api/channels/{id}` 的 `enabled` 字段完成；`PATCH /api/channels/{id}/models/{model}` 支持 `display_name`、`enabled`、三态能力覆盖（显式 `null` 表示清除回继承）、`context_window`、`max_output_tokens`、`upstream_model` 等。

Provider 与探测：

- `GET /api/provider-presets`
- `POST /api/provider-probe`
- `POST /api/provider-probe/report`
- `POST /api/provider-probe/report/apply`
- `POST /api/provider-setup/validate`、`POST /api/provider-setup/apply`

模型与别名：

- `GET /api/models`、`GET /api/models/{model}`
- `GET /api/models/{model}/spec-lookup`、`POST /api/models/{model}/spec-lookup`
- `GET /api/model-aliases`、`POST /api/model-aliases`
- `POST /api/model-aliases/validate`
- `GET /api/model-aliases/{id}`、`PATCH /api/model-aliases/{id}`、`DELETE /api/model-aliases/{id}`

`GET /api/models` 只返回当前窗口内 `request_count > 0` 的模型；模型详情的渠道覆盖来自 `channel_models`。

设置、路由解释与本地密钥：

- `GET /api/settings/routing`、`PATCH /api/settings/routing`
- `GET /api/settings/channels`、`DELETE /api/settings/channels`
- `POST /api/routing/inspect`
- `GET /api/routing/exchanges`、`GET /api/routing/summary`
- `GET /api/secrets/local-key`（`?export=1` 下载备份）、`POST /api/secrets/local-key?rotate=1`
- 旧运行时诊断：`GET /api/upstreams`、`GET /api/upstreams/{id}`

`PATCH /api/settings/routing` 接受 `responses_strategy`、`selection_policy`、`missing_model_policy`、`route_plan_log_level`；持久化到应用库 `app_settings` 键 `routing.settings`。
`POST /api/routing/inspect` 接受 `endpoint`、`model`、`stream`、`tools`，返回 route plan 与候选排除原因。

## Provider 探测与模型写回

探测入口是 `channel.Service.ProbeWithOptions(channelID, options)`，`options` 对应 `POST /api/channels/{id}/probe` 的 `enable_discovered` 与 `detect_provider`：

1. 从数据库读取渠道并解密 secret。
2. 可选执行 provider 自动识别（`providerprobe.Probe`）。
3. 用 `internal/upstream.Resolve` 解析出 `ResolvedUpstream`，并执行 startup diagnostics 得到连通性 endpoint。
4. 按 provider family 选择模型发现策略，得到模型 ID 列表。
5. 成功时 upsert `channel_models`、`model_catalog`、`upstream_models`，更新渠道 `last_probe_*`，并写一条 `channel_probe_runs`。

行为要点：

- 新发现模型的默认启用状态由 `enable_discovered` 决定，缺省为 `false`；重复探测只刷新 `last_seen_at` / `last_probe_at`，不会覆盖操作者的启用状态或已采纳的模型 profile。
- `upstream_models` 只在探测成功时整体替换。
- 探测失败不会自动禁用渠道；失败会写入 `channel_probe_runs`，并在 `request_meta_json` 中记录分类与重试建议：`auth_error`、`not_found`、`invalid_json`、`network_error`、`rate_limited`、`upstream_error`、`configuration_error`、`probe_error`。
- `POST /api/provider-probe/report/apply` 只对 `detected` 的结果生效，且只填补缺失字段（`no missing fields to apply`）；应用了字段才触发 router reload。
- `POST /api/provider-probe` 是无渠道的临时探测（`provider_id`、`base_url`、`api_key`、`headers`、`api_type`、`protocol_family`），不需要已有渠道。

## 路由选择与 responses_strategy

入口与执行模式定义在 `internal/routeplan`：客户端入口有 `chat_completions`（`/v1/chat/completions`）、`responses`（`/v1/responses`）、`anthropic_messages`（`/v1/messages`）；执行模式有 `proxy_pass` 与 `responses_server`。

- Chat Completions 与 Anthropic Messages 只生成 `proxy_pass` 计划；候选过滤顺序为渠道启用 → 模型匹配 → endpoint 能力（`requires_chat_completions` / `requires_anthropic_messages`）→ `HasTools` 时的 tool calling 能力。
- 模型匹配基于渠道的启用模型集合与别名；`UpstreamCandidate.ModelCapabilities` 支持按模型覆盖能力，键大小写不敏感。
- `/v1/responses` 按 `responses_strategy` 生成计划（rank 越小越优先）：
  - `auto`（默认）与 `prefer_native`：native Responses 直通 rank 0，Chat Completions 本地 runtime rank 1。
  - `prefer_local_server`：本地 runtime rank 0，native 直通 rank 1。
  - `native_only`：只保留 native，并把 chat 候选标记为 `strategy_disallows_chat_fallback`。
  - `local_server_only`：只保留本地 runtime，并把 native 候选标记为 `strategy_disallows_native_responses`。
  - 其它取值返回 `unsupported_responses_strategy`。
- 代理热路径上的 `responsesRoutingDecision` 与上述规则一致：native 可选中则直通；native 存在但本请求不可选时直接拒绝并说明原因；否则在有本地 runtime 且存在 Chat Completions backend 时走 `responses_server`。
- 每个 plan candidate 都带过滤原因（`selected`、`channel_not_enabled`、`model_not_matched`、`requires_chat_completions`、`requires_responses`、`requires_anthropic_messages`、`requires_tool_calling`、`no_route_candidate` 等），route plan 会写入 V3 cassette event 并在 Monitor trace detail 展示。
- native-vs-local 按模型解析：`channel_models.supports_responses` / `supports_chat_completions` 的显式值覆盖渠道级 `api_type` / `capabilities`，未声明则回退渠道级；YAML 等价项是 `upstream.model_capabilities`。
- 负载均衡策略取自 router 配置 `router.selection.policy`：`p2c`（默认）或 `first_available`；缺失模型回退由 `router.fallback.on_missing_model` 控制（默认 `reject`）。
- 别名在 proxy 侧改写上游请求体 `model`：`Target.ResolveModelAlias` 命中时把下游模型名替换为 `target_model`，route plan event 同时记录 `requested_model` 与 `upstream_model`。

## Sticky Route Target

- route target 身份是 `<channel_id>:<credential_id>`，没有显式凭据时为 `<channel_id>:default`。
- sticky key 依次从请求头 `Session_id`、`X-Claude-Code-Session-Id`、`X-Codex-Window-Id`（取 `:` 前前缀）、`X-Codex-Turn-Metadata` 的 `session_id` 提取；都没有时回退到请求体的 `previous_response_id`。
- binding 默认 TTL 为 1 小时，记录在进程内的 `StickyBindingStore`。
- sticky 命中且该 target 仍可用时复用同一 route target；命中但 target 不可用时记录一次 `sticky break` 并重新绑定到新的可用 target；未命中则正常选择后绑定。
- Monitor、MCP 与 cassette event 只暴露 `sticky_key_fingerprint`，不暴露原始 sticky key。

## 凭据与隐式 default credential

- 渠道负责 base URL、provider preset、routing profile、priority/weight/capacity 与模型覆盖；credential 负责 secret、credential 级 `headers` 与 route target 身份。
- 配置了显式 `credentials` 时，每个 credential 展开成一个独立 route target（`<channel_id>:<credential_id>`）；credential 的 `api_key` 覆盖 target 的 resolved key，`credential_id` 缺省由 `name` 生成 slug，再缺省为 `credential-N`。
- 没有显式 `credentials` 且 `upstream.api_key` 非空时，该 inline key 被视为隐式 credential：`credential_id = default`、`route_target_id = <channel_id>:default`。
- 没有显式 credentials 也没有 inline key 时，渠道仍会产生一个 route target，其 credential id 为 `default`。
- `api_key` 应引用环境变量（例如 `$env:OPENAI_PRIMARY_API_KEY`），`id` 要稳定，因为它进入 route target identity 并可能出现在 cassette metadata 中。
- `credentials[].concurrency_limit` 目前只是配置字段，没有运行时消费者；实际并发限制由下方 limit scope 统一处理。

## Limit 与 scope

`limit` 由 `LimitConfig`（`enabled`、`scope`、`max_concurrent`、`max_queued`、`channel_key_header`）控制，作用在代理热路径上。

- scope 取值只有 `global`、`header`、`channel`、`route_target`、`credential`。
- 生效值由 `ScopeOrDefault` 决定：显式 `scope` 优先；未写 `scope` 但写了 `channel_key_header` 时按 `header`；否则按 `global`。
- 当 `limits.enabled = true` 时，未知 scope 在配置加载期直接报错 `limits.scope %q is not supported; use one of global, header, channel, route_target, credential`；`header` scope 缺少 `limits.channel_key_header` 也会报错。因此不会出现“限流被静默忽略”。
- pre-selection 判定 `global` 与 `header`（按 `channel_key_header` 取值分桶，取不到时归入 `missing`）。
- post-selection 判定 `channel`（键为 channel ID）、`route_target`（键为 route target ID）、`credential`（键为 channel + credential）——三者都依赖 router 选择结果中的凭据身份。
- 拒绝语义：并发/速率拒绝返回 `429` 与 `limit.concurrency_rejected`；队列饱和返回 `503` 与 `limit.queue_saturated`。
- 拒绝会写入带 `limit_key_fingerprint` 的事件，不写原始 limit key。
- tracked 的 `config/config.yaml` 目前没有 `limits` 示例块，实际字段以 `internal/config` 结构体为准。

## 安全 metadata 与脱敏

V3 cassette routing event 会写入的安全字段包括：`route_target_id`、`channel_id`、`credential_id`、`credential_hint`、`sticky_key_fingerprint`、`credential_selectable`、`candidate` 列表中的 `base_url`（经过 `redaction.DisplayURL`）、`excluded` 与 `filter_reason`。

- `credential_health_state` 与 `credential_filter_reason` 在事件结构中存在，但当前代码没有为它们赋值，因此实际不会出现。
- 禁止写入 cassette、日志、Monitor JSON 或 MCP 输出：API key、bearer token、OAuth access/refresh token、service-account JSON、自定义 auth header 值、完整 raw sticky key。
- `redaction.DisplayURL` 会把 URL userinfo 中的密码替换为 `REDACTED`，并把名字含 `key`、`token`、`secret`、`password`、`passwd`、`credential`、`signature`、`sig`、`access_token`、`api_key` 的 query 参数值替换为 `REDACTED`。
- `redaction.SafeCredentialHint` 先做 metadata 脱敏再截断到 32 字符，值等于 `REDACTED` 时按空处理。
- 渠道 API 返回值中的 `headers` 会对敏感 header 名（如 `Authorization`、`api-key`、`token` 类）掩码为 `***`；写路径接受 `{ "keep": true }` 表示保留原值，避免 UI 回传掩码覆盖真实密钥。
- 渠道 API 始终不返回明文 API key，只返回 `api_key_hint` 与 `secret_storage_mode`；`base_url` 按配置原样返回，路由事件里的候选 base URL 才会做 URL 脱敏。
- 本地 secret：API key 与敏感 header 以 `tlsec:v1:` envelope 保存，主密钥文件是 `{{output_dir}}/trace_index.secret`（权限 `0600`，本地 AES-GCM）；历史明文保持可读，下次写入时转为加密 envelope。
- 运维入口：`llm-tracelab db secret status`（检查 key 文件、可读性与 fingerprint）、`db secret export --out backup.key`、`db secret rotate --yes`（备份旧 key、生成新 key、全量重加密）；Monitor 侧对应 `/api/secrets/local-key` 的状态、导出与轮换。
- 没有加密能力时 `secret_storage_mode` 为 `plaintext-local`，UI 据此提示本地风险。

## Monitor 与 MCP 的管理入口

Monitor 侧是渠道/模型/别名配置的主入口：导航中的 `Providers` 页面（API 仍为 `/api/channels`）负责渠道创建、编辑、启停、探测与 headers/能力配置，`Models` 页面负责模型广场、模型详情、模型启停与别名相关操作，`Routing` 页面展示 selected route 记录，`Connect` 页面展示协议入口。

MCP 侧只提供查询工具，没有渠道/模型/别名写入口：

- `list_upstreams`：上游分析列表。
- `query_routing_decisions`：单条 trace 的路由决策事件、候选、选中上游与失败原因。
- `query_sticky_routing`：按状态、上游、前一上游与 fingerprint 过滤 `routing.sticky.*` 事件。
- `query_failures`、`summarize_failure_clusters`：失败 trace 与失败聚类。

CLI 侧的管理入口包括 `llm-tracelab provider probe` / `probe-report` / `probe-apply`、`models codex-config`、`db secret status/export/rotate`。

## Replay 兼容性

凭据路由不改变 replay 事实：

- 原始 HTTP 请求与响应字节仍然完整保留。
- V3 routing event 是附加 metadata；V2 cassette 仍可读。
- `pkg/replay` 不依赖渠道或凭据存储。
- replay 不会刷新凭据、查询账号、重新执行 sticky binding 或修改健康状态。
- 旧 cassette 只有 `upstream_id` 时，Monitor 与 MCP 退回 upstream 级展示。

## 非目标与未实现

- 不做多租户计费、团队权限、集中式云管理与订阅转售。
- 不在代理转发热路径做跨协议翻译；唯一例外是 `/v1/responses` 的本地 Responses runtime。
- 没有 `POST /api/router/reload`，也无需手动 reload：管理写成功即自动发布新快照。
- 没有 `POST /api/channels/{id}/enable`、`/disable` 与 `models:bulk-update`；启停走 `PATCH`，批量启停走 `PATCH /api/channels/{id}/models/batch`。
- 没有 `GET /api/models/{model}/channels`、`GET /api/models/{model}/trends`、`GET /api/channels/{id}/trends`；覆盖与趋势在模型详情与渠道详情响应内返回。
- `selection_policy`、`missing_model_policy`、`route_plan_log_level` 会被持久化与校验，但当前没有作用于运行中的 router；运行时策略来自 YAML `router.selection.policy` 与 `router.fallback.on_missing_model`。
- `credentials[].concurrency_limit` 没有运行时消费者。
- `requires_local_responses_runtime` 在 routeplan 中定义，但当前没有代码设置它。
- 没有独立的 `route_decisions` 表；路由决策以 V3 cassette event 与日志字段形式保存。
- 没有渠道导入/导出能力。
- `model_catalog` 只由探测与人工写回填充，没有独立的目录管理页面或 API。
- `POST /api/settings/channels` 不存在；该端点只有 `GET` 与 `DELETE`。
