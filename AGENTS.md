# AGENTS.md

## 文档目的

本仓库构建的是 `llm-tracelab`：一个基于 Go 的 LLM 代理服务。它负责记录真实 API 流量为结构化 `.http` 工件，提供轻量监控页面，并支持在测试中回放这些录制结果。

本文档面向两类协作者：

- 人类开发者
- 编码代理与自动化助手

它定义了仓库导航方式、关键不变量，以及进行安全修改时需要遵守的规则。

## 项目目标

当前仓库主要围绕三件事优化：

1. 高保真捕获真实 LLM HTTP 交互。
2. 通过可读的日志工件与简洁的监控页面提升本地调试效率。
3. 让录制流量可以在单元测试中复用。

更长期的方向，是将它演进为以 trace 工件为核心的 harness 化评测工作流。

## 仓库结构

- `cmd/server`
  二进制入口。
- `internal/config`
  YAML 配置加载。
- `internal/proxy`
  反向代理、请求改写、usage 嗅探、时延采集。
- `internal/recorder`
  `.http` 文件创建与元数据回填。
- `internal/monitor`
  日志扫描、解析、HTML 渲染、下载接口。
- `internal/upstream`
  启动时上游连通性检查。
- `internal/chaos`
  延迟与错误注入规则。
- `pkg/replay`
  测试用回放传输层。
- `pkg/llm`
  OpenAI、Anthropic、Gemini 的统一请求/响应映射层。
- `unittest`
  基于录制工件的回放测试。
- `config`
  示例运行配置。
- `docs`
  架构、harness、trace 格式等文档。

## 核心不变量

### 录制格式

- `.http` 文件是当前仓库最核心的工件。
- 文件前 `2048` 字节保留给 JSON 头部块和结尾换行。
- 文件布局固定为：
  `头部块` + `请求头` + `请求体` + `\n` + `响应头` + `响应体`
- `internal/recorder.HeaderLen` 与 `pkg/replay.HeaderLen` 必须保持一致。

### 代理行为

- 流式请求可能会被注入 `stream_options.include_usage=true`。
- 响应体在返回给客户端的同时会写入磁盘。
- usage 提取逻辑不能破坏流式传输语义。
- TTFT、duration、status code、content length 都属于一等元数据。

### 回放行为

- 回放强依赖录制文件布局稳定。
- 任何格式调整都必须同步更新 recorder、parser、replay 和文档。

### Monitor 行为

- Monitor 当前是有意保持轻量、文件驱动的。
- 解析逻辑应尽量容忍部分损坏或未完整写入的工件。

## 协作约定

### 修改格式之前

如果你改动了 `.http` 工件格式，至少同时检查并更新：

- `internal/recorder/recorder.go`
- `pkg/replay/transport.go`
- `internal/monitor/parser.go`
- `docs/TRACE_FORMAT.md`
- `unittest` 或 `pkg/llm` 下受影响的测试

### 修改代理语义之前

请先确认以下下游路径是否会受影响：

- stream 与 non-stream 路径
- usage 提取
- monitor 解析
- replay 假设
- `unittest/testdata` 中的测试工件

### 常用命令

- 构建：`go build -o llm-tracelab ./cmd/server`
- 测试：`go test ./...`
- 运行：`./llm-tracelab -c config/config.yaml`

### 配置说明

- 代理端口和 monitor 端口定义在 `config/config.yaml` 中。
- 示例配置默认指向本地 upstream。
- `debug.output_dir` 是录制工件根目录。

## 文档集合

- `README.md`
  产品级介绍与快速开始。
- `ARCHITECTURE.md`
  当前系统架构与数据流。
- `docs/HARNESS.md`
  harness 化方向与目标抽象。
- `docs/TRACE_FORMAT.md`
  `.http` 工件格式规范。
- `docs/harness-engineering-analysis.md`
  从 Harness Engineering 视角对项目进行分析。

## 安全修改策略

优先做小而完整的修改。当前仓库最脆弱的部分主要有：

- 文件格式兼容性
- 流式处理
- 请求/响应解析
- 多厂商统一抽象的假设

如果无法确定取舍，优先保证工件兼容性，再考虑新增元数据或增强能力。
