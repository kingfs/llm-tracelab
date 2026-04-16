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

### `google_genai`

Used for Google Gemini / Google GenAI-native content-generation APIs.

Supported routing profiles:

- `google_ai_studio`

Typical endpoints:

- `/v1beta/models/{model}:generateContent`
- `/v1beta/models/{model}:streamGenerateContent`
- `/v1beta/models`

## Support Levels

Presets are classified as:

- `verified`: explicitly covered by behavior tests or cassette-level regression tests
- `compatible`: expected to work because they map cleanly to an existing family, but have lighter direct verification
- `planned`: not yet a preset or not yet implemented

## Current Preset Matrix

These presets currently resolve without requiring extra code changes:

| Provider preset | Support | Protocol family | Routing profile | Notes |
| --- | --- | --- | --- | --- |
| `openai` | `verified` | `openai_compatible` | `openai_default` | default OpenAI-style routing |
| `openrouter` | `verified` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `fireworks` | `verified` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `together` | `verified` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `deepseek` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `groq` | `verified` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `xai` | `verified` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `moonshot` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `cerebras` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `baseten` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `perplexity` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `alibaba` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `hugging_face` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `nvidia_nim` | `compatible` | `openai_compatible` | `openai_default` | OpenAI-compatible gateway |
| `github_models` | `verified` | `openai_compatible` | `openai_default` | GitHub Models OpenAI-compatible surface |
| `azure` | `verified` | `openai_compatible` | inferred | chooses `azure_openai_v1` or `azure_openai_deployment` |
| `azure_openai` | `verified` | `openai_compatible` | inferred | alias of `azure` |
| `vllm` | `verified` | `openai_compatible` | `vllm_openai` | self-hosted OpenAI-compatible server |
| `anthropic` | `verified` | `anthropic_messages` | `anthropic_default` | Claude Messages API |
| `google_genai` | `verified` | `google_genai` | `google_ai_studio` | Google Gemini API |
| `google` | `verified` | `google_genai` | `google_ai_studio` | alias of `google_genai` |
| `gemini` | `verified` | `google_genai` | `google_ai_studio` | alias of `google_genai` |

Invalid combinations now fail fast at startup. For example:

- `provider_preset: anthropic` with `protocol_family: google_genai`
- `provider_preset: openrouter` with `routing_profile: azure_openai_v1`
- unknown `provider_preset` values

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

## Next Candidate

### `vertex_native`

The current recommendation is to add `vertex_native` as the fourth protocol family, instead of folding Vertex into `google_genai`.

Reason:

- Vertex-native differs enough in routing and auth semantics to justify isolation
- it is a stronger architecture test than adding more OpenAI-compatible aliases
- a minimal first cut can still reuse much of the existing Gemini body parsing logic

Planning note:

- see [VERTEX_NATIVE_PLAN.md](./VERTEX_NATIVE_PLAN.md) for the proposed family boundary, routing profiles, and implementation order
