import React, { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DetailMetaPill, HomeIcon, InlineTag, TokenBadge, ViewIcon } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { BreakdownList } from "../components/monitor/BreakdownList";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import {
  buildFailureContexts,
  buildFailureDelta,
  buildFailureDetail,
  buildFailureSummary,
  buildTraceLink,
  formatDateTime,
  formatDuration,
  formatEndpointTag,
  formatFailureReason,
  formatProviderTag,
  formatSignedMetric,
  formatTokenRate,
} from "../lib/monitor";

export function SessionDetailPage() {
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

      {detail.error ? <EmptyState title="Unable to load session detail" detail={detail.error} tone="danger" /> : null}
      {detail.loading && !detail.data ? <EmptyState title="Loading session detail" detail="Resolving timeline, breakdown, and grouped traces for this session." /> : null}

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
              <StatCard label="Duration" value={formatDuration(summary?.total_duration_ms ?? 0)} detail={`${summary?.total_duration_ms ?? 0} ms total`} />
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

      {detail.data && timeline.length ? (
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
                    <span>duration {formatDuration(item.duration_ms)}</span>
                    <span>ttft {formatDuration(item.ttft_ms)}</span>
                    <span>tokens {item.total_tokens}</span>
                    <span>rate {formatTokenRate(item.total_tokens, item.duration_ms)}</span>
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
      ) : detail.data ? (
        <EmptyState title="No session timeline" detail="This session does not yet have a timeline of recorded requests." />
      ) : null}

      {detail.data && failureContexts.length ? (
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
      ) : detail.data ? (
        <EmptyState title="No failure context" detail="This session has no failed requests, so no adjacent context needs review." />
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
          <EmptyState title="No failed traces" detail="This session has no failed requests under the current filter." />
        ) : (
          <RequestList items={visibleTraces} fromSessionID={summary?.session_id || sessionID} focusFailures groupSessionFailures />
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
      <span>duration {formatDuration(item.duration_ms)}</span>
      <span>tokens {item.total_tokens}</span>
      <span>rate {formatTokenRate(item.total_tokens, item.duration_ms)}</span>
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
