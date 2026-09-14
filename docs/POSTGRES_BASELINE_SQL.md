# PostgreSQL Baseline SQL

本页提供长期运行优化前后的只读基线采集 SQL。除最后明确标注的 `pg_stat_statements_reset()` 外，其余语句都应只读执行。

也可以直接把脚本复制到服务器执行，并将所有输出写入一个文件：

```bash
LLM_TRACELAB_DATABASE_DSN='postgres://user:pass@host/db?sslmode=require' \
  BASELINE_WINDOW='7 days' \
  scripts/postgres-baseline.sh /tmp/tracelab-postgres-baseline.txt
```

建议优先保存这些输出：`pg_stat_statements` Top SQL、表/索引大小、vacuum 状态、invalid index、lock wait，以及 trace list、session list、overview、system events 的 `EXPLAIN (ANALYZE, BUFFERS)`。

将 `interval '7 days'` 替换成实际采样窗口，例如 `interval '24 hours'`。

## 环境状态

```sql
SELECT version();

SELECT extname, extversion
FROM pg_extension
WHERE extname IN ('pg_stat_statements', 'pgstattuple');

SELECT name, setting, unit, source
FROM pg_settings
WHERE name IN (
  'shared_preload_libraries',
  'track_io_timing',
  'pg_stat_statements.track',
  'pg_stat_statements.max',
  'autovacuum',
  'autovacuum_vacuum_scale_factor',
  'autovacuum_analyze_scale_factor'
)
ORDER BY name;
```

## Table Size

```sql
SELECT
  n.nspname AS schema_name,
  c.relname AS table_name,
  c.reltuples::bigint AS estimated_rows,
  pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size,
  pg_size_pretty(pg_relation_size(c.oid)) AS table_size,
  pg_size_pretty(pg_indexes_size(c.oid)) AS indexes_size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r'
  AND n.nspname = current_schema()
  AND c.relname IN (
    'logs',
    'session_summaries',
    'overview_metric_buckets',
    'overview_metric_bucket_members',
    'trace_observations',
    'parse_jobs',
    'system_events',
    'analysis_runs',
    'trace_findings',
    'request_audits',
    'execution_events',
    'upstream_exchanges',
    'tool_call_audits',
    'analysis_jobs',
    'app_settings',
    'channel_configs',
    'channel_models',
    'model_catalog',
    'model_aliases'
  )
ORDER BY pg_total_relation_size(c.oid) DESC;
```

## Vacuum And Dead Tuples

```sql
SELECT
  relname,
  n_live_tup,
  n_dead_tup,
  ROUND(100.0 * n_dead_tup / NULLIF(n_live_tup + n_dead_tup, 0), 2) AS dead_pct,
  last_vacuum,
  last_autovacuum,
  last_analyze,
  last_autoanalyze,
  vacuum_count,
  autovacuum_count,
  analyze_count,
  autoanalyze_count
FROM pg_stat_user_tables
WHERE relname IN (
  'logs',
  'session_summaries',
  'overview_metric_buckets',
  'overview_metric_bucket_members',
  'trace_observations',
  'parse_jobs',
  'system_events',
  'analysis_runs',
  'trace_findings',
  'request_audits',
  'execution_events',
  'upstream_exchanges',
  'tool_call_audits',
  'analysis_jobs',
  'app_settings',
  'channel_configs',
  'channel_models',
  'model_catalog',
  'model_aliases'
)
ORDER BY n_dead_tup DESC;
```

## Index Usage

```sql
SELECT
  s.relname AS table_name,
  s.indexrelname AS index_name,
  s.idx_scan,
  s.idx_tup_read,
  s.idx_tup_fetch,
  pg_size_pretty(pg_relation_size(i.indexrelid)) AS index_size,
  pg_get_indexdef(i.indexrelid) AS index_def
FROM pg_stat_user_indexes s
JOIN pg_index i ON i.indexrelid = s.indexrelid
WHERE s.relname IN (
  'logs',
  'session_summaries',
  'overview_metric_buckets',
  'overview_metric_bucket_members',
  'trace_observations',
  'parse_jobs',
  'system_events',
  'analysis_runs',
  'trace_findings',
  'tool_call_audits',
  'analysis_jobs',
  'app_settings',
  'channel_configs',
  'channel_models',
  'model_catalog',
  'model_aliases'
)
ORDER BY pg_relation_size(i.indexrelid) DESC, s.idx_scan ASC;
```

