# Protocol Differences

The major LLM APIs overlap at the concept level: model, instructions, user input, tool definitions, generated content, tool calls, usage, and streaming.

They differ enough that TraceLab treats them as separate protocol families.

## High-Level Shape

| API surface | Request core | Response core | Streaming shape | Tool shape | Usage shape |
| --- | --- | --- | --- | --- | --- |
| OpenAI Chat Completions | `messages[]` with roles and optional `tools[]` | `choices[].message` | SSE chunks with `choices[].delta` | `tools[].function`, `tool_calls[]` | `usage.prompt_tokens`, `completion_tokens`, `total_tokens` |
| OpenAI Responses | `input`, `instructions`, `tools`, reasoning/text config | `output[]` typed items | SSE events named `response.*` and item/content deltas | typed output items such as function calls and tool call outputs | response `usage` with input/output token fields |
| Anthropic Messages | `system`, `messages[]`, `tools[]`, `max_tokens` | top-level assistant message with `content[]` blocks | SSE events such as message/content block lifecycle and deltas | content blocks such as `tool_use` and `tool_result` | `usage.input_tokens`, `output_tokens`, cache read/create fields |
| Google Gemini GenerateContent | `contents[]`, `systemInstruction`, `tools[]`, generation/safety config | `candidates[]` with `content.parts[]` | stream returns GenerateContentResponse chunks | function declarations, function calls/responses in parts | `usageMetadata` token fields |
| Vertex Native GenerateContent | similar Gemini schema with Vertex resource paths and auth | similar Gemini response schema | Vertex stream variants | Vertex/Google tool schemas | Vertex usage metadata |

## Why Recognition Is Not Conversion

TraceLab can recognize and parse Anthropic Messages, OpenAI Responses, OpenAI Chat Completions, and Gemini GenerateContent. Recognition means:

- classify the endpoint
- extract model and usage metadata
- record provider-specific stream events
- parse provider payloads into Observation IR
- preserve raw bytes for replay

Conversion would require more:

- transform request schemas
- transform tool declarations and tool call/result wiring
- map reasoning/thinking fields with provider-specific privacy rules
- map cache controls and cache accounting
- convert streaming event lifecycles without losing partial tool-call state
- preserve provider-specific error and safety semantics

That conversion is not part of the current proxy hot path.

## OpenAI Chat Completions Vs Responses

Chat Completions is message-list oriented:

- request: `messages[]`
- response: `choices[]`
- stream: `choices[].delta`

Responses is item/event oriented:

- request: `input` plus richer `tools`, `reasoning`, and output config
- response: `output[]` items with typed content and tool-call records
- stream: named lifecycle events and item/content deltas

Responses is better suited for agentic workflows with richer built-in tool surfaces. Chat Completions remains the dominant OpenAI-compatible baseline for many third-party gateways.

## Anthropic Messages Vs OpenAI-Compatible

Anthropic Messages separates system instructions from conversation messages and uses content blocks heavily:

- `system` is separate from `messages[]`
- user/assistant content can be an array of typed blocks
- tool calls and tool results are content blocks
- `max_tokens` is required by the Anthropic API surface
- prompt caching is represented by cache read/create token fields

OpenAI Chat Completions places system/developer instructions inside `messages[]`, uses `tools[].function`, and returns assistant tool calls in `choices[].message.tool_calls`.

OpenAI Responses uses a different typed item model again, so it is not a simple alias for either Chat Completions or Anthropic Messages.

## Gemini/Vertex Vs OpenAI/Anthropic

Gemini GenerateContent uses:

- `contents[]` instead of `messages[]`
- `parts[]` instead of OpenAI/Anthropic content blocks
- `systemInstruction`
- `generationConfig` and `safetySettings`
- function calls and function responses as parts
- `candidates[]` in the response

Vertex native adds resource path and auth differences around the same broad GenerateContent concept.

## Practical Routing Consequence

If Claude Code sends `/v1/messages`, the selected upstream must support Anthropic Messages semantics. A model named `glm-5.1` on an OpenAI-compatible upstream does not automatically become usable through `/v1/messages`.

If Codex sends `/v1/responses`, the selected upstream must support the OpenAI Responses surface. Many OpenAI-compatible gateways support Chat Completions but not Responses, so compatibility must be checked per endpoint, not only per model name.

## Implementation Guidance

When adding a new parser or protocol family:

- start from the official upstream snapshot in this directory
- add cassette fixtures for both non-streaming and streaming responses
- parse into Observation IR without changing raw replay bytes
- keep unknown provider fields in raw/provider-specific metadata
- avoid cross-protocol conversion unless it is an explicit feature with tests covering tool calls, streaming, usage, and errors
