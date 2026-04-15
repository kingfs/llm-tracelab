# llm-tracelab

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](./LICENSE)

**中文说明** | [English](./README_EN.md)

`llm-tracelab` 是一个面向 OpenAI Compatible API 的 LLM 录制与回放代理。它的核心目标很直接：

- 开发时把真实大模型 HTTP 请求录下来
- 单元测试时直接回放，不再依赖外网和真实模型
- 让测试更稳定、更快、更省钱

项目定位接近 `http record/replay`，但针对 LLM 场景补了流式响应、Token usage、可视化查看和故障注入能力。

## 当前版本发布说明

这次重构后的版本重点有四个：

- `pkg/llm` 升级为按 provider/endpoint 工作的 adapter 层，统一处理 request、response、stream transcript 和 usage pipeline
- Monitor 改成 Go embed 的 React UI，列表页异步分页，详情页支持 timeline / summary / raw protocol
- SQLite 索引改用稳定 `trace_id`，不再在 URL 中暴露本地路径
- `LLM_PROXY_V3` 的 `# event:` 现在不仅有 request/response 基础事件，还会落 `llm.*` provider timeline

## 适合什么场景

- 给 SDK 或业务代码做高可靠单元测试
- 复现线上 prompt / tool call / stream 问题
- 统计模型调用耗时、TTFT、Token 消耗
- 在本地做 LLM API 代理调试和混沌测试

## 核心能力

- 透明代理 OpenAI compatible 请求
- 将一次请求/响应保存为本地 `.http` cassette
- 使用 `pkg/replay.Transport` 在测试中直接回放
- Monitor 页面查看请求详情、统一 timeline、原始协议和 Token 消耗
- 使用 SQLite 维护 metadata 索引，避免统计页每次全量读文件
- 支持对旧版 V2 记录文件兼容读取

## 项目结构

```text
cmd/server            服务入口
internal/proxy        代理转发、stream 注入、响应拦截
internal/recorder     .http 录制与落盘
internal/store        SQLite 元数据索引
internal/monitor      Monitor UI 与详情解析
pkg/recordfile        录制文件格式 V2/V3 解析与 V3 写入
pkg/replay            单元测试回放 Transport
pkg/llm               多厂商请求/响应归一化
```

更适合 AI 阅读的项目约定见 [AGENTS.md](./AGENTS.md)，架构摘要见 [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md)，上游兼容矩阵见 [docs/UPSTREAM_PROVIDERS.md](./docs/UPSTREAM_PROVIDERS.md)。

## 录制文件与索引

当前写入格式是 `LLM_PROXY_V3`：

1. 文件前导包含紧凑元数据行，而不是固定 2KB 占位行
2. 原始 HTTP request/response 仍然完整保留，方便人工排查
3. `# event:` 会记录统一 timeline，例如 `llm.output_text.delta`、`llm.reasoning.delta`、`llm.tool_call`、`llm.usage`
4. 请求摘要、耗时、Token、trace id 等会同步索引到 `trace_index.sqlite3`

默认存储布局：

```text
logs/
  trace_index.sqlite3
  <upstream-host>/<model>/<yyyy>/<mm>/<dd>/*.http
```

## 快速开始

### 1. 配置

编辑 [config/config.yaml](./config/config.yaml)：

```yaml
server:
  port: "8080"

monitor:
  port: "8081"

upstream:
  base_url: "https://api.openai.com"
  api_key: "sk-xxx"
  provider_preset: "openai"
  protocol_family: ""
  routing_profile: ""
  api_version: ""
  deployment: ""
  headers: {}

debug:
  output_dir: "./logs"
  mask_key: false
```

支持的环境变量覆盖：

