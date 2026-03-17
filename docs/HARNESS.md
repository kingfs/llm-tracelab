# HARNESS.md

## 文档目的

本文档描述 `llm-tracelab` 应如何沿着 Harness Engineering 的思路演进为一个 harness 化工作流。

当前仓库已经掌握了最关键的原材料：真实 LLM trace。下一步要做的，是把这些 trace 变成稳定、可重复、可比较的工程工件。

## 在本项目里，Harness 指什么

对本项目而言，harness 不只是 replay 工具。它应提供一个完整闭环，用于：

1. 收集有代表性的输入。
2. 规范化输出。
3. 执行测试套件。
4. 对结果打分或断言。
5. 与 baseline 进行比较。
6. 将结果反馈回开发流程与 CI。

## 已有基础能力

当前仓库已经具备很强的基础设施：

- 通过 proxy 采集真实流量
- 结构化 `.http` 工件
- usage 与 latency 元数据
- 浏览器可视化检查
- 测试用 replay transport
- `pkg/llm` 中初步的多厂商统一映射

这些能力不应被视作临时工具，而应被视作未来 harness 的底座。

## Harness 所需的一等对象

要从 trace 收集走向 harness 执行，仓库需要三个一等对象。

### 1. Case

`case` 表示一个可评测场景。

建议字段：

- `id`
- `trace`
- `task`
- `tags`
- `provider`
- `model`
- `assertions`
- `normalization_strategy`

示例：

```yaml
id: chat.basic.identity
trace: unittest/testdata/non-stream.http
task: chat
tags: [smoke, replay]
assertions:
  - type: contains
    target: final_text
    value: AI小助手
  - type: max_latency_ms
    value: 3000
```

### 2. Suite

`suite` 是一组按顺序或按主题组织的 case。

建议用途：

- smoke 套件
- replay 套件
- provider 兼容性套件
- chaos 韧性套件
- latency 预算套件

### 3. Run

`run` 表示某个 suite 在某次代码版本或某个运行目标上的执行结果。

建议输出：

- run id
- git sha
- suite id
- timestamp
- case results
- pass/fail summary
- metrics summary
- baseline diff

## 目标执行流程

```text
recorded traces
    |
    v
case definitions
    |
    v
normalization layer
    |
    v
harness runner
    |
    +--> scorer / assertions
    |
    +--> run artifacts
    |
    \--> markdown / json / html report
```

## 规范化层

harness 不应直接比较原始 provider payload，而应先将其转成共享结构。

建议的规范化响应字段：

- `final_text`
- `reasoning_text`
- `tool_calls`
- `usage`
- `finish_reason`
- `status_code`
- `duration_ms`
- `ttft_ms`
- `provider_metadata`

这层应尽量复用 `pkg/llm`，避免 monitor、测试和未来 harness 代码各自维护一套解析逻辑。

## 断言类型

第一版 harness 应尽量保持小而确定。

建议优先支持：

- `contains`
- `exact`
- `json_schema`
- `tool_call_name`
- `finish_reason`
- `max_latency_ms`
- `max_total_tokens`
- `status_code`

后续可以再加入：

- 语义相似度
- LLM-as-judge 评分
- refusal 质量检查
- flaky 检测

## 建议的 CLI 形态

当 harness 具备可执行能力后，最小 CLI 可以长这样：

```bash
llm-tracelab harness case generate <trace.http>
llm-tracelab harness run -suite suites/smoke.yaml
llm-tracelab harness compare -run runs/20260317-smoke.json -baseline runs/main-smoke.json
```

## 实现优先级

### 第一阶段

- 定义 `case / suite / run`
- 从现有 trace 自动生成 case
- 产出机器可读的 run 输出

### 第二阶段

- 收敛统一规范化路径
- 增加确定性断言
- 增加 markdown 报告

### 第三阶段

- 增加 baseline 对比
- 接入 CI
- 增加 provider 与 chaos 套件

### 第四阶段

- 增加语义评分
- 增加 flaky 检测
- 增加质量 / 成本 / 延迟的综合报告

## 设计规则

### 规则 1

尽量优先使用真实 trace，而不是合成 prompt。

### 规则 2

保持工件可读性。工程师必须能直接检查失败样本，而不是先反向理解某种二进制输出。

### 规则 3

将 provider 专有解析收敛到统一接口之后。

### 规则 4

将 run report 视为可版本化工件，而不是一次性的控制台输出。

### 规则 5

以降低系统熵为目标。统一一条规范化路径，要优于三条部分重叠的解析路径。

## 第一阶段的非目标

- 构建分布式评测平台
- 在没有确定性断言前先做高级 judge 模型
- 重写 monitor UI
- 在真实 suite 使用之前过度抽象 provider

## 与现有文档的关系

- `ARCHITECTURE.md` 解释当前系统如何实现。
- `docs/TRACE_FORMAT.md` 定义当前工件结构。
- `docs/harness-engineering-analysis.md` 解释为什么这个方向适合当前仓库。
