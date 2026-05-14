import React, { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import { formatCount, formatTime, setOrDeleteParam } from "../lib/monitor";

const REFRESH_MS = 60_000;
const WINDOW_OPTIONS = ["24h", "7d", "30d", "all"];

export function RoutingPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeRoutingWindow(searchParams.get("window"));
  const modelValue = searchParams.get("model") || "";
  const upstreamValue = searchParams.get("upstream") || "";
  const [refreshTick, setRefreshTick] = useState(0);
  const [filters, setFilters] = useState({ model: modelValue, upstream: upstreamValue });
  const params = new URLSearchParams();
  params.set("page", "1");
  params.set("page_size", "100");
  if (modelValue) {
    params.set("model", modelValue);
  }
  if (upstreamValue) {
    params.set("upstream", upstreamValue);
  }
  const traces = useJSON(apiURL(apiPaths.traces, params), [refreshTick, windowValue, modelValue, upstreamValue]);
  const routedItems = useMemo(() => filterByWindow(traces.data?.items || [], windowValue), [traces.data, windowValue]);
  const summary = useMemo(() => summarizeRouting(routedItems), [routedItems]);

  useEffect(() => {
    const timer = window.setInterval(() => setRefreshTick((tick) => tick + 1), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    setFilters({ model: modelValue, upstream: upstreamValue });
  }, [modelValue, upstreamValue]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };
  const applyFilters = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "model", filters.model);
    setOrDeleteParam(next, "upstream", filters.upstream);
    setSearchParams(next);
  };
  const resetFilters = () => {
    setFilters({ model: "", upstream: "" });
    const next = new URLSearchParams(searchParams);
    next.delete("model");
    next.delete("upstream");
    setSearchParams(next);
  };

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
        <form className="filter-bar" onSubmit={applyFilters}>
          <input className="filter-input" type="search" name="routing_model" placeholder="Model" value={filters.model} onChange={(event) => setFilters((current) => ({ ...current, model: event.target.value }))} />
          <input className="filter-input" type="search" name="routing_upstream" placeholder="Channel / upstream" value={filters.upstream} onChange={(event) => setFilters((current) => ({ ...current, upstream: event.target.value }))} />
          <button className="ghost-button" type="submit">Apply</button>
          <button className="ghost-button" type="button" onClick={resetFilters}>Reset</button>
        </form>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Routed requests" value={formatCount(summary.requests)} />
          <StatCard label="Channels" value={formatCount(summary.channels)} />
          <StatCard label="Errors" value={formatCount(summary.errors)} accent={summary.errors ? "accent-red" : ""} />
          <StatCard label="Tokens" value={formatCount(summary.tokens)} />
        </div>
      </section>

      {traces.error ? <EmptyState title="Unable to load routing records" detail={traces.error} tone="danger" /> : null}
      {traces.loading && !traces.data ? <EmptyState title="Loading routing records" detail="Reading recent traces with selected channels, status, tokens, duration, and TTFT." /> : null}
      {traces.data ? <RequestList items={routedItems} fromView="routing" focusFailures /> : null}
    </div>
  );
}

function normalizeRoutingWindow(value) {
  return WINDOW_OPTIONS.includes(value) ? value : "24h";
}

function filterByWindow(items, windowValue) {
  if (windowValue === "all") {
    return items.filter((item) => item.selected_upstream_id);
  }
  const durationMs = windowValue === "7d" ? 7 * 24 * 60 * 60 * 1000 : windowValue === "30d" ? 30 * 24 * 60 * 60 * 1000 : 24 * 60 * 60 * 1000;
  const since = Date.now() - durationMs;
  return items.filter((item) => item.selected_upstream_id && new Date(item.recorded_at).getTime() >= since);
}

function summarizeRouting(items) {
  const channels = new Set();
  return items.reduce((state, item) => {
    state.requests += 1;
    state.tokens += Number(item.total_tokens || 0);
    if (item.status_code < 200 || item.status_code >= 300) {
      state.errors += 1;
    }
    if (item.selected_upstream_id) {
      channels.add(item.selected_upstream_id);
    }
    state.channels = channels.size;
    return state;
  }, { requests: 0, errors: 0, tokens: 0, channels: 0 });
}
