import React from "react";
import { Link, useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";
import { DetailMetaPill, InlineTag } from "../components/common/Badges";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";
import { setOrDeleteParam } from "../lib/monitor";

export function AuditPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const category = searchParams.get("category") || "";
  const severity = searchParams.get("severity") || "";
  const params = new URLSearchParams({ limit: "50" });
  if (category) {
    params.set("category", category);
  }
  if (severity) {
    params.set("severity", severity);
  }
  const findings = useJSON(apiURL(apiPaths.findings, params), [category, severity]);
  const items = findings.data?.items || [];
  const setFilter = (key, value) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, key, value);
    setSearchParams(next);
  };
  const resetFilters = () => {
    const next = new URLSearchParams(searchParams);
    next.delete("category");
    next.delete("severity");
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Findings</p>
          <h1>Audit</h1>
        </div>
      </header>
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Cross-trace findings</p>
            <h2>Latest findings</h2>
          </div>
          <InlineTag tone={items.length ? "danger" : "green"}>{findings.data?.total ?? 0} total</InlineTag>
        </div>
        <form className="filter-bar" onSubmit={(event) => event.preventDefault()}>
          <input className="filter-input" type="search" placeholder="category" value={category} onChange={(event) => setFilter("category", event.target.value)} />
          <select className="filter-input" aria-label="Finding severity" value={severity} onChange={(event) => setFilter("severity", event.target.value)}>
            <option value="">Any severity</option>
            <option value="critical">Critical</option>
            <option value="high">High</option>
            <option value="medium">Medium</option>
            <option value="low">Low</option>
          </select>
          <button className="ghost-button" type="button" onClick={resetFilters}>Reset</button>
        </form>
        {findings.error ? <EmptyState title="Unable to load findings" detail={findings.error} tone="danger" /> : null}
        {findings.loading && !findings.data ? <EmptyState title="Loading findings" detail="Reading deterministic findings across traces." /> : null}
        {items.length ? (
          <div className="finding-list">
            {items.map((finding) => (
              <article key={`${finding.trace_id}-${finding.id}`} className="finding-card">
                <div className="finding-card-head">
                  <div>
                    <strong>{finding.title || finding.category}</strong>
                    <span>{finding.description || finding.evidence_path}</span>
                  </div>
                  <div className="trace-tag-group">
                    <InlineTag tone={finding.severity === "high" || finding.severity === "critical" ? "danger" : "gold"}>{finding.severity}</InlineTag>
                    <InlineTag>{finding.category}</InlineTag>
                  </div>
                </div>
                <div className="detail-meta-strip">
                  <DetailMetaPill label="trace" value={finding.trace_id} mono />
                  <DetailMetaPill label="node" value={finding.node_id || "-"} mono />
                  <DetailMetaPill label="detector" value={`${finding.detector || "-"} ${finding.detector_version || ""}`.trim()} />
                </div>
                <div className="action-group action-group-start">
                  <Link className="ghost-button" to={`/traces/${encodeURIComponent(finding.trace_id)}?tab=audit`}>Open Finding</Link>
                  <Link className="ghost-button" to={`/traces/${encodeURIComponent(finding.trace_id)}?tab=protocol`}>Protocol</Link>
                </div>
              </article>
            ))}
          </div>
        ) : findings.data ? (
          <EmptyState title="No findings" detail="No deterministic audit findings have been stored yet." />
        ) : null}
      </section>
    </div>
  );
}
