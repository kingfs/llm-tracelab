# 模型广场与渠道管理设计

## 背景

当前 `llm-tracelab` 已经具备多 upstream 路由、模型发现、基础健康状态、selected upstream 录制和 upstream 监控能力，但上游来源仍主要来自 YAML 配置。这个形态适合启动期和测试期，不适合长期使用：

- 增删渠道需要编辑配置并重启。
- API key、headers、模型启停与路由权重不适合直接暴露在静态文件中反复修改。
- 现有 Upstreams 页面偏运行时诊断，缺少模型视角、渠道视角和趋势分析。
- 当某个模型在多个 provider 上同时可用时，缺少统一入口排查“哪个渠道在用、哪个渠道在错、token 花在哪里”。

本设计把 upstream 从“静态配置”升级为“数据库托管的渠道资源”，并新增模型广场、渠道分析和渠道配置页面。目标不是把项目变成云端网关平台，而是在本地优先、可自托管前提下，让用户能稳定管理多个模型渠道并解释路由行为。

## 目标

1. 模型广场：展示系统见过和配置过的所有模型，按模型卡片进入详情，查看 provider 覆盖、请求、错误、token、今日用量和 7/30 天趋势。
2. 渠道视角：展示每个渠道提供的模型数量、请求量、token、错误、健康状态、模型分布和趋势。
3. 渠道配置：通过页面新增、编辑、启用、禁用渠道；支持 base URL、API key、自定义 headers、provider preset、protocol family、routing profile、权重、优先级和模型启停。
4. 探测能力：支持对渠道执行模型发现，优先使用 provider 对应的模型列表端点，并兼容 OpenAI Responses API 相关模型获取方式。
5. 路由融合：数据库配置成为 router 的主配置源，同时保留 YAML 作为 bootstrap、环境覆盖和兼容层。
6. 回放不变：录制、回放和 raw cassette 格式不因为渠道管理变化而破坏兼容性。

## 非目标

- 不做多租户账单、限额、团队权限和集中式云管理。
- 不把不同 provider 协议强行抹平成单一能力模型。
- 不在首期实现自动价格计费或真实成本核算；token 与请求统计先来自已录制 trace。
- 不在代理热路径上为每次请求查询数据库。
- 不要求所有 provider 都能动态列出模型；不支持时允许人工维护模型列表。

## 术语

- 渠道：用户配置的一个可调用上游目标，对应当前 router target/upstream target。
- Provider preset：项目已有的 provider 预设，如 `openai`、`openrouter`、`anthropic`、`google_genai`、`vertex`。
- 模型：路由使用的规范化模型名，如 `gpt-5`、`claude-sonnet-4-5`、`gemini-2.5-flash`。
- 模型供应关系：某个渠道声明、发现或推断支持某个模型。
- 启用模型：某个渠道上的模型被允许参与路由。
- 见过的模型：从 trace、discovery、static config 或人工配置中出现过的模型。

## 现有基线

当前代码中已有可复用能力：

- `internal/router`：多 target、模型到 target 映射、P2C 选择、健康状态和模型刷新。
- `internal/upstream`：provider preset、protocol family、routing profile、模型发现和启动诊断。
- `internal/store`：`upstream_targets`、`upstream_models`、`logs.selected_upstream_*`、upstream analytics 查询。
- `internal/monitor`：requests、sessions、trace detail、performance、upstream detail API。
- `pkg/llm`：跨 provider 的请求/响应模型、usage、endpoint 和 provider error 抽取。

本设计优先扩展这些边界，不引入独立的网关子系统。

## 总体架构

新增 `internal/channel` 作为渠道配置与探测服务，职责是管理数据库中的渠道资源，并向 router 发布可运行配置快照。

推荐分层：

- `internal/channel`
  - CRUD 渠道与模型启停。
  - API key 和 header 的 redaction/encryption 处理。
  - 调用 `internal/upstream` 执行探测。
  - 产出 `[]config.UpstreamTargetConfig` 兼容快照。
- `internal/router`
  - 继续只消费内存快照。
  - 增加 `Reload(targets []config.UpstreamTargetConfig)` 或等价替换机制。
  - 热路径不访问数据库。
- `internal/store`
  - 增加渠道配置表、模型供应关系表和探测历史表。
  - 保留当前 `upstream_targets`、`upstream_models` 作为运行时快照/索引，或迁移为新表的派生投影。
