import React from "react";
import { Link } from "react-router-dom";
import { StatCard } from "../common/Display";
import { EmptyState } from "../common/EmptyState";
import { InlineTag } from "../common/Badges";
import { BreakdownList } from "../monitor/BreakdownList";
import { RoutingFailureTimeline } from "../monitor/RoutingFailureTimeline";
import {
  buildTraceLink,
  buildUpstreamLink,
  formatCapacity,
  formatDateTime,
  formatEndpointTag,
  formatFailureReason,
  formatHealthLabel,
  formatRatio,
  healthTone,
} from "../../lib/monitor";

export function UpstreamOverview({ items, analyticsWindow = "24h", analyticsModel = "", routingFailures = {} }) {
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
          <EmptyState title="No routing failures" detail="The router has not produced selection-stage failures in this analytics window." />
        )}
        {(routingFailures.timeline || []).length ? (
          <section className="breakdown-card">
            <div className="breakdown-title">Failure timeline</div>
            <RoutingFailureTimeline items={routingFailures.timeline || []} />
          </section>
        ) : null}
      </div>
      {!items.length ? <EmptyState title="No upstream targets discovered" detail="No upstream routing targets have been indexed for the current window and model filter." /> : null}
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
                <Link className="ghost-button" to={buildUpstreamLink(item.id, analyticsWindow, analyticsModel)}>
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
