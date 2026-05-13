import React, { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { DownloadIcon, InlineTag, LatencyMetric, MiniToken, StackIcon, ViewIcon } from "../common/Badges";
import { EmptyState } from "../common/EmptyState";
import { apiPaths } from "../../lib/api";
import { buildTraceLink, formatCacheRate, formatDateTime, formatDuration, formatEndpointTag, formatGenerationSpeed, formatPrefillSpeed, formatProviderTag } from "../../lib/monitor";

export function RequestList({ items, fromView = "", fromSessionID = "", focusFailures = false, groupSessionFailures = false }) {
  const [expandedGroups, setExpandedGroups] = useState(() => new Set());
  const rows = useMemo(() => buildRequestRows(items, groupSessionFailures), [items, groupSessionFailures]);

  if (!items.length) {
    return <EmptyState title="No traces found" detail="No trace records matched the current filter or page range." />;
  }

  const toggleGroup = (key) => {
    setExpandedGroups((current) => {
      const next = new Set(current);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  return (
    <div className="trace-table">
      <div className="trace-table-head">
        <span>Model</span>
        <span>Status</span>
        <span>Speed</span>
        <span>Tokens</span>
        <span>Actions</span>
      </div>
      {rows.map((row) => {
        if (row.type === "group") {
          const isOpen = expandedGroups.has(row.key);
          return (
            <React.Fragment key={row.key}>
              <FailureGroupRow group={row} isOpen={isOpen} onToggle={() => toggleGroup(row.key)} />
              {isOpen ? row.items.map((item) => <RequestRow key={item.id} item={item} fromView={fromView} fromSessionID={fromSessionID} focusFailures={focusFailures} groupedChild />) : null}
            </React.Fragment>
          );
        }
        return <RequestRow key={row.item.id} item={row.item} fromView={fromView} fromSessionID={fromSessionID} focusFailures={focusFailures} />;
      })}
    </div>
  );
}

function RequestRow({ item, fromView = "", fromSessionID = "", focusFailures = false, groupedChild = false }) {
  const failed = item.status_code < 200 || item.status_code >= 300;
  const focus = focusFailures && failed ? "failure" : "";

  return (
    <article className={`${failed ? "trace-row trace-row-failed" : "trace-row"}${groupedChild ? " trace-row-grouped-child" : ""}`}>
      <div>
        <div className="trace-title-row">
          <strong className="trace-model-name">{item.model || "unknown-model"}</strong>
          <div className="trace-tag-group">
            <InlineTag tone="accent">{formatEndpointTag(item.endpoint || item.operation)}</InlineTag>
            <InlineTag>{formatProviderTag(item.provider)}</InlineTag>
            {item.session_id ? <InlineTag tone="green">session</InlineTag> : null}
            {item.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
          </div>
        </div>
        <div className="trace-subline-group">
          <span className="trace-subline">{formatDateTime(item.recorded_at)}</span>
          {item.session_id ? <span className="trace-subline mono">session {item.session_id}</span> : null}
        </div>
      </div>
      <div className="trace-metric-stack">
        <strong className={failed ? "status-err" : "status-ok"}>{item.status_code}</strong>
        <span>{item.method || "POST"}</span>
      </div>
      <div className="latency-metric-stack">
        <LatencyMetric label="total" value={formatDuration(item.duration_ms)} icon="duration" title={`${item.duration_ms || 0} ms`} />
        <LatencyMetric label="ttft" value={formatDuration(item.ttft_ms)} icon="ttft" title={`${item.ttft_ms || 0} ms`} />
        <LatencyMetric label="pp" value={formatPrefillSpeed(item.prompt_tokens, item.ttft_ms, item.duration_ms, item.is_stream)} icon="pp" title={`prefill speed`} />
        <LatencyMetric label="tg" value={formatGenerationSpeed(item.completion_tokens, item.duration_ms, item.ttft_ms, item.is_stream)} icon="tg" title={`generation speed`} />
      </div>
      <TokenMetrics item={item} />
      <RowActions item={item} fromView={fromView} fromSessionID={fromSessionID} focus={focus} />
    </article>
  );
}

function FailureGroupRow({ group, isOpen, onToggle }) {
  const first = group.items[0] || {};
  return (
    <article className="trace-row trace-row-failed trace-row-group">
      <div>
        <div className="trace-title-row">
          <strong className="trace-model-name">{first.model || "unknown-model"}</strong>
          <div className="trace-tag-group">
            <InlineTag tone="danger">{group.count} failures</InlineTag>
            <InlineTag tone="accent">{formatEndpointTag(first.endpoint || first.operation)}</InlineTag>
            <InlineTag>{formatProviderTag(first.provider)}</InlineTag>
            {first.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
          </div>
        </div>
        <div className="trace-subline-group">
          <span className="trace-subline">{formatDateTime(group.firstSeen)} - {formatDateTime(group.lastSeen)}</span>
          {group.sessionID ? <span className="trace-subline mono">session {group.sessionID}</span> : null}
        </div>
      </div>
      <div className="trace-metric-stack">
        <strong className="status-err">{group.statusCode}</strong>
        <span>HTTP</span>
      </div>
      <div className="latency-metric-stack">
        <LatencyMetric label="total" value={formatDuration(group.totalDuration)} icon="duration" title={`${group.totalDuration} ms combined`} />
        <LatencyMetric label="avg" value={formatDuration(group.avgDuration)} icon="ttft" title={`${group.avgDuration} ms average`} />
      </div>
      <TokenMetrics item={group} />
      <div className="action-group trace-row-actions">
        <button className="ghost-button" type="button" onClick={onToggle}>
          {isOpen ? "Collapse" : "Expand"}
        </button>
      </div>
    </article>
  );
}

function TokenMetrics({ item }) {
  return (
    <div>
      <div className="token-inline-row">
        <MiniToken metric="in" value={item.prompt_tokens} tone="accent" icon="input" />
        <MiniToken metric="out" value={item.completion_tokens} tone="green" icon="output" />
        <MiniToken metric="total" value={item.total_tokens} tone="default" icon="total" />
        <MiniToken metric="cached" value={item.cached_tokens} tone="gold" icon="cached" />
        <MiniToken metric="cache%" value={formatCacheRate(item.cached_tokens, item.total_tokens)} tone="gold" icon="cached" />
      </div>
    </div>
  );
}

function RowActions({ item, fromView = "", fromSessionID = "", focus = "" }) {
  return (
    <div className="action-group trace-row-actions">
      {item.session_id ? (
        <Link className="icon-button" to={`/sessions/${encodeURIComponent(item.session_id)}`} title="View session" aria-label="View session">
          <StackIcon />
        </Link>
      ) : null}
      {fromSessionID ? (
        <Link className="ghost-button" to={buildTraceLink(item.id, fromView, fromSessionID, "timeline", focus === "failure" ? "timeline_error" : "timeline")}>
          Timeline
        </Link>
      ) : null}
      {fromSessionID ? (
        <Link className="ghost-button" to={buildTraceLink(item.id, fromView, fromSessionID, "raw", focus === "failure" ? "response" : focus)}>
          Raw
        </Link>
      ) : null}
      <Link className="icon-button" to={buildTraceLink(item.id, fromView, fromSessionID, "", focus)} title="View trace" aria-label="View trace">
        <ViewIcon />
      </Link>
      <a className="icon-button" href={apiPaths.traceDownload(item.id)} title="Download .http" aria-label="Download trace">
        <DownloadIcon />
      </a>
    </div>
  );
}

function buildRequestRows(items, groupSessionFailures) {
  if (!groupSessionFailures) {
    return items.map((item) => ({ type: "item", item }));
  }
  const groups = new Map();
  const itemToGroupKey = new Map();
  for (const item of items) {
    if (!isFailed(item) || !item.session_id) {
      continue;
    }
    const key = `${item.session_id}:${item.status_code}`;
    if (!groups.has(key)) {
      groups.set(key, []);
    }
    groups.get(key).push(item);
    itemToGroupKey.set(item.id, key);
  }
  const emittedGroups = new Set();
  const rows = [];
  for (const item of items) {
    const groupKey = itemToGroupKey.get(item.id);
    const groupItems = groupKey ? groups.get(groupKey) || [] : [];
    if (!groupKey || groupItems.length < 2) {
      rows.push({ type: "item", item });
      continue;
    }
    if (emittedGroups.has(groupKey)) {
      continue;
    }
    rows.push(buildFailureGroup(groupKey, groupItems));
    emittedGroups.add(groupKey);
  }
  return rows;
}

function buildFailureGroup(groupKey, items) {
  const first = items[0];
  const totalDuration = items.reduce((sum, item) => sum + Number(item.duration_ms || 0), 0);
  const totalTokens = items.reduce((sum, item) => sum + Number(item.total_tokens || 0), 0);
  return {
    type: "group",
    key: `failure-group:${groupKey}`,
    sessionID: first.session_id,
    statusCode: first.status_code,
    count: items.length,
    firstSeen: minRecordedAt(items),
    lastSeen: maxRecordedAt(items),
    totalDuration,
    avgDuration: Math.round(totalDuration / items.length),
    duration_ms: totalDuration,
    total_tokens: totalTokens,
    prompt_tokens: items.reduce((sum, item) => sum + Number(item.prompt_tokens || 0), 0),
    completion_tokens: items.reduce((sum, item) => sum + Number(item.completion_tokens || 0), 0),
    cached_tokens: items.reduce((sum, item) => sum + Number(item.cached_tokens || 0), 0),
    items,
  };
}

function isFailed(item) {
  return item.status_code < 200 || item.status_code >= 300;
}

function minRecordedAt(items) {
  return items.reduce((min, item) => {
    if (!min || new Date(item.recorded_at) < new Date(min)) {
      return item.recorded_at;
    }
    return min;
  }, "");
}

function maxRecordedAt(items) {
  return items.reduce((max, item) => {
    if (!max || new Date(item.recorded_at) > new Date(max)) {
      return item.recorded_at;
    }
    return max;
  }, "");
}
