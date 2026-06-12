import React, { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { InlineTag } from "../components/common/Badges";
import { BreakdownList } from "../components/monitor/BreakdownList";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import { formatCount, formatTime, MONITOR_WINDOW_OPTIONS, setOrDeleteParam } from "../lib/monitor";

const REFRESH_MS = 60_000;
const WINDOW_OPTIONS = MONITOR_WINDOW_OPTIONS;
const FILTER_KEYS = ["model", "upstream", "status", "min_duration_ms", "max_duration_ms", "min_ttft_ms", "max_ttft_ms", "min_tokens", "max_tokens"];

export function RoutingPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeRoutingWindow(searchParams.get("window"));
  const activeFilters = readRoutingFilters(searchParams);
  const [refreshTick, setRefreshTick] = useState(0);
  const [filters, setFilters] = useState(activeFilters);
  const params = new URLSearchParams();
  params.set("page", "1");
  params.set("page_size", "200");
  FILTER_KEYS.forEach((key) => {
    if (activeFilters[key]) {
      params.set(key, activeFilters[key]);
    }
  });
  const summaryParams = new URLSearchParams();
  summaryParams.set("window", routingSummaryWindow(windowValue));
  if (activeFilters.model) {
    summaryParams.set("model", activeFilters.model);
  }
  const traces = useJSON(apiURL(apiPaths.traces, params), [refreshTick, windowValue, ...FILTER_KEYS.map((key) => activeFilters[key])]);
  const routingSummary = useJSON(apiURL(apiPaths.routingSummary, summaryParams), [refreshTick, windowValue, activeFilters.model]);
  const routedItems = useMemo(() => filterByWindow(traces.data?.items || [], windowValue), [traces.data, windowValue]);
  const summary = useMemo(() => summarizeRouting(routedItems), [routedItems]);
  const credentialSummary = useMemo(() => normalizeCredentialRoutingSummary(routingSummary.data), [routingSummary.data]);

  useEffect(() => {
    const timer = window.setInterval(() => setRefreshTick((tick) => tick + 1), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    setFilters(activeFilters);
  }, [searchParams]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "today" ? "" : nextWindow);
    setSearchParams(next);
  };
  const applyFilters = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    FILTER_KEYS.forEach((key) => setOrDeleteParam(next, key, filters[key]));
    setSearchParams(next);
  };
  const resetFilters = () => {
    setFilters(emptyRoutingFilters());
    const next = new URLSearchParams(searchParams);
    FILTER_KEYS.forEach((key) => next.delete(key));
    setSearchParams(next);
  };
  const updateFilter = (key, value) => setFilters((current) => ({ ...current, [key]: value }));

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Routing decisions</p>
          <h1>Routing</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">refresh / 60s</span>
          <span className="badge">{traces.data?.refreshed_at ? formatTime(traces.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Decision log</p>
            <h2>Recent selected routes</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Routing window">
              {WINDOW_OPTIONS.map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <form className="filter-bar routing-filter-bar" onSubmit={applyFilters}>
          <input className="filter-input" type="search" name="routing_model" placeholder="Model" value={filters.model} onChange={(event) => updateFilter("model", event.target.value)} />
          <input className="filter-input" type="search" name="routing_upstream" placeholder="Channel / upstream" value={filters.upstream} onChange={(event) => updateFilter("upstream", event.target.value)} />
          <select className="filter-input" name="routing_status" aria-label="Routing status" value={filters.status} onChange={(event) => updateFilter("status", event.target.value)}>
            <option value="">Any status</option>
            <option value="success">Success</option>
            <option value="error">Error</option>
          </select>
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_min_duration" placeholder="Min duration" value={filters.min_duration_ms} onChange={(event) => updateFilter("min_duration_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_max_duration" placeholder="Max duration" value={filters.max_duration_ms} onChange={(event) => updateFilter("max_duration_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_min_ttft" placeholder="Min TTFT" value={filters.min_ttft_ms} onChange={(event) => updateFilter("min_ttft_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_max_ttft" placeholder="Max TTFT" value={filters.max_ttft_ms} onChange={(event) => updateFilter("max_ttft_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_min_tokens" placeholder="Min tokens" value={filters.min_tokens} onChange={(event) => updateFilter("min_tokens", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_max_tokens" placeholder="Max tokens" value={filters.max_tokens} onChange={(event) => updateFilter("max_tokens", event.target.value)} />
          <button className="ghost-button" type="submit">Apply</button>
          <button className="ghost-button" type="button" onClick={resetFilters}>Reset</button>
        </form>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Routed requests" value={formatCount(summary.requests)} />
          <StatCard label="Channels" value={formatCount(summary.channels)} />
          <StatCard label="Errors" value={formatCount(summary.errors)} accent={summary.errors ? "accent-red" : ""} />
          <StatCard label="Tokens" value={formatCount(summary.tokens)} detail={usageCoverageDetail(summary.missing)} />
        </div>
      </section>

      {traces.error ? <EmptyState title="Unable to load routing records" detail={traces.error} tone="danger" /> : null}
      {routingSummary.error ? <EmptyState title="Unable to load routing event summary" detail={routingSummary.error} tone="danger" compact /> : null}
      {traces.loading && !traces.data ? <EmptyState title="Loading routing records" detail="Reading recent traces with selected channels, status, tokens, duration, and TTFT." /> : null}
      {routingSummary.data ? <CredentialRoutingSummaryPanel summary={credentialSummary} windowValue={windowValue} /> : null}
      {traces.data ? <RequestList items={routedItems} fromView="routing" focusFailures /> : null}
    </div>
  );
}

function normalizeRoutingWindow(value) {
  return WINDOW_OPTIONS.includes(value) ? value : "today";
}

function emptyRoutingFilters() {
  return FILTER_KEYS.reduce((state, key) => ({ ...state, [key]: "" }), {});
}

function readRoutingFilters(searchParams) {
  const filters = emptyRoutingFilters();
  FILTER_KEYS.forEach((key) => {
    filters[key] = searchParams.get(key) || "";
  });
  return filters;
}

function filterByWindow(items, windowValue) {
  if (windowValue === "all") {
    return items.filter((item) => item.selected_upstream_id);
  }
  const now = new Date();
  const since = windowValue === "today"
    ? new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
    : Date.now() - (windowValue === "7d" ? 7 * 24 * 60 * 60 * 1000 : 30 * 24 * 60 * 60 * 1000);
  return items.filter((item) => item.selected_upstream_id && new Date(item.recorded_at).getTime() >= since);
}

function summarizeRouting(items) {
  const channels = new Set();
  return items.reduce((state, item) => {
    state.requests += 1;
    state.tokens += Number(item.total_tokens || 0);
    if (hasMissingUsage(item)) {
      state.missing += 1;
    }
    if (item.status_code < 200 || item.status_code >= 300) {
      state.errors += 1;
    }
    if (item.selected_upstream_id) {
      channels.add(item.selected_upstream_id);
    }
    state.channels = channels.size;
    return state;
  }, { requests: 0, errors: 0, tokens: 0, missing: 0, channels: 0 });
}

function hasMissingUsage(item) {
  return item.status_code >= 200 && item.status_code < 300 && Number(item.total_tokens || 0) === 0 && Number(item.prompt_tokens || 0) === 0 && Number(item.completion_tokens || 0) === 0;
}

function usageCoverageDetail(missing) {
  const count = Number(missing || 0);
  return count > 0 ? `${formatCount(count)} missing usage` : "";
}

function routingSummaryWindow(windowValue) {
  return windowValue === "30d" ? "all" : windowValue;
}

function normalizeCredentialRoutingSummary(payload) {
  const routeTargets = arrayItems(payload?.selected_route_targets);
  const channels = arrayItems(payload?.selected_channels);
  const credentials = arrayItems(payload?.selected_credentials);
  const stickyBreaks = payload?.sticky_breaks || {};
  return {
    eventfulTraces: Number(payload?.eventful_traces || 0),
    missingEvents: Number(payload?.legacy_or_missing_events || 0),
    parseErrors: Number(payload?.parse_errors || 0),
    routeTargets,
    channels,
    credentials,
    selectedUpstreams: arrayItems(payload?.selected_upstreams),
    failureReasons: arrayItems(payload?.failure_reasons),
    stickyStatuses: arrayItems(payload?.sticky_statuses),
    stickyBreakTotal: Number(stickyBreaks.total || 0),
    stickyBreakRouteTargets: mergeCountItems(stickyBreaks.previous_route_targets, stickyBreaks.next_route_targets),
    stickyBreakChannels: mergeCountItems(stickyBreaks.previous_channels, stickyBreaks.next_channels),
    stickyBreakCredentials: mergeCountItems(stickyBreaks.previous_credentials, stickyBreaks.next_credentials),
    stickyBreakPreviousRouteTargets: arrayItems(stickyBreaks.previous_route_targets),
    stickyBreakPreviousUpstreams: arrayItems(stickyBreaks.previous_upstreams),
    stickyBreakNextUpstreams: arrayItems(stickyBreaks.next_upstreams),
  };
}

function arrayItems(value) {
  return Array.isArray(value) ? value : [];
}

function mergeCountItems(...groups) {
  const counts = new Map();
  groups.flatMap(arrayItems).forEach((item) => {
    const label = item?.label || "";
    if (!label) {
      return;
    }
    counts.set(label, (counts.get(label) || 0) + Number(item.count || 0));
  });
  return Array.from(counts.entries())
    .map(([label, count]) => ({ label, count }))
    .sort((a, b) => (b.count !== a.count ? b.count - a.count : a.label.localeCompare(b.label)));
}

function CredentialRoutingSummaryPanel({ summary, windowValue }) {
  const hasCredentialData = summary.routeTargets.length || summary.channels.length || summary.credentials.length || summary.stickyBreakTotal > 0;
  const stickyBreakContext = firstNonEmptyItem(summary.stickyBreakRouteTargets, summary.stickyBreakPreviousRouteTargets, summary.stickyBreakPreviousUpstreams);
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Credential routing</p>
          <h2>Event-backed route target summary</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag>{windowValue}</InlineTag>
          <InlineTag tone={summary.eventfulTraces ? "green" : "default"}>{formatCount(summary.eventfulTraces)} eventful</InlineTag>
          {summary.missingEvents ? <InlineTag tone="gold">{formatCount(summary.missingEvents)} legacy / missing</InlineTag> : null}
          {summary.parseErrors ? <InlineTag tone="danger">{formatCount(summary.parseErrors)} parse errors</InlineTag> : null}
        </div>
      </div>
      <div className="hero-grid hero-grid-compact">
        <StatCard label="Route targets" value={formatCount(summary.routeTargets.length)} detail={topCountDetail(summary.routeTargets)} mono />
        <StatCard label="Channels" value={formatCount(summary.channels.length || summary.selectedUpstreams.length)} detail={topCountDetail(summary.channels.length ? summary.channels : summary.selectedUpstreams)} mono />
        <StatCard label="Credentials" value={formatCount(summary.credentials.length)} detail={topCountDetail(summary.credentials)} mono />
        <StatCard label="Sticky breaks" value={formatCount(summary.stickyBreakTotal)} detail={stickyBreakContext ? stickyBreakContext.label : ""} accent={summary.stickyBreakTotal ? "accent-red" : ""} mono />
      </div>
      {hasCredentialData ? (
        <div className="session-breakdown-grid">
          <BreakdownList title="Route targets" items={summary.routeTargets} formatter={(item) => item.label} />
          <BreakdownList title="Channels" items={summary.channels.length ? summary.channels : summary.selectedUpstreams} formatter={(item) => item.label} />
          <BreakdownList title="Credentials" items={summary.credentials} formatter={(item) => item.label} />
          <BreakdownList title="Sticky credential breaks" items={stickyBreakItems(summary)} formatter={(item) => item.label} />
        </div>
      ) : (
        <EmptyState title="No credential routing events" detail="Recent routing events do not include credential fields yet. Legacy route and sticky counts still appear in the selected route list." compact />
      )}
    </section>
  );
}

function topCountDetail(items = []) {
  const first = items[0];
  if (!first?.label) {
    return "";
  }
  return `${first.label} · ${formatCount(first.count || 0)}`;
}

function firstNonEmptyItem(...groups) {
  for (const group of groups) {
    if (group?.[0]?.label) {
      return group[0];
    }
  }
  return null;
}

function stickyBreakItems(summary) {
  if (summary.stickyBreakRouteTargets.length) {
    return summary.stickyBreakRouteTargets;
  }
  if (summary.stickyBreakCredentials.length) {
    return summary.stickyBreakCredentials;
  }
  if (summary.stickyBreakChannels.length) {
    return summary.stickyBreakChannels;
  }
  if (summary.stickyBreakPreviousRouteTargets.length) {
    return summary.stickyBreakPreviousRouteTargets;
  }
  return summary.stickyBreakPreviousUpstreams.length ? summary.stickyBreakPreviousUpstreams : summary.stickyBreakNextUpstreams;
}
