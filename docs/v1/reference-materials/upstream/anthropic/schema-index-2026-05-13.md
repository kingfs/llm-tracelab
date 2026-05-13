---
id: upstream-anthropic-schema-index-2026-05-13
title: Anthropic Claude API 文档归档索引
type: source-card
topic: protocols/llm-api
status: stable
summary: 索引 2026-05-13 抓取的 Claude Messages API、streaming 与 tool use 官方 Markdown 文档。
updated_at: 2026-05-13
evidence_level: L2
tags: [anthropic, claude, upstream]
---

# Anthropic Claude API 文档归档索引

## 抓取信息

- 抓取日期: 2026-05-13
- 主来源: https://platform.claude.com/docs/en/api/messages.md
- 辅助来源:
  - https://platform.claude.com/docs/en/build-with-claude/streaming.md
  - https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview.md

## 归档文件

| 文件 | 内容 | 行数 |
| --- | --- | ---: |
| [`docs/sources/upstream/anthropic/messages-api-2026-05-13.md`](/docs/sources/upstream/anthropic/messages-api-2026-05-13.md) | Messages API reference，含请求、响应、batch、多语言 SDK 内容 | 37793 |
| [`docs/sources/upstream/anthropic/streaming-messages-2026-05-13.md`](/docs/sources/upstream/anthropic/streaming-messages-2026-05-13.md) | Streaming messages guide 与 SSE event flow | 1728 |
| [`docs/sources/upstream/anthropic/tool-use-overview-2026-05-13.md`](/docs/sources/upstream/anthropic/tool-use-overview-2026-05-13.md) | Tool use overview，区分 client tools 与 server tools | 160 |

## 关键定位

- `POST /v1/messages`: [`docs/sources/upstream/anthropic/messages-api-2026-05-13.md`](/docs/sources/upstream/anthropic/messages-api-2026-05-13.md)
- request body parameters: `max_tokens`、`messages`、`model`、`system`、`tools`、`tool_choice`、`stream`、`thinking` 等。
- response `Message`: `id`、`content[]`、`model`、`role`、`stop_reason`、`usage` 等。
- SSE event flow: `message_start` -> content block events -> `message_delta` -> `message_stop`。
