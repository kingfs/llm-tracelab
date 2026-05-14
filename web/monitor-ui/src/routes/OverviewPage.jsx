import React, { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { BreakdownList } from "../components/monitor/BreakdownList";
import { MultiLineChart } from "../components/common/Charts";
import { InlineTag } from "../components/common/Badges";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import {
  buildRoutingLink,
  buildTraceLink,
  formatDateTime,
  formatDuration,
  formatEndpointTag,
  formatFailureReason,
  formatProviderTag,
  normalizeUpstreamWindow,
  setOrDeleteParam,
} from "../lib/monitor";

const REFRESH_MS = 60_000;
const WINDOW_OPTIONS = ["1h", "24h", "7d", "all"];

export function OverviewPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeOverviewWindow(searchParams.get("window") || "24h");
  const [refreshTick, setRefreshTick] = useState(0);
  const { loading, data, error } = useJSON(apiURL(apiPaths.overview, { window: windowValue }), [windowValue, refreshTick]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setRefreshTick((tick) => tick + 1);
    }, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  const summary = data?.summary || {};
  const breakdown = data?.breakdown || {};
  const attention = data?.attention || {};
  const analysis = data?.analysis || {};

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Local First LLM Replay Proxy</p>
          <h1>Overview</h1>
        </div>
        <div className="topbar-meta">
          <div className="view-toggle" aria-label="Overview window">
            {WINDOW_OPTIONS.map((option) => (
              <button key={option} className={`ghost-button ${windowValue === option ? "active" : ""}`.trim()} type="button" onClick={() => setWindow(option)}>
                {option}
              </button>
            ))}
          </div>
          <span className="badge badge-live">refresh / 60s</span>
          <span className="badge">{data?.refreshed_at ? formatDateTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>

      {error ? <EmptyState title="Unable to load overview" detail={error} tone="danger" /> : null}
      {loading && !data ? <EmptyState title="Loading overview" detail="Aggregating indexed traffic, audit, routing, and analysis signals." /> : null}

      <section className="hero-grid overview-kpi-grid">
        <StatCard label="Requests" value={summary.request_count ?? 0} detail={`${summary.session_count ?? 0} active sessions`} />
        <StatCard label="Success" value={`${Number(summary.success_rate ?? 0).toFixed(1)}%`} detail={`${summary.success_request ?? 0} successful`} accent="accent-green" />
        <StatCard label="Failed" value={summary.failed_request ?? 0} detail={`${attention.recent_failures?.length ?? 0} recent failures`} accent={(summary.failed_request ?? 0) > 0 ? "accent-red" : ""} />
        <StatCard label="Tokens" value={formatOverviewCount(summary.total_tokens ?? 0)} detail={`${summary.stream_count ?? 0} streaming traces`} accent="accent-gold" />
        <StatCard label="Avg TTFT" value={formatDuration(summary.avg_ttft_ms ?? 0)} />
        <StatCard label="Avg Latency" value={formatDuration(summary.avg_duration_ms ?? 0)} />
        <StatCard label="Findings" value={breakdown.finding_categories?.reduce((sum, item) => sum + Number(item.count || 0), 0) ?? 0} detail={`${attention.high_risk_findings?.length ?? 0} high risk`} accent={(attention.high_risk_findings?.length ?? 0) ? "accent-red" : ""} />
        <StatCard label="Analysis" value={analysis.total ?? 0} detail={`${analysis.failed ?? 0} failed runs`} accent={(analysis.failed ?? 0) ? "accent-red" : "accent-gold"} />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Trend</p>
            <h2>Workspace activity</h2>
          </div>
        </div>
        <div className="overview-chart-grid">
          <section className="usage-chart-panel">
            <div className="breakdown-title">Requests and failures</div>
            <MultiLineChart
              items={(data?.timeline || []).map((item) => ({
                time: item.time,
                series: {
                  requests: { value: item.request_count },
                  failures: { value: item.failed_request },
                },
              }))}
              series={[
                { key: "requests", name: "requests" },
                { key: "failures", name: "failures" },
              ]}
              metric="value"
              height={220}
            />
          </section>
          <section className="usage-chart-panel">
            <div className="breakdown-title">Tokens</div>
            <MultiLineChart
              items={(data?.timeline || []).map((item) => ({ time: item.time, value: item.total_tokens }))}
              series={[{ key: "value", name: "tokens" }]}
              metric="value"
              height={220}
            />
          </section>
          <section className="usage-chart-panel">
            <div className="breakdown-title">TTFT and latency</div>
            <MultiLineChart
              items={(data?.timeline || []).map((item) => ({
                time: item.time,
                series: {
                  ttft: { value: item.avg_ttft_ms },
                  latency: { value: item.avg_duration_ms },
                },
              }))}
              series={[
                { key: "ttft", name: "ttft ms" },
                { key: "latency", name: "latency ms" },
              ]}
              metric="value"
              height={220}
            />
          </section>
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Distribution</p>
            <h2>Top breakdowns</h2>
          </div>
        </div>
        <div className="session-breakdown-grid overview-breakdown-grid">
          <BreakdownList title="Models" items={breakdown.models || []} formatter={(item) => item.label || "unknown-model"} />
          <BreakdownList title="Providers" items={breakdown.providers || []} formatter={(item) => formatProviderTag(item.label)} />
          <BreakdownList title="Endpoints" items={breakdown.endpoints || []} formatter={(item) => formatEndpointTag(item.label)} />
          <BreakdownList title="Upstreams" items={breakdown.upstreams || []} formatter={(item) => item.label || "unknown-upstream"} />
          <BreakdownList title="Routing failures" items={breakdown.routing_failure_reasons || []} formatter={(item) => formatFailureReason(item.label)} />
          <BreakdownList title="Finding categories" items={breakdown.finding_categories || []} formatter={(item) => formatFailureReason(item.label)} />
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Attention</p>
            <h2>Needs review</h2>
          </div>
          <div className="panel-head-actions">
            <Link className="ghost-button" to="/audit">Audit</Link>
            <Link className="ghost-button" to={buildRoutingLink(normalizeUpstreamWindow(windowValue))}>Routing</Link>
          </div>
        </div>
        <div className="overview-attention-grid">
          <AttentionPanel title="Recent failures" emptyTitle="No recent failures">
            {(attention.recent_failures || []).length ? <RequestList items={attention.recent_failures || []} fromView="overview" focusFailures /> : null}
          </AttentionPanel>
          <AttentionPanel title="Slow traces" emptyTitle="No slow traces">
            {(attention.slow_traces || []).length ? <RequestList items={attention.slow_traces || []} fromView="overview" /> : null}
          </AttentionPanel>
          <AttentionPanel title="High-risk findings" emptyTitle="No high-risk findings">
            {(attention.high_risk_findings || []).length ? <FindingQueue items={attention.high_risk_findings || []} /> : null}
          </AttentionPanel>
          <AttentionPanel title="Routing failures" emptyTitle="No routing failures">
            {(attention.routing_failures || []).length ? <RoutingFailureQueue items={attention.routing_failures || []} /> : null}
          </AttentionPanel>
        </div>
      </section>
    </div>
  );
}

