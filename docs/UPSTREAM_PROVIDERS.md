# 上游 Provider 与协议族

TraceLab 不为每个 provider 写一套独立集成，而是把上游解析成：

- `protocol_family`
- `routing_profile`
- 鉴权、版本、header 和 URL 构造规则

协议 schema 和差异见 [协议参考](./protocol-reference/README.md)。

## 协议族

### `openai_compatible`

适用于 OpenAI 风格 API。

支持 routing profile：

- `openai_default`
- `azure_openai_v1`
- `azure_openai_deployment`
- `vllm_openai`

典型 endpoint：

- `/v1/chat/completions`
- `/v1/responses`
- `/v1/embeddings`
- `/v1/models`

配置注意：

- `upstream.base_url` 应包含上游 API prefix，例如 `/v1`、`/api/v1`、`/openai`、`/openai/v1`。
- client 仍请求 TraceLab 的 `/v1/...`。
- TraceLab 转发时按 routing profile 构造上游 URL。

### `anthropic_messages`

适用于 Anthropic Claude Messages API。

支持 routing profile：

- `anthropic_default`

典型 endpoint：

- `/v1/messages`
- `/v1/models` 用于 connectivity/model discovery

注意：

- 鉴权使用 `x-api-key`。
- 可自动补 `anthropic-version`。
- 不会自动转为 OpenAI-compatible 请求。

### `google_genai`

适用于 Google AI Studio / Gemini API。

支持 routing profile：

- `google_ai_studio`

典型 endpoint：

- `/v1beta/models/{model}:generateContent`
- `/v1beta/models/{model}:streamGenerateContent`
- `/v1beta/models`

### `vertex_native`

适用于 Vertex AI native Gemini API。

支持 routing profile：

- `vertex_express`
- `vertex_project_location`

当前验证 endpoint：

- `/v1/publishers/{publisher}/models/{model}:generateContent`
- `/v1/publishers/{publisher}/models/{model}:streamGenerateContent`
- `/v1/projects/{project}/locations/{location}/publishers/{publisher}/models/{model}:generateContent`
- `/v1/projects/{project}/locations/{location}/publishers/{publisher}/models/{model}:streamGenerateContent`

## Provider Preset

当前可用 preset：

| preset | 协议族 | routing profile | 说明 |
| --- | --- | --- | --- |
| `openai` | `openai_compatible` | `openai_default` | OpenAI 风格默认 |
| `openrouter` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `fireworks` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `together` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `deepseek` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `groq` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `xai` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `moonshot` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `cerebras` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `baseten` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `perplexity` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `alibaba` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `hugging_face` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `nvidia_nim` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `github_models` | `openai_compatible` | `openai_default` | GitHub Models |
| `azure` | `openai_compatible` | 自动推断 | Azure OpenAI |
| `azure_openai` | `openai_compatible` | 自动推断 | `azure` 别名 |
| `vllm` | `openai_compatible` | `vllm_openai` | 自托管 vLLM |
| `anthropic` | `anthropic_messages` | `anthropic_default` | Claude Messages |
| `google_genai` | `google_genai` | `google_ai_studio` | Gemini API |
| `google` | `google_genai` | `google_ai_studio` | `google_genai` 别名 |
| `gemini` | `google_genai` | `google_ai_studio` | `google_genai` 别名 |
| `vertex` | `vertex_native` | 自动推断 | Vertex Gemini |

非法组合会在启动或配置解析时失败。例如：

- `provider_preset: anthropic` 搭配 `protocol_family: google_genai`。
- `provider_preset: openrouter` 搭配 `routing_profile: azure_openai_v1`。
- 未知 `provider_preset`。

## 配置来源

当前支持两类输入：

- YAML `upstream` / `upstreams`：兼容启动和首次 bootstrap。
- SQLite `channel_configs` / `channel_models`：长期配置事实源。

当数据库已有 channel 配置时，router 优先使用数据库配置。

## 新增 preset 的原则

可以新增 preset 的条件：

- 上游在生态中常见。
- 能清晰映射到已有协议族。
- 不需要新的请求/响应语义。

只有当请求 schema、响应 schema、stream 事件、usage 或 replay 行为明显不同，才应新增协议族。
