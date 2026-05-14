import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { NavLink } from "react-router-dom";
import { apiPaths, postJSON } from "../lib/api";
import { applyTheme, currentTheme, THEME_KEY, themeOptions } from "../lib/theme";

const navItems = [
  { to: "/overview", label: "Overview", icon: "grid" },
  { to: "/sessions", label: "Sessions", icon: "layers" },
  { to: "/traces", label: "Traces", icon: "activity" },
  { to: "/audit", label: "Audit", icon: "shield" },
  { to: "/routing", label: "Upstreams", icon: "route" },
  { to: "/analysis", label: "Analysis", icon: "spark" },
  { to: "/tokens", label: "Tokens", icon: "key" },
];

export function PrimaryNav({ user, onLogout, collapsed = false, onToggleCollapsed }) {
  const [accountOpen, setAccountOpen] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const menuRef = useRef(null);

  useEffect(() => {
    const close = (event) => {
      if (menuRef.current && !menuRef.current.contains(event.target)) {
        setAccountOpen(false);
      }
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, []);

  return (
    <nav className="primary-nav" aria-label="Primary navigation">
      <div className="nav-top">
        <div className="nav-brand">
          <div className="nav-brand-copy">
            <strong>TraceLab</strong>
          </div>
          <button className="sidebar-toggle" type="button" onClick={onToggleCollapsed} aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"} title={collapsed ? "Expand sidebar" : "Collapse sidebar"}>
            <NavIcon name="sidebar" />
          </button>
        </div>
        <div className="nav-section">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              title={collapsed ? item.label : undefined}
              className={({ isActive }) => (isActive || isLegacyActive(item.to) ? "nav-chip nav-chip-active" : "nav-chip")}
            >
              <NavIcon name={item.icon} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </div>
      </div>
      <div className="nav-account" ref={menuRef}>
        {accountOpen ? (
          <div className="account-menu">
            <AccountMenuContent
              user={user}
              onLogout={onLogout}
              onToken={() => {
                setTokenOpen(true);
                setAccountOpen(false);
              }}
              onPassword={() => {
                setPasswordOpen(true);
                setAccountOpen(false);
              }}
            />
          </div>
        ) : null}
        <button className="account-trigger" type="button" onClick={() => setAccountOpen((open) => !open)} aria-haspopup="menu" aria-expanded={accountOpen} title={collapsed ? displayName(user) : undefined}>
          <span className="account-avatar">{initials(user)}</span>
          <span className="account-copy">
            <strong>{displayName(user)}</strong>
            <small>{user?.role || "monitor"}</small>
          </span>
        </button>
      </div>
      {tokenOpen ? <TokenDialog onClose={() => setTokenOpen(false)} /> : null}
      {passwordOpen ? <PasswordDialog onClose={() => setPasswordOpen(false)} /> : null}
    </nav>
  );
}

function AccountMenuContent({ user, onLogout, onToken, onPassword }) {
  return (
    <>
      <div className="account-menu-head">
        <span className="account-avatar account-avatar-menu">{initials(user)}</span>
        <div>
          <strong>{displayName(user)}</strong>
          <span>{user?.role || "monitor"} · {user?.scope || "all"}</span>
        </div>
      </div>
      <div className="account-menu-section">
        <div className="account-menu-label">Theme</div>
        <ThemeSwitcher />
      </div>
      <button className="account-menu-item" type="button" onClick={onToken}>
        <NavIcon name="key" />
        <span>Get token</span>
      </button>
      <button className="account-menu-item" type="button" onClick={onPassword}>
        <NavIcon name="lock" />
        <span>Change password</span>
      </button>
      <div className="account-menu-divider" />
      <button className="account-menu-item account-menu-danger" type="button" onClick={onLogout}>
        <NavIcon name="logout" />
        <span>Sign out</span>
      </button>
    </>
  );
}

function NavIcon({ name }) {
  const common = { width: 18, height: 18, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": "true" };
  switch (name) {
    case "sidebar":
      return <svg {...common}><rect x="3" y="4" width="18" height="16" rx="3" /><path d="M9 4v16" /><path d="M14 9l3 3-3 3" /></svg>;
    case "grid":
      return <svg {...common}><rect x="4" y="4" width="6" height="6" rx="1.5" /><rect x="14" y="4" width="6" height="6" rx="1.5" /><rect x="4" y="14" width="6" height="6" rx="1.5" /><rect x="14" y="14" width="6" height="6" rx="1.5" /></svg>;
    case "layers":
      return <svg {...common}><path d="m12 3 8 4-8 4-8-4 8-4Z" /><path d="m4 12 8 4 8-4" /><path d="m4 17 8 4 8-4" /></svg>;
    case "activity":
      return <svg {...common}><path d="M4 12h4l2-6 4 12 2-6h4" /></svg>;
    case "shield":
      return <svg {...common}><path d="M12 3 5 6v5c0 4.2 2.8 8 7 10 4.2-2 7-5.8 7-10V6l-7-3Z" /><path d="m9.5 12 1.7 1.7 3.8-4" /></svg>;
    case "route":
      return <svg {...common}><circle cx="6" cy="6" r="2" /><circle cx="18" cy="18" r="2" /><path d="M8 6h5a3 3 0 0 1 0 6h-2a3 3 0 0 0 0 6h5" /></svg>;
    case "spark":
      return <svg {...common}><path d="m12 3 1.7 5.2L19 10l-5.3 1.8L12 17l-1.7-5.2L5 10l5.3-1.8L12 3Z" /><path d="M19 15v4" /><path d="M21 17h-4" /></svg>;
    case "key":
      return <svg {...common}><circle cx="8" cy="15" r="4" /><path d="m11 12 8-8" /><path d="m15 8 3 3" /><path d="m17 6 2 2" /></svg>;
    case "lock":
      return <svg {...common}><rect x="5" y="10" width="14" height="10" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3" /></svg>;
    case "logout":
      return <svg {...common}><path d="M10 17l5-5-5-5" /><path d="M15 12H3" /><path d="M14 4h4a3 3 0 0 1 3 3v10a3 3 0 0 1-3 3h-4" /></svg>;
    case "back":
      return <svg {...common}><path d="m15 18-6-6 6-6" /></svg>;
    default:
      return null;
  }
}

function ThemeSwitcher() {
  const [theme, setTheme] = useState(() => currentTheme());

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  return (
    <div className="theme-switcher" role="group" aria-label="Theme">
      {themeOptions.map((option) => (
        <button
          key={option.value}
          className={theme === option.value ? "theme-option theme-option-active" : "theme-option"}
          type="button"
          title={option.label}
          aria-label={option.label}
          onClick={() => {
            window.localStorage.setItem(THEME_KEY, option.value);
            setTheme(option.value);
          }}
        >
          <span className={`theme-dot theme-dot-${option.value}`} aria-hidden="true" />
          <span className="theme-option-text">{option.short}</span>
        </button>
      ))}
    </div>
  );
}

function TokenDialog({ onClose }) {
  const [name, setName] = useState("monitor-ui");
  const [ttl, setTTL] = useState("24h");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");

  const submit = async (event) => {
    event.preventDefault();
    setError("");
    setToken("");
    try {
      const payload = await postJSON(apiPaths.authTokens, { name, ttl });
      setToken(payload.token || "");
    } catch (err) {
      setError(err.message || "Unable to create token.");
    }
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal" onSubmit={submit}>
      <div className="nav-modal-head">
        <div>
          <p className="eyebrow">Access token</p>
          <h2>Get token</h2>
        </div>
        <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
      </div>
      <label className="nav-field">Name<input value={name} onChange={(event) => setName(event.target.value)} /></label>
      <label className="nav-field">TTL<input value={ttl} onChange={(event) => setTTL(event.target.value)} placeholder="24h, 168h, or empty" /></label>
      {error ? <p className="auth-error">{error}</p> : null}
      {token ? <div className="token-result"><span>Token</span><code>{token}</code></div> : null}
      <div className="nav-modal-actions">
        <button className="ghost-button" type="button" onClick={onClose}>Cancel</button>
        <button className="ghost-button active" type="submit">Create</button>
      </div>
      </form>
    </div>,
    document.body,
  );
}

function PasswordDialog({ onClose }) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");

  const submit = async (event) => {
    event.preventDefault();
    setError("");
    setStatus("");
    try {
      await postJSON(apiPaths.authPassword, { current_password: currentPassword, new_password: newPassword });
      setCurrentPassword("");
      setNewPassword("");
      setStatus("Password updated.");
    } catch (err) {
      setError(err.message || "Unable to change password.");
    }
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal" onSubmit={submit}>
      <div className="nav-modal-head">
        <div>
          <p className="eyebrow">Security</p>
          <h2>Change password</h2>
        </div>
        <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
      </div>
      <label className="nav-field">Current password<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /></label>
      <label className="nav-field">New password<input type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></label>
      {error ? <p className="auth-error">{error}</p> : null}
      {status ? <p className="auth-success">{status}</p> : null}
      <div className="nav-modal-actions">
        <button className="ghost-button" type="button" onClick={onClose}>Cancel</button>
        <button className="ghost-button active" type="submit">Update</button>
      </div>
      </form>
    </div>,
    document.body,
  );
}

function displayName(user) {
  return user?.username || "Monitor user";
}

function initials(user) {
  return displayName(user).slice(0, 2).toUpperCase();
}

function isLegacyActive(path) {
  return path === "/traces" && window.location.pathname === "/requests";
}
