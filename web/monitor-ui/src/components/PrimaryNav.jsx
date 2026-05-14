import React, { useEffect, useRef, useState } from "react";
import { NavLink } from "react-router-dom";
import { apiPaths, postJSON } from "../lib/api";

const navItems = [
  { to: "/overview", label: "Overview", hint: "Status" },
  { to: "/sessions", label: "Sessions", hint: "Workflows" },
  { to: "/traces", label: "Traces", hint: "HTTP" },
  { to: "/audit", label: "Audit", hint: "Findings" },
  { to: "/routing", label: "Upstreams", hint: "Routing" },
  { to: "/analysis", label: "Analysis", hint: "Runs" },
  { to: "/tokens", label: "Tokens", hint: "Access" },
];

const THEME_KEY = "llm-tracelab.monitor.theme";
const themeOptions = [
  { value: "system", label: "System" },
  { value: "dark", label: "Dark" },
  { value: "light", label: "Light" },
];

export function PrimaryNav({ user, onLogout }) {
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
      </div>
      <div className="nav-account" ref={menuRef}>
        {accountOpen ? (
          <div className="account-menu">
            <div className="account-menu-head">
              <strong>{displayName(user)}</strong>
              <span>{user?.role || "monitor"} · {user?.scope || "all"}</span>
            </div>
            <ThemeSwitcher />
            <button className="account-menu-item" type="button" onClick={() => { setTokenOpen(true); setAccountOpen(false); }}>
              <span>Get token</span>
              <small>Create API access</small>
            </button>
            <button className="account-menu-item" type="button" onClick={() => { setPasswordOpen(true); setAccountOpen(false); }}>
              <span>Change password</span>
              <small>Update monitor login</small>
            </button>
            <button className="account-menu-item account-menu-danger" type="button" onClick={onLogout}>
              <span>Sign out</span>
              <small>Remove this browser session</small>
            </button>
          </div>
        ) : null}
        <button className="account-trigger" type="button" onClick={() => setAccountOpen((open) => !open)} aria-haspopup="menu" aria-expanded={accountOpen}>
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
          onClick={() => {
            window.localStorage.setItem(THEME_KEY, option.value);
            setTheme(option.value);
          }}
        >
          {option.label}
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

  return (
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal" onSubmit={submit}>
        <div className="nav-modal-head">
          <div>
            <p className="eyebrow">Access token</p>
            <h2>Get API token</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
        </div>
        <label className="nav-field">Name<input value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label className="nav-field">TTL<input value={ttl} onChange={(event) => setTTL(event.target.value)} placeholder="24h, 168h, or empty" /></label>
        {error ? <p className="auth-error">{error}</p> : null}
        {token ? <div className="token-result"><span>Token</span><code>{token}</code></div> : null}
        <div className="nav-modal-actions">
          <button className="ghost-button" type="button" onClick={onClose}>Close</button>
          <button className="ghost-button active" type="submit">Create token</button>
        </div>
      </form>
    </div>
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

  return (
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
          <button className="ghost-button" type="button" onClick={onClose}>Close</button>
          <button className="ghost-button active" type="submit">Update password</button>
        </div>
      </form>
    </div>
  );
}

function displayName(user) {
  return user?.username || "Monitor user";
}

function initials(user) {
  return displayName(user).slice(0, 2).toUpperCase();
}

export function applyTheme(theme = currentTheme()) {
  const normalized = themeOptions.some((option) => option.value === theme) ? theme : "system";
  document.documentElement.dataset.theme = normalized;
}

function currentTheme() {
  return window.localStorage.getItem(THEME_KEY) || "system";
}

applyTheme();

function isLegacyActive(path) {
  return path === "/traces" && window.location.pathname === "/requests";
}
