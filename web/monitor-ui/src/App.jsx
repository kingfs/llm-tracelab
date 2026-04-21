import React, { useEffect, useRef, useState } from "react";
import { Link, Navigate, Route, Routes, useParams, useSearchParams } from "react-router-dom";
import { PrimaryNav } from "./components/PrimaryNav";
import { useJSON } from "./hooks/useJSON";
import {
  buildFailureContexts,
  buildFailureDelta,
  buildFailureDetail,
  buildFailureSummary,
  buildRoutingDecisionSummary,
  buildRoutingLink,
  buildTraceLink,
  buildTraceUpstreamHealthSummary,
  buildUpstreamHealthSummary,
  buildUpstreamLink,
  computeTTFTRatio,
  formatCapacity,
  formatDateTime,
  formatEndpointTag,
  formatFailureReason,
  formatHealthLabel,
  formatMultiplier,
  formatProviderTag,
  formatRatio,
  formatRoutingScore,
  formatSignedMetric,
  formatTime,
  formatTimelineBucketLabel,
  healthTone,
  metricThresholdTone,
  normalizeTraceTab,
  normalizeUpstreamWindow,
  resolveThresholdState,
  setOrDeleteParam,
  summarizeSessionItems,
  summarizeTraceFailure,
} from "./lib/monitor";
import {
  buildToolMessageSummary,
  buildToolSchemaSummary,
  collectTraceToolCalls,
  countToolMatches,
  findDeclaredToolForCall,
  isSameToolName,
} from "./lib/traceTools";

const REFRESH_MS = 60_000;
const PAGE_SIZE = 50;

function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/requests" replace />} />
      <Route path="/requests" element={<RequestsPage />} />
      <Route path="/sessions" element={<SessionsPage />} />
      <Route path="/routing" element={<RoutingPage />} />
      <Route path="/sessions/:sessionID" element={<SessionDetailPage />} />
      <Route path="/upstreams/:upstreamID" element={<UpstreamDetailPage />} />
      <Route path="/traces/:traceID" element={<TraceDetailPage />} />
    </Routes>
  );
}

