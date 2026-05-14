import React, { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { InlineTag } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import {
  buildModelLink,
  formatCount,
  formatDateTime,
  formatTime,
  normalizeAnalyticsWindow,
  setOrDeleteParam,
} from "../lib/monitor";

export function ModelsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const queryValue = searchParams.get("q") || "";
  const [queryDraft, setQueryDraft] = useState(queryValue);
  const params = new URLSearchParams();
  params.set("window", windowValue);
  if (queryValue) {
    params.set("q", queryValue);
  }
  const models = useJSON(apiURL(apiPaths.models, params), [windowValue, queryValue]);
  const items = models.data?.items || [];
  const totals = useMemo(() => summarizeModels(items), [items]);

  useEffect(() => {
    setQueryDraft(queryValue);
  }, [queryValue]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };
  const applySearch = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "q", queryDraft);
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Model marketplace</p>
          <h1>Models</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge">{models.data?.refreshed_at ? formatTime(models.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Catalog</p>
            <h2>Seen and configured models</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Model analytics window">
              {["24h", "7d", "30d", "all"].map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <form className="filter-bar" onSubmit={applySearch}>
          <input className="filter-input filter-input-wide" type="search" value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} placeholder="Search model name" />
          <button className="ghost-button" type="submit">Apply</button>
          <button
            className="ghost-button"
            type="button"
            onClick={() => {
              setQueryDraft("");
              const next = new URLSearchParams(searchParams);
              next.delete("q");
              setSearchParams(next);
            }}
          >
            Reset
          </button>
        </form>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Models" value={formatCount(items.length)} />
          <StatCard label="Requests" value={formatCount(totals.requests)} />
          <StatCard label="Failed" value={formatCount(totals.failed)} accent={totals.failed ? "accent-red" : ""} />
          <StatCard label="Tokens" value={formatCount(totals.tokens)} detail={usageCoverageDetail(totals.missing)} />
        </div>
      </section>

      {models.error ? <EmptyState title="Unable to load models" detail={models.error} tone="danger" /> : null}
      {models.loading && !models.data ? <EmptyState title="Loading models" detail="Collecting model catalog and usage summary." /> : null}

      {models.data ? (
        <section className="model-market-grid" aria-label="Model cards">
          {items.length ? items.map((item) => <ModelCard key={item.model} item={item} windowValue={windowValue} />) : <EmptyState title="No models" detail="No model has been discovered, configured, or recorded in the current window." />}
        </section>
      ) : null}
    </div>
  );
}

function ModelCard({ item, windowValue }) {
  const summary = item.summary || {};
  const today = item.today || {};
  const failed = Number(summary.failed_request || 0);
  return (
    <Link className="model-market-card" to={buildModelLink(item.model, windowValue)}>
      <div className="model-market-card-head">
        <div>
          <p className="eyebrow">Model</p>
          <h2>{item.display_name || item.model}</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone="accent">{formatCount(item.enabled_channel_count || 0)} enabled</InlineTag>
          <InlineTag>{formatCount(item.channel_count || 0)} channels</InlineTag>
        </div>
      </div>
      <div className="model-market-metrics">
        <Metric label="requests" value={formatCount(summary.request_count)} />
        <Metric label="errors" value={formatCount(failed)} danger={failed > 0} />
        <Metric label="tokens" value={formatCount(summary.total_tokens)} detail={usageCoverageDetail(summary.missing_usage_request)} />
        <Metric label="today" value={formatCount(today.total_tokens)} detail={usageCoverageDetail(today.missing_usage_request)} />
      </div>
      <div className="model-market-footer">
        <span>{(item.channels || []).slice(0, 4).join(" · ") || "no channel"}</span>
        <span>{formatDateTime(summary.last_seen)}</span>
      </div>
    </Link>
  );
}

function Metric({ label, value, detail = "", danger = false }) {
  return (
    <span className={danger ? "model-market-metric model-market-metric-danger" : "model-market-metric"}>
      <span>{label}</span>
      <strong>{value}</strong>
      {detail ? <small>{detail}</small> : null}
    </span>
  );
}

function summarizeModels(items) {
  return items.reduce(
    (state, item) => {
      const summary = item.summary || {};
      state.requests += Number(summary.request_count || 0);
      state.failed += Number(summary.failed_request || 0);
      state.missing += Number(summary.missing_usage_request || 0);
      state.tokens += Number(summary.total_tokens || 0);
      return state;
    },
    { requests: 0, failed: 0, tokens: 0, missing: 0 },
  );
}

function usageCoverageDetail(missing) {
  const count = Number(missing || 0);
  return count > 0 ? `${formatCount(count)} missing usage` : "";
}
