import React from "react";
import { Link } from "react-router-dom";

const entries = [
  { to: "/sessions", title: "Sessions", detail: "Review grouped agent and workflow activity." },
  { to: "/traces", title: "Traces", detail: "Inspect raw HTTP exchanges and protocol details." },
  { to: "/audit", title: "Audit", detail: "Triage findings and evidence paths." },
  { to: "/routing", title: "Upstreams", detail: "Check provider routing, health, and failures." },
  { to: "/analysis", title: "Analysis", detail: "Browse deterministic session analysis runs." },
];

export function OverviewPage() {
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
    </div>
  );
}