## Invalid Indexes

```sql
SELECT
  n.nspname AS schema_name,
  c.relname AS index_name,
  t.relname AS table_name,
  i.indisvalid,
  i.indisready,
  pg_size_pretty(pg_relation_size(c.oid)) AS index_size,
  pg_get_indexdef(c.oid) AS index_def
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema()
  AND (NOT i.indisvalid OR NOT i.indisready)
ORDER BY pg_relation_size(c.oid) DESC;
```

## pg_stat_statements

总耗时最高：

```sql
SELECT
  queryid,
  calls,
  ROUND(total_exec_time::numeric, 2) AS total_exec_ms,
  ROUND(mean_exec_time::numeric, 2) AS mean_exec_ms,
  ROUND(max_exec_time::numeric, 2) AS max_exec_ms,
  rows,
  shared_blks_hit,
  shared_blks_read,
  shared_blks_dirtied,
  temp_blks_read,
  temp_blks_written,
  LEFT(query, 500) AS query_sample
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 30;
```

平均耗时最高，过滤低频噪声：

```sql
SELECT
  queryid,
  calls,
  ROUND(mean_exec_time::numeric, 2) AS mean_exec_ms,
  ROUND(max_exec_time::numeric, 2) AS max_exec_ms,
  ROUND(total_exec_time::numeric, 2) AS total_exec_ms,
  rows,
  LEFT(query, 500) AS query_sample
FROM pg_stat_statements
WHERE calls >= 10
  AND dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY mean_exec_time DESC
LIMIT 30;
```

## Locks And Long Transactions

```sql
SELECT
  a.pid,
  a.usename,
  a.application_name,
  a.state,
  now() - a.xact_start AS xact_age,
  now() - a.query_start AS query_age,
  a.wait_event_type,
  a.wait_event,
  LEFT(a.query, 500) AS query_sample
FROM pg_stat_activity a
WHERE a.datname = current_database()
  AND (
    a.wait_event IS NOT NULL
    OR (a.xact_start IS NOT NULL AND now() - a.xact_start > interval '5 minutes')
    OR (a.query_start IS NOT NULL AND now() - a.query_start > interval '30 seconds')
  )
ORDER BY COALESCE(a.xact_start, a.query_start) ASC NULLS LAST;
```

## Data Distribution

```sql
SELECT
  COUNT(*) AS total_logs,
  COUNT(*) FILTER (WHERE COALESCE(exchange_kind, '') IN ('', 'entry', 'proxy')) AS client_visible_logs,
  COUNT(DISTINCT session_id) FILTER (WHERE session_id <> '') AS sessions,
  MIN(recorded_at) AS first_recorded_at,
  MAX(recorded_at) AS last_recorded_at
FROM logs;
```

```sql
SELECT
  date_trunc('hour', recorded_at) AS hour,
  COUNT(*) AS request_count,
  COUNT(*) FILTER (WHERE status_code BETWEEN 200 AND 299 AND error_text = '') AS success_count,
  COUNT(*) FILTER (WHERE status_code < 200 OR status_code >= 300 OR error_text <> '') AS failure_count,
  SUM(total_tokens) AS total_tokens,
  ROUND(AVG(NULLIF(ttft_ms, 0))::numeric, 2) AS avg_ttft_ms,
  ROUND(AVG(NULLIF(duration_ms, 0))::numeric, 2) AS avg_duration_ms
FROM logs
WHERE recorded_at >= now() - interval '7 days'
  AND COALESCE(exchange_kind, '') IN ('', 'entry', 'proxy')
GROUP BY 1
ORDER BY 1 DESC
LIMIT 168;
```

## Summary Coverage

```sql
SELECT
  (SELECT COUNT(DISTINCT session_id) FROM logs WHERE session_id <> '' AND COALESCE(exchange_kind, '') IN ('', 'entry', 'proxy')) AS log_sessions,
  (SELECT COUNT(*) FROM session_summaries) AS summary_sessions,
  (SELECT MAX(updated_at) FROM session_summaries) AS summary_max_updated_at,
  (SELECT MIN(updated_at) FROM session_summaries) AS summary_min_updated_at;
```

```sql
SELECT
  COUNT(*) AS bucket_count,
  MIN(bucket_start) AS first_bucket,
  MAX(bucket_start) AS last_bucket,
  SUM(request_count) AS bucket_requests,
  SUM(success_request) AS bucket_success,
  SUM(failed_request) AS bucket_failed,
  SUM(total_tokens) AS bucket_tokens
FROM overview_metric_buckets;
```

