# 协议族与上游 Provider

TraceLab 不为每个上游写一套独立集成，而是把上游解析为协议族（`protocol_family`）、路由 profile（`routing_profile`），以及鉴权、版本、header 与 URL 构造规则。代理是协议感知的透传录制与路由层：它按协议族识别请求、录制 `.http` cassette、把 trace 解析为 Observation IR，但不在转发路径上把一个协议族的请求 schema 翻译成另一个。

协议 schema 快照与英文事实源见 [协议参考](./protocol-reference/README.md)、[已实现的协议](./protocol-reference/implemented-protocols.md) 与 [协议差异](./protocol-reference/protocol-differences.md)。

## 术语

- **Provider**：面向用户（Monitor UI 与文档）的上游服务配置，可以是官方模型厂商、公司网关或本地/自托管代理。
- **协议族（`protocol_family`）**：上游 API 请求/响应 schema、认证方式与 endpoint 形态的归类。当前有 4 个：`openai_compatible`、`anthropic_messages`、`google_genai`、`vertex_native`。
- **路由 profile（`routing_profile`）**：同一协议族内的 URL 构造与鉴权差异，例如 Azure、vLLM、Vertex express 与 project/location。
- **客户端入口（entrypoint）**：TraceLab 暴露给客户端的 URL 形态，只选择客户端 API 形态，不跨协议族翻译。
- **能力（capability）**：`capabilities.*` 布尔声明，用于按 endpoint 判断某个上游或模型能否服务某类请求。

内部实现与数据库仍沿用 `channel_*` 命名：`channel_configs`、`channel_models`、`/api/channels`。这些是实现细节；除讨论存储与迁移外一律称 Provider。Monitor UI 已把 Channels 改名为 Providers，旧前端路由 `/channels` 会重定向到 `/providers`。存储细节见 [存储与部署](./STORAGE_AND_DEPLOYMENT.md)。

## 协议族与请求入口

客户端入口前缀在 routing、recording、forwarding 之前由 `internal/proxy/entrypoints.go` 归一化。原始请求 body 保持原样；`.http` 记录中的协议路径保持规范化，因此 parser 与 replay 稳定。

| 客户端入口 | 归一化 endpoint | 目标客户端 | 需要的 provider 能力 |
| --- | --- | --- | --- |
| `/v1/chat/completions` | `/v1/chat/completions` | OpenAI 兼容 SDK 与旧集成 | `openai_compatible` Chat Completions |
| `/responses`、`/v1/responses` | `/v1/responses` | Codex 与 OpenAI Responses 客户端 | 匹配的原生 Responses 上游，或可承接本地 Responses runtime 的 Chat Completions 上游 |
| `/anthropic/messages`、`/anthropic/v1/messages`、`/v1/messages` | `/v1/messages` | Claude Code 与 Anthropic SDK | `anthropic_messages` |
| `/anthropic/messages/count_tokens`、`/anthropic/v1/messages/count_tokens` | `/v1/messages/count_tokens` | Anthropic token 计数客户端 | `anthropic_messages` |
| `/v1/tokenize`、`/v1/detokenize` | `/tokenize`、`/detokenize` | vLLM 分词客户端 | `openai_compatible`（`vllm_openai`） |

`/v1/models` 是跨上游聚合的 OpenAI 兼容模型列表 endpoint，不是单上游透传。

各协议族的当前 endpoint 覆盖与 parser 覆盖：