- `internal/monitor`
  - 增加模型广场、渠道列表、渠道配置、探测和模型启停 API。
  - 现有 upstream analytics API 保留并向新页面复用。

数据流：

1. 用户在 Monitor 创建渠道。
2. `channel.Service` 校验并保存配置。
3. 用户点击探测，服务调用 provider 模型列表端点，写入模型供应关系和探测历史。
4. 用户选择模型启用，服务更新模型启用状态。
5. 服务生成 router target 快照并触发 router reload。
6. 请求进入 proxy 后，router 基于内存模型目录选择渠道。
7. recorder 把 selected upstream、model、usage、error 写入 cassette 和 SQLite。
8. 模型广场和渠道分析从 SQLite 派生查询趋势。

## 配置来源策略

### 目标形态

数据库是渠道配置主源。YAML 继续保留：

- server、monitor、auth、trace、database、router 默认策略。
- bootstrap upstreams，用于首次启动或无 DB 记录时导入。
- 环境变量覆盖，用于 CI、本地临时调试和无 UI 场景。

### 启动规则

1. 打开 SQLite 并完成迁移。
2. 如果数据库存在至少一个渠道配置，用数据库构建 router。
3. 如果数据库没有渠道配置，但 YAML 中存在 `upstreams` 或 legacy `upstream`，按兼容规则导入为托管渠道。
4. 导入后不自动覆盖用户在 UI 中的后续修改，除非显式执行 `tracelab db import-upstreams --replace` 之类命令。
5. 如果数据库和 YAML 同时存在配置，启动日志应明确说明“使用数据库配置，YAML upstreams 仅作为未导入的 bootstrap 配置”。

### 导入幂等性

bootstrap 导入使用稳定 ID：

- YAML 显式 `id` 优先。
- legacy single upstream 使用 `default`。
- 无 ID 时从 provider preset + base URL 生成 slug，并在冲突时追加短 hash。

导入只在 DB 无渠道时自动执行，避免启动时覆盖页面修改。

## 数据模型

现有 `upstream_targets` 和 `upstream_models` 已经适合作为“运行时发现快照”，但不够表达用户配置。建议新增配置表，避免把敏感信息和运行时状态混在一起。

### `channel_configs`

渠道主配置。

```sql
CREATE TABLE channel_configs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL,
    provider_preset TEXT NOT NULL DEFAULT '',
    protocol_family TEXT NOT NULL DEFAULT '',
    routing_profile TEXT NOT NULL DEFAULT '',
    api_version TEXT NOT NULL DEFAULT '',
    deployment TEXT NOT NULL DEFAULT '',
    project TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    model_resource TEXT NOT NULL DEFAULT '',
    api_key_ciphertext BLOB,
    api_key_hint TEXT NOT NULL DEFAULT '',
    headers_json TEXT NOT NULL DEFAULT '{}',
    enabled INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 0,
    weight REAL NOT NULL DEFAULT 1,
    capacity_hint REAL NOT NULL DEFAULT 1,
    model_discovery TEXT NOT NULL DEFAULT 'list_models',
    allow_unknown_models INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_probe_at TEXT,
    last_probe_status TEXT NOT NULL DEFAULT '',
    last_probe_error TEXT NOT NULL DEFAULT ''
);
```

说明：

- `api_key_ciphertext` 首期可以先使用本地 DB 明文兼容字段，但接口和 schema 应预留加密字段；生产目标应支持 OS keyring 或本地 master key。
- `api_key_hint` 只保存末尾 4 到 8 位或 provider key 前缀提示，用于 UI 展示。
- `headers_json` 存用户自定义 headers，必须在 API 响应中 redacted。

### `channel_models`

渠道与模型的供应和启用关系。

```sql
CREATE TABLE channel_models (
    channel_id TEXT NOT NULL,
    model TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL,             -- discovered | static | manual | inferred | trace
    enabled INTEGER NOT NULL DEFAULT 1,
    supports_responses INTEGER,
    supports_chat_completions INTEGER,
    supports_embeddings INTEGER,
    context_window INTEGER,
    input_modalities_json TEXT NOT NULL DEFAULT '[]',
    output_modalities_json TEXT NOT NULL DEFAULT '[]',
    raw_model_json TEXT NOT NULL DEFAULT '{}',
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    last_probe_at TEXT,
    PRIMARY KEY (channel_id, model)
);

CREATE INDEX idx_channel_models_model ON channel_models(model);
CREATE INDEX idx_channel_models_enabled ON channel_models(channel_id, enabled);
```

