# 维护基线

本文档记录维护者修改核心模块时必须遵守的当前约束。

适用范围：

- `internal/store`
- `internal/monitor`
- `internal/recorder`
- `internal/proxy`
- `internal/router`
- `pkg/recordfile`
- `pkg/replay`
- `pkg/llm`
- `pkg/observe`

## 事实源边界

raw `.http` cassette 是：

- replay 事实源。
- raw protocol 详情源。
- Observation IR / findings / usage repair 的重建来源。

Application DB 是结构化查询源。生产部署必须使用 Postgres；SQLite 只保留为
legacy/dev/test 兼容 fallback。

- 列表、聚合、过滤、分页的查询源。
- auth、channel/model 配置、system events、analysis jobs、Observation IR、findings、eval 的结构化源。

不要让列表页依赖扫描 raw 文件。

不要让 replay 依赖 SQLite 或网络。

## Record Format

当前写入格式：

- `LLM_PROXY_V3`

读取兼容：

- `LLM_PROXY_V2`
- `LLM_PROXY_V3`

改 record format 时：

1. 先改 `pkg/recordfile`。
2. 同步 recorder、monitor、replay。
3. 保持 `.http` 人类可读。
4. 保持旧 cassette 可读，除非明确做 breaking migration。

## Storage 升级

Postgres 是生产迁移主路径：

- `db migrate up` 使用 checked-in `ent/postgres-migrations`。
- `db migrate down` 不作为 CLI 生产回滚路径；需要 backup restore 或审阅过的手工迁移计划。
- auth 表当前由 application Postgres migration set 拥有；`auth migrate down` 不得回滚共享 application schema。

SQLite schema 只能按兼容 fallback 演进。

schema 演进必须 additive。

规则：

- 新列必须通过启动时 `ensureColumn` 或等价迁移兼容旧 DB。
- 查询或索引依赖新列前，必须保证列已存在。
- 旧本地 `trace_index.sqlite3` / 当前 SQLite 文件必须可原地升级。

## Channel 与模型配置

长期配置源是 application DB；生产为 Postgres，SQLite 仅用于 legacy/dev/test：

- `channel_configs`
- `channel_models`
- `model_catalog`
- `channel_probe_runs`

YAML `upstream` / `upstreams` 是兼容 bootstrap 输入。

修改渠道管理时必须保持：

- DB 优先。
- legacy YAML 可首次导入。
- API key 和敏感 header 本地加密。
- channel/model 启停能 reload router。

## 协议边界

当前代理不做跨协议转换。

修改 `pkg/llm` 或 `pkg/observe` 时要区分：

- 协议识别。
- usage/timeline 抽取。
- Observation IR 解析。
- 请求转发。

前三者可以跨 provider 归一化；请求转发必须保持 raw/replay 安全，不能隐式改变协议语义。

## Monitor API

当前稳定 API 族：

- `/api/overview`
- `/api/traces`
- `/api/sessions`
- `/api/models`
- `/api/channels`
- `/api/routing/summary`
- `/api/events`
- `/api/findings`
- `/api/analysis`
- `/api/upstreams`
- `/api/auth/*`

修改这些接口要视为产品行为变化，并同步更新文档和测试。

## MCP

MCP 当前是受限工具面：

- read-only 查询为主。
- reanalysis 工具只创建/执行本地 `analysis_jobs`。
- 不调用上游 provider。
- 不暴露 raw secret。
- 复用 Monitor/store 查询语义。

不要为 MCP 建立另一套不一致的事实模型。

## System Events

system events 是 TraceLab 自身运行和派生管道异常，不是普通用户请求失败列表。

事件来源包括：

- parser failure。
- analyzer failure。
- router selection failure。
- upstream transport error。

维护要求：

- 事件 details 不保存 raw request/response body。
- fingerprint 要能合并重复事件。
- ignored 事件保持 ignored。
- read/resolved 事件复发时重新 unread。

## Reanalysis

reanalysis 只基于本地 cassette 和 SQLite 派生状态工作。

任务必须写入 `analysis_jobs`，便于审计：

- trace reparse。
- trace rescan。
- trace repair usage。
- trace reanalyze。
- session reanalyze。
- batch reanalyze。

## 测试基线

常用验证：

```bash
task check:quick
go test ./pkg/recordfile ./pkg/replay ./pkg/llm ./pkg/observe
go test ./internal/proxy ./internal/router ./internal/store ./internal/monitor
```

改 streaming、router、store 并发时补：

```bash
task test:race
```

前端改动补：

```bash
task ui:build
task ui:test
go test ./internal/monitor
```
