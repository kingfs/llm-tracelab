# v1 审计与高级分析设计

## 目标

审计与高级分析层基于 Observation IR 工作，用来识别：

- 危险工具调用。
- 危险 shell/bash 命令。
- 敏感信息泄露。
- provider safety/refusal。
- 工具执行错误。
- 异常模型行为。
- 长期使用经验和模型横向差异。

该层不在转发热路径执行。

## 分析原则

- 优先使用结构化节点，而不是扫描整段 raw 文本。
- deterministic scanner 优先，LLM 分析后置。
- 每个 finding 必须有 evidence path。
- 每个 detector 必须有 version。
- finding 可以重算。
- 审计结果不能替代 raw 证据。

## Finding 结构

```go
type Finding struct {
    ID              string
    TraceID         string
    Category        string
    Severity        string
    Confidence      float64
    Title           string
    Description     string
    EvidencePath    string
    EvidenceExcerpt string
    NodeID          string
    Detector        string
    DetectorVersion string
    CreatedAt       time.Time
}
```

## Severity

建议分级：

```text
info
low
medium
high
critical
```

## Category

第一版支持：

```text
dangerous_command
sensitive_data
credential_leak
network_exfiltration
filesystem_destructive_operation
unsafe_code_execution
provider_safety_block
model_refusal
unexpected_tool_call
tool_result_error
prompt_injection_signal
schema_parse_warning
performance_anomaly
```

## 危险命令检测

### 扫描位置

优先扫描：

- OpenAI Responses `local_shell_call`
- OpenAI Responses `code_interpreter_call`
- OpenAI Responses `apply_patch`
- Claude `tool_use` 中的 bash/text_editor/code execution 工具。
- Gemini `Part.executableCode`
- Gemini `Part.functionCall.args`
- OpenAI Chat `tool_calls[].function.arguments`

### 高风险命令样例

```text
rm -rf /
rm -rf ~
sudo rm
chmod -R 777
chown -R
curl ... | sh
wget ... | sh
nc -e
bash -i
dd if=
mkfs
diskutil erase
security find-generic-password
cat ~/.ssh/id_rsa
cat ~/.aws/credentials
env | curl
```

### 识别策略

- 先做 shell tokenization，避免简单 substring 误判。
- 识别 pipe、redirect、subshell。
- 识别下载后执行。
- 识别环境变量和凭据文件读取。
- 识别 destructive path。

## 敏感信息检测

### 类型

第一版支持：

- API key。
- Bearer token。
- 私钥块。
- SSH key。
- AWS access key。
- GitHub token。
- 数据库连接串。
- 身份证号。
- 手机号。
- 邮箱。
- 内网 URL/IP。
- Cookie。

### 扫描位置

- user message。
- system/developer instruction。
- tool call args。
- tool result。
- model output。
- code block。
- file/path metadata。

### 模式

支持三种模式：

```text
observe
redact_at_rest
inline_redact
```

v1 默认只实现 `observe`：

- 不修改请求。
- 不修改响应。
- 只产生 finding。

`redact_at_rest` 和 `inline_redact` 留作后续扩展。

## Tool Owner 分类

工具执行归属必须明确：

```text
model_requested
client_executed
provider_executed
inferred
unknown
```

示例：

- Claude `tool_use`: `model_requested`
- Claude `tool_result`: `client_executed`
- Claude web_search server tool: `provider_executed`
- OpenAI Responses `function_call`: `model_requested`
- OpenAI Responses `web_search_call`: 通常 `provider_executed`
- Gemini `functionCall`: `model_requested`
- Gemini `codeExecution`: 根据字段判断 provider/system 执行。

## Provider Safety 统一

OpenAI：

- refusal content。
- content filter finish reason。
- response error/incomplete details。

Claude：

- refusal text。
- stop_reason。
- safety/tool errors。

Gemini：

- `promptFeedback.blockReason`
- `safetyRatings`
- `finishReason=SAFETY` 等。

统一映射到：

```text
provider_safety_block
model_refusal
```

## 性能分析

性能 findings 可以包括：

- TTFT 过高。
- tokens/s 过低。
- cache hit ratio 低。
- upstream fallback 频繁。
- 某 provider/model 错误率上升。
- streaming 中断。

性能分析应结合：

- trace metadata。
- routing metadata。
- usage metadata。
- stream event timing。

## LLM 高级总结

LLM-based analysis 是可选层。

适合任务：

- session 摘要。
- 失败根因总结。
- 模型横向比较。
- prompt 改进建议。
- 工具设计问题归纳。
- 长期经验沉淀。

要求：

- 输入应来自 Observation IR 和 findings。
- 尽量使用 redacted/minimized 内容。
- 分析模型、prompt、版本需要记录。
- 输出必须引用 trace/node/finding 证据。

## 检测器接口

建议接口：

```go
type Detector interface {
    Name() string
    Version() string
    Analyze(ctx context.Context, obs TraceObservation) ([]Finding, error)
}
```

## 第一版 detector

优先级：

1. `dangerous_shell_detector`
2. `credential_detector`
3. `pii_detector`
4. `provider_safety_detector`
5. `tool_error_detector`
6. `performance_detector`

## 测试要求

- 每个 detector 都有 fixture。
- fixture 不依赖外网。
- 对误报和漏报分别有测试。
- finding 必须包含 evidence path。
- detector version 变化时测试应能解释差异。
