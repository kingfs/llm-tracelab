import React, { useMemo, useRef, useState } from "react";
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

      <ProviderProbeBatchPanel providers={items} onApplied={() => setRefreshTick((tick) => tick + 1)} />

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

function ProviderProbeBatchPanel({ providers, onApplied }) {
  const [report, setReport] = useState(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [applyResult, setApplyResult] = useState(null);
  const providerMap = useMemo(() => new Map((providers || []).map((item) => [item.id, item])), [providers]);
  const summary = useMemo(() => summarizeProbeBatchReport(report, providerMap), [report, providerMap]);

  const previewReport = async () => {
    setBusy("preview");
    setError("");
    setApplyResult(null);
    try {
      const nextReport = await postJSON(apiPaths.providerProbeReport, {});
      setReport(nextReport);
    } catch (err) {
      setError(err.message || "Unable to preview provider probe report.");
    } finally {
      setBusy("");
    }
  };

  const applyDetected = async () => {
    if (!summary.applyable.length) {
      return;
    }
    setBusy("apply");
    setError("");
    try {
      const result = await postJSON(apiPaths.providerProbeApply, providerProbeBatchApplyPayload());
      setApplyResult(result);
      onApplied?.();
    } catch (err) {
      setError(err.message || "Unable to apply provider probe suggestions.");
    } finally {
      setBusy("");
    }
  };

  return (
    <section className="panel provider-batch-probe-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Provider detection</p>
          <h2>Batch probe and apply</h2>
        </div>
        <div className="panel-head-actions">
          <button className="ghost-button" type="button" onClick={previewReport} disabled={busy === "preview" || !providers.length}>{busy === "preview" ? "Previewing" : "Preview batch probe"}</button>
          <button className="ghost-button active" type="button" onClick={applyDetected} disabled={busy === "apply" || !summary.applyable.length}>{busy === "apply" ? "Applying" : "Apply detected suggestions"}</button>
        </div>
      </div>
      <p className="trace-subline">Preview runs a read-only provider probe report first. Apply only fills missing API type, missing protocol family, and capabilities that are not set; explicit configuration and false capabilities are preserved.</p>
      <div className="detail-meta-strip">
        <Metric label="detected" value={formatCount(summary.detected)} />
        <Metric label="unknown" value={formatCount(summary.unknown)} />
        <Metric label="errors" value={formatCount(summary.errors)} />
        <Metric label="applyable" value={formatCount(summary.applyable.length)} />
      </div>
      {report ? (
        <div className="provider-batch-report-list">
          {summary.rows.length ? summary.rows.map((row) => <ProviderProbeBatchRow key={row.providerID || row.baseURL} row={row} />) : <EmptyState title="No probe targets" detail="No enabled provider with a base URL was returned by the report." compact />}
        </div>
      ) : null}
      {applyResult ? <p className="trace-subline">Apply request accepted: {formatProviderProbeApplyResult(applyResult)}</p> : null}
      {error ? <EmptyState title="Provider batch probe failed" detail={error} tone="danger" compact /> : null}
    </section>
  );
}

function ProviderProbeBatchRow({ row }) {
  return (
    <div className={row.status === "error" ? "provider-probe-card provider-probe-card-failed" : "provider-probe-card"}>
      <div className="provider-probe-card-head">
        <div>
          <p className="eyebrow">{row.providerID || "provider"}</p>
          <h3>{row.providerName || row.providerID || row.baseURL || "Unnamed provider"}</h3>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={row.status === "detected" ? "green" : row.status === "error" ? "danger" : "gold"}>{row.status || "unknown"}</InlineTag>
          {row.fillableCount ? <InlineTag tone="accent">{row.fillableCount} fillable</InlineTag> : null}
        </div>
      </div>
      <div className="detail-meta-strip">
        <Metric label="api type" value={row.suggestedAPIType || "-"} detail={row.apiTypeFillable ? "missing" : row.currentAPIType ? "set" : ""} />
        <Metric label="protocol" value={row.suggestedProtocolFamily || "-"} detail={row.protocolFillable ? "missing" : row.currentProtocolFamily ? "set" : ""} />
        <Metric label="capabilities" value={row.capabilities.length ? row.capabilities.join(", ") : "-"} detail={row.fillableCapabilities.length ? `${row.fillableCapabilities.length} unset` : ""} />
      </div>
      {row.warnings.length ? <p className="trace-subline">{row.warnings.join(" · ")}</p> : null}
      {!row.fillableCount && row.status === "detected" ? <p className="trace-subline">Detected suggestions are already explicit or protected by capability false.</p> : null}
    </div>
  );
}

function CreateProviderDialog({ presetData, onClose, onCreated }) {
  const [form, setForm] = useState(DEFAULT_FORM);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [validating, setValidating] = useState(false);
  const [probeReport, setProbeReport] = useState(null);
  const [setupResult, setSetupResult] = useState(null);
  const [validatedSignature, setValidatedSignature] = useState("");
  const [error, setError] = useState("");
  const formVersion = useRef(0);
  const presetState = buildPresetState(presetData, form.provider_preset, form.routing_profile);
  const currentSetupSignature = setupValidationSignature(form);
  const setupStale = Boolean(setupResult && validatedSignature !== currentSetupSignature);
  const setupStatus = buildSetupStatus(setupResult, setupStale, form);
  const updateForm = (key, value, options = {}) => {
    formVersion.current += 1;
    setForm((current) => normalizePresetSelection({ ...current, [key]: value }, presetData, key));
    if (!options.keepSetupResult && SETUP_VALIDATION_FIELDS.has(key)) {
      setSetupResult(null);
      setValidatedSignature("");
    }
  };
  const detectProvider = async () => {
    setDetecting(true);
    setError("");
    try {
      const report = await postJSON(apiPaths.providerProbePreview, providerProbePreviewPayload(form));
      setProbeReport(report);
      setAdvancedOpen(true);
    } catch (err) {
      if (err.payload?.status) {
        setProbeReport(err.payload);
      }
      setError(err.message || "Unable to detect provider.");
    } finally {
      setDetecting(false);
    }
  };
  const applyProbeSuggestions = () => {
    formVersion.current += 1;
    setForm((current) => ({
      ...current,
      ...providerProbeSuggestionPayload(current, probeReport),
    }));
    setSetupResult(null);
    setValidatedSignature("");
  };
  const applySetupConfig = (normalized = {}) => {
    setForm((current) => {
      const next = {
        ...current,
        ...providerConfigFormPatch(normalized),
        api_key: current.api_key,
      };
      setValidatedSignature(setupValidationSignature(next));
      return next;
    });
  };
  const validateSetup = async () => {
    setValidating(true);
    setError("");
    setSetupResult(null);
    setValidatedSignature("");
    const validationVersion = formVersion.current;
    try {
      const result = await postJSON(apiPaths.providerSetupValidate, normalizeProviderPayload(form));
      if (validationVersion !== formVersion.current) {
        setError("Configuration changed during validation. Run Validate setup again.");
        return;
      }
      setSetupResult(result);
      setProbeReport(result.probe || null);
      applySetupConfig(result.normalized_config);
      setAdvancedOpen(true);
    } catch (err) {
      if (validationVersion !== formVersion.current) {
        setError("Configuration changed during validation. Run Validate setup again.");
        return;
      }
      if (err.payload?.normalized_config) {
        setSetupResult(err.payload);
        setProbeReport(err.payload.probe || null);
        applySetupConfig(err.payload.normalized_config);
        setAdvancedOpen(true);
      }
      setError(err.message || "Unable to validate provider setup.");
    } finally {
      setValidating(false);
    }
  };

  const submit = async (event) => {
    event.preventDefault();
    if (!setupStatus.canApply) {
      setError(setupStatus.reason);
      return;
    }
    setSaving(true);
    setError("");
    try {
      await postJSON(apiPaths.providerSetupApply, normalizeProviderPayload(form));
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
        <div className="provider-form-actions">
          <button className="ghost-button" type="button" onClick={detectProvider} disabled={detecting || !form.base_url.trim()}>{detecting ? "Detecting" : "Detect provider"}</button>
          <button className="ghost-button active" type="button" onClick={validateSetup} disabled={validating || !form.base_url.trim()}>{validating ? "Validating" : "Validate setup"}</button>
          <button className="ghost-button" type="button" onClick={() => setAdvancedOpen((open) => !open)}>{advancedOpen ? "Hide advanced" : "Advanced options"}</button>
        </div>
        {setupResult ? <ProviderSetupStatusPanel result={setupResult} status={setupStatus} /> : <p className="trace-subline">Validate setup before creating the provider.</p>}
        {probeReport ? <ProviderProbeSuggestionPanel report={probeReport} onApply={applyProbeSuggestions} /> : null}
        {advancedOpen ? (
          <div className="provider-form provider-form-modal">
            <ProviderAdvancedFields form={form} presetState={presetState} onChange={updateForm} includeHeaders={false} />
          </div>
        ) : null}
        {error ? <p className="auth-error">{error}</p> : null}
        <div className="nav-modal-actions">
          <button className="ghost-button" type="button" onClick={onClose}>Cancel</button>
          <button className="ghost-button active" type="submit" disabled={saving || !setupStatus.canApply}>{saving ? "Creating" : "Create provider"}</button>
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

function ProviderSetupStatusPanel({ result, status }) {
  const normalized = result?.normalized_config || {};
  const probe = result?.probe || {};
  const warnings = Array.isArray(probe.warnings) ? probe.warnings : [];
  const secret = result?.secret || {};
  return (
    <div className="provider-probe-card">
      <div className="provider-probe-card-head">
        <div>
          <p className="eyebrow">Setup validation</p>
          <h3>{status.title}</h3>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={status.tone}>{status.label}</InlineTag>
          {probe.status ? <InlineTag tone={probe.status === "detected" ? "green" : probe.status === "error" ? "danger" : "gold"}>{probe.status}</InlineTag> : null}
        </div>
      </div>
      <div className="detail-meta-strip">
        <Metric label="api type" value={normalized.api_type || "-"} />
        <Metric label="protocol" value={normalized.protocol_family || "-"} />
        <Metric label="secret" value={secret.api_key_hint ? `stored as ${secret.api_key_hint}` : secret.secret_storage_mode || "-"} />
      </div>
      <p className="trace-subline">{status.reason}</p>
      {warnings.length ? <p className="trace-subline">{warnings.join(" · ")}</p> : null}
    </div>
  );
}

function ProviderProbeSuggestionPanel({ report, onApply }) {
  const capabilities = Array.isArray(report.capabilities) ? report.capabilities : [];
  const warnings = Array.isArray(report.warnings) ? report.warnings : [];
  return (
    <div className="provider-probe-card">
      <div className="provider-probe-card-head">
        <div>
          <p className="eyebrow">Provider detection</p>
          <h3>Probe suggestions</h3>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={report.status === "detected" ? "green" : report.status === "error" ? "danger" : "gold"}>{report.status || "unknown"}</InlineTag>
          {report.confidence ? <InlineTag tone="accent">{Math.round(Number(report.confidence) * 100)}%</InlineTag> : null}
        </div>
      </div>
      <div className="detail-meta-strip">
        <Metric label="api type" value={report.suggested_api_type || "-"} />
        <Metric label="protocol" value={report.suggested_protocol_family || "-"} />
        <Metric label="capabilities" value={capabilities.length ? capabilities.join(", ") : "-"} />
      </div>
      {warnings.length ? <p className="trace-subline">{warnings.join(" · ")}</p> : null}
      <div className="provider-form-actions">
        <button className="ghost-button active" type="button" onClick={onApply} disabled={report.status !== "detected"}>Apply suggestions</button>
      </div>
    </div>
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

const SETUP_VALIDATION_FIELDS = new Set([
  "name",
  "base_url",
  "provider_preset",
  "api_type",
  "mode",
  "capabilities",
  "protocol_family",
  "routing_profile",
  "api_version",
  "deployment",
  "project",
  "location",
  "model_resource",
  "api_key",
  "enabled",
  "priority",
  "weight",
  "capacity_hint",
  "model_discovery",
  "allow_unknown_models",
]);

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

function setupValidationSignature(form) {
  const payload = normalizeProviderPayload(form);
  const apiKey = payload.api_key || "";
  delete payload.api_key;
  payload.api_key_present = apiKey.trim() !== "";
  payload.api_key_length = apiKey.length;
  return JSON.stringify(payload);
}

function buildSetupStatus(result, stale, form) {
  if (!result) {
    return {
      canApply: false,
      title: "Not validated",
      label: "required",
      tone: "gold",
      reason: "Validate setup before creating the provider.",
    };
  }
  if (stale) {
    return {
      canApply: false,
      title: "Validation is stale",
      label: "stale",
      tone: "gold",
      reason: "Configuration changed after validation. Run Validate setup again.",
    };
  }
  const detected = result.probe?.status === "detected";
  const normalized = result.normalized_config || {};
  const explicitSurface = Boolean((form.api_type || normalized.api_type || "").trim() && (form.protocol_family || normalized.protocol_family || "").trim());
  if (detected) {
    return {
      canApply: true,
      title: "Ready to create",
      label: "detected",
      tone: "green",
      reason: "Provider probe detected a compatible setup.",
    };
  }
  if (explicitSurface) {
    return {
      canApply: true,
      title: "Ready with explicit protocol",
      label: "explicit",
      tone: "gold",
      reason: "Probe did not detect the provider, but API type and protocol family are explicitly set.",
    };
  }
  return {
    canApply: false,
    title: "Needs protocol selection",
    label: "blocked",
    tone: "danger",
    reason: "Probe did not detect the provider. Select both API type and protocol family in advanced options, then validate again.",
  };
}

function providerConfigFormPatch(config = {}) {
  const patch = {};
  for (const key of [
    "name",
    "base_url",
    "provider_preset",
    "api_type",
    "mode",
    "capabilities",
    "protocol_family",
    "routing_profile",
    "api_version",
    "deployment",
    "project",
    "location",
    "model_resource",
    "enabled",
    "priority",
    "weight",
    "capacity_hint",
    "model_discovery",
    "allow_unknown_models",
  ]) {
    if (config[key] !== undefined && config[key] !== null) {
      patch[key] = config[key];
    }
  }
  return patch;
}

function providerProbePreviewPayload(form) {
  return {
    provider_id: form.name || form.provider_preset || "new-provider",
    base_url: form.base_url,
    api_key: form.api_key,
    api_type: form.api_type,
    protocol_family: form.protocol_family,
  };
}

function providerProbeSuggestionPayload(form = {}, report = {}) {
  const payload = {};
  if (report.suggested_api_type) {
    payload.api_type = report.suggested_api_type;
  }
  if (report.suggested_protocol_family) {
    payload.protocol_family = report.suggested_protocol_family;
  }
  const capabilities = { ...(form.capabilities || {}) };
  for (const capability of report.capabilities || []) {
    switch (capability) {
      case "responses":
        capabilities.responses = true;
        break;
      case "chat_completions":
        capabilities.chat_completions = true;
        break;
      case "tool_calling":
        capabilities.tool_calling = true;
        break;
      case "models":
        capabilities.models = true;
        break;
      case "embeddings":
        capabilities.embeddings = true;
        break;
      case "tokenize":
        capabilities.tokenize = true;
        break;
      default:
        break;
    }
  }
  payload.capabilities = normalizeCapabilities(capabilities);
  return payload;
}

function providerProbeBatchApplyPayload(channelID = "") {
  return channelID ? { channel_id: channelID } : {};
}

function summarizeProbeBatchReport(report, providerMap) {
  const rows = (Array.isArray(report?.reports) ? report.reports : []).map((item) => buildProbeBatchRow(item, providerMap));
  return rows.reduce(
    (state, row) => {
      if (row.status === "detected") {
        state.detected += 1;
      } else if (row.status === "error") {
        state.errors += 1;
      } else {
        state.unknown += 1;
      }
      if (row.status === "detected" && row.fillableCount > 0) {
        state.applyable.push(row);
      }
      state.rows.push(row);
      return state;
    },
    { rows: [], detected: 0, unknown: 0, errors: 0, applyable: [] },
  );
}

function buildProbeBatchRow(report, providerMap) {
  const providerID = report.provider_id || "";
  const provider = providerMap.get(providerID) || {};
  const capabilities = Array.isArray(report.capabilities) ? report.capabilities : [];
  const currentCapabilities = normalizeCapabilities(provider.capabilities || {});
  const fillableCapabilities = capabilities.filter((capability) => BATCH_APPLY_CAPABILITIES.has(capability) && currentCapabilities[capability] === undefined);
  const currentAPIType = provider.api_type || report.specified_api_type || "";
  const currentProtocolFamily = provider.protocol_family || report.specified_protocol_family || "";
  const suggestedAPIType = report.suggested_api_type || "";
  const suggestedProtocolFamily = report.suggested_protocol_family || "";
  const apiTypeFillable = Boolean(suggestedAPIType && !currentAPIType);
  const protocolFillable = Boolean(suggestedProtocolFamily && !currentProtocolFamily);
  const fillableCount = Number(apiTypeFillable) + Number(protocolFillable) + fillableCapabilities.length;
  return {
    providerID,
    providerName: provider.name || "",
    baseURL: report.base_url || provider.base_url || "",
    status: report.status || "unknown",
    warnings: Array.isArray(report.warnings) ? report.warnings : [],
    capabilities,
    fillableCapabilities,
    fillableCount,
    currentAPIType,
    currentProtocolFamily,
    suggestedAPIType,
    suggestedProtocolFamily,
    apiTypeFillable,
    protocolFillable,
  };
}

const BATCH_APPLY_CAPABILITIES = new Set(["responses", "chat_completions", "tool_calling", "models", "embeddings", "tokenize"]);

function formatProviderProbeApplyResult(result = {}) {
  const applied = Array.isArray(result.applied) ? result.applied : [];
  if (applied.length) {
    const updated = applied.filter((item) => item.applied).length;
    return `${formatCount(updated)} applied, ${formatCount(applied.length - updated)} skipped`;
  }
  if (result.status) {
    return result.status;
  }
  return "backend response received";
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