- `LLM_TRACELAB_SERVER_PORT`
- `LLM_TRACELAB_MONITOR_PORT`
- `LLM_TRACELAB_UPSTREAM_BASE_URL`
- `LLM_TRACELAB_UPSTREAM_API_KEY`
- `LLM_TRACELAB_UPSTREAM_PROVIDER_PRESET`
- `LLM_TRACELAB_UPSTREAM_PROTOCOL_FAMILY`
- `LLM_TRACELAB_UPSTREAM_ROUTING_PROFILE`
- `LLM_TRACELAB_UPSTREAM_API_VERSION`
- `LLM_TRACELAB_UPSTREAM_DEPLOYMENT`
- `LLM_TRACELAB_OUTPUT_DIR`
- `LLM_TRACELAB_MASK_KEY`

推荐的兼容配置思路：

- OpenAI / OpenRouter / Fireworks / Together / DeepSeek / Groq 等 OpenAI-compatible 服务：只设置 `provider_preset` 和 `base_url`
- Azure OpenAI `/openai/v1/...`：设置 `provider_preset: azure`，可选 `api_version`
- Azure deployment 路由：设置 `provider_preset: azure`，并补 `deployment`
- vLLM OpenAI-compatible server：设置 `provider_preset: vllm`
- Anthropic Messages API：设置 `provider_preset: anthropic`，如需 beta 能力可在 `headers` 里补 `anthropic-beta`
- Google GenAI API：设置 `provider_preset: google_genai`，当前支持 `generateContent` 和 `streamGenerateContent` 基础闭环

当前推荐支持矩阵：

- `provider_preset: openai`
  `protocol_family: openai_compatible`
  `routing_profile: openai_default`
- `provider_preset: openrouter | fireworks | together | deepseek | groq | moonshot | cerebras | perplexity`
  `protocol_family: openai_compatible`
  `routing_profile: openai_default`
- `provider_preset: azure`
  `protocol_family: openai_compatible`
  `routing_profile: azure_openai_v1` 或 `azure_openai_deployment`
- `provider_preset: vllm`
  `protocol_family: openai_compatible`
  `routing_profile: vllm_openai`
- `provider_preset: anthropic`
  `protocol_family: anthropic_messages`
  `routing_profile: anthropic_default`
- `provider_preset: google_genai | google | gemini`
  `protocol_family: google_genai`
  `routing_profile: google_ai_studio`

Anthropic 示例：

```yaml
upstream:
  base_url: "https://api.anthropic.com"
  api_key: "sk-ant-xxx"
  provider_preset: "anthropic"
  api_version: "2023-06-01"
  headers:
    anthropic-beta: "tools-2024-04-04"
```

Google GenAI 示例：

```yaml
upstream:
  base_url: "https://generativelanguage.googleapis.com"
  api_key: "AIza..."
  provider_preset: "google_genai"
```

### 2. 构建和运行

推荐使用 `go-task`：

```bash
task build
task run
task migrate
```

如果只想直接运行：

```bash
go run ./cmd/server -c config/config.yaml
```

把你的 SDK `base_url` 指向 `http://localhost:8080/v1` 后，请求就会被代理并录制。

### 3. 打开 Monitor

访问 `http://localhost:8081`。

详情页现在包含三个主视图：

- `Timeline`：消费 cassette 中的统一 `llm.*` 事件
- `Summary`：按对话、工具、输出块聚合展示
- `Raw Protocol`：左右分栏查看原始 request/response

## 老日志迁移与索引重建

显式迁移命令：

```bash
go run ./cmd/server migrate -c config/config.yaml
```

这个命令默认会做两件事：

- 将旧的 `LLM_PROXY_V2` `.http` 文件原地改写成 `LLM_PROXY_V3`
- 清空并重建 `trace_index.sqlite3`

如果只想做其中一部分：

```bash
go run ./cmd/server migrate -c config/config.yaml -rewrite-v2=false
go run ./cmd/server migrate -c config/config.yaml -rebuild-index=false
```

适合老日志目录批量升级，或者 SQLite 索引损坏/丢失后的全量恢复。

## Docker / Compose

容器内约定的标准路径：

- 可执行文件：`/app/bin/llm-tracelab`
- 配置文件：`/app/config/config.yaml`
- 数据目录：`/app/data/traces`
- SQLite 索引：`/app/data/traces/trace_index.sqlite3`

