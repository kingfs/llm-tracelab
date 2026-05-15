import React, { useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { apiPaths, MONITOR_TOKEN_KEY, postJSON, requestJSON } from "./lib/api";
import { AnalysisPage } from "./routes/AnalysisPage";
import { AuditPage } from "./routes/AuditPage";
import { ChannelDetailPage } from "./routes/ChannelDetailPage";
import { ChannelsPage } from "./routes/ChannelsPage";
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
            setAuth({ loading: false, required: true, authorized: false, error: "Invalid or missing monitor token.", user: null });
          }
        }
      } catch (error) {
        if (!cancelled) {
          setAuth({ loading: false, required: true, authorized: false, error: error.message || "Unable to verify monitor token.", user: null });
        }
      }
    }

    resolveAuth();
    return () => {
      cancelled = true;
    };
  }, []);

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
        setAuth({ loading: false, required: true, authorized: false, error: "Invalid username or password.", user: null });
      }
      return;
    }
    setAuth({ loading: false, required: true, authorized: false, error: "Enter username and password.", user: null });
  };

  const logout = () => {
    window.localStorage.removeItem(MONITOR_TOKEN_KEY);
    setAuth({ loading: false, required: true, authorized: false, error: "Signed out.", user: null });
  };

  if (auth.loading) {
    return <div className="auth-screen"><div className="auth-panel"><p className="eyebrow">Access control</p><h1>Checking monitor access</h1></div></div>;
  }

  if (auth.required && !auth.authorized) {
    return (
      <div className="auth-screen">
        <form className="auth-panel" onSubmit={submitToken}>
          <p className="eyebrow">Access control</p>
          <h1>Sign in to monitor</h1>
          <label htmlFor="monitor-username">Username</label>
          <input id="monitor-username" type="text" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} autoFocus />
          <label htmlFor="monitor-password">Password</label>
          <input id="monitor-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
          {auth.error ? <p className="auth-error">{auth.error}</p> : null}
          <button className="ghost-button" type="submit">Sign in</button>
        </form>
      </div>
    );
  }

  return (
    <AppShell user={auth.user} onLogout={logout}>
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
        <Route path="/channels" element={<ChannelsPage />} />
        <Route path="/channels/:channelID" element={<ChannelDetailPage />} />
        <Route path="/routing" element={<RoutingPage />} />
        <Route path="/analysis" element={<AnalysisPage />} />
        <Route path="/tokens" element={<TokensPage />} />
        <Route path="/sessions/:sessionID" element={<SessionDetailPage />} />
        <Route path="/upstreams/:upstreamID" element={<UpstreamDetailPage />} />
        <Route path="/traces/:traceID" element={<TraceDetailPage />} />
      </Routes>
    </AppShell>
  );
}

export default App;
