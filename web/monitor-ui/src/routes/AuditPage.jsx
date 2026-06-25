import React, { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";
import { DetailMetaPill, InlineTag } from "../components/common/Badges";
import { CodeBlock } from "../components/common/Display";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, requestJSON } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatDateTime, setOrDeleteParam } from "../lib/monitor";

export function AuditPage() {
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const category = searchParams.get("category") || "";
  const severity = searchParams.get("severity") || "";
  const responseID = searchParams.get("response_id") || "";
  const requestAuditID = searchParams.get("request_audit_id") || "";
  const [traceForm, setTraceForm] = useState({ responseID, requestAuditID });
  const [traceState, setTraceState] = useState({ loading: false, data: null, error: "" });
  const params = new URLSearchParams({ limit: "50" });
  if (category) {
    params.set("category", category);
  }
  if (severity) {
    params.set("severity", severity);
  }
  const findings = useJSON(apiURL(apiPaths.findings, params), [category, severity]);
  const functionExecutors = useJSON(apiPaths.responsesFunctionExecutors, []);
  const items = findings.data?.items || [];

  useEffect(() => {
    setTraceForm({ responseID, requestAuditID });
  }, [responseID, requestAuditID]);

  useEffect(() => {
    if (!responseID && !requestAuditID) {
      setTraceState({ loading: false, data: null, error: "" });
      return undefined;
    }
    let cancelled = false;
    const controller = new AbortController();
    const traceParams = new URLSearchParams();
    if (responseID) {
      traceParams.set("response_id", responseID);
    }
    if (requestAuditID) {
      traceParams.set("request_audit_id", requestAuditID);
    }
    setTraceState((current) => ({ ...current, loading: true, error: "" }));
    requestJSON(apiURL(apiPaths.responsesAuditTrace, traceParams), { signal: controller.signal })
      .then((data) => {
        if (!cancelled) {
          setTraceState({ loading: false, data, error: "" });
        }
      })
      .catch((error) => {
        if (cancelled || error.name === "AbortError") {
          return;
        }
        setTraceState({ loading: false, data: null, error: error.message || t("audit.loadResponsesTraceError") });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [responseID, requestAuditID, t]);

  const setFilter = (key, value) => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, key, value);
    setSearchParams(next);
  };
  const resetFilters = () => {
    const next = new URLSearchParams(searchParams);
    next.delete("category");
    next.delete("severity");
    setSearchParams(next);
  };
  const applyTraceQuery = (event) => {
    event.preventDefault();
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "response_id", traceForm.responseID.trim());
    setOrDeleteParam(next, "request_audit_id", traceForm.requestAuditID.trim());
    setSearchParams(next);
  };
  const resetTraceQuery = () => {
    const next = new URLSearchParams(searchParams);
    next.delete("response_id");
    next.delete("request_audit_id");
    setSearchParams(next);
    setTraceForm({ responseID: "", requestAuditID: "" });
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Findings</p>
          <h1>{t("audit.title")}</h1>
        </div>
      </header>
      <section className="panel responses-audit-panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Responses audit trace</p>
            <h2>{t("audit.requestLineage")}</h2>
          </div>
          {traceState.data ? (
            <InlineTag tone={traceState.data.request_audit?.status === "completed" ? "green" : "gold"}>{traceState.data.request_audit?.status || "loaded"}</InlineTag>
          ) : null}
        </div>
        <form className="filter-bar responses-audit-query" onSubmit={applyTraceQuery}>
          <input
            className="filter-input"
            type="search"
            placeholder="response_id"
            value={traceForm.responseID}
            onChange={(event) => setTraceForm((current) => ({ ...current, responseID: event.target.value }))}
          />
          <input
            className="filter-input"
            type="search"
            placeholder="request_audit_id"
            value={traceForm.requestAuditID}
            onChange={(event) => setTraceForm((current) => ({ ...current, requestAuditID: event.target.value }))}
          />
          <button className="ghost-button active" type="submit" disabled={!traceForm.responseID.trim() && !traceForm.requestAuditID.trim()}>{t("audit.loadTrace")}</button>
          <button className="ghost-button" type="button" onClick={resetTraceQuery}>{t("audit.clear")}</button>
        </form>
        {!responseID && !requestAuditID ? <EmptyState title={t("audit.noResponsesTrace")} detail={t("audit.noResponsesTraceDetail")} compact /> : null}
        {traceState.loading ? <EmptyState title={t("audit.loadingResponsesTrace")} detail={t("audit.loadingResponsesTraceDetail")} compact /> : null}
        {traceState.error ? <EmptyState title={t("audit.loadResponsesTraceError")} detail={traceState.error} tone="danger" compact /> : null}
        {traceState.data ? <ResponsesAuditTrace trace={traceState.data} /> : null}
      </section>
      <ResponsesFunctionExecutorsPanel state={functionExecutors} />
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Cross-trace findings</p>
            <h2>{t("audit.latestFindings")}</h2>
          </div>
          <InlineTag tone={items.length ? "danger" : "green"}>{t("audit.totalFindings", { count: findings.data?.total ?? 0 })}</InlineTag>
        </div>
        <form className="filter-bar" onSubmit={(event) => event.preventDefault()}>
          <input className="filter-input" type="search" placeholder={t("audit.category")} value={category} onChange={(event) => setFilter("category", event.target.value)} />
          <select className="filter-input" aria-label="Finding severity" value={severity} onChange={(event) => setFilter("severity", event.target.value)}>
            <option value="">{t("audit.anySeverity")}</option>
            <option value="critical">{t("audit.severityCritical")}</option>
            <option value="high">{t("audit.severityHigh")}</option>
            <option value="medium">{t("audit.severityMedium")}</option>
            <option value="low">{t("audit.severityLow")}</option>
          </select>
          <button className="ghost-button" type="button" onClick={resetFilters}>{t("common.reset")}</button>
        </form>
        {findings.error ? <EmptyState title={t("audit.loadFindingsError")} detail={findings.error} tone="danger" /> : null}
        {findings.loading && !findings.data ? <EmptyState title={t("audit.loadingFindings")} detail={t("audit.loadingFindingsDetail")} /> : null}
        {items.length ? (
          <div className="finding-list">
            {items.map((finding) => (
              <article key={`${finding.trace_id}-${finding.id}`} className="finding-card">
                <div className="finding-card-head">
                  <div>
                    <strong>{finding.title || finding.category}</strong>
                    <span>{finding.description || finding.evidence_path}</span>
                  </div>
                  <div className="trace-tag-group">
                    <InlineTag tone={finding.severity === "high" || finding.severity === "critical" ? "danger" : "gold"}>{finding.severity}</InlineTag>
                    <InlineTag>{finding.category}</InlineTag>
                  </div>
                </div>
                <div className="detail-meta-strip">
                  <DetailMetaPill label="trace" value={finding.trace_id} mono />
                  <DetailMetaPill label="node" value={finding.node_id || "-"} mono />
                  <DetailMetaPill label="detector" value={`${finding.detector || "-"} ${finding.detector_version || ""}`.trim()} />
                </div>
                <div className="action-group action-group-start">
                  <Link className="ghost-button" to={`/traces/${encodeURIComponent(finding.trace_id)}?tab=audit`}>{t("audit.openFinding")}</Link>
                  <Link className="ghost-button" to={`/traces/${encodeURIComponent(finding.trace_id)}?tab=protocol`}>{t("audit.protocol")}</Link>
                </div>
              </article>
            ))}
          </div>
        ) : findings.data ? (
          <EmptyState title={t("audit.noFindings")} detail={t("audit.noFindingsDetail")} />
        ) : null}
      </section>
    </div>
  );
}