```sql
SELECT
  COUNT(*) AS member_count,
  MIN(updated_at) AS first_member_update,
  MAX(updated_at) AS last_member_update
FROM overview_metric_bucket_members;
```

## Backlog And Events

```sql
SELECT
  'trace_observations' AS source,
  status,
  COUNT(*) AS count
FROM trace_observations
GROUP BY status
UNION ALL
SELECT
  'parse_jobs' AS source,
  status,
  COUNT(*) AS count
FROM parse_jobs
GROUP BY status
ORDER BY source, count DESC;
```

```sql
SELECT
  status,
  severity,
  source,
  category,
  COUNT(*) AS count,
  MAX(last_seen_at) AS newest
FROM system_events
GROUP BY status, severity, source, category
ORDER BY count DESC, newest DESC
LIMIT 50;
```

## EXPLAIN Templates

Trace list 最新页：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT
  trace_id, path, recorded_at, model, provider, operation, endpoint, status_code,
  duration_ms, ttft_ms, total_tokens, session_id
FROM logs
WHERE COALESCE(exchange_kind, '') IN ('', 'entry', 'proxy')
ORDER BY recorded_at DESC, trace_id DESC
LIMIT 50;
```

Session 列表第一页 session id 阶段：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT s.session_id
FROM logs s
WHERE s.session_id <> ''
  AND COALESCE(s.exchange_kind, '') IN ('', 'entry', 'proxy')
GROUP BY s.session_id
ORDER BY MAX(s.recorded_at) DESC
LIMIT 50 OFFSET 0;
```

Session 当前页聚合阶段，将 `VALUES` 中的 session id 替换成上一条返回值：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT
  s.session_id,
  COUNT(*) AS request_count,
  MIN(s.recorded_at) AS first_seen,
  MAX(s.recorded_at) AS last_seen,
  SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS success_request,
  SUM(CASE WHEN s.status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS failed_request,
  SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.total_tokens ELSE 0 END) AS total_tokens,
  AVG(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.ttft_ms END) AS avg_ttft
FROM logs s
WHERE s.session_id IN (
  SELECT session_id
  FROM (VALUES ('REPLACE_WITH_SESSION_ID_1'), ('REPLACE_WITH_SESSION_ID_2')) AS v(session_id)
)
  AND s.session_id <> ''
  AND COALESCE(s.exchange_kind, '') IN ('', 'entry', 'proxy')
GROUP BY s.session_id;
```

Overview summary：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT
  COUNT(*) AS request_count,
  SUM(CASE WHEN status_code >= 200 AND status_code < 300 AND error_text = '' THEN 1 ELSE 0 END) AS success_request,
  SUM(total_tokens) AS total_tokens,
  AVG(CASE WHEN ttft_ms > 0 THEN ttft_ms END) AS avg_ttft,
  AVG(CASE WHEN duration_ms > 0 THEN duration_ms END) AS avg_duration,
  COUNT(DISTINCT CASE WHEN session_id <> '' THEN session_id END) AS session_count
FROM logs
WHERE recorded_at >= now() - interval '7 days'
  AND COALESCE(exchange_kind, '') IN ('', 'entry', 'proxy');
```

Unparsed observation count：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT COUNT(*)
FROM logs l
WHERE NOT EXISTS (
  SELECT 1
  FROM trace_observations o
  WHERE o.trace_id = l.trace_id
);
```

System events latest list：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT
  id, fingerprint, source, category, severity, status, title, last_seen_at
FROM system_events
WHERE status = 'unread'
ORDER BY last_seen_at DESC, id DESC
LIMIT 50 OFFSET 0;
```

System events keyset next page：

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE)
SELECT
  id, fingerprint, source, category, severity, status, title, last_seen_at
FROM system_events
WHERE status = 'unread'
  AND (last_seen_at < TIMESTAMPTZ '2026-07-03T00:00:00Z'
    OR (last_seen_at = TIMESTAMPTZ '2026-07-03T00:00:00Z' AND id < 'REPLACE_WITH_LAST_ID'))
ORDER BY last_seen_at DESC, id DESC
LIMIT 51;
```

## Optional Reset

下面语句有副作用，只在准备开始明确采样窗口时执行：

```sql
SELECT pg_stat_statements_reset();
```
