import React, { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";
import { DetailMetaPill, InlineTag } from "../components/common/Badges";
import { CodeBlock } from "../components/common/Display";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, requestJSON } from "../lib/api";
import { formatDateTime, setOrDeleteParam } from "../lib/monitor";

export function AuditPage() {
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
        setTraceState({ loading: false, data: null, error: error.message || "Unable to load responses audit trace." });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [responseID, requestAuditID]);

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
          <h1>Audit</h1>
        </div>
      </header>
      <section className="panel responses-audit-panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Responses audit trace</p>
            <h2>Request lineage</h2>
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
          <button className="ghost-button active" type="submit" disabled={!traceForm.responseID.trim() && !traceForm.requestAuditID.trim()}>Load trace</button>
          <button className="ghost-button" type="button" onClick={resetTraceQuery}>Clear</button>
        </form>
        {!responseID && !requestAuditID ? <EmptyState title="No Responses trace selected" detail="Enter a response_id or request_audit_id to inspect request audit, execution events, and upstream exchanges." compact /> : null}
        {traceState.loading ? <EmptyState title="Loading Responses trace" detail="Resolving request audit lineage from the local store." compact /> : null}
        {traceState.error ? <EmptyState title="Unable to load Responses trace" detail={traceState.error} tone="danger" compact /> : null}
        {traceState.data ? <ResponsesAuditTrace trace={traceState.data} /> : null}
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Cross-trace findings</p>
            <h2>Latest findings</h2>
          </div>
          <InlineTag tone={items.length ? "danger" : "green"}>{findings.data?.total ?? 0} total</InlineTag>
        </div>
        <form className="filter-bar" onSubmit={(event) => event.preventDefault()}>
          <input className="filter-input" type="search" placeholder="category" value={category} onChange={(event) => setFilter("category", event.target.value)} />
          <select className="filter-input" aria-label="Finding severity" value={severity} onChange={(event) => setFilter("severity", event.target.value)}>
            <option value="">Any severity</option>
            <option value="critical">Critical</option>
            <option value="high">High</option>
            <option value="medium">Medium</option>
            <option value="low">Low</option>
          </select>
          <button className="ghost-button" type="button" onClick={resetFilters}>Reset</button>
        </form>
        {findings.error ? <EmptyState title="Unable to load findings" detail={findings.error} tone="danger" /> : null}
        {findings.loading && !findings.data ? <EmptyState title="Loading findings" detail="Reading deterministic findings across traces." /> : null}
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
                  <Link className="ghost-button" to={`/traces/${encodeURIComponent(finding.trace_id)}?tab=audit`}>Open Finding</Link>
                  <Link className="ghost-button" to={`/traces/${encodeURIComponent(finding.trace_id)}?tab=protocol`}>Protocol</Link>
                </div>
              </article>
            ))}
          </div>
        ) : findings.data ? (
          <EmptyState title="No findings" detail="No deterministic audit findings have been stored yet." />
        ) : null}
      </section>
    </div>
  );
}

function ResponsesAuditTrace({ trace }) {
  const audit = trace.request_audit || {};
  const events = trace.events || [];
  const exchanges = trace.upstream_exchanges || [];
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
            <p className="eyebrow">Upstream exchanges</p>
            <h2>{exchanges.length} exchange{exchanges.length === 1 ? "" : "s"}</h2>
          </div>
        </div>
        {exchanges.length ? <ResponsesExchangeTable exchanges={exchanges} /> : <EmptyState title="No upstream exchanges" detail="No recorded upstream exchange is linked to this audit record." compact />}
      </section>
    </div>
  );
}

function ResponsesExchangeTable({ exchanges }) {
  return (
    <div className="responses-exchange-table">
      <div className="responses-exchange-head">
        <span>Trace</span>
        <span>Upstream</span>
        <span>Model</span>
        <span>Status</span>
        <span>Started</span>
      </div>
      {exchanges.map((exchange) => (
        <div className="responses-exchange-row" key={exchange.id}>
          <span className="mono">{exchange.trace_id ? <Link to={`/traces/${encodeURIComponent(exchange.trace_id)}`}>{exchange.trace_id}</Link> : exchange.id}</span>
          <span>{exchange.upstream_id || exchange.route_target || "-"}</span>
          <span>{exchange.model || exchange.endpoint || "-"}</span>
          <span>
            <InlineTag tone={exchange.error_text || Number(exchange.status_code || 0) >= 400 ? "danger" : "green"}>{exchange.status_code || (exchange.error_text ? "error" : "-")}</InlineTag>
          </span>
          <span>{formatDateTime(exchange.started_at)}</span>
          {exchange.error_text ? <span className="responses-exchange-error">{exchange.error_text}</span> : null}
        </div>
      ))}
    </div>
  );
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
