import React, { useMemo, useState } from "react";
import { InlineTag } from "../components/common/Badges";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, postJSON, requestJSON } from "../lib/api";
import { formatDateTime } from "../lib/monitor";

export function TokensPage() {
  const [name, setName] = useState("local-dev");
  const [ttl, setTTL] = useState("");
  const [scope, setScope] = useState("api");
  const [created, setCreated] = useState(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busyToken, setBusyToken] = useState(0);
  const [refreshTick, setRefreshTick] = useState(0);
  const tokens = useJSON(apiPaths.authTokens, [refreshTick]);
  const items = tokens.data?.items || [];
  const summary = useMemo(() => summarizeTokens(items), [items]);

  const createToken = async (event) => {
    event.preventDefault();
    setLoading(true);
    setError("");
    setCreated(null);
    try {
      const payload = await postJSON(apiPaths.authTokens, { name, ttl, scope });
      setCreated(payload);
      setRefreshTick((tick) => tick + 1);
    } catch (err) {
      setError(err.message || "Unable to create token.");
    } finally {
      setLoading(false);
    }
  };

  const revokeToken = async (tokenID) => {
    setBusyToken(tokenID);
    setError("");
    try {
      await requestJSON(`${apiPaths.authTokens}/${encodeURIComponent(tokenID)}`, { method: "DELETE" });
      setRefreshTick((tick) => tick + 1);
    } catch (err) {
      setError(err.message || "Unable to revoke token.");
    } finally {
      setBusyToken(0);
    }
  };

  return (
    <main className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Access control</p>
          <h1>API tokens</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge">{summary.active} active</span>
          <span className="badge">{summary.total} total</span>
        </div>
      </header>

      <section className="hero-grid hero-grid-compact token-summary-grid">
        <StatCard label="Total" value={summary.total} />
        <StatCard label="Active" value={summary.active} accent="accent-green" />
        <StatCard label="Expired" value={summary.expired} accent={summary.expired ? "accent-gold" : ""} />
        <StatCard label="Revoked" value={summary.revoked} accent={summary.revoked ? "accent-red" : ""} />
      </section>

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
          <label className="token-field" htmlFor="token-scope">
            <span>Scope</span>
            <input id="token-scope" type="text" value={scope} onChange={(event) => setScope(event.target.value)} />
          </label>
          <button className="ghost-button" type="submit" disabled={loading}>{loading ? "Creating" : "Create token"}</button>
        </form>
        {error ? <p className="auth-error">{error}</p> : null}
        {created?.token ? (
          <div className="token-result">
            <span>Bearer token, shown once</span>
            <code>{created.token}</code>
            <small>Prefix {created.prefix || "-"} is stored for future identification. The raw token cannot be shown again.</small>
          </div>
        ) : null}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Token inventory</p>
            <h2>Your tokens</h2>
          </div>
        </div>
        {tokens.error ? <EmptyState title="Unable to load tokens" detail={tokens.error} tone="danger" /> : null}
        {tokens.loading && !tokens.data ? <EmptyState title="Loading tokens" detail="Reading token metadata for the current user." /> : null}
        {tokens.data ? <TokenTable items={items} busyToken={busyToken} onRevoke={revokeToken} /> : null}
      </section>
    </main>
  );
}

function TokenTable({ items, busyToken, onRevoke }) {
  if (!items.length) {
    return <EmptyState title="No tokens" detail="No API tokens have been created for the current user." />;
  }
  return (
    <div className="token-table">
      <div className="token-table-head">
        <span>Name</span>
        <span>Prefix</span>
        <span>Scope</span>
        <span>Status</span>
        <span>Created</span>
        <span>Expires</span>
        <span>Last used</span>
        <span>Actions</span>
      </div>
      {items.map((item) => (
        <article className="token-row" key={item.id}>
          <strong>{item.name || "api-token"}</strong>
          <code>{item.prefix || "-"}</code>
          <span>{item.scope || "all"}</span>
          <InlineTag tone={statusTone(item.status)}>{item.status || "unknown"}</InlineTag>
          <span>{formatDateTime(item.created_at)}</span>
          <span>{item.expires_at ? formatDateTime(item.expires_at) : "never"}</span>
          <span>{item.last_used_at ? formatDateTime(item.last_used_at) : "never"}</span>
          <div className="action-group">
            <button className="ghost-button" type="button" disabled={item.status !== "active" || busyToken === item.id} onClick={() => onRevoke(item.id)}>
              {busyToken === item.id ? "Revoking" : "Revoke"}
            </button>
          </div>
        </article>
      ))}
    </div>
  );
}

function summarizeTokens(items) {
  return items.reduce(
    (summary, item) => {
      summary.total += 1;
      if (item.status === "active") {
        summary.active += 1;
      } else if (item.status === "expired") {
        summary.expired += 1;
      } else if (item.status === "revoked") {
        summary.revoked += 1;
      }
      return summary;
    },
    { total: 0, active: 0, expired: 0, revoked: 0 },
  );
}

function statusTone(status) {
  switch (status) {
    case "active":
      return "green";
    case "expired":
      return "gold";
    case "revoked":
      return "danger";
    default:
      return "default";
  }
}