| 协议族 | provider 标签 | 路由 profile | 当前 endpoint 覆盖 | parser 覆盖 |
| --- | --- | --- | --- | --- |
| `openai_compatible` | `openai_compatible`、`azure_openai`、`vllm` | `openai_default`、`azure_openai_v1`、`azure_openai_deployment`、`vllm_openai` | `/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`、`/v1/models`、vLLM `/tokenize`、`/detokenize` | Chat Completions、Responses、Models（Observation IR parser）；Embeddings 与 Tokenization 只被分类、路由与录制 |
| `anthropic_messages` | `anthropic` | `anthropic_default` | `/v1/messages`、`/v1/messages/count_tokens`；连通性与模型发现使用 `/v1/models` | Messages |
| `google_genai` | `google_genai` | `google_ai_studio` | `/v1beta/models/{model}:generateContent`、`/v1beta/models/{model}:streamGenerateContent`、`/v1beta/models` | GenerateContent、streamGenerateContent |
| `vertex_native` | `vertex_native` | `vertex_express`、`vertex_project_location` | Vertex Gemini `generateContent`、`streamGenerateContent`、模型列表路径 | GenerateContent、streamGenerateContent |

`openai_compatible` 要点：

- `upstream.base_url` 必须包含上游 API prefix，例如 `/v1`、`/api/v1`、`/openai`、`/openai/v1`；唯一例外是 `provider_preset: deepseek`，它以 origin 作为 base URL，endpoint 位于 `/responses`、`/models`。
- `vllm_openai` profile 下，客户端 `/tokenize`、`/v1/tokenize`、`/detokenize`、`/v1/detokenize` 路由到 vLLM 根分词 endpoint。
- Embeddings 与 vLLM 分词请求会被分类、路由与录制，但没有对应的 Observation IR parser，因此不是深度解析目标。
- Responses 与 Chat Completions 是 OpenAI 的两个不同 surface；Codex 流量通常走 `/v1/responses`。

`anthropic_messages` 要点：

- 鉴权重写为 `x-api-key`；`anthropic-version` 缺失时按 `api_version` 补齐，默认 `2023-06-01`。
- 请求与响应 body 原样透传；请求、响应与 streaming 事件会解析进 Observation IR。

`google_genai` 与 `vertex_native` 是独立协议族，因为其 endpoint 形态、认证模型与请求/响应 schema 都不同于 OpenAI 与 Anthropic。两者都经 GenerateContent 语义适配路径解析；Vertex 使用资源路径（`model_resource`，可选的 `project`/`location`）与 `Authorization: Bearer` 鉴权。

## 协议差异要点

各主流 API 在概念层高度重叠（model、instructions、用户输入、工具定义、生成内容、tool calls、usage、streaming），但差异足以让 TraceLab 把它们当作独立协议族。

| API surface | 请求核心 | 响应核心 | streaming 形态 | tool 形态 | usage 形态 |
| --- | --- | --- | --- | --- | --- |
| OpenAI Chat Completions | 带 role 的 `messages[]`，可选 `tools[]` | `choices[].message` | SSE chunk 中的 `choices[].delta` | `tools[].function`、`tool_calls[]` | `usage.prompt_tokens`、`completion_tokens`、`total_tokens` |
| OpenAI Responses | `input`、`instructions`、`tools`、reasoning/text 配置 | `output[]` 类型化条目 | 名为 `response.*` 的事件与 item/content delta | 类型化输出条目，如 function call 与 tool call output | response `usage` 的 input/output token 字段 |
| Anthropic Messages | `system`、`messages[]`、`tools[]`、`max_tokens` | 顶层 assistant message 加 `content[]` block | message/content block 生命周期事件与 delta | `tool_use`、`tool_result` 等 content block | `usage.input_tokens`、`output_tokens`、cache read/create 字段 |
| Google Gemini GenerateContent | `contents[]`、`systemInstruction`、`tools[]`、生成与安全配置 | 含 `content.parts[]` 的 `candidates[]` | 返回 GenerateContentResponse chunk | parts 中的 function declaration 与 function call/response | `usageMetadata` token 字段 |
| Vertex Native GenerateContent | 与 Gemini 类似，但使用 Vertex 资源路径与鉴权 | 与 Gemini 类似的响应 schema | Vertex streaming 变体 | Vertex/Google tool schema | Vertex usage metadata |

