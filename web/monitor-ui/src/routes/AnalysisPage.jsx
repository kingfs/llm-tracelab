import React from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";

export function AnalysisPage() {
  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Offline runs</p>
          <h1>Analysis</h1>
        </div>
      </header>
      <section className="panel">
        <EmptyState title="Analysis workspace is staged" detail="Session analysis runs are exposed through the session detail API. The dedicated run browser lands in the next frontend phase." />
        <div className="panel-foot-actions">
          <Link className="ghost-button" to="/sessions">Open Sessions</Link>
        </div>
      </section>
    </div>
  );
}
