# Implemented Protocols

This document records the protocol behavior implemented in the current codebase. It is descriptive, not aspirational.

## Implementation Shape

TraceLab currently does three protocol-aware things:

1. classify request paths and upstreams into provider/protocol semantics in `pkg/llm`
2. resolve configured upstreams into protocol families, routing profiles, auth headers, and URL rewrite behavior in `internal/upstream`
3. pass through raw HTTP bytes while recording cassettes, extracting usage/timeline events, and parsing traces into Observation IR

The proxy hot path does not translate one provider request schema into another. For example, an Anthropic Messages request is not converted into an OpenAI Chat Completions or OpenAI Responses request before forwarding.

## Protocol Families

| Protocol family | Provider labels | Routing profiles | Current endpoint coverage | Parser coverage |
| --- | --- | --- | --- | --- |
| `openai_compatible` | `openai_compatible`, `azure_openai`, `vllm` | `openai_default`, `azure_openai_v1`, `azure_openai_deployment`, `vllm_openai` | `/v1/chat/completions`, `/v1/responses`, `/v1/embeddings`, `/v1/models`, vLLM `/tokenize`, `/detokenize` | Chat Completions, Responses, Models, Tokenization |
| `anthropic_messages` | `anthropic` | `anthropic_default` | `/v1/messages`, `/v1/models` for connectivity/model discovery | Messages |
| `google_genai` | `google_genai` | `google_ai_studio` | `/v1beta/models/{model}:generateContent`, `/v1beta/models/{model}:streamGenerateContent`, `/v1beta/models` | GenerateContent, streamGenerateContent |
| `vertex_native` | `vertex_native` | `vertex_express`, `vertex_project_location` | Vertex Gemini `generateContent`, `streamGenerateContent`, model list paths | GenerateContent, streamGenerateContent |

## OpenAI-Compatible

OpenAI-compatible routing is used for providers whose API surface follows OpenAI-style request and response semantics.

Current normalized endpoints:

- `/v1/chat/completions`
- `/v1/responses`
- `/v1/embeddings`
- `/v1/models`
- `/tokenize`
- `/detokenize`

Important details:

- client `/responses` is accepted as a TraceLab entrypoint alias and is normalized to `/v1/responses`
- for the `vllm_openai` routing profile, client `/tokenize`, `/v1/tokenize`, `/detokenize`, and `/v1/detokenize` are routed to the vLLM root tokenization endpoints
- `upstream.base_url` should include the provider's API prefix such as `/v1`, `/api/v1`, `/openai`, or `/openai/v1`.
- The proxy records and parses Chat Completions and Responses. Embeddings are routed/recorded but are not a deep Observation IR parser target today.
- Responses and Chat Completions are different OpenAI surfaces. Codex traffic commonly uses `/v1/responses`.

## Anthropic Messages

Anthropic Messages routing is used for Claude-style `/v1/messages` traffic.

Current behavior:

- client `/anthropic/messages` and `/anthropic/v1/messages` are accepted as TraceLab entrypoint aliases and normalized to `/v1/messages`
- request and response bodies are passed through unchanged
- auth headers are rewritten to Anthropic-style `x-api-key`
- `anthropic-version` is injected from upstream config when missing
- request/response bodies and streaming events are parsed into Observation IR

This means Claude Code works when it is pointed at a TraceLab base URL that does not duplicate `/v1`, and when a compatible Anthropic Messages upstream is configured or the selected gateway actually supports that endpoint.

## Google Gemini And Vertex Native

Google Gemini and Vertex native are separate protocol families because their endpoint shape, auth model, and request/response schema differ from OpenAI and Anthropic.

Current behavior:

- Google AI Studio uses `/v1beta/models/{model}:generateContent` and stream variants.
- Vertex native supports express and project/location routing profiles.
- Both are parsed through the GenerateContent semantic adapter path.

## Non-Goals In Current Code

These are not implemented today:

- Anthropic Messages to OpenAI-compatible request translation
- OpenAI Responses to Anthropic Messages request translation
- Gemini GenerateContent to OpenAI-compatible request translation
- full semantic equivalence for provider-specific tools, reasoning, cache controls, citations, safety blocks, or streaming event lifecycles

`pkg/llm.Adapter` has marshal methods for common internal shapes, but the proxy forwarding path does not use them as a cross-protocol conversion layer.