识别不等于转换。TraceLab 能识别并解析 Anthropic Messages、OpenAI Responses、OpenAI Chat Completions 与 Gemini GenerateContent，含义是：归类 endpoint、抽取 model 与 usage 元数据、记录 provider 特有 streaming 事件、把 provider payload 解析为 Observation IR、保留原始字节用于 replay。转换则需要改写请求 schema、转换 tool 声明与 tool call/result 连接、按 provider 隐私规则映射 reasoning/thinking 字段、映射 cache 控制与计费、在不丢失部分 tool-call 状态的前提下转换 streaming 事件生命周期，并保留 provider 特有错误与安全语义；这些都不在当前代理转发路径中。

路由后果：客户端发 `/v1/messages`，被选中的上游必须支持 Anthropic Messages 语义，OpenAI 兼容上游上的某个模型不会因为名字相同就能通过 `/v1/messages` 使用；客户端发 `/v1/responses`，被选中的上游或模型必须支持 Responses surface，许多 OpenAI 兼容网关只支持 Chat Completions 而不支持 Responses，因此兼容性要按 endpoint 判断而不是只看模型名。

## Provider Preset 清单

`internal/upstream/resolved.go` 的 `providerPresetRegistry` 当前有 25 个键；其中若干键的 spec 完全相同而互为别名，去重后为 20 个独立 provider。Monitor 通过 `GET /api/provider-presets` 暴露同一份矩阵。

| `provider_preset` | 协议族 | 默认 `routing_profile` | 允许的 `routing_profile` | 支持级别 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `alibaba` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `azure` | `openai_compatible` | 自动推断 | `azure_openai_v1`、`azure_openai_deployment` | verified | |
| `azure_openai` | `openai_compatible` | 自动推断 | 同上 | verified | `azure` 别名 |
| `baseten` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `cerebras` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `deepseek` | `openai_compatible` | `openai_default` | `openai_default` | compatible | 允许以 origin 作为 base URL |
| `fireworks` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `github` | `openai_compatible` | `openai_default` | `openai_default` | verified | `github_models` 别名 |
| `github_models` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `groq` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `hugging_face` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `moonshot` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `nvidia_nim` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `openai` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `openrouter` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `perplexity` | `openai_compatible` | `openai_default` | `openai_default` | compatible | |
| `together` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `vllm` | `openai_compatible` | `vllm_openai` | `vllm_openai` | verified | |
| `xai` | `openai_compatible` | `openai_default` | `openai_default` | verified | |
| `anthropic` | `anthropic_messages` | `anthropic_default` | `anthropic_default` | verified | |
| `google` | `google_genai` | `google_ai_studio` | `google_ai_studio` | verified | `google_genai` 别名 |
| `google_ai_studio` | `google_genai` | `google_ai_studio` | `google_ai_studio` | verified | `google_genai` 别名 |
| `google_genai` | `google_genai` | `google_ai_studio` | `google_ai_studio` | verified | |
| `gemini` | `google_genai` | `google_ai_studio` | `google_ai_studio` | verified | `google_genai` 别名 |
| `vertex` | `vertex_native` | 自动推断 | `vertex_express`、`vertex_project_location` | verified | |

`provider_preset` 是可选项；省略时由 host、base path 与 `protocol_family` 推断协议族和 routing profile。提供 preset 时，若与显式 `protocol_family` 冲突，或使用了不在允许列表内的 routing profile，会在配置解析或启动时失败。例如 `provider_preset: anthropic` 搭配 `protocol_family: google_genai`、`provider_preset: openrouter` 搭配 `routing_profile: azure_openai_v1`、以及未知 preset 都会报错。

