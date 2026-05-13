import React from "react";
import { PrimaryNav } from "./PrimaryNav";

export function AppShell({ children }) {
  return (
    <div className="app-shell">
      <aside className="app-sidebar">
        <PrimaryNav />
      </aside>
      <main className="app-main">{children}</main>
    </div>
  );
}