function AttentionPanel({ title, emptyTitle, children }) {
  return (
    <section className="overview-attention-panel">
      <div className="breakdown-title">{title}</div>
      {children || <EmptyState title={emptyTitle} detail="No indexed records require attention in the current window." compact />}
    </section>
  );
}

function FindingQueue({ items }) {
  return (
    <div className="overview-queue">
      {items.map((item) => (
        <Link className="overview-queue-row" key={item.id} to={buildTraceLink(item.trace_id, "overview", "", "audit", item.node_id || item.evidence_path || "finding")}>
          <div>
            <strong>{item.title || item.category || "Finding"}</strong>
            <span>{item.evidence_path || item.trace_id}</span>
          </div>
          <div className="trace-tag-group">
            <InlineTag tone={item.severity === "critical" ? "danger" : "gold"}>{item.severity}</InlineTag>
            <InlineTag>{formatFailureReason(item.category)}</InlineTag>
          </div>
        </Link>
      ))}
    </div>
  );
}

function RoutingFailureQueue({ items }) {
  return (
    <div className="overview-queue">
      {items.map((item) => (
        <Link className="overview-queue-row" key={`${item.trace_id}-${item.recorded_at}`} to={buildTraceLink(item.trace_id, "overview", "", "", "failure")}>
          <div>
            <strong>{item.model || "unknown-model"}</strong>
            <span>{formatDateTime(item.recorded_at)}</span>
          </div>
          <div className="trace-tag-group">
            <InlineTag tone="danger">{item.status_code}</InlineTag>
            <InlineTag tone="accent">{formatEndpointTag(item.endpoint)}</InlineTag>
            <InlineTag>{formatFailureReason(item.reason)}</InlineTag>
          </div>
        </Link>
      ))}
    </div>
  );
}

function normalizeOverviewWindow(value = "") {
  switch (value) {
    case "1h":
    case "7d":
    case "all":
      return value;
    default:
      return "24h";
  }
}

function formatOverviewCount(value = 0) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) {
    return "0";
  }
  if (Math.abs(number) >= 1_000_000) {
    return `${(number / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (Math.abs(number) >= 1_000) {
    return `${(number / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  }
  return String(Math.round(number));
}
