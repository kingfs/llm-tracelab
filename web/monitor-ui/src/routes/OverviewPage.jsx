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
import { useI18n } from "../lib/i18n";
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
  const { t } = useI18n();
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
          <p className="eyebrow">{t("overview.eyebrow")}</p>
          <h1>{t("overview.title")}</h1>
        </div>
        <div className="topbar-meta">
          <div className="view-toggle" aria-label={t("overview.window")}>
            {MONITOR_WINDOW_OPTIONS.map((option) => (
              <button key={option} className={`ghost-button ${windowValue === option ? "active" : ""}`.trim()} type="button" onClick={() => setWindow(option)}>
                {option}
              </button>
            ))}
          </div>
          <span className="badge badge-live">{t("overview.refresh")}</span>
          <span className="badge">{data?.refreshed_at ? formatDateTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>

      {error ? <EmptyState title={t("overview.loadError")} detail={error} tone="danger" /> : null}
      {loading && !data ? <EmptyState title={t("overview.loading")} detail={t("overview.loadingDetail")} /> : null}

      <section className="hero-grid overview-kpi-grid">
        <StatCard label={t("overview.requests")} value={summary.request_count ?? 0} detail={t("overview.activeSessions", { count: summary.session_count ?? 0 })} />
        <StatCard label={t("overview.success")} value={`${Number(summary.success_rate ?? 0).toFixed(1)}%`} detail={t("overview.successful", { count: summary.success_request ?? 0 })} accent="accent-green" />
        <StatCard label={t("overview.failed")} value={summary.failed_request ?? 0} detail={t("overview.recentFailures", { count: attention.recent_failures?.length ?? 0 })} accent={(summary.failed_request ?? 0) > 0 ? "accent-red" : ""} />
        <StatCard label={t("overview.tokens")} value={formatTokenCount(summary.total_tokens ?? 0)} detail={t("overview.streamingTraces", { count: summary.stream_count ?? 0 })} accent="accent-gold" title={String(summary.total_tokens ?? 0)} />
        <StatCard label="TTFT" value={formatDuration(summary.avg_ttft_ms ?? 0)} detail={`p95 ${formatDuration(summary.p95_ttft_ms ?? 0)}`} />
        <StatCard label={t("overview.latency")} value={formatDuration(summary.avg_duration_ms ?? 0)} detail={`p95 ${formatDuration(summary.p95_duration_ms ?? 0)}`} />
        <StatCard label={t("overview.findings")} value={breakdown.finding_categories?.reduce((sum, item) => sum + Number(item.count || 0), 0) ?? 0} detail={t("overview.highRisk", { count: attention.high_risk_findings?.length ?? 0 })} accent={(attention.high_risk_findings?.length ?? 0) ? "accent-red" : ""} />
        <StatCard label={t("overview.systemEvents")} value={eventSummary?.unread ?? 0} detail={t("overview.eventCounts", { errors: eventSummary?.error ?? 0, warnings: eventSummary?.warning ?? 0 })} accent={(eventSummary?.unread ?? 0) ? "accent-red" : "accent-green"} />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">{t("overview.derivedData")}</p>
            <h2>{t("overview.health")}</h2>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact overview-health-grid">
          <StatCard label={t("overview.unreadEvents")} value={eventSummary?.unread ?? 0} detail={eventSummary?.last_seen_at ? t("overview.latest", { time: formatDateTime(eventSummary.last_seen_at) }) : t("overview.noRuntimeExceptions")} accent={(eventSummary?.unread ?? 0) ? "accent-red" : "accent-green"} />
          <StatCard label={t("overview.parsed")} value={observation.parsed ?? 0} detail={t("overview.observationRows", { count: observation.total_observations ?? 0 })} accent="accent-green" />
          <Link className="stat-card stat-card-link" to="/requests?observation=unparsed">
            <span>{t("overview.unparsed")}</span>
            <strong>{observation.unparsed ?? 0}</strong>
            <small className="stat-detail">{t("overview.unparsedDetail")}</small>
          </Link>
          <StatCard label={t("overview.parseQueue")} value={(observation.queued ?? 0) + (observation.running ?? 0)} detail={t("overview.queueDetail", { queued: observation.queued ?? 0, running: observation.running ?? 0 })} accent={(observation.queued ?? 0) || (observation.running ?? 0) ? "accent-gold" : ""} />
          <StatCard label={t("nav.analysis")} value={analysis.total ?? 0} detail={t("overview.analysisFailed", { count: analysis.failed ?? 0 })} accent={(analysis.failed ?? 0) ? "accent-red" : "accent-gold"} />
        </div>
        <div className="panel-foot-actions overview-events-link">
          <Link className="ghost-button active" to="/events">{t("overview.openEvents")}</Link>
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">{t("overview.providers")}</p>
            <h2>{t("overview.configuredUpstreams")}</h2>
          </div>
          <div className="panel-head-actions">
            <Link className="ghost-button active icon-text-button" to="/providers">
              <PlusIcon />
              <span>{t("overview.newProvider")}</span>
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
                  <InlineTag tone={provider.enabled ? "green" : "gold"}>{provider.enabled ? t("overview.enabled") : t("overview.disabled")}</InlineTag>
                  {provider.last_probe_status ? <InlineTag tone={provider.last_probe_status === "success" ? "green" : "danger"}>{provider.last_probe_status}</InlineTag> : null}
                </div>
                <div className="detail-meta-strip">
                  <OverviewProviderMetric label={t("overview.models")} value={`${formatCount(provider.enabled_model_count)} / ${formatCount(provider.model_count)}`} />
                  <OverviewProviderMetric label={t("overview.requests")} value={formatCount(provider.summary?.request_count)} />
                  <OverviewProviderMetric label={t("overview.tokens")} value={formatTokenCount(provider.summary?.total_tokens || 0)} />
                </div>
              </Link>
            ))}
          </div>
        ) : (
          <EmptyState title={t("overview.noProviders")} detail={t("overview.noProvidersDetail")} compact />
        )}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">{t("overview.trend")}</p>
            <h2>{t("overview.workspaceActivity")}</h2>
          </div>
        </div>
        <div className="overview-chart-grid">
          <section className="usage-chart-panel">
            <div className="breakdown-title">{t("overview.requestsFailures")}</div>
            <MultiLineChart
              items={(data?.timeline || []).map((item) => ({
                time: item.time,
                series: {
                  requests: { value: item.request_count },
                  failures: { value: item.failed_request },
                },
              }))}
              series={[
                { key: "requests", name: t("overview.requests") },
                { key: "failures", name: t("overview.failed") },
              ]}
              metric="value"
              height={220}
            />
          </section>
          <section className="usage-chart-panel">
            <div className="breakdown-title">{t("overview.tokens")}</div>
            <MultiLineChart
              items={(data?.timeline || []).map((item) => ({ time: item.time, value: item.total_tokens }))}
              series={[{ key: "value", name: t("overview.tokens") }]}
              metric="value"
              height={220}
            />
          </section>
          <section className="usage-chart-panel">
            <div className="breakdown-title">{t("overview.ttftLatency")}</div>
            <MultiLineChart
              items={(data?.timeline || []).map((item) => ({
                time: item.time,
                series: {
                  ttft: { value: Number(item.avg_ttft_ms || 0) / 1000 },
                  latency: { value: Number(item.avg_duration_ms || 0) / 1000 },
                },
              }))}
              series={[
                { key: "ttft", name: "TTFT s" },
                { key: "latency", name: `${t("overview.latency")} s` },
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
            <p className="eyebrow">{t("overview.distribution")}</p>
            <h2>{t("overview.topBreakdowns")}</h2>
          </div>
        </div>
        <div className="session-breakdown-grid overview-breakdown-grid">
          <BreakdownList title={t("overview.models")} items={breakdown.models || []} formatter={(item) => item.label || "unknown-model"} linkFor={(item) => buildOverviewBreakdownLink("model", item.label, windowValue)} />
          <BreakdownList title={t("overview.providers")} items={breakdown.providers || []} formatter={(item) => formatProviderTag(item.label)} linkFor={(item) => buildOverviewBreakdownLink("provider", item.label, windowValue)} />
          <BreakdownList title={t("overview.endpoints")} items={breakdown.endpoints || []} formatter={(item) => formatEndpointTag(item.label)} linkFor={(item) => buildOverviewBreakdownLink("endpoint", item.label, windowValue)} />
          <BreakdownList title={t("overview.upstreams")} items={breakdown.upstreams || []} formatter={(item) => item.label || "unknown-upstream"} linkFor={(item) => buildOverviewBreakdownLink("upstream", item.label, windowValue)} />
          <BreakdownList title={t("overview.routingFailures")} items={breakdown.routing_failure_reasons || []} formatter={(item) => formatFailureReason(item.label)} linkFor={(item) => buildOverviewBreakdownLink("routing_failure", item.label, windowValue)} />
          <BreakdownList title={t("overview.findingCategories")} items={breakdown.finding_categories || []} formatter={(item) => formatFailureReason(item.label)} linkFor={(item) => buildOverviewBreakdownLink("finding_category", item.label, windowValue)} />
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">{t("overview.attention")}</p>
            <h2>{t("overview.needsReview")}</h2>
          </div>
          <div className="panel-head-actions">
            <Link className="ghost-button" to="/audit">{t("overview.audit")}</Link>
            <Link className="ghost-button" to={buildRoutingLink(normalizeUpstreamWindow(windowValue))}>{t("overview.routing")}</Link>
          </div>
        </div>
        <div className="overview-attention-grid">
          <AttentionPanel title={t("overview.recentFailuresTitle")} emptyTitle={t("overview.noRecentFailures")}>
            {(attention.recent_failures || []).length ? <RequestList items={attention.recent_failures || []} fromView="overview" focusFailures /> : null}
          </AttentionPanel>
          <AttentionPanel title={t("overview.slowTraces")} emptyTitle={t("overview.noSlowTraces")}>
            {(attention.slow_traces || []).length ? <RequestList items={attention.slow_traces || []} fromView="overview" /> : null}
          </AttentionPanel>
          <AttentionPanel title={t("overview.highRiskFindings")} emptyTitle={t("overview.noHighRiskFindings")}>
            {(attention.high_risk_findings || []).length ? <FindingQueue items={attention.high_risk_findings || []} /> : null}
          </AttentionPanel>
          <AttentionPanel title={t("overview.routingFailures")} emptyTitle={t("overview.noRoutingFailures")}>
            {(attention.routing_failures || []).length ? <RoutingFailureQueue items={attention.routing_failures || []} /> : null}
          </AttentionPanel>
        </div>
      </section>
    </div>
  );
}

function AttentionPanel({ title, emptyTitle, children }) {
  const { t } = useI18n();
  return (
    <section className="overview-attention-panel">
      <div className="breakdown-title">{title}</div>
      {children || <EmptyState title={emptyTitle} detail={t("overview.noAttentionDetail")} compact />}
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
  const { t } = useI18n();
  return (
    <div className="overview-queue">
      {items.map((item) => (
        <Link className="overview-queue-row" key={item.id} to={buildTraceLink(item.trace_id, "overview", "", "audit", item.node_id || item.evidence_path || "finding")}>
          <div>
            <strong>{item.title || item.category || t("overview.finding")}</strong>
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