function ResponsesFunctionExecutorsPanel({ state }) {
  const { t } = useI18n();
  const [localSummary, setLocalSummary] = useState(null);
  const [enabledDraft, setEnabledDraft] = useState(false);
  const [writeState, setWriteState] = useState({ loading: false, message: "", error: "" });
  const data = localSummary || state.data || {};
  const executors = data.executors || [];
  const warnings = data.warnings || [];

  useEffect(() => {
    if (state.data) {
      setLocalSummary(null);
      setEnabledDraft(Boolean(state.data.enabled));
      setWriteState({ loading: false, message: "", error: "" });
    }
  }, [state.data]);

  const submitExecutorConfig = (validateOnly) => {
    setWriteState({ loading: true, message: "", error: "" });
    requestJSON(apiPaths.responsesFunctionExecutors, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ validate_only: validateOnly, enabled: enabledDraft }),
    })
      .then((payload) => {
        if (!payload.validate_only && payload.summary) {
          setLocalSummary(payload.summary);
        }
        setWriteState({ loading: false, message: payload.validate_only ? "Validation passed" : "Applied to current process", error: "" });
      })
      .catch((error) => {
        setWriteState({ loading: false, message: "", error: error.message || "Unable to update function executors" });
      });
  };

  return (
    <section className="panel responses-function-executors-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Responses function executors</p>
            <h2>{t("audit.serverTools")}</h2>
        </div>
        {state.loading && !state.data ? <InlineTag>{t("audit.loading")}</InlineTag> : <InlineTag tone={data.enabled ? "green" : "gold"}>{data.enabled ? t("audit.enabled") : t("audit.disabled")}</InlineTag>}
      </div>
      {state.error ? <EmptyState title={t("audit.loadFunctionExecutorsError")} detail={state.error} tone="danger" compact /> : null}
      {!state.error ? (
        <>
          <div className="responses-function-executor-controls">
            <label className="provider-form-check">
              <input type="checkbox" checked={enabledDraft} onChange={(event) => setEnabledDraft(event.target.checked)} />
              {t("audit.enabled")}
            </label>
            <div className="provider-form-actions">
              <button className="ghost-button" type="button" disabled={writeState.loading} onClick={() => submitExecutorConfig(true)}>{t("audit.validate")}</button>
              <button className="ghost-button active" type="button" disabled={writeState.loading} onClick={() => submitExecutorConfig(false)}>{t("common.apply")}</button>
            </div>
            {writeState.message ? <InlineTag tone="green">{writeState.message}</InlineTag> : null}
            {writeState.error ? <InlineTag tone="danger">{writeState.error}</InlineTag> : null}
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label={t("audit.timeout")} value={data.timeout || "-"} />
            <DetailMetaPill label={t("audit.maxResult")} value={data.max_result_bytes ? `${data.max_result_bytes} bytes` : "-"} />
            <DetailMetaPill label={t("audit.arguments")} value={data.redaction?.arguments ? t("audit.redacted") : t("audit.visible")} />
            <DetailMetaPill label={t("audit.output")} value={data.redaction?.output ? t("audit.redacted") : t("audit.visible")} />
          </div>
          <div className="trace-tag-group">
            {(data.supported_types || []).map((type) => <InlineTag key={type} tone="accent">{type}</InlineTag>)}
            {!data.supported_types?.length ? <InlineTag>{t("audit.noSupportedTypes")}</InlineTag> : null}
          </div>
          <ExecutorWarnings title="Warnings" warnings={warnings} />
          {executors.length ? (
            <div className="responses-function-executor-list">
              {executors.map((executor) => (
                <article key={`${executor.name}:${executor.type}`} className="finding-card responses-function-executor-card">
                  <div className="finding-card-head">
                    <div>
                      <strong>{executor.name || "(unnamed)"}</strong>
                      <span>{executor.type || "unknown"}</span>
                    </div>
                    <div className="trace-tag-group">
                      <InlineTag tone={executor.enabled ? "green" : "gold"}>{executor.enabled ? "enabled" : "disabled"}</InlineTag>
                      <InlineTag tone={executor.available ? "green" : "danger"}>{executor.available ? "available" : "unavailable"}</InlineTag>
                      <InlineTag tone={executor.output_configured ? "accent" : "default"}>{executor.output_configured ? "output configured" : "no output"}</InlineTag>
                      <InlineTag tone={executor.command_configured ? "accent" : "gold"}>{executor.command_configured ? "command configured" : "no command"}</InlineTag>
                    </div>
                  </div>
                  <div className="detail-meta-strip responses-function-executor-state">
                    <DetailMetaPill label="available" value={formatBool(executor.available)} />
                    <DetailMetaPill label="command" value={formatBool(executor.command_configured)} />
                    <DetailMetaPill label="output" value={formatBool(executor.output_configured)} />
                  </div>
                  <ExecutorWarnings title="Executor warnings" warnings={executor.warnings || []} compact />
                </article>
              ))}
            </div>
          ) : (
            <EmptyState title={t("audit.noFunctionExecutors")} detail={t("audit.noFunctionExecutorsDetail")} compact />
          )}
        </>
      ) : null}
    </section>
  );
}

