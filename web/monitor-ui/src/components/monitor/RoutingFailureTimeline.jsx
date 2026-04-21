import React from "react";
import { formatTimelineBucketLabel } from "../../lib/monitor";

export function RoutingFailureTimeline({ items = [] }) {
  const maxCount = items.reduce((max, item) => Math.max(max, Number(item.count || 0)), 0);
  const columnCount = Math.max(items.length, 1);

  return (
    <div className="routing-timeline" style={{ gridTemplateColumns: `repeat(${columnCount}, minmax(0, 1fr))` }}>
      {items.map((item, index) => {
        const count = Number(item.count || 0);
        const height = maxCount > 0 ? Math.max((count / maxCount) * 100, count > 0 ? 16 : 6) : 6;
        return (
          <div key={`${item.time || index}`} className="routing-timeline-item">
            <div className="routing-timeline-count">{count}</div>
            <div className="routing-timeline-bar-wrap">
              <div className={count > 0 ? "routing-timeline-bar routing-timeline-bar-active" : "routing-timeline-bar"} style={{ height: `${height}%` }} />
            </div>
            <div className="routing-timeline-label">{formatTimelineBucketLabel(item.time)}</div>
          </div>
        );
      })}
    </div>
  );
}
