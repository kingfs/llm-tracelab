# PostgreSQL 长期运行优化 Runbook

本文档面向生产或准生产环境的 llm-tracelab application database 运维。目标是把长期运行优化做成可执行流程，而不是临时 SQL 调参。

可直接运行的基线采集 SQL 见 [PostgreSQL Baseline SQL](./POSTGRES_BASELINE_SQL.md)。

适用范围：

- Postgres production application DB。
- Monitor / MCP / CLI 依赖的结构化查询：trace index、sessions、Responses audit、analysis jobs、system events、routing/channel/model 数据。
- 只读诊断、索引和查询优化、派生 summary、回填、灰度读、分区/归档规划。

不适用范围：

- 不改变 `.http` cassette 作为 replay 和详情事实源的约束。
- 不把 SQLite 作为生产优化目标；SQLite 仍是 legacy/local/test fallback。
- 不在事故中用 `db migrate down` 作为生产回滚路径。生产回滚依赖备份恢复、流量回退或审阅过的手工计划。

## 基本原则

- 先基线，后改动。没有 `pg_stat_statements`、慢查询样本和行数/膨胀数据时，不创建“猜测型”索引。
- 所有生产 DDL 必须可审阅、可暂停、可回滚。大表索引默认使用 `CREATE INDEX CONCURRENTLY`，不要包在事务里。
- 优先优化读路径和派生 summary，避免让列表页、overview、sessions、audit 查询扫描 raw `.http` 文件。
- 回填必须批量、小步、可重入、可 dry-run，不能重写 cassette 文件。
- 灰度读先验证新索引或新 summary 的结果一致性，再把用户流量切到新路径。
- 分区和归档是容量治理手段，不是第一轮性能修复。只有在基线证明热表持续增长导致 vacuum、索引大小、查询延迟不可控时才启动。

## 0. 环境和权限

生产连接建议使用只读诊断用户和受控 DDL 用户分离：

```bash
export LLM_TRACELAB_DATABASE_DSN='postgres://...'
psql "$LLM_TRACELAB_DATABASE_DSN" -v ON_ERROR_STOP=1
```

上线前确认：

```bash
llm-tracelab -c config/config.yaml db migrate status --check-db
llm-tracelab -c config/config.yaml auth migrate status --check-db
```

`db migrate status --check-db` 和 `auth migrate status --check-db` 应报告共享 application `schema_migrations` 状态健康且非 dirty。若 schema 处于 dirty 状态，先处理迁移一致性，不进入优化流程。

## 1. pg_stat_statements 基线

### 启用要求

Postgres 实例需要加载扩展：

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

如果托管数据库要求在参数组中配置 `shared_preload_libraries = 'pg_stat_statements'`，需要按托管平台流程重启或滚动重启数据库。不要在没有变更窗口的情况下临时重启生产库。

建议参数：

```text
pg_stat_statements.track = all
pg_stat_statements.max = 10000
track_io_timing = on
```

### 采样窗口

基线至少覆盖一个完整业务峰谷周期。低流量环境建议采样 24 小时；高流量环境至少采样 2 小时峰值窗口。

采样开始时记录：

```sql
SELECT now() AS baseline_started_at;
SELECT pg_stat_statements_reset();
```

采样结束导出 top SQL：

```sql
SELECT
  calls,
  round(total_exec_time::numeric, 2) AS total_ms,
  round(mean_exec_time::numeric, 2) AS mean_ms,
  round(max_exec_time::numeric, 2) AS max_ms,
  rows,
  shared_blks_hit,
  shared_blks_read,
  temp_blks_written,
  query
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 30;
```

重点关注：

- `total_exec_time` 高：整体成本高，常见于列表、overview、sessions 聚合。
- `mean_exec_time` 或 `max_exec_time` 高：用户可感知慢查询或偶发执行计划问题。
- `shared_blks_read` 高：缓存命中不足或索引不匹配。
- `temp_blks_written` 高：排序、聚合或 hash 溢出，需要检查 work_mem、索引顺序或 summary 设计。
- `rows/calls` 远高于页面大小：候选过滤条件或分页策略有问题。

### TraceLab 热表优先级

优先观察这些表：

- `logs`：trace list、session list、overview、model/provider/time filters。
- `request_audits`、`execution_events`、`upstream_exchanges`、`tool_call_audits`：Responses audit 和 exchange correlation。
- `analysis_jobs`、`analysis_runs`、`trace_observations`、`trace_findings`、`system_events`：重分析、findings、Monitor summary。
- `channel_configs`、`channel_models`、`model_catalog`、`model_aliases`：routing/model 管理读路径。

