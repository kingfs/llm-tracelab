import React, { useMemo, useState } from "react";
import { InlineTag, PlusIcon } from "../components/common/Badges";
import { StatCard } from "../components/common/Display";
import { EmptyState } from "../components/common/EmptyState";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, deleteJSON, postJSON, requestJSON } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatDateTime } from "../lib/monitor";

export function TokensPage() {
  const { t } = useI18n();
  const [name, setName] = useState("local-dev");
  const [ttl, setTTL] = useState("");
  const [scope, setScope] = useState("api");
  const [created, setCreated] = useState(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busyToken, setBusyToken] = useState(0);
  const [refreshTick, setRefreshTick] = useState(0);
  const [showAll, setShowAll] = useState(false);
  const tokens = useJSON(apiPaths.authTokens, [refreshTick]);
  const items = tokens.data?.items || [];
  const visibleItems = showAll ? items : items.filter((item) => item.status === "active");
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
      setError(err.message || t("tokens.createError"));
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
      setError(err.message || t("tokens.revokeError"));
    } finally {
      setBusyToken(0);
    }
  };

  const deleteToken = async (tokenID) => {
    setBusyToken(tokenID);
    setError("");
    try {
      await deleteJSON(apiURL(`${apiPaths.authTokens}/${encodeURIComponent(tokenID)}`, { delete: "1" }));
      setRefreshTick((tick) => tick + 1);
    } catch (err) {
      setError(err.message || t("tokens.deleteError"));
    } finally {
      setBusyToken(0);
    }
  };

  return (
    <main className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Access control</p>
          <h1>{t("tokens.title")}</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge">{t("tokens.activeBadge", { count: summary.active })}</span>
          <span className="badge">{t("tokens.totalBadge", { count: summary.total })}</span>
        </div>
      </header>

      <section className="hero-grid hero-grid-compact token-summary-grid">
        <StatCard label={t("common.total")} value={summary.total} />
        <StatCard label={t("common.active")} value={summary.active} accent="accent-green" />
        <StatCard label={t("common.expired")} value={summary.expired} accent={summary.expired ? "accent-gold" : ""} />
        <StatCard label={t("common.revoked")} value={summary.revoked} accent={summary.revoked ? "accent-red" : ""} />
      </section>

      <section className="panel token-panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Current user</p>
            <h2>{t("tokens.create")}</h2>
          </div>
        </div>
        <form className="token-form" onSubmit={createToken}>
          <label className="token-field" htmlFor="token-name">
            <span>{t("tokens.name")}</span>
            <input id="token-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label className="token-field" htmlFor="token-ttl">
            <span>{t("tokens.ttl")}</span>
            <input id="token-ttl" type="text" placeholder={t("tokens.ttlPlaceholder")} value={ttl} onChange={(event) => setTTL(event.target.value)} />
          </label>
          <label className="token-field" htmlFor="token-scope">
            <span>{t("tokens.scope")}</span>
            <input id="token-scope" type="text" value={scope} onChange={(event) => setScope(event.target.value)} />
          </label>
          <button className="icon-button token-create-button" type="submit" disabled={loading} title={loading ? t("tokens.creating") : t("tokens.create")} aria-label={loading ? t("tokens.creating") : t("tokens.create")}>
            <PlusIcon />
          </button>
        </form>
        {error ? <p className="auth-error">{error}</p> : null}
        {created?.token ? (
          <div className="token-result">
            <span>{t("tokens.shownOnce")}</span>
            <code>{created.token}</code>
            <small>{t("tokens.prefixStored", { prefix: created.prefix || "-" })}</small>
          </div>
        ) : null}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">{t("tokens.inventory")}</p>
            <h2>{showAll ? t("tokens.allTokens") : t("tokens.activeTokens")}</h2>
          </div>
          <button className={showAll ? "ghost-button active" : "ghost-button"} type="button" onClick={() => setShowAll((value) => !value)}>
            {showAll ? t("tokens.showActive") : t("tokens.showAll")}
          </button>
        </div>
        {tokens.error ? <EmptyState title={t("tokens.loadError")} detail={tokens.error} tone="danger" /> : null}
        {tokens.loading && !tokens.data ? <EmptyState title={t("tokens.loading")} detail={t("tokens.loadingDetail")} /> : null}
        {tokens.data ? <TokenTable items={visibleItems} busyToken={busyToken} onRevoke={revokeToken} onDelete={deleteToken} /> : null}
      </section>
    </main>
  );
}

function TokenTable({ items, busyToken, onRevoke, onDelete }) {
  const { t } = useI18n();
  if (!items.length) {
    return <EmptyState title={t("tokens.noTokens")} detail={t("tokens.noTokensDetail")} />;
  }
  return (
    <div className="token-table">
      <div className="token-table-head">
        <span>{t("tokens.name")}</span>
        <span>{t("tokens.prefix")}</span>
        <span>{t("tokens.scope")}</span>
        <span>{t("common.status")}</span>
        <span>{t("common.created")}</span>
        <span>{t("tokens.expires")}</span>
        <span>{t("tokens.lastUsed")}</span>
        <span>{t("common.actions")}</span>
      </div>
      {items.map((item) => (
        <article className="token-row" key={item.id}>
          <strong>{item.name || "api-token"}</strong>
          <code>{item.prefix || "-"}</code>
          <span>{item.scope || "all"}</span>
          <InlineTag tone={statusTone(item.status)}>{item.status || "unknown"}</InlineTag>
          <span>{formatDateTime(item.created_at)}</span>
          <span>{item.expires_at ? formatDateTime(item.expires_at) : t("common.never")}</span>
          <span>{item.last_used_at ? formatDateTime(item.last_used_at) : t("common.never")}</span>
          <div className="action-group">
            <button className="ghost-button" type="button" disabled={item.status !== "active" || busyToken === item.id} onClick={() => onRevoke(item.id)}>
              {busyToken === item.id ? t("tokens.revoking") : t("tokens.revoke")}
            </button>
            <button className="ghost-button" type="button" disabled={busyToken === item.id} onClick={() => onDelete(item.id)}>
              {busyToken === item.id ? t("tokens.deleting") : t("tokens.delete")}
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
