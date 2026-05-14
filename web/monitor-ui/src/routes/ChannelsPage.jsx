import React, { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { Link, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { InlineTag, PlusIcon } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { MultiLineChart } from "../components/common/Charts";
import { Switch } from "../components/common/Controls";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, downloadBlob, patchJSON, postJSON } from "../lib/api";
import { buildChannelLink, formatCount, formatDateTime, formatTime, normalizeAnalyticsWindow, setOrDeleteParam } from "../lib/monitor";

const DEFAULT_FORM = {
  name: "",
  base_url: "",
  provider_preset: "openai",
  protocol_family: "openai_compatible",
  routing_profile: "openai_default",
  api_version: "",
  deployment: "",
  project: "",
  location: "",
  model_resource: "",
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
  const [secretTick, setSecretTick] = useState(0);
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const channels = useJSON(apiURL(apiPaths.channels, params), [windowValue, refreshTick]);
  const secret = useJSON(apiPaths.localSecretKey, [secretTick]);
  const presets = useJSON(apiPaths.providerPresets, []);
  const items = channels.data?.items || [];
  const totals = useMemo(() => summarizeChannels(items), [items]);
  const chartItems = useMemo(() => buildChannelTrendItems(items), [items]);
  const chartSeries = useMemo(() => items.map((item) => ({ key: item.id, name: item.name || item.id })), [items]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "24h" ? "" : nextWindow);
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Channel management</p>
          <h1>Channels</h1>
        </div>
        <div className="topbar-meta">
          <button className="ghost-button active icon-text-button" type="button" onClick={() => setFormOpen(true)}>
            <PlusIcon />
            <span>New channel</span>
          </button>
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
          <StatCard label="Tokens" value={formatCount(totals.tokens)} detail={usageCoverageDetail(totals.missing)} />
        </div>
        <div className="usage-chart-grid chart-grid-two">
          <section className="usage-chart-panel">
            <div className="breakdown-title">Requests by channel</div>
            <MultiLineChart items={chartItems} series={chartSeries} metric="request_count" />
          </section>
          <section className="usage-chart-panel">
            <div className="breakdown-title">Tokens by channel</div>
            <MultiLineChart items={chartItems} series={chartSeries} metric="total_tokens" />
          </section>
        </div>
      </section>

      <LocalSecretPanel data={secret.data} loading={secret.loading} error={secret.error} onRefresh={() => setSecretTick((tick) => tick + 1)} />

      {channels.error ? <EmptyState title="Unable to load channels" detail={channels.error} tone="danger" /> : null}
      {channels.loading && !channels.data ? <EmptyState title="Loading channels" detail="Collecting channel configuration and usage summary." /> : null}
      {channels.data ? (
        <section className="channel-grid">
          {items.length ? items.map((item) => <ChannelCard key={item.id} item={item} windowValue={windowValue} onRefresh={() => setRefreshTick((tick) => tick + 1)} />) : <EmptyState title="No channels" detail="Create a channel from Monitor. YAML upstreams are only used as first-run bootstrap input." />}
        </section>
      ) : null}
      {formOpen ? (
        <CreateChannelDialog
          presetData={presets.data}
          onClose={() => setFormOpen(false)}
          onCreated={() => {
            setFormOpen(false);
            setRefreshTick((tick) => tick + 1);
          }}
        />
      ) : null}
    </div>
  );
}

