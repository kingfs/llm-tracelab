import React, { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { Link, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DeleteIcon, InlineTag, PlusIcon } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { MultiLineChart } from "../components/common/Charts";
import { Switch } from "../components/common/Controls";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, deleteJSON, downloadBlob, patchJSON, postJSON } from "../lib/api";
import { buildProviderLink, formatCount, formatDateTime, formatTime, MONITOR_WINDOW_OPTIONS, normalizeAnalyticsWindow, setOrDeleteParam } from "../lib/monitor";

const DEFAULT_FORM = {
  name: "",
  base_url: "",
  provider_preset: "openai",
  api_type: "chat_completions",
  mode: "proxy",
  capabilities: {},
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

export function ProvidersPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const [refreshTick, setRefreshTick] = useState(0);
  const [formOpen, setFormOpen] = useState(false);
  const [secretTick, setSecretTick] = useState(0);
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const providers = useJSON(apiURL(apiPaths.providers, params), [windowValue, refreshTick]);
  const secret = useJSON(apiPaths.localSecretKey, [secretTick]);
  const presets = useJSON(apiPaths.providerPresets, []);
  const items = providers.data?.items || [];
  const totals = useMemo(() => summarizeProviders(items), [items]);
  const chartItems = useMemo(() => buildProviderTrendItems(items), [items]);
  const chartSeries = useMemo(() => items.map((item) => ({ key: item.id, name: item.name || item.id })), [items]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "today" ? "" : nextWindow);
    setSearchParams(next);
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Provider management</p>
          <h1>Providers</h1>
        </div>
        <div className="topbar-meta">
          <button className="ghost-button active icon-text-button" type="button" onClick={() => setFormOpen(true)}>
            <PlusIcon />
            <span>New provider</span>
          </button>
          <span className="badge">{providers.data?.refreshed_at ? formatTime(providers.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Overview</p>
            <h2>Managed upstream providers</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Provider analytics window">
              {MONITOR_WINDOW_OPTIONS.map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact">
          <StatCard label="Providers" value={formatCount(items.length)} />
          <StatCard label="Enabled" value={formatCount(totals.enabled)} />
          <StatCard label="Requests" value={formatCount(totals.requests)} />
          <StatCard label="Tokens" value={formatCount(totals.tokens)} detail={usageCoverageDetail(totals.missing)} />
        </div>
        <div className="usage-chart-grid chart-grid-two">
          <section className="usage-chart-panel">
            <div className="breakdown-title">Requests by provider</div>
            <MultiLineChart items={chartItems} series={chartSeries} metric="request_count" />
          </section>
          <section className="usage-chart-panel">
            <div className="breakdown-title">Tokens by provider</div>
            <MultiLineChart items={chartItems} series={chartSeries} metric="total_tokens" />
          </section>
        </div>
      </section>

      <LocalSecretPanel data={secret.data} loading={secret.loading} error={secret.error} onRefresh={() => setSecretTick((tick) => tick + 1)} />

      {providers.error ? <EmptyState title="Unable to load providers" detail={providers.error} tone="danger" /> : null}
      {providers.loading && !providers.data ? <EmptyState title="Loading providers" detail="Collecting provider configuration and usage summary." /> : null}
      {providers.data ? (
        <section className="provider-grid">
          {items.length ? items.map((item) => <ProviderCard key={item.id} item={item} windowValue={windowValue} onRefresh={() => setRefreshTick((tick) => tick + 1)} />) : <EmptyState title="No providers" detail="Create a provider from Monitor. YAML upstreams are only used as first-run bootstrap input." />}
        </section>
      ) : null}
      {formOpen ? (
        <CreateProviderDialog
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

export const ChannelsPage = ProvidersPage;

function CreateProviderDialog({ presetData, onClose, onCreated }) {
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
      await postJSON(apiPaths.providers, normalizeProviderPayload(form));
      onCreated();
    } catch (err) {
      setError(err.message || "Unable to save provider.");
    } finally {
      setSaving(false);
    }
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal provider-create-modal" onSubmit={submit}>
        <div className="nav-modal-head">
          <div>
            <p className="eyebrow">Configuration</p>
            <h2>Create provider</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
        </div>
        <div className="provider-form provider-form-modal">
          <label>Name<input required value={form.name} onChange={(event) => updateForm("name", event.target.value)} placeholder="OpenAI Primary" /></label>
          <label>Provider preset<select value={form.provider_preset} onChange={(event) => updateForm("provider_preset", event.target.value)}>{presetState.options.map((preset) => <option key={preset} value={preset}>{preset}</option>)}</select></label>
          <label className="provider-form-wide">Base URL<input required value={form.base_url} onChange={(event) => updateForm("base_url", event.target.value)} placeholder="https://api.openai.com/v1" /></label>
          <label className="provider-form-wide">API key<input type="password" value={form.api_key} onChange={(event) => updateForm("api_key", event.target.value)} placeholder="sk-..." /></label>
          <label className="provider-form-check provider-form-wide"><input type="checkbox" checked={form.allow_unknown_models} onChange={(event) => updateForm("allow_unknown_models", event.target.checked)} /> Allow unknown models</label>
        </div>
        <button className="ghost-button" type="button" onClick={() => setAdvancedOpen((open) => !open)}>{advancedOpen ? "Hide advanced" : "Advanced options"}</button>
        {advancedOpen ? (
          <div className="provider-form provider-form-modal">
            <ProviderAdvancedFields form={form} presetState={presetState} onChange={updateForm} includeHeaders={false} />
          </div>
        ) : null}
        {error ? <p className="auth-error">{error}</p> : null}
        <div className="nav-modal-actions">
          <button className="ghost-button" type="button" onClick={onClose}>Cancel</button>
          <button className="ghost-button active" type="submit" disabled={saving}>{saving ? "Saving" : "Create provider"}</button>
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
          <h2>Provider secret storage</h2>
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
      <div className="provider-form-actions">
        <button className="ghost-button" type="button" onClick={downloadKey} disabled={!readable || busy === "download"}>{busy === "download" ? "Downloading" : "Download backup"}</button>
        <label className="provider-form-check"><input type="checkbox" checked={confirmRotate} onChange={(event) => setConfirmRotate(event.target.checked)} /> Confirm rotate</label>
        <button className="ghost-button" type="button" onClick={rotateKey} disabled={!readable || !confirmRotate || busy === "rotate"}>{busy === "rotate" ? "Rotating" : "Rotate key"}</button>
      </div>
    </section>
  );
}

function ProviderCard({ item, windowValue, onRefresh }) {
  const summary = item.summary || {};
  const [saving, setSaving] = useState(false);
  const setEnabled = async (enabled, event) => {
    event?.preventDefault();
    event?.stopPropagation();
    setSaving(true);
    try {
      await patchJSON(apiPaths.provider(item.id), { enabled });
      onRefresh?.();
    } finally {
      setSaving(false);
    }
  };
  const deleteProvider = async (event) => {
    event.preventDefault();
    event.stopPropagation();
    if (!window.confirm(`Delete provider ${item.name || item.id}? Configured models for this provider will also be removed.`)) {
      return;
    }
    setSaving(true);
    try {
      await deleteJSON(apiPaths.provider(item.id));
      onRefresh?.();
    } finally {
      setSaving(false);
    }
  };
  return (
    <Link className="upstream-card" to={buildProviderLink(item.id, windowValue)}>
      <div className="upstream-card-head">
        <div>
          <p className="eyebrow">{item.provider_preset || "custom"}</p>
          <h2>{item.name || item.id}</h2>
        </div>
        <div className="trace-tag-group">
          <Switch checked={Boolean(item.enabled)} onChange={setEnabled} disabled={saving} label={`${item.name || item.id} enabled`} />
          <button className="icon-button" type="button" onClick={deleteProvider} disabled={saving} title="Delete provider" aria-label={`Delete ${item.name || item.id}`}>
            <DeleteIcon />
          </button>
          <InlineTag tone={item.source === "bootstrap" ? "gold" : "green"}>{providerSourceLabel(item.source)}</InlineTag>
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

function providerSourceLabel(source) {
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

function buildProviderTrendItems(items) {
  const times = [];
  const byTime = new Map();
  for (const provider of items) {
    for (const trend of provider.trends || []) {
      const key = trend.time;
      if (!byTime.has(key)) {
        times.push(key);
        byTime.set(key, { time: key, series: {} });
      }
      byTime.get(key).series[provider.id] = trend;
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
      <label>API type<select value={form.api_type || "chat_completions"} onChange={(event) => onChange("api_type", event.target.value)}>{API_TYPE_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>API mode<select value={form.mode || "proxy"} onChange={(event) => onChange("mode", event.target.value)}>{API_MODE_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Protocol family<select value={form.protocol_family || ""} onChange={(event) => onChange("protocol_family", event.target.value)}>{presetState.protocolOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      <label>Routing profile<select value={form.routing_profile || ""} onChange={(event) => onChange("routing_profile", event.target.value)}>{presetState.routingOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      {presetState.needsAPIVersion ? <label>API version<input value={form.api_version || ""} onChange={(event) => onChange("api_version", event.target.value)} placeholder={presetState.apiVersionPlaceholder} /></label> : null}
      {presetState.needsDeployment ? <label>Deployment<input value={form.deployment || ""} onChange={(event) => onChange("deployment", event.target.value)} placeholder="gpt-4o-mini" /></label> : null}
      {presetState.needsProject ? <label>Project<input value={form.project || ""} onChange={(event) => onChange("project", event.target.value)} placeholder="my-gcp-project" /></label> : null}
      {presetState.needsLocation ? <label>Location<input value={form.location || ""} onChange={(event) => onChange("location", event.target.value)} placeholder="us-central1" /></label> : null}
      {presetState.needsModelResource ? <label className="provider-form-wide">Model resource<input value={form.model_resource || ""} onChange={(event) => onChange("model_resource", event.target.value)} placeholder="publishers/google/models/gemini-2.5-flash" /></label> : null}
      <label>Model discovery<select value={form.model_discovery || "list_models"} onChange={(event) => onChange("model_discovery", event.target.value)}>{discoveryOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
      <label>Priority<input type="number" value={form.priority} onChange={(event) => onChange("priority", event.target.value)} /></label>
      <label>Weight<input type="number" step="0.1" value={form.weight} onChange={(event) => onChange("weight", event.target.value)} /></label>
      <label>Capacity<input type="number" step="0.1" value={form.capacity_hint} onChange={(event) => onChange("capacity_hint", event.target.value)} /></label>
      <CapabilitySelect form={form} name="responses" label="Responses API" onChange={onChange} />
      <CapabilitySelect form={form} name="chat_completions" label="Chat Completions" onChange={onChange} />
      <CapabilitySelect form={form} name="tool_calling" label="Tool calling" onChange={onChange} />
      <CapabilitySelect form={form} name="models" label="Models API" onChange={onChange} />
      {includeHeaders ? <label className="provider-form-wide">Headers<textarea value={form.headers_text} onChange={(event) => onChange("headers_text", event.target.value)} spellCheck={false} /></label> : null}
    </>
  );
}

const API_TYPE_OPTIONS = [
  { value: "chat_completions", label: "Chat Completions" },
  { value: "responses", label: "Responses" },
  { value: "responses_native", label: "Responses native" },
  { value: "messages", label: "Anthropic messages" },
  { value: "gemini_generate_content", label: "Gemini generateContent" },
];

const API_MODE_OPTIONS = [
  { value: "proxy", label: "Proxy" },
  { value: "record_only", label: "Record only" },
  { value: "server", label: "Server" },
  { value: "responses_server", label: "Responses server" },
];

function CapabilitySelect({ form, name, label, onChange }) {
  const capabilities = form.capabilities || {};
  const current = capabilities[name];
  const value = current === true ? "true" : current === false ? "false" : "";
  const setValue = (nextValue) => {
    const next = { ...capabilities };
    if (nextValue === "") {
      delete next[name];
    } else {
      next[name] = nextValue === "true";
    }
    onChange("capabilities", next);
  };
  return (
    <label>{label}<select value={value} onChange={(event) => setValue(event.target.value)}>{CAPABILITY_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
  );
}

const CAPABILITY_OPTIONS = [
  { value: "", label: "Auto / inherit" },
  { value: "true", label: "Supported" },
  { value: "false", label: "Unsupported" },
];

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
  const protocolFamily = spec.protocol_family || form.protocol_family || "";
  return {
    ...form,
    api_type: defaultAPITypeForProtocolFamily(protocolFamily),
    protocol_family: protocolFamily,
    routing_profile: spec.routing_profile || allowedProfiles[0] || form.routing_profile || "",
  };
}

function defaultAPITypeForProtocolFamily(protocolFamily) {
  switch (protocolFamily) {
    case "anthropic_messages":
      return "messages";
    case "google_genai":
    case "vertex_native":
      return "gemini_generate_content";
    case "openai_compatible":
    default:
      return "chat_completions";
  }
}

function uniqueSorted(values) {
  return Array.from(new Set(values.filter(Boolean))).sort();
}

function normalizeProviderPayload(form) {
  return {
    ...form,
    priority: Number(form.priority || 0),
    weight: Number(form.weight || 1),
    capacity_hint: Number(form.capacity_hint || 1),
    capabilities: normalizeCapabilities(form.capabilities),
  };
}

function normalizeCapabilities(value) {
  const capabilities = {};
  for (const key of ["responses", "chat_completions", "tool_calling", "models", "embeddings", "tokenize"]) {
    if (typeof value?.[key] === "boolean") {
      capabilities[key] = value[key];
    }
  }
  return capabilities;
}

function summarizeProviders(items) {
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
