import React, { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DetailMetaPill, HomeIcon, InlineTag } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, patchJSON, postJSON } from "../lib/api";
import { buildTraceLink, formatCount, formatDateTime, formatTime, formatTimelineBucketLabel, normalizeAnalyticsWindow, setOrDeleteParam } from "../lib/monitor";

export function ChannelDetailPage() {
  const { channelID = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const [refreshTick, setRefreshTick] = useState(0);
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState("");
  const [modelDraft, setModelDraft] = useState("");
  const [editOpen, setEditOpen] = useState(false);
  const [editForm, setEditForm] = useState(() => emptyEditForm());
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const detail = useJSON(apiURL(apiPaths.channel(channelID), params), [channelID, windowValue, refreshTick]);
  const channel = detail.data || {};
  const summary = channel.summary || {};
  const modelsUsage = channel.models_usage || [];
  const failures = channel.recent_failures || [];
  const trends = channel.trends || [];

  useEffect(() => {
    if (!detail.data) {
      return;
    }
    setEditForm(editFormFromChannel(detail.data));
  }, [detail.data]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };
  const reload = () => setRefreshTick((tick) => tick + 1);
  const probe = async () => {
    setBusy("probe");
    setActionError("");
    try {
      await postJSON(apiPaths.channelProbe(channelID), {});
      reload();
    } catch (err) {
      setActionError(err.message || "Probe failed.");
    } finally {
      setBusy("");
    }
  };
  const setChannelEnabled = async (enabled) => {
    setBusy("channel");
    setActionError("");
    try {
      await patchJSON(apiPaths.channel(channelID), { enabled });
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to update channel.");
    } finally {
      setBusy("");
    }
  };
  const saveChannel = async (event) => {
    event.preventDefault();
    setBusy("save-channel");
    setActionError("");
    try {
      await patchJSON(apiPaths.channel(channelID), channelPayloadFromForm(editForm));
      setEditOpen(false);
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to save channel.");
    } finally {
      setBusy("");
    }
  };
  const setModelEnabled = async (model, enabled) => {
    setBusy(model);
    setActionError("");
    try {
      await patchJSON(apiPaths.channelModel(channelID, model), { enabled });
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to update model.");
    } finally {
      setBusy("");
    }
  };
  const addModel = async (event) => {
    event.preventDefault();
    const model = modelDraft.trim();
    if (!model) {
      return;
    }
    setBusy("add-model");
    setActionError("");
    try {
      await postJSON(apiPaths.channelModels(channelID), { model, display_name: model, enabled: true });
      setModelDraft("");
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to add model.");
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{channel.name || channelID}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone={channel.enabled ? "green" : "default"}>{channel.enabled ? "enabled" : "disabled"}</InlineTag>
              <InlineTag tone="accent">{channel.provider_preset || "custom"}</InlineTag>
              {channel.last_probe_status ? <InlineTag tone={channel.last_probe_status === "success" ? "green" : "danger"}>{channel.last_probe_status}</InlineTag> : null}
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label="base url" value={channel.base_url || "-"} mono />
            <DetailMetaPill label="models" value={`${formatCount(channel.enabled_model_count)} / ${formatCount(channel.model_count)}`} />
            <DetailMetaPill label="requests" value={formatCount(summary.request_count)} />
            <DetailMetaPill label="tokens" value={formatCount(summary.total_tokens)} />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to="/channels" title="Back to channels" aria-label="Back to channels">
              <HomeIcon />
            </Link>
            <button className="ghost-button" type="button" onClick={probe} disabled={busy === "probe"}>{busy === "probe" ? "Probing" : "Probe"}</button>
            <button className="ghost-button" type="button" onClick={() => setEditOpen((open) => !open)}>{editOpen ? "Close edit" : "Edit"}</button>
            <button className="ghost-button" type="button" onClick={() => setChannelEnabled(!channel.enabled)} disabled={busy === "channel"}>{channel.enabled ? "Disable" : "Enable"}</button>
          </div>
          <span className="badge">{detail.data ? formatTime(detail.data.updated_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Analytics</p>
            <h2>Channel usage</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Channel detail window">
              {["24h", "7d", "30d", "all"].map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Requests" value={formatCount(summary.request_count)} />
          <StatCard label="Errors" value={formatCount(summary.failed_request)} accent={summary.failed_request ? "accent-red" : ""} />
          <StatCard label="Tokens" value={formatCount(summary.total_tokens)} />
          <StatCard label="Success" value={`${Number(summary.success_rate || 0).toFixed(1)}%`} />
        </div>
      </section>

      {actionError ? <EmptyState title="Channel action failed" detail={actionError} tone="danger" /> : null}
      {detail.error ? <EmptyState title="Unable to load channel" detail={detail.error} tone="danger" /> : null}
      {detail.loading && !detail.data ? <EmptyState title="Loading channel" detail="Collecting channel configuration, models, and usage." /> : null}

      {detail.data && editOpen ? (
        <section className="panel">
          <div className="panel-head">
            <div>
              <p className="eyebrow">Configuration</p>
              <h2>Edit channel</h2>
            </div>
          </div>
          <form className="channel-form" onSubmit={saveChannel}>
            <label>Name<input value={editForm.name} onChange={(event) => setEditValue(setEditForm, "name", event.target.value)} /></label>
            <label>Base URL<input value={editForm.base_url} onChange={(event) => setEditValue(setEditForm, "base_url", event.target.value)} /></label>
            <label>Provider preset<input value={editForm.provider_preset} onChange={(event) => setEditValue(setEditForm, "provider_preset", event.target.value)} /></label>
            <label>API key<input type="password" value={editForm.api_key} onChange={(event) => setEditValue(setEditForm, "api_key", event.target.value)} placeholder={channel.api_key_hint ? `keep ${channel.api_key_hint}` : "unchanged"} /></label>
            <label>Priority<input type="number" value={editForm.priority} onChange={(event) => setEditValue(setEditForm, "priority", event.target.value)} /></label>
            <label>Weight<input type="number" step="0.1" value={editForm.weight} onChange={(event) => setEditValue(setEditForm, "weight", event.target.value)} /></label>
            <label>Capacity<input type="number" step="0.1" value={editForm.capacity_hint} onChange={(event) => setEditValue(setEditForm, "capacity_hint", event.target.value)} /></label>
            <label>Model discovery<input value={editForm.model_discovery} onChange={(event) => setEditValue(setEditForm, "model_discovery", event.target.value)} /></label>
            <label className="channel-form-check"><input type="checkbox" checked={editForm.enabled} onChange={(event) => setEditValue(setEditForm, "enabled", event.target.checked)} /> Enabled</label>
            <label className="channel-form-check"><input type="checkbox" checked={editForm.allow_unknown_models} onChange={(event) => setEditValue(setEditForm, "allow_unknown_models", event.target.checked)} /> Allow unknown models</label>
            <label className="channel-form-wide">Headers<textarea value={editForm.headers_text} onChange={(event) => setEditValue(setEditForm, "headers_text", event.target.value)} spellCheck={false} /></label>
            <div className="channel-form-actions">
              <button className="ghost-button" type="button" onClick={() => setEditForm(editFormFromChannel(channel))}>Reset</button>
              <button className="ghost-button active" type="submit" disabled={busy === "save-channel"}>{busy === "save-channel" ? "Saving" : "Save changes"}</button>
            </div>
          </form>
        </section>
      ) : null}

      {detail.data ? (
        <>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Trend</p>
                <h2>Token and request buckets</h2>
              </div>
            </div>
            <UsageBars items={trends} />
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Models</p>
                <h2>Usage by model</h2>
              </div>
            </div>
            <form className="filter-bar" onSubmit={addModel}>
              <input className="filter-input filter-input-wide" type="search" value={modelDraft} onChange={(event) => setModelDraft(event.target.value)} placeholder="Add model manually" />
              <button className="ghost-button active" type="submit" disabled={busy === "add-model"}>{busy === "add-model" ? "Adding" : "Add model"}</button>
            </form>
            <div className="channel-model-table">
              {modelsUsage.length ? modelsUsage.map((model) => <ChannelModelRow key={model.model} item={model} busy={busy === model.model} onToggle={() => setModelEnabled(model.model, !model.enabled)} />) : <EmptyState title="No models" detail="Probe or manually configure models for this channel." compact />}
            </div>
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Failures</p>
                <h2>Recent failed traces</h2>
              </div>
            </div>
            {failures.length ? (
              <div className="upstream-failure-list upstream-failure-list-detail">
                {failures.map((failure) => (
                  <Link key={failure.trace_id} className="upstream-failure-card" to={buildTraceLink(failure.trace_id, "channels", "", "", "failure")}>
                    <div className="trace-tag-group">
                      <InlineTag tone="danger">{failure.status_code}</InlineTag>
                      {failure.reason ? <InlineTag>{failure.reason}</InlineTag> : null}
                    </div>
                    <strong>{failure.model || "unknown-model"}</strong>
                    <span>{formatDateTime(failure.recorded_at)}</span>
                    {failure.error_text ? <div className="upstream-failure-detail">{failure.error_text}</div> : null}
                  </Link>
                ))}
              </div>
            ) : (
              <EmptyState title="No recent failures" detail="This channel has no failed trace in the selected window." />
            )}
          </section>
        </>
      ) : null}
    </div>
  );
}

function ChannelModelRow({ item, busy, onToggle }) {
  const summary = item.summary || {};
  return (
    <div className="channel-model-row">
      <div>
        <strong>{item.model}</strong>
        <span>{item.source || "unknown"}</span>
      </div>
      <InlineTag tone={item.enabled ? "green" : "default"}>{item.enabled ? "enabled" : "disabled"}</InlineTag>
      <span>{formatCount(summary.request_count)} req</span>
      <span>{formatCount(summary.failed_request)} err</span>
      <span>{formatCount(summary.total_tokens)} tok</span>
      <button className="ghost-button" type="button" onClick={onToggle} disabled={busy}>{busy ? "Saving" : item.enabled ? "Disable" : "Enable"}</button>
    </div>
  );
}

function emptyEditForm() {
  return {
    name: "",
    base_url: "",
    provider_preset: "",
    api_key: "",
    priority: 0,
    weight: 1,
    capacity_hint: 1,
    model_discovery: "list_models",
    enabled: true,
    allow_unknown_models: false,
    headers_text: "",
  };
}

function editFormFromChannel(channel = {}) {
  const headers = channel.headers || {};
  return {
    name: channel.name || "",
    base_url: channel.base_url || "",
    provider_preset: channel.provider_preset || "",
    api_key: "",
    priority: channel.priority ?? 0,
    weight: channel.weight ?? 1,
    capacity_hint: channel.capacity_hint ?? 1,
    model_discovery: channel.model_discovery || "list_models",
    enabled: Boolean(channel.enabled),
    allow_unknown_models: Boolean(channel.allow_unknown_models),
    headers_text: Object.keys(headers).sort().map((key) => `${key}: ${headers[key]}`).join("\n"),
  };
}

function setEditValue(setEditForm, key, value) {
  setEditForm((current) => ({ ...current, [key]: value }));
}

function channelPayloadFromForm(form) {
  const payload = {
    name: form.name,
    base_url: form.base_url,
    provider_preset: form.provider_preset,
    priority: Number(form.priority || 0),
    weight: Number(form.weight || 1),
    capacity_hint: Number(form.capacity_hint || 1),
    model_discovery: form.model_discovery,
    enabled: Boolean(form.enabled),
    allow_unknown_models: Boolean(form.allow_unknown_models),
    headers: parseHeadersText(form.headers_text),
  };
  if (form.api_key.trim()) {
    payload.api_key = form.api_key.trim();
  }
  return payload;
}

function parseHeadersText(value) {
  const headers = {};
  String(value || "").split(/\r?\n/).forEach((line) => {
    const trimmed = line.trim();
    if (!trimmed) {
      return;
    }
    const index = trimmed.indexOf(":");
    if (index <= 0) {
      return;
    }
    const key = trimmed.slice(0, index).trim();
    const headerValue = trimmed.slice(index + 1).trim();
    if (!key) {
      return;
    }
    headers[key] = headerValue === "***" ? { keep: true } : headerValue;
  });
  return headers;
}

function UsageBars({ items }) {
  const maxTokens = Math.max(1, ...items.map((item) => Number(item.total_tokens || 0)));
  if (!items.length) {
    return <EmptyState title="No trend" detail="No usage buckets are available for this channel." compact />;
  }
  return (
    <div className="usage-bars">
      {items.map((item) => {
        const height = Math.max(6, Math.round((Number(item.total_tokens || 0) / maxTokens) * 120));
        return (
          <div className="usage-bar-item" key={item.time} title={`${formatTimelineBucketLabel(item.time)} · ${formatCount(item.total_tokens)} tokens · ${formatCount(item.request_count)} requests`}>
            <span className="usage-bar-count">{formatCount(item.request_count)}</span>
            <div className="usage-bar-wrap">
              <span className={item.failed_request ? "usage-bar usage-bar-danger" : "usage-bar"} style={{ height }} />
            </div>
            <span className="usage-bar-label">{formatTimelineBucketLabel(item.time)}</span>
          </div>
        );
      })}
    </div>
  );
}
