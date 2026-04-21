import React from "react";
import { Link } from "react-router-dom";
import { DownloadIcon, InlineTag, MiniToken, StackIcon, ViewIcon } from "../common/Badges";
import { buildTraceLink, formatDateTime, formatEndpointTag, formatProviderTag } from "../../lib/monitor";

export function RequestList({ items, fromView = "", fromSessionID = "", focusFailures = false }) {
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