## 2. Concurrent Indexes

### 何时加索引

满足以下条件才加索引：

- `pg_stat_statements` 中有明确慢 SQL 或高成本 SQL。
- `EXPLAIN (ANALYZE, BUFFERS)` 证明现有索引没有覆盖过滤、排序或 join 条件。
- 候选索引匹配稳定产品查询，而不是一次性排障查询。
- 已评估写入放大、索引大小和 vacuum 成本。

### 索引设计清单

- 时间列表查询通常需要 `(filter_columns..., recorded_at DESC)` 或 `(filter_columns..., created_at DESC)`。
- 只查询非空 session / response / request id 时，优先考虑 partial index。
- 不为低选择性布尔字段单独建索引；需要和时间或状态字段组合。
- JSON/JSONB 字段只有在产品查询稳定后才加表达式或 GIN 索引。
- 新索引名称应和现有命名风格一致，例如 `tracelog_*`、`requestaudit_*`、`executionevent_*`。

### 生产 DDL 模板

llm-tracelab 内置的当前安全索引优化入口是：

```bash
llm-tracelab -c config/config.yaml db migrate optimize-indexes --dry-run
llm-tracelab -c config/config.yaml db migrate optimize-indexes
```

该命令逐条执行非事务 `CREATE INDEX CONCURRENTLY IF NOT EXISTS`，用于当前已知的 `logs` 热点查询。上线前先运行 `--dry-run` 审阅语句；只有在基线和变更窗口确认后再执行实际命令。

大表索引默认使用：

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS tracelog_provider_recorded_at
ON logs (provider, recorded_at DESC);
```

partial index 示例：

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS tracelog_session_nonempty_recorded_at
ON logs (session_id, recorded_at DESC)
WHERE session_id <> '';
```

注意：

- `CREATE INDEX CONCURRENTLY` 不能在事务中执行。
- 如果失败留下 invalid index，先确认没有查询依赖，再清理：

```sql
DROP INDEX CONCURRENTLY IF EXISTS tracelog_provider_recorded_at;
```

- 创建后执行：

```sql
ANALYZE logs;
```

### 索引验证

上线后确认索引被使用：

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT trace_id, recorded_at, model, provider, status_code
FROM logs
WHERE provider = 'openai' AND recorded_at >= now() - interval '24 hours'
ORDER BY recorded_at DESC
LIMIT 50;
```

同时导出现有索引清单：

```sql
SELECT
  schemaname,
  tablename,
  indexname
FROM pg_indexes
WHERE schemaname = 'public'
ORDER BY tablename, indexname;
```

若要检查 invalid index，需要查询 `pg_index.indisvalid`：

```sql
SELECT
  c.relname AS index_name,
  t.relname AS table_name,
  i.indisvalid,
  i.indisready
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_class t ON t.oid = i.indrelid
WHERE NOT i.indisvalid OR NOT i.indisready;
```

## 3. Query Tuning

### 分析步骤

1. 从 `pg_stat_statements` 取 queryid、归一化 SQL、calls、mean/max latency。
2. 用真实参数重放 `EXPLAIN (ANALYZE, BUFFERS, VERBOSE)`。
3. 判断慢点是 filter、sort、join、aggregate、offset pagination、JSON 解析还是数据倾斜。
4. 先尝试查询形态优化，再决定是否加索引或 summary。

### TraceLab 常见优化方向

- Trace list：避免深 `OFFSET`；优先使用 `(recorded_at, trace_id/path)` seek pagination。
- Session list：避免每次从 `logs` 全量 group by；当 session 数量大时，进入 session summary 方案。
- Overview：多指标聚合如果反复扫描 `logs`、`trace_findings`、`system_events`，改为按时间窗口派生 summary。
- Audit trace：按 `response_id`、`request_audit_id`、`conversation_id`、`trace_id` 明确走窄索引，避免在 audit 表上做模糊搜索。
- Analysis jobs：worker 取任务应稳定使用 `(status, updated_at)` 或等价索引，并限制批量大小。
- JSONB 查询：除非用户界面稳定需要，不要直接把 raw payload JSON 查询变成热路径。

### 验收阈值

每个 query tuning 变更都需要记录：

- 变更前后 `EXPLAIN`。
- p50 / p95 / p99 latency。
- rows scanned、shared blocks read/hit、temp blocks。
- 对写入 TPS 和 autovacuum 的影响。

若优化后 p95 改善小于 20%，或写入成本明显上升，应重新评估是否值得保留。

## 4. Session Summary

### 触发条件

当 `/api/sessions`、session detail、session reanalysis 相关查询出现以下情况，启动 session summary 设计：

- session 列表依赖 `logs GROUP BY session_id`，并在生产数据量下成为 top SQL。
- session count、latest trace、models、tokens、duration、failure counts 每次实时聚合成本高。
- 用户主要读取最近活跃 session，而历史 session 很少变化。

### 推荐模型

新增派生 read model，而不是改变 raw trace index：

```text
session_summaries
- session_id primary key
- first_recorded_at
- last_recorded_at
- trace_count
- error_count
- stream_count
- total_tokens
- prompt_tokens
- completion_tokens
- model_count
- provider_count
- latest_trace_id
- latest_status_code
- updated_at
- source_version
```

约束：

- `logs` 仍是 trace index 事实源。
- summary 可以删除重建。
- summary 更新必须可重入，并能按 session 或时间窗口局部回填。
- Monitor 读路径灰度切换前必须能对比 `logs` 实时聚合结果。

### 回填和一致性验证

先 dry-run：

```sql
SELECT
  count(DISTINCT session_id) AS sessions,
  count(*) AS traces