function ExecutorWarnings({ title, warnings, compact = false }) {
  const items = Array.isArray(warnings) ? warnings.filter(Boolean) : [];
  if (!items.length) {
    return null;
  }
  return (
    <div className={`responses-function-executor-warnings ${compact ? "responses-function-executor-warnings-compact" : ""}`.trim()}>
      <strong>{title}</strong>
      <ul>
        {items.map((warning, index) => <li key={`${String(warning)}:${index}`}>{String(warning)}</li>)}
      </ul>
    </div>
  );
}

function formatBool(value) {
  return value ? "yes" : "no";
}

function ResponsesAuditTrace({ trace }) {
  const audit = trace.request_audit || {};
  const events = trace.events || [];
  const entryExchange = trace.entry_exchange || null;
  const exchanges = trace.model_exchanges?.length ? trace.model_exchanges : (trace.upstream_exchanges || []);
  return (
    <div className="responses-audit-trace">
      <div className="finding-card responses-audit-record">
        <div className="finding-card-head">
          <div>
            <strong>{audit.method || "REQUEST"} {audit.path || "/v1/responses"}</strong>
            <span>{audit.id || trace.query?.request_audit_id || "-"}</span>
          </div>
          <div className="trace-tag-group">
            <InlineTag tone={audit.status === "completed" ? "green" : audit.error_text ? "danger" : "gold"}>{audit.status || "unknown"}</InlineTag>
            {audit.response_id ? <InlineTag tone="accent">{audit.response_id}</InlineTag> : null}
          </div>
        </div>
        <div className="detail-meta-strip">
          <DetailMetaPill label="created" value={formatDateTime(audit.created_at)} />
          <DetailMetaPill label="conversation" value={audit.conversation_id || "-"} mono />
          <DetailMetaPill label="client request" value={audit.client_request_id || "-"} mono />
          <DetailMetaPill label="body sha256" value={audit.body_sha256 || "-"} mono />
        </div>
        {audit.error_text ? <pre className="timeline-message responses-audit-error">{audit.error_text}</pre> : null}
        <div className="responses-audit-json-grid">
          <div>
            <div className="breakdown-title">Body preview</div>
            <CodeBlock value={audit.body_preview || "(empty)"} />
          </div>
          <div>
            <div className="breakdown-title">Headers</div>
            <CodeBlock value={formatJSON(audit.header_json)} />
          </div>
        </div>
      </div>

      {entryExchange ? <ResponsesEntryExchangeSummary exchange={entryExchange} /> : null}

      <section className="responses-audit-section">
        <div className="panel-head panel-head-compact">
          <div>
            <p className="eyebrow">Execution events</p>
            <h2>{events.length} event{events.length === 1 ? "" : "s"}</h2>
          </div>
        </div>
        {events.length ? (
          <div className="timeline-list responses-audit-events">
            {events.map((event) => (
              <article key={event.id} className="timeline-item">
                <div className="timeline-rail">
                  <span className={event.status === "failed" || event.status === "rejected" ? "timeline-dot timeline-dot-danger" : "timeline-dot timeline-dot-live"} />
                </div>
                <div className="timeline-card">
                  <div className="timeline-head">
                    <strong>{event.event_type || "event"}</strong>
                    <span>{formatDateTime(event.occurred_at)}</span>
                    <span className="timeline-badge">{event.status || event.phase || "event"}</span>
                  </div>
                  <div className="detail-meta-strip">
                    <DetailMetaPill label="phase" value={event.phase || "-"} />
                    <DetailMetaPill label="event id" value={event.id || "-"} mono />
                    <DetailMetaPill label="response" value={event.response_id || "-"} mono />
                  </div>
                  {event.message ? <div className="timeline-message">{event.message}</div> : null}
                  {hasObjectFields(event.details_json) ? <CodeBlock value={formatJSON(event.details_json)} /> : null}
                </div>
              </article>
            ))}
          </div>
        ) : (
          <EmptyState title="No execution events" detail="No Responses execution events are linked to this audit record." compact />
        )}
      </section>

      <section className="responses-audit-section">
        <div className="panel-head panel-head-compact">
          <div>
            <p className="eyebrow">Model exchanges</p>
            <h2>{exchanges.length} exchange{exchanges.length === 1 ? "" : "s"}</h2>
          </div>
        </div>
        {exchanges.length ? <ResponsesExchangeTable exchanges={exchanges} /> : <EmptyState title="No model exchanges" detail="No recorded model exchange is linked to this audit record." compact />}
      </section>
    </div>
  );
}

