import React from "react";
import { Link } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { RequestList } from "../components/monitor/RequestList";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL } from "../lib/api";

const entries = [
  { to: "/sessions", title: "Sessions", detail: "Review grouped agent and workflow activity." },
  { to: "/traces", title: "Traces", detail: "Inspect raw HTTP exchanges and protocol details." },
  { to: "/audit", title: "Audit", detail: "Triage findings and evidence paths." },
  { to: "/routing", title: "Upstreams", detail: "Check provider routing, health, and failures." },
  { to: "/analysis", title: "Analysis", detail: "Browse deterministic session analysis runs." },
];

export function OverviewPage() {
  const traces = useJSON(apiURL(apiPaths.traces, { page: "1", page_size: "10" }), []);
  const findings = useJSON(apiURL(apiPaths.findings, { limit: "10" }), []);
  const analysis = useJSON(apiURL(apiPaths.analysis, { limit: "5" }), []);
  const stats = traces.data?.stats || {};
  const highFindings = (findings.data?.items || []).filter((item) => item.severity === "high" || item.severity === "critical");

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Local First LLM Replay Proxy</p>
          <h1>Overview</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">workspace</span>
        </div>
      </header>

      <section className="overview-grid">
        {entries.map((entry) => (
          <Link className="overview-tile" key={entry.to} to={entry.to}>
            <strong>{entry.title}</strong>
            <span>{entry.detail}</span>
          </Link>
        ))}
      </section>
      <section className="hero-grid">
        <StatCard label="Requests" value={stats.total_request ?? 0} />
        <StatCard label="Success" value={`${Number(stats.success_rate ?? 0).toFixed(1)}%`} accent="accent-green" />
        <StatCard label="Findings" value={findings.data?.total ?? 0} accent={highFindings.length ? "accent-red" : ""} />
        <StatCard label="Analysis" value={analysis.data?.total ?? 0} accent="accent-gold" />
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent traffic</p>
            <h2>Latest traces</h2>
          </div>
        </div>
        {traces.error ? <EmptyState title="Unable to load traces" detail={traces.error} tone="danger" /> : null}
        {traces.data ? <RequestList items={traces.data.items || []} /> : null}
      </section>
    </div>
  );
}
