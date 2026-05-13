import React from "react";
import { NavLink } from "react-router-dom";

const navItems = [
  { to: "/overview", label: "Overview", hint: "Status" },
  { to: "/sessions", label: "Sessions", hint: "Workflows" },
  { to: "/traces", label: "Traces", hint: "HTTP" },
  { to: "/audit", label: "Audit", hint: "Findings" },
  { to: "/routing", label: "Upstreams", hint: "Routing" },
  { to: "/analysis", label: "Analysis", hint: "Runs" },
  { to: "/tokens", label: "Tokens", hint: "Access" },
];

export function PrimaryNav() {
  return (
    <nav className="primary-nav" aria-label="Primary navigation">
      <div className="nav-brand">
        <span className="nav-brand-mark">LT</span>
        <div>
          <strong>TraceLab</strong>
          <span>Local AI observability</span>
        </div>
      </div>
      <div className="nav-section">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) => (isActive || isLegacyActive(item.to) ? "nav-chip nav-chip-active" : "nav-chip")}
          >
            <span>{item.label}</span>
            <small>{item.hint}</small>
          </NavLink>
        ))}
      </div>
    </nav>
  );
}

function isLegacyActive(path) {
  return path === "/traces" && window.location.pathname === "/requests";
}
