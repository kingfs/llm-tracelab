import React from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "../common/EmptyState";
import { useI18n } from "../../lib/i18n";

export function BreakdownList({ title, items, formatter, linkFor }) {
  const { t } = useI18n();
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
        <EmptyState title={t("common.noDistribution")} detail={t("common.noDistributionDetail")} compact />
      )}
    </section>
  );
}
