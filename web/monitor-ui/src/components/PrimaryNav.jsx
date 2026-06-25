import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { NavLink } from "react-router-dom";
import { apiPaths, apiURL, MONITOR_TOKEN_KEY, postJSON, requestJSON } from "../lib/api";
import { languageOptions, useI18n } from "../lib/i18n";
import { applyTheme, currentTheme, THEME_KEY, themeOptions } from "../lib/theme";

const navItems = [
  { to: "/overview", labelKey: "nav.overview", icon: "grid" },
  { to: "/events", labelKey: "nav.events", icon: "bell", badge: "events" },
  { to: "/sessions", labelKey: "nav.sessions", icon: "layers" },
  { to: "/traces", labelKey: "nav.traces", icon: "activity" },
  { to: "/audit", labelKey: "nav.audit", icon: "shield" },
  { to: "/models", labelKey: "nav.models", icon: "box" },
  { to: "/providers", labelKey: "nav.providers", icon: "plug" },
  { to: "/connect", labelKey: "nav.connect", icon: "terminal" },
  { to: "/routing", labelKey: "nav.routing", icon: "route" },
  { to: "/analysis", labelKey: "nav.analysis", icon: "spark" },
  { to: "/tokens", labelKey: "nav.tokens", icon: "key" },
];

