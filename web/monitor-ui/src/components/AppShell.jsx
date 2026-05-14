import React, { useEffect, useState } from "react";
import { PrimaryNav } from "./PrimaryNav";

const SIDEBAR_COLLAPSED_KEY = "llm-tracelab.monitor.sidebar.collapsed";

export function AppShell({ children, user, onLogout }) {
  const [collapsed, setCollapsed] = useState(() => window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "true");

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? "true" : "false");
  }, [collapsed]);

  return (
    <div className={collapsed ? "app-shell app-shell-collapsed" : "app-shell"}>
      <aside className="app-sidebar">
        <PrimaryNav user={user} onLogout={onLogout} collapsed={collapsed} onToggleCollapsed={() => setCollapsed((value) => !value)} />
      </aside>
      <main className="app-main">{children}</main>
    </div>
  );
}