host 推断的当前规则（按顺序匹配）：`api.deepseek.com` 且未指定 preset（或指定为 `openai`）时自动使用 `deepseek`；host 含 `anthropic.com` 或 `claude` 推断 `anthropic_messages`；`aiplatform.googleapis.com` 推断 `vertex_native`，其中该 host 默认 `vertex_express`，其他情况默认 `vertex_project_location`；host 含 `generativelanguage.googleapis.com` 或 `googleapis.com` 推断 `google_genai`；host 含 `azure.com`/`azure.net` 或 path 含 `/openai/` 时，有 `deployment` 或 path 含 `/deployments/` 用 `azure_openai_deployment`，否则用 `azure_openai_v1`；host 含 `vllm` 用 `vllm_openai`；其余默认 `openai_default`。

## Provider 配置来源

- YAML `upstream` / `upstreams`（含 `credentials`）只作为启动与首次 bootstrap 输入。
- 应用数据库（生产为 Postgres，本地/开发 fallback 为 SQLite）的 `channel_configs` / `channel_models` 是长期配置事实源。
- 首次数据库写入会记录 `app_settings` 键 `channels.initialized`。此后数据库接管路由配置，即使所有 channel 都被禁用或删除；`GET /api/settings/channels` 报告该标记，`DELETE /api/settings/channels` 清除它，仅在数据库仍无 channel 时重新打开 YAML bootstrap。
- 带显式 `credentials` 列表的 YAML 配置保持 YAML 管理，Monitor 对 channel/model/alias 的写入会以 409 拒绝。
- 所有管理写入（channels、models、aliases、provider setup 与 probe apply）都在一个 `store.ConfigurationTransaction` 中完成，持有进程级配置锁、上游写锁与一个 SQL 事务；运行时路由只在提交成功后发布。
- Responses 的本地执行模式没有配置开关，按模型可用；要退出本地翻译，可把数据库 `app_settings` 键 `routing.settings` 设为 `{"responses_strategy":"native_only"}`（不是 YAML 键）。路由与凭据细节见 [路由与凭据](./ROUTING_AND_CREDENTIALS.md)，Responses runtime 见 [Responses 运行时](./RESPONSES_RUNTIME.md)。

## 能力声明与 protocol_family 映射

- 能力字段为 `responses`、`chat_completions`、`tool_calling`、`embeddings`、`models`、`tokenize`，对应 YAML `upstream.capabilities`（数据库为 channel 的 capabilities JSON）。每个字段都是可选布尔，未声明表示未知。
- `api_type` 的全局合法值为 `chat_completions`、`responses`、`responses_native`、`messages`、`gemini_generate_content`；协议族另有约束：`anthropic_messages` 只接受 `messages`，`google_genai` 与 `vertex_native` 只接受 `gemini_generate_content`，`openai_compatible` 没有额外的 `api_type` 约束。`api_type` 缺失时按协议族取默认值：`anthropic_messages` → `messages`，`google_genai`、`vertex_native` → `gemini_generate_content`，其余 → `chat_completions`。
- `mode` 的合法值为 `proxy`、`record_only`、`server`、`responses_server`。
- 按模型覆盖：数据库 `channel_models.supports_responses` / `supports_chat_completions`（Monitor 可编辑，YAML 对应 `upstream.model_capabilities`）对该模型覆盖 channel 级 `api_type`/capabilities；没有声明覆盖的模型回退到 channel 级行为。这使一个 channel 能对部分模型走原生 surface，对另一些模型走其他 surface。
- endpoint 级判定：`/v1/chat/completions` 要求该模型的 Chat Completions 支持；`/v1/responses` 要求原生 Responses 支持或 Chat Completions 支持（后者走本地 Responses runtime）。
- `internal/upstream` 的 capability registry 被 probe endpoint、provider setup/apply 写入与 routing API surface 判断共用，各控制面不维护各自事实源；probe 未探测到的能力不会被自动补出。

## Provider Probe

`provider probe` 是手动诊断命令，用于检查配置中的 upstream endpoint 是否暴露常见 API surface 并给出保守建议：

```bash
llm-tracelab --config config.yaml provider probe --id openai-local --format json
```

