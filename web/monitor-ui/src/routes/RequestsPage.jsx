import React, { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { PrimaryNav } from "../components/PrimaryNav";
import { StatCard } from "../components/common/Display";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { formatTime, setOrDeleteParam } from "../lib/monitor";

const REFRESH_MS = 60_000;
const PAGE_SIZE = 50;

export function RequestsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(1, Number(searchParams.get("page") || "1"));
  const query = searchParams.get("q") || "";
  const provider = searchParams.get("provider") || "";
  const model = searchParams.get("model") || "";
  const [refreshTick, setRefreshTick] = useState(0);
  const [filters, setFilters] = useState({ query, provider, model });
  const requestParams = new URLSearchParams({
    page: String(page),
    page_size: String(PAGE_SIZE),
  });
  if (query) {
    requestParams.set("q", query);
  }
  if (provider) {
    requestParams.set("provider", provider);
  }
  if (model) {
    requestParams.set("model", model);
  }
  const { loading, data, error } = useJSON(`/api/traces?${requestParams.toString()}`, [page, query, provider, model, refreshTick]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setRefreshTick((tick) => tick + 1);
    }, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    setFilters({ query, provider, model });
  }, [query, provider, model]);

  const items = data?.items ?? [];
  const stats = data?.stats ?? {};
  const goToPage = (nextPage) => {
    const next = new URLSearchParams(searchParams);
    next.set("page", String(nextPage));
    setSearchParams(next);
  };
  const applyFilters = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    next.set("page", "1");
    setOrDeleteParam(next, "q", filters.query);
    setOrDeleteParam(next, "provider", filters.provider);
    setOrDeleteParam(next, "model", filters.model);
    setSearchParams(next);
  };
  const resetFilters = () => {
    setFilters({ query: "", provider: "", model: "" });
    const next = new URLSearchParams(searchParams);
    next.set("page", "1");
    next.delete("q");
    next.delete("provider");
    next.delete("model");
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Local First LLM Replay Proxy</p>
          <h1>Requests</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">refresh / 60s</span>
          <span className="badge">{data?.refreshed_at ? formatTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>
      <PrimaryNav />

      <section className="hero-grid">
        <StatCard label="Total" value={stats.total_request ?? 0} />
        <StatCard label="Avg TTFT" value={`${stats.avg_ttft ?? 0} ms`} />
        <StatCard label="Tokens" value={stats.total_tokens ?? 0} accent="accent-gold" />
        <StatCard label="Success" value={`${Number(stats.success_rate ?? 0).toFixed(1)}%`} accent="accent-green" />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent traffic</p>
            <h2>Latest 50 traces</h2>
          </div>
          <div className="panel-head-actions">
            <div className="pager">
              <button className="ghost-button" disabled={page <= 1} onClick={() => goToPage(page - 1)}>
                Previous
              </button>
              <span className="pager-label">
                {data?.page ?? page} / {Math.max(data?.total_pages ?? 1, 1)}
              </span>
              <button className="ghost-button" disabled={!data || page >= (data.total_pages || 1)} onClick={() => goToPage(page + 1)}>
                Next
              </button>
            </div>
          </div>
        </div>
        <form className="filter-bar" onSubmit={applyFilters}>
          <input
            className="filter-input filter-input-wide"
            type="search"
            placeholder="Search trace id, session id, model"
            value={filters.query}
            onChange={(event) => setFilters((current) => ({ ...current, query: event.target.value }))}
          />
          <input
            className="filter-input"
            type="text"
            placeholder="provider"
            value={filters.provider}
            onChange={(event) => setFilters((current) => ({ ...current, provider: event.target.value }))}
          />
          <input
            className="filter-input"
            type="text"
            placeholder="model"
            value={filters.model}
            onChange={(event) => setFilters((current) => ({ ...current, model: event.target.value }))}
          />
          <button className="ghost-button" type="submit">
            Apply
          </button>
          <button className="ghost-button" type="button" onClick={resetFilters}>
            Reset
          </button>
        </form>

        {error ? <div className="empty-state error-box">{error}</div> : null}
        {loading && !data ? <div className="empty-state">Loading requests...</div> : null}

        <RequestList items={items} fromView="requests" />
      </section>
    </div>
  );
}
