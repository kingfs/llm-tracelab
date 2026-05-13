import React from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";

export function AuditPage() {
  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Findings</p>
          <h1>Audit</h1>
        </div>
      </header>
      <section className="panel">
        <EmptyState title="Audit workspace is staged" detail="Trace-level findings are available from trace detail today. The cross-trace audit list lands in the next frontend phase." />
        <div className="panel-foot-actions">
          <Link className="ghost-button" to="/traces">Open Traces</Link>
        </div>
      </section>
    </div>
  );
}
