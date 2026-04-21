import React, { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { PrimaryNav } from "../components/PrimaryNav";
import { EmptyState } from "../components/common/EmptyState";
import { UpstreamOverview } from "../components/routing/UpstreamOverview";
import { useJSON } from "../hooks/useJSON";
import { formatTime, normalizeUpstreamWindow, setOrDeleteParam } from "../lib/monitor";

const REFRESH_MS = 60_000;

export function RoutingPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const upstreamWindow = normalizeUpstreamWindow(searchParams.get("window"));
  const upstreamModel = searchParams.get("model") || "";
  const [refreshTick, setRefreshTick] = useState(0);
  const [filters, setFilters] = useState({ model: upstreamModel });
  const upstreamParams = new URLSearchParams();
  upstreamParams.set("window", upstreamWindow);
  if (upstreamModel) {
    upstreamParams.set("model", upstreamModel);
  }
  const upstreams = useJSON(`/api/upstreams?${upstreamParams.toString()}`, [refreshTick, upstreamWindow, upstreamModel]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setRefreshTick((tick) => tick + 1);
    }, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    setFilters({ model: upstreamModel });
  }, [upstreamModel]);

  const setUpstreamWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };
  const applyFilters = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "model", filters.model);
    setSearchParams(next);
  };
  const resetFilters = () => {
    setFilters({ model: "" });
    const next = new URLSearchParams(searchParams);
    next.delete("model");
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Local First LLM Replay Proxy</p>
          <h1>Routing</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">refresh / 60s</span>
          <span className="badge">{upstreams.data?.refreshed_at ? formatTime(upstreams.data.refreshed_at) : "..."}</span>
        </div>
      </header>
      <PrimaryNav />

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Routing surface</p>
            <h2>Active upstream targets</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Upstream analytics window">
              {["1h", "24h", "7d", "all"].map((window) => (
                <button
                  key={window}
                  className={upstreamWindow === window ? "ghost-button active" : "ghost-button"}
                  onClick={() => setUpstreamWindow(window)}
                >
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <form className="filter-bar" onSubmit={applyFilters}>
          <input
            className="filter-input filter-input-wide"
            type="search"
            name="upstream_model"
            placeholder="Filter upstream analytics by model"
            value={filters.model}
            onChange={(event) => setFilters({ model: event.target.value })}
          />
          <button className="ghost-button" type="submit">
            Apply
          </button>
          <button className="ghost-button" type="button" onClick={resetFilters}>
            Reset
          </button>
        </form>
        {upstreams.error ? <EmptyState title="Unable to load routing targets" detail={upstreams.error} tone="danger" /> : null}
        {upstreams.loading && !upstreams.data ? <EmptyState title="Loading routing surface" detail="Collecting indexed upstream health and routing statistics." /> : null}
        {upstreams.data ? (
          <UpstreamOverview
            items={upstreams.data.items || []}
            analyticsWindow={upstreams.data.window || upstreamWindow}
            analyticsModel={upstreams.data.model || upstreamModel}
            routingFailures={upstreams.data.routing_failures || {}}
          />
        ) : null}
      </section>
    </div>
  );
}
