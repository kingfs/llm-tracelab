# llm-tracelab

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](./LICENSE)

[中文说明](./README.md) | **English**

`llm-tracelab` is a local-first record/replay proxy for OpenAI-compatible LLM APIs.
The core workflow is simple:

- record real LLM HTTP traffic during development
- replay it in unit tests without calling the upstream model
- get faster, cheaper, and more reliable tests

It is similar in spirit to HTTP record/replay tooling, but tuned for LLM traffic: streaming responses, token usage, trace inspection, and lightweight chaos testing.

## Good Fit

- stable unit tests for SDK or application code
- reproducing prompt, tool-call, or streaming issues
- inspecting latency, TTFT, and token usage
- local proxying and debugging of LLM traffic

## Features

- transparent proxy for OpenAI-compatible requests
- persists each exchange as a local `.http` cassette
- `pkg/replay.Transport` for replay-based unit tests
- monitor UI for request detail and raw protocol views
- SQLite metadata index for fast list/stat queries
- backward-compatible readers for legacy V2 record files

## Layout

```text
cmd/server            server entrypoint
internal/proxy        reverse proxy and response interception
internal/recorder     cassette recording and persistence
internal/store        SQLite metadata index
internal/monitor      monitor UI and detail parsing
pkg/recordfile        shared V2/V3 record format parser/writer
pkg/replay            replay transport for tests
pkg/llm               cross-provider normalization helpers
```

AI-oriented project guidance lives in [AGENTS.md](./AGENTS.md). A short technical summary is in [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md).

## Record Format And Index

New recordings use `LLM_PROXY_V3`:

1. a compact metadata prelude instead of a fixed 2KB header block
2. full raw HTTP request/response bytes kept for inspection and replay
3. metadata indexed into `trace_index.sqlite3` for fast monitor queries

Default storage layout:

```text
logs/
  trace_index.sqlite3
  <upstream-host>/<model>/<yyyy>/<mm>/<dd>/*.http
```

## Quick Start

### 1. Configure

Edit [config/config.yaml](./config/config.yaml):

```yaml
server:
  port: "8080"

monitor:
  port: "8081"

upstream:
  base_url: "https://api.openai.com"
  api_key: "sk-xxx"

debug:
  output_dir: "./logs"
  mask_key: false
```

Supported environment variable overrides:

- `LLM_TRACELAB_SERVER_PORT`
- `LLM_TRACELAB_MONITOR_PORT`
- `LLM_TRACELAB_UPSTREAM_BASE_URL`
- `LLM_TRACELAB_UPSTREAM_API_KEY`
- `LLM_TRACELAB_OUTPUT_DIR`
- `LLM_TRACELAB_MASK_KEY`

### 2. Build And Run

Using `go-task` is the recommended workflow:

```bash
task build
task run
task migrate
```

Direct run also works:

```bash
go run ./cmd/server -c config/config.yaml
```

Point your SDK `base_url` to `http://localhost:8080/v1` and traffic will be recorded through the proxy.

### 3. Open Monitor

Visit `http://localhost:8081`.

## Legacy Migration And SQLite Rebuild

Use the explicit migration command:

```bash
go run ./cmd/server migrate -c config/config.yaml
```

By default it does both:

- rewrites legacy `LLM_PROXY_V2` `.http` cassettes in place to `LLM_PROXY_V3`
- clears and rebuilds `trace_index.sqlite3`

Run only one part if needed:

```bash
go run ./cmd/server migrate -c config/config.yaml -rewrite-v2=false
go run ./cmd/server migrate -c config/config.yaml -rebuild-index=false
```

This is intended for bulk upgrades of old cassette directories and for full SQLite recovery.

## Docker And Compose

The standardized in-container paths are:

- binary: `/app/bin/llm-tracelab`
- config file: `/app/config/config.yaml`
- trace directory: `/app/data/traces`
- SQLite index: `/app/data/traces/trace_index.sqlite3`

The repo now includes:

- [Dockerfile](./Dockerfile)
- [docker-compose.yml](./docker-compose.yml)
- [config/config.docker.yaml](./config/config.docker.yaml)

Start it with:

```bash
export LLM_TRACELAB_UPSTREAM_API_KEY=sk-xxx
docker compose up --build
```

If you only want to use the published Docker Hub image, you can run it directly without cloning the repo:

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

If you prefer `docker compose`, you can also reference the Docker Hub image directly:

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

If the default Go module proxy is slow or blocked in your network, pass `GOPROXY` at build time:

```bash
GOPROXY=https://goproxy.cn,direct docker compose build
```

Default mounts:

- `./config/config.docker.yaml -> /app/config/config.yaml:ro`
- `./docker-data -> /app/data`

The runtime image starts as `root` by default. This avoids common bind-mount permission failures when the host directory owner does not match a fixed in-container UID/GID, such as failing to create `/app/data/traces`.

For external configuration, prefer mounting a config file and overriding runtime values through environment variables. Keep `debug.output_dir` on a stable path inside the mounted data volume.

## Developer Commands

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

## Replay In Unit Tests

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

## Design Rules

- raw `.http` cassettes are the source of truth for replay
- SQLite is a metadata index, not a replacement for raw files
- new writes use V3, old V2 files remain readable
- prefer readable files, offline tests, and local-first workflows

## Screenshots

- Monitor overview
  ![](./images/traffic_monitor.png)
- Detail page
  ![](./images/message_detail.png)
- Raw SSE stream
  ![](./images/sse_message_raw.png)
- Non-stream response
  ![](./images/message_raw.png)

## License

[MIT](./LICENSE)