能力字段允许为空。首期不要为了填满这些字段而做不可靠推断。

### `model_catalog`

可选的全局模型目录，用于模型卡片稳定展示。

```sql
CREATE TABLE model_catalog (
    model TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    family TEXT NOT NULL DEFAULT '',
    vendor TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    tags_json TEXT NOT NULL DEFAULT '[]',
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    last_used_at TEXT
);
```

该表只保存用户可见的模型元信息，不作为路由事实来源。模型是否可路由以 `channel_models.enabled` 和 router 内存目录为准。

### `channel_probe_runs`

记录探测历史，便于排查“为什么列表为空”。

```sql
CREATE TABLE channel_probe_runs (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    status TEXT NOT NULL,             -- success | partial | failed
    started_at TEXT NOT NULL,
    completed_at TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    discovered_count INTEGER NOT NULL DEFAULT 0,
    enabled_count INTEGER NOT NULL DEFAULT 0,
    endpoint TEXT NOT NULL DEFAULT '',
    status_code INTEGER NOT NULL DEFAULT 0,
    error_text TEXT NOT NULL DEFAULT '',
    request_meta_json TEXT NOT NULL DEFAULT '{}',
    response_sample_json TEXT NOT NULL DEFAULT '{}'
);
```

不要保存完整 API key 或完整响应大 body。只保存可诊断摘要。

## 统计口径

所有分析统计默认来自 `logs` 派生表，而不是实时调用 provider。

统一口径：

- 请求数：`COUNT(*)`。
- 错误数：`status_code >= 400 OR error_text <> '' OR provider_error <> ''`，具体字段按现有 schema 落地。
- 成功率：成功请求 / 总请求。
- token：优先使用 provider 返回的 usage 字段；缺失时不估算，标记为 unknown。
- 今日：按本地时区自然日，API 可接受 `timezone` 参数，默认使用服务端时区。
- 最近：默认最近 24 小时或最近 N 天滚动窗口，页面文案必须明确。
- 7/30 天趋势：按天 bucket；后续可增加小时 bucket。

模型维度查询基于 `logs.model`。渠道维度查询基于 `logs.selected_upstream_id`，其中 ID 应映射到 `channel_configs.id`。

建议新增可复用聚合结构：

```json
{
  "request_count": 120,
  "failed_request": 4,
  "success_rate": 0.9667,
  "total_tokens": 251000,
  "prompt_tokens": 190000,
  "completion_tokens": 61000,
  "avg_ttft_ms": 820,
  "avg_duration_ms": 4100
}
```

趋势点：

```json
{
  "bucket_start": "2026-05-14T00:00:00+08:00",
  "request_count": 20,
  "failed_request": 1,
  "total_tokens": 43000,
  "model_count": 6
}
```

## 后端 API 设计

API 路径建议挂在现有 Monitor API 下，避免新增管理端口。

### 模型广场

- `GET /api/models`
  - query：`q`、`provider`、`channel_id`、`only_enabled`、`sort`、`window=24h|7d|30d`、`page`、`page_size`
  - 返回模型卡片列表。
- `GET /api/models/{model}`
  - 返回模型详情、渠道覆盖、摘要统计、今日统计、趋势和最近失败。
- `GET /api/models/{model}/channels`
  - 返回支持该模型的渠道、启用状态、健康状态、最近统计。
- `GET /api/models/{model}/trends?window=7d&metric=requests,tokens,errors`
  - 返回趋势点。

### 渠道分析

- `GET /api/channels`
  - 返回渠道列表、配置摘要、模型数量、统计摘要和健康状态。
- `GET /api/channels/{id}`
  - 返回渠道详情、配置摘要、模型分布、错误分布、趋势、最近失败。
- `GET /api/channels/{id}/models`
  - 返回渠道上的模型列表、启用状态、source、last seen、模型级统计。
- `GET /api/channels/{id}/trends?window=7d`
  - 返回请求、token、错误、活跃模型数趋势。

### 渠道配置

- `POST /api/channels`
  - 创建渠道。请求中可包含 api key；响应只返回 redacted 版本。
- `PATCH /api/channels/{id}`
  - 修改名称、URL、preset、headers、权重、启停等。
- `DELETE /api/channels/{id}`
  - 默认软删除或禁用；不建议物理删除历史关联。
