import React from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DetailMetaPill, HomeIcon, InlineTag } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { SingleUsageCharts } from "../components/common/Charts";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import {
  buildChannelLink,
  formatCount,
  formatDateTime,
  formatTime,
  normalizeAnalyticsWindow,
  setOrDeleteParam,
} from "../lib/monitor";

export function ModelDetailPage() {
  const { model = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const detail = useJSON(apiURL(apiPaths.model(model), params), [model, windowValue]);
  const modelItem = detail.data?.model || {};
  const summary = modelItem.summary || {};
  const trends = detail.data?.trends || [];
  const channels = detail.data?.channels || [];

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{modelItem.display_name || model}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone="accent">{formatCount(modelItem.enabled_channel_count || 0)} enabled</InlineTag>
              <InlineTag>{formatCount(modelItem.channel_count || 0)} channels</InlineTag>
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label="requests" value={formatCount(summary.request_count)} />
            <DetailMetaPill label="tokens" value={formatCount(summary.total_tokens)} />
            <DetailMetaPill label="failed" value={formatCount(summary.failed_request)} />
            <DetailMetaPill label="last seen" value={formatDateTime(summary.last_seen)} />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to="/models" title="Back to models" aria-label="Back to models">
              <HomeIcon />
            </Link>
          </div>
          <span className="badge">{detail.data?.refreshed_at ? formatTime(detail.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Analytics</p>
            <h2>Usage window</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Model detail window">
              {["24h", "7d", "30d", "all"].map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Requests" value={formatCount(summary.request_count)} />
          <StatCard label="Errors" value={formatCount(summary.failed_request)} accent={summary.failed_request ? "accent-red" : ""} />
          <StatCard label="Tokens" value={formatCount(summary.total_tokens)} />
          <StatCard label="Today tokens" value={formatCount(modelItem.today?.total_tokens)} />
        </div>
      </section>

      {detail.error ? <EmptyState title="Unable to load model detail" detail={detail.error} tone="danger" /> : null}
      {detail.loading && !detail.data ? <EmptyState title="Loading model detail" detail="Collecting channel coverage and usage trend." /> : null}

      {detail.data ? (
        <>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Trend</p>
                <h2>Requests and tokens</h2>
              </div>
            </div>
            <SingleUsageCharts items={trends} />
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Channels</p>
                <h2>Provider coverage</h2>
              </div>
            </div>
            <div className="channel-model-table">
              {channels.length ? channels.map((channel) => <ModelChannelRow key={channel.channel_id} item={channel} windowValue={windowValue} />) : <EmptyState title="No channels" detail="This model is not currently linked to any channel." compact />}
            </div>
          </section>
        </>
      ) : null}
    </div>
  );
}

function ModelChannelRow({ item, windowValue }) {
  const summary = item.summary || {};
  return (
    <Link className="channel-model-row" to={buildChannelLink(item.channel_id, windowValue)}>
      <div>
        <strong>{item.channel_id}</strong>
        <span>{item.source || "unknown"}</span>
      </div>
      <InlineTag tone={item.enabled ? "green" : "default"}>{item.enabled ? "enabled" : "disabled"}</InlineTag>
      <span>{formatCount(summary.request_count)} req</span>
      <span>{formatCount(summary.failed_request)} err</span>
      <span>{formatCount(summary.total_tokens)} tok</span>
    </Link>
  );
}
