import React, { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { StatCard } from "../components/common/Display";
import { DetailMetaPill, HomeIcon, InlineTag } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { SingleUsageCharts } from "../components/common/Charts";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, patchJSON } from "../lib/api";
import { useI18n } from "../lib/i18n";
import {
  buildChannelLink,
  formatCount,
  formatDateTime,
  formatTime,
  MONITOR_WINDOW_OPTIONS,
  normalizeAnalyticsWindow,
  setOrDeleteParam,
} from "../lib/monitor";

export function ModelDetailPage() {
  const { model = "" } = useParams();
  const { language, t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const windowValue = normalizeAnalyticsWindow(searchParams.get("window"));
  const params = new URLSearchParams();
  params.set("window", windowValue);
  const detail = useJSON(apiURL(apiPaths.model(model), params), [model, windowValue]);
  const spec = useJSON(apiPaths.modelSpecLookup(model), [model]);
  const modelItem = detail.data?.model || {};
  const summary = modelItem.summary || {};
  const trends = detail.data?.trends || [];
  const channels = detail.data?.channels || [];

  const setWindow = (nextWindow) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "window", nextWindow === "today" ? "" : nextWindow);
    setSearchParams(next);
  };

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{modelItem.display_name || model}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone="accent">{t("models.enabled", { count: formatCount(modelItem.enabled_channel_count || 0) })}</InlineTag>
              <InlineTag>{t("models.channels", { count: formatCount(modelItem.channel_count || 0) })}</InlineTag>
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label={t("common.requests")} value={formatCount(summary.request_count)} />
            <DetailMetaPill label={t("common.tokens")} value={formatCount(summary.total_tokens)} />
            {summary.missing_usage_request ? <DetailMetaPill label={t("models.missingUsageShort")} value={formatCount(summary.missing_usage_request)} /> : null}
            <DetailMetaPill label={t("common.failed")} value={formatCount(summary.failed_request)} />
            <DetailMetaPill label={t("models.lastSeen")} value={formatDateTime(summary.last_seen)} />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to="/models" title={t("models.back")} aria-label={t("models.back")}>
              <HomeIcon />
            </Link>
          </div>
          <span className="badge">{detail.data?.refreshed_at ? formatTime(detail.data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Analytics</p>
            <h2>Usage window</h2>
          </div>
          <div className="panel-head-actions">
            <div className="view-toggle" role="tablist" aria-label={t("models.window")}>
              {MONITOR_WINDOW_OPTIONS.map((window) => (
                <button key={window} className={windowValue === window ? "ghost-button active" : "ghost-button"} onClick={() => setWindow(window)}>
                  {window}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="hero-grid hero-grid-compact">
          <StatCard label={t("common.requests")} value={formatCount(summary.request_count)} />
          <StatCard label={t("common.errors")} value={formatCount(summary.failed_request)} accent={summary.failed_request ? "accent-red" : ""} />
          <StatCard label={t("common.tokens")} value={formatCount(summary.total_tokens)} detail={usageCoverageDetail(summary.missing_usage_request, t)} />
          <StatCard label={t("models.today")} value={formatCount(modelItem.today?.total_tokens)} detail={usageCoverageDetail(modelItem.today?.missing_usage_request, t)} />
        </div>
      </section>

      {detail.error ? <EmptyState title={t("models.detailLoadError")} detail={detail.error} tone="danger" /> : null}
      {detail.loading && !detail.data ? <EmptyState title={t("models.detailLoading")} detail={t("models.detailLoadingDetail")} /> : null}

      {detail.data ? (
        <>
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Trend</p>
                <h2>{t("models.requestsTokens")}</h2>
              </div>
            </div>
            <SingleUsageCharts items={trends} />
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Channels</p>
                <h2>{t("models.providerCoverage")}</h2>
              </div>
            </div>
            <div className="channel-model-table">
              {channels.length ? channels.map((channel) => <ModelChannelRow key={channel.channel_id} item={channel} windowValue={windowValue} t={t} />) : <EmptyState title={t("models.noChannels")} detail={t("models.noChannelsDetail")} compact />}
            </div>
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Codex</p>
                <h2>{t("models.codexConfig")}</h2>
              </div>
              {spec.data?.suggestion ? <InlineTag tone={spec.data.matched ? "green" : "accent"}>{spec.data.matched ? t("models.specMatched") : t("models.specCandidate")}</InlineTag> : null}
            </div>
            {spec.error ? <EmptyState title={t("models.specLoadError")} detail={spec.error} tone="danger" compact /> : null}
            <div className="model-config-grid">
              {channels.length ? channels.map((channel) => (
                <ModelConfigCard key={`${channel.channel_id}-config`} item={channel} model={model} suggestion={spec.data?.suggestion} language={language} t={t} />
              )) : <EmptyState title={t("models.noConfigTargets")} detail={t("models.noChannelsDetail")} compact />}
            </div>
          </section>
        </>
      ) : null}
    </div>
  );
}

function ModelChannelRow({ item, windowValue, t }) {
  const summary = item.summary || {};
  return (
    <Link className="channel-model-row" to={buildChannelLink(item.channel_id, windowValue)}>
      <div>
        <strong>{item.channel_id}</strong>
        <span>{item.source || "unknown"}</span>
      </div>
      <InlineTag tone={item.enabled ? "green" : "default"}>{item.enabled ? t("overview.enabled") : t("overview.disabled")}</InlineTag>
      <span>{t("models.reqShort", { count: formatCount(summary.request_count) })}</span>
      <span>{t("models.errShort", { count: formatCount(summary.failed_request) })}</span>
      <span>{t("models.tokShort", { count: formatCount(summary.total_tokens) })}{summary.missing_usage_request ? ` · ${usageCoverageDetail(summary.missing_usage_request, t)}` : ""}</span>
    </Link>
  );
}

function ModelConfigCard({ item, model, suggestion, language, t }) {
  const [form, setForm] = useState(() => modelConfigFormFromItem(item));
  const [status, setStatus] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setForm(modelConfigFormFromItem(item));
  }, [item.channel_id, item.context_window, item.max_output_tokens, item.compact_history_item_threshold, item.upstream_model, item.profile_adoption_status, item.supports_responses, item.supports_chat_completions, item.supports_embeddings]);

  const update = (key, value) => {
    setForm((current) => ({ ...current, [key]: value }));
    setStatus("");
  };

  const applySuggestion = () => {
    if (!suggestion) {
      return;
    }
    setForm((current) => ({
      ...current,
      display_name: current.display_name || suggestion.name || suggestion.id || model,
      context_window: suggestion.context_window ? String(suggestion.context_window) : current.context_window,
      max_output_tokens: suggestion.max_output_tokens ? String(suggestion.max_output_tokens) : current.max_output_tokens,
      compact_history_item_threshold: current.compact_history_item_threshold || "20",
      supports_chat_completions: suggestion.supports_chat_completions ? "on" : "off",
      supports_embeddings: suggestion.supports_embeddings ? "on" : "off",
      profile_source: "go-llm-specs",
      profile_adoption_status: current.profile_adoption_status || "adopted",
    }));
    setStatus(t("models.specApplied"));
  };

  const save = async (event) => {
    event.preventDefault();
    setSaving(true);
    setStatus("");
    try {
      const payload = modelConfigPayload(form);
      const updated = await patchJSON(apiPaths.channelModel(item.channel_id, model), payload);
      setForm(modelConfigFormFromItem({ ...item, ...updated }));
      setStatus(t("models.saved"));
    } catch (error) {
      setStatus(error.message || t("models.saveFailed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form className="model-config-card" onSubmit={save}>
      <div className="model-config-card-head">
        <div>
          <strong>{item.channel_id}</strong>
          <span>{item.source || t("providers.providerFallback")}</span>
        </div>
        <div className="action-group">
          <button className="ghost-button" type="button" onClick={applySuggestion} disabled={!suggestion}>{t("models.applySpec")}</button>
          <button className="ghost-button active" type="submit" disabled={saving}>{saving ? t("common.saving") : t("common.save")}</button>
        </div>
      </div>
      {suggestion ? (
        <div className="model-spec-summary">
          <strong>{suggestion.name || suggestion.id}</strong>
          <span>{language === "zh-CN" ? suggestion.description_cn || suggestion.summary : suggestion.description || suggestion.summary}</span>
        </div>
      ) : null}
      <div className="model-config-fields">
        <label>
          <span>{t("models.displayName")}</span>
          <input value={form.display_name} onChange={(event) => update("display_name", event.target.value)} />
        </label>
        <label>
          <span>{t("models.upstreamModel")}</span>
          <input value={form.upstream_model} onChange={(event) => update("upstream_model", event.target.value)} placeholder={model} />
        </label>
        <label>
          <span>{t("models.contextWindow")}</span>
          <input type="number" min="0" value={form.context_window} onChange={(event) => update("context_window", event.target.value)} />
        </label>
        <label>
          <span>{t("models.maxOutput")}</span>
          <input type="number" min="0" value={form.max_output_tokens} onChange={(event) => update("max_output_tokens", event.target.value)} />
        </label>
        <label>
          <span>{t("models.compactThreshold")}</span>
          <input type="number" min="0" value={form.compact_history_item_threshold} onChange={(event) => update("compact_history_item_threshold", event.target.value)} />
        </label>
        <label>
          <span>{t("models.adoption")}</span>
          <select value={form.profile_adoption_status} onChange={(event) => update("profile_adoption_status", event.target.value)}>
            <option value="">{t("models.reportOnly")}</option>
            <option value="adopted">{t("models.adopted")}</option>
          </select>
        </label>
      </div>
      <div className="model-capability-toggles">
        <label><input type="checkbox" checked={form.enabled} onChange={(event) => update("enabled", event.target.checked)} />{t("overview.enabled")}</label>
        <label>
          <span>Responses</span>
          <select value={form.supports_responses} onChange={(event) => update("supports_responses", event.target.value)}>
            <option value="">{t("models.capabilityInherit")}</option>
            <option value="on">{t("models.capabilitySupported")}</option>
            <option value="off">{t("models.capabilityUnsupported")}</option>
          </select>
        </label>
        <label>
          <span>Chat Completions</span>
          <select value={form.supports_chat_completions} onChange={(event) => update("supports_chat_completions", event.target.value)}>
            <option value="">{t("models.capabilityInherit")}</option>
            <option value="on">{t("models.capabilitySupported")}</option>
            <option value="off">{t("models.capabilityUnsupported")}</option>
          </select>
        </label>
        <label>
          <span>Embeddings</span>
          <select value={form.supports_embeddings} onChange={(event) => update("supports_embeddings", event.target.value)}>
            <option value="">{t("models.capabilityInherit")}</option>
            <option value="on">{t("models.capabilitySupported")}</option>
            <option value="off">{t("models.capabilityUnsupported")}</option>
          </select>
        </label>
      </div>
      {status ? <div className="model-config-status">{status}</div> : null}
    </form>
  );
}

function modelConfigFormFromItem(item) {
  return {
    display_name: item.display_name || "",
    enabled: Boolean(item.enabled),
    supports_responses: capabilityFormValue(item.supports_responses),
    supports_chat_completions: capabilityFormValue(item.supports_chat_completions),
    supports_embeddings: capabilityFormValue(item.supports_embeddings),
    context_window: item.context_window ? String(item.context_window) : "",
    max_output_tokens: item.max_output_tokens ? String(item.max_output_tokens) : "",
    compact_history_item_threshold: item.compact_history_item_threshold ? String(item.compact_history_item_threshold) : "",
    upstream_model: item.upstream_model || "",
    profile_source: item.profile_source || "manual",
    profile_adoption_status: item.profile_adoption_status || "",
  };
}

function modelConfigPayload(form) {
  return {
    display_name: form.display_name,
    enabled: form.enabled,
    supports_responses: capabilityPayloadValue(form.supports_responses),
    supports_chat_completions: capabilityPayloadValue(form.supports_chat_completions),
    supports_embeddings: capabilityPayloadValue(form.supports_embeddings),
    context_window: intOrZero(form.context_window),
    max_output_tokens: intOrZero(form.max_output_tokens),
    compact_history_item_threshold: intOrZero(form.compact_history_item_threshold),
    upstream_model: form.upstream_model,
    profile_source: form.profile_source || "manual",
    profile_adoption_status: form.profile_adoption_status,
  };
}

// The per-model capability columns are tri-state: unset means "inherit the
// channel-level capabilities", so the form keeps an explicit "inherit" option
// instead of defaulting an unset column to a boolean and pinning it on save.
function capabilityFormValue(value) {
  if (value === true) {
    return "on";
  }
  if (value === false) {
    return "off";
  }
  return "";
}

function capabilityPayloadValue(value) {
  if (value === "on") {
    return true;
  }
  if (value === "off") {
    return false;
  }
  return null;
}

function intOrZero(value) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function usageCoverageDetail(missing, t) {
  const count = Number(missing || 0);
  return count > 0 ? t("providers.missingUsage", { count: formatCount(count) }) : "";
}
