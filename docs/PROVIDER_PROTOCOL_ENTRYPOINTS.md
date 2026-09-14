# Provider Protocol Entrypoints

This document describes the product-facing provider model and client URL entrypoints for TraceLab.

## Terminology

TraceLab uses **Provider** in user-facing Monitor UI and docs for an upstream service configuration. A provider may be an official model vendor, a company gateway, or a local/self-hosted proxy.

The existing code and SQLite schema still use `channel_*` names internally for compatibility:

- `channel_configs`
- `channel_models`
- `/api/channels`

Those names are implementation details. New user-facing docs and UI should prefer **Provider** unless discussing storage or migration internals.

## Protocol Entrypoints

TraceLab exposes protocol-aware client entrypoints. These entrypoints select the client API shape; they do not translate one protocol family into another.

| Entrypoint | Canonical endpoint | Intended client | Required provider capability |
| --- | --- | --- | --- |
| `/v1/chat/completions` | `/v1/chat/completions` | OpenAI-compatible SDKs and older integrations | `openai_compatible` Chat Completions |
| `/v1/responses` or `/responses` | `/v1/responses` | Codex and OpenAI Responses clients | `openai_compatible` Responses |
| `/anthropic/messages`, `/anthropic/v1/messages`, or `/v1/messages` | `/v1/messages` | Claude Code and Anthropic SDKs | `anthropic_messages` |

`/v1/models` remains the aggregated OpenAI-compatible model list endpoint.

## Provider Configuration

When an upstream gateway exposes multiple protocol families, configure one TraceLab provider per upstream protocol entrypoint. For example:

- OpenAI-compatible provider
  - `base_url: https://ai-api-gateway.example.com/api/openai`
  - `protocol_family: openai_compatible`
  - supports `/v1/chat/completions`, `/v1/responses`, and `/v1/models` according to the gateway's actual capability
- Anthropic provider
  - `base_url: https://ai-api-gateway.example.com/api/anthropic`
  - `protocol_family: anthropic_messages`
  - supports `/v1/messages`

This keeps routing explicit: Anthropic requests only select Anthropic-capable providers, and OpenAI-compatible requests only select OpenAI-compatible providers.

## Non-Goal

These entrypoints are not cross-protocol conversion:

- `/anthropic/messages` is not converted into OpenAI Chat Completions or Responses.
- `/responses` is not converted into Anthropic Messages.
- Tool calls, streaming events, usage, cache controls, reasoning fields, and provider-specific errors remain provider-specific.

TraceLab remains a protocol-aware pass-through recorder and router.

## Implementation Status

以下各项均已落地（对应 `internal/proxy/entrypoints.go` 与 Monitor UI）：

1. 客户端入口前缀在 routing、recording、forwarding 之前归一化。
   - `/responses` -> `/v1/responses`
   - `/anthropic/messages` -> `/v1/messages`
   - `/anthropic/v1/messages` -> `/v1/messages`
   - `/anthropic/messages/count_tokens` -> `/v1/messages/count_tokens`
2. 原始请求 body 保持原样。
3. `.http` 文件中的 record/replay 协议路径保持规范化，parser 与 replay 因此稳定。
4. Monitor UI 标签已从 Channels 改名为 Providers，同时保留 `/api/channels` 兼容（旧路由 `/channels` 会重定向到 `/providers`）。
5. 已提供应用内 Connect 页面，列出 Chat Completions、Responses 和 Anthropic Messages 的 base URL 与 curl 示例。
