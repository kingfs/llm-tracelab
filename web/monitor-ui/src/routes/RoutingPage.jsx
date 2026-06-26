import React, { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { InlineTag } from "../components/common/Badges";
import { BreakdownList } from "../components/monitor/BreakdownList";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, patchJSON, postJSON, requestJSON } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatCount, formatTime, MONITOR_WINDOW_OPTIONS, setOrDeleteParam } from "../lib/monitor";

const REFRESH_MS = 60_000;
const WINDOW_OPTIONS = MONITOR_WINDOW_OPTIONS;
const FILTER_KEYS = ["model", "upstream", "status", "min_duration_ms", "max_duration_ms", "min_ttft_ms", "max_ttft_ms", "min_tokens", "max_tokens"];
const ROUTING_TABS = ["decisions", "settings", "aliases", "inspect"];

export function RoutingPage() {
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeRoutingWindow(searchParams.get("window"));
  const activeTab = normalizeRoutingTab(searchParams.get("tab"));
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
  const traces = useJSON(apiURL(apiPaths.routingExchanges, params), [refreshTick, windowValue, ...FILTER_KEYS.map((key) => activeFilters[key])]);
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
  const setTab = (nextTab) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "tab", nextTab === "decisions" ? "" : nextTab);
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
          <h1>{t("routing.title")}</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">{t("common.refresh60")}</span>
          <span className="badge">{traces.data?.refreshed_at ? formatTime(traces.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Gateway routing</p>
            <h2>Workspace</h2>
          </div>
        </div>
        <div className="view-toggle routing-mode-toggle" role="tablist" aria-label="Routing workspace">
          {ROUTING_TABS.map((tab) => (
            <button key={tab} className={activeTab === tab ? "ghost-button active" : "ghost-button"} type="button" onClick={() => setTab(tab)}>
              {routingTabLabel(tab)}
            </button>
          ))}
        </div>
      </section>

      {activeTab === "settings" ? <RoutingSettingsPanel /> : null}
      {activeTab === "aliases" ? <ModelAliasesPanel /> : null}
      {activeTab === "inspect" ? <RouteInspectorPanel /> : null}

      {activeTab === "decisions" ? (
        <>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Decision log</p>
                <h2>{t("routing.recent")}</h2>
              </div>
              <div className="panel-head-actions">
                <div className="view-toggle" role="tablist" aria-label={t("routing.window")}>
                  {WINDOW_OPTIONS.map((window) => (
                    <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                      {window}
                    </button>
                  ))}
                </div>
              </div>
            </div>
            <form className="filter-bar routing-filter-bar" onSubmit={applyFilters}>
          <input className="filter-input" type="search" name="routing_model" placeholder={t("routing.model")} value={filters.model} onChange={(event) => updateFilter("model", event.target.value)} />
          <input className="filter-input" type="search" name="routing_upstream" placeholder={t("routing.channelUpstream")} value={filters.upstream} onChange={(event) => updateFilter("upstream", event.target.value)} />
          <select className="filter-input" name="routing_status" aria-label="Routing status" value={filters.status} onChange={(event) => updateFilter("status", event.target.value)}>
            <option value="">{t("routing.anyStatus")}</option>
            <option value="success">{t("routing.statusSuccess")}</option>
            <option value="error">{t("routing.statusError")}</option>
          </select>
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_min_duration" placeholder={t("routing.minDuration")} value={filters.min_duration_ms} onChange={(event) => updateFilter("min_duration_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_max_duration" placeholder={t("routing.maxDuration")} value={filters.max_duration_ms} onChange={(event) => updateFilter("max_duration_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_min_ttft" placeholder={t("routing.minTTFT")} value={filters.min_ttft_ms} onChange={(event) => updateFilter("min_ttft_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_max_ttft" placeholder={t("routing.maxTTFT")} value={filters.max_ttft_ms} onChange={(event) => updateFilter("max_ttft_ms", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_min_tokens" placeholder={t("routing.minTokens")} value={filters.min_tokens} onChange={(event) => updateFilter("min_tokens", event.target.value)} />
          <input className="filter-input filter-input-small" type="number" min="0" name="routing_max_tokens" placeholder={t("routing.maxTokens")} value={filters.max_tokens} onChange={(event) => updateFilter("max_tokens", event.target.value)} />
          <button className="ghost-button" type="submit">{t("common.apply")}</button>
          <button className="ghost-button" type="button" onClick={resetFilters}>{t("common.reset")}</button>
        </form>
        <div className="hero-grid hero-grid-compact">
          <StatCard label={t("routing.routedRequests")} value={formatCount(summary.requests)} />
          <StatCard label={t("routing.channels")} value={formatCount(summary.channels)} />
          <StatCard label={t("common.errors")} value={formatCount(summary.errors)} accent={summary.errors ? "accent-red" : ""} />
          <StatCard label={t("common.tokens")} value={formatCount(summary.tokens)} detail={usageCoverageDetail(summary.missing, t)} />
        </div>
          </section>

          {traces.error ? <EmptyState title={t("routing.loadError")} detail={traces.error} tone="danger" /> : null}
          {routingSummary.error ? <EmptyState title={t("routing.summaryError")} detail={routingSummary.error} tone="danger" compact /> : null}
          {traces.loading && !traces.data ? <EmptyState title={t("routing.loading")} detail={t("routing.loadingDetail")} /> : null}
          {routingSummary.data ? <CredentialRoutingSummaryPanel summary={credentialSummary} windowValue={windowValue} /> : null}
          {traces.data ? <RequestList items={routedItems} fromView="routing" focusFailures /> : null}
        </>
      ) : null}
    </div>
  );
}

