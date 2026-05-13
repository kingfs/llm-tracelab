# v1 协议解析设计

## 目标

v1 协议解析的目标不是做 provider 互转，而是深度识别每种协议真实传输了什么、模型调用了什么、返回了什么。

解析结果应进入 [`observation-ir.md`](./observation-ir.md) 定义的 Observation IR。

## 通用要求

每个 parser 必须：

- 从 raw request/response body 解析。
- 支持非 streaming 和 streaming。
- 保留 provider 原生类型。
- 保留 raw JSON path。
- 保留 unknown fields。
- 输出 parse warnings。
- 具备 fixture 测试。
- 不影响 replay。

## OpenAI Chat Completions

### 请求

关注字段：

- `model`
- `messages[]`
- `messages[].role`
- `messages[].content`
- `messages[].tool_calls`
- `messages[].tool_call_id`
- `tools[]`
- `tool_choice`
- `response_format`
- `stream`
- `stream_options`
- `temperature`
- `top_p`
- `max_tokens`
- `max_completion_tokens`

### 响应

关注字段：

- `choices[]`
- `choices[].message`
- `choices[].message.content`
- `choices[].message.tool_calls`
- `choices[].finish_reason`
- `usage`
- `system_fingerprint`

### Streaming

关注字段：

- `choices[].delta.content`
- `choices[].delta.reasoning_content`
- `choices[].delta.tool_calls`
- `choices[].finish_reason`
- 最终 usage chunk。

### 输出节点

- message -> `message`
- text content -> `text`
- tool_calls -> `tool_call`
- tool role message -> `tool_result`
- reasoning_content -> `reasoning`
- finish_reason/content_filter -> `safety` 或 `refusal`

## OpenAI Responses

### 请求

关注字段：

- `model`
- `input`
- `instructions`
- `previous_response_id`
- `conversation`
- `tools`
- `tool_choice`
- `reasoning`
- `text`
- `include`
- `stream`
- `truncation`
- `max_output_tokens`

### 响应

关注字段：

- `id`
- `status`
- `output[]`
- `output[].type`
- `output[].content[]`
- `output[].arguments`
- `output[].call_id`
- `output[].output`
- `error`
- `incomplete_details`
- `usage`

### 重要 output item

第一版至少支持：

- `message`
- `reasoning`
- `function_call`
- `function_call_output`
- `web_search_call`
- `file_search_call`
- `computer_call`
- `code_interpreter_call`
- `mcp_call`
- `custom_tool_call`
- `local_shell_call`
- `apply_patch`

未知 item 也必须作为 `unknown` node 保留。

### Streaming

关注事件：

- `response.created`
- `response.in_progress`
- `response.completed`
- `response.failed`
- `response.incomplete`
- `response.output_item.added`
- `response.output_item.done`
- `response.output_text.delta`
- `response.output_text.done`
- `response.reasoning_text.delta`
- `response.reasoning_summary_text.delta`
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- MCP/tool/code/file/web search 相关事件。

### 输出节点

- `output[].type=message` -> `message` / `text`
- `output[].type=reasoning` -> `reasoning`
- `*_call` -> `tool_call` 或 `server_tool_call`
- `*_output` -> `tool_result`
- `refusal` -> `refusal`

## Anthropic Claude Messages

### 请求

关注字段：

- `model`
- `max_tokens`
- `system`
- `messages[]`
- `messages[].role`
- `messages[].content`
- `tools[]`
- `tool_choice`
- `stream`
- `thinking`
- `stop_sequences`
- `temperature`
- `top_p`
- `top_k`
- `metadata`

注意：

- `system` 是顶层字段，不是 message role。
- `messages[].content` 可以是 string，也可以是 content block array。

### 请求 content block

至少支持：

- `text`
- `image`
- `document`
- `thinking`
- `redacted_thinking`
- `tool_use`
- `tool_result`
- `server_tool_use`
- `web_search_tool_result`
- `web_fetch_tool_result`
- `code_execution_tool_result`
- `bash_code_execution_tool_result`
- `text_editor_code_execution_tool_result`
- `tool_search_tool_result`
- `container_upload`

### 响应

关注字段：

- `id`
- `type`
- `role`
- `model`
- `content[]`
- `stop_reason`
- `stop_sequence`
- `usage`
- `container`

### Streaming

关注事件：

- `message_start`
- `content_block_start`
- `content_block_delta`
- `content_block_stop`
- `message_delta`
- `message_stop`
- `ping`
- `error`

delta 类型：

- `text_delta`
- `input_json_delta`
- `thinking_delta`
- `signature_delta`

### 输出节点

- `text` -> `text`
- `thinking` -> `reasoning`
- `redacted_thinking` -> `reasoning` with redacted metadata
- `tool_use` -> `tool_call`
- `tool_result` -> `tool_result`
- server tool blocks -> `server_tool_call` / `server_tool_result`

## Google Gemini GenerateContent

### 请求

关注字段：

- `model`
- `systemInstruction`
- `contents[]`
- `contents[].role`
- `contents[].parts[]`
- `tools[]`
- `toolConfig`
- `generationConfig`
- `safetySettings`
- `cachedContent`
- `serviceTier`
- `store`

### Part 类型

官方 discovery 中 `Part` 可包含：

- `text`
- `inlineData`
- `fileData`
- `functionCall`
- `functionResponse`
- `executableCode`
- `codeExecutionResult`
- `toolCall`
- `toolResponse`
- `videoMetadata`
- `thought`
- `thoughtSignature`
- `partMetadata`

第一版 parser 不能只支持 `text`。

### 响应

关注字段：

- `candidates[]`
- `candidates[].content`
- `candidates[].content.parts[]`
- `candidates[].finishReason`
- `candidates[].safetyRatings`
- `promptFeedback`
- `usageMetadata`
- `modelVersion`
- `responseId`

### Streaming

关注：

- `streamGenerateContent` 返回的 chunk。
- 每个 chunk 中的 `candidates[].content.parts[]`。
- 增量 text。
- functionCall / functionResponse。
- safetyRatings。
- promptFeedback。

### 输出节点

- `Part.text` -> `text`
- `Part.functionCall` -> `tool_call`
- `Part.functionResponse` -> `tool_result`
- `Part.executableCode` -> `code`
- `Part.codeExecutionResult` -> `code_result`
- `Part.toolCall` -> `server_tool_call`
- `Part.toolResponse` -> `server_tool_result`
- `Part.thought` / `thoughtSignature` -> `reasoning`
- `safetyRatings` / `promptFeedback` -> `safety`

## Vertex Native

Vertex Native 可先复用 Gemini GenerateContent parser，但需要独立处理：

- endpoint path。
- model resource。
- auth/routing metadata。
- error shape。

## OpenAI-compatible Provider

OpenAI-compatible 不能假设完全等同 OpenAI。

parser 策略：

- 先按 OpenAI Chat/Responses 解析。
- 对 unknown fields 保留。
- 对常见扩展字段保留到 metadata：
  - `reasoning_content`
  - `reasoning`
  - vendor-specific usage details
  - extra finish reasons

## 测试要求

每个协议至少需要 fixture：

- non-stream text。
- stream text。
- tool call。
- tool result。
- reasoning/thinking。
- refusal/safety block。
- provider error。
- unknown field preservation。

测试应验证：

- SemanticNode 数量。
- ProviderType。
- NormalizedType。
- Path。
- Tool owner。
- Usage。
- Findings 入口字段。
