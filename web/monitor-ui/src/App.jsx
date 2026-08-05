import React, { useEffect, useState } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { apiPaths, MONITOR_TOKEN_KEY, postJSON, requestJSON } from "./lib/api";
import { useI18n } from "./lib/i18n";
import { AnalysisPage } from "./routes/AnalysisPage";
import { AuditPage } from "./routes/AuditPage";
import { ChannelDetailPage } from "./routes/ChannelDetailPage";
import { ChannelsPage } from "./routes/ChannelsPage";
import { ConnectPage } from "./routes/ConnectPage";
import { EventsPage } from "./routes/EventsPage";
import { ModelDetailPage } from "./routes/ModelDetailPage";
import { ModelsPage } from "./routes/ModelsPage";
import { OverviewPage } from "./routes/OverviewPage";
import { RequestsPage } from "./routes/RequestsPage";
import { RoutingPage } from "./routes/RoutingPage";
import { SessionDetailPage } from "./routes/SessionDetailPage";
import { SessionsPage } from "./routes/SessionsPage";
import { TokensPage } from "./routes/TokensPage";
import { TraceDetailPage } from "./routes/TraceDetailPage";
import { UpstreamDetailPage } from "./routes/UpstreamDetailPage";

function App() {
  const { t } = useI18n();
  const location = useLocation();
  const [auth, setAuth] = useState({ loading: true, required: false, authorized: false, error: "", user: null });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  useEffect(() => {
    let cancelled = false;

    async function resolveAuth() {
      try {
        const status = await requestJSON(apiPaths.authStatus);
        if (!status.auth_required) {
          if (!cancelled) {
            setAuth({ loading: false, required: false, authorized: true, error: "", user: { username: "local", role: "local", scope: "all" } });
          }
          return;
        }
        try {
          await requestJSON(apiPaths.authCheck);
          const user = await requestJSON(apiPaths.authMe).catch(() => null);
          if (!cancelled) {
            setAuth({ loading: false, required: true, authorized: true, error: "", user });
          }
        } catch {
          if (!cancelled) {
            setAuth({ loading: false, required: true, authorized: false, error: t("auth.invalidToken"), user: null });
          }
        }
      } catch (error) {
        if (!cancelled) {
          setAuth({ loading: false, required: true, authorized: false, error: error.message || t("auth.verifyFailed"), user: null });
        }
      }
    }

    resolveAuth();
    return () => {
      cancelled = true;
    };
  }, [t]);

  const submitToken = async (event) => {
    event.preventDefault();
    if (username.trim() && password) {
      try {
        const payload = await postJSON(apiPaths.authLogin, { username, password });
        window.localStorage.setItem(MONITOR_TOKEN_KEY, payload.token);
        const user = await requestJSON(apiPaths.authMe).catch(() => ({ username: username.trim(), role: "admin", scope: "all" }));
        setAuth({ loading: false, required: true, authorized: true, error: "", user });
        setPassword("");
      } catch {
        setAuth({ loading: false, required: true, authorized: false, error: t("auth.invalidCredentials"), user: null });
      }
      return;
    }
    setAuth({ loading: false, required: true, authorized: false, error: t("auth.enterCredentials"), user: null });
  };

  const logout = () => {
    window.localStorage.removeItem(MONITOR_TOKEN_KEY);
    setAuth({ loading: false, required: true, authorized: false, error: t("auth.signedOut"), user: null });
  };

  if (auth.loading) {
    return <div className="auth-screen"><div className="auth-panel"><p className="eyebrow">{t("auth.eyebrow")}</p><h1>{t("auth.checking")}</h1></div></div>;
  }

  if (auth.required && !auth.authorized) {
    return (
      <div className="auth-screen">
        <form className="auth-panel" onSubmit={submitToken}>
          <p className="eyebrow">{t("auth.eyebrow")}</p>
          <h1>{t("auth.signInTitle")}</h1>
          <label htmlFor="monitor-username">{t("auth.username")}</label>
          <input id="monitor-username" type="text" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} autoFocus />
          <label htmlFor="monitor-password">{t("auth.password")}</label>
          <input id="monitor-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
          {auth.error ? <p className="auth-error">{auth.error}</p> : null}
          <button className="ghost-button" type="submit">{t("auth.signIn")}</button>
        </form>
      </div>
    );
  }

  return (
    <AppShell user={auth.user} onLogout={logout}>
      <MonitorErrorBoundary key={location.pathname}>
        <Routes>
          <Route path="/" element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<OverviewPage />} />
          <Route path="/events" element={<EventsPage />} />
          <Route path="/requests" element={<RequestsPage />} />
          <Route path="/traces" element={<RequestsPage />} />
          <Route path="/sessions" element={<SessionsPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/models" element={<ModelsPage />} />
          <Route path="/models/:model" element={<ModelDetailPage />} />
          <Route path="/providers" element={<ChannelsPage />} />
          <Route path="/providers/:providerID" element={<ChannelDetailPage />} />
          <Route path="/channels" element={<Navigate to="/providers" replace />} />
          <Route path="/channels/:channelID" element={<ChannelDetailPage />} />
          <Route path="/connect" element={<ConnectPage />} />
          <Route path="/routing" element={<RoutingPage />} />
          <Route path="/analysis" element={<AnalysisPage />} />
          <Route path="/tokens" element={<TokensPage />} />
          <Route path="/sessions/:sessionID" element={<SessionDetailPage />} />
          <Route path="/upstreams/:upstreamID" element={<UpstreamDetailPage />} />
          <Route path="/traces/:traceID" element={<TraceDetailPage />} />
        </Routes>
      </MonitorErrorBoundary>
    </AppShell>
  );
}

class MonitorErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidCatch(error) {
    console.error("Monitor page render failed", error);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="shell shell-list">
          <section className="panel">
            <div className="panel-head">
              <div>
                <h2>Unable to render this page</h2>
              </div>
            </div>
            <p className="event-message">{this.state.error.message || "The monitor UI hit a rendering error."}</p>
          </section>
        </div>
      );
    }
    return this.props.children;
  }
}

export default App;
