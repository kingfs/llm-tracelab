import React, { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DeleteIcon, DetailMetaPill, EditIcon, HomeIcon, InlineTag, ProbeIcon } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { SingleUsageCharts } from "../components/common/Charts";
import { Switch } from "../components/common/Controls";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, deleteJSON, patchJSON, postJSON } from "../lib/api";
import { buildTraceLink, formatCount, formatDateTime, formatDuration, formatTime, MONITOR_WINDOW_OPTIONS, normalizeAnalyticsWindow, setOrDeleteParam } from "../lib/monitor";
import { buildPresetState, normalizePresetSelection, ProviderAdvancedFields } from "./ChannelsPage";

export function ProviderDetailPage() {
  const { providerID = "", channelID = "" } = useParams();
  const effectiveProviderID = providerID || channelID;
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const [refreshTick, setRefreshTick] = useState(0);
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState("");
  const [modelDraft, setModelDraft] = useState("");
  const [editOpen, setEditOpen] = useState(false);
  const [editForm, setEditForm] = useState(() => emptyEditForm());
  const [lastProbe, setLastProbe] = useState(null);
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const detail = useJSON(apiURL(apiPaths.provider(effectiveProviderID), params), [effectiveProviderID, windowValue, refreshTick]);
  const presets = useJSON(apiPaths.providerPresets, []);
  const provider = detail.data || {};
  const summary = provider.summary || {};
  const modelsUsage = sortProviderModels(provider.models_usage || []);
  const discoveredDisabledModels = modelsUsage.filter((model) => model.source === "discovered" && !model.enabled).map((model) => model.model);
  const failures = provider.recent_failures || [];
  const probeRuns = provider.recent_probe_runs || [];
  const trends = provider.trends || [];

  useEffect(() => {
    if (!detail.data) {
      return;
    }
    setEditForm(editFormFromProvider(detail.data));
  }, [detail.data]);

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "today" ? "" : nextWindow);
    setSearchParams(next);
  };
  const reload = () => setRefreshTick((tick) => tick + 1);
  const probe = async () => {
    setBusy("probe");
    setActionError("");
    try {
      const result = await postJSON(apiPaths.providerProbe(effectiveProviderID), { enable_discovered: false, detect_provider: true });
      setLastProbe(result);
      reload();
    } catch (err) {
      if (err.payload?.provider_probe) {
        setLastProbe(err.payload);
      }
      setActionError(formatProbeActionError(err));
      reload();
    } finally {
      setBusy("");
    }
  };
  const applyProbeSuggestions = async () => {
    const report = lastProbe?.provider_probe;
    if (!report) {
      return;
    }
    setBusy("apply-probe");
    setActionError("");
    try {
      await patchJSON(apiPaths.provider(effectiveProviderID), providerProbeSuggestionPayload(provider, report));
      setLastProbe(null);
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to apply provider probe suggestions.");
    } finally {
      setBusy("");
    }
  };
  const setProviderEnabled = async (enabled) => {
    setBusy("provider");
    setActionError("");
    try {
      await patchJSON(apiPaths.provider(effectiveProviderID), { enabled });
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to update provider.");
    } finally {
      setBusy("");
    }
  };
  const saveProvider = async () => {
    setBusy("save-provider");
    setActionError("");
    try {
      await patchJSON(apiPaths.provider(effectiveProviderID), providerPayloadFromForm(editForm));
      setEditOpen(false);
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to save provider.");
    } finally {
      setBusy("");
    }
  };
  const setModelEnabled = async (model, enabled) => {
    setBusy(model);
    setActionError("");
    try {
      await patchJSON(apiPaths.providerModel(effectiveProviderID, model), { enabled });
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to update model.");
    } finally {
      setBusy("");
    }
  };
  const deleteProvider = async () => {
    if (!window.confirm(`Delete provider ${provider.name || effectiveProviderID}? Configured models for this provider will also be removed.`)) {
      return;
    }
    setBusy("delete-provider");
    setActionError("");
    try {
      await deleteJSON(apiPaths.provider(effectiveProviderID));
      navigate("/providers");
    } catch (err) {
      setActionError(err.message || "Unable to delete provider.");
    } finally {
      setBusy("");
    }
  };
  const deleteModel = async (model) => {
    if (!window.confirm(`Delete model ${model} from this provider?`)) {
      return;
    }
    setBusy(`delete:${model}`);
    setActionError("");
    try {
      await deleteJSON(apiPaths.providerModel(effectiveProviderID, model));
      reload();
    } catch (err) {
      setActionError(err.message || "Unable to delete model.");
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
      await postJSON(apiPaths.providerModels(effectiveProviderID), { model, display_name: model, enabled: true });
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
      await patchJSON(apiPaths.providerModelsBatch(effectiveProviderID), { models, enabled });
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
            <h1>{provider.name || effectiveProviderID}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone={provider.enabled ? "green" : "default"}>{provider.enabled ? "enabled" : "disabled"}</InlineTag>
              <InlineTag tone={provider.source === "bootstrap" ? "gold" : "green"}>{providerSourceLabel(provider.source)}</InlineTag>
              <InlineTag tone="accent">{provider.provider_preset || "custom"}</InlineTag>
              {provider.secret_storage_mode ? <InlineTag tone={provider.secret_storage_mode === "plaintext-local" ? "gold" : "green"}>{provider.secret_storage_mode}</InlineTag> : null}
              {provider.last_probe_status ? <InlineTag tone={provider.last_probe_status === "success" ? "green" : "danger"}>{provider.last_probe_status}</InlineTag> : null}
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label="config source" value={providerSourceLabel(provider.source)} />
            <DetailMetaPill label="api type" value={provider.api_type || "-"} />
            <DetailMetaPill label="mode" value={provider.mode || "-"} />
            <DetailMetaPill label="base url" value={provider.base_url || "-"} mono />
            <DetailMetaPill label="models" value={`${formatCount(provider.enabled_model_count)} / ${formatCount(provider.model_count)}`} />
            <DetailMetaPill label="requests" value={formatCount(summary.request_count)} />
            <DetailMetaPill label="tokens" value={formatCount(summary.total_tokens)} />
            {summary.missing_usage_request ? <DetailMetaPill label="missing usage" value={formatCount(summary.missing_usage_request)} /> : null}
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to="/providers" title="Back to providers" aria-label="Back to providers">
              <HomeIcon />
            </Link>
            <button className="icon-button" type="button" onClick={probe} disabled={busy === "probe"} title="Probe provider" aria-label="Probe provider"><ProbeIcon /></button>
            <button className="icon-button" type="button" onClick={() => setEditOpen(true)} title="Edit provider" aria-label="Edit provider"><EditIcon /></button>
            <button className="icon-button" type="button" onClick={deleteProvider} disabled={busy === "delete-provider"} title="Delete provider" aria-label="Delete provider"><DeleteIcon /></button>
            <Switch checked={Boolean(provider.enabled)} onChange={setProviderEnabled} disabled={busy === "provider"} label="Provider enabled" />
          </div>
          <span className="badge">{detail.data ? formatTime(detail.data.updated_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Analytics</p>
            <h2>Provider usage</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label="Provider detail window">
              {MONITOR_WINDOW_OPTIONS.map((window) => (
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
          <StatCard label="Tokens" value={formatCount(summary.total_tokens)} detail={usageCoverageDetail(summary.missing_usage_request)} />
          <StatCard label="Success" value={`${Number(summary.success_rate || 0).toFixed(1)}%`} />
        </div>
      </section>

      {actionError ? <EmptyState title="Provider action failed" detail={actionError} tone="danger" /> : null}
      {detail.error ? <EmptyState title="Unable to load provider" detail={detail.error} tone="danger" /> : null}
      {detail.loading && !detail.data ? <EmptyState title="Loading provider" detail="Collecting provider configuration, models, and usage." /> : null}
      {detail.data?.secret_storage_mode === "plaintext-local" ? (
        <EmptyState title="Local plaintext secret storage" detail="API keys and secret headers are redacted in Monitor responses, but currently stored in the local SQLite database without encryption." tone="danger" />
      ) : null}
      {lastProbe?.provider_probe ? (
        <ProviderProbeSuggestionPanel
          report={lastProbe.provider_probe}
          busy={busy === "apply-probe"}
          onApply={applyProbeSuggestions}
        />
      ) : null}

      {detail.data && editOpen ? (
        <EditProviderDialog
          provider={provider}
          form={editForm}
          presetData={presets.data}
          saving={busy === "save-provider"}
          onChange={setEditForm}
          onReset={() => setEditForm(editFormFromProvider(provider))}
          onClose={() => setEditOpen(false)}
          onSave={saveProvider}
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
            <div className="provider-model-card-grid">
              {modelsUsage.length ? modelsUsage.map((model) => (
                <ProviderModelRow
                  key={model.model}
                  item={model}
                  busy={busy === model.model}
                  deleting={busy === `delete:${model.model}`}
                  onToggle={() => setModelEnabled(model.model, !model.enabled)}
                  onDelete={() => deleteModel(model.model)}
                />
              )) : <EmptyState title="No models" detail="Probe or manually configure models for this provider." compact />}
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
              <div className="provider-probe-list">
                {probeRuns.map((run) => <ProbeRunCard key={run.id} item={run} />)}
              </div>
            ) : (
              <EmptyState title="No probe runs" detail="Run a provider probe to record discovery status and troubleshooting context." />
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
                  <Link key={failure.trace_id} className="upstream-failure-card" to={buildTraceLink(failure.trace_id, "providers", "", "", "failure")}>
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
              <EmptyState title="No recent failures" detail="This provider has no failed trace in the selected window." />
            )}
          </section>
        </>
      ) : null}
    </div>
  );
}

function EditProviderDialog({ provider, form, presetData, saving, onChange, onReset, onClose, onSave }) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const presetState = buildPresetState(presetData, form.provider_preset, form.routing_profile);
  const updateForm = (key, value) => {
    onChange((current) => normalizePresetSelection({ ...current, [key]: value }, presetData, key));
  };
  const submit = async (event) => {
    event.preventDefault();
    await onSave();
  };

  return createPortal(
    <div className="nav-modal-backdrop" role="presentation">
      <form className="nav-modal provider-edit-modal" onSubmit={submit}>
        <div className="nav-modal-head">
          <div>
            <p className="eyebrow">Configuration</p>
            <h2>Edit provider</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close">x</button>
        </div>
        <div className="provider-form provider-form-modal">
          <label>Name<input required value={form.name} onChange={(event) => updateForm("name", event.target.value)} /></label>
          <label>Provider preset<select value={form.provider_preset} onChange={(event) => updateForm("provider_preset", event.target.value)}>{presetState.options.map((preset) => <option key={preset} value={preset}>{preset}</option>)}</select></label>
          <label className="provider-form-wide">Base URL<input required value={form.base_url} onChange={(event) => updateForm("base_url", event.target.value)} /></label>
          <label className="provider-form-wide">API key<input type="password" value={form.api_key} onChange={(event) => updateForm("api_key", event.target.value)} placeholder={provider.api_key_hint ? `keep ${provider.api_key_hint}` : "unchanged"} /></label>
          <label className="provider-form-check provider-form-wide"><input type="checkbox" checked={form.allow_unknown_models} onChange={(event) => updateForm("allow_unknown_models", event.target.checked)} /> Allow unknown models</label>
        </div>
        <button className="ghost-button" type="button" onClick={() => setAdvancedOpen((open) => !open)}>{advancedOpen ? "Hide advanced" : "Advanced options"}</button>
        {advancedOpen ? (
          <div className="provider-form provider-form-modal">
            <ProviderAdvancedFields form={form} presetState={presetState} onChange={updateForm} includeHeaders />
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
    <div className={failed ? "provider-probe-card provider-probe-card-failed" : "provider-probe-card"}>
      <div className="provider-probe-card-head">
        <div className="trace-tag-group">
          <InlineTag tone={failed ? "danger" : "green"}>{item.status || "unknown"}</InlineTag>
          {item.failure_reason ? <InlineTag tone="accent">{item.failure_reason}</InlineTag> : null}
          {item.status_code ? <InlineTag>{item.status_code}</InlineTag> : null}
        </div>
        <span>{formatDateTime(item.completed_at || item.started_at)}</span>
      </div>
      <div className="provider-probe-meta">
        <span>{formatCount(item.discovered_count)} discovered</span>
        <span>{formatCount(item.enabled_count)} enabled</span>
        <span>{formatDuration(item.duration_ms)}</span>
      </div>
      {item.endpoint ? <div className="provider-probe-endpoint">{item.endpoint}</div> : null}
      {item.error_text ? <div className="upstream-failure-detail">{item.error_text}</div> : null}
      {item.retry_hint ? <div className="provider-probe-hint">{item.retry_hint}</div> : null}
    </div>
  );
}

function ProviderProbeSuggestionPanel({ report, busy, onApply }) {
  const capabilities = Array.isArray(report.capabilities) ? report.capabilities : [];
  const warnings = Array.isArray(report.warnings) ? report.warnings : [];
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Provider detection</p>
          <h2>Probe suggestions</h2>
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
        <button className="ghost-button active" type="button" onClick={onApply} disabled={busy || report.status !== "detected"}>{busy ? "Applying" : "Apply suggestions"}</button>
      </div>
    </section>
  );
}

function ProviderModelRow({ item, busy, deleting, onToggle, onDelete }) {
  const summary = item.summary || {};
  const isDiscoveredDisabled = item.source === "discovered" && !item.enabled;
  const canDelete = item.source !== "trace";
  return (
    <div className="provider-model-card">
      <div className="provider-model-card-head">
        <div>
          <strong>{item.model}</strong>
          <span>{isDiscoveredDisabled ? "discovered, awaiting enable" : modelSourceLabel(item.source)}</span>
        </div>
        <div className="action-group">
          <Switch checked={Boolean(item.enabled)} onChange={onToggle} disabled={busy} label={`${item.model} enabled`} />
          {canDelete ? (
            <button className="icon-button" type="button" onClick={onDelete} disabled={deleting} title="Delete model" aria-label={`Delete ${item.model}`}>
              <DeleteIcon />
            </button>
          ) : null}
        </div>
      </div>
      <div className="trace-tag-group">
        <InlineTag tone={item.enabled ? "green" : "default"}>{item.enabled ? "enabled" : "disabled"}</InlineTag>
        {isDiscoveredDisabled ? <InlineTag tone="gold">new</InlineTag> : null}
      </div>
      <div className="model-market-metrics model-market-metrics-compact">
        <Metric label="req" value={formatCount(summary.request_count)} />
        <Metric label="err" value={formatCount(summary.failed_request)} danger={Number(summary.failed_request || 0) > 0} />
        <Metric label="tok" value={formatCount(summary.total_tokens)} detail={usageCoverageDetail(summary.missing_usage_request)} />
      </div>
    </div>
  );
}

function sortProviderModels(items) {
  return items.slice().sort((left, right) => {
    if (Boolean(left.enabled) !== Boolean(right.enabled)) {
      return left.enabled ? -1 : 1;
    }
    const leftRequests = Number(left.summary?.request_count || 0);
    const rightRequests = Number(right.summary?.request_count || 0);
    if (leftRequests !== rightRequests) {
      return rightRequests - leftRequests;
    }
    return String(left.model || "").localeCompare(String(right.model || ""));
  });
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

function modelSourceLabel(source) {
  switch (source) {
    case "manual":
      return "manual";
    case "static":
      return "bootstrap static";
    case "discovered":
      return "probe discovered";
    case "trace":
      return "seen in trace";
    default:
      return source || "unknown";
  }
}

function Metric({ label, value, detail = "", danger = false }) {
  return (
    <span className={danger ? "model-market-metric model-market-metric-danger" : "model-market-metric"}>
      <span>{label}</span>
      <strong>{value}</strong>
      {detail ? <small>{detail}</small> : null}
    </span>
  );
}

function usageCoverageDetail(missing) {
  const count = Number(missing || 0);
  return count > 0 ? `${formatCount(count)} missing usage` : "";
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
    api_type: "chat_completions",
    mode: "proxy",
    capabilities: {},
    protocol_family: "",
    routing_profile: "",
    api_version: "",
    deployment: "",
    project: "",
    location: "",
    model_resource: "",
    api_key: "",
    priority: 0,
    weight: 1,
    capacity_hint: 1,
    model_discovery: "list_models",
    allow_unknown_models: false,
    headers_text: "",
  };
}

function editFormFromProvider(provider = {}) {
  const headers = provider.headers || {};
  return {
    name: provider.name || "",
    base_url: provider.base_url || "",
    provider_preset: provider.provider_preset || "",
    api_type: provider.api_type || "chat_completions",
    mode: provider.mode || "proxy",
    capabilities: provider.capabilities || {},
    protocol_family: provider.protocol_family || "",
    routing_profile: provider.routing_profile || "",
    api_version: provider.api_version || "",
    deployment: provider.deployment || "",
    project: provider.project || "",
    location: provider.location || "",
    model_resource: provider.model_resource || "",
    api_key: "",
    priority: provider.priority ?? 0,
    weight: provider.weight ?? 1,
    capacity_hint: provider.capacity_hint ?? 1,
    model_discovery: provider.model_discovery || "list_models",
    allow_unknown_models: Boolean(provider.allow_unknown_models),
    headers_text: Object.keys(headers).sort().map((key) => `${key}: ${headers[key]}`).join("\n"),
  };
}

function providerPayloadFromForm(form) {
  const payload = {
    name: form.name,
    base_url: form.base_url,
    provider_preset: form.provider_preset,
    api_type: form.api_type,
    mode: form.mode,
    capabilities: normalizeCapabilities(form.capabilities),
    protocol_family: form.protocol_family,
    routing_profile: form.routing_profile,
    api_version: form.api_version,
    deployment: form.deployment,
    project: form.project,
    location: form.location,
    model_resource: form.model_resource,
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

function providerProbeSuggestionPayload(provider = {}, report = {}) {
  const payload = {};
  if (report.suggested_api_type) {
    payload.api_type = report.suggested_api_type;
  }
  if (report.suggested_protocol_family) {
    payload.protocol_family = report.suggested_protocol_family;
  }
  const capabilities = { ...(provider.capabilities || {}) };
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

function normalizeCapabilities(value) {
  const capabilities = {};
  for (const key of ["responses", "chat_completions", "tool_calling", "models", "embeddings", "tokenize"]) {
    if (typeof value?.[key] === "boolean") {
      capabilities[key] = value[key];
    }
  }
  return capabilities;
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