function CreateChannelDialog({ presetData, onClose, onCreated }) {
  const [form, setForm] = useState(DEFAULT_FORM);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const presetState = buildPresetState(presetData, form.provider_preset, form.routing_profile);
  const updateForm = (key, value) => {
    setForm((current) => normalizePresetSelection({ ...current, [key]: value }, presetData, key));
  };

  const submit = async (event) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await postJSON(apiPaths.channels, normalizeChannelPayload(form));
      onCreated();
    } catch (err) {
      setError(err.message || "Unable to save channel.");
    } finally {
      setSaving(false);
    }
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal channel-create-modal" onSubmit={submit}>
        <div className="nav-modal-head">
          <div>
            <p className="eyebrow">Configuration</p>
            <h2>Create channel</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
        </div>
        <div className="channel-form channel-form-modal">
          <label>Name<input required value={form.name} onChange={(event) => updateForm("name", event.target.value)} placeholder="OpenAI Primary" /></label>
          <label>Provider preset<select value={form.provider_preset} onChange={(event) => updateForm("provider_preset", event.target.value)}>{presetState.options.map((preset) => <option key={preset} value={preset}>{preset}</option>)}</select></label>
          <label className="channel-form-wide">Base URL<input required value={form.base_url} onChange={(event) => updateForm("base_url", event.target.value)} placeholder="https://api.openai.com/v1" /></label>
          <label className="channel-form-wide">API key<input type="password" value={form.api_key} onChange={(event) => updateForm("api_key", event.target.value)} placeholder="sk-..." /></label>
          <label className="channel-form-check channel-form-wide"><input type="checkbox" checked={form.allow_unknown_models} onChange={(event) => updateForm("allow_unknown_models", event.target.checked)} /> Allow unknown models</label>
        </div>
        <button className="ghost-button" type="button" onClick={() => setAdvancedOpen((open) => !open)}>{advancedOpen ? "Hide advanced" : "Advanced options"}</button>
        {advancedOpen ? (
          <div className="channel-form channel-form-modal">
            <ProviderAdvancedFields form={form} presetState={presetState} onChange={updateForm} includeHeaders={false} />
          </div>
        ) : null}
        {error ? <p className="auth-error">{error}</p> : null}
        <div className="nav-modal-actions">
          <button className="ghost-button" type="button" onClick={onClose}>Cancel</button>
          <button className="ghost-button active" type="submit" disabled={saving}>{saving ? "Saving" : "Create channel"}</button>
        </div>
      </form>
    </div>,
    document.body,
  );
}

function LocalSecretPanel({ data, loading, error, onRefresh }) {
  const [busy, setBusy] = useState("");
  const [actionError, setActionError] = useState("");
  const [confirmRotate, setConfirmRotate] = useState(false);

  const downloadKey = async () => {
    setBusy("download");
    setActionError("");
    try {
      const blob = await downloadBlob(apiPaths.localSecretKeyExport);
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `trace_index.secret.${data?.fingerprint || "backup"}`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
    } catch (err) {
      setActionError(err.message || "Unable to download key backup.");
    } finally {
      setBusy("");
    }
  };

  const rotateKey = async () => {
    if (!confirmRotate) {
      return;
    }
    setBusy("rotate");
    setActionError("");
    try {
      await postJSON(apiPaths.localSecretKeyRotate, {});
      setConfirmRotate(false);
      onRefresh();
    } catch (err) {
      setActionError(err.message || "Unable to rotate local key.");
    } finally {
      setBusy("");
    }
  };

  const readable = data?.readable && !data?.error;
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Local secret key</p>
          <h2>Channel secret storage</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={readable ? "green" : "danger"}>{loading && !data ? "loading" : readable ? "readable" : "attention"}</InlineTag>
          {data?.mode ? <InlineTag tone="accent">{data.mode}</InlineTag> : null}
        </div>
      </div>
      <div className="detail-meta-strip">
        <Metric label="fingerprint" value={data?.fingerprint || "-"} />
        <Metric label="exists" value={data ? String(Boolean(data.exists)) : "-"} />
        <Metric label="readable" value={data ? String(Boolean(data.readable)) : "-"} />
      </div>
      {data?.key_path ? <p className="trace-subline mono">{data.key_path}</p> : null}
      {error || data?.error || actionError ? <EmptyState title="Secret key action failed" detail={actionError || data?.error || error} tone="danger" compact /> : null}
      <div className="channel-form-actions">
        <button className="ghost-button" type="button" onClick={downloadKey} disabled={!readable || busy === "download"}>{busy === "download" ? "Downloading" : "Download backup"}</button>
        <label className="channel-form-check"><input type="checkbox" checked={confirmRotate} onChange={(event) => setConfirmRotate(event.target.checked)} /> Confirm rotate</label>
        <button className="ghost-button" type="button" onClick={rotateKey} disabled={!readable || !confirmRotate || busy === "rotate"}>{busy === "rotate" ? "Rotating" : "Rotate key"}</button>
      </div>
    </section>
  );
}