默认提供：

- [Dockerfile](./Dockerfile)
- [docker-compose.yml](./docker-compose.yml)
- [config/config.docker.yaml](./config/config.docker.yaml)

启动方式：

```bash
export LLM_TRACELAB_UPSTREAM_API_KEY=sk-xxx
docker compose up --build
```

如果只想直接使用已经发布到 Docker Hub 的镜像，可以不克隆仓库，直接运行：

```bash
docker run --rm \
  -p 8080:8080 \
  -p 8081:8081 \
  -e LLM_TRACELAB_UPSTREAM_BASE_URL=https://api.openai.com \
  -e LLM_TRACELAB_UPSTREAM_API_KEY=sk-xxx \
  -e LLM_TRACELAB_OUTPUT_DIR=/app/data/traces \
  -e LLM_TRACELAB_SERVER_PORT=8080 \
  -e LLM_TRACELAB_MONITOR_PORT=8081 \
  -v "$(pwd)/docker-data:/app/data" \
  kingfs/llm-tracelab:latest serve -c /app/config/config.yaml
```

如果你更习惯 `docker compose`，也可以直接引用 Docker Hub 镜像：

```yaml
services:
  llm-tracelab:
    image: kingfs/llm-tracelab:latest
    ports:
      - "8080:8080"
      - "8081:8081"
    environment:
      LLM_TRACELAB_UPSTREAM_BASE_URL: https://api.openai.com
      LLM_TRACELAB_UPSTREAM_API_KEY: ${LLM_TRACELAB_UPSTREAM_API_KEY}
      LLM_TRACELAB_OUTPUT_DIR: /app/data/traces
      LLM_TRACELAB_SERVER_PORT: "8080"
      LLM_TRACELAB_MONITOR_PORT: "8081"
    volumes:
      - ./config/config.docker.yaml:/app/config/config.yaml:ro
      - ./docker-data:/app/data
    command: ["serve", "-c", "/app/config/config.yaml"]
```

如果本机访问 Go 官方模块代理较慢，可以在构建时直接传入 `GOPROXY`：

```bash
GOPROXY=https://goproxy.cn,direct docker compose build
```

默认挂载：

- `./config/config.docker.yaml -> /app/config/config.yaml:ro`
- `./docker-data -> /app/data`

运行镜像默认使用 `root` 用户启动。这是为了兼容最常见的 bind mount 场景，避免宿主机目录属主与容器内固定 UID/GID 不一致时出现 `permission denied`，例如无法创建 `/app/data/traces`。

如果在容器外部配置，优先通过挂载配置文件和环境变量覆盖 `upstream`、端口、输出目录；`debug.output_dir` 建议始终指向容器内挂载卷中的固定路径。

## 开发命令

```bash
task fmt
task lint
task test
task build
task run
task migrate
task check
task docker:build
task docker:up
```

## 在单元测试中回放

```go
func TestChat(t *testing.T) {
    tr := replay.NewTransport("testdata/chat.http")

    cfg := openai.DefaultConfig("fake-key")
    cfg.BaseURL = "http://localhost/v1"
    cfg.HTTPClient = &http.Client{Transport: tr}

    client := openai.NewClientWithConfig(cfg)
    resp, err := client.CreateChatCompletion(context.Background(), req)
    _ = resp
    _ = err
}
```

## 当前设计原则

- `.http` cassette 是回放的事实来源
- SQLite 只做 metadata 索引，不替代原始文件
- 新文件写 V3，旧文件继续兼容读取
- 尽量保持文件可读、测试离线、实现本地优先
- provider 语义、stream transcript、usage 和 event timeline 尽量收敛在 `pkg/llm`

## 截图

- Monitor 总览
  ![](./images/traffic_monitor.png)
- 对话详情
  ![](./images/message_detail.png)
- SSE 原始流
  ![](./images/sse_message_raw.png)
- 非流式响应
  ![](./images/message_raw.png)

## License

[MIT](./LICENSE)
