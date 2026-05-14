import React from "react";
import { PrimaryNav } from "./PrimaryNav";

export function AppShell({ children, user, onLogout }) {
  return (
    <div className="app-shell">
      <aside className="app-sidebar">
        <PrimaryNav user={user} onLogout={onLogout} />
      </aside>
      <main className="app-main">{children}</main>
    </div>
  );
}
