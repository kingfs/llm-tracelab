# v1 存储与管道设计

## 目标

v1 存储设计要同时满足：

- raw 事实保留。
- replay 稳定。
- 列表查询快速。
- 语义解析可重算。
- 审计 findings 可追踪。
- 后续支持长期分析和报表。

## 存储层次

```text
Raw cassette
  -> lightweight trace index
  -> observation tables
  -> finding tables
  -> analysis run tables
```

## Raw Cassette

继续使用 `LLM_PROXY_V3`。

职责：

- 保存 raw request。
- 保存 raw response。
- 保存 prelude meta。
- 保存基础 timeline event。

不建议在 v1 第一阶段修改 cassette 主格式。

如果必须扩展，优先扩展 `# event:` 和 `# meta:`，不要破坏已有 parser。

## SQLite 轻量索引

当前 SQLite 已经承担 trace list、session、upstream、routing、token 等基础查询。

v1 保持这个职责，但不要把大规模语义节点直接塞进现有 trace 表。

## 新增派生表

### trace_observations

```text
trace_id
parser
parser_version
status
provider
operation
model
summary_json
warnings_json
created_at
updated_at
```

### semantic_nodes

```text
id
trace_id
node_id
parent_node_id
provider_type
normalized_type
role
path
index
text_preview
json
raw_ref
created_at
```

### trace_findings

```text
id
trace_id
finding_id
category
severity
confidence
title
description
evidence_path
evidence_excerpt
node_id
detector
detector_version
created_at
```

### analysis_runs

```text
id
trace_id
session_id
kind
analyzer
analyzer_version
model
input_ref
output_json
status
created_at
```

### parser_versions

```text
parser
version
description
created_at
```

## 队列设计

第一版可以不引入外部队列。

建议使用 SQLite 表或内存 worker + 启动扫描：

```text
parse_jobs
  id
  trace_id
  status
  attempts
  last_error
  created_at
  updated_at
```

后续如需生产化，可替换为独立队列。

## 管道

### Recording Pipeline

```text
proxy captures bytes
  -> cassette writer
  -> trace index
  -> enqueue parse job
```

要求：

- enqueue parse job 失败不能导致请求失败。
- cassette 写入失败必须可观测。

### Parse Pipeline

```text
parse job
  -> load cassette
  -> extract request/response
  -> provider parser
  -> TraceObservation
  -> semantic_nodes
  -> enqueue analysis job
```

### Analysis Pipeline

```text
analysis job
  -> load TraceObservation
  -> detectors
  -> trace_findings
  -> optional LLM analysis
```

## 重算机制

需要支持：

- 单 trace reparse。
- 单 session reparse。
- 按 parser version 批量 reparse。
- 按 detector version 批量 reanalyze。

CLI 示例：

```bash
llm-tracelab analyze reparse --trace-id xxx
llm-tracelab analyze reparse --parser openai-responses --since-version 0.1.0
llm-tracelab analyze scan --detector dangerous-shell
```

## 数据体积策略

大字段处理原则：

- raw body 不复制到 semantic_nodes。
- semantic_nodes 存 text preview 和必要 JSON。
- 大 blob 留在 cassette 或 sidecar。
- 多模态数据只索引 metadata。

## 迁移策略

- 所有 schema 变更 additive。
- 现有 DB 启动升级必须成功。
- 不自动重写 cassette。
- 派生表可以清空重建。

## 故障处理

### parse failed

记录：

- trace_id。
- parser。
- version。
- error。
- raw cassette path。

Monitor 显示：

- Raw 可用。
- Protocol 不可用。
- Audit 可能未运行。

### analysis failed

记录：

- detector/analyzer。
- version。
- error。

Monitor 显示：

- Protocol 可用。
- Audit 部分可用。

## 测试要求

- 旧 cassette 仍可 replay。
- 没有 observation 表时 monitor 基础功能可用。
- parse job 失败不影响 trace list。
- reparse 结果幂等。
- semantic_nodes 能通过 trace_id 清理并重建。