- `POST /api/channels/{id}/enable`
- `POST /api/channels/{id}/disable`
- `POST /api/channels/{id}/probe`
  - 执行探测并返回发现模型列表和错误摘要。
- `PATCH /api/channels/{id}/models/{model}`
  - 启用/禁用某个模型，或更新 display name/source。
- `POST /api/channels/{id}/models:bulk-update`
  - 批量启用探测结果。
- `POST /api/router/reload`
  - 管理端主动刷新 router；一般由配置变更自动触发。

### API 安全要求

- API key、Authorization header、provider key header 永远不在响应中明文返回。
- PATCH 修改 headers 时支持保留已有 secret 值，例如 UI 发送 `{ "Authorization": { "keep": true } }`。
- 所有写操作需要复用现有 auth/session 机制。
- 日志记录只能输出 redacted base config，不输出 secret。

## 探测设计

探测由 `channel.Service.Probe(channelID)` 统一入口执行。

### 探测步骤

1. 从 DB 读取渠道配置并解密 secret。
2. 使用 `internal/upstream.Resolve` 得到 `ResolvedUpstream`。
3. 执行 provider startup diagnostics，捕获 base URL、routing profile 和认证问题。
4. 按 provider family 选择模型发现策略。
5. 将模型 ID 规范化为 router 使用的模型 key。
6. upsert `channel_models`、`model_catalog`、`upstream_models`。
7. 写入 `channel_probe_runs`。
8. 触发 router reload 或 refresh target。

### 模型发现策略

OpenAI-compatible：

- 优先调用 `GET {base_url}/models`。
- 响应格式按 `data[].id` 解析。
- 对只支持 Responses API 但仍兼容 OpenAI 模型列表的 provider，仍使用 `/models`。
- 如果 provider 明确不支持 `/models`，允许用户输入 manual/static models。

OpenAI 官方：

- `GET /v1/models` 是主发现方式。
- Responses API 的模型选择仍通过 request body `model` 字段，不需要为 Responses 单独改变路由模型 key。
- 如果后续 OpenAI 模型能力元数据需要细分，可把 `/v1/models/{model}` 或官方模型文档快照作为增强来源，但不作为首期阻塞。

Anthropic：

- 如果 provider 暴露模型列表 API，则使用 provider preset 对应实现。
- 如果不可用，首期以 manual/static models 为主。
- 不应因为不能动态发现而阻止渠道启用。

Google GenAI：

- 使用 `GET /v1beta/models`，解析 `models[].name` 和 `displayName`。
- 模型 key 应同时保存原始 resource name 和路由常用短名的映射策略。首期 router 使用当前 `pkg/llm` 能识别的模型名。

Vertex：

- 很多场景依赖 project/location/publisher scoped resource。
- 动态发现可作为增强，首期允许 `model_resource` 或 manual models。
- 固定 deployment/resource 场景应写入 `source=inferred`。

### 探测失败策略

- 认证失败、404、超时、解析失败要区分错误类型。
- 探测失败不自动禁用渠道，除非用户配置“探测失败自动禁用”。
- 如果已有模型目录，探测失败时保留 last known good catalog。
- 探测结果为 0 个模型时，UI 应允许用户手动添加模型。

## Router 集成

### 快照生成

`channel.Service` 负责把 DB 配置转换为当前 router 可消费结构：

```go
type RuntimeTargetSnapshot struct {
    Targets []config.UpstreamTargetConfig
    Version int64
    BuiltAt time.Time
}
```

转换规则：

- 只包含 `channel_configs.enabled = true` 的渠道。
- `static_models` 来自 `channel_models.enabled = true`。
- `model_discovery` 使用渠道配置。
- `UpstreamConfig` 字段保持与现有 YAML 兼容。
- headers 和 api key 在内存中只给 router/proxy 使用，不进入 API 响应。

### Reload 语义

新增 router reload 应满足：

- 原子替换 target 列表和 model map。
- 不中断已经在飞的请求。
- 已有 target 的 EWMA/health 可按 ID 继承，避免每次编辑名称都丢失健康状态。
- reload 失败时保留旧 router 状态，并返回明确错误。

### 路由行为

- 只有启用渠道上的启用模型参与候选集。
- 渠道禁用后不再进入新请求候选，但历史 trace 仍可在分析页展示。
- 模型禁用后只影响该渠道，不影响其他渠道同名模型。
- unknown model 的 fallback 继续由 `router.fallback.on_missing_model` 控制。

