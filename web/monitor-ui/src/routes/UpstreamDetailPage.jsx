import React, { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useJSON } from "../hooks/useJSON";
import {
  buildRoutingLink,
  buildTraceLink,
  computeTTFTRatio,
  formatCapacity,
  formatDateTime,
  formatEndpointTag,
  formatFailureReason,
  formatHealthLabel,
  formatMultiplier,
  formatRatio,
  formatTime,
  healthTone,
  metricThresholdTone,
  normalizeUpstreamWindow,
  resolveThresholdState,
  setOrDeleteParam,
} from "../lib/monitor";

export function UpstreamDetailPage({
  BreakdownList,
  DetailMetaPill,
  HomeIcon,
  InlineTag,
  RequestList,
  RoutingFailureTimeline,
  StatCard,
  TokenBadge,
}) {
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

function buildUpstreamHealthSummary(target, failureReasons = [], thresholds = null) {
  const health = formatHealthLabel(target?.health_state || "unknown");
  const errorRate = formatRatio(target?.error_rate);
  const timeoutRate = formatRatio(target?.timeout_rate);
  const topReason = failureReasons[0]?.label ? formatFailureReason(failureReasons[0].label) : "no dominant failure reason";
  const signals = [];
  const errorState = resolveThresholdState(target?.error_rate, thresholds?.error_rate_degraded, thresholds?.error_rate_open);
  if (errorState !== "healthy" && errorState !== "unknown") {
    signals.push(`error ${errorState}`);
  }
  const timeoutState = resolveThresholdState(target?.timeout_rate, thresholds?.timeout_rate_degraded, thresholds?.timeout_rate_open);
  if (timeoutState !== "healthy" && timeoutState !== "unknown") {
    signals.push(`timeout ${timeoutState}`);
  }
  const ttftState = resolveThresholdState(computeTTFTRatio(target), thresholds?.ttft_degraded_ratio, null);
  if (ttftState !== "healthy" && ttftState !== "unknown") {
    signals.push(`ttft ${ttftState}`);
  }
  const signalText = signals.length ? ` Thresholds: ${signals.join(", ")}.` : "";
  return `${health} with error ${errorRate}, timeout ${timeoutRate}, dominant failure ${topReason}.${signalText}`;
}
