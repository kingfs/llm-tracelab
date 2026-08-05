import React, { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { SessionList } from "../components/monitor/SessionList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatTime, setOrDeleteParam, summarizeSessionItems } from "../lib/monitor";

const REFRESH_MS = 60_000;
const PAGE_SIZE = 50;

export function SessionsPage() {
  const { t } = useI18n();
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
  const { loading, data, error } = useJSON(apiURL(apiPaths.sessions, requestParams), [page, query, provider, model, refreshTick]);

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
  const sessionStats = summarizeSessionItems(items);
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
          <h1>{t("sessions.title")}</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">{t("common.refresh60")}</span>
          <span className="badge">{data?.refreshed_at ? formatTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>
      <section className="hero-grid">
        <StatCard label={t("sessions.title")} value={sessionStats.totalSessions} />
        <StatCard label={t("common.requests")} value={sessionStats.totalRequests} />
        <StatCard label={t("common.tokens")} value={sessionStats.totalTokens} accent="accent-gold" />
        <StatCard label={t("sessions.avgSuccess")} value={`${sessionStats.avgSuccessRate.toFixed(1)}%`} accent="accent-green" />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent sessions</p>
            <h2>{t("sessions.latest")}</h2>
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
            placeholder={t("sessions.search")}
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
          <button className="ghost-button" type="submit">
            {t("common.apply")}
          </button>
          <button className="ghost-button" type="button" onClick={resetFilters}>
            {t("common.reset")}
          </button>
        </form>

        {error ? <EmptyState title={t("sessions.loadError")} detail={error} tone="danger" /> : null}
        {loading && !data ? <EmptyState title={t("sessions.loading")} detail={t("sessions.loadingDetail")} /> : null}

        <SessionList items={items} />
      </section>
    </div>
  );
}