## 页面设计

### 导航

Monitor 导航建议新增或重组为：

- Overview
- Sessions
- Traces
- Models
- Channels
- Audit
- Analysis
- Settings

现有 Upstreams 页面可以演进为 Channels；保留 `/upstreams` 作为兼容路由跳转到 `/channels`。

### Models：模型广场

列表形态：模型卡片 + 筛选。

卡片信息：

- 模型名和 display name。
- provider/channel 数量。
- 最近请求数、错误数、成功率。
- 总 token 和今日 token。
- 今日请求数。
- 最近 7 天 request/token 小趋势。
- 健康摘要：所有渠道健康、部分异常、无可用渠道。

筛选：

- 搜索模型名。
- provider preset。
- enabled only。
- has errors。
- used today。
- source：seen in trace / discovered / manual。

模型详情：

- 顶部摘要：请求、错误、token、今日统计、平均 TTFT、平均耗时。
- 渠道覆盖表：渠道、启用、健康、优先级、权重、最近请求、错误、token、最后成功、最后失败。
- 趋势图：7/30 天 requests、tokens、errors。
- 最近失败：trace 链接、渠道、endpoint、status、provider error。
- 最近请求：可跳转 trace detail。

### Channels：渠道视角

渠道列表：

- 渠道名、provider preset、base URL redacted。
- enabled 状态。
- 模型数量：总数 / 启用数。
- 请求数、错误数、成功率、token。
- 健康状态、last probe、last refresh。
- 最近 7 天趋势 mini chart。

渠道详情：

- 配置摘要：base URL、provider、family/profile、priority、weight、discovery mode。
- 操作：启用/禁用、探测、编辑、刷新 router。
- 模型表：模型名、source、enabled、请求、错误、token、最后调用、最后发现。
- 分布：token by model、request by model、error by model。
- 趋势：7/30 天 requests、tokens、errors、active models。
- 失败分析：状态码分布、provider error 摘要、最近失败 trace。

### Channel Config：渠道配置页

表单字段：

- name。
- base URL。
- provider preset。
- protocol family、routing profile（高级折叠）。
- API key。
- custom headers。
- model discovery mode。
- priority、weight、capacity hint。
- enabled。
- allow unknown models。

交互：

1. 用户填写基础信息。
2. 点击 Test Connection/Probe。
3. 页面展示发现模型列表。
4. 用户勾选启用模型。
5. 保存后自动 reload router。

Headers 编辑：

- 键值表格。
- 常见 header 模板：`Authorization`、`HTTP-Referer`、`X-Title`、provider-specific key。
- secret header 默认 masked。

模型选择：

- 支持全选、搜索、只看新模型、只看已启用。
- 对探测不到的 provider 支持手动添加模型。

## 安全与本地密钥

首期最低要求：

- API 响应和日志全部 redacted。
- raw cassette 继续保存真实上游 HTTP exchange；这是项目事实源约束，用户需自行控制录制目录权限。
- 渠道配置中的 secret 不写入 `upstream_targets` 运行时快照表。

生产目标：

- 使用本地 master key 加密 `api_key_ciphertext` 和敏感 header 值。
- 当前实现使用 `{{output_dir}}/trace_index.secret` 作为本地 AES-GCM master key 文件，权限为 `0600`；旧版明文值保持可读，下一次写入会转换为加密 envelope。
- 运维入口：`llm-tracelab db secret status` 检查 key 文件存在性、可读性和 fingerprint；`llm-tracelab db secret export --out backup.key` 以 `0600` 权限导出本地 master key 备份。
- 轮换入口：`llm-tracelab db secret rotate --yes` 生成新 master key、备份旧 key，并对渠道 API key 与敏感 headers 全量重加密。
- 后续 master key 来源优先级：OS keyring、环境变量、用户指定文件。
- 无加密能力时明确标记 DB secret storage mode 为 `plaintext-local`，UI 显示本地风险提示。

## 存储与迁移

迁移必须 additive：

1. 新增 `channel_configs`、`channel_models`、`model_catalog`、`channel_probe_runs`。
2. 保留 `upstream_targets`、`upstream_models`，作为 router 运行状态和兼容查询投影。
3. 启动时若 DB 中无 channel config，则从现有 YAML/旧 upstream 表导入。
4. `logs.selected_upstream_id` 不改名，语义上与 channel ID 对齐。
5. 历史 trace 中没有 selected upstream 的记录仍可按 provider/model 展示，只是渠道维度归为 `unknown`。