FROM logs
WHERE session_id <> '';
```

抽样一致性：

```sql
SELECT session_id
FROM logs
WHERE session_id <> ''
GROUP BY session_id
ORDER BY max(recorded_at) DESC
LIMIT 20;
```

对每个抽样 session 比较：

- `trace_count`
- `first_recorded_at`
- `last_recorded_at`
- `total_tokens`
- latest trace id
- failure/error count

验收通过后再让 session list 读 summary。

## 5. Backfill

### 已有入口

项目已有 exchange metadata 回填入口：

```bash
llm-tracelab -c config/config.yaml analyze backfill-exchanges --dry-run
llm-tracelab -c config/config.yaml analyze backfill-exchanges
```

该入口用于补齐 exchange metadata 索引。它不能重写 raw `.http` cassette。

### 新回填任务规范

新增任何 backfill 前，文档和实现设计必须说明：

- 输入事实源：Postgres table、raw `.http` cassette、还是两者组合。
- 输出表和字段。
- 是否可 dry-run。
- 批量大小、排序键、断点续跑策略。
- 幂等键和冲突处理。
- 速率限制、锁等待超时和 statement timeout。
- 验收 SQL 和回滚方式。

推荐运行参数：

```sql
SET lock_timeout = '2s';
SET statement_timeout = '30s';
```

批处理推荐按稳定键或时间窗口推进：

```sql
SELECT path, trace_id
FROM logs
WHERE recorded_at >= $1 AND recorded_at < $2
ORDER BY recorded_at, path
LIMIT 1000;
```

### Backfill 回滚

优先采用字段级或 summary 级回滚：

- 新增字段：记录变更前为空的范围，必要时按条件置回默认值。
- 新增 summary 表：可 truncate 或 drop summary，再从事实源重建。
- 新增索引：`DROP INDEX CONCURRENTLY`。

禁止把 `db migrate down` 当作普通 backfill 回滚。

## 6. 灰度读

灰度读用于把新 summary、新索引依赖查询或新查询形态逐步接入用户流量。

### 阶段

1. Shadow read：主路径仍读旧查询，新路径只在后台执行并记录差异。
2. Operator canary：只给内部 operator 或单实例启用新读路径。
3. Low percentage：小比例流量读新路径，保留旧路径 fallback。
4. Full read：默认读新路径，继续保留快速回退开关至少一个发布周期。

### 对比指标

- 行数是否一致。
- 排序是否一致，尤其是 `recorded_at DESC`、`created_at DESC` 和 tie-breaker。
- 聚合值是否一致。
- p95/p99 是否下降。
- Postgres CPU、IO、temp files、lock waits 是否稳定。
- 应用层错误率和 Monitor API 5xx 是否无回归。

### 回退

灰度读回退必须是配置或发布级开关，不依赖 DDL rollback。索引和 summary 表可以先保留，等确认不再使用后再清理。

## 7. 分区和归档

### 触发条件

只有出现以下长期信号时才启动分区/归档设计：

- `logs`、`execution_events`、`upstream_exchanges`、`tool_call_audits` 等 append-heavy 表持续增长，单表和索引膨胀影响 vacuum 或备份窗口。
- 热查询几乎都按时间窗口读取，历史数据读取频率低。
- 删除历史数据或搬迁历史数据造成长事务、锁等待或大量 dead tuples。
- 备份恢复时间目标要求缩小热数据集。

### 候选表

优先评估：

- `logs`：按 `recorded_at` 月/周分区。
- `request_audits`：按 `created_at` 月/周分区。
- `execution_events`：按 `occurred_at` 月/周分区。
- `upstream_exchanges`：按 `started_at` 月/周分区。
- `tool_call_audits`：按 `created_at` 月/周分区。
- `system_events` 通常不优先分区，因为它按 fingerprint 合并更新，不是纯 append-only。

### 设计要求

- 分区键必须出现在热查询过滤条件中。
- 所有唯一约束必须满足 Postgres 分区约束规则；不能破坏现有 primary key 和 replay 查找。
- 应用查询必须继续支持跨分区读取。
- 归档不能删除 raw `.http` cassette，除非另有明确的数据保留策略和 replay 兼容方案。
- 分区迁移必须单独设计，不应夹在普通 schema migration 里顺手完成。

### 归档策略

推荐分层：

- Hot：最近 30 到 90 天，完整索引，支持 Monitor 高频查询。
- Warm：保留 Postgres 分区，降低索引数量，只支持低频查询。
- Cold：导出到对象存储或归档库，Postgres 仅保留最小定位 metadata。

归档前必须明确：

- 用户是否还能在 Monitor 查询历史 trace。
- replay 是否仍可从 `.http` cassette 工作。
- MCP 是否需要访问历史 metadata。
- 恢复一个归档窗口需要多久。

## Operator Checklist

### 上线前

- [ ] 已确认目标环境是 Postgres production DB，不是 SQLite fallback。
- [ ] `db migrate status --check-db` 非 dirty，migration version 符合预期。
- [ ] 已完成 `pg_stat_statements` 基线采样并保存 top SQL。
- [ ] 已保存相关查询的 `EXPLAIN (ANALYZE, BUFFERS)`。
- [ ] 新索引、summary 或回填方案有审阅过的 SQL/步骤。
- [ ] 大表 DDL 使用 `CONCURRENTLY`，且不在事务中执行。
- [ ] 已设置 `lock_timeout`、`statement_timeout` 和批量大小。
- [ ] 已确认备份、PITR 或快照可用，并记录恢复点。
- [ ] 已准备回退开关或旧读路径 fallback。
- [ ] 已定义验收指标：latency、错误率、锁等待、CPU/IO、结果一致性。

### 上线中

- [ ] 先执行 read-only 检查和 dry-run。
- [ ] DDL 逐条执行；每条完成后检查 invalid index、锁等待和错误日志。
- [ ] 回填按小批次推进，记录 scanned、updated、conflicts、skipped、duration。
- [ ] 灰度读从 shadow/operator canary 开始，不直接全量切换。
- [ ] 持续观察 Monitor API p95/p99、Postgres CPU/IO、temp files、deadlocks、lock waits。
- [ ] 发现 statement timeout、lock timeout 或结果差异时暂停推进。

### 回滚

- [ ] 读路径问题：先关闭灰度开关，回到旧查询。
- [ ] 新 summary 问题：停止写入/读取 summary，保留表用于排查；必要时 truncate 后重建。
- [ ] 新索引问题：确认未被依赖后执行 `DROP INDEX CONCURRENTLY IF EXISTS ...`。
- [ ] 回填问题：按记录的范围和幂等键撤销派生字段或重建 summary。
- [ ] schema dirty 或数据破坏：停止写入，按备份/PITR/审阅过的手工计划恢复。
- [ ] 不使用 `db migrate down` 作为生产快速回滚。

### 验收指标

- [ ] 目标 Monitor/API 查询 p95 至少下降 20%，或达到预先定义的 SLO。
- [ ] p99 无显著回归。
- [ ] 结果一致性抽样通过，排序和分页无重复/漏行。
- [ ] `pg_stat_statements` 中目标 query 的 total/mean/max execution time 下降。
- [ ] 无新增 deadlock、长时间 lock wait、invalid index。
- [ ] Postgres CPU、IO、WAL、autovacuum 压力在可接受范围。
- [ ] 应用错误率、MCP 查询错误、analysis job backlog 无回归。
- [ ] runbook 记录了实际执行时间、SQL、操作者、指标截图或导出结果。

## 文档和验证要求

涉及长期运行 Postgres 优化的 PR 至少更新：

- 本 runbook 中对应流程或 checklist。
- [维护基线](./MAINTAINER_BASELINE.md) 中的存储/运维约束，如果改变长期约束。
- [开发命令](./DEVELOPMENT_COMMANDS.md) 中的验证入口，如果新增稳定命令。

文档-only 变更至少执行：

```bash
git diff --check
```

如果新增 migration SQL、回填命令或查询路径，再按风险补充：

```bash
task check:quick
go test ./internal/store ./internal/monitor
```
