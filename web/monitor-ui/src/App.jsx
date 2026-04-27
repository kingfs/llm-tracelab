import React, { useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { MONITOR_AUTH_TOKEN_KEY, monitorAuthHeaders } from "./hooks/useJSON";
import { RequestsPage } from "./routes/RequestsPage";
import { RoutingPage } from "./routes/RoutingPage";
import { SessionDetailPage } from "./routes/SessionDetailPage";
import { SessionsPage } from "./routes/SessionsPage";
import { TraceDetailPage } from "./routes/TraceDetailPage";
import { UpstreamDetailPage } from "./routes/UpstreamDetailPage";

function App() {
  const [auth, setAuth] = useState({ loading: true, required: false, authorized: false, error: "" });
  const [token, setToken] = useState(() => window.localStorage.getItem(MONITOR_AUTH_TOKEN_KEY) || "");

  useEffect(() => {
    let cancelled = false;

    async function resolveAuth() {
      try {
        const statusResponse = await fetch("/api/auth/status");
        const status = await statusResponse.json();
        if (!status.auth_required) {
          if (!cancelled) {
            setAuth({ loading: false, required: false, authorized: true, error: "" });
          }
          return;
        }
        const checkResponse = await fetch("/api/auth/check", { headers: monitorAuthHeaders() });
        if (!cancelled) {
          setAuth({ loading: false, required: true, authorized: checkResponse.ok, error: checkResponse.ok ? "" : "Invalid or missing monitor token." });
        }
      } catch (error) {
        if (!cancelled) {
          setAuth({ loading: false, required: true, authorized: false, error: error.message || "Unable to verify monitor token." });
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
    const nextToken = token.trim();
    if (!nextToken) {
      setAuth({ loading: false, required: true, authorized: false, error: "Enter the monitor token." });
      return;
    }
    window.localStorage.setItem(MONITOR_AUTH_TOKEN_KEY, nextToken);
    const response = await fetch("/api/auth/check", { headers: monitorAuthHeaders() });
    setAuth({ loading: false, required: true, authorized: response.ok, error: response.ok ? "" : "Invalid monitor token." });
  };

  if (auth.loading) {
    return <div className="auth-screen"><div className="auth-panel"><p className="eyebrow">Access control</p><h1>Checking monitor access</h1></div></div>;
  }

  if (auth.required && !auth.authorized) {
    return (
      <div className="auth-screen">
        <form className="auth-panel" onSubmit={submitToken}>
          <p className="eyebrow">Access control</p>
          <h1>Monitor token required</h1>
          <label htmlFor="monitor-token">Bearer token</label>
          <input id="monitor-token" type="password" autoComplete="current-password" value={token} onChange={(event) => setToken(event.target.value)} autoFocus />
          {auth.error ? <p className="auth-error">{auth.error}</p> : null}
          <button className="ghost-button" type="submit">Unlock monitor</button>
        </form>
      </div>
    );
  }

  return (
    <Routes>
      <Route path="/" element={<Navigate to="/requests" replace />} />
      <Route path="/requests" element={<RequestsPage />} />
      <Route path="/sessions" element={<SessionsPage />} />
      <Route path="/routing" element={<RoutingPage />} />
      <Route path="/sessions/:sessionID" element={<SessionDetailPage />} />
      <Route path="/upstreams/:upstreamID" element={<UpstreamDetailPage />} />
      <Route path="/traces/:traceID" element={<TraceDetailPage />} />
    </Routes>
  );
}

export default App;