## 测试策略

单元测试：

- YAML bootstrap 导入幂等性。
- channel CRUD redaction。
- secret 更新保留语义。
- provider 探测响应解析。
- channel_models 启停影响 runtime snapshot。
- router reload 原子性和失败保留旧状态。
- 趋势 bucket 边界和时区。

集成测试：

- 使用 fake OpenAI-compatible upstream 返回 `/models`，创建渠道、探测、启用模型、代理请求成功。
- 禁用渠道后同模型路由到另一个渠道。
- 禁用某渠道模型后，该渠道不再参与该模型候选。
- 探测失败保留 last known good catalog。
- 录制 replay 不依赖 channel config。

前端测试：

- 模型广场空状态、错误状态、搜索和筛选。
- 渠道配置表单 secret masking。
- 探测结果选择和批量启用。
- 趋势图无数据、部分 token 缺失、错误数据展示。

## 分阶段实施

### Phase 0：设计和 API 契约

- 添加本文档。
- 确认 DB 表、API response 和 router reload 契约。
- 明确 `/upstreams` 到 `/channels` 的兼容策略。

验收：

- 设计被 README/v1 入口引用。
- 任务可拆分为后端存储、router、monitor API、前端四条线。

### Phase 1：数据库托管渠道

- 新增 channel 配置表和 store 方法。
- 启动时从 YAML bootstrap 导入。
- 实现 channel CRUD API，含 redaction。
- 生成 runtime target snapshot。

验收：

- 无 DB 渠道时旧 YAML 配置继续可用。
- 有 DB 渠道时 router 使用 DB 配置。
- API 不泄漏 secret。

### Phase 2：探测和模型启停

- 实现 channel probe。
- 写入 channel_models/model_catalog/probe_runs。
- 支持模型启用禁用和手动模型。
- 变更后触发 router reload。

验收：

- fake upstream `/models` 可被探测并启用。
- 禁用模型立即影响后续路由。
- 探测失败不破坏已有路由。

### Phase 3：模型广场和渠道分析 API

- 新增模型列表/详情/趋势 API。
- 新增渠道列表/详情/趋势 API。
- 复用现有 upstream analytics，并补齐 model-centric 聚合。

验收：

- 能回答“某模型有哪些渠道提供、最近是否报错、token 花在哪里”。
- 能回答“某渠道哪些模型最耗 token、最近 7/30 天错误是否上升”。

### Phase 4：前端页面

- 新增 Models 页面。
- 将 Upstreams 演进为 Channels。
- 新增渠道配置和探测流程。
- Trace detail 保持 selected upstream 跳转到 channel detail。

验收：

- 用户可以不编辑 YAML 完成新增渠道、探测、启用模型、代理请求、查看统计的闭环。
- 页面在空数据和探测失败时可解释下一步。

### Phase 5：安全与增强

- 本地 secret 加密。
- provider-specific 模型能力增强。
- 更细 bucket 趋势和导出。
- 渠道健康事件 timeline。

## 风险与决策

### Secret 存储

风险：SQLite 本地保存 API key 会增加泄漏面。

决策：首期必须 redaction；生产目标必须支持加密。不要把 secret 写入运行时快照表或探测历史。

### DB 配置与 YAML 冲突

风险：用户编辑 YAML 后发现页面没有变化。

决策：DB 优先，并在启动日志和 Settings 页面明确当前配置源。提供显式导入命令，而不是隐式覆盖。

### 探测覆盖不完整

风险：很多 provider 不支持标准模型列表。

决策：探测是辅助能力，不是启用渠道的前置条件。manual/static models 是一等能力。

### Router reload 复杂度

风险：热更新可能影响在飞请求。

决策：router 使用快照原子替换；旧 target 允许被在飞请求持有，完成后释放。首期不做跨进程协调。

### 统计口径误解

风险：token 缺失时用户误以为是 0。

决策：API 和 UI 区分 `0` 与 `unknown`。聚合时 total tokens 只统计已知 usage，并返回 missing usage request count。

## 最小可交付闭环

第一版可交付范围应压缩到：