function normalizeRoutingTab(value) {
  return ROUTING_TABS.includes(value) ? value : "decisions";
}

function routingTabLabel(tab) {
  switch (tab) {
    case "settings":
      return "Settings";
    case "aliases":
      return "Aliases";
    case "inspect":
      return "Inspector";
    default:
      return "Decisions";
  }
}

function RoutingSettingsPanel() {
  const [settings, setSettings] = useState({ responses_strategy: "auto", selection_policy: "p2c", missing_model_policy: "reject" });
  const [status, setStatus] = useState({ loading: true, error: "", saved: false });

  useEffect(() => {
    let cancelled = false;
    requestJSON(apiPaths.routingSettings)
      .then((payload) => {
        if (!cancelled) {
          setSettings({ ...settings, ...payload });
          setStatus({ loading: false, error: "", saved: false });
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setStatus({ loading: false, error: error.message, saved: false });
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const update = (key, value) => setSettings((current) => ({ ...current, [key]: value }));
  const save = async (event) => {
    event.preventDefault();
    setStatus({ loading: false, error: "", saved: false });
    try {
      const payload = await patchJSON(apiPaths.routingSettings, settings);
      setSettings({ ...settings, ...payload });
      setStatus({ loading: false, error: "", saved: true });
    } catch (error) {
      setStatus({ loading: false, error: error.message, saved: false });
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">System policy</p>
          <h2>Routing settings</h2>
        </div>
        {status.saved ? <InlineTag tone="green">Saved</InlineTag> : null}
      </div>
      {status.error ? <EmptyState title="Routing settings API unavailable" detail={status.error} compact /> : null}
      <form className="filter-bar routing-filter-bar" onSubmit={save}>
        <label className="filter-label">
          Responses strategy
          <select className="filter-input" value={settings.responses_strategy || "auto"} onChange={(event) => update("responses_strategy", event.target.value)}>
            <option value="auto">auto</option>
            <option value="prefer_native">prefer_native</option>
            <option value="prefer_local_server">prefer_local_server</option>
            <option value="native_only">native_only</option>
            <option value="local_server_only">local_server_only</option>
          </select>
        </label>
        <label className="filter-label">
          Selection policy
          <select className="filter-input" value={settings.selection_policy || "p2c"} onChange={(event) => update("selection_policy", event.target.value)}>
            <option value="p2c">p2c</option>
            <option value="first_available">first_available</option>
          </select>
        </label>
        <label className="filter-label">
          Missing model
          <select className="filter-input" value={settings.missing_model_policy || "reject"} onChange={(event) => update("missing_model_policy", event.target.value)}>
            <option value="reject">reject</option>
            <option value="fallback">fallback</option>
          </select>
        </label>
        <button className="ghost-button" type="submit">Save</button>
      </form>
    </section>
  );
}

function ModelAliasesPanel() {
  const [refreshTick, setRefreshTick] = useState(0);
  const aliases = useJSON(apiPaths.modelAliases, [refreshTick]);
  const [form, setForm] = useState({ alias: "", target_model: "", channel_id: "" });
  const [submitError, setSubmitError] = useState("");
  const items = Array.isArray(aliases.data?.items) ? aliases.data.items : [];
  const update = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const create = async (event) => {
    event.preventDefault();
    setSubmitError("");
    try {
      await postJSON(apiPaths.modelAliases, form);
      setForm({ alias: "", target_model: "", channel_id: "" });
      setRefreshTick((tick) => tick + 1);
    } catch (error) {
      setSubmitError(error.message);
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Model resolution</p>
          <h2>Model aliases</h2>
        </div>
        <InlineTag>{formatCount(items.length)} aliases</InlineTag>
      </div>
      {aliases.error ? <EmptyState title="Model aliases API unavailable" detail={aliases.error} compact /> : null}
      <form className="filter-bar routing-filter-bar" onSubmit={create}>
        <input className="filter-input" placeholder="Alias, e.g. abc" value={form.alias} onChange={(event) => update("alias", event.target.value)} />
        <input className="filter-input" placeholder="Target model, e.g. gpt-5.5" value={form.target_model} onChange={(event) => update("target_model", event.target.value)} />
        <input className="filter-input" placeholder="Optional channel" value={form.channel_id} onChange={(event) => update("channel_id", event.target.value)} />
        <button className="ghost-button" type="submit">Create</button>
      </form>
      {submitError ? <p className="event-message">{submitError}</p> : null}
      {items.length ? (
        <div className="session-breakdown-grid">
          {items.map((item) => (
            <div className="metric-card" key={item.id || `${item.alias}:${item.channel_id}:${item.target_model}`}>
              <span>{item.channel_id || "global"}</span>
              <strong>{item.alias} to {item.target_model}</strong>
              <small>{item.enabled === false ? "disabled" : "enabled"}</small>
            </div>
          ))}
        </div>
      ) : !aliases.error ? <EmptyState title="No aliases configured" detail="Create aliases here once the backend API is enabled." compact /> : null}
    </section>
  );
}

function RouteInspectorPanel() {
  const [form, setForm] = useState({ endpoint: "responses", model: "", stream: false, tools: false });
  const [result, setResult] = useState(null);
  const [error, setError] = useState("");
  const update = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const inspect = async (event) => {
    event.preventDefault();
    setError("");
    setResult(null);
    try {
      setResult(await postJSON(apiPaths.routingInspect, form));
    } catch (err) {
      setError(err.message);
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Dry run</p>
          <h2>Route inspector</h2>
        </div>
      </div>
      <form className="filter-bar routing-filter-bar" onSubmit={inspect}>
        <select className="filter-input" value={form.endpoint} onChange={(event) => update("endpoint", event.target.value)}>
          <option value="chat_completions">chat_completions</option>
          <option value="responses">responses</option>
          <option value="anthropic_messages">anthropic_messages</option>
        </select>
        <input className="filter-input" placeholder="Model" value={form.model} onChange={(event) => update("model", event.target.value)} />
        <label className="checkbox-row"><input type="checkbox" checked={form.stream} onChange={(event) => update("stream", event.target.checked)} /> stream</label>
        <label className="checkbox-row"><input type="checkbox" checked={form.tools} onChange={(event) => update("tools", event.target.checked)} /> tools</label>
        <button className="ghost-button" type="submit">Inspect</button>
      </form>
      {error ? <EmptyState title="Route inspector API unavailable" detail={error} compact /> : null}
      {result ? <pre className="trace-json-block">{JSON.stringify(result, null, 2)}</pre> : null}
      {!result && !error ? <EmptyState title="No dry run yet" detail="Submit endpoint and model to preview the planned route." compact /> : null}
    </section>
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
    return items;
  }
  const now = new Date();
  const since = windowValue === "today"
    ? new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
    : Date.now() - (windowValue === "7d" ? 7 * 24 * 60 * 60 * 1000 : 30 * 24 * 60 * 60 * 1000);
  return items.filter((item) => new Date(item.recorded_at).getTime() >= since);
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

function usageCoverageDetail(missing, t = (key, values) => `${values?.count || 0} missing usage`) {
  const count = Number(missing || 0);
  return count > 0 ? t("providers.missingUsage", { count: formatCount(count) }) : "";
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
  const { t } = useI18n();
  const hasCredentialData = summary.routeTargets.length || summary.channels.length || summary.credentials.length || summary.stickyBreakTotal > 0;
  const stickyBreakContext = firstNonEmptyItem(summary.stickyBreakRouteTargets, summary.stickyBreakPreviousRouteTargets, summary.stickyBreakPreviousUpstreams);
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Credential routing</p>
          <h2>{t("routing.credentialSummary")}</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag>{windowValue}</InlineTag>
          <InlineTag tone={summary.eventfulTraces ? "green" : "default"}>{t("routing.eventful", { count: formatCount(summary.eventfulTraces) })}</InlineTag>
          {summary.missingEvents ? <InlineTag tone="gold">{t("routing.legacyMissing", { count: formatCount(summary.missingEvents) })}</InlineTag> : null}
          {summary.parseErrors ? <InlineTag tone="danger">{t("routing.parseErrors", { count: formatCount(summary.parseErrors) })}</InlineTag> : null}
        </div>
      </div>
      <div className="hero-grid hero-grid-compact">
        <StatCard label={t("routing.routeTargets")} value={formatCount(summary.routeTargets.length)} detail={topCountDetail(summary.routeTargets)} mono />
        <StatCard label={t("routing.channels")} value={formatCount(summary.channels.length || summary.selectedUpstreams.length)} detail={topCountDetail(summary.channels.length ? summary.channels : summary.selectedUpstreams)} mono />
        <StatCard label={t("routing.credentials")} value={formatCount(summary.credentials.length)} detail={topCountDetail(summary.credentials)} mono />
        <StatCard label={t("routing.stickyBreaks")} value={formatCount(summary.stickyBreakTotal)} detail={stickyBreakContext ? stickyBreakContext.label : ""} accent={summary.stickyBreakTotal ? "accent-red" : ""} mono />
      </div>
      {hasCredentialData ? (
        <div className="session-breakdown-grid">
          <BreakdownList title={t("routing.routeTargets")} items={summary.routeTargets} formatter={(item) => item.label} />
          <BreakdownList title={t("routing.channels")} items={summary.channels.length ? summary.channels : summary.selectedUpstreams} formatter={(item) => item.label} />
          <BreakdownList title={t("routing.credentials")} items={summary.credentials} formatter={(item) => item.label} />
          <BreakdownList title={t("routing.stickyCredentialBreaks")} items={stickyBreakItems(summary)} formatter={(item) => item.label} />
        </div>
      ) : (
        <EmptyState title={t("routing.noCredentialEvents")} detail={t("routing.noCredentialEventsDetail")} compact />
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