function ChannelCard({ item, windowValue, onRefresh }) {
  const summary = item.summary || {};
  const [saving, setSaving] = useState(false);
  const setEnabled = async (enabled) => {
    setSaving(true);
    try {
      await patchJSON(apiPaths.channel(item.id), { enabled });
      onRefresh?.();
    } finally {
      setSaving(false);
    }
  };
  return (
    <Link className="upstream-card" to={buildChannelLink(item.id, windowValue)}>
      <div className="upstream-card-head">
        <div>
          <p className="eyebrow">{item.provider_preset || "custom"}</p>
          <h2>{item.name || item.id}</h2>
        </div>
        <div className="trace-tag-group">
          <Switch checked={Boolean(item.enabled)} onChange={setEnabled} disabled={saving} label={`${item.name || item.id} enabled`} />
          <InlineTag tone={item.source === "bootstrap" ? "gold" : "green"}>{channelSourceLabel(item.source)}</InlineTag>
          {item.secret_storage_mode ? <InlineTag tone={item.secret_storage_mode === "plaintext-local" ? "gold" : "green"}>{item.secret_storage_mode}</InlineTag> : null}
          {item.last_probe_status ? <InlineTag tone={item.last_probe_status === "success" ? "green" : "danger"}>{item.last_probe_status}</InlineTag> : null}
        </div>
      </div>
      <div className="upstream-meta-grid">
        <Metric label="models" value={`${formatCount(item.enabled_model_count)} / ${formatCount(item.model_count)}`} />
        <Metric label="requests" value={formatCount(summary.request_count)} />
        <Metric label="tokens" value={formatCount(summary.total_tokens)} detail={usageCoverageDetail(summary.missing_usage_request)} />
      </div>
      <div className="upstream-card-footer">
        <span className="mono">{item.base_url}</span>
        <span>{formatDateTime(item.last_probe_at || item.updated_at)}</span>
      </div>
    </Link>
  );
}

function channelSourceLabel(source) {
  switch (source) {
    case "bootstrap":
      return "bootstrap";
    case "manual":
    case "":
    case undefined:
      return "web-managed";
    default:
      return source;
  }
}

function buildChannelTrendItems(items) {
  const times = [];
  const byTime = new Map();
  for (const channel of items) {
    for (const trend of channel.trends || []) {
      const key = trend.time;
      if (!byTime.has(key)) {
        times.push(key);
        byTime.set(key, { time: key, series: {} });
      }
      byTime.get(key).series[channel.id] = trend;
    }
  }
  times.sort();
  return times.map((time) => byTime.get(time));
}

function Metric({ label, value, detail = "" }) {
  return (
    <span className="detail-meta-pill">
      <span className="detail-meta-label">{label}</span>
      <strong>{value}</strong>
      {detail ? <small>{detail}</small> : null}
    </span>
  );
}

