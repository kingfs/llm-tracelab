import React from "react";
import { Link } from "react-router-dom";
import { InlineTag, StackIcon } from "../common/Badges";
import { formatDateTime, formatProviderTag } from "../../lib/monitor";

export function SessionList({ items }) {
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
