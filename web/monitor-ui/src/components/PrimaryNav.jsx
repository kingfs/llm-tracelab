import React from "react";
import { NavLink } from "react-router-dom";

export function PrimaryNav() {
  return (
    <nav className="primary-nav" aria-label="Primary">
      <NavLink to="/requests" className={({ isActive }) => isActive ? "nav-chip nav-chip-active" : "nav-chip"}>
        Requests
      </NavLink>
      <NavLink to="/sessions" className={({ isActive }) => isActive ? "nav-chip nav-chip-active" : "nav-chip"}>
        Sessions
      </NavLink>
      <NavLink to="/routing" className={({ isActive }) => isActive ? "nav-chip nav-chip-active" : "nav-chip"}>
        Routing
      </NavLink>
    </nav>
  );
}
