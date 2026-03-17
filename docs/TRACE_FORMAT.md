# TRACE_FORMAT.md

## 文档目的

本文档定义 `llm-tracelab` 当前使用的 `.http` 工件格式。

该格式的设计目标是：

- 人类可读
- 程序可解析
- 便于 seek 定位与回放
- 能作为运行时、monitor 与测试之间共享的稳定工件

## 文件结构

每个工件都是一个单独的 `.http` 文件，其二进制布局如下：

```text
[固定头部块：2048 字节]
[请求头字节]
[请求体字节]
[\n]
[响应头字节]
[响应体字节]
```

## 固定头部块

文件前 `2048` 字节预留给 JSON 头部及结尾换行。

规则如下：

- 在请求转发前预分配这些字节
- 未使用字节以空格填充
- 块的最后一个字节始终为 `\n`
- 请求完成后会把真实 JSON 回写到该块中

这样 recorder 就可以在响应持续写入的同时，最后再回填元数据。

## 头部 Schema

头部块保存的是一个序列化后的 `RecordHeader`。

顶层字段包括：

- `version`
- `meta`
- `layout`
- `usage`

### version

当前值：

- `LLM_PROXY_V2`

### meta

描述请求生命周期的元数据。

当前字段：

- `request_id`
- `time`
- `model`
- `url`
- `method`
- `status_code`
- `duration_ms`
- `ttft_ms`
- `client_ip`
- `content_length`
- `error`

### layout

用于重建文件片段的字节长度信息。

当前字段：

- `req_header_len`
- `req_body_len`
- `res_header_len`
- `res_body_len`
- `is_stream`

### usage

从 upstream 响应中提取的 token usage。

当前字段：

- `prompt_tokens`
- `completion_tokens`
- `total_tokens`
- `prompt_tokens_details.cached_tokens`

## 请求段

请求段包含两部分：

1. 由 `httputil.DumpRequest(req, false)` 生成的请求行和请求头
2. 原始请求体字节

如果开启 masking，录制时会暂时替换 authorization 头，之后再恢复内存中的原值。

## 响应段

响应段从请求体后的单个换行分隔符之后开始。

其内容包括：

1. HTTP 状态行和响应头
2. 原始响应体字节

对于流式响应，响应体按线上真实传输负载写入，不会被重组为单一 JSON 对象。

## 流式处理

如果一个请求被识别为流式 JSON，代理可能会注入：

```json
{
  "stream_options": {
    "include_usage": true
  }
}
```

这样做是为了提高 upstream 在 stream 中返回 usage 元数据的概率。

usage 提取规则：

- stream 响应按行扫描 SSE `data:` 负载中是否包含 `usage`
- non-stream 响应从尾部缓冲区扫描最后一个 `usage` 对象

## 回放契约

replay 依赖以下假设：

- 前 2048 字节中存在可读的 JSON 头部块
- layout 中的长度信息是正确的
- 响应段起始位置为：

```text
2048 + req_header_len + req_body_len + 1
```

如果这些假设发生变化，必须同步更新：

- recorder
- replay transport
- monitor parser
- tests
- docs

## 兼容性规则

### 向后兼容修改

通常是安全的：

- 在 `meta` 中增加新字段
- 在 `usage` 中增加新字段
- 增加现有 reader 可忽略的 provider 元数据

### 可能破坏兼容的修改

需要联动更新：

- 修改头部块大小
- 修改响应分隔符语义
- 修改请求/响应片段顺序
- 修改流式 body 存储方式
- 修改 parser 或 replay 依赖的字段名

## 运行说明

- 工件存储在 `debug.output_dir` 下
- 当前路径布局包含 upstream host、model 与日期分区
- 当前格式针对本地文件系统使用进行了优化
- 读取方应尽量防御性处理损坏或不完整工件

## 未来扩展方向

后续可能增加：

- schema version 演进策略
- request fingerprint
- provider 名称元数据
- 面向 harness 的 normalized summary block
- 可选的 sidecar run metadata

在那之前，`.http` 文件仍是当前系统的事实来源 trace 工件。
