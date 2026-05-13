---
id: upstream-openai-schema-index-2026-05-13
title: OpenAI documented OpenAPI schema 摘录索引
type: source-card
topic: protocols/llm-api
status: stable
summary: 索引 2026-05-13 抓取的 OpenAI documented OpenAPI spec 及 Chat/Responses 相关摘录文件。
updated_at: 2026-05-13
evidence_level: L2
tags: [openai, openapi, schema, upstream]
---

# OpenAI documented OpenAPI schema 摘录索引

## 抓取信息

- 抓取日期: 2026-05-13
- 来源 URL: https://app.stainless.com/api/spec/documented/openai/openapi.documented.yml
- OpenAPI 版本: `3.1.0`
- OpenAI API spec 版本: `2.3.0`
- 完整归档: [`docs/sources/upstream/openai/openapi.documented-2026-05-13.yml`](/docs/sources/upstream/openai/openapi.documented-2026-05-13.yml)

## 摘录文件

| 文件 | 内容 | 行数 |
| --- | --- | ---: |
| [`docs/sources/upstream/openai/chat-completions-path-2026-05-13.json`](/docs/sources/upstream/openai/chat-completions-path-2026-05-13.json) | `/chat/completions` endpoint path object | 201 |
| [`docs/sources/upstream/openai/chat-completions-core-schemas-2026-05-13.yml`](/docs/sources/upstream/openai/chat-completions-core-schemas-2026-05-13.yml) | Chat Completions 请求、响应、stream、message、tool schema 摘录 | 688 |
| [`docs/sources/upstream/openai/responses-path-2026-05-13.json`](/docs/sources/upstream/openai/responses-path-2026-05-13.json) | `/responses` endpoint path object | 154 |
| [`docs/sources/upstream/openai/responses-core-schemas-2026-05-13.yml`](/docs/sources/upstream/openai/responses-core-schemas-2026-05-13.yml) | Responses 请求、响应、input item、output item、stream、tool schema 摘录 | 506 |

## 关键定位

- `/chat/completions`: 完整 spec 中 path 位于 `paths["/chat/completions"]`。
- `/responses`: 完整 spec 中 path 位于 `paths["/responses"]`。
- `CreateChatCompletionRequest`: 完整 spec 中位于 `components.schemas.CreateChatCompletionRequest`。
- `CreateResponse`: 完整 spec 中位于 `components.schemas.CreateResponse`。
- `Response`: 完整 spec 中位于 `components.schemas.Response`。

## 备注

- Chat Completions schema 中存在大整数边界值，例如 `seed` 的最大/最小值；为避免 JSON 转换损失，核心 schema 摘录保留为 YAML。
- path object 已可安全转为 JSON，便于后续脚本消费。
