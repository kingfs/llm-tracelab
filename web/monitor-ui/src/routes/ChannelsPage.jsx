import React, { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { InlineTag } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, postJSON } from "../lib/api";
import { buildChannelLink, formatCount, formatDateTime, formatTime, normalizeAnalyticsWindow, setOrDeleteParam } from "../lib/monitor";

const DEFAULT_FORM = {
  id: "",
  name: "",
  base_url: "",
  provider_preset: "openai",
  api_key: "",
  enabled: true,
  priority: 100,
  weight: 1,
  capacity_hint: 1,
  model_discovery: "list_models",
  allow_unknown_models: false,
};

export function ChannelsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const [refreshTick, setRefreshTick] = useState(0);
  const [formOpen, setFormOpen] = useState(false);
  const [form, setForm] = useState(DEFAULT_FORM);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const channels = useJSON(apiURL(apiPaths.channels, params), [windowValue, refreshTick]);
  const items = channels.data?.items || [];
  const totals = useMemo(() => summarizeChannels(items), [items]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };
  const submit = async (event) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await postJSON(apiPaths.channels, normalizeChannelPayload(form));
      setForm(DEFAULT_FORM);
      setFormOpen(false);
      setRefreshTick((tick) => tick + 1);
    } catch (err) {
      setError(err.message || "Unable to save channel.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Channel management</p>
          <h1>Channels</h1>
        </div>
        <div className="topbar-meta">
          <button className="ghost-button active" type="button" onClick={() => setFormOpen((open) => !open)}>{formOpen ? "Close" : "New channel"}</button>
          <span className="badge">{channels.data?.refreshed_at ? formatTime(channels.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Overview</p>
            <h2>Managed upstream channels</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Channel analytics window">
              {["24h", "7d", "30d", "all"].map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Channels" value={formatCount(items.length)} />
          <StatCard label="Enabled" value={formatCount(totals.enabled)} />
          <StatCard label="Requests" value={formatCount(totals.requests)} />
          <StatCard label="Tokens" value={formatCount(totals.tokens)} />
        </div>
      </section>

      {formOpen ? (
        <section className="panel">
          <div className="panel-head">
            <div>
              <p className="eyebrow">Configuration</p>
              <h2>Create channel</h2>
            </div>
          </div>
          <form className="channel-form" onSubmit={submit}>
            <label>ID<input value={form.id} onChange={(event) => setFormValue(setForm, "id", event.target.value)} placeholder="openai-primary" /></label>
            <label>Name<input value={form.name} onChange={(event) => setFormValue(setForm, "name", event.target.value)} placeholder="OpenAI Primary" /></label>
            <label>Base URL<input value={form.base_url} onChange={(event) => setFormValue(setForm, "base_url", event.target.value)} placeholder="https://api.openai.com/v1" /></label>
            <label>Provider preset<input value={form.provider_preset} onChange={(event) => setFormValue(setForm, "provider_preset", event.target.value)} placeholder="openai" /></label>
            <label>API key<input type="password" value={form.api_key} onChange={(event) => setFormValue(setForm, "api_key", event.target.value)} placeholder="sk-..." /></label>
            <label>Priority<input type="number" value={form.priority} onChange={(event) => setFormValue(setForm, "priority", event.target.value)} /></label>
            <label>Weight<input type="number" step="0.1" value={form.weight} onChange={(event) => setFormValue(setForm, "weight", event.target.value)} /></label>
            <label>Capacity<input type="number" step="0.1" value={form.capacity_hint} onChange={(event) => setFormValue(setForm, "capacity_hint", event.target.value)} /></label>
            <label className="channel-form-check"><input type="checkbox" checked={form.enabled} onChange={(event) => setFormValue(setForm, "enabled", event.target.checked)} /> Enabled</label>
            <label className="channel-form-check"><input type="checkbox" checked={form.allow_unknown_models} onChange={(event) => setFormValue(setForm, "allow_unknown_models", event.target.checked)} /> Allow unknown models</label>
            {error ? <p className="auth-error">{error}</p> : null}
            <div className="channel-form-actions">
              <button className="ghost-button" type="button" onClick={() => setForm(DEFAULT_FORM)}>Reset</button>
              <button className="ghost-button active" type="submit" disabled={saving}>{saving ? "Saving" : "Save"}</button>
            </div>
          </form>
        </section>
      ) : null}

      {channels.error ? <EmptyState title="Unable to load channels" detail={channels.error} tone="danger" /> : null}
      {channels.loading && !channels.data ? <EmptyState title="Loading channels" detail="Collecting channel configuration and usage summary." /> : null}
      {channels.data ? (
        <section className="channel-grid">
          {items.length ? items.map((item) => <ChannelCard key={item.id} item={item} windowValue={windowValue} />) : <EmptyState title="No channels" detail="Create a channel or bootstrap one from upstream configuration." />}
        </section>
      ) : null}
    </div>
  );
}

function ChannelCard({ item, windowValue }) {
  const summary = item.summary || {};
  return (
    <Link className="upstream-card" to={buildChannelLink(item.id, windowValue)}>
      <div className="upstream-card-head">
        <div>
          <p className="eyebrow">{item.provider_preset || "custom"}</p>
          <h2>{item.name || item.id}</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={item.enabled ? "green" : "default"}>{item.enabled ? "enabled" : "disabled"}</InlineTag>
          {item.last_probe_status ? <InlineTag tone={item.last_probe_status === "success" ? "green" : "danger"}>{item.last_probe_status}</InlineTag> : null}
        </div>
      </div>
      <div className="upstream-meta-grid">
        <Metric label="models" value={`${formatCount(item.enabled_model_count)} / ${formatCount(item.model_count)}`} />
        <Metric label="requests" value={formatCount(summary.request_count)} />
        <Metric label="tokens" value={formatCount(summary.total_tokens)} />
      </div>
      <div className="upstream-card-footer">
        <span className="mono">{item.base_url}</span>
        <span>{formatDateTime(item.last_probe_at || item.updated_at)}</span>
      </div>
    </Link>
  );
}

function Metric({ label, value }) {
  return (
    <span className="detail-meta-pill">
      <span className="detail-meta-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function setFormValue(setForm, key, value) {
  setForm((current) => ({ ...current, [key]: value }));
}

function normalizeChannelPayload(form) {
  return {
    ...form,
    priority: Number(form.priority || 0),
    weight: Number(form.weight || 1),
    capacity_hint: Number(form.capacity_hint || 1),
  };
}

function summarizeChannels(items) {
  return items.reduce(
    (state, item) => {
      const summary = item.summary || {};
      if (item.enabled) {
        state.enabled += 1;
      }
      state.requests += Number(summary.request_count || 0);
      state.tokens += Number(summary.total_tokens || 0);
      return state;
    },
    { enabled: 0, requests: 0, tokens: 0 },
  );
}
