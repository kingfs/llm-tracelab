import React from "react";
import { Link } from "react-router-dom";
import { InlineTag, StackIcon } from "../common/Badges";
import { EmptyState } from "../common/EmptyState";
import { useI18n } from "../../lib/i18n";
import { formatDateTime, formatDuration, formatProviderTag, formatTokenCount } from "../../lib/monitor";

export function SessionList({ items }) {
  const { t } = useI18n();
  if (!items.length) {
    return <EmptyState title={t("sessions.noFound")} detail={t("sessions.noFoundDetail")} />;
  }

  return (
    <div className="session-table">
      <div className="session-table-head">
        <span>{t("sessions.title")}</span>
        <span>{t("common.requests")}</span>
        <span>{t("sessions.health")}</span>
        <span>{t("common.tokens")}</span>
        <span>{t("common.actions")}</span>
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
            <div className="trace-subline-group">
              <span className="trace-subline mono">{item.session_id}</span>
              <span className="trace-subline">{t("sessions.last")} {formatDateTime(item.last_seen)}</span>
            </div>
          </div>
          <div className="trace-metric-stack">
            <strong>{item.request_count}</strong>
            <span>{t("sessions.streams")} {item.stream_count || 0}</span>
          </div>
          <div className="trace-metric-stack">
            <strong className={item.failed_request > 0 ? "status-err" : "status-ok"}>{Number(item.success_rate ?? 0).toFixed(1)}%</strong>
            <span>ttft {formatDuration(item.avg_ttft ?? 0)}</span>
          </div>
          <div className="trace-metric-stack">
            <strong title={String(item.total_tokens ?? 0)}>{formatTokenCount(item.total_tokens ?? 0)}</strong>
            <span>{t("sessions.duration")} {formatDuration(item.total_duration_ms ?? 0)}</span>
          </div>
          <div className="action-group">
            <Link className="icon-button" to={`/sessions/${encodeURIComponent(item.session_id)}`} title={t("requests.viewSession")} aria-label={t("requests.viewSession")}>
              <StackIcon />
            </Link>
          </div>
        </article>
      ))}
    </div>
  );
}