function ResponsesEntryExchangeSummary({ exchange }) {
  return (
    <section className="responses-audit-section">
      <div className="panel-head panel-head-compact">
        <div>
          <p className="eyebrow">Entry exchange</p>
          <h2>{exchangeLabel(exchange.exchange_role || exchange.exchange_kind || "client request")}</h2>
        </div>
        <InlineTag tone={exchange.error_text || Number(exchange.status_code || 0) >= 400 ? "danger" : "green"}>{exchange.status_code || (exchange.error_text ? "error" : "entry")}</InlineTag>
      </div>
      <div className="detail-meta-strip responses-entry-exchange-summary">
        <DetailMetaPill label="kind" value={exchangeLabel(exchange.exchange_kind || "entry")} />
        <DetailMetaPill label="role" value={exchangeLabel(exchange.exchange_role || "-")} />
        <DetailMetaPill label="sequence" value={formatSequence(exchange.sequence_index)} />
        <DetailMetaPill label="model" value={exchange.model || "-"} />
        <DetailMetaPill label="provider" value={exchange.provider || "-"} />
        <DetailMetaPill label="endpoint" value={exchange.endpoint || "-"} mono />
        <DetailMetaPill label="cassette" value={exchange.cassette_path || "-"} mono />
      </div>
      {exchange.error_text ? <pre className="timeline-message responses-audit-error">{exchange.error_text}</pre> : null}
    </section>
  );
}

