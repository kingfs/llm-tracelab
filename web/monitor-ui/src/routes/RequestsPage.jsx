import React, { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatDuration, formatTime, formatTokenCount, setOrDeleteParam } from "../lib/monitor";

const REFRESH_MS = 60_000;
const PAGE_SIZE = 50;

export function RequestsPage() {
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(1, Number(searchParams.get("page") || "1"));
  const query = searchParams.get("q") || "";
  const provider = searchParams.get("provider") || "";
  const model = searchParams.get("model") || "";
  const observation = searchParams.get("observation") || "";
  const [refreshTick, setRefreshTick] = useState(0);
  const [filters, setFilters] = useState({ query, provider, model, observation });
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
  if (observation) {
    requestParams.set("observation", observation);
  }
  const { loading, data, error } = useJSON(apiURL(apiPaths.traces, requestParams), [page, query, provider, model, observation, refreshTick]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setRefreshTick((tick) => tick + 1);
    }, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    setFilters({ query, provider, model, observation });
  }, [query, provider, model, observation]);

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
    setOrDeleteParam(next, "observation", filters.observation);
    setSearchParams(next);
  };
  const resetFilters = () => {
    setFilters({ query: "", provider: "", model: "", observation: "" });
    const next = new URLSearchParams(searchParams);
    next.set("page", "1");
    next.delete("q");
    next.delete("provider");
    next.delete("model");
    next.delete("observation");
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Local First LLM Replay Proxy</p>
          <h1>{t("requests.title")}</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">{t("common.refresh60")}</span>
          <span className="badge">{data?.refreshed_at ? formatTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>
      <section className="hero-grid">
        <StatCard label={t("common.total")} value={stats.total_request ?? 0} />
        <StatCard label="Avg TTFT" value={formatDuration(stats.avg_ttft ?? 0)} title={`${stats.avg_ttft ?? 0} ms`} />
        <StatCard label={t("common.tokens")} value={formatTokenCount(stats.total_tokens ?? 0)} accent="accent-gold" title={String(stats.total_tokens ?? 0)} />
        <StatCard label={t("common.success")} value={`${Number(stats.success_rate ?? 0).toFixed(1)}%`} accent="accent-green" />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent traffic</p>
            <h2>{t("requests.latest")}</h2>
          </div>
          <div className="panel-head-actions">
            <div className="pager">
              <button className="ghost-button" disabled={page <= 1} onClick={() => goToPage(page - 1)}>
                {t("common.previous")}
              </button>
              <span className="pager-label">
                {data?.page ?? page} / {Math.max(data?.total_pages ?? 1, 1)}
              </span>
              <button className="ghost-button" disabled={!data || page >= (data.total_pages || 1)} onClick={() => goToPage(page + 1)}>
                {t("common.next")}
              </button>
            </div>
          </div>
        </div>
        <form className="filter-bar" onSubmit={applyFilters}>
          <input
            className="filter-input filter-input-wide"
            type="search"
            placeholder={t("requests.search")}
            value={filters.query}
            onChange={(event) => setFilters((current) => ({ ...current, query: event.target.value }))}
          />
          <input
            className="filter-input"
            type="text"
            placeholder={t("sessions.provider")}
            value={filters.provider}
            onChange={(event) => setFilters((current) => ({ ...current, provider: event.target.value }))}
          />
          <input
            className="filter-input"
            type="text"
            placeholder={t("sessions.model")}
            value={filters.model}
            onChange={(event) => setFilters((current) => ({ ...current, model: event.target.value }))}
          />
          <select
            className="filter-input filter-select"
            value={filters.observation}
            onChange={(event) => setFilters((current) => ({ ...current, observation: event.target.value }))}
            aria-label={t("requests.observationStatus")}
          >
            <option value="">{t("requests.allObservations")}</option>
            <option value="unparsed">{t("requests.unparsed")}</option>
            <option value="parsed">{t("requests.parsed")}</option>
            <option value="failed">{t("requests.parseFailed")}</option>
            <option value="queued">{t("requests.parseQueued")}</option>
            <option value="running">{t("requests.parseRunning")}</option>
          </select>
          <button className="ghost-button" type="submit">
            {t("common.apply")}
          </button>
          <button className="ghost-button" type="button" onClick={resetFilters}>
            {t("common.reset")}
          </button>
        </form>

        {error ? <EmptyState title={t("requests.loadError")} detail={error} tone="danger" /> : null}
        {loading && !data ? <EmptyState title={t("requests.loading")} detail={t("requests.loadingDetail")} /> : null}

        <RequestList items={items} fromView="requests" />
      </section>
    </div>
  );
}
