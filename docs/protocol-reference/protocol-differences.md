# 协议差异

主流 LLM API 在概念层面高度重叠：模型、指令、用户输入、工具定义、生成内容、工具调用、usage、流式。

但它们差异足够大，因此 TraceLab 把它们当作彼此独立的协议族处理。

## 总体形态

| 接口面 | 请求核心 | 响应核心 | 流式形态 | 工具形态 | Usage 形态 |
| --- | --- | --- | --- | --- | --- |
| OpenAI Chat Completions | 带 role 的 `messages[]`，可选 `tools[]` | `choices[].message` | `choices[].delta` 的 SSE chunk | `tools[].function`、`tool_calls[]` | `usage.prompt_tokens`、`completion_tokens`、`total_tokens` |
| OpenAI Responses | `input`、`instructions`、`tools`、reasoning／text 配置 | `output[]` 类型化 item | 名为 `response.*` 的 SSE 事件与 item／content delta | 类型化 output item，如 function call 与 tool call output | 响应级 `usage`，含 input／output token 字段 |
| Anthropic Messages | `system`、`messages[]`、`tools[]`、`max_tokens` | 顶层 assistant message，含 `content[]` block | message／content block 生命周期与 delta 类 SSE 事件 | `tool_use`、`tool_result` 等内容 block | `usage.input_tokens`、`output_tokens`、cache read／create 字段 |
| Google Gemini GenerateContent | `contents[]`、`systemInstruction`、`tools[]`、生成与安全配置 | `candidates[]`，含 `content.parts[]` | 流式返回 GenerateContentResponse chunk | parts 中的 function declaration 与 function call／response | `usageMetadata` 的 token 字段 |
| Vertex Native GenerateContent | 与 Gemini 相近的 schema，但使用 Vertex 的资源路径与认证 | 与 Gemini 相近的响应 schema | Vertex 的流式变体 | Vertex／Google 工具 schema | Vertex usage metadata |

## 为什么是"识别"而不是"转换"

TraceLab 能够识别并解析 Anthropic Messages、OpenAI Responses、OpenAI Chat Completions 与 Gemini GenerateContent。识别指：

- 归类 endpoint
- 抽取模型与 usage 元数据
- 记录 provider 专有的流式事件
- 把 provider 载荷解析为 Observation IR
- 保留原始字节用于回放

转换则需要更多工作：

- 改写请求 schema
- 改写工具声明以及工具调用／结果的接线
- 在 provider 专有隐私规则下映射 reasoning／thinking 字段
- 映射 cache 控制与 cache 计费
- 在不丢失部分工具调用状态的前提下转换流式事件生命周期
- 保留 provider 专有的错误与安全语义

这些转换不属于当前代理热路径。

## OpenAI Chat Completions 与 Responses 的差异

Chat Completions 面向消息列表：

- 请求：`messages[]`
- 响应：`choices[]`
- 流式：`choices[].delta`

Responses 面向 item／event：

- 请求：`input`，外加更丰富的 `tools`、`reasoning` 与输出配置
- 响应：`output[]` item，带类型化 content 与工具调用记录
- 流式：具名生命周期事件与 item／content delta

Responses 更适合带丰富内置工具面的 agent 工作流。Chat Completions 仍是许多第三方网关最主流的 OpenAI-compatible 基线。

## Anthropic Messages 与 OpenAI-Compatible 的差异

Anthropic Messages 把系统指令与对话消息分离，并大量使用 content block：

- `system` 与 `messages[]` 分离
- user／assistant 的 content 可以是类型化 block 数组
- 工具调用与工具结果是 content block
- Anthropic 接口要求 `max_tokens` 必填
- prompt caching 由 cache read／create token 字段表示

OpenAI Chat Completions 把 system／developer 指令放进 `messages[]`，使用 `tools[].function`，并在 `choices[].message.tool_calls` 中返回 assistant 工具调用。

OpenAI Responses 又是另一套类型化 item 模型，因此它既不是 Chat Completions 的别名，也不是 Anthropic Messages 的别名。

## Gemini／Vertex 与 OpenAI／Anthropic 的差异

Gemini GenerateContent 使用：

- `contents[]` 而非 `messages[]`
- `parts[]` 而非 OpenAI／Anthropic 的 content block
- `systemInstruction`
- `generationConfig` 与 `safetySettings`
- parts 形式的 function call 与 function response
- 响应中的 `candidates[]`

Vertex native 在同一个宽泛的 GenerateContent 概念之上，增加了资源路径与认证差异。

## 对路由的实际影响

如果 Claude Code 发送 `/v1/messages`，被选中的上游必须支持 Anthropic Messages 语义。挂在 OpenAI-compatible 上游上的 `glm-5.1` 并不会因为模型名存在就自动能通过 `/v1/messages` 使用。

如果 Codex 发送 `/v1/responses`，被选中的上游必须支持 OpenAI Responses 接口面。许多 OpenAI-compatible 网关支持 Chat Completions 但不支持 Responses，因此兼容性必须按 endpoint 逐个核对，而不是只看模型名。

## 实现指引

新增 parser 或协议族时：

- 以本目录中的官方上游快照为起点
- 为非流式与流式响应都补 cassette fixture
- 解析进 Observation IR，但不改变原始回放字节
- 未知的 provider 字段保留在 raw／provider 专有元数据中
- 除非是被明确立项、且有针对工具调用、流式、usage、错误写测试的特性，否则不要做跨协议转换
