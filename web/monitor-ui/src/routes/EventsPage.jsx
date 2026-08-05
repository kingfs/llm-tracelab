import React, { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { InlineTag } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { StatCard } from "../components/common/Display";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, postJSON } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { buildTraceLink, formatDateTime, formatFailureReason, MONITOR_WINDOW_OPTIONS, setOrDeleteParam } from "../lib/monitor";

const WINDOW_OPTIONS = MONITOR_WINDOW_OPTIONS;
const DEFAULT_EVENT_WINDOW = "all";
const STATUS_OPTIONS = ["unread", "read", "resolved", "ignored", "all"];
const SEVERITY_OPTIONS = ["all", "critical", "error", "warning", "info"];
const SOURCE_OPTIONS = ["all", "parser", "analyzer", "router", "upstream", "proxy", "recorder", "monitor", "store", "auth", "mcp"];

export function EventsPage() {
  const { language, t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const [refreshTick, setRefreshTick] = useState(0);
  const [selectedID, setSelectedID] = useState("");
  const [busyID, setBusyID] = useState("");
  const params = useMemo(() => eventQueryParams(searchParams), [searchParams]);
  const { loading, data, error } = useJSON(apiURL(apiPaths.events, params), [params.toString(), refreshTick]);
  const summaryWindow = params.get("window") || DEFAULT_EVENT_WINDOW;
  const { data: summary } = useJSON(apiURL(apiPaths.eventsSummary, { window: summaryWindow }), [summaryWindow, refreshTick]);
  const items = data?.items || [];
  const selected = items.find((item) => item.id === selectedID) || items[0] || null;

  const setFilter = (key, value) => {
    const next = new URLSearchParams(searchParams);
    const shouldDelete = key === "window" ? value === DEFAULT_EVENT_WINDOW : value === "all";
    setOrDeleteParam(next, key, shouldDelete ? "" : value);
    if (key !== "page") {
      next.delete("page");
    }
    setSearchParams(next);
  };

  const mutateEvent = async (eventID, action) => {
    const path = {
      read: apiPaths.eventRead(eventID),
      resolve: apiPaths.eventResolve(eventID),
      ignore: apiPaths.eventIgnore(eventID),
    }[action];
    if (!path) {
      return;
    }
    setBusyID(`${eventID}:${action}`);
    try {
      await postJSON(path, {});
      setRefreshTick((tick) => tick + 1);
      window.dispatchEvent(new Event("llm-tracelab:events-refresh"));
    } finally {
      setBusyID("");
    }
  };

  const markAllRead = async () => {
    setBusyID("read-all");
    try {
      await postJSON(apiURL(apiPaths.eventsReadAll, params), {});
      setRefreshTick((tick) => tick + 1);
      window.dispatchEvent(new Event("llm-tracelab:events-refresh"));
    } finally {
      setBusyID("");
    }
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">System health</p>
          <h1>{t("events.title")}</h1>
        </div>
        <div className="topbar-meta">
          <div className="view-toggle" aria-label={t("events.window")}>
            {WINDOW_OPTIONS.map((option) => (
              <button key={option} className={`ghost-button ${currentFilter(searchParams, "window", DEFAULT_EVENT_WINDOW) === option ? "active" : ""}`.trim()} type="button" onClick={() => setFilter("window", option)}>
                {option}
              </button>
            ))}
          </div>
          <button className="ghost-button" type="button" onClick={markAllRead} disabled={busyID === "read-all"}>
            {t("events.markAllRead")}
          </button>
          <span className="badge">{data?.refreshed_at ? formatDateTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>

      {error ? <EmptyState title={t("events.loadError")} detail={error} tone="danger" /> : null}
      {loading && !data ? <EmptyState title={t("events.loading")} detail={t("events.loadingDetail")} /> : null}

      <section className="hero-grid overview-kpi-grid">
        <StatCard label={t("events.unread")} value={summary?.unread ?? 0} detail={t("events.totalEvents", { count: summary?.total ?? 0 })} accent={(summary?.unread ?? 0) ? "accent-red" : "accent-green"} />
        <StatCard label={t("events.critical")} value={summary?.critical ?? 0} detail={t("events.unreadCritical")} accent={(summary?.critical ?? 0) ? "accent-red" : ""} />
        <StatCard label={t("events.error")} value={summary?.error ?? 0} detail={t("events.unreadErrors")} accent={(summary?.error ?? 0) ? "accent-red" : ""} />
        <StatCard label={t("events.warning")} value={summary?.warning ?? 0} detail={t("events.unreadWarnings")} accent={(summary?.warning ?? 0) ? "accent-gold" : ""} />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Inbox filters</p>
            <h2>{t("events.runtimeExceptions")}</h2>
          </div>
          <span className="badge">{t("events.matching", { count: data?.total ?? 0 })}</span>
        </div>
        <div className="filter-bar event-filter-bar">
          <select className="filter-input" value={currentFilter(searchParams, "status", "unread")} onChange={(event) => setFilter("status", event.target.value)} aria-label={t("events.status")}>
            {STATUS_OPTIONS.map((option) => <option key={option} value={option}>{formatEventOption(option, t, language)}</option>)}
          </select>
          <select className="filter-input" value={currentFilter(searchParams, "severity", "all")} onChange={(event) => setFilter("severity", event.target.value)} aria-label={t("events.severity")}>
            {SEVERITY_OPTIONS.map((option) => <option key={option} value={option}>{formatEventOption(option, t, language)}</option>)}
          </select>
          <select className="filter-input" value={currentFilter(searchParams, "source", "all")} onChange={(event) => setFilter("source", event.target.value)} aria-label={t("events.source")}>
            {SOURCE_OPTIONS.map((option) => <option key={option} value={option}>{formatEventOption(option, t, language)}</option>)}
          </select>
          <input className="filter-input filter-input-wide" type="search" value={searchParams.get("q") || ""} onChange={(event) => setFilter("q", event.target.value)} placeholder={t("events.search")} />
        </div>
      </section>

      <div className="event-workspace">
        <section className="panel event-list-panel">
          <div className="event-list">
            {items.length ? items.map((item) => (
              <button key={item.id} className={selected?.id === item.id ? "event-row event-row-active" : "event-row"} type="button" onClick={() => setSelectedID(item.id)}>
                <span className={`event-severity event-severity-${item.severity || "info"}`} />
                <span className="event-row-main">
                  <strong>{item.title || item.fingerprint}</strong>
                  <span>{item.message || item.fingerprint}</span>
                </span>
                <span className="event-row-meta">
                  <InlineTag tone={severityTone(item.severity)}>{item.severity}</InlineTag>
                  <InlineTag>{item.status}</InlineTag>
                  <small>{formatDateTime(item.last_seen_at)}</small>
                </span>
              </button>
            )) : <EmptyState title={t("events.noMatch")} detail={t("events.noMatchDetail")} compact />}
          </div>
        </section>

        <section className="panel event-detail-panel">
          {selected ? (
            <EventDetail event={selected} busyID={busyID} onAction={mutateEvent} />
          ) : (
            <EmptyState title={t("events.noSelected")} detail={t("events.noSelectedDetail")} />
          )}
        </section>
      </div>
    </div>
  );
}

function EventDetail({ event, busyID, onAction }) {
  const { t } = useI18n();
  return (
    <div className="event-detail">
      <div className="panel-head event-detail-head">
        <div>
          <p className="eyebrow">{event.source} / {formatFailureReason(event.category)}</p>
          <h2>{event.title || event.fingerprint}</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={severityTone(event.severity)}>{event.severity}</InlineTag>
          <InlineTag>{event.status}</InlineTag>
        </div>
      </div>
      <p className="event-message">{event.message || event.fingerprint}</p>
      <div className="detail-meta-strip event-meta-strip">
        <Meta label="occurrences" value={event.occurrence_count ?? 1} />
        <Meta label="first seen" value={formatDateTime(event.first_seen_at)} />
        <Meta label="last seen" value={formatDateTime(event.last_seen_at)} />
        <Meta label="fingerprint" value={event.fingerprint} mono />
      </div>
      <div className="trace-tag-group event-links">
        {event.trace_id ? <Link className="ghost-button" to={buildTraceLink(event.trace_id, "events", event.session_id || "", "protocol", "observation")}>{t("events.trace")}</Link> : null}
        {event.session_id ? <Link className="ghost-button" to={`/sessions/${encodeURIComponent(event.session_id)}`}>{t("events.session")}</Link> : null}
        {event.upstream_id ? <Link className="ghost-button" to={`/upstreams/${encodeURIComponent(event.upstream_id)}`}>{t("events.upstream")}</Link> : null}
      </div>
      <div className="event-actions">
        <button className="ghost-button" type="button" onClick={() => onAction(event.id, "read")} disabled={busyID === `${event.id}:read` || event.status === "read"}>{t("events.markRead")}</button>
        <button className="ghost-button" type="button" onClick={() => onAction(event.id, "resolve")} disabled={busyID === `${event.id}:resolve` || event.status === "resolved"}>{t("events.resolve")}</button>
        <button className="ghost-button" type="button" onClick={() => onAction(event.id, "ignore")} disabled={busyID === `${event.id}:ignore` || event.status === "ignored"}>{t("events.ignore")}</button>
      </div>
      <pre className="code-block event-details-json">{formatJSON(event.details_json)}</pre>
    </div>
  );
}

function Meta({ label, value, mono = false }) {
  return <span className="detail-meta-pill"><span className="detail-meta-label">{label}</span><strong className={mono ? "mono" : ""}>{value || "-"}</strong></span>;
}

function formatEventOption(option, t, language) {
  if (language === "en") {
    return option;
  }
  const map = {
    all: "全部",
    unread: "未读",
    read: "已读",
    resolved: "已解决",
    ignored: "已忽略",
    critical: t("events.critical"),
    error: t("events.error"),
    warning: t("events.warning"),
    info: "信息",
  };
  return map[option] || option;
}

function eventQueryParams(searchParams) {
  const params = new URLSearchParams();
  params.set("window", currentFilter(searchParams, "window", DEFAULT_EVENT_WINDOW));
  params.set("status", currentFilter(searchParams, "status", "unread"));
  for (const key of ["severity", "source", "category", "q", "page"]) {
    const value = searchParams.get(key);
    if (value && value !== "all") {
      params.set(key, value);
    }
  }
  params.set("page_size", "50");
  return params;
}

function currentFilter(searchParams, key, fallback) {
  return searchParams.get(key) || fallback;
}

function severityTone(severity = "") {
  switch (severity) {
    case "critical":
    case "error":
      return "danger";
    case "warning":
      return "gold";
    default:
      return "default";
  }
}

function formatJSON(value) {
  if (!value) {
    return "{}";
  }
  try {
    return JSON.stringify(typeof value === "string" ? JSON.parse(value) : value, null, 2);
  } catch {
    return String(value);
  }
}