export function PrimaryNav({ user, onLogout, collapsed = false, onToggleCollapsed }) {
  const { t } = useI18n();
  const [accountOpen, setAccountOpen] = useState(false);
  const [preferencesOpen, setPreferencesOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [eventSummary, setEventSummary] = useState(null);
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

  useEffect(() => {
    let cancelled = false;
    let timer = 0;
    let source = null;
    const refresh = async () => {
      try {
        const payload = await requestJSON(apiURL(apiPaths.eventsSummary, { window: "all" }));
        if (!cancelled) {
          setEventSummary(payload);
        }
      } catch {
        if (!cancelled) {
          setEventSummary(null);
        }
      }
    };
    refresh();
    const onRefresh = () => refresh();
    window.addEventListener("llm-tracelab:events-refresh", onRefresh);
    if (typeof window.EventSource !== "undefined") {
      const token = window.localStorage.getItem(MONITOR_TOKEN_KEY) || "";
      const streamURL = token ? `${apiPaths.eventsStream}?access_token=${encodeURIComponent(token)}` : apiPaths.eventsStream;
      source = new window.EventSource(streamURL);
      const handleStream = (event) => {
        try {
          const payload = JSON.parse(event.data || "{}");
          setEventSummary((current) => ({
            ...(current || {}),
            unread: Number(payload.unread || 0),
          }));
        } catch {
          refresh();
        }
      };
      source.addEventListener("system_event.summary", handleStream);
      source.addEventListener("system_event.updated", handleStream);
      source.onerror = () => refresh();
    }
    timer = window.setInterval(refresh, 60_000);
    return () => {
      cancelled = true;
      window.removeEventListener("llm-tracelab:events-refresh", onRefresh);
      if (source) {
        source.close();
      }
      window.clearInterval(timer);
    };
  }, []);

  return (
    <nav className="primary-nav" aria-label={t("nav.primary")}>
      <div className="nav-top">
        <div className="nav-brand">
          <div className="nav-brand-copy">
            <strong>TraceLab</strong>
          </div>
          <button className="sidebar-toggle" type="button" onClick={onToggleCollapsed} aria-label={collapsed ? t("nav.expandSidebar") : t("nav.collapseSidebar")} title={collapsed ? t("nav.expandSidebar") : t("nav.collapseSidebar")}>
            <NavIcon name="sidebar" />
          </button>
        </div>
        <div className="nav-section">
          {navItems.map((item) => {
            const label = t(item.labelKey);
            return (
              <NavLink
                key={item.to}
                to={item.to}
                title={collapsed ? label : undefined}
                className={({ isActive }) => (isActive || isLegacyActive(item.to) ? "nav-chip nav-chip-active" : "nav-chip")}
              >
                <NavIcon name={item.icon} />
                <span>{label}</span>
                {item.badge === "events" && Number(eventSummary?.unread || 0) > 0 ? <span className="nav-badge">{formatBadgeCount(eventSummary.unread)}</span> : null}
              </NavLink>
            );
          })}
        </div>
      </div>
      <div className="nav-account" ref={menuRef}>
        {accountOpen ? (
          <div className="account-menu">
            <AccountMenuContent
              user={user}
              onLogout={onLogout}
              onPreferences={() => {
                setPreferencesOpen(true);
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
            <small>{user?.role || t("account.roleFallback")}</small>
          </span>
        </button>
      </div>
      {preferencesOpen ? <PreferencesDialog onClose={() => setPreferencesOpen(false)} /> : null}
      {passwordOpen ? <PasswordDialog onClose={() => setPasswordOpen(false)} /> : null}
    </nav>
  );
}

function AccountMenuContent({ user, onLogout, onPreferences, onPassword }) {
  const { t } = useI18n();
  return (
    <>
      <div className="account-menu-head">
        <span className="account-avatar account-avatar-menu">{initials(user)}</span>
        <div>
          <strong>{displayName(user)}</strong>
          <span>{user?.role || t("account.roleFallback")} · {user?.scope || t("account.scopeFallback")}</span>
        </div>
      </div>
      <button className="account-menu-item" type="button" onClick={onPreferences}>
        <NavIcon name="settings" />
        <span>{t("account.preferences")}</span>
      </button>
      <button className="account-menu-item" type="button" onClick={onPassword}>
        <NavIcon name="lock" />
        <span>{t("account.changePassword")}</span>
      </button>
      <div className="account-menu-divider" />
      <button className="account-menu-item account-menu-danger" type="button" onClick={onLogout}>
        <NavIcon name="logout" />
        <span>{t("account.signOut")}</span>
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
    case "bell":
      return <svg {...common}><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9" /><path d="M10 21h4" /></svg>;
    case "layers":
      return <svg {...common}><path d="m12 3 8 4-8 4-8-4 8-4Z" /><path d="m4 12 8 4 8-4" /><path d="m4 17 8 4 8-4" /></svg>;
    case "activity":
      return <svg {...common}><path d="M4 12h4l2-6 4 12 2-6h4" /></svg>;
    case "shield":
      return <svg {...common}><path d="M12 3 5 6v5c0 4.2 2.8 8 7 10 4.2-2 7-5.8 7-10V6l-7-3Z" /><path d="m9.5 12 1.7 1.7 3.8-4" /></svg>;
    case "route":
      return <svg {...common}><circle cx="6" cy="6" r="2" /><circle cx="18" cy="18" r="2" /><path d="M8 6h5a3 3 0 0 1 0 6h-2a3 3 0 0 0 0 6h5" /></svg>;
    case "box":
      return <svg {...common}><path d="m12 3 8 4.4v9.2L12 21l-8-4.4V7.4L12 3Z" /><path d="M4.5 7.7 12 12l7.5-4.3" /><path d="M12 12v8.5" /></svg>;
    case "plug":
      return <svg {...common}><path d="M9 7V3" /><path d="M15 7V3" /><path d="M7 7h10v4a5 5 0 0 1-10 0V7Z" /><path d="M12 16v5" /><path d="M8 21h8" /></svg>;
    case "terminal":
      return <svg {...common}><path d="m5 7 5 5-5 5" /><path d="M12 17h7" /></svg>;
    case "spark":
      return <svg {...common}><path d="m12 3 1.7 5.2L19 10l-5.3 1.8L12 17l-1.7-5.2L5 10l5.3-1.8L12 3Z" /><path d="M19 15v4" /><path d="M21 17h-4" /></svg>;
    case "key":
      return <svg {...common}><circle cx="8" cy="15" r="4" /><path d="m11 12 8-8" /><path d="m15 8 3 3" /><path d="m17 6 2 2" /></svg>;
    case "lock":
      return <svg {...common}><rect x="5" y="10" width="14" height="10" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3" /></svg>;
    case "logout":
      return <svg {...common}><path d="M10 17l5-5-5-5" /><path d="M15 12H3" /><path d="M14 4h4a3 3 0 0 1 3 3v10a3 3 0 0 1-3 3h-4" /></svg>;
    case "settings":
      return <svg {...common}><path d="M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Z" /><path d="M19.4 15a1.8 1.8 0 0 0 .36 1.98l.06.06a2.1 2.1 0 0 1-2.97 2.97l-.06-.06a1.8 1.8 0 0 0-1.98-.36 1.8 1.8 0 0 0-1.1 1.66V21.4a2.1 2.1 0 0 1-4.2 0v-.09a1.8 1.8 0 0 0-1.1-1.66 1.8 1.8 0 0 0-1.98.36l-.06.06a2.1 2.1 0 0 1-2.97-2.97l.06-.06A1.8 1.8 0 0 0 4.6 15a1.8 1.8 0 0 0-1.66-1.1H2.8a2.1 2.1 0 0 1 0-4.2h.09A1.8 1.8 0 0 0 4.55 8.6a1.8 1.8 0 0 0-.36-1.98l-.06-.06a2.1 2.1 0 0 1 2.97-2.97l.06.06a1.8 1.8 0 0 0 1.98.36 1.8 1.8 0 0 0 1.1-1.66V2.6a2.1 2.1 0 0 1 4.2 0v.09a1.8 1.8 0 0 0 1.1 1.66 1.8 1.8 0 0 0 1.98-.36l.06-.06a2.1 2.1 0 0 1 2.97 2.97l-.06.06a1.8 1.8 0 0 0-.36 1.98 1.8 1.8 0 0 0 1.66 1.1h.09a2.1 2.1 0 0 1 0 4.2h-.09A1.8 1.8 0 0 0 19.4 15Z" /></svg>;
    case "back":
      return <svg {...common}><path d="m15 18-6-6 6-6" /></svg>;
    default:
      return null;
  }
}

function ThemeSwitcher({ labelled = false }) {
  const { t } = useI18n();
  const [theme, setTheme] = useState(() => currentTheme());

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  return (
    <div className={labelled ? "theme-switcher theme-switcher-labelled" : "theme-switcher"} role="group" aria-label={t("preferences.theme")}>
      {themeOptions.map((option) => (
        <button
          key={option.value}
          className={theme === option.value ? "theme-option theme-option-active" : "theme-option"}
          type="button"
          title={t(`theme.${option.value}`)}
          aria-label={t(`theme.${option.value}`)}
          onClick={() => {
            window.localStorage.setItem(THEME_KEY, option.value);
            setTheme(option.value);
          }}
        >
          <span className={`theme-dot theme-dot-${option.value}`} aria-hidden="true" />
          <span className="theme-option-text">{labelled ? t(`theme.${option.value}`) : option.short}</span>
        </button>
      ))}
    </div>
  );
}

function LanguageSwitcher() {
  const { language, setLanguage, t } = useI18n();
  return (
    <div className="language-switcher" role="group" aria-label={t("preferences.language")}>
      {languageOptions.map((option) => (
        <button key={option.value} className={language === option.value ? "language-option language-option-active" : "language-option"} type="button" onClick={() => setLanguage(option.value)}>
          <span>{option.label}</span>
        </button>
      ))}
    </div>
  );
}

function PreferencesDialog({ onClose }) {
  const { t } = useI18n();
  return createPortal(
    <div className="nav-modal-backdrop" role="presentation" onClick={onClose}>
      <div className="nav-modal preferences-modal" role="dialog" aria-modal="true" aria-labelledby="preferences-title" onClick={(event) => event.stopPropagation()}>
        <div className="nav-modal-head">
          <div>
            <p className="eyebrow">{t("preferences.eyebrow")}</p>
            <h2 id="preferences-title">{t("preferences.title")}</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label={t("common.close")}>x</button>
        </div>
        <div className="preferences-list">
          <section className="preferences-row">
            <div>
              <strong>{t("preferences.language")}</strong>
              <span>{t("preferences.saved")}</span>
            </div>
            <LanguageSwitcher />
          </section>
          <section className="preferences-row">
            <div>
              <strong>{t("preferences.theme")}</strong>
              <span>{t("preferences.saved")}</span>
            </div>
            <ThemeSwitcher labelled />
          </section>
        </div>
        <div className="nav-modal-actions">
          <button className="ghost-button active" type="button" onClick={onClose}>{t("preferences.close")}</button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

function PasswordDialog({ onClose }) {
  const { t } = useI18n();
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
      setStatus(t("password.updated"));
    } catch (err) {
      setError(err.message || t("password.failed"));
    }
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal" onSubmit={submit}>
      <div className="nav-modal-head">
        <div>
          <p className="eyebrow">{t("password.eyebrow")}</p>
          <h2>{t("password.title")}</h2>
        </div>
        <button className="icon-button" type="button" onClick={onClose} aria-label={t("common.close")}>x</button>
      </div>
      <label className="nav-field">{t("password.current")}<input type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /></label>
      <label className="nav-field">{t("password.next")}<input type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></label>
      {error ? <p className="auth-error">{error}</p> : null}
      {status ? <p className="auth-success">{status}</p> : null}
      <div className="nav-modal-actions">
        <button className="ghost-button" type="button" onClick={onClose}>{t("password.cancel")}</button>
        <button className="ghost-button active" type="submit">{t("password.update")}</button>
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

function formatBadgeCount(value) {
  const count = Number(value || 0);
  return count > 99 ? "99+" : String(count);
}

function isLegacyActive(path) {
  return path === "/traces" && window.location.pathname === "/requests";
}
