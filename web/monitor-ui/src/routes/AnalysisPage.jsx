import React from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";
import { DetailMetaPill, InlineTag } from "../components/common/Badges";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import { formatDateTime } from "../lib/monitor";

export function AnalysisPage() {
  const analysis = useJSON(apiURL(apiPaths.analysis, { limit: "50" }), []);
  const items = analysis.data?.items || [];
  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Offline runs</p>
          <h1>Analysis</h1>
        </div>
      </header>
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Persisted runs</p>
            <h2>Latest analysis</h2>
          </div>
          <InlineTag>{analysis.data?.total ?? 0} total</InlineTag>
        </div>
        {analysis.error ? <EmptyState title="Unable to load analysis" detail={analysis.error} tone="danger" /> : null}
        {analysis.loading && !analysis.data ? <EmptyState title="Loading analysis" detail="Reading persisted analysis runs." /> : null}
        {items.length ? (
          <div className="finding-list">
            {items.map((run) => (
              <article key={run.id} className="finding-card">
                <div className="finding-card-head">
                  <div>
                    <strong>{run.kind}</strong>
                    <span>{run.analyzer} {run.analyzer_version}</span>
                  </div>
                  <InlineTag tone={run.status === "completed" ? "green" : "gold"}>{run.status}</InlineTag>
                </div>
                <div className="detail-meta-strip">
                  <DetailMetaPill label="session" value={run.session_id || "-"} mono />
                  <DetailMetaPill label="trace" value={run.trace_id || "-"} mono />
                  <DetailMetaPill label="input" value={run.input_ref || "-"} mono />
                  <DetailMetaPill label="created" value={formatDateTime(run.created_at)} />
                </div>
                <div className="action-group action-group-start">
                  {run.session_id ? <Link className="ghost-button" to={`/sessions/${encodeURIComponent(run.session_id)}`}>Open Session</Link> : null}
                  {run.trace_id ? <Link className="ghost-button" to={`/traces/${encodeURIComponent(run.trace_id)}`}>Open Trace</Link> : null}
                </div>
              </article>
            ))}
          </div>
        ) : analysis.data ? (
          <EmptyState title="No analysis runs" detail="Run analyze session --session-id to create deterministic session analysis." />
        ) : null}
      </section>
    </div>
  );
}
