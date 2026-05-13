import React, { useState } from "react";
import { PrimaryNav } from "../components/PrimaryNav";
import { apiPaths, postJSON } from "../lib/api";

export function TokensPage() {
  const [name, setName] = useState("local-dev");
  const [ttl, setTTL] = useState("");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const createToken = async (event) => {
    event.preventDefault();
    setLoading(true);
    setError("");
    setToken("");
    try {
      const payload = await postJSON(apiPaths.authTokens, { name, ttl, scope: "api" });
      setToken(payload.token || "");
    } catch (err) {
      setError(err.message || "Unable to create token.");
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Access control</p>
          <h1>API tokens</h1>
        </div>
      </header>
      <PrimaryNav />

      <section className="panel token-panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Current user</p>
            <h2>Create token</h2>
          </div>
        </div>
        <form className="token-form" onSubmit={createToken}>
          <label className="token-field" htmlFor="token-name">
            <span>Name</span>
            <input id="token-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label className="token-field" htmlFor="token-ttl">
            <span>TTL</span>
            <input id="token-ttl" type="text" placeholder="24h, 720h, or empty" value={ttl} onChange={(event) => setTTL(event.target.value)} />
          </label>
          <button className="ghost-button" type="submit" disabled={loading}>{loading ? "Creating" : "Create token"}</button>
        </form>
        {error ? <p className="auth-error">{error}</p> : null}
        {token ? (
          <div className="token-result">
            <span>Bearer token</span>
            <code>{token}</code>
          </div>
        ) : null}
      </section>
    </main>
  );
}
