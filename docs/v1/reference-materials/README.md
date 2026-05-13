# v1 协议参考材料

本目录保存 v1 协议深度解析设计所依赖的上游原始材料和抽取 schema，避免后续开发时重复调研。

## 来源时间

这些材料来自 2026-05-13 的调研快照。后续如果上游 API 发生变化，应新增带日期的新快照文件，不直接覆盖旧文件。

## 目录结构

```text
reference-materials/
  upstream/
    openai/
    anthropic/
    google-gemini/
```

## OpenAI

路径：[`upstream/openai/`](./upstream/openai/)

包含：

- [`openapi.documented-2026-05-13.yml`](./upstream/openai/openapi.documented-2026-05-13.yml)：OpenAI documented OpenAPI spec 快照。
- [`chat-completions-path-2026-05-13.json`](./upstream/openai/chat-completions-path-2026-05-13.json)：`/chat/completions` path item 抽取。
- [`chat-completions-core-schemas-2026-05-13.yml`](./upstream/openai/chat-completions-core-schemas-2026-05-13.yml)：Chat Completions 核心 schema 抽取。
- [`responses-path-2026-05-13.json`](./upstream/openai/responses-path-2026-05-13.json)：`/responses` path item 抽取。
- [`responses-core-schemas-2026-05-13.yml`](./upstream/openai/responses-core-schemas-2026-05-13.yml)：Responses 核心 schema 抽取。
- [`schema-index-2026-05-13.md`](./upstream/openai/schema-index-2026-05-13.md)：schema 快照索引与调研说明。

## Anthropic Claude

路径：[`upstream/anthropic/`](./upstream/anthropic/)

包含：

- [`messages-api-2026-05-13.md`](./upstream/anthropic/messages-api-2026-05-13.md)：Messages API 核心请求/响应材料。
- [`streaming-messages-2026-05-13.md`](./upstream/anthropic/streaming-messages-2026-05-13.md)：streaming Messages 事件材料。
- [`tool-use-overview-2026-05-13.md`](./upstream/anthropic/tool-use-overview-2026-05-13.md)：tool use 相关材料。
- [`schema-index-2026-05-13.md`](./upstream/anthropic/schema-index-2026-05-13.md)：schema 快照索引与调研说明。

## Google Gemini

路径：[`upstream/google-gemini/`](./upstream/google-gemini/)

包含：

- [`generative-language-discovery-v1beta-2026-05-13.json`](./upstream/google-gemini/generative-language-discovery-v1beta-2026-05-13.json)：Google Generative Language API discovery document 快照。
- [`generate-content-core-schemas-2026-05-13.json`](./upstream/google-gemini/generate-content-core-schemas-2026-05-13.json)：GenerateContent 相关核心 schema 抽取。
- [`schema-index-2026-05-13.md`](./upstream/google-gemini/schema-index-2026-05-13.md)：schema 快照索引与调研说明。

## 使用原则

- parser 实现优先以这些原始材料为依据，再结合实际 cassette fixture 做兼容处理。
- 不把上游 schema 直接等同为项目内部 IR；内部 IR 仍以 [`../observation-ir.md`](../observation-ir.md) 为准。
- 当 schema 与实际 provider 行为冲突时，保留冲突样本，并在 parser warning 或 fixture 注释中记录原因。
- OpenAI-compatible provider 只能声明“兼容某个行为子集”，不能默认等价于 OpenAI 官方 API。
