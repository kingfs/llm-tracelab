# Upstream Providers

## Goal

`llm-tracelab` does not try to model every upstream as a unique integration.
Instead, it resolves each upstream into:

- a `protocol_family`
- a `routing_profile`
- a small set of auth/version/header rules

This keeps provider growth additive instead of turning the proxy into a large tree of special cases.

## Current Families

### `openai_compatible`

Used for providers whose request and response semantics follow the OpenAI-style API surface.

Supported routing profiles:

- `openai_default`
- `azure_openai_v1`
- `azure_openai_deployment`
- `vllm_openai`

Typical endpoints:

- `/v1/chat/completions`
- `/v1/responses`
- `/v1/embeddings`
- `/v1/models`

### `anthropic_messages`

Used for Anthropic Claude Messages-style APIs.

Supported routing profiles:

- `anthropic_default`

Typical endpoint:

- `/v1/messages`

## Current Preset Matrix

These presets currently resolve without requiring extra code changes:

| Provider preset | Protocol family | Routing profile | Notes |
| --- | --- | --- | --- |
| `openai` | `openai_compatible` | `openai_default` | default OpenAI-style routing |
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
| `github_models` | `openai_compatible` | `openai_default` | GitHub Models OpenAI-compatible surface |
| `azure` | `openai_compatible` | inferred | chooses `azure_openai_v1` or `azure_openai_deployment` |
| `azure_openai` | `openai_compatible` | inferred | alias of `azure` |
| `vllm` | `openai_compatible` | `vllm_openai` | self-hosted OpenAI-compatible server |
| `anthropic` | `anthropic_messages` | `anthropic_default` | Claude Messages API |

## Selection Rules

Resolution order is:

1. explicit config fields such as `protocol_family` and `routing_profile`
2. `provider_preset`
3. inference from `base_url`

This means presets are convenience defaults, not hard locks.

## When To Add A New Preset

Add a new preset when:

- the upstream is already well-known in the ecosystem
- it cleanly maps to an existing protocol family
- it does not require new request/response semantics

Do not add a new protocol family unless the payload semantics or stream/event model are genuinely different.

## When To Add A New Protocol Family

Add one only when the upstream differs materially in:

- request schema
- response schema
- streaming event structure
- usage extraction rules
- replay-critical behavior

Examples that may justify future families:

- Google GenAI-native APIs
- Bedrock or Vertex APIs that are not used through an OpenAI-compatible surface
- realtime or session-based APIs
