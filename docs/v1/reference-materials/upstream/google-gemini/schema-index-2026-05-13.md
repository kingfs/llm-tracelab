---
id: upstream-google-gemini-schema-index-2026-05-13
title: Google Gemini discovery schema 摘录索引
type: source-card
topic: protocols/llm-api
status: stable
summary: 索引 2026-05-13 抓取的 Google Generative Language v1beta discovery document 与 GenerateContent 核心 schema 摘录。
updated_at: 2026-05-13
evidence_level: L2
tags: [google, gemini, upstream, discovery]
---

# Google Gemini discovery schema 摘录索引

## 抓取信息

- 抓取日期: 2026-05-13
- 来源 URL: https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta
- Discovery revision: `20260512`
- 完整归档: [`docs/sources/upstream/google-gemini/generative-language-discovery-v1beta-2026-05-13.json`](/docs/sources/upstream/google-gemini/generative-language-discovery-v1beta-2026-05-13.json)

## 摘录文件

| 文件 | 内容 | 行数 |
| --- | --- | ---: |
| [`docs/sources/upstream/google-gemini/generate-content-core-schemas-2026-05-13.json`](/docs/sources/upstream/google-gemini/generate-content-core-schemas-2026-05-13.json) | GenerateContent method、streamGenerateContent method、核心 request/response/content/tool/generation schema | 612 |

## 关键定位

- `generateContent`: `resources.models.methods.generateContent`
- `streamGenerateContent`: `resources.models.methods.streamGenerateContent`
- `GenerateContentRequest`: `schemas.GenerateContentRequest`
- `GenerateContentResponse`: `schemas.GenerateContentResponse`
- `Content` / `Part`: `schemas.Content` / `schemas.Part`
- `Tool` / `FunctionDeclaration` / `ToolConfig`: 对应 `schemas.*`