1. 数据库保存渠道配置。
2. UI 创建 OpenAI-compatible 渠道。
3. 调用 `/models` 探测。
4. 勾选模型启用。
5. router 热加载并可代理请求。
6. 模型广场显示模型卡片、渠道覆盖、请求数、错误数、token 和 7 天趋势。
7. 渠道详情显示模型分布、错误、token 和最近失败。

这条闭环完成后，再扩展 provider-specific 能力、secret 加密和更复杂趋势。

## 开发进度

截至 2026-05-14：

- 已完成 Phase 1/2 后端主链路：渠道配置表、渠道模型表、模型目录、探测记录、YAML bootstrap 导入、DB 优先 router 配置源、router reload、探测写入模型供应关系、模型启停影响路由。
- 已完成 Phase 3 分析 API：`/api/models`、`/api/models/{model}`、`/api/channels`、`/api/channels/{channel}` 返回模型/渠道视角的请求数、错误数、token、趋势、渠道覆盖、模型用量和最近失败。
- 已完成 Phase 4 首版 UI：Monitor 新增 Models、Model Detail、Channels、Channel Detail 页面；支持渠道创建、探测、渠道启停、模型启停、模型卡片、渠道卡片、趋势和失败明细。
- 已修正渠道 PATCH 语义：部分更新渠道时保留未提交的 `allow_unknown_models` 等既有配置，避免 UI 启停操作造成隐式配置回退。
- 已补齐人工模型添加闭环：`POST /api/channels/{channel}/models` 可手动添加模型并写入模型目录，Channel Detail 可直接添加模型，用于不支持模型列表探测的 provider。
- 已完成渠道编辑首版：Channel Detail 支持编辑基础字段、API key、权重、探测模式和 headers；headers 支持字符串更新与 `{ "keep": true }` 保留 redacted secret，避免 UI 回传 `***` 覆盖真实密钥。
- 已完成 Trace detail 入口衔接：selected upstream 同时提供 Open Channel 与 Open Upstream，前者面向托管渠道配置与分析，后者保留旧运行时诊断视角。
- 已显式标记 secret 存储模式：渠道 API 返回 `secret_storage_mode`，UI 在渠道卡片与详情页展示本地 secret 存储模式。
- 已引入本地加密存储：API key 与 `Authorization`、`api-key`、`token` 类敏感 header 在 SQLite 中以 `tlsec:v1:` envelope 保存，运行时读取仍返回明文供代理调用；历史明文数据保持兼容。
- 已新增本地 master key 运维入口：`db secret status/export` 支持检查 key 文件、查看 fingerprint、导出备份；status 不返回原始 key。
- 已新增本地 master key rotate：`db secret rotate --yes` 备份旧 key，生成新 key，并全量重加密渠道 API key 与敏感 headers。
- 已在 Monitor UI 增加本地 secret key 运维入口：Channels 页面展示 key 状态、路径、fingerprint，支持下载备份和确认后轮换。
- 已增强渠道探测失败 UX：`channel.Service.Probe` 对认证、404、网络、限流、上游 5xx、JSON/schema 和配置类错误做稳定分类；分类与 retry hint 写入 `channel_probe_runs.request_meta_json`，渠道详情 API 返回最近探测记录，Channel Detail 展示成功/失败探测、错误摘要和重试建议。
- 已按产品取舍跳过 UI E2E 截图归档，不作为 v1 必做项。
- 已增强探测结果启用体验：探测支持 `enable_discovered` 选项，默认保持兼容；Monitor UI 探测时先发现模型但不自动启用新模型，保留既有 manual/static/discovered 模型状态，并在模型表标记待启用的新发现模型。
- 已新增 UI 浏览器 smoke：`task ui:test` 使用 Playwright 和 mock Monitor API 覆盖模型广场、模型详情、渠道列表、渠道详情、核心操作与 Trace 到 Channel/Upstream 跳转。
- 已新增真实 Monitor server 浏览器 E2E：`task ui:test:real` 启动本地 Go Monitor fixture、临时 SQLite 和本地假上游，覆盖嵌入式 UI 到真实 API 的模型/渠道/trace 主链路，并覆盖本地失败探测提示。

下一步建议：

1. 增加批量模型选择操作：对探测到但未启用的模型提供批量启用/禁用，减少渠道首次接入时的逐个点击成本。
2. 补齐渠道配置表单高级字段：协议族、routing profile、Azure/Gemini/Vertex 相关字段使用结构化控件，降低手填出错概率。