export function ProviderAdvancedFields({ form, presetState, onChange, includeHeaders = false }) {
  const discoveryOptions = presetState.modelDiscoveryOptions.length ? presetState.modelDiscoveryOptions : ["list_models", "disabled"];
  return (
    <>
      <label>Protocol family<select value={form.protocol_family || ""} onChange={(event) => onChange("protocol_family", event.target.value)}>{presetState.protocolOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      <label>Routing profile<select value={form.routing_profile || ""} onChange={(event) => onChange("routing_profile", event.target.value)}>{presetState.routingOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      {presetState.needsAPIVersion ? <label>API version<input value={form.api_version || ""} onChange={(event) => onChange("api_version", event.target.value)} placeholder={presetState.apiVersionPlaceholder} /></label> : null}
      {presetState.needsDeployment ? <label>Deployment<input value={form.deployment || ""} onChange={(event) => onChange("deployment", event.target.value)} placeholder="gpt-4o-mini" /></label> : null}
      {presetState.needsProject ? <label>Project<input value={form.project || ""} onChange={(event) => onChange("project", event.target.value)} placeholder="my-gcp-project" /></label> : null}
      {presetState.needsLocation ? <label>Location<input value={form.location || ""} onChange={(event) => onChange("location", event.target.value)} placeholder="us-central1" /></label> : null}
      {presetState.needsModelResource ? <label className="channel-form-wide">Model resource<input value={form.model_resource || ""} onChange={(event) => onChange("model_resource", event.target.value)} placeholder="publishers/google/models/gemini-2.5-flash" /></label> : null}
      <label>Model discovery<select value={form.model_discovery || "list_models"} onChange={(event) => onChange("model_discovery", event.target.value)}>{discoveryOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      <label>Priority<input type="number" value={form.priority} onChange={(event) => onChange("priority", event.target.value)} /></label>
      <label>Weight<input type="number" step="0.1" value={form.weight} onChange={(event) => onChange("weight", event.target.value)} /></label>
      <label>Capacity<input type="number" step="0.1" value={form.capacity_hint} onChange={(event) => onChange("capacity_hint", event.target.value)} /></label>
      {includeHeaders ? <label className="channel-form-wide">Headers<textarea value={form.headers_text} onChange={(event) => onChange("headers_text", event.target.value)} spellCheck={false} /></label> : null}
    </>
  );
}

export function buildPresetState(presetData, providerPreset, routingProfile) {
  const presets = Array.isArray(presetData?.presets) ? presetData.presets : [];
  const byID = new Map(presets.map((item) => [item.id, item]));
  const fallbackOptions = ["openai", "openrouter", "anthropic", "google_genai", "azure_openai", "vertex", "vllm"];
  const options = (Array.isArray(presetData?.items) && presetData.items.length ? presetData.items : fallbackOptions).slice().sort();
  const spec = byID.get(providerPreset) || {};
  const defaultProtocolOptions = presetData?.defaults?.protocol_families || ["openai_compatible", "anthropic_messages", "google_genai", "vertex_native"];
  const defaultRoutingOptions = presetData?.defaults?.routing_profiles || ["openai_default", "azure_openai_v1", "azure_openai_deployment", "vllm_openai", "anthropic_default", "google_ai_studio", "vertex_express", "vertex_project_location"];
  const protocolOptions = uniqueSorted([spec.protocol_family, ...defaultProtocolOptions].filter(Boolean));
  const allowedProfiles = Array.isArray(spec.allowed_profiles) && spec.allowed_profiles.length ? spec.allowed_profiles : defaultRoutingOptions;
  const routingOptions = uniqueSorted([routingProfile, spec.routing_profile, ...allowedProfiles].filter(Boolean));
  const effectiveProfile = routingProfile || spec.routing_profile || routingOptions[0] || "";
  return {
    options,
    spec,
    protocolOptions,
    routingOptions,
    modelDiscoveryOptions: presetData?.defaults?.model_discovery || ["list_models", "disabled"],
    needsAPIVersion: effectiveProfile.startsWith("azure_openai") || spec.protocol_family === "anthropic_messages",
    needsDeployment: effectiveProfile === "azure_openai_deployment",
    needsProject: effectiveProfile === "vertex_project_location",
    needsLocation: effectiveProfile === "vertex_project_location",
    needsModelResource: effectiveProfile === "vertex_express" || effectiveProfile === "vertex_project_location",
    apiVersionPlaceholder: spec.protocol_family === "anthropic_messages" ? "2023-06-01" : "preview",
  };
}

export function normalizePresetSelection(form, presetData, changedKey) {
  if (changedKey !== "provider_preset") {
    return form;
  }
  const spec = (presetData?.presets || []).find((item) => item.id === form.provider_preset) || {};
  const allowedProfiles = Array.isArray(spec.allowed_profiles) ? spec.allowed_profiles : [];
  return {
    ...form,
    protocol_family: spec.protocol_family || form.protocol_family || "",
    routing_profile: spec.routing_profile || allowedProfiles[0] || form.routing_profile || "",
  };
}

function uniqueSorted(values) {
  return Array.from(new Set(values.filter(Boolean))).sort();
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
      state.missing += Number(summary.missing_usage_request || 0);
      state.tokens += Number(summary.total_tokens || 0);
      return state;
    },
    { enabled: 0, requests: 0, tokens: 0, missing: 0 },
  );
}

function usageCoverageDetail(missing) {
  const count = Number(missing || 0);
  return count > 0 ? `${formatCount(count)} missing usage` : "";
}
