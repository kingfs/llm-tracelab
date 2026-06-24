import React, { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { BreakdownList } from "../components/monitor/BreakdownList";
import { MultiLineChart } from "../components/common/Charts";
import { InlineTag, PlusIcon } from "../components/common/Badges";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import {
  buildRoutingLink,
  buildTraceLink,
  buildProviderLink,
  formatCount,
  formatDateTime,
  formatDuration,
  formatEndpointTag,
  formatFailureReason,
  formatProviderTag,
  formatTokenCount,
  MONITOR_WINDOW_OPTIONS,
  normalizeAnalyticsWindow,
  normalizeUpstreamWindow,
  setOrDeleteParam,
} from "../lib/monitor";

const REFRESH_MS = 60_000;

export function OverviewPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const [refreshTick, setRefreshTick] = useState(0);
  const { loading, data, error } = useJSON(apiURL(apiPaths.overview, { window: windowValue }), [windowValue, refreshTick]);
  const { data: eventSummary } = useJSON(apiURL(apiPaths.eventsSummary, { window: windowValue }), [windowValue, refreshTick]);
  const { data: providerData } = useJSON(apiURL(apiPaths.providers, { window: windowValue }), [windowValue, refreshTick]);

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
  const observation = data?.observation || {};
  const providers = providerData?.items || [];

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "today" ? "" : nextWindow);
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
            {MONITOR_WINDOW_OPTIONS.map((option) => (
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
        <StatCard label="Tokens" value={formatTokenCount(summary.total_tokens ?? 0)} detail={`${summary.stream_count ?? 0} streaming traces`} accent="accent-gold" title={String(summary.total_tokens ?? 0)} />
        <StatCard label="TTFT" value={formatDuration(summary.avg_ttft_ms ?? 0)} detail={`p95 ${formatDuration(summary.p95_ttft_ms ?? 0)}`} />
        <StatCard label="Latency" value={formatDuration(summary.avg_duration_ms ?? 0)} detail={`p95 ${formatDuration(summary.p95_duration_ms ?? 0)}`} />
        <StatCard label="Findings" value={breakdown.finding_categories?.reduce((sum, item) => sum + Number(item.count || 0), 0) ?? 0} detail={`${attention.high_risk_findings?.length ?? 0} high risk`} accent={(attention.high_risk_findings?.length ?? 0) ? "accent-red" : ""} />
        <StatCard label="System Events" value={eventSummary?.unread ?? 0} detail={`${eventSummary?.error ?? 0} errors, ${eventSummary?.warning ?? 0} warnings`} accent={(eventSummary?.unread ?? 0) ? "accent-red" : "accent-green"} />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Derived data</p>
            <h2>Observation and analysis health</h2>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact overview-health-grid">
          <StatCard label="Unread Events" value={eventSummary?.unread ?? 0} detail={eventSummary?.last_seen_at ? `latest ${formatDateTime(eventSummary.last_seen_at)}` : "no runtime exceptions"} accent={(eventSummary?.unread ?? 0) ? "accent-red" : "accent-green"} />
          <StatCard label="Parsed" value={observation.parsed ?? 0} detail={`${observation.total_observations ?? 0} observation rows`} accent="accent-green" />
          <Link className="stat-card stat-card-link" to="/requests?observation=unparsed">
            <span>Unparsed</span>
            <strong>{observation.unparsed ?? 0}</strong>
            <small className="stat-detail">indexed traces without observation</small>
          </Link>
          <StatCard label="Parse Queue" value={(observation.queued ?? 0) + (observation.running ?? 0)} detail={`${observation.queued ?? 0} queued, ${observation.running ?? 0} running`} accent={(observation.queued ?? 0) || (observation.running ?? 0) ? "accent-gold" : ""} />
          <StatCard label="Analysis" value={analysis.total ?? 0} detail={`${analysis.failed ?? 0} failed runs`} accent={(analysis.failed ?? 0) ? "accent-red" : "accent-gold"} />
        </div>
        <div className="panel-foot-actions overview-events-link">
          <Link className="ghost-button active" to="/events">Open Events</Link>
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Providers</p>
            <h2>Configured upstreams</h2>
          </div>
          <div className="panel-head-actions">
            <Link className="ghost-button active icon-text-button" to="/providers">
              <PlusIcon />
              <span>New provider</span>
            </Link>
          </div>
        </div>
        {providers.length ? (
          <div className="overview-provider-grid">
            {providers.map((provider) => (
              <Link className="overview-provider-card" key={provider.id} to={buildProviderLink(provider.id, windowValue)}>
                <div className="provider-logo-button" aria-hidden="true">{providerLogoText(provider)}</div>
                <div>
                  <strong>{provider.name || provider.id}</strong>
                  <span>{provider.provider_preset || "custom"}</span>
                </div>
                <div className="trace-tag-group">
                  <InlineTag tone={provider.enabled ? "green" : "gold"}>{provider.enabled ? "enabled" : "disabled"}</InlineTag>
                  {provider.last_probe_status ? <InlineTag tone={provider.last_probe_status === "success" ? "green" : "danger"}>{provider.last_probe_status}</InlineTag> : null}
                </div>
                <div className="detail-meta-strip">
                  <OverviewProviderMetric label="models" value={`${formatCount(provider.enabled_model_count)} / ${formatCount(provider.model_count)}`} />
                  <OverviewProviderMetric label="requests" value={formatCount(provider.summary?.request_count)} />
                  <OverviewProviderMetric label="tokens" value={formatTokenCount(provider.summary?.total_tokens || 0)} />
                </div>
              </Link>
            ))}
          </div>
        ) : (
          <EmptyState title="No providers configured" detail="Add a provider before routing client traffic through TraceLab." compact />
        )}
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
                  ttft: { value: Number(item.avg_ttft_ms || 0) / 1000 },
                  latency: { value: Number(item.avg_duration_ms || 0) / 1000 },
                },
              }))}
              series={[
                { key: "ttft", name: "ttft s" },
                { key: "latency", name: "latency s" },
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
          <BreakdownList title="Models" items={breakdown.models || []} formatter={(item) => item.label || "unknown-model"} linkFor={(item) => buildOverviewBreakdownLink("model", item.label, windowValue)} />
          <BreakdownList title="Providers" items={breakdown.providers || []} formatter={(item) => formatProviderTag(item.label)} linkFor={(item) => buildOverviewBreakdownLink("provider", item.label, windowValue)} />
          <BreakdownList title="Endpoints" items={breakdown.endpoints || []} formatter={(item) => formatEndpointTag(item.label)} linkFor={(item) => buildOverviewBreakdownLink("endpoint", item.label, windowValue)} />
          <BreakdownList title="Upstreams" items={breakdown.upstreams || []} formatter={(item) => item.label || "unknown-upstream"} linkFor={(item) => buildOverviewBreakdownLink("upstream", item.label, windowValue)} />
          <BreakdownList title="Routing failures" items={breakdown.routing_failure_reasons || []} formatter={(item) => formatFailureReason(item.label)} linkFor={(item) => buildOverviewBreakdownLink("routing_failure", item.label, windowValue)} />
          <BreakdownList title="Finding categories" items={breakdown.finding_categories || []} formatter={(item) => formatFailureReason(item.label)} linkFor={(item) => buildOverviewBreakdownLink("finding_category", item.label, windowValue)} />
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

function providerLogoText(provider = {}) {
  const source = provider.provider_preset || provider.name || provider.id || "AI";
  const parts = String(source).replace(/[_-]+/g, " ").trim().split(/\s+/).filter(Boolean);
  if (!parts.length) {
    return "AI";
  }
  if (parts.length === 1) {
    return parts[0].slice(0, 2).toUpperCase();
  }
  return `${parts[0][0]}${parts[1][0]}`.toUpperCase();
}

function OverviewProviderMetric({ label, value }) {
  return (
    <span className="detail-meta-pill">
      <span className="detail-meta-label">{label}</span>
      <strong>{value}</strong>
    </span>
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

function buildOverviewBreakdownLink(kind, value, windowValue) {
  const label = String(value || "").trim();
  if (!label) {
    return "";
  }
  switch (kind) {
    case "model":
      return `/models/${encodeURIComponent(label)}${windowValue && windowValue !== "today" ? `?window=${encodeURIComponent(windowValue)}` : ""}`;
    case "provider":
      return `/traces?provider=${encodeURIComponent(label)}`;
    case "endpoint":
      return `/traces?q=${encodeURIComponent(label)}`;
    case "upstream":
      return buildRoutingLink(normalizeUpstreamWindow(windowValue), label);
    case "routing_failure":
      return `/routing?status=error${windowValue && windowValue !== "today" ? `&window=${encodeURIComponent(windowValue)}` : ""}`;
    case "finding_category":
      return `/audit?category=${encodeURIComponent(label)}`;
    default:
      return "";
  }
}
