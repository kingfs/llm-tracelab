# 协议参考

本目录是 TraceLab 的协议参考入口。

它区分两类内容：

- TraceLab 当前实现事实：代理现在能路由、录制、回放、解析什么
- 上游协议材料：实现 parser 与协议族路由时使用的官方 API 规范或文档快照

TraceLab 当前是一个协议族感知的透传录制器，代理热路径不做协议族之间的请求翻译。

## 当前实现参考

- [已实现的协议](./implemented-protocols.md)：当前代码支持的协议族、endpoint、路由 profile 与 parser 覆盖范围。
- [协议差异](./protocol-differences.md)：OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Google Gemini GenerateContent 与 Vertex native GenerateContent 之间的实际差异。

## 上游快照

当前快照日期：2026-06-02。

上游材料存放在 [`upstream/`](./upstream/) 下。上游 API 变化时，**新增**一个带日期的快照，而不是覆盖旧文件。

```text
protocol-reference/
  upstream/
    openai/
    anthropic/
    google-gemini/
    google-vertex/
```

## 取材原则

- 优先使用官方上游规范或文档。
- 原始快照保持可人工定位，并带日期。
- 抽取出的 schema 子集与原始快照放在一起，便于实现时查阅。
- 不要把上游 schema 当作 TraceLab 的内部 IR。TraceLab 的语义 parser 输出始终是 Observation IR。
- OpenAI-compatible provider 只声明"兼容 OpenAI 风格行为的某个子集"，不自动等同于 OpenAI 官方 API。
- 本目录是上游协议快照的唯一事实源。

## 相关文档

- [文档入口](../README.md)
- [协议族与上游 Provider](../PROTOCOLS_AND_PROVIDERS.md)
- [语义解析、Observation IR 与审计](../OBSERVATION_AND_AUDIT.md)
