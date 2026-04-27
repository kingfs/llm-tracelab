# Proxy Usage Examples

These examples call the local llm-tracelab proxy. The proxy requires a personal token generated from the Monitor `Tokens` page or from the CLI:

```bash
go run ./cmd/server auth create-token -c config/config.yaml --username admin --name local-dev
```

Set the local proxy URL and token before running the examples:

```bash
export LLM_TRACELAB_URL=http://localhost:8080
export LLM_TRACELAB_TOKEN=llmtl_xxx
```

If your local `server.port` is not `8080`, update `LLM_TRACELAB_URL` to match the actual proxy port.

## List Models

```bash
curl -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  "${LLM_TRACELAB_URL}/v1/models" | jq
```

## Non-Stream Chat Completion

```bash
curl "${LLM_TRACELAB_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"1+1=? Just answer with a number."}],"max_completion_tokens":64}'
```

```bash
curl "${LLM_TRACELAB_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-r1","messages":[{"role":"user","content":"ping"}],"max_completion_tokens":128}'
```

## Stream Chat Completion

```bash
curl -N "${LLM_TRACELAB_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${LLM_TRACELAB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-max","messages":[{"role":"user","content":"讲一个20字笑话"}],"max_completion_tokens":128,"stream":true,"stream_options":{"include_usage":true}}'
```

For OpenAI-compatible chat completions, llm-tracelab can add `stream_options.include_usage` when it is missing so usage remains visible in recorded stream traces.

## OpenAI SDK

OpenAI-compatible SDKs usually send the SDK `api_key` as `Authorization: Bearer <api_key>`. Use the llm-tracelab personal token as the SDK API key and point `base_url` to the proxy:

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

## Provider Quirks

Some upstream providers have stricter message rules than the OpenAI baseline. For example, Zhipu-style chat requests should not contain only a `system` message; make sure the final message is a `user` message.
