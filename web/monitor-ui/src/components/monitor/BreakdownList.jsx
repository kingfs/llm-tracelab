import React from "react";
import { EmptyState } from "../common/EmptyState";

export function BreakdownList({ title, items, formatter }) {
  return (
    <section className="breakdown-card">
      <div className="breakdown-title">{title}</div>
      {items.length ? (
        <div className="breakdown-list">
          {items.map((item) => (
            <div key={`${title}-${item.label}`} className="breakdown-row">
              <span className="breakdown-label">{formatter(item)}</span>
              <strong>{item.count}</strong>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState title="No distribution data" detail="This section has nothing to aggregate in the current filter window." compact />
      )}
    </section>
  );
}
