import React from "react";

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
        <div className="empty-state">No data</div>
      )}
    </section>
  );
}
