import React from "react";

export function EmptyState({ title, detail = "", tone = "default", compact = false }) {
  const className = [
    "empty-state",
    compact ? "empty-state-inline" : "",
    tone === "danger" ? "empty-state-danger" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={className}>
      <strong className="empty-state-title">{title}</strong>
      {detail ? <div className="empty-state-detail">{detail}</div> : null}
    </div>
  );
}
