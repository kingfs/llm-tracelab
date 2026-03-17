# llm-tracelab 项目分析与 Harness Engineering 化建议

## 1. 文档目的

本文档基于对当前仓库实现的梳理，结合 OpenAI 官方文章 [Harness Engineering](https://openai.com/zh-Hans-CN/index/harness-engineering/) 的核心思想，分析 `llm-tracelab` 的能力边界、当前优势、关键缺口，以及如何把它从“LLM 流量记录与回放工具”演进为“面向 LLM 工程迭代的测试与评测 harness”。

本文不是泛化的 LLM 平台设计，而是针对当前代码结构提出可落地的演进建议。

## 2. 对项目的整体判断

`llm-tracelab` 当前最成熟的能力不是“模型抽象”，而是“请求事实的保真采集”。

从主链路看，项目已经具备一条清晰的数据通路：

1. 代理拦截真实请求。
2. 将请求与响应按固定布局落盘为 `.http` 文件。
3. 抽取部分关键指标，如状态码、时延、TTFT、Token Usage。
4. 提供 Web UI 做浏览和人工分析。
5. 提供 `replay.Transport` 将录制结果重新接入单元测试。

这条链路意味着项目已经有了 harness 的雏形，因为 harness 的本质不是“再造一个 SDK”，而是把系统行为稳定地捕获、重放、比较和解释。

但从工程完整性看，当前项目仍停留在 harness 的前半段，偏重：

- 记录
- 浏览
- 手工回放

而尚未形成 harness 更关键的后半段：

- 用例编目
- 结果标准化
- 自动评分
- 基线对比
- 回归判定
- 面向 PR/版本迭代的持续评测

简化地说，`llm-tracelab` 已经有了“trace lab”，但还没有真正长成“evaluation harness”。

## 3. 当前架构拆解

### 3.1 入口与服务编排

程序入口在 [cmd/server/main.go](../cmd/server/main.go)。对应主流程可参考 `main()`，大致位于文件第 16 行附近。启动流程很直接：

- 读取配置
- 启动 upstream 自检
- 启动 Monitor Web UI
- 创建代理 Handler
- 启动 HTTP 服务

这说明项目当前是单进程、一体化部署模型，适合本地调试和轻量团队使用，也降低了接入成本。

### 3.2 代理层是当前系统的核心资产

代理逻辑在 [internal/proxy/handler.go](../internal/proxy/handler.go)。`NewHandler` 和 `ServeHTTP` 是关键入口。

其中最重要的设计点有三个：

1. `ensureStreamOptions` 会在流式请求中主动注入 `stream_options.include_usage=true`，保证上游尽量返回 usage 信息。这是非常典型的 harness 思维：不是被动接受输出，而是主动改善可观测性。
2. `UsageSniffer` 会在响应流经代理时同步写盘并从流式或非流式响应中提取 usage。这让“业务响应”和“观测信息采集”合并在同一条数据路径上，减少了额外埋点成本。
3. `InstrumentedResponseWriter` 会记录状态码、返回字节数和 TTFT，形成对推理体验很关键的一组指标。

这部分设计非常契合 Harness Engineering 中“先建立稳定、可读、可重复的反馈回路”的思想。

### 3.3 录制格式设计较好，是继续演进的基础

录制逻辑在 [internal/recorder/recorder.go](../internal/recorder/recorder.go)。`PrepareLogFile` 和 `UpdateLogFile` 是格式定义的核心。

当前格式的优点：

- 文件头保留结构化元数据 `RecordHeader`
- 请求头、请求体、响应头、响应体都以确定偏移落在同一个 `.http` 文件中
- 目录结构按 `upstream host / model / 年/月/日` 分层
- 记录了 `status_code`、`duration_ms`、`ttft_ms`、`usage`

这类“单文件、固定布局、可 seek”的格式很适合做 harness 输入资产，因为它同时满足：

- 人类可读
- 程序可解析
- 易于归档
- 易于做离线回放

这实际上已经比很多只吐 JSON 日志的方案更接近可运营的测试资产。

### 3.4 Monitor 解决了“可解释性”，但还没有解决“可比较性”

解析与 UI 逻辑在 [internal/monitor/parser.go](../internal/monitor/parser.go) 和 [internal/monitor/server.go](../internal/monitor/server.go)。

当前 Monitor 已经能做到：

- 浏览最近日志
- 聚合请求数、成功率、总 Token、平均 TTFT
- 展示原始请求与响应
- 提炼聊天内容、Embedding 输入、Rerank 请求
- 从响应中抽取最终 AI 内容与 reasoning 内容

这很适合人工排障和抽样分析。

但它目前主要回答的是：

- 发生了什么
- 这次请求长什么样
- 单条请求表现如何

它还不能系统回答：

- 这次变更比上个版本变好了还是变差了
- 哪类请求回归了
- 哪个模型适配层破坏了统一抽象
- 某次优化带来了吞吐改进还是语义退化

这正是 harness 下一步要补的能力。

### 3.5 Replay 已经打通测试接口，但目前仍是“单文件回放”

回放实现在 [pkg/replay/transport.go](../pkg/replay/transport.go)。

这个设计足够简单，也很实用：

- 把 `.http` 文件当成响应源
- 定位到 response 偏移
- 用 `http.ReadResponse` 恢复成标准 `http.Response`

它的价值在于把真实线上交互资产带回到测试环境，这是 harness 的关键一步。

但当前粒度仍然偏粗：

- 一次测试绑定一个文件
- 没有成套用例描述
- 没有多样本批量运行
- 没有预期断言模板
- 没有结果比对器

因此它更像“回放能力”，还不是“回归测试框架”。

### 3.6 `pkg/llm` 表明项目已经在尝试做跨厂商规范化

`pkg/llm` 中对 OpenAI、Anthropic、Gemini 做了统一请求/响应抽象，见：

- [pkg/llm/llm.go](../pkg/llm/llm.go)
- [pkg/llm/openai.go](../pkg/llm/openai.go)
- [pkg/llm/anthropic.go](../pkg/llm/anthropic.go)
- [pkg/llm/google.go](../pkg/llm/google.go)

这条线很重要，因为真正的 harness 不是只记录 HTTP，而是要把“厂商差异”逐步压缩成“统一评测对象”。

如果后续要做批量评测、跨模型对比、跨供应商回归，这个包会成为关键中间层。

## 4. 从 Harness Engineering 视角看，这个项目已经做对了什么

结合 OpenAI 官方文章，我认为当前项目已经具备以下几个和 harness 思想高度一致的优点。

### 4.1 以真实工件为中心，而不是以口头描述为中心

这个项目不是人工抄写 prompt 和输出，而是把真实 HTTP 交互完整保存下来。  
这意味着评测输入不是“回忆中的请求”，而是“事实发生过的请求”。在 LLM 工程里，这一点价值很高，因为大量问题都隐藏在：

- headers
- stream / non-stream 差异
- SDK 默认参数
- tool 调用结构
- usage 返回时机
- 上游兼容层行为

真实工件优先，是 harness 能产生可信结论的前提。

### 4.2 重视可读性与人工调试效率

Harness 并不只是自动跑分，好的 harness 还必须让工程师读得懂结果。当前 `.http + header JSON + monitor UI` 的组合，明显是面向工程分析优化过的。这一点比“只产出一个分数”更有价值。

### 4.3 把观测信息放进主数据面

`ttft_ms`、`duration_ms`、`usage` 都是在主链路里采集，而不是靠外围监控系统“猜”。这能显著降低指标漂移问题。

### 4.4 混沌注入已经为鲁棒性测试留出了接口

[internal/chaos/manager.go](../internal/chaos/manager.go) 的存在很关键。  
Harness 不只服务“正确性”，还服务“系统行为可验证性”。延迟与错误注入能力，天然可以扩展成稳定性基准、客户端重试验证、超时策略验证等场景。

## 5. 当前最关键的缺口

如果目标是参考 Harness Engineering 的核心思想，当前项目至少有六个关键缺口。

### 5.1 缺少“用例定义层”

现在的核心资产是 `.http` 文件，但 `.http` 仍偏底层。  
它适合作为事实记录，却不适合作为测试入口抽象。

当前缺的是类似 `cases/*.yaml` 或 `suite.json` 的用例描述层，用来表达：

- 这个 case 属于哪类任务
- 使用哪份 trace 或输入模板
- 期待评测哪些维度
- 哪些字段允许波动
- 哪些字段必须严格一致
- 适用于哪些 provider / model / adapter

没有这一层，就很难形成大规模 harness。

### 5.2 缺少“标准化输出层”

项目目前能记录和回放，但对结果比较还不够友好。  
LLM 输出天然带噪声，如果只比较原始文本，会造成高误报率。

需要一个标准化层，把响应规约为统一结构，例如：

- final_text
- reasoning_text
- tool_calls
- usage
- finish_reason
- latency metrics
- safety / refusal signals

`pkg/llm` 已经迈出了第一步，但它还没有被接到录制文件解析、回放运行和评测结果汇总的主流程里。

### 5.3 缺少“评分器 / 判定器”

Harness 的核心不是采集，而是比较和判定。  
当前仓库中的测试主要还是写死断言，例如 `want contain "AI小助手"`，见 [unittest/go-openai_replay_test.go](../unittest/go-openai_replay_test.go)。

这对演示有效，但对真实工程不够：

- 文本类任务需要语义评分
- 结构化输出需要 schema 校验
- tool 调用需要参数匹配
- latency 需要阈值比较
- usage 需要 budget 判定
- provider 兼容性需要字段归一

没有 scorer，项目很难成为团队级回归工具。

### 5.4 缺少“基线与差异报告”

Monitor 可以看单次请求，但没有 baseline comparison。  
Harness 更需要：

- 与上一个版本比
- 与另一个模型比
- 与另一套 prompt / system policy 比
- 与某个提交前后比

这要求系统能产出稳定的 run artifact，而不仅是单条 trace。

### 5.5 缺少“批量运行编排”

当前回放主要面向单测。  
真正的 harness 往往要支持：

- 批量读取多个 trace
- 按 suite 分组
- 并发运行
- 汇总统计
- 输出失败样本
- 保存 run manifest

这部分目前还不存在。

### 5.6 缺少“资产治理”

日志文件按日期和模型存储，适合观察，但还不适合长期治理。  
后续如果数据量变大，会遇到：

- 录制数据去重
- 敏感信息脱敏
- 用例采样策略
- 基线冻结与版本化
- 老旧样本淘汰
- 同一任务多个代表性样本管理

Harness Engineering 很强调降低系统熵，而资产治理正是降低熵的核心。

## 6. 对项目的一个更准确定位

我认为这个项目当前最准确的定位不是：

“一个 LLM 代理工具”

而应该是：

“一个以真实 API trace 为核心资产的 LLM 调试、回放与评测基础设施”

这个定位很重要，因为它会直接决定后续设计优先级。

如果按“代理工具”定位，重点会落在：

- 支持更多厂商
- 提高兼容性
- 做更多 UI

如果按“评测基础设施”定位，重点应该转向：

- 规范 trace 资产
- 建立 case / suite / run 三层模型
- 增加 scorer
- 做 diff 与回归报告
- 把 CI 接到评测结果上

后者更接近 Harness Engineering 的路线，也更能形成项目的差异化价值。

## 7. 建议的目标架构

建议把项目演进为五层结构。

### 7.1 Layer 1: Trace Capture

职责：继续负责真实流量拦截和保真录制。  
当前实现已经基本具备，只需继续增强：

- 更稳定的脱敏策略
- 更明确的 schema version
- 更多 provider-specific metadata
- request fingerprint

这层继续由 `proxy + recorder` 负责。

### 7.2 Layer 2: Canonical Normalization

职责：把各厂商请求/响应、流式/非流式结果转成统一结构。

建议新增类似：

```text
internal/harness/normalize/
```

核心对象建议包括：

- `CanonicalCaseInput`
- `CanonicalResponse`
- `CanonicalMetrics`
- `CanonicalToolCall`

这一层应复用 `pkg/llm`，不要再让 monitor、replay、test 各自做一套解析。

### 7.3 Layer 3: Case / Suite Definition

职责：让 trace 从“日志”升级为“可运行的测试资产”。

建议新增：

```yaml
id: chat.basic.identity
trace: logs/api.openai.com/gpt-4o/2026/03/17/xxx.http
task: chat
tags: [smoke, replay, openai-compatible]
assertions:
  - type: contains
    target: final_text
    value: "AI小助手"
  - type: max_latency_ms
    value: 3000
  - type: max_total_tokens
    value: 256
```

也可以进一步支持：

- `semantic_similarity`
- `json_schema`
- `tool_call_name`
- `finish_reason`
- `refusal_expected`

### 7.4 Layer 4: Runner + Scorer

职责：批量执行 suite，形成结构化 run 结果。

建议新增命令，例如：

```bash
llm-tracelab harness run -suite suites/smoke.yaml
```

输出对象至少包括：

- run id
- git sha
- suite id
- pass / fail / flaky
- per-case metrics
- baseline diff
- failure summary

### 7.5 Layer 5: Report + Feedback Loop

职责：把结果反馈给工程迭代流程。

建议支持三类输出：

1. `json`
2. `markdown`
3. 简化 HTML report

其中 Markdown report 最适合接 CI，直接发到 PR 或构建产物中。

## 8. 面向当前仓库的分阶段路线图

### 阶段 A: 先把 trace 变成稳定 case

目标：低成本形成第一版 harness。

建议做法：

1. 增加 `docs/trace-format.md`，正式定义 `.http` 文件格式和字段语义。
2. 新增 `cases/` 目录，引入用例定义文件。
3. 提供 `llm-tracelab case generate <trace.http>` 命令，从录制文件自动生成 case 草稿。
4. 把现有 `unittest` 示例改为基于 case 运行，而不是直接写死文件名和字符串断言。

这是最小可行路径，投入不大，但收益很高。

### 阶段 B: 统一标准化与评分

目标：让结果“可比”。

建议做法：

1. 新增统一 normalize 包，封装流式、非流式、不同 provider 的输出规约。
2. 把 `monitor.ParseLogFile` 中对 AI 内容的解析迁移到共享逻辑。
3. 新增 `assert` / `score` 模块，先支持：
   - contains
   - exact
   - json_schema
   - max_latency_ms
   - max_total_tokens
4. 输出统一 run result JSON。

### 阶段 C: 引入 baseline 与 diff

目标：让 harness 开始服务日常迭代。

建议做法：

1. 保存每次运行结果到 `runs/`。
2. 支持 `compare --baseline <run-id>`。
3. 在 CI 中增加：
   - smoke suite
   - replay suite
   - provider compatibility suite
4. 生成 Markdown diff 报告。

### 阶段 D: 引入更高级的评测能力

目标：把项目从 replay 工具升级为真正的 LLM 工程基础设施。

可以逐步加入：

- LLM-as-judge 评分
- 语义相似度评分
- 多模型横向对比
- 成本/延迟/质量三角报告
- 混沌场景自动验证
- flaky case 标记与重跑

## 9. 优先级建议

如果只允许做三件事，我建议优先做：

1. `case/suite/run` 三层对象建模
2. canonical normalize + scorer
3. CI 可消费的 run report

原因很简单：

- 这三件事会把项目从“看日志”提升到“做工程决策”
- 这三件事复用现有 recorder/replay 资产最多
- 这三件事最符合 Harness Engineering 的核心思想

而“支持更多 provider”虽然也有价值，但优先级应次于“让现有资产能产生稳定结论”。

## 10. 对现有测试体系的评价

当前测试可以分为两类：

- `pkg/llm` 的格式映射测试
- `unittest` 中基于录制文件的 replay 测试

优点：

- 已经覆盖了最基本的回放路径
- 已经验证了统一抽象和真实录制文件的结合

不足：

- 断言过于样例化
- 缺少 suite 级组织
- 缺少批量统计
- 缺少回归 diff
- 缺少失败工件输出

它们适合作为 harness 的种子，但还不能替代 harness 本身。

## 11. 一份更贴近该项目的 Harness Engineering 原则清单

参考官方文章，我建议本项目后续开发遵循以下原则。

### 原则 1: 任何结论都尽量建立在真实 trace 上

不要优先发明 synthetic case。  
优先从真实调用中沉淀代表性 case，再做归档、筛选和标注。

### 原则 2: 先做统一对象，再做更多 provider

兼容更多厂商不是问题本身。  
真正的问题是不同厂商结果能否在统一框架里比较。

### 原则 3: 报告必须可读

分数不够。  
每个失败样本都应能追溯到原始 trace、标准化结果、断言结果和 diff 摘要。

### 原则 4: 把评测运行结果也视为工件

不仅 trace 是工件，run result、baseline、diff report 也都应被保存和版本化。

### 原则 5: 优先降低系统熵

不要让解析逻辑散落在 monitor、tests、adapters 里。  
统一 normalize，统一 scorer，统一 report。

## 12. 建议新增的文档体系

如果要把项目文档化做扎实，建议后续补齐以下文档：

- `docs/harness-engineering-analysis.md`
  当前文档，负责解释方向与架构判断。
- `docs/architecture.md`
  描述代理、录制、回放、监控、标准化、评测编排的整体结构。
- `docs/trace-format.md`
  定义 `.http` 记录格式、字段、版本策略和兼容约束。
- `docs/harness-cli.md`
  定义未来 `case/suite/run/compare` CLI 的使用方式。
- `docs/roadmap.md`
  描述阶段性目标与里程碑。

## 13. 结论

`llm-tracelab` 当前已经具备成为优秀 harness 基础设施的关键前提：

- 真实流量保真录制
- 结构化元数据
- 回放接入测试
- UI 可视化
- 部分多厂商统一抽象

它最缺的不是“再多几个 provider”，而是把已有能力组织成一套完整的、可持续演进的 harness 闭环。

换句话说，这个项目离 Harness Engineering 并不远。  
它已经拿到了最难复制的资产：真实 trace 和真实工程路径。  
下一步要做的，是把这些资产从“可查看”升级为“可比较、可判定、可回归、可集成到日常开发流程”。

一旦完成这一步，`llm-tracelab` 的价值会从本地调试工具，提升为团队级 LLM 工程质量基础设施。

## 14. 参考资料

- OpenAI, Harness Engineering: <https://openai.com/zh-Hans-CN/index/harness-engineering/>
- 项目入口：[cmd/server/main.go](../cmd/server/main.go)
- 代理实现：[internal/proxy/handler.go](../internal/proxy/handler.go)
- 录制实现：[internal/recorder/recorder.go](../internal/recorder/recorder.go)
- Monitor 解析：[internal/monitor/parser.go](../internal/monitor/parser.go)
- 回放实现：[pkg/replay/transport.go](../pkg/replay/transport.go)
