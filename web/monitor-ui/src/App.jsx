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
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

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
    if (username.trim() && password) {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
      });
      if (!response.ok) {
        setAuth({ loading: false, required: true, authorized: false, error: "Invalid username or password." });
        return;
      }
      const payload = await response.json();
      window.localStorage.setItem(MONITOR_AUTH_TOKEN_KEY, payload.token);
      setToken(payload.token || "");
      setAuth({ loading: false, required: true, authorized: true, error: "" });
      return;
    }

    const nextToken = token.trim();
    if (!nextToken) {
      setAuth({ loading: false, required: true, authorized: false, error: "Enter username and password, or paste an existing token." });
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
          <h1>Sign in to monitor</h1>
          <label htmlFor="monitor-username">Username</label>
          <input id="monitor-username" type="text" autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} autoFocus />
          <label htmlFor="monitor-password">Password</label>
          <input id="monitor-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
          <label htmlFor="monitor-token">Bearer token</label>
          <input id="monitor-token" type="password" autoComplete="off" value={token} onChange={(event) => setToken(event.target.value)} />
          {auth.error ? <p className="auth-error">{auth.error}</p> : null}
          <button className="ghost-button" type="submit">Sign in</button>
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
