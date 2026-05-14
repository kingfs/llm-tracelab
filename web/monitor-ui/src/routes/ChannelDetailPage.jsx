import React, { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DetailMetaPill, EditIcon, HomeIcon, InlineTag, ProbeIcon } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { SingleUsageCharts } from "../components/common/Charts";
import { Switch } from "../components/common/Controls";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, patchJSON, postJSON } from "../lib/api";
import { buildTraceLink, formatCount, formatDateTime, formatTime, normalizeAnalyticsWindow, setOrDeleteParam } from "../lib/monitor";

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
  const presets = useJSON(apiPaths.providerPresets, []);
  const channel = detail.data || {};
  const summary = channel.summary || {};
  const modelsUsage = channel.models_usage || [];
  const discoveredDisabledModels = modelsUsage.filter((model) => model.source === "discovered" && !model.enabled).map((model) => model.model);
  const failures = channel.recent_failures || [];
  const probeRuns = channel.recent_probe_runs || [];
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
      await postJSON(apiPaths.channelProbe(channelID), { enable_discovered: false });
      reload();
    } catch (err) {
      setActionError(formatProbeActionError(err));
      reload();
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
  const saveChannel = async () => {
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
  const setModelsEnabled = async (models, enabled) => {
    if (!models.length) {
      return;
    }
    setBusy(enabled ? "models-enable" : "models-disable");
    setActionError("");
    try {
      await patchJSON(apiPaths.channelModelsBatch(channelID), { models, enabled });
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to update models.");
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
              {channel.secret_storage_mode ? <InlineTag tone={channel.secret_storage_mode === "plaintext-local" ? "gold" : "green"}>{channel.secret_storage_mode}</InlineTag> : null}
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
            <button className="icon-button" type="button" onClick={probe} disabled={busy === "probe"} title="Probe channel" aria-label="Probe channel"><ProbeIcon /></button>
            <button className="icon-button" type="button" onClick={() => setEditOpen(true)} title="Edit channel" aria-label="Edit channel"><EditIcon /></button>
            <Switch checked={Boolean(channel.enabled)} onChange={setChannelEnabled} disabled={busy === "channel"} label="Channel enabled" />
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
      {detail.data?.secret_storage_mode === "plaintext-local" ? (
        <EmptyState title="Local plaintext secret storage" detail="API keys and secret headers are redacted in Monitor responses, but currently stored in the local SQLite database without encryption." tone="danger" />
      ) : null}

      {detail.data && editOpen ? (
        <EditChannelDialog
          channel={channel}
          form={editForm}
          presets={presets.data?.items || []}
          saving={busy === "save-channel"}
          onChange={setEditForm}
          onReset={() => setEditForm(editFormFromChannel(channel))}
          onClose={() => setEditOpen(false)}
          onSave={saveChannel}
        />
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
            <SingleUsageCharts items={trends} />
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
              <button className="ghost-button" type="button" onClick={() => setModelsEnabled(discoveredDisabledModels, true)} disabled={!discoveredDisabledModels.length || busy === "models-enable"}>{busy === "models-enable" ? "Enabling" : `Enable new (${formatCount(discoveredDisabledModels.length)})`}</button>
            </form>
            <div className="channel-model-card-grid">
              {modelsUsage.length ? modelsUsage.map((model) => <ChannelModelRow key={model.model} item={model} busy={busy === model.model} onToggle={() => setModelEnabled(model.model, !model.enabled)} />) : <EmptyState title="No models" detail="Probe or manually configure models for this channel." compact />}
            </div>
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Discovery</p>
                <h2>Recent probes</h2>
              </div>
            </div>
            {probeRuns.length ? (
              <div className="channel-probe-list">
                {probeRuns.map((run) => <ProbeRunCard key={run.id} item={run} />)}
              </div>
            ) : (
              <EmptyState title="No probe runs" detail="Run a channel probe to record discovery status and troubleshooting context." />
            )}
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

function EditChannelDialog({ channel, form, presets = [], saving, onChange, onReset, onClose, onSave }) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const submit = async (event) => {
    event.preventDefault();
    await onSave();
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal channel-edit-modal" onSubmit={submit}>
        <div className="nav-modal-head">
          <div>
            <p className="eyebrow">Configuration</p>
            <h2>Edit channel</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
        </div>
        <div className="channel-form channel-form-modal">
          <label>Name<input required value={form.name} onChange={(event) => setEditValue(onChange, "name", event.target.value)} /></label>
          <label>Provider preset<select value={form.provider_preset} onChange={(event) => setEditValue(onChange, "provider_preset", event.target.value)}>{providerOptions(presets, form.provider_preset).map((preset) => <option key={preset} value={preset}>{preset}</option>)}</select></label>
          <label className="channel-form-wide">Base URL<input required value={form.base_url} onChange={(event) => setEditValue(onChange, "base_url", event.target.value)} /></label>
          <label className="channel-form-wide">API key<input type="password" value={form.api_key} onChange={(event) => setEditValue(onChange, "api_key", event.target.value)} placeholder={channel.api_key_hint ? `keep ${channel.api_key_hint}` : "unchanged"} /></label>
          <label className="channel-form-check channel-form-wide"><input type="checkbox" checked={form.allow_unknown_models} onChange={(event) => setEditValue(onChange, "allow_unknown_models", event.target.checked)} /> Allow unknown models</label>
        </div>
        <button className="ghost-button" type="button" onClick={() => setAdvancedOpen((open) => !open)}>{advancedOpen ? "Hide advanced" : "Advanced options"}</button>
        {advancedOpen ? (
          <div className="channel-form channel-form-modal">
            <label>Priority<input type="number" value={form.priority} onChange={(event) => setEditValue(onChange, "priority", event.target.value)} /></label>
            <label>Weight<input type="number" step="0.1" value={form.weight} onChange={(event) => setEditValue(onChange, "weight", event.target.value)} /></label>
            <label>Capacity<input type="number" step="0.1" value={form.capacity_hint} onChange={(event) => setEditValue(onChange, "capacity_hint", event.target.value)} /></label>
            <label>Model discovery<select value={form.model_discovery} onChange={(event) => setEditValue(onChange, "model_discovery", event.target.value)}><option value="list_models">list_models</option><option value="disabled">disabled</option></select></label>
            <label className="channel-form-wide">Headers<textarea value={form.headers_text} onChange={(event) => setEditValue(onChange, "headers_text", event.target.value)} spellCheck={false} /></label>
          </div>
        ) : null}
        <div className="nav-modal-actions">
          <button className="ghost-button" type="button" onClick={onReset}>Reset</button>
          <button className="ghost-button" type="button" onClick={onClose}>Cancel</button>
          <button className="ghost-button active" type="submit" disabled={saving}>{saving ? "Saving" : "Save changes"}</button>
        </div>
      </form>
    </div>,
    document.body,
  );
}

function ProbeRunCard({ item }) {
  const failed = item.status !== "success";
  return (
    <div className={failed ? "channel-probe-card channel-probe-card-failed" : "channel-probe-card"}>
      <div className="channel-probe-card-head">
        <div className="trace-tag-group">
          <InlineTag tone={failed ? "danger" : "green"}>{item.status || "unknown"}</InlineTag>
          {item.failure_reason ? <InlineTag tone="accent">{item.failure_reason}</InlineTag> : null}
          {item.status_code ? <InlineTag>{item.status_code}</InlineTag> : null}
        </div>
        <span>{formatDateTime(item.completed_at || item.started_at)}</span>
      </div>
      <div className="channel-probe-meta">
        <span>{formatCount(item.discovered_count)} discovered</span>
        <span>{formatCount(item.enabled_count)} enabled</span>
        <span>{formatCount(item.duration_ms)} ms</span>
      </div>
      {item.endpoint ? <div className="channel-probe-endpoint">{item.endpoint}</div> : null}
      {item.error_text ? <div className="upstream-failure-detail">{item.error_text}</div> : null}
      {item.retry_hint ? <div className="channel-probe-hint">{item.retry_hint}</div> : null}
    </div>
  );
}

function ChannelModelRow({ item, busy, onToggle }) {
  const summary = item.summary || {};
  const isDiscoveredDisabled = item.source === "discovered" && !item.enabled;
  return (
    <div className="channel-model-card">
      <div className="channel-model-card-head">
        <div>
          <strong>{item.model}</strong>
          <span>{isDiscoveredDisabled ? "discovered, awaiting enable" : item.source || "unknown"}</span>
        </div>
        <Switch checked={Boolean(item.enabled)} onChange={onToggle} disabled={busy} label={`${item.model} enabled`} />
      </div>
      <div className="trace-tag-group">
        <InlineTag tone={item.enabled ? "green" : "default"}>{item.enabled ? "enabled" : "disabled"}</InlineTag>
        {isDiscoveredDisabled ? <InlineTag tone="gold">new</InlineTag> : null}
      </div>
      <div className="model-market-metrics model-market-metrics-compact">
        <Metric label="req" value={formatCount(summary.request_count)} />
        <Metric label="err" value={formatCount(summary.failed_request)} danger={Number(summary.failed_request || 0) > 0} />
        <Metric label="tok" value={formatCount(summary.total_tokens)} />
      </div>
    </div>
  );
}

function Metric({ label, value, danger = false }) {
  return (
    <span className={danger ? "model-market-metric model-market-metric-danger" : "model-market-metric"}>
      <span>{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function formatProbeActionError(err) {
  const payload = err?.payload || {};
  const parts = [];
  if (payload.failure_reason) {
    parts.push(payload.failure_reason);
  }
  if (payload.error_text || err?.message) {
    parts.push(payload.error_text || err.message);
  }
  if (payload.retry_hint) {
    parts.push(payload.retry_hint);
  }
  return parts.join(" · ") || "Probe failed.";
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

function providerOptions(presets, current) {
  const fallback = ["openai", "openrouter", "anthropic", "google_genai", "azure_openai", "vertex", "vllm"];
  return Array.from(new Set([...(presets.length ? presets : fallback), current].filter(Boolean))).sort();
}
