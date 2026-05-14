import React from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "../common/EmptyState";

export function BreakdownList({ title, items, formatter, linkFor }) {
  return (
    <section className="breakdown-card">
      <div className="breakdown-title">{title}</div>
      {items.length ? (
        <div className="breakdown-list">
          {items.map((item) => {
            const content = (
              <>
                <span className="breakdown-label">{formatter(item)}</span>
                <strong>{item.count}</strong>
              </>
            );
            const link = linkFor ? linkFor(item) : "";
            return link ? (
              <Link key={`${title}-${item.label}`} className="breakdown-row breakdown-row-link" to={link}>
                {content}
              </Link>
            ) : (
              <div key={`${title}-${item.label}`} className="breakdown-row">
                {content}
              </div>
            );
          })}
        </div>
      ) : (
        <EmptyState title="No distribution data" detail="This section has nothing to aggregate in the current filter window." compact />
      )}
    </section>
  );
}
