# 代理使用示例

这些示例调用本地 `llm-tracelab` 代理。

代理 API 需要个人 token。可以在 Monitor 的 `Tokens` 页面创建，也可以用 CLI 创建。
`config/config.yaml` 使用 Postgres 且 `database.dsn` 为空，运行前需导出
`LLM_TRACELAB_DATABASE_DSN`；纯本地运行可改用 `config/examples/local-sqlite.yaml`：

```bash
export LLM_TRACELAB_DATABASE_DSN='postgres://user:pass@host:5432/llm_tracelab?sslmode=disable'
go run ./cmd/server auth create-token -c config/config.yaml --username admin --name local-dev
```

设置本地地址和 token：

```bash
export LLM_TRACELAB_URL=http://localhost:8080
export LLM_TRACELAB_TOKEN=llmtl_xxx
```

如果 `server.port` 不是 `8080`，请调整 `LLM_TRACELAB_URL`。

## 查询模型

```bash
curl -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  "${LLM_TRACELAB_URL}/v1/models" | jq
```

## OpenAI-Compatible Chat Completions

非流式：

```bash
curl "${LLM_TRACELAB_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"1+1=? Just answer with a number."}],"max_completion_tokens":64}'
```

流式：

```bash
curl -N "${LLM_TRACELAB_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"讲一个20字笑话"}],"max_completion_tokens":128,"stream":true,"stream_options":{"include_usage":true}}'
```

对于 OpenAI-compatible Chat Completions，TraceLab 可以在缺失时补充 `stream_options.include_usage=true`，以便流式 trace 也记录 usage。

## OpenAI SDK

OpenAI-compatible SDK 通常把 `api_key` 放到 `Authorization: Bearer <api_key>`。

使用 TraceLab 时，把个人 token 作为 SDK API key，并把 `base_url` 指向代理：

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url=os.getenv("LLM_TRACELAB_BASE_URL", "http://localhost:8080/v1"),
    api_key=os.environ["LLM_TRACELAB_TOKEN"],
)

resp = client.chat.completions.create(
    model="qwen3-max",
    messages=[{"role": "user", "content": "ping"}],
)
print(resp.choices[0].message.content)
```

## Claude Code / Anthropic Messages

Claude Code 走 Anthropic Messages 协议。

配置 TraceLab 时，base URL 应指向代理根地址，不要重复追加 `/v1`：

```text
http://localhost:8080
```

Claude Code 会自己请求 `/v1/messages`。

注意：TraceLab 当前不会把 Anthropic Messages 请求转换成 OpenAI-compatible 请求。`/v1/messages` 需要路由到支持 Anthropic Messages 的上游或兼容网关。

## Provider 差异

不同上游即使标称 OpenAI-compatible，也可能只支持部分 endpoint 或有更严格消息规则。

常见排查顺序：

1. 确认 client base URL 是否重复 `/v1`。
2. 确认请求 endpoint 是否被目标上游支持。
3. 在 Monitor 的 `Routing` / `Events` / trace detail 中查看路由和 provider 错误。
4. 使用 [协议参考](./protocol-reference/README.md) 判断是不是协议族不匹配。
