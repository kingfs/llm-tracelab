import React from "react";

export function InlineTag({ children, tone = "default" }) {
  return <span className={`inline-tag inline-tag-${tone}`}>{children}</span>;
}

export function MiniToken({ metric, value, tone = "default", icon = "total" }) {
  return (
    <span className={`mini-token mini-token-${tone}`}>
      <span className="metric-icon-wrap">
        <MetricIcon type={icon} />
      </span>
      <span className="mini-token-label">{metric}</span>
      <strong>{value || 0}</strong>
    </span>
  );
}

export function TokenBadge({ label, value, accent = "", icon = "total" }) {
  return (
    <span className={`badge token-badge ${accent}`.trim()}>
      <span className="metric-icon-wrap token-badge-icon">
        <MetricIcon type={icon} />
      </span>
      <span className="token-badge-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

export function DetailMetaPill({ label, value, mono = false }) {
  return (
    <span className={`detail-meta-pill ${mono ? "mono" : ""}`.trim()}>
      <span className="detail-meta-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function IconFrame({ children }) {
  return <span className="icon-frame">{children}</span>;
}

function MetricIcon({ type = "total" }) {
  if (type === "input") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M14 3.5h-4.5M14 12.5h-4.5M6 8H14" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <path d="m6.5 4.5-3.5 3.5 3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (type === "output") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M2 3.5h4.5M2 12.5h4.5M2 8H10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <path d="m9.5 4.5 3.5 3.5-3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (type === "cached") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M5 5.5h7v7H5z" fill="none" stroke="currentColor" strokeWidth="1.3" />
        <path d="M3.5 3.5h7v7" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 4.5h10M3 8h10M3 11.5h10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

export function ViewIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M2.5 12s3.4-6 9.5-6 9.5 6 9.5 6-3.4 6-9.5 6-9.5-6-9.5-6Z" fill="none" stroke="currentColor" strokeWidth="1.8" />
        <circle cx="12" cy="12" r="3.2" fill="none" stroke="currentColor" strokeWidth="1.8" />
      </svg>
    </IconFrame>
  );
}

export function DownloadIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 4v10" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        <path d="m8 11.5 4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M5 19h14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    </IconFrame>
  );
}

export function HomeIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M4 11.5 12 5l8 6.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M7.5 10.5V19h9v-8.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </IconFrame>
  );
}

export function StackIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 4 4 8l8 4 8-4-8-4Z" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="m4 12 8 4 8-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="m4 16 8 4 8-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </IconFrame>
  );
}