function ResponsesExchangeTable({ exchanges }) {
  return (
    <div className="responses-exchange-table">
      <div className="responses-exchange-head">
        <span>Exchange</span>
        <span>Model / provider</span>
        <span>Endpoint</span>
        <span>Status</span>
        <span>Trace</span>
        <span>Cassette</span>
        <span>Started</span>
      </div>
      {exchanges.map((exchange) => (
        <div className="responses-exchange-row" key={exchange.id || exchange.trace_id || `${exchange.exchange_role || "exchange"}-${exchange.sequence_index || 0}`}>
          <span>
            <span className="responses-exchange-primary">{exchangeLabel(exchange.exchange_role || exchange.exchange_kind || "model")}</span>
            <span className="responses-exchange-subline">{exchangeLabel(exchange.exchange_kind || "model")} / seq {formatSequence(exchange.sequence_index)}</span>
          </span>
          <span>
            <span className="responses-exchange-primary">{exchange.model || "-"}</span>
            <span className="responses-exchange-subline">{exchange.provider || exchange.upstream_id || exchange.route_target || "-"}</span>
          </span>
          <span className="mono">{exchange.endpoint || "-"}</span>
          <span>
            <InlineTag tone={exchange.error_text || Number(exchange.status_code || 0) >= 400 ? "danger" : "green"}>{exchange.status_code || (exchange.error_text ? "error" : "-")}</InlineTag>
          </span>
          <span className="mono">{exchange.trace_id ? <Link to={`/traces/${encodeURIComponent(exchange.trace_id)}`}>{exchange.trace_id}</Link> : exchange.id || "-"}</span>
          <span className="mono">{exchange.cassette_path || "-"}</span>
          <span>{formatDateTime(exchange.started_at)}</span>
          {exchange.error_text ? <span className="responses-exchange-error">{exchange.error_text}</span> : null}
        </div>
      ))}
    </div>
  );
}

function formatSequence(value) {
  return Number.isFinite(Number(value)) && Number(value) !== 0 ? String(value) : "0";
}

function exchangeLabel(value = "") {
  switch (String(value || "").trim()) {
    case "primary_model_call":
      return "Model";
    case "client_request":
      return "Request";
    case "upstream_model_call":
    case "model_call":
      return "Model";
    case "model":
      return "Model";
    case "entry":
      return "Request";
    case "-":
      return "-";
    default:
      return value || "-";
  }
}

function formatJSON(value) {
  if (!hasObjectFields(value)) {
    return "{}";
  }
  return JSON.stringify(value, null, 2);
}

function hasObjectFields(value) {
  return Boolean(value && typeof value === "object" && Object.keys(value).length);
}