探测的 endpoint 固定为：OpenAI 兼容 `GET /v1/models`、`POST /v1/chat/completions`、`POST /v1/responses`；Anthropic `POST /v1/messages`、`GET /v1/models`；Gemini `GET /v1beta/models`。鉴权按 endpoint 选择：OpenAI 风格用 `Authorization: Bearer`，Anthropic 用 `x-api-key` 加 `anthropic-version: 2023-06-01`，Gemini 用 `x-goog-api-key`。

- 只要返回 2xx 或 400、401、403、405、415、422、429 之一，就认为 endpoint 存在。
- 每个 endpoint 带权重：`POST /v1/responses`、`POST /v1/messages` 与 `GET /v1beta/models` 为 4，`POST /v1/chat/completions` 为 3，OpenAI 与 Anthropic 的 `GET /v1/models` 为 1。按协议族累计得分后取最高者作为建议，并汇总命中的 capability。
- 输出包含建议的 `protocol_family`、`api_type`、capabilities、confidence 与 warnings。confidence 按得分给出：≥5 为 0.9，≥4 为 0.8，≥3 为 0.7，≥1 为 0.45，否则 0。
- 当显式配置的 `api_type` 或 `protocol_family` 与探测建议不一致时，报告写入 warning，但不改写配置。

`provider probe-report` 是面向 YAML upstream 的只读批量报告；`provider probe-apply` 是面向 managed channel 的写入口，会打开 application store，对已有 channel 运行同类 probe 并只填补缺失字段：

```bash
llm-tracelab --config config.yaml provider probe-apply --id openai-local --format json
```

`provider probe-apply` 只填补空白的 `api_type`、`protocol_family` 和未设置的能力布尔；不写入 API key 或 header secret，也不覆盖显式 `api_type`、`protocol_family` 或显式 `false` 能力。省略 `--id` 时处理所有启用且有 `base_url` 的 channel。

默认启动不执行 provider probe，也不依赖网络。需要明确 opt-in 时配置：

```yaml
provider_probe:
  startup_fill: true
  timeout: 2s
```

开启后，serve 启动会对启用的 YAML upstream target 做一次 best-effort probe，并只在内存配置中填补缺失的 `api_type`、`protocol_family` 和未声明的能力布尔；不会写回 YAML。显式配置值不会被覆盖；probe 失败或建议不一致时只记录 warning/log，不阻断服务启动。

## 新增 preset 的原则

可以新增 preset 的条件：

- 上游在生态中常见。
- 能清晰映射到已有协议族。
- 不需要新的请求/响应语义。

只有当请求 schema、响应 schema、stream 事件、usage 或 replay 行为明显不同，才应新增协议族。新增 preset 时用 [开发](./DEVELOPMENT.md) 中的命令矩阵做校验；新增协议族还需补齐官方 schema 快照与流式/非流式 cassette fixture，并保证解析进 Observation IR 时不改变 replay 原始字节。

## 非目标与未实现

- Anthropic Messages、OpenAI Responses 与 Gemini GenerateContent 之间不做跨协议请求翻译。`pkg/llm.Adapter` 有内部通用形态的 marshal 方法，但代理转发路径不用它们做跨协议转换层。
- `/anthropic/messages` 不会转换成 OpenAI Chat Completions 或 Responses；`/responses` 不会转换成 Anthropic Messages。tool calls、streaming 事件、usage、cache 控制、reasoning 字段与 provider 特有错误保持 provider 特有。
- provider 特有 tools、reasoning、cache 控制、citations、safety block 与 streaming 事件生命周期没有完整语义等价。
- 唯一的 Responses 特例发生在同一协议族内部：没有匹配的原生 Responses 上游时，本地 Responses runtime 会把 `/v1/responses` 编排为一次内部 `/v1/chat/completions` 调用，而不是跨协议族翻译。录制、观测与审计细节见 [观测与审计](./OBSERVATION_AND_AUDIT.md)。