function RequestsPage() {
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
        <>
          <StatCard label="Total" value={stats.total_request ?? 0} />
          <StatCard label="Avg TTFT" value={`${stats.avg_ttft ?? 0} ms`} />
          <StatCard label="Tokens" value={stats.total_tokens ?? 0} accent="accent-gold" />
          <StatCard label="Success" value={`${Number(stats.success_rate ?? 0).toFixed(1)}%`} accent="accent-green" />
        </>
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

function SessionsPage() {
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
  const { loading, data, error } = useJSON(`/api/sessions?${requestParams.toString()}`, [page, query, provider, model, refreshTick]);

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
          <h1>Sessions</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">refresh / 60s</span>
          <span className="badge">{data?.refreshed_at ? formatTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>
      <PrimaryNav />

      <section className="hero-grid">
        <StatCard label="Sessions" value={sessionStats.totalSessions} />
        <StatCard label="Requests" value={sessionStats.totalRequests} />
        <StatCard label="Tokens" value={sessionStats.totalTokens} accent="accent-gold" />
        <StatCard label="Avg Success" value={`${sessionStats.avgSuccessRate.toFixed(1)}%`} accent="accent-green" />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent sessions</p>
            <h2>Latest 50 sessions</h2>
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
            placeholder="Search session id, model, provider"
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
        {loading && !data ? <div className="empty-state">Loading sessions...</div> : null}

        <SessionList items={items} />
      </section>
    </div>
  );
}

function RoutingPage() {
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
        {upstreams.error ? <div className="empty-state error-box">{upstreams.error}</div> : null}
        {upstreams.loading && !upstreams.data ? <div className="empty-state">Loading upstream targets...</div> : null}
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

function UpstreamOverview({ items, analyticsWindow = "24h", analyticsModel = "", routingFailures = {} }) {
  const healthyCount = items.filter((item) => item.health_state === "healthy").length;
  const attentionCount = items.filter((item) => item.health_state !== "healthy").length;
  const modelCount = new Set(items.flatMap((item) => item.models || [])).size;

  return (
    <div className="upstream-section">
      <div className="hero-grid hero-grid-compact upstream-hero-grid">
        <StatCard label="Targets" value={items.length} />
        <StatCard label="Healthy" value={healthyCount} accent="accent-green" />
        <StatCard label="Attention" value={attentionCount} accent={attentionCount > 0 ? "accent-red" : ""} />
        <StatCard label="Models" value={modelCount} accent="accent-gold" />
      </div>
      <div className="upstream-routing-strip">
        <span>window {analyticsWindow}</span>
        <span>model filter {analyticsModel || "all"}</span>
      </div>
      <div className="routing-failure-summary">
        <div className="routing-failure-summary-head">
          <div>
            <div className="upstream-card-label">Routing failures</div>
            <span className="trace-subline">Selection-stage failures without a chosen upstream</span>
          </div>
          <strong>{routingFailures.total ?? 0}</strong>
        </div>
        {(routingFailures.reasons || []).length ? (
          <div className="session-breakdown-grid">
            <BreakdownList title="Reasons" items={routingFailures.reasons || []} formatter={(item) => formatFailureReason(item.label)} />
            <section className="breakdown-card">
              <div className="breakdown-title">Recent failures</div>
              <div className="routing-failure-recent-list">
                {(routingFailures.recent || []).map((item) => (
                  <Link key={item.trace_id} className="upstream-failure-card" to={buildTraceLink(item.trace_id, "requests", "", "", "failure")}>
                    <div className="trace-tag-group">
                      <InlineTag tone="danger">{item.status_code}</InlineTag>
                      <InlineTag>{formatFailureReason(item.reason)}</InlineTag>
                    </div>
                    <strong>{item.model || "unknown-model"}</strong>
                    <span>{formatDateTime(item.recorded_at)}</span>
                    {item.error_text ? <div className="upstream-failure-detail">{item.error_text}</div> : null}
                  </Link>
                ))}
              </div>
            </section>
          </div>
        ) : (
          <div className="empty-state">No routing failures in this window.</div>
        )}
        {(routingFailures.timeline || []).length ? (
          <section className="breakdown-card">
            <div className="breakdown-title">Failure timeline</div>
            <RoutingFailureTimeline items={routingFailures.timeline || []} />
          </section>
        ) : null}
      </div>
      {!items.length ? <div className="empty-state">No upstream targets discovered.</div> : null}
      <div className="upstream-grid">
        {items.map((item) => (
          <article key={item.id} className="upstream-card">
            <div className="upstream-card-head">
              <div>
                <div className="trace-title-row">
                  <strong className="trace-model-name">{item.id}</strong>
                  <div className="trace-tag-group">
                    <InlineTag tone={healthTone(item.health_state)}>{formatHealthLabel(item.health_state)}</InlineTag>
                    <InlineTag tone="accent">{item.provider_preset || "custom"}</InlineTag>
                    <InlineTag>{item.routing_profile || item.protocol_family || "route"}</InlineTag>
                  </div>
                </div>
                <span className="trace-subline mono">{item.base_url}</span>
              </div>
              <div className="trace-metric-stack">
                <strong>{item.inflight ?? 0}</strong>
                <span>inflight</span>
              </div>
            </div>

            <div className="upstream-meta-grid">
              <div className="detail-meta-pill">
                <span className="detail-meta-label">requests</span>
                <strong>{item.request_count ?? 0}</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">success</span>
                <strong>{Number(item.success_rate || 0).toFixed(1)}%</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">refresh</span>
                <strong>{item.last_refresh_status || "unknown"}</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">ttft</span>
                <strong>{Math.round(item.ttft_fast_ms || item.avg_ttft || 0)} ms</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">latency</span>
                <strong>{Math.round(item.latency_fast_ms || 0)} ms</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">error</span>
                <strong>{formatRatio(item.error_rate)}</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">timeout</span>
                <strong>{formatRatio(item.timeout_rate)}</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">tokens</span>
                <strong>{item.total_tokens ?? 0}</strong>
              </div>
              <div className="detail-meta-pill">
                <span className="detail-meta-label">capacity</span>
                <strong>{formatCapacity(item.weight, item.capacity_hint)}</strong>
              </div>
            </div>

            <div className="upstream-card-section">
              <div className="upstream-card-label">Catalog</div>
              <div className="upstream-model-list">
                {(item.models || []).length ? (
                  (item.models || []).slice(0, 8).map((model) => (
                    <span key={`${item.id}-${model}`} className="upstream-model-pill" title={model}>
                      {model}
                    </span>
                  ))
                ) : (
                  <span className="trace-subline">No models indexed</span>
                )}
                {(item.models || []).length > 8 ? (
                  <span className="upstream-model-pill upstream-model-pill-more">+{item.models.length - 8} more</span>
                ) : null}
              </div>
              {(item.models || []).length ? (
                <div className="upstream-card-actions">
                  <Link className="inline-link" to={`${buildUpstreamLink(item.id, analyticsWindow, analyticsModel)}#models`}>
                    View all models
                  </Link>
                </div>
              ) : null}
            </div>

            <div className="upstream-card-section">
              <div className="upstream-card-label">Recent routing</div>
              <div className="upstream-model-list">
                {(item.recent_models || []).length ? (
                  (item.recent_models || []).slice(0, 5).map((model) => (
                    <span key={`${item.id}-recent-${model}`} className="upstream-model-pill" title={model}>
                      {model}
                    </span>
                  ))
                ) : (
                  <span className="trace-subline">No routed models yet</span>
                )}
                {(item.recent_models || []).length > 5 ? (
                  <span className="upstream-model-pill upstream-model-pill-more">+{item.recent_models.length - 5} more</span>
                ) : null}
              </div>
              <div className="upstream-routing-strip">
                <span>success {item.success_request ?? 0}</span>
                <span>failed {item.failed_request ?? 0}</span>
                <span>last model {item.last_model || "-"}</span>
                <span>last seen {formatDateTime(item.last_seen)}</span>
              </div>
              {(item.recent_models || []).length ? (
                <div className="upstream-card-actions">
                  <Link className="inline-link" to={`${buildUpstreamLink(item.id, analyticsWindow, analyticsModel)}#models`}>
                    Open full routing catalog
                  </Link>
                </div>
              ) : null}
              {(item.recent_errors || []).length ? (
                <div className="upstream-error-list">
                  {(item.recent_errors || []).map((errorText, index) => (
                    <div key={`${item.id}-error-${index}`} className="upstream-error-item">
                      {errorText}
                    </div>
                  ))}
                </div>
              ) : null}
              {(item.recent_failures || []).length ? (
                <div className="upstream-failure-list">
                  {item.recent_failures.map((failure) => (
                    <Link key={`${item.id}-${failure.trace_id}`} className="upstream-failure-card" to={buildTraceLink(failure.trace_id, "", "", "", "failure")}>
                      <div className="trace-tag-group">
                        <InlineTag tone="danger">{failure.status_code}</InlineTag>
                        <InlineTag tone="accent">{formatEndpointTag(failure.endpoint)}</InlineTag>
                      </div>
                      <strong>{failure.model || "unknown-model"}</strong>
                      <span>{formatDateTime(failure.recorded_at)}</span>
                      {failure.error_text ? <div className="upstream-failure-detail">{failure.error_text}</div> : null}
                    </Link>
                  ))}
                </div>
              ) : null}
            </div>

            <div className="upstream-card-footer">
              <span>last refresh {formatDateTime(item.last_refresh_at)}</span>
              <div className="action-group action-group-start">
                <Link
                  className="ghost-button"
                  to={buildUpstreamLink(item.id, analyticsWindow, analyticsModel)}
                >
                  Detail
                </Link>
                {item.last_refresh_error ? <span className="status-err">{item.last_refresh_error}</span> : null}
                {item.open_until ? <span>open until {formatDateTime(item.open_until)}</span> : null}
              </div>
            </div>
          </article>
        ))}
      </div>
    </div>
  );
}

function RequestList({ items, fromView = "", fromSessionID = "", focusFailures = false }) {
  return (
    <div className="trace-table">
      <div className="trace-table-head">
        <span>Model</span>
        <span>Status</span>
        <span>Latency</span>
        <span>Tokens</span>
        <span>Actions</span>
      </div>
      {items.map((item) => {
        const focus = focusFailures && (item.status_code < 200 || item.status_code >= 300) ? "failure" : "";

        return (
          <article key={item.id} className={item.status_code >= 200 && item.status_code < 300 ? "trace-row" : "trace-row trace-row-failed"}>
          <div>
            <div className="trace-title-row">
              <strong className="trace-model-name">{item.model || "unknown-model"}</strong>
              <div className="trace-tag-group">
                <InlineTag tone="accent">{formatEndpointTag(item.endpoint || item.operation)}</InlineTag>
                <InlineTag>{formatProviderTag(item.provider)}</InlineTag>
                {item.session_id ? <InlineTag tone="green">session</InlineTag> : null}
                {item.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
              </div>
            </div>
            <span className="trace-subline">{formatDateTime(item.recorded_at)}</span>
            {item.session_id ? <span className="trace-subline mono">session {item.session_id}</span> : null}
          </div>
          <div className="trace-metric-stack">
            <strong className={item.status_code >= 200 && item.status_code < 300 ? "status-ok" : "status-err"}>{item.status_code}</strong>
            <span>{item.method || "POST"}</span>
          </div>
          <div className="trace-metric-stack">
            <strong>{item.duration_ms} ms</strong>
            <span>ttft {item.ttft_ms} ms</span>
          </div>
          <div>
            <div className="token-inline-row">
              <MiniToken metric="in" value={item.prompt_tokens} tone="accent" icon="input" />
              <MiniToken metric="out" value={item.completion_tokens} tone="green" icon="output" />
              <MiniToken metric="total" value={item.total_tokens} tone="default" icon="total" />
              <MiniToken metric="cached" value={item.cached_tokens} tone="gold" icon="cached" />
            </div>
          </div>
          <div className="action-group">
            {item.session_id ? (
              <Link className="icon-button" to={`/sessions/${encodeURIComponent(item.session_id)}`} title="View session" aria-label="View session">
                <StackIcon />
              </Link>
            ) : null}
            {fromSessionID ? (
              <Link className="ghost-button" to={buildTraceLink(item.id, fromView, fromSessionID, "timeline", focus === "failure" ? "timeline_error" : "timeline")}>
                Timeline
              </Link>
            ) : null}
            {fromSessionID ? (
              <Link className="ghost-button" to={buildTraceLink(item.id, fromView, fromSessionID, "raw", focus === "failure" ? "response" : focus)}>
                Raw
              </Link>
            ) : null}
            <Link className="icon-button" to={buildTraceLink(item.id, fromView, fromSessionID, "", focus)} title="View trace" aria-label="View trace">
              <ViewIcon />
            </Link>
            <a className="icon-button" href={`/api/traces/${item.id}/download`} title="Download .http" aria-label="Download trace">
              <DownloadIcon />
            </a>
          </div>
          </article>
        );
      })}
    </div>
  );
}

function SessionList({ items }) {
  return (
    <div className="session-table">
      <div className="session-table-head">
        <span>Session</span>
        <span>Requests</span>
        <span>Health</span>
        <span>Tokens</span>
        <span>Actions</span>
      </div>
      {items.map((item) => (
        <article key={item.session_id} className="session-row">
          <div>
            <div className="trace-title-row">
              <strong className="trace-model-name">{item.last_model || item.session_id}</strong>
              <div className="trace-tag-group">
                <InlineTag tone="accent">{item.session_source || "session"}</InlineTag>
                {(item.providers || []).map((provider) => (
                  <InlineTag key={provider}>{formatProviderTag(provider)}</InlineTag>
                ))}
              </div>
            </div>
            <span className="trace-subline mono">{item.session_id}</span>
            <span className="trace-subline">last {formatDateTime(item.last_seen)}</span>
          </div>
          <div className="trace-metric-stack">
            <strong>{item.request_count}</strong>
            <span>streams {item.stream_count || 0}</span>
          </div>
          <div className="trace-metric-stack">
            <strong className={item.failed_request > 0 ? "status-err" : "status-ok"}>{Number(item.success_rate ?? 0).toFixed(1)}%</strong>
            <span>ttft {item.avg_ttft ?? 0} ms</span>
          </div>
          <div className="trace-metric-stack">
            <strong>{item.total_tokens ?? 0}</strong>
            <span>duration {item.total_duration_ms ?? 0} ms</span>
          </div>
          <div className="action-group">
            <Link className="icon-button" to={`/sessions/${encodeURIComponent(item.session_id)}`} title="View session" aria-label="View session">
              <StackIcon />
            </Link>
          </div>
        </article>
      ))}
    </div>
  );
}

function SessionDetailPage() {
  const { sessionID = "" } = useParams();
  const [traceFilter, setTraceFilter] = useState("all");
  const detail = useJSON(`/api/sessions/${encodeURIComponent(sessionID)}`, [sessionID]);
  const summary = detail.data?.summary;
  const breakdown = detail.data?.breakdown;
  const timeline = detail.data?.timeline ?? [];
  const traces = detail.data?.traces ?? [];
  const visibleTraces = traceFilter === "failed" ? traces.filter((trace) => trace.status_code < 200 || trace.status_code >= 300) : traces;
  const failureContexts = buildFailureContexts(timeline);

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{summary?.last_model || "session detail"}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone="accent">{summary?.session_source || "session"}</InlineTag>
              {(summary?.providers || []).map((provider) => (
                <InlineTag key={provider}>{formatProviderTag(provider)}</InlineTag>
              ))}
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label="session" value={summary?.session_id || sessionID} mono />
            <DetailMetaPill label="first seen" value={formatDateTime(summary?.first_seen)} />
            <DetailMetaPill label="last seen" value={formatDateTime(summary?.last_seen)} />
            <DetailMetaPill label="requests" value={summary?.request_count ?? 0} />
            <DetailMetaPill label="success" value={`${Number(summary?.success_rate ?? 0).toFixed(1)}%`} />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to="/sessions" title="Back to sessions" aria-label="Back to sessions">
              <HomeIcon />
            </Link>
          </div>
          <div className="detail-toolbar-tokens">
            <TokenBadge label="ttft" value={summary?.avg_ttft ?? 0} icon="total" />
            <TokenBadge label="tokens" value={summary?.total_tokens ?? 0} icon="output" accent="token-badge-strong" />
            <TokenBadge label="failed" value={summary?.failed_request ?? 0} icon="cached" />
          </div>
        </div>
      </header>

      {detail.error ? <div className="empty-state error-box">{detail.error}</div> : null}
      {detail.loading && !detail.data ? <div className="empty-state">Loading session...</div> : null}

      {detail.data ? (
        <div className="detail-grid detail-grid-compact">
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Failure surface</p>
                <h2>Session health</h2>
              </div>
            </div>
            <div className="hero-grid hero-grid-compact">
              <StatCard label="Failed" value={breakdown?.failed_traces ?? 0} accent={(breakdown?.failed_traces ?? 0) > 0 ? "accent-red" : ""} />
              <StatCard label="Success" value={summary?.success_request ?? 0} />
              <StatCard label="Streams" value={summary?.stream_count ?? 0} />
              <StatCard label="Duration" value={`${summary?.total_duration_ms ?? 0} ms`} />
            </div>
          </section>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Distribution</p>
                <h2>Models and endpoints</h2>
              </div>
            </div>
            <div className="session-breakdown-grid">
              <BreakdownList title="Models" items={breakdown?.models || []} formatter={(item) => item.label} />
              <BreakdownList title="Endpoints" items={breakdown?.endpoints || []} formatter={(item) => formatEndpointTag(item.label)} />
              <BreakdownList title="Failure reasons" items={breakdown?.failure_reasons || []} formatter={(item) => formatFailureReason(item.label)} />
            </div>
          </section>
        </div>
      ) : null}

      {timeline.length ? (
        <section className="panel timeline-panel">
          <div className="panel-head">
            <div>
              <p className="eyebrow">Session timeline</p>
              <h2>Request sequence</h2>
            </div>
          </div>
          <div className="timeline-list">
            {timeline.map((item) => (
              <article key={item.trace_id} className="timeline-item">
                <div className="timeline-rail">
                  <span className={item.status_code >= 200 && item.status_code < 300 ? "timeline-dot" : "timeline-dot timeline-dot-danger"} />
                </div>
                <div className="timeline-card">
                  <div className="timeline-head">
                    <div>
                      <strong>{item.model || "unknown-model"}</strong>
                      <span>{formatDateTime(item.time)}</span>
                    </div>
                    <span className="timeline-badge">{item.is_stream ? "stream" : "request"}</span>
                  </div>
                  <div className="trace-tag-group">
                    <InlineTag tone="accent">{formatEndpointTag(item.endpoint)}</InlineTag>
                    <InlineTag>{formatProviderTag(item.provider)}</InlineTag>
                    <InlineTag tone={item.status_code >= 200 && item.status_code < 300 ? "green" : "danger"}>{item.status_code}</InlineTag>
                  </div>
                  <div className="session-timeline-meta">
                    <span>duration {item.duration_ms} ms</span>
                    <span>ttft {item.ttft_ms} ms</span>
                    <span>tokens {item.total_tokens}</span>
                  </div>
                  {item.error ? <div className="timeline-message">{item.error}</div> : null}
                  <div className="action-group action-group-start">
                    <Link
                      className="ghost-button"
                      to={buildTraceLink(item.trace_id, "", summary?.session_id || sessionID, "timeline", item.status_code >= 200 && item.status_code < 300 ? "timeline" : "timeline_error")}
                    >
                      Timeline
                    </Link>
                    <Link className="ghost-button" to={buildTraceLink(item.trace_id, "", summary?.session_id || sessionID, "raw", item.status_code >= 200 && item.status_code < 300 ? "" : "response")}>
                      Raw
                    </Link>
                    <Link className="icon-button" to={buildTraceLink(item.trace_id, "", summary?.session_id || sessionID, "", item.status_code >= 200 && item.status_code < 300 ? "" : "failure")} title="View trace" aria-label="View trace">
                      <ViewIcon />
                    </Link>
                  </div>
                </div>
              </article>
            ))}
          </div>
        </section>
      ) : null}

      {failureContexts.length ? (
        <section className="panel">
          <div className="panel-head">
            <div>
              <p className="eyebrow">Failure context</p>
              <h2>Requests around each failure</h2>
            </div>
          </div>
          <div className="failure-context-list">
            {failureContexts.map((context) => (
              <article key={context.current.trace_id} className="failure-context-card">
                <div className="failure-context-head">
                  <strong>{context.current.model || "unknown-model"}</strong>
                  <span>{formatDateTime(context.current.time)}</span>
                </div>
                <p className="failure-context-summary">{buildFailureSummary(context)}</p>
                <div className="failure-context-strip">
                  {context.previous ? <FailureContextNode label="Before" item={context.previous} tone="default" sessionID={summary?.session_id || sessionID} /> : null}
                  <FailureContextNode
                    label="Failed"
                    item={context.current}
                    tone="danger"
                    sessionID={summary?.session_id || sessionID}
                    delta={buildFailureDelta(context.previous, context.current)}
                    detail={context.current.error || buildFailureDetail(context.current)}
                  />
                  {context.next ? <FailureContextNode label="After" item={context.next} tone="accent" sessionID={summary?.session_id || sessionID} /> : null}
                </div>
              </article>
            ))}
          </div>
        </section>
      ) : null}

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Session traces</p>
            <h2>{traceFilter === "failed" ? "Failed request list" : "Grouped request list"}</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Session trace filter">
              <button className={traceFilter === "all" ? "ghost-button active" : "ghost-button"} onClick={() => setTraceFilter("all")}>
                All
              </button>
              <button className={traceFilter === "failed" ? "ghost-button active" : "ghost-button"} onClick={() => setTraceFilter("failed")}>
                Failed only
              </button>
            </div>
            <span className="session-filter-count">
              {visibleTraces.length} / {traces.length} traces
            </span>
          </div>
        </div>
        {traceFilter === "failed" && visibleTraces.length === 0 ? (
          <div className="empty-state">No failed traces in this session.</div>
        ) : (
          <RequestList items={visibleTraces} fromSessionID={summary?.session_id || sessionID} focusFailures />
        )}
      </section>
    </div>
  );
}

function UpstreamDetailPage() {
  const { upstreamID = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeUpstreamWindow(searchParams.get("window"));
  const modelValue = searchParams.get("model") || "";
  const [modelDraft, setModelDraft] = useState(modelValue);
  const [catalogQuery, setCatalogQuery] = useState("");
  const params = new URLSearchParams();
  params.set("window", windowValue);
  if (modelValue) {
    params.set("model", modelValue);
  }
  const detail = useJSON(`/api/upstreams/${encodeURIComponent(upstreamID)}?${params.toString()}`, [upstreamID, windowValue, modelValue]);
  const target = detail.data?.target;
  const breakdown = detail.data?.breakdown;
  const traces = detail.data?.traces ?? [];
  const timeline = detail.data?.timeline ?? [];
  const failureTimeline = detail.data?.failure_timeline ?? [];
  const thresholds = detail.data?.health_thresholds;
  const catalogModels = target?.models || [];
  const recentModels = target?.recent_models || [];
  const normalizedCatalogQuery = catalogQuery.trim().toLowerCase();
  const visibleCatalogModels = normalizedCatalogQuery
    ? catalogModels.filter((model) => model.toLowerCase().includes(normalizedCatalogQuery))
    : catalogModels;
  const visibleRecentModels = normalizedCatalogQuery
    ? recentModels.filter((model) => model.toLowerCase().includes(normalizedCatalogQuery))
    : recentModels;

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };
  const applyModel = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "model", modelDraft);
    setSearchParams(next);
  };

  useEffect(() => {
    setModelDraft(modelValue);
  }, [modelValue]);

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{target?.id || upstreamID || "upstream detail"}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone={healthTone(target?.health_state)}>{formatHealthLabel(target?.health_state)}</InlineTag>
              <InlineTag tone="accent">{target?.provider_preset || "custom"}</InlineTag>
              <InlineTag>{target?.routing_profile || target?.protocol_family || "route"}</InlineTag>
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label="base url" value={target?.base_url || "-"} mono />
            <DetailMetaPill label="last seen" value={formatDateTime(target?.last_seen)} />
            <DetailMetaPill label="requests" value={target?.request_count ?? 0} />
            <DetailMetaPill label="success" value={`${Number(target?.success_rate || 0).toFixed(1)}%`} />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to={buildRoutingLink(windowValue, modelValue)} title="Back to routing" aria-label="Back to routing">
              <HomeIcon />
            </Link>
          </div>
          <div className="detail-toolbar-tokens">
            <TokenBadge label="ttft" value={target?.avg_ttft ?? 0} icon="total" />
            <TokenBadge label="tokens" value={target?.total_tokens ?? 0} icon="output" accent="token-badge-strong" />
            <TokenBadge label="failed" value={target?.failed_request ?? 0} icon="cached" />
          </div>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Analytics filters</p>
            <h2>Window and model</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Upstream detail window">
              {["1h", "24h", "7d", "all"].map((window) => (
                <button
                  key={window}
                  className={windowValue === window ? "ghost-button active" : "ghost-button"}
                  onClick={() => setWindow(window)}
                >
                  {window}
                </button>
              ))}
            </div>
            <span className="badge">{detail.data?.refreshed_at ? formatTime(detail.data.refreshed_at) : "..."}</span>
          </div>
        </div>
        <form className="filter-bar" onSubmit={applyModel}>
          <input
            className="filter-input filter-input-wide"
            type="search"
            name="model"
            value={modelDraft}
            onChange={(event) => setModelDraft(event.target.value)}
            placeholder="Filter detail by model"
          />
          <button className="ghost-button" type="submit">Apply</button>
          <button
            className="ghost-button"
            type="button"
            onClick={() => {
              setModelDraft("");
              const next = new URLSearchParams(searchParams);
              next.delete("model");
              setSearchParams(next);
            }}
          >
            Reset
          </button>
        </form>
      </section>

      {detail.error ? <div className="empty-state error-box">{detail.error}</div> : null}
      {detail.loading && !detail.data ? <div className="empty-state">Loading upstream detail...</div> : null}

      {detail.data ? (
        <div className="detail-grid detail-grid-compact">
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Traffic summary</p>
                <h2>Routing health</h2>
              </div>
            </div>
            <div className="hero-grid hero-grid-compact">
              <StatCard label="Requests" value={target?.request_count ?? 0} />
              <StatCard label="Failed" value={breakdown?.failed_traces ?? 0} accent={(breakdown?.failed_traces ?? 0) > 0 ? "accent-red" : ""} />
              <StatCard label="Inflight" value={target?.inflight ?? 0} />
              <StatCard label="Capacity" value={formatCapacity(target?.weight, target?.capacity_hint)} />
            </div>
          </section>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Router health</p>
                <h2>Decision signals</h2>
              </div>
            </div>
            <div className="session-breakdown-grid">
              <section className="breakdown-card">
                <div className="breakdown-title">Health state</div>
                <div className="routing-summary-stack">
                  <strong className="trace-model-name">{formatHealthLabel(target?.health_state)}</strong>
                  <div className="trace-tag-group">
                    <InlineTag tone={healthTone(target?.health_state)}>{formatHealthLabel(target?.health_state)}</InlineTag>
                    <InlineTag tone="accent">{formatFailureReason(breakdown?.failure_reasons?.[0]?.label || "unknown_failure")}</InlineTag>
                  </div>
                  <span className="trace-subline">{buildUpstreamHealthSummary(target, breakdown?.failure_reasons || [], thresholds)}</span>
                </div>
              </section>
              <section className="breakdown-card">
                <div className="breakdown-title">Live metrics</div>
                <div className="detail-meta-strip">
                  <DetailMetaPill label="error" value={formatRatio(target?.error_rate)} />
                  <DetailMetaPill label="timeout" value={formatRatio(target?.timeout_rate)} />
                  <DetailMetaPill label="ttft" value={`${Math.round(target?.ttft_fast_ms || target?.avg_ttft || 0)} ms`} />
                  <DetailMetaPill label="latency" value={`${Math.round(target?.latency_fast_ms || 0)} ms`} />
                  <DetailMetaPill label="refresh" value={target?.last_refresh_status || "unknown"} />
                </div>
              </section>
              <section className="breakdown-card">
                <div className="breakdown-title">Threshold checks</div>
                <div className="breakdown-list">
                  <div className="breakdown-row">
                    <span className="breakdown-label">error rate</span>
                    <div className="trace-tag-group">
                      <InlineTag tone={metricThresholdTone(resolveThresholdState(target?.error_rate, thresholds?.error_rate_degraded, thresholds?.error_rate_open))}>
                        {resolveThresholdState(target?.error_rate, thresholds?.error_rate_degraded, thresholds?.error_rate_open)}
                      </InlineTag>
                      <strong>{formatRatio(target?.error_rate)} / {formatRatio(thresholds?.error_rate_degraded)} / {formatRatio(thresholds?.error_rate_open)}</strong>
                    </div>
                  </div>
                  <div className="breakdown-row">
                    <span className="breakdown-label">timeout rate</span>
                    <div className="trace-tag-group">
                      <InlineTag tone={metricThresholdTone(resolveThresholdState(target?.timeout_rate, thresholds?.timeout_rate_degraded, thresholds?.timeout_rate_open))}>
                        {resolveThresholdState(target?.timeout_rate, thresholds?.timeout_rate_degraded, thresholds?.timeout_rate_open)}
                      </InlineTag>
                      <strong>{formatRatio(target?.timeout_rate)} / {formatRatio(thresholds?.timeout_rate_degraded)} / {formatRatio(thresholds?.timeout_rate_open)}</strong>
                    </div>
                  </div>
                  <div className="breakdown-row">
                    <span className="breakdown-label">ttft ratio</span>
                    <div className="trace-tag-group">
                      <InlineTag tone={metricThresholdTone(resolveThresholdState(computeTTFTRatio(target), thresholds?.ttft_degraded_ratio, null))}>
                        {resolveThresholdState(computeTTFTRatio(target), thresholds?.ttft_degraded_ratio, null)}
                      </InlineTag>
                      <strong>{formatMultiplier(computeTTFTRatio(target))} / {formatMultiplier(thresholds?.ttft_degraded_ratio)}</strong>
                    </div>
                  </div>
                  <div className="breakdown-row">
                    <span className="breakdown-label">router gates</span>
                    <strong>{thresholds?.failure_threshold ?? 0} failures · open {thresholds?.open_window || "-"}</strong>
                  </div>
                </div>
              </section>
            </div>
          </section>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Distribution</p>
                <h2>Models and endpoints</h2>
              </div>
            </div>
            <div className="session-breakdown-grid">
              <BreakdownList title="Models" items={breakdown?.models || []} formatter={(item) => item.label} />
              <BreakdownList title="Endpoints" items={breakdown?.endpoints || []} formatter={(item) => formatEndpointTag(item.label)} />
            </div>
          </section>
          <section className="panel" id="models">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Model catalog</p>
                <h2>Full routing surface</h2>
              </div>
              <div className="panel-head-actions">
                <span className="session-filter-count">
                  {visibleCatalogModels.length} / {catalogModels.length} indexed
                </span>
                <span className="session-filter-count">
                  {visibleRecentModels.length} / {recentModels.length} recent
                </span>
              </div>
            </div>
            <form className="filter-bar" onSubmit={(event) => event.preventDefault()}>
              <input
                className="filter-input filter-input-wide"
                type="search"
                value={catalogQuery}
                onChange={(event) => setCatalogQuery(event.target.value)}
                placeholder="Search model names"
              />
            </form>
            <div className="session-breakdown-grid">
              <section className="breakdown-card">
                <div className="breakdown-title">Recently routed models</div>
                {visibleRecentModels.length ? (
                  <div className="model-catalog-list">
                    {visibleRecentModels.map((model) => (
                      <div key={`recent-${model}`} className="model-catalog-row" title={model}>
                        <strong>{model}</strong>
                        {target?.last_model === model ? <InlineTag tone="accent">last</InlineTag> : null}
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="empty-state empty-state-inline">No recent models match this filter.</div>
                )}
              </section>
              <section className="breakdown-card">
                <div className="breakdown-title">Indexed models</div>
                {visibleCatalogModels.length ? (
                  <div className="model-catalog-list">
                    {visibleCatalogModels.map((model) => (
                      <div key={`catalog-${model}`} className="model-catalog-row" title={model}>
                        <strong>{model}</strong>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="empty-state empty-state-inline">No indexed models match this filter.</div>
                )}
              </section>
            </div>
          </section>
        </div>
      ) : null}

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Failure trend</p>
            <h2>Time-bucketed failures</h2>
          </div>
        </div>
        {failureTimeline.length ? (
          <RoutingFailureTimeline items={failureTimeline} />
        ) : (
          <div className="empty-state">No failure timeline available for this upstream.</div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent failures</p>
            <h2>Latest failed traces</h2>
          </div>
        </div>
        {timeline.length ? (
          <div className="upstream-failure-list upstream-failure-list-detail">
            {timeline.map((failure) => (
              <Link key={failure.trace_id} className="upstream-failure-card" to={buildTraceLink(failure.trace_id, "requests", "", "", "failure")}>
                <div className="trace-tag-group">
                  <InlineTag tone="danger">{failure.status_code}</InlineTag>
                  <InlineTag tone="accent">{formatEndpointTag(failure.endpoint)}</InlineTag>
                  {failure.reason ? <InlineTag>{formatFailureReason(failure.reason)}</InlineTag> : null}
                </div>
                <strong>{failure.model || "unknown-model"}</strong>
                <span>{formatDateTime(failure.recorded_at)}</span>
                {failure.error_text ? <div className="upstream-failure-detail">{failure.error_text}</div> : null}
              </Link>
            ))}
          </div>
        ) : (
          <div className="empty-state">No recent failures for this upstream.</div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent requests</p>
            <h2>Latest routed traces</h2>
          </div>
        </div>
        {traces.length ? <RequestList items={traces} focusFailures /> : <div className="empty-state">No traces for this upstream.</div>}
      </section>
    </div>
  );
}

function FailureContextNode({ label, item, tone = "default", sessionID = "", delta = null, detail = "" }) {
  const focus = tone === "danger" ? "failure" : "";
  const traceLink = buildTraceLink(item.trace_id, "", sessionID, "", focus);
  const timelineLink = buildTraceLink(item.trace_id, "", sessionID, "timeline", tone === "danger" ? "timeline_error" : "timeline");
  const rawLink = buildTraceLink(item.trace_id, "", sessionID, "raw", focus === "failure" ? "response" : focus);

  return (
    <div className={`failure-node failure-node-${tone}`}>
      <div className="failure-node-label">{label}</div>
      <div className="trace-tag-group">
        <InlineTag tone={tone === "danger" ? "danger" : tone === "accent" ? "accent" : "default"}>{formatEndpointTag(item.endpoint)}</InlineTag>
        <InlineTag>{item.status_code}</InlineTag>
      </div>
      <strong>{item.model || "unknown-model"}</strong>
      <span>{formatDateTime(item.time)}</span>
      <span>duration {item.duration_ms} ms</span>
      <span>tokens {item.total_tokens}</span>
      {delta ? (
        <div className="failure-delta-row">
          <span>vs prev duration {formatSignedMetric(delta.duration_ms)} ms</span>
          <span>tokens {formatSignedMetric(delta.total_tokens)}</span>
        </div>
      ) : null}
      {detail ? <div className="failure-node-detail">{detail}</div> : null}
      <div className="action-group action-group-start">
        <Link className="ghost-button" to={timelineLink}>
          Timeline
        </Link>
        <Link className="ghost-button" to={rawLink}>
          Raw
        </Link>
        <Link className="icon-button" to={traceLink} title="View trace" aria-label="View trace">
          <ViewIcon />
        </Link>
      </div>
    </div>
  );
}

function BreakdownList({ title, items, formatter }) {
  return (
    <section className="breakdown-card">
      <div className="breakdown-title">{title}</div>
      {items.length ? (
        <div className="breakdown-list">
          {items.map((item) => (
            <div key={`${title}-${item.label}`} className="breakdown-row">
              <span className="breakdown-label">{formatter(item)}</span>
              <strong>{item.count}</strong>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-state">No data</div>
      )}
    </section>
  );
}

function RoutingFailureTimeline({ items = [] }) {
  const maxCount = items.reduce((max, item) => Math.max(max, Number(item.count || 0)), 0);
  const columnCount = Math.max(items.length, 1);

  return (
    <div className="routing-timeline" style={{ gridTemplateColumns: `repeat(${columnCount}, minmax(0, 1fr))` }}>
      {items.map((item, index) => {
        const count = Number(item.count || 0);
        const height = maxCount > 0 ? Math.max((count / maxCount) * 100, count > 0 ? 16 : 6) : 6;
        return (
          <div key={`${item.time || index}`} className="routing-timeline-item">
            <div className="routing-timeline-count">{count}</div>
            <div className="routing-timeline-bar-wrap">
              <div className={count > 0 ? "routing-timeline-bar routing-timeline-bar-active" : "routing-timeline-bar"} style={{ height: `${height}%` }} />
            </div>
            <div className="routing-timeline-label">{formatTimelineBucketLabel(item.time)}</div>
          </div>
        );
      })}
    </div>
  );
}

function TraceDetailPage() {
  const { traceID = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [tab, setTab] = useState(() => normalizeTraceTab(searchParams.get("tab")));
  const [renderMarkdown, setRenderMarkdown] = useState(true);
  const failureSummaryRef = useRef(null);
  const detail = useJSON(`/api/traces/${traceID}`, [traceID]);
  const raw = useJSON(`/api/traces/${traceID}/raw`, [traceID, tab === "raw" ? "raw" : "summary"]);
  const header = detail.data?.header?.meta;
  const usage = detail.data?.header?.usage;
  const session = detail.data?.session;
  const failureSummary = summarizeTraceFailure(detail.data);
  const selectedUpstreamID = header?.selected_upstream_id || "";
  const selectedUpstreamBaseURL = header?.selected_upstream_base_url || "";
  const selectedUpstreamProviderPreset = header?.selected_upstream_provider_preset || "";
  const routingPolicy = header?.routing_policy || "";
  const routingScore = Number(header?.routing_score || 0);
  const routingCandidateCount = Number(header?.routing_candidate_count || 0);
  const routingFailureReason = header?.routing_failure_reason || "";
  const selectedUpstreamHealth = detail.data?.selected_upstream_health;
  const declaredTools = detail.data?.tools || [];
  const traceToolCalls = collectTraceToolCalls(detail.data);
  const focusTarget = searchParams.get("focus") || "";
  const hasDeclaredToolsTab = Boolean(declaredTools.length);
  const fromSessionID = searchParams.get("from_session") || "";
  const fromView = searchParams.get("view") === "sessions" ? "sessions" : "requests";
  const backLink = fromSessionID ? `/sessions/${encodeURIComponent(fromSessionID)}` : `/${fromView}`;
  const applyTraceFocus = (nextTab, nextFocus = "") => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "tab", nextTab === "timeline" ? "" : nextTab);
    setOrDeleteParam(next, "focus", nextFocus);
    setSearchParams(next, { replace: true });
  };

  useEffect(() => {
    const requestedTab = normalizeTraceTab(searchParams.get("tab"));
    setTab((current) => (current === requestedTab ? current : requestedTab));
  }, [searchParams]);

  useEffect(() => {
    if (tab !== "tools" || hasDeclaredToolsTab) {
      return;
    }
    setTab("timeline");
  }, [hasDeclaredToolsTab, tab]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "tab", tab === "timeline" ? "" : tab);
    if (next.toString() === searchParams.toString()) {
      return;
    }
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams, tab]);

  useEffect(() => {
    if (focusTarget !== "failure" || !failureSummary || !failureSummaryRef.current) {
      return;
    }
    failureSummaryRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [failureSummary, focusTarget]);

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{header?.model || "trace detail"}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone="accent">{formatEndpointTag(header?.endpoint || header?.operation)}</InlineTag>
              <InlineTag>{formatProviderTag(header?.provider)}</InlineTag>
              {selectedUpstreamID ? <InlineTag tone="green">{selectedUpstreamID}</InlineTag> : null}
              {detail.data?.header?.layout?.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
              <InlineTag tone={header?.status_code >= 200 && header?.status_code < 300 ? "green" : "danger"}>{header?.status_code || 0}</InlineTag>
            </div>
          </div>
          <div className="detail-meta-strip">
            {session?.session_id ? <DetailMetaPill label="session" value={session.session_id} mono /> : null}
            <DetailMetaPill label="time" value={formatDateTime(header?.time)} />
            <DetailMetaPill label="endpoint" value={header?.endpoint || header?.url || "-"} />
            <DetailMetaPill label="duration" value={`${header?.duration_ms || 0} ms`} />
            <DetailMetaPill label="ttft" value={`${header?.ttft_ms || 0} ms`} />
            <DetailMetaPill label="request id" value={header?.request_id || "-"} mono />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to={backLink} title={fromSessionID ? "Back to session" : "Back to list"} aria-label={fromSessionID ? "Back to session" : "Back to list"}>
              <HomeIcon />
            </Link>
            {session?.session_id ? (
              <Link className="icon-button" to={`/sessions/${encodeURIComponent(session.session_id)}`} title="View session" aria-label="View session">
                <StackIcon />
              </Link>
            ) : null}
            <a className="icon-button" href={`/api/traces/${traceID}/download`} title="Download .http" aria-label="Download trace">
              <DownloadIcon />
            </a>
          </div>
          <div className="detail-toolbar-tokens">
            <TokenBadge label="in" value={usage?.prompt_tokens || 0} icon="input" />
            <TokenBadge label="out" value={usage?.completion_tokens || 0} icon="output" />
            <TokenBadge label="total" value={usage?.total_tokens || 0} icon="total" accent="token-badge-strong" />
            <TokenBadge label="cached" value={usage?.prompt_token_details?.cached_tokens || 0} icon="cached" />
          </div>
        </div>
      </header>

      {failureSummary ? (
        <section
          ref={failureSummaryRef}
          className={focusTarget === "failure" ? "panel trace-failure-panel trace-failure-panel-focused" : "panel trace-failure-panel"}
        >
          <div className="trace-failure-head">
            <div>
              <p className="eyebrow">Failure summary</p>
              <h2>{failureSummary.title}</h2>
            </div>
            <InlineTag tone="danger">{header?.status_code || 0}</InlineTag>
          </div>
          <p className="trace-failure-summary">{failureSummary.summary}</p>
          <div className="trace-failure-meta">
            <span>{header?.endpoint || header?.url || "-"}</span>
            <span>duration {header?.duration_ms || 0} ms</span>
            <span>ttft {header?.ttft_ms || 0} ms</span>
            <span>tokens {usage?.total_tokens || 0}</span>
          </div>
          <div className="trace-failure-actions">
            <button
              className={tab === "timeline" ? "ghost-button active" : "ghost-button"}
              onClick={() => applyTraceFocus("timeline", "timeline_error")}
            >
              Open Timeline
            </button>
            <button
              className={tab === "raw" ? "ghost-button active" : "ghost-button"}
              onClick={() => applyTraceFocus("raw", "response")}
            >
              Open Raw Protocol
            </button>
            {session?.session_id ? (
              <Link className="ghost-button" to={`/sessions/${encodeURIComponent(session.session_id)}`}>
                Back to Session
              </Link>
            ) : null}
          </div>
          {failureSummary.detail ? <pre className="trace-failure-detail">{failureSummary.detail}</pre> : null}
        </section>
      ) : null}

      <nav className="detail-tabs">
        <button className={tab === "timeline" ? "tab active" : "tab"} onClick={() => setTab("timeline")}>
          Timeline
        </button>
        <button className={tab === "summary" ? "tab active" : "tab"} onClick={() => setTab("summary")}>
          Summary
        </button>
        <button className={tab === "raw" ? "tab active" : "tab"} onClick={() => setTab("raw")}>
          Raw Protocol
        </button>
        {hasDeclaredToolsTab ? (
          <button className={tab === "tools" ? "tab active" : "tab"} onClick={() => setTab("tools")}>
            Declared Tools
          </button>
        ) : null}
      </nav>

      {detail.error ? <div className="empty-state error-box">{detail.error}</div> : null}
      {detail.loading && !detail.data ? <div className="empty-state">Loading trace...</div> : null}

      {tab === "timeline" && detail.data ? <TimelinePanel events={detail.data.events || []} focusTarget={focusTarget} /> : null}

      {tab === "summary" && detail.data ? (
        <div className="detail-grid">
          {selectedUpstreamID || routingFailureReason ? (
            <section className="panel">
              <div className="panel-head">
                <div>
                  <p className="eyebrow">Routing decision</p>
                  <h2>{selectedUpstreamID ? "Selected upstream" : "Routing failure"}</h2>
                </div>
                <div className="panel-head-actions">
                  {selectedUpstreamID ? (
                    <Link className="ghost-button" to={buildUpstreamLink(selectedUpstreamID)}>
                      Open Upstream
                    </Link>
                  ) : null}
                </div>
              </div>
              <div className="detail-meta-strip">
                <DetailMetaPill label="upstream" value={selectedUpstreamID || "-"} mono />
                <DetailMetaPill label="provider" value={selectedUpstreamProviderPreset || "-"} />
                <DetailMetaPill label="policy" value={routingPolicy || "-"} />
                <DetailMetaPill label="score" value={formatRoutingScore(routingScore)} />
                <DetailMetaPill label="candidates" value={routingCandidateCount || 0} />
                {routingFailureReason ? <DetailMetaPill label="failure" value={formatFailureReason(routingFailureReason)} /> : null}
              </div>
              <div className="routing-summary-grid">
                <section className="breakdown-card">
                  <div className="breakdown-title">{selectedUpstreamID ? "Resolved upstream" : "Failure class"}</div>
                  <div className="routing-summary-stack">
                    <strong className="trace-model-name">{selectedUpstreamID || formatFailureReason(routingFailureReason) || "routing failure"}</strong>
                    <span className="trace-subline mono">{selectedUpstreamBaseURL || "-"}</span>
                    {selectedUpstreamID || routingPolicy ? (
                      <div className="trace-tag-group">
                        {selectedUpstreamProviderPreset ? <InlineTag tone="accent">{selectedUpstreamProviderPreset}</InlineTag> : null}
                        {routingPolicy ? <InlineTag>{routingPolicy}</InlineTag> : null}
                      </div>
                    ) : null}
                  </div>
                </section>
                <section className="breakdown-card">
                  <div className="breakdown-title">Decision explanation</div>
                  <div className="routing-summary-stack">
                    <span className="trace-subline">
                      {buildRoutingDecisionSummary({
                        upstreamID: selectedUpstreamID,
                        policy: routingPolicy,
                        score: routingScore,
                        candidateCount: routingCandidateCount,
                        failureReason: routingFailureReason,
                      })}
                    </span>
                  </div>
                </section>
                {selectedUpstreamHealth ? (
                  <section className="breakdown-card">
                    <div className="breakdown-title">Upstream health at review time</div>
                    <div className="routing-summary-stack">
                      <div className="trace-tag-group">
                        <InlineTag tone={healthTone(selectedUpstreamHealth.health_state)}>{formatHealthLabel(selectedUpstreamHealth.health_state)}</InlineTag>
                        <InlineTag tone={metricThresholdTone(resolveThresholdState(selectedUpstreamHealth.error_rate, selectedUpstreamHealth.health_thresholds?.error_rate_degraded, selectedUpstreamHealth.health_thresholds?.error_rate_open))}>
                          error {resolveThresholdState(selectedUpstreamHealth.error_rate, selectedUpstreamHealth.health_thresholds?.error_rate_degraded, selectedUpstreamHealth.health_thresholds?.error_rate_open)}
                        </InlineTag>
                        <InlineTag tone={metricThresholdTone(resolveThresholdState(selectedUpstreamHealth.timeout_rate, selectedUpstreamHealth.health_thresholds?.timeout_rate_degraded, selectedUpstreamHealth.health_thresholds?.timeout_rate_open))}>
                          timeout {resolveThresholdState(selectedUpstreamHealth.timeout_rate, selectedUpstreamHealth.health_thresholds?.timeout_rate_degraded, selectedUpstreamHealth.health_thresholds?.timeout_rate_open)}
                        </InlineTag>
                      </div>
                      <span className="trace-subline">
                        {buildTraceUpstreamHealthSummary(selectedUpstreamHealth)}
                      </span>
                      <div className="detail-meta-strip">
                        <DetailMetaPill label="error" value={formatRatio(selectedUpstreamHealth.error_rate)} />
                        <DetailMetaPill label="timeout" value={formatRatio(selectedUpstreamHealth.timeout_rate)} />
                        <DetailMetaPill label="ttft" value={`${Math.round(selectedUpstreamHealth.ttft_fast_ms || 0)} ms`} />
                        <DetailMetaPill label="latency" value={`${Math.round(selectedUpstreamHealth.latency_fast_ms || 0)} ms`} />
                      </div>
                    </div>
                  </section>
                ) : null}
              </div>
            </section>
          ) : null}
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">{hasConversation(detail.data) ? "Conversation" : "Payload"}</p>
                <h2>{hasConversation(detail.data) ? "Request and response" : "Request / response body"}</h2>
              </div>
              <label className="wrap-toggle">
                <input type="checkbox" checked={renderMarkdown} onChange={(event) => setRenderMarkdown(event.target.checked)} />
                Render markdown
              </label>
            </div>
            {hasConversation(detail.data) ? (
              <div className="message-list">
                {detail.data.messages.map((message, index) => (
                  <MessageCard key={`${message.role}-${index}`} message={message} renderMarkdown={renderMarkdown} declaredTools={declaredTools} />
                ))}
                {detail.data.ai_reasoning ? (
                  <CollapsibleCard title="Reasoning" subtitle="assistant reasoning" defaultOpen={false}>
                    <CodeBlock value={detail.data.ai_reasoning} />
                  </CollapsibleCard>
                ) : null}
                {detail.data.ai_content ? (
                  <article className="message-card message-assistant">
                    <div className="message-meta">
                      <span className="role-pill">assistant</span>
                      <span className="message-kind">final output</span>
                    </div>
                    <MessageContent value={detail.data.ai_content} format="markdown" renderMarkdown={renderMarkdown} className="message-body" />
                  </article>
                ) : null}
                {detail.data.tool_calls?.length ? (
                  <CollapsibleCard title="Tool Calls" subtitle={`${detail.data.tool_calls.length} call(s)`} defaultOpen={false}>
                    {detail.data.tool_calls.map((call) => (
                      <ToolCallView key={call.id || call.function?.name} call={call} match={findDeclaredToolForCall(call, declaredTools)} />
                    ))}
                  </CollapsibleCard>
                ) : null}
                {detail.data.ai_blocks?.length ? (
                  <CollapsibleCard title="Output Blocks" subtitle={`${detail.data.ai_blocks.length} block(s)`} defaultOpen={false}>
                    {detail.data.ai_blocks.map((block, index) => (
                      <BlockView key={`${block.kind}-${index}`} block={block} />
                    ))}
                  </CollapsibleCard>
                ) : null}
              </div>
            ) : (
              <PayloadSummary raw={raw} />
            )}
          </section>
        </div>
      ) : null}

      {tab === "raw" ? <RawProtocolPanel raw={raw} focusTarget={focusTarget} /> : null}
      {tab === "tools" && detail.data ? <DeclaredToolsPanel tools={declaredTools} toolCalls={traceToolCalls} /> : null}
    </div>
  );
}

function DeclaredToolsPanel({ tools, toolCalls = [] }) {
  const [selectedToolName, setSelectedToolName] = useState(() => tools[0]?.name || "");

  useEffect(() => {
    if (!tools.length) {
      setSelectedToolName("");
      return;
    }
    if (tools.some((tool) => tool.name === selectedToolName)) {
      return;
    }
    setSelectedToolName(tools[0].name || "");
  }, [selectedToolName, tools]);

  const selectedTool = tools.find((tool) => tool.name === selectedToolName) || tools[0] || null;
  const selectedToolCalls = selectedTool ? toolCalls.filter((call) => isSameToolName(call.function?.name, selectedTool.name)) : [];

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Declared tools</p>
          <h2>Request tools</h2>
        </div>
      </div>
      {tools.length ? (
        <div className="tool-layout">
          <div className="tool-list-column">
            {tools.map((tool, index) => {
              const count = countToolMatches(toolCalls, tool.name);
              const isSelected = selectedTool?.name === tool.name;
              return (
                <button
                  key={`${tool.name}-${index}`}
                  className={isSelected ? "tool-list-item tool-list-item-active" : "tool-list-item"}
                  onClick={() => setSelectedToolName(tool.name)}
                >
                  <div className="tool-list-item-head">
                    <strong>{tool.name}</strong>
                    <InlineTag tone={count > 0 ? "green" : "default"}>{count > 0 ? `${count} call${count > 1 ? "s" : ""}` : "not invoked"}</InlineTag>
                  </div>
                  <div className="tool-list-item-meta">
                    <span>{tool.source || tool.type || "tool"}</span>
                    <span>{buildToolSchemaSummary(tool.parameters)}</span>
                  </div>
                </button>
              );
            })}
          </div>
          <div className="tool-detail-column">
            {selectedTool ? (
              <>
                <div className="tool-detail-header">
                  <div>
                    <p className="eyebrow">Tool definition</p>
                    <h3>{selectedTool.name}</h3>
                  </div>
                  <div className="trace-tag-group">
                    <InlineTag tone="accent">{selectedTool.source || selectedTool.type || "tool"}</InlineTag>
                    <InlineTag tone={selectedToolCalls.length ? "green" : "default"}>
                      {selectedToolCalls.length ? `${selectedToolCalls.length} matched call${selectedToolCalls.length > 1 ? "s" : ""}` : "unused"}
                    </InlineTag>
                  </div>
                </div>
                <p className="tool-description">{selectedTool.description || "No description"}</p>
                <div className="tool-summary-grid">
                  <section className="breakdown-card">
                    <div className="breakdown-title">Schema summary</div>
                    <div className="tool-schema-summary">{buildToolSchemaSummary(selectedTool.parameters)}</div>
                  </section>
                  <section className="breakdown-card">
                    <div className="breakdown-title">Matched calls</div>
                    {selectedToolCalls.length ? (
                      <div className="tool-call-summary-list">
                        {selectedToolCalls.map((call, index) => (
                          <div key={`${call.id || call.function?.name}-${index}`} className="tool-call-summary-row">
                            <strong>{call.function?.name || "tool"}</strong>
                            <span>{call.id || "no call id"}</span>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <div className="empty-state empty-state-inline">This request declared the tool but did not call it.</div>
                    )}
                  </section>
                </div>
                <section className="breakdown-card">
                  <div className="breakdown-title">Raw schema</div>
                  <CodeBlock value={selectedTool.parameters || "{}"} />
                </section>
                {selectedToolCalls.length ? (
                  <section className="breakdown-card">
                    <div className="breakdown-title">Call arguments</div>
                    {selectedToolCalls.map((call, index) => (
                      <ToolCallView key={`${call.id || call.function?.name}-${index}`} call={call} match={selectedTool} />
                    ))}
                  </section>
                ) : null}
              </>
            ) : null}
          </div>
        </div>
      ) : (
        <div className="empty-state">No tool definitions in request.</div>
      )}
    </section>
  );
}

function RawProtocolPanel({ raw, focusTarget = "" }) {
  const [wrap, setWrap] = useState(false);
  const requestRef = useRef(null);
  const responseRef = useRef(null);

  useEffect(() => {
    if (focusTarget === "request" && requestRef.current) {
      requestRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
      return;
    }
    if (focusTarget === "response" && responseRef.current) {
      responseRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }, [focusTarget]);

  if (raw.error) {
    return <div className="empty-state error-box">{raw.error}</div>;
  }
  if (raw.loading && !raw.data) {
    return <div className="empty-state">Loading raw protocol...</div>;
  }

  return (
    <section className="panel raw-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Raw HTTP exchange</p>
          <h2>Request / Response</h2>
        </div>
        <label className="wrap-toggle">
          <input type="checkbox" checked={wrap} onChange={(event) => setWrap(event.target.checked)} />
          Wrap lines
        </label>
      </div>
      <div className="raw-grid">
        <ProtocolColumn ref={requestRef} title="Request" value={raw.data?.request_protocol || ""} wrap={wrap} focused={focusTarget === "request"} />
        <ProtocolColumn ref={responseRef} title="Response" value={raw.data?.response_protocol || ""} wrap={wrap} focused={focusTarget === "response"} />
      </div>
    </section>
  );
}

function TimelinePanel({ events, focusTarget = "" }) {
  const panelRef = useRef(null);
  const focusPath = focusTarget === "timeline_error" ? findFirstTimelineErrorPath(events) : [];

  useEffect(() => {
    if ((focusTarget !== "timeline" && focusTarget !== "timeline_error") || !panelRef.current) {
      return;
    }
    panelRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [focusTarget]);

  if (!events.length) {
    return <div className="empty-state">No timeline events recorded for this trace.</div>;
  }

  return (
    <section ref={panelRef} className={focusTarget === "timeline" ? "panel timeline-panel timeline-panel-focused" : "panel timeline-panel"}>
      <div className="panel-head">
        <div>
          <p className="eyebrow">Provider timeline</p>
          <h2>Unified llm event stream</h2>
        </div>
      </div>
      <div className="timeline-list">
        {events.map((event, index) => (
          <article key={`${event.type}-${index}`} className="timeline-item">
            <div className="timeline-rail">
              <span className={event.type?.startsWith("llm.") ? "timeline-dot timeline-dot-live" : "timeline-dot"} />
            </div>
            <div className="timeline-card">
              <div className="timeline-head">
                <div>
                  <strong>{event.type || "event"}</strong>
                  <span>{formatDateTime(event.time)}</span>
                </div>
                <span className="timeline-badge">{event.is_stream ? "stream" : "record"}</span>
              </div>
              {event.timeline_items?.length ? <TimelineTree items={event.timeline_items} focusPath={focusPath} /> : null}
              {!event.timeline_items?.length && event.message ? <div className="timeline-message">{event.message}</div> : null}
              {event.attributes ? <CodeBlock value={JSON.stringify(event.attributes, null, 2)} /> : null}
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

function TimelineTree({ items, focusPath = [] }) {
  return (
    <div className="timeline-tree">
      {items.map((item, index) => (
        <TimelineNode key={buildTimelineNodeKey(item, index)} nodeKey={buildTimelineNodeKey(item, index)} item={item} depth={0} focusPath={focusPath} />
      ))}
    </div>
  );
}

function TimelineNode({ item, depth = 0, nodeKey = "", focusPath = [] }) {
  const nodeRef = useRef(null);
  const hasChildren = Boolean(item.children?.length);
  const hasDetails = Boolean(item.body && item.body !== item.summary);
  const collapsible = hasChildren || hasDetails;
  const focused = focusPath.includes(nodeKey);
  const focusedBranch = focusPath.length > 0 && focused;
  const className = `timeline-node timeline-node-${item.kind || "item"}${focused ? " timeline-node-focused" : ""}`;

  useEffect(() => {
    if (!focused || !nodeRef.current) {
      return;
    }
    nodeRef.current.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [focused]);

  if (!collapsible) {
    return (
      <div ref={nodeRef} className={className}>
        <div className="timeline-node-leaf">
          <TimelineNodeHeading item={item} />
          {item.id ? <span className="timeline-node-id">{item.id}</span> : null}
          {item.status === "error" ? <InlineTag tone="danger">error</InlineTag> : null}
        </div>
        {item.summary ? <div className="timeline-node-preview">{item.summary}</div> : null}
      </div>
    );
  }

  return (
    <details ref={nodeRef} className={className} open={(depth === 0 && hasChildren) || focusedBranch}>
      <summary className="timeline-node-summary">
        <TimelineNodeHeading item={item} />
        {item.id ? <span className="timeline-node-id">{item.id}</span> : null}
        {item.status === "error" ? <InlineTag tone="danger">error</InlineTag> : null}
      </summary>
      {item.summary ? <div className="timeline-node-preview">{item.summary}</div> : null}
      {hasDetails ? <pre className="timeline-node-body">{item.body}</pre> : null}
      {hasChildren ? (
        <div className="timeline-children">
          {item.children.map((child, index) => (
            <TimelineNode
              key={buildTimelineNodeKey(child, index)}
              nodeKey={buildTimelineNodeKey(child, index)}
              item={child}
              depth={depth + 1}
              focusPath={focusPath}
            />
          ))}
        </div>
      ) : null}
    </details>
  );
}

function TimelineNodeHeading({ item }) {
  return (
    <div className="timeline-node-heading">
      <span className="timeline-node-kind">{formatTimelineKind(item.kind)}</span>
      <strong className="timeline-node-title">{formatTimelineTitle(item)}</strong>
    </div>
  );
}

function PayloadSummary({ raw }) {
  const requestBody = extractHTTPBody(raw.data?.request_protocol || "");
  const responseBody = extractHTTPBody(raw.data?.response_protocol || "");

  return (
    <div className="payload-grid">
      <section className="payload-card">
        <div className="protocol-head">Request body</div>
        <CodeBlock value={formatBodyForDisplay(requestBody)} />
      </section>
      <section className="payload-card">
        <div className="protocol-head">Response body</div>
        <CodeBlock value={formatBodyForDisplay(responseBody)} />
      </section>
    </div>
  );
}

const ProtocolColumn = React.forwardRef(function ProtocolColumn({ title, value, wrap, focused = false }, ref) {
  return (
    <div ref={ref} className={focused ? "protocol-column protocol-column-focused" : "protocol-column"}>
      <div className="protocol-head">{title}</div>
      <pre className={wrap ? "protocol-code protocol-code-wrap" : "protocol-code"}>{value}</pre>
    </div>
  );
});

function InlineTag({ children, tone = "default" }) {
  return <span className={`inline-tag inline-tag-${tone}`}>{children}</span>;
}

function MiniToken({ metric, value, tone = "default", icon = "total" }) {
  return (
    <span className={`mini-token mini-token-${tone}`}>
      <span className="metric-icon-wrap">
        <MetricIcon type={icon} />
      </span>
      <span className="mini-token-label">{metric}</span>
      <strong>{value || 0}</strong>
    </span>
  );
}

function TokenBadge({ label, value, accent = "", icon = "total" }) {
  return (
    <span className={`badge token-badge ${accent}`.trim()}>
      <span className="metric-icon-wrap token-badge-icon">
        <MetricIcon type={icon} />
      </span>
      <span className="token-badge-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function DetailMetaPill({ label, value, mono = false }) {
  return (
    <span className={`detail-meta-pill ${mono ? "mono" : ""}`.trim()}>
      <span className="detail-meta-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function IconFrame({ children }) {
  return <span className="icon-frame">{children}</span>;
}

function MetricIcon({ type = "total" }) {
  if (type === "input") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M14 3.5h-4.5M14 12.5h-4.5M6 8H14" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <path d="m6.5 4.5-3.5 3.5 3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (type === "output") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M2 3.5h4.5M2 12.5h4.5M2 8H10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <path d="m9.5 4.5 3.5 3.5-3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (type === "cached") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M5 5.5h7v7H5z" fill="none" stroke="currentColor" strokeWidth="1.3" />
        <path d="M3.5 3.5h7v7" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 4.5h10M3 8h10M3 11.5h10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

function ViewIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M2.5 12s3.4-6 9.5-6 9.5 6 9.5 6-3.4 6-9.5 6-9.5-6-9.5-6Z" fill="none" stroke="currentColor" strokeWidth="1.8" />
        <circle cx="12" cy="12" r="3.2" fill="none" stroke="currentColor" strokeWidth="1.8" />
      </svg>
    </IconFrame>
  );
}

function DownloadIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 4v10" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        <path d="m8 11.5 4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M5 19h14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    </IconFrame>
  );
}

function HomeIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M4 11.5 12 5l8 6.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M7.5 10.5V19h9v-8.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </IconFrame>
  );
}

function StackIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 4 4 8l8 4 8-4-8-4Z" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="m4 12 8 4 8-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="m4 16 8 4 8-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </IconFrame>
  );
}

function MessageCard({ message, renderMarkdown, declaredTools = [] }) {
  const alignClass = message.role === "assistant" ? "message-assistant" : message.role === "tool" ? "message-tool" : "message-user";
  const isCollapsible = message.message_type === "tool_use" || message.message_type === "tool_result";
  const toolSummary = buildToolMessageSummary(message, declaredTools);

  const body = (
    <article className={`message-card ${alignClass}`}>
      <div className="message-meta">
        <span className="role-pill">{message.role}</span>
        <span className="message-kind">{message.message_type || "message"}</span>
      </div>
      {toolSummary ? <div className="tool-message-summary">{toolSummary}</div> : null}
      {message.content ? (
        <MessageContent
          value={message.content}
          format={message.content_format}
          renderMarkdown={renderMarkdown}
          className="message-body"
        />
      ) : null}
      {message.tool_calls?.length ? message.tool_calls.map((call) => (
        <ToolCallView
          key={call.id || call.function?.name}
          call={call}
          match={findDeclaredToolForCall(call, declaredTools)}
        />
      )) : null}
      {message.blocks?.length ? message.blocks.map((block, index) => <BlockView key={`${block.kind}-${index}`} block={block} />) : null}
      {!message.content && !message.tool_calls?.length && !message.blocks?.length ? (
        <div className="tool-message-placeholder">No structured payload was captured for this tool event.</div>
      ) : null}
    </article>
  );

  if (!isCollapsible) {
    return body;
  }

  return (
    <CollapsibleCard
      title={`${message.role} / ${message.message_type}`}
      subtitle={toolSummary || message.name || message.tool_call_id || ""}
      defaultOpen={false}
      bodyClassName="collapse-plain"
    >
      {body}
    </CollapsibleCard>
  );
}

function ToolCallView({ call, match = null }) {
  return (
    <div className="tool-call-box">
      <div className="tool-call-head">
        <div className="tool-call-title">{call.function?.name || "tool"}</div>
        {match?.name ? <InlineTag tone="accent">declared</InlineTag> : null}
      </div>
      {call.id ? <div className="tool-call-meta">call id {call.id}</div> : null}
      <CodeBlock value={call.function?.arguments || "{}"} />
    </div>
  );
}

function BlockView({ block }) {
  return (
    <div className="tool-call-box">
      <div className="tool-call-title">{block.title || block.kind}</div>
      <CodeBlock value={block.text || block.meta || ""} />
    </div>
  );
}

function CollapsibleCard({ title, subtitle, defaultOpen = false, children, bodyClassName = "" }) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <section className="collapse-card">
      <button className="collapse-head" onClick={() => setOpen((value) => !value)}>
        <div>
          <strong>{title}</strong>
          {subtitle ? <span>{subtitle}</span> : null}
        </div>
        <span>{open ? "hide" : "show"}</span>
      </button>
      {open ? <div className={`collapse-body ${bodyClassName}`.trim()}>{children}</div> : null}
    </section>
  );
}

function StatCard({ label, value, accent = "", detail = "", mono = false }) {
  return (
    <article className={`stat-card ${accent}`.trim()}>
      <span>{label}</span>
      <strong className={mono ? "mono" : ""}>{value}</strong>
      {detail ? <small className={mono ? "mono stat-detail" : "stat-detail"}>{detail}</small> : null}
    </article>
  );
}

function CodeBlock({ value }) {
  return <pre className="code-block">{value}</pre>;
}

function MessageContent({ value, format, renderMarkdown, className = "" }) {
  if (renderMarkdown && format === "markdown") {
    return <MarkdownBlock value={value} className={className} />;
  }
  return <div className={`${className} prose-block`.trim()}>{value}</div>;
}

function MarkdownBlock({ value, className = "" }) {
  return <div className={`${className} prose-block rendered-markdown`.trim()} dangerouslySetInnerHTML={{ __html: renderMarkdownToHTML(value) }} />;
}

function renderMarkdownToHTML(input) {
  if (!input) {
    return "";
  }

  const codeBlocks = [];
  const placeholderPrefix = "__LLM_TRACELAB_CODE_BLOCK_";
  let text = String(input).replace(/\r\n/g, "\n");

  text = text.replace(/```([\w-]+)?\n([\s\S]*?)```/g, (_, language = "", code = "") => {
    const html = `<pre class="md-pre"><code${language ? ` data-lang="${escapeHTML(language)}"` : ""}>${escapeHTML(code.trimEnd())}</code></pre>`;
    const token = `${placeholderPrefix}${codeBlocks.length}__`;
    codeBlocks.push(html);
    return token;
  });

  const blocks = text
    .split(/\n{2,}/)
    .map((block) => block.trim())
    .filter(Boolean)
    .map((block) => renderMarkdownBlock(block, placeholderPrefix));

  let html = blocks.join("");
  codeBlocks.forEach((codeBlock, index) => {
    html = html.replace(`${placeholderPrefix}${index}__`, codeBlock);
  });
  return html;
}

function renderMarkdownBlock(block, placeholderPrefix) {
  if (block.startsWith(placeholderPrefix)) {
    return block;
  }

  const lines = block.split("\n");
  if (lines.every((line) => /^>\s?/.test(line))) {
    const content = lines.map((line) => renderMarkdownInline(line.replace(/^>\s?/, ""))).join("<br />");
    return `<blockquote>${content}</blockquote>`;
  }
  if (lines.every((line) => /^[-*]\s+/.test(line))) {
    return `<ul>${lines.map((line) => `<li>${renderMarkdownInline(line.replace(/^[-*]\s+/, ""))}</li>`).join("")}</ul>`;
  }
  if (lines.every((line) => /^\d+\.\s+/.test(line))) {
    return `<ol>${lines.map((line) => `<li>${renderMarkdownInline(line.replace(/^\d+\.\s+/, ""))}</li>`).join("")}</ol>`;
  }

  const heading = block.match(/^(#{1,6})\s+(.+)$/);
  if (heading) {
    const level = Math.min(heading[1].length, 6);
    return `<h${level}>${renderMarkdownInline(heading[2])}</h${level}>`;
  }

  return `<p>${lines.map((line) => renderMarkdownInline(line)).join("<br />")}</p>`;
}

function renderMarkdownInline(text) {
  let html = escapeHTML(text);
  html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
  html = html.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|mailto:[^\s)]+)\)/g, (_, label, href) => {
    const safeHref = escapeHTML(href);
    return `<a href="${safeHref}" target="_blank" rel="noreferrer">${label}</a>`;
  });
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  html = html.replace(/(^|[\s(])\*([^*]+)\*(?=[\s).,!?:;]|$)/g, "$1<em>$2</em>");
  html = html.replace(/(^|[\s(])_([^_]+)_(?=[\s).,!?:;]|$)/g, "$1<em>$2</em>");
  return html;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function hasConversation(detail) {
  return Boolean(
    detail.messages?.length ||
      detail.ai_content ||
      detail.ai_reasoning ||
      detail.ai_blocks?.length ||
      detail.tool_calls?.length
  );
}

function extractHTTPBody(protocol = "") {
  if (!protocol) {
    return "";
  }
  const separator = protocol.includes("\r\n\r\n") ? "\r\n\r\n" : "\n\n";
  const index = protocol.indexOf(separator);
  if (index === -1) {
    return protocol;
  }
  return protocol.slice(index + separator.length);
}

function formatBodyForDisplay(value = "") {
  const trimmed = String(value).trim();
  if (!trimmed) {
    return "(empty)";
  }
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return trimmed;
  }
}

function formatTimelineKind(kind = "") {
  switch (kind) {
    case "message":
      return "message";
    case "tool_call":
      return "tool call";
    case "tool_response":
      return "tool response";
    case "thinking":
      return "thinking";
    case "output":
      return "output";
    default:
      return kind || "item";
  }
}

function formatTimelineTitle(item = {}) {
  if (item.kind === "message") {
    return item.label || item.role || "Message";
  }
  return item.name || item.label || formatTimelineKind(item.kind);
}

function buildTimelineNodeKey(item = {}, index = 0) {
  return `${item.kind || "item"}-${item.id || item.name || item.label || index}`;
}

function findFirstTimelineErrorPath(events = []) {
  for (const event of events) {
    const path = findTimelineItemErrorPath(event.timeline_items || []);
    if (path.length) {
      return path;
    }
  }
  return [];
}

function findTimelineItemErrorPath(items = []) {
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    const nodeKey = buildTimelineNodeKey(item, index);
    if (item.status === "error") {
      return [nodeKey];
    }
    const childPath = findTimelineItemErrorPath(item.children || []);
    if (childPath.length) {
      return [nodeKey, ...childPath];
    }
  }
  return [];
}

export default App;
