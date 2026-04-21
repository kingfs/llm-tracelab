import React, { useEffect, useState } from "react";
import { Link, Navigate, Route, Routes, useParams, useSearchParams } from "react-router-dom";
import { PrimaryNav } from "./components/PrimaryNav";
import { useJSON } from "./hooks/useJSON";
import { TraceDetailPage } from "./routes/TraceDetailPage";
import { UpstreamDetailPage } from "./routes/UpstreamDetailPage";
import {
  buildFailureContexts,
  buildFailureDelta,
  buildFailureDetail,
  buildFailureSummary,
  buildTraceLink,
  buildUpstreamLink,
  formatCapacity,
  formatDateTime,
  formatEndpointTag,
  formatFailureReason,
  formatHealthLabel,
  formatProviderTag,
  formatRatio,
  formatSignedMetric,
  formatTime,
  formatTimelineBucketLabel,
  healthTone,
  normalizeUpstreamWindow,
  setOrDeleteParam,
  summarizeSessionItems,
} from "./lib/monitor";

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
      <Route
        path="/upstreams/:upstreamID"
        element={
          <UpstreamDetailPage
            BreakdownList={BreakdownList}
            DetailMetaPill={DetailMetaPill}
            HomeIcon={HomeIcon}
            InlineTag={InlineTag}
            RequestList={RequestList}
            RoutingFailureTimeline={RoutingFailureTimeline}
            StatCard={StatCard}
            TokenBadge={TokenBadge}
          />
        }
      />
      <Route
        path="/traces/:traceID"
        element={
          <TraceDetailPage
            CollapsibleCard={CollapsibleCard}
            CodeBlock={CodeBlock}
            DetailMetaPill={DetailMetaPill}
            DownloadIcon={DownloadIcon}
            HomeIcon={HomeIcon}
            InlineTag={InlineTag}
            MessageContent={MessageContent}
            StackIcon={StackIcon}
            TokenBadge={TokenBadge}
          />
        }
      />
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

export default App;
