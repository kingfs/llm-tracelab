# ARCHITECTURE.md

## 概览

`llm-tracelab` 是一个单二进制 Go 应用，位于 SDK 客户端与 LLM 上游 API 之间。它负责将请求与响应记录为结构化 `.http` 工件，提供轻量浏览器监控页面，并支持在测试中回放录制流量。

从高层看，系统当前承担三类职责：

1. 代理线上流量。
2. 持久化 trace 工件。
3. 将这些工件转化为调试与测试输入。

## 运行拓扑

```text
SDK / Client
    |
    v
llm-tracelab proxy (:server.port)
    | \
    |  \__ recorder -> logs/*.http
    |
    \----> upstream LLM API

monitor UI (:monitor.port)
    ^
    |
log parser / scanner
```

## 启动流程

二进制入口位于 `cmd/server/main.go`。

启动顺序如下：

1. 从 `-c` 加载 YAML 配置。
2. 初始化结构化日志。
3. 对 `/v1/models` 进行上游连通性检查。
4. 如果配置了 monitor，则启动 monitor HTTP 服务。
5. 创建代理 handler。
6. 启动主 HTTP 服务。

这种设计降低了部署复杂度，也方便本地直接运行。

## 主要组件

### 1. Config

`internal/config` 负责将 YAML 文件加载为强类型配置对象，覆盖：

- 主服务端口
- monitor 端口
- upstream base URL 与 API key
- debug 输出目录与脱敏开关
- chaos 规则

### 2. Proxy

`internal/proxy` 负责实时请求/响应主链路。

关键职责包括：

- 将请求转发到配置的 upstream
- 重写鉴权和 host 头
- 强制 `Accept-Encoding: identity`
- 在可能的情况下为流式请求注入 usage 选项
- 为请求挂载日志上下文
- 采集 TTFT、响应字节数等指标
- 在保持客户端流式体验的同时，将响应镜像写盘

它是整个系统的运行核心。

### 3. Recorder

`internal/recorder` 负责工件生成。

每次请求会生成一个 `.http` 文件，包含：

- 固定长度头部块
- 序列化后的请求头
- 原始请求体
- 响应分隔符
- 序列化后的响应头
- 原始响应体

头部块中保存结构化元数据，包括：

- 请求标识
- 时间戳
- 模型名
- URL 与方法
- 状态码
- 总耗时
- TTFT
- 内容长度
- usage 字段
- 布局偏移信息

这种设计使同一个工件既能被人直接阅读，也能被程序稳定解析。

### 4. Monitor

`internal/monitor` 负责扫描日志目录、读取工件头部并渲染 HTML 页面。

当前 monitor 支持：

- 最近请求列表
- 聚合统计
- 单条工件详情页
- 原始请求/响应展示
- 聊天、Embedding、Rerank 内容提取
- 工件下载

它是一个有意保持轻量的文件驱动 UI。

### 5. Upstream Checker

`internal/upstream` 在启动阶段执行 fail-fast 连通性校验。如果 upstream 配置错误或不可用，进程会直接退出。

### 6. Chaos Manager

`internal/chaos` 根据模型匹配与概率规则注入延迟或错误响应。目前它主要服务于运行时行为测试，还没有接入更完整的 harness 编排。

### 7. Replay

`pkg/replay` 负责从录制的 `.http` 工件中重建 `http.Response`，并以自定义 transport 的形式接入测试。

这使得系统可以支持：

- 离线测试执行
- 对历史 upstream 交互的确定性回放
- SDK 级兼容性验证

### 8. Canonical LLM Mapping

`pkg/llm` 提供面向 provider 的统一内部抽象层。

当前已支持：

- OpenAI 兼容 Chat 接口
- Anthropic Messages 接口
- Gemini generateContent 接口

这个包是后续 harness 化过程中做标准化与跨厂商对比的天然基础。

## 数据流

### 实时请求链路

1. 客户端向代理发送 HTTP 请求。
2. 代理按需修改流式请求，争取拿到 usage 元数据。
3. Recorder 创建新的 `.http` 工件，并先写入头部占位字节。
4. 代理将请求转发给 upstream。
5. 响应头写入工件。
6. 响应体一边回传客户端，一边镜像写入工件。
7. usage 从 stream 或 non-stream 内容中被嗅探出来。
8. 最终元数据回填到工件头部块。

### Monitor 读路径

1. Monitor 从 `debug.output_dir` 扫描日志文件。
2. 它尽量只读取第一行或头部块。
3. 根据布局偏移重建请求与响应片段。
4. 解析其中一部分请求/响应字段用于展示。

### 测试回放路径

1. 测试创建 `replay.NewTransport(...)`。
2. 该 transport 打开录制 `.http` 文件。
3. 读取布局头部块。
4. 直接 seek 到响应段。
5. 向被测 SDK 客户端返回一个 `http.Response`。

## 当前架构优势

- 基于真实流量采集，而不是纯手写 mock。
- 同一份工件可同时用于调试和测试。
- 运行拓扑简单。
- 已具备 latency 和 usage 相关元数据。
- 已有初步多厂商统一抽象层。

## 当前架构限制

- 还没有一等公民的 `case / suite / run` 抽象。
- 解析逻辑仍散落在 monitor、测试和适配层中。
- replay 还是文件级能力，不是编排级能力。
- 缺少评分、baseline 对比和回归报告。
- 工件治理仍较为临时。

## 目标演进方向

仓库应逐步从：

`proxy + logs + monitor + replay`

演进为：

`trace capture + canonical normalization + case catalog + harness runner + diff reporting`

更具体的方向说明见 `docs/HARNESS.md`。
