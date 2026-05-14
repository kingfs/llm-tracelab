import React, { useEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { CollapsibleCard, CodeBlock, MessageContent } from "../components/common/Display";
import { DetailMetaPill, DownloadIcon, HomeIcon, InlineTag, StackIcon, TokenBadge } from "../components/common/Badges";
import { EmptyState } from "../components/common/EmptyState";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, downloadBlob } from "../lib/api";
import {
  buildRoutingDecisionSummary,
  buildChannelLink,
  buildTraceUpstreamHealthSummary,
  buildUpstreamLink,
  formatDateTime,
  formatDuration,
  formatEndpointTag,
  formatFailureReason,
  formatHealthLabel,
  formatProviderTag,
  formatRatio,
  formatRoutingScore,
  formatTokenRate,
  healthTone,
  metricThresholdTone,
  normalizeTraceTab,
  resolveThresholdState,
  setOrDeleteParam,
  summarizeTraceFailure,
} from "../lib/monitor";
import {
  buildToolMessageSummary,
  buildToolSchemaSummary,
  collectTraceToolCalls,
  countToolMatches,
  findDeclaredToolForCall,
  normalizeDeclaredTool,
  isSameToolName,
} from "../lib/traceTools";

export function TraceDetailPage() {
  const { traceID = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [tab, setTab] = useState(() => normalizeTraceTab(searchParams.get("tab")));
  const [renderMarkdown, setRenderMarkdown] = useState(true);
  const failureSummaryRef = useRef(null);
  const detail = useJSON(apiPaths.trace(traceID), [traceID]);
  const raw = useJSON(apiPaths.traceRaw(traceID), [traceID, tab === "raw" ? "raw" : "summary"]);
  const observation = useJSON(apiPaths.traceObservation(traceID), [traceID, tab === "protocol" ? "protocol" : "idle"]);
  const findings = useJSON(apiPaths.traceFindings(traceID), [traceID, tab === "audit" ? "audit" : "idle"]);
  const performance = useJSON(apiPaths.tracePerformance(traceID), [traceID, tab === "performance" ? "performance" : "idle"]);
  const header = detail.data?.header?.meta;
  const usage = detail.data?.header?.usage;
  const session = detail.data?.session;
  const failureSummary = summarizeTraceFailure(detail.data);
  const selectedUpstreamID = header?.selected_upstream_id || "";
  const selectedUpstreamBaseURL = header?.selected_upstream_base_url || "";
  const selectedUpstreamProviderPreset = header?.selected_upstream_provider_preset || "";
  const routingPolicy = header?.routing_policy || "";
  const routingScore = Number(header?.routing_score || 0);
  const routingCandidateCount = Number(header?.routing_candidate_count || 0);
  const routingFailureReason = header?.routing_failure_reason || "";
  const selectedUpstreamHealth = detail.data?.selected_upstream_health;
  const declaredTools = (detail.data?.tools || []).map((tool, index) => normalizeDeclaredTool(tool, index)).filter((tool) => tool.name || tool.description || tool.parameters);
  const traceToolCalls = collectTraceToolCalls(detail.data);
  const focusTarget = searchParams.get("focus") || "";
  const hasDeclaredToolsTab = Boolean(declaredTools.length);
  const fromSessionID = searchParams.get("from_session") || "";
  const fromView = searchParams.get("view") === "sessions" ? "sessions" : "requests";
  const backLink = fromSessionID ? `/sessions/${encodeURIComponent(fromSessionID)}` : `/${fromView}`;
  const conversation = hasConversation(detail.data);
  const timelineCount = detail.data?.events?.length || 0;
  const messageCount = detail.data?.messages?.length || 0;
  const toolCount = declaredTools.length;

  const applyTraceFocus = (nextTab, nextFocus = "") => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "tab", nextTab === "conversation" ? "" : nextTab);
    setOrDeleteParam(next, "focus", nextFocus);
    setSearchParams(next, { replace: true });
  };

  const downloadTrace = async () => {
    let blob;
    try {
      blob = await downloadBlob(apiPaths.traceDownload(traceID));
    } catch {
      return;
    }
    const url = window.URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `${traceID}.http`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.URL.revokeObjectURL(url);
  };

  useEffect(() => {
    const requestedTab = normalizeTraceTab(searchParams.get("tab"));
    setTab((current) => (current === requestedTab ? current : requestedTab));
  }, [searchParams]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    setOrDeleteParam(next, "tab", tab === "conversation" ? "" : tab);
    if (next.toString() === searchParams.toString()) {
      return;
    }
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams, tab]);

  useEffect(() => {
    if (focusTarget !== "failure" || !failureSummary || !failureSummaryRef.current) {
      return;
    }
    failureSummaryRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [failureSummary, focusTarget]);

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{header?.model || "trace detail"}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone="accent">{formatEndpointTag(header?.endpoint || header?.operation)}</InlineTag>
              <InlineTag>{formatProviderTag(header?.provider)}</InlineTag>
              {selectedUpstreamID ? <InlineTag tone="green">{selectedUpstreamID}</InlineTag> : null}
              {detail.data?.header?.layout?.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
              <InlineTag tone={header?.status_code >= 200 && header?.status_code < 300 ? "green" : "danger"}>{header?.status_code || 0}</InlineTag>
            </div>
          </div>
          <div className="detail-meta-strip">
            {session?.session_id ? <DetailMetaPill label="session" value={session.session_id} mono /> : null}
            <DetailMetaPill label="time" value={formatDateTime(header?.time)} />
            <DetailMetaPill label="endpoint" value={header?.endpoint || header?.url || "-"} />
            <DetailMetaPill label="duration" value={formatDuration(header?.duration_ms || 0, { precise: true })} />
            <DetailMetaPill label="ttft" value={formatDuration(header?.ttft_ms || 0, { precise: true })} />
            <DetailMetaPill label="rate" value={formatTokenRate(usage?.total_tokens || 0, header?.duration_ms || 0)} />
            <DetailMetaPill label="request id" value={header?.request_id || "-"} mono />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to={backLink} title={fromSessionID ? "Back to session" : "Back to list"} aria-label={fromSessionID ? "Back to session" : "Back to list"}>
              <HomeIcon />
            </Link>
            {session?.session_id ? (
              <Link className="icon-button" to={`/sessions/${encodeURIComponent(session.session_id)}`} title="View session" aria-label="View session">
                <StackIcon />
              </Link>
            ) : null}
            <button className="icon-button" type="button" onClick={downloadTrace} title="Download .http" aria-label="Download trace">
              <DownloadIcon />
            </button>
          </div>
          <div className="detail-toolbar-tokens">
            <TokenBadge label="in" value={usage?.prompt_tokens || 0} icon="input" />
            <TokenBadge label="out" value={usage?.completion_tokens || 0} icon="output" />
            <TokenBadge label="total" value={usage?.total_tokens || 0} icon="total" accent="token-badge-strong" />
            <TokenBadge label="cached" value={usage?.prompt_token_details?.cached_tokens || 0} icon="cached" />
          </div>
        </div>
      </header>

      {failureSummary ? (
        <section
          ref={failureSummaryRef}
          className={focusTarget === "failure" ? "panel trace-failure-panel trace-failure-panel-focused" : "panel trace-failure-panel"}
        >
          <div className="trace-failure-head">
            <div>
              <p className="eyebrow">Failure summary</p>
              <h2>{failureSummary.title}</h2>
            </div>
            <InlineTag tone="danger">{header?.status_code || 0}</InlineTag>
          </div>
          <p className="trace-failure-summary">{failureSummary.summary}</p>
          <div className="trace-failure-meta">
            <span>{header?.endpoint || header?.url || "-"}</span>
            <span>duration {formatDuration(header?.duration_ms || 0, { precise: true })}</span>
            <span>ttft {formatDuration(header?.ttft_ms || 0, { precise: true })}</span>
            <span>tokens {usage?.total_tokens || 0}</span>
            <span>rate {formatTokenRate(usage?.total_tokens || 0, header?.duration_ms || 0)}</span>
          </div>
          <div className="trace-failure-actions">
            <button className={tab === "conversation" ? "ghost-button active" : "ghost-button"} onClick={() => applyTraceFocus("conversation", "timeline_error")}>
              Open Conversation
            </button>
            <button className={tab === "raw" ? "ghost-button active" : "ghost-button"} onClick={() => applyTraceFocus("raw", "response")}>
              Open Raw Protocol
            </button>
            {session?.session_id ? (
              <Link className="ghost-button" to={`/sessions/${encodeURIComponent(session.session_id)}`}>
                Back to Session
              </Link>
            ) : null}
          </div>
          {failureSummary.detail ? <pre className="trace-failure-detail">{failureSummary.detail}</pre> : null}
        </section>
      ) : null}

      {detail.data ? (
        <section className="panel trace-reading-panel">
          <div className="panel-head">
            <div>
              <p className="eyebrow">Reading guide</p>
              <h2>Where to inspect this trace</h2>
            </div>
          </div>
          <div className="trace-reading-grid">
            <button className={tab === "conversation" ? "trace-reading-card trace-reading-card-active" : "trace-reading-card"} onClick={() => setTab("conversation")}>
              <strong>Conversation</strong>
              <span>{conversation ? `${messageCount} captured message${messageCount > 1 ? "s" : ""}` : `${timelineCount} event record${timelineCount > 1 ? "s" : ""}`}</span>
              <p>Use this for routing context, conversation payloads, final output, and captured timeline events.</p>
            </button>
            <button className={tab === "protocol" ? "trace-reading-card trace-reading-card-active" : "trace-reading-card"} onClick={() => setTab("protocol")}>
              <strong>Protocol</strong>
              <span>Observation IR</span>
              <p>Use this for provider-specific semantic nodes, normalized types, JSON paths, and raw node payloads.</p>
            </button>
            <button className={tab === "audit" ? "trace-reading-card trace-reading-card-active" : "trace-reading-card"} onClick={() => setTab("audit")}>
              <strong>Audit</strong>
              <span>Deterministic findings</span>
              <p>Use this for dangerous tool calls, credential leaks, safety findings, and evidence paths.</p>
            </button>
            <button className={tab === "performance" ? "trace-reading-card trace-reading-card-active" : "trace-reading-card"} onClick={() => setTab("performance")}>
              <strong>Performance</strong>
              <span>Latency and token speed</span>
              <p>Use this for latency, TTFT, token throughput, cache ratio, status, and routing context.</p>
            </button>
            <button className={tab === "raw" ? "trace-reading-card trace-reading-card-active" : "trace-reading-card"} onClick={() => setTab("raw")}>
              <strong>Raw</strong>
              <span>Original HTTP exchange</span>
              <p>Use this when you need exact request or response bytes, headers, and provider-facing payloads.</p>
            </button>
          </div>
        </section>
      ) : null}

      {detail.error ? <EmptyState title="Unable to load trace detail" detail={detail.error} tone="danger" /> : null}
      {detail.loading && !detail.data ? <EmptyState title="Loading trace detail" detail="Resolving timeline, routing, payload, and tool information for this trace." /> : null}

      {tab === "conversation" && detail.data ? (
        <div className="detail-grid">
          {selectedUpstreamID || routingFailureReason ? (
            <section className="panel">
              <div className="panel-head">
                <div>
                  <p className="eyebrow">Routing decision</p>
                  <h2>{selectedUpstreamID ? "Selected upstream" : "Routing failure"}</h2>
                </div>
                <div className="panel-head-actions">
                  {selectedUpstreamID ? (
                    <Link className="ghost-button active" to={buildChannelLink(selectedUpstreamID)}>
                      Open Channel
                    </Link>
                  ) : null}
                  {selectedUpstreamID ? (
                    <Link className="ghost-button" to={buildUpstreamLink(selectedUpstreamID)}>
                      Open Upstream
                    </Link>
                  ) : null}
                </div>
              </div>
              <div className="detail-meta-strip">
                <DetailMetaPill label="upstream" value={selectedUpstreamID || "-"} mono />
                <DetailMetaPill label="provider" value={selectedUpstreamProviderPreset || "-"} />
                <DetailMetaPill label="policy" value={routingPolicy || "-"} />
                <DetailMetaPill label="score" value={formatRoutingScore(routingScore)} />
                <DetailMetaPill label="candidates" value={routingCandidateCount || 0} />
                {routingFailureReason ? <DetailMetaPill label="failure" value={formatFailureReason(routingFailureReason)} /> : null}
              </div>
              <div className="routing-summary-grid">
                <section className="breakdown-card">
                  <div className="breakdown-title">{selectedUpstreamID ? "Resolved upstream" : "Failure class"}</div>
                  <div className="routing-summary-stack">
                    <strong className="trace-model-name">{selectedUpstreamID || formatFailureReason(routingFailureReason) || "routing failure"}</strong>
                    <span className="trace-subline mono">{selectedUpstreamBaseURL || "-"}</span>
                    {selectedUpstreamID || routingPolicy ? (
                      <div className="trace-tag-group">
                        {selectedUpstreamProviderPreset ? <InlineTag tone="accent">{selectedUpstreamProviderPreset}</InlineTag> : null}
                        {routingPolicy ? <InlineTag>{routingPolicy}</InlineTag> : null}
                      </div>
                    ) : null}
                  </div>
                </section>
                <section className="breakdown-card">
                  <div className="breakdown-title">Decision explanation</div>
                  <div className="routing-summary-stack">
                    <span className="trace-subline">
                      {buildRoutingDecisionSummary({
                        upstreamID: selectedUpstreamID,
                        policy: routingPolicy,
                        score: routingScore,
                        candidateCount: routingCandidateCount,
                        failureReason: routingFailureReason,
                      })}
                    </span>
                  </div>
                </section>
                {selectedUpstreamHealth ? (
                  <section className="breakdown-card">
                    <div className="breakdown-title">Upstream health at review time</div>
                    <div className="routing-summary-stack">
                      <div className="trace-tag-group">
                        <InlineTag tone={healthTone(selectedUpstreamHealth.health_state)}>{formatHealthLabel(selectedUpstreamHealth.health_state)}</InlineTag>
                        <InlineTag tone={metricThresholdTone(resolveThresholdState(selectedUpstreamHealth.error_rate, selectedUpstreamHealth.health_thresholds?.error_rate_degraded, selectedUpstreamHealth.health_thresholds?.error_rate_open))}>
                          error {resolveThresholdState(selectedUpstreamHealth.error_rate, selectedUpstreamHealth.health_thresholds?.error_rate_degraded, selectedUpstreamHealth.health_thresholds?.error_rate_open)}
                        </InlineTag>
                        <InlineTag tone={metricThresholdTone(resolveThresholdState(selectedUpstreamHealth.timeout_rate, selectedUpstreamHealth.health_thresholds?.timeout_rate_degraded, selectedUpstreamHealth.health_thresholds?.timeout_rate_open))}>
                          timeout {resolveThresholdState(selectedUpstreamHealth.timeout_rate, selectedUpstreamHealth.health_thresholds?.timeout_rate_degraded, selectedUpstreamHealth.health_thresholds?.timeout_rate_open)}
                        </InlineTag>
                      </div>
                      <span className="trace-subline">{buildTraceUpstreamHealthSummary(selectedUpstreamHealth)}</span>
                      <div className="detail-meta-strip">
                        <DetailMetaPill label="error" value={formatRatio(selectedUpstreamHealth.error_rate)} />
                        <DetailMetaPill label="timeout" value={formatRatio(selectedUpstreamHealth.timeout_rate)} />
                        <DetailMetaPill label="ttft" value={`${Math.round(selectedUpstreamHealth.ttft_fast_ms || 0)} ms`} />
                        <DetailMetaPill label="latency" value={`${Math.round(selectedUpstreamHealth.latency_fast_ms || 0)} ms`} />
                      </div>
                    </div>
                  </section>
                ) : null}
              </div>
            </section>
          ) : null}
          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">{hasConversation(detail.data) ? "Conversation" : "Payload"}</p>
                <h2>{hasConversation(detail.data) ? "Request and response" : "Request / response body"}</h2>
              </div>
              <label className="wrap-toggle">
                <input type="checkbox" checked={renderMarkdown} onChange={(event) => setRenderMarkdown(event.target.checked)} />
                Render markdown
              </label>
            </div>
            {hasConversation(detail.data) ? (
              <div className="message-list">
                {detail.data.messages.map((message, index) => (
                  <MessageCard
                    key={`${message.role}-${index}`}
                    message={message}
                    renderMarkdown={renderMarkdown}
                    declaredTools={declaredTools}
                    CollapsibleCard={CollapsibleCard}
                    CodeBlock={CodeBlock}
                    InlineTag={InlineTag}
                    MessageContent={MessageContent}
                  />
                ))}
                {detail.data.ai_reasoning ? (
                  <CollapsibleCard title="Reasoning" subtitle="assistant reasoning" defaultOpen={false}>
                    <CodeBlock value={detail.data.ai_reasoning} />
                  </CollapsibleCard>
                ) : null}
                {detail.data.ai_content ? (
                  <article className="message-card message-assistant">
                    <div className="message-meta">
                      <span className="role-pill">assistant</span>
                      <span className="message-kind">final output</span>
                    </div>
                    <MessageContent value={detail.data.ai_content} format="markdown" renderMarkdown={renderMarkdown} className="message-body" />
                  </article>
                ) : null}
                {detail.data.tool_calls?.length ? (
                  <CollapsibleCard title="Tool Calls" subtitle={`${detail.data.tool_calls.length} call(s)`} defaultOpen={false}>
                    {detail.data.tool_calls.map((call) => (
                      <ToolCallView key={call.id || call.function?.name} call={call} match={findDeclaredToolForCall(call, declaredTools)} CodeBlock={CodeBlock} InlineTag={InlineTag} />
                    ))}
                  </CollapsibleCard>
                ) : null}
                {detail.data.ai_blocks?.length ? (
                  <CollapsibleCard title="Output Blocks" subtitle={`${detail.data.ai_blocks.length} block(s)`} defaultOpen={false}>
                    {detail.data.ai_blocks.map((block, index) => (
                      <BlockView key={`${block.kind}-${index}`} block={block} CodeBlock={CodeBlock} />
                    ))}
                  </CollapsibleCard>
                ) : null}
              </div>
            ) : (
              <PayloadSummary raw={raw} CodeBlock={CodeBlock} />
            )}
          </section>
          <TimelinePanel events={detail.data.events || []} focusTarget={focusTarget} CodeBlock={CodeBlock} InlineTag={InlineTag} />
          {hasDeclaredToolsTab ? <DeclaredToolsPanel tools={declaredTools} toolCalls={traceToolCalls} CodeBlock={CodeBlock} InlineTag={InlineTag} /> : null}
        </div>
      ) : null}

      {tab === "protocol" ? <ProtocolPanel observation={observation} CodeBlock={CodeBlock} InlineTag={InlineTag} /> : null}
      {tab === "audit" ? <AuditPanel findings={findings} InlineTag={InlineTag} CodeBlock={CodeBlock} /> : null}
      {tab === "performance" ? <PerformancePanel performance={performance} /> : null}
      {tab === "raw" ? <RawProtocolPanel raw={raw} focusTarget={focusTarget} /> : null}
    </div>
  );
}

function DeclaredToolsPanel({ tools, toolCalls = [], CodeBlock, InlineTag }) {
  const [selectedToolName, setSelectedToolName] = useState(() => tools[0]?.name || "");
  const [schemaToolName, setSchemaToolName] = useState("");

  useEffect(() => {
    if (!tools.length) {
      setSelectedToolName("");
      return;
    }
    if (tools.some((tool) => tool.name === selectedToolName)) {
      return;
    }
    setSelectedToolName(tools[0].name || "");
  }, [selectedToolName, tools]);

  const selectedTool = tools.find((tool) => tool.name === selectedToolName) || tools[0] || null;
  const selectedToolCalls = selectedTool ? toolCalls.filter((call) => isSameToolName(call.function?.name, selectedTool.name)) : [];
  const schemaTool = tools.find((tool) => tool.name === schemaToolName) || null;

  return (
    <>
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Declared tools</p>
            <h2>Request tools</h2>
          </div>
        </div>
        {tools.length ? (
          <div className="tool-layout">
            <div className="tool-list-column">
              {tools.map((tool, index) => {
                const count = countToolMatches(toolCalls, tool.name);
                const isSelected = selectedTool?.name === tool.name;
                return (
                  <button
                    key={`${tool.name || "tool"}-${index}`}
                    className={isSelected ? "tool-list-item tool-list-item-active" : "tool-list-item"}
                    onClick={() => {
                      setSelectedToolName(tool.name);
                      setSchemaToolName(tool.name);
                    }}
                  >
                    <div className="tool-list-item-head">
                      <strong>{tool.name || `tool ${index + 1}`}</strong>
                      <InlineTag tone={count > 0 ? "green" : "default"}>{count > 0 ? `${count} call${count > 1 ? "s" : ""}` : "not invoked"}</InlineTag>
                    </div>
                    <div className="tool-list-item-meta">
                      <span>{tool.source || tool.type || "tool"}</span>
                      <span>{tool.description || "Click to inspect the tool definition."}</span>
                    </div>
                  </button>
                );
              })}
            </div>
            <div className="tool-detail-column">
              {selectedTool ? (
                <>
                  <div className="tool-detail-header">
                    <div>
                      <p className="eyebrow">Tool overview</p>
                      <h3>{selectedTool.name}</h3>
                    </div>
                    <div className="trace-tag-group">
                      <InlineTag tone="accent">{selectedTool.source || selectedTool.type || "tool"}</InlineTag>
                      <InlineTag tone={selectedToolCalls.length ? "green" : "default"}>
                        {selectedToolCalls.length ? `${selectedToolCalls.length} matched call${selectedToolCalls.length > 1 ? "s" : ""}` : "unused"}
                      </InlineTag>
                    </div>
                  </div>
                  <p className="tool-description">{selectedTool.description || "No description"}</p>
                  <div className="tool-detail-actions">
                    <button className="ghost-button" onClick={() => setSchemaToolName(selectedTool.name)}>
                      View Definition
                    </button>
                  </div>
                  {selectedToolCalls.length ? (
                    <section className="breakdown-card">
                      <div className="breakdown-title">Call arguments</div>
                      {selectedToolCalls.map((call, index) => (
                        <ToolCallView key={`${call.id || call.function?.name}-${index}`} call={call} match={selectedTool} CodeBlock={CodeBlock} InlineTag={InlineTag} />
                      ))}
                    </section>
                  ) : (
                    <EmptyState title="Tool not invoked" detail="This request declared the tool but did not execute a matching call." compact />
                  )}
                </>
              ) : null}
            </div>
          </div>
        ) : (
          <EmptyState title="No declared tools" detail="This request did not include tool definitions in its captured payload." />
        )}
      </section>
      {schemaTool ? (
        <div className="tool-modal-backdrop" role="presentation" onClick={() => setSchemaToolName("")}>
          <div className="tool-modal" role="dialog" aria-modal="true" aria-label={`${schemaTool.name} definition`} onClick={(event) => event.stopPropagation()}>
            <div className="tool-modal-head">
              <div>
                <p className="eyebrow">Tool definition</p>
                <h3>{schemaTool.name}</h3>
              </div>
              <button className="icon-button" onClick={() => setSchemaToolName("")} aria-label="Close tool definition">
                <span className="tool-modal-close">x</span>
              </button>
            </div>
            <div className="trace-tag-group">
              <InlineTag tone="accent">{schemaTool.source || schemaTool.type || "tool"}</InlineTag>
              <InlineTag>{buildToolSchemaSummary(schemaTool.parameters)}</InlineTag>
            </div>
            {schemaTool.description ? <p className="tool-description">{schemaTool.description}</p> : null}
            <CodeBlock value={schemaTool.parameters || "{}"} />
          </div>
        </div>
      ) : null}
    </>
  );
}

function ProtocolPanel({ observation, CodeBlock, InlineTag }) {
  if (observation.error) {
    return <EmptyState title="Protocol observation unavailable" detail={observation.error} tone="danger" />;
  }
  if (observation.loading && !observation.data) {
    return <EmptyState title="Loading protocol observation" detail="Reading derived semantic nodes for this trace." />;
  }
  const summary = observation.data?.summary;
  const tree = observation.data?.tree || [];
  if (!observation.data) {
    return <EmptyState title="No protocol observation" detail="Run analyze reparse for this trace to build Observation IR." />;
  }
  return (
    <section className="panel protocol-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Observation IR</p>
          <h2>Protocol</h2>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone={summary?.status === "parsed" ? "green" : "gold"}>{summary?.status || "unknown"}</InlineTag>
          <InlineTag>{summary?.parser || "parser"}</InlineTag>
          <InlineTag>{summary?.provider || "provider"}</InlineTag>
        </div>
      </div>
      <div className="detail-meta-strip">
        <DetailMetaPill label="model" value={summary?.model || "-"} />
        <DetailMetaPill label="operation" value={summary?.operation || "-"} />
        <DetailMetaPill label="parser" value={`${summary?.parser || "-"} ${summary?.parser_version || ""}`.trim()} />
      </div>
      {summary?.warnings ? (
        <CollapsibleCard title="Parser warnings" subtitle="tolerant parse notes" defaultOpen={false}>
          <CodeBlock value={JSON.stringify(summary.warnings, null, 2)} />
        </CollapsibleCard>
      ) : null}
      {tree.length ? (
        <div className="semantic-tree">
          {tree.map((node) => (
            <SemanticNodeView key={node.id} node={node} CodeBlock={CodeBlock} InlineTag={InlineTag} />
          ))}
        </div>
      ) : (
        <EmptyState title="No semantic nodes" detail="The parser completed without persisted semantic nodes." compact />
      )}
    </section>
  );
}

function SemanticNodeView({ node, CodeBlock, InlineTag }) {
  const hasChildren = Boolean(node.children?.length);
  const raw = node.raw ? JSON.stringify(node.raw, null, 2) : "";
  const body = (
    <>
      {node.text_preview ? <p className="semantic-node-preview">{node.text_preview}</p> : null}
      <div className="detail-meta-strip semantic-node-meta">
        <DetailMetaPill label="path" value={node.path || "-"} mono />
        <DetailMetaPill label="index" value={node.index ?? 0} />
      </div>
      {raw ? (
        <CollapsibleCard title="Raw node" subtitle={node.path || node.id} defaultOpen={false}>
          <CodeBlock value={raw} />
        </CollapsibleCard>
      ) : null}
      {hasChildren ? (
        <div className="semantic-children">
          {node.children.map((child) => (
            <SemanticNodeView key={child.id} node={child} CodeBlock={CodeBlock} InlineTag={InlineTag} />
          ))}
        </div>
      ) : null}
    </>
  );
  return (
    <article className="semantic-node">
      <div className="semantic-node-head">
        <div>
          <strong>{node.normalized_type || node.provider_type || "node"}</strong>
          <span className="mono">{node.id}</span>
        </div>
        <div className="trace-tag-group">
          <InlineTag tone="accent">{node.provider_type || "provider"}</InlineTag>
          {node.role ? <InlineTag>{node.role}</InlineTag> : null}
        </div>
      </div>
      {body}
    </article>
  );
}

function AuditPanel({ findings, InlineTag, CodeBlock }) {
  if (findings.error) {
    return <EmptyState title="Unable to load findings" detail={findings.error} tone="danger" />;
  }
  if (findings.loading && !findings.data) {
    return <EmptyState title="Loading findings" detail="Reading deterministic audit findings for this trace." />;
  }
  const items = findings.data?.items || [];
  return (
    <section className="panel audit-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Deterministic audit</p>
          <h2>Findings</h2>
        </div>
        <InlineTag tone={items.length ? "danger" : "green"}>{items.length} finding{items.length === 1 ? "" : "s"}</InlineTag>
      </div>
      {items.length ? (
        <div className="finding-list">
          {items.map((finding) => (
            <article key={finding.id} className="finding-card">
              <div className="finding-card-head">
                <div>
                  <strong>{finding.title || finding.category}</strong>
                  <span>{finding.description || finding.category}</span>
                </div>
                <div className="trace-tag-group">
                  <InlineTag tone={finding.severity === "high" || finding.severity === "critical" ? "danger" : "gold"}>{finding.severity}</InlineTag>
                  <InlineTag>{finding.category}</InlineTag>
                </div>
              </div>
              <div className="detail-meta-strip">
                <DetailMetaPill label="detector" value={`${finding.detector || "-"} ${finding.detector_version || ""}`.trim()} />
                <DetailMetaPill label="confidence" value={Number(finding.confidence || 0).toFixed(2)} />
                <DetailMetaPill label="node" value={finding.node_id || "-"} mono />
                <DetailMetaPill label="evidence" value={finding.evidence_path || "-"} mono />
              </div>
              {finding.evidence_excerpt ? <CodeBlock value={finding.evidence_excerpt} /> : null}
            </article>
          ))}
        </div>
      ) : (
        <EmptyState title="No findings" detail="No deterministic audit findings are stored for this trace." compact />
      )}
    </section>
  );
}

function PerformancePanel({ performance }) {
  if (performance.error) {
    return <EmptyState title="Unable to load performance" detail={performance.error} tone="danger" />;
  }
  if (performance.loading && !performance.data) {
    return <EmptyState title="Loading performance" detail="Reading trace-level latency and token metrics." />;
  }
  const perf = performance.data?.performance;
  if (!perf) {
    return <EmptyState title="No performance data" detail="This trace does not have indexed performance metrics." />;
  }
  return (
    <section className="panel performance-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Runtime metrics</p>
          <h2>Performance</h2>
        </div>
      </div>
      <section className="hero-grid">
        <StatCard label="Duration" value={formatDuration(perf.duration_ms || 0, { precise: true })} />
        <StatCard label="TTFT" value={formatDuration(perf.ttft_ms || 0, { precise: true })} />
        <StatCard label="Tokens / sec" value={Number(perf.tokens_per_sec || 0).toFixed(2)} accent="accent-green" />
        <StatCard label="Cache" value={`${Number(perf.cache_ratio || 0).toFixed(1)}%`} accent="accent-gold" />
      </section>
      <div className="detail-meta-strip">
        <DetailMetaPill label="status" value={perf.status_code || 0} />
        <DetailMetaPill label="total tokens" value={perf.total_tokens || 0} />
        <DetailMetaPill label="input" value={perf.prompt_tokens || 0} />
        <DetailMetaPill label="output" value={perf.completion_tokens || 0} />
        <DetailMetaPill label="cached" value={perf.cached_tokens || 0} />
        <DetailMetaPill label="stream" value={perf.is_stream ? "yes" : "no"} />
        <DetailMetaPill label="upstream" value={perf.selected_upstream_id || "-"} mono />
        <DetailMetaPill label="policy" value={perf.routing_policy || "-"} />
      </div>
      {perf.provider_error ? <pre className="trace-failure-detail">{perf.provider_error}</pre> : null}
    </section>
  );
}

function RawProtocolPanel({ raw, focusTarget = "" }) {
  const [wrap, setWrap] = useState(false);
  const requestRef = useRef(null);
  const responseRef = useRef(null);

  useEffect(() => {
    if (focusTarget === "request" && requestRef.current) {
      requestRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
      return;
    }
    if (focusTarget === "response" && responseRef.current) {
      responseRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }, [focusTarget]);

  if (raw.error) {
    return <EmptyState title="Unable to load raw protocol" detail={raw.error} tone="danger" />;
  }
  if (raw.loading && !raw.data) {
    return <EmptyState title="Loading raw protocol" detail="Fetching the original request and response exchange for this trace." />;
  }

  return (
    <section className="panel raw-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Raw HTTP exchange</p>
          <h2>Request / Response</h2>
        </div>
        <label className="wrap-toggle">
          <input type="checkbox" checked={wrap} onChange={(event) => setWrap(event.target.checked)} />
          Wrap lines
        </label>
      </div>
      <div className="raw-grid">
        <ProtocolColumn ref={requestRef} title="Request" value={raw.data?.request_protocol || ""} wrap={wrap} focused={focusTarget === "request"} />
        <ProtocolColumn ref={responseRef} title="Response" value={raw.data?.response_protocol || ""} wrap={wrap} focused={focusTarget === "response"} />
      </div>
    </section>
  );
}

function TimelinePanel({ events, focusTarget = "", CodeBlock, InlineTag }) {
  const panelRef = useRef(null);
  const focusPath = focusTarget === "timeline_error" ? findFirstTimelineErrorPath(events) : [];

  useEffect(() => {
    if ((focusTarget !== "timeline" && focusTarget !== "timeline_error") || !panelRef.current) {
      return;
    }
    panelRef.current.scrollIntoView({ block: "start", behavior: "smooth" });
  }, [focusTarget]);

  if (!events.length) {
    return <EmptyState title="No timeline events" detail="This trace does not include a structured llm event timeline." />;
  }

  return (
    <section ref={panelRef} className={focusTarget === "timeline" ? "panel timeline-panel timeline-panel-focused" : "panel timeline-panel"}>
      <div className="panel-head">
        <div>
          <p className="eyebrow">Provider timeline</p>
          <h2>Unified llm event stream</h2>
        </div>
      </div>
      <div className="timeline-list">
        {events.map((event, index) => (
          <article key={`${event.type}-${index}`} className="timeline-item">
            <div className="timeline-rail">
              <span className={event.type?.startsWith("llm.") ? "timeline-dot timeline-dot-live" : "timeline-dot"} />
            </div>
            <div className="timeline-card">
              <div className="timeline-head">
                <div>
                  <strong>{event.type || "event"}</strong>
                  <span>{formatDateTime(event.time)}</span>
                </div>
                <span className="timeline-badge">{event.is_stream ? "stream" : "record"}</span>
              </div>
              {event.timeline_items?.length ? <TimelineTree items={event.timeline_items} focusPath={focusPath} InlineTag={InlineTag} /> : null}
              {!event.timeline_items?.length && event.message ? <div className="timeline-message">{event.message}</div> : null}
              {event.attributes ? <CodeBlock value={JSON.stringify(event.attributes, null, 2)} /> : null}
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

function TimelineTree({ items, focusPath = [], InlineTag }) {
  return (
    <div className="timeline-tree">
      {items.map((item, index) => (
        <TimelineNode key={buildTimelineNodeKey(item, index)} nodeKey={buildTimelineNodeKey(item, index)} item={item} depth={0} focusPath={focusPath} InlineTag={InlineTag} />
      ))}
    </div>
  );
}

function TimelineNode({ item, depth = 0, nodeKey = "", focusPath = [], InlineTag }) {
  const nodeRef = useRef(null);
  const hasChildren = Boolean(item.children?.length);
  const hasDetails = Boolean(item.body && item.body !== item.summary);
  const collapsible = hasChildren || hasDetails;
  const focused = focusPath.includes(nodeKey);
  const focusedBranch = focusPath.length > 0 && focused;
  const className = `timeline-node timeline-node-${item.kind || "item"}${focused ? " timeline-node-focused" : ""}`;

  useEffect(() => {
    if (!focused || !nodeRef.current) {
      return;
    }
    nodeRef.current.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [focused]);

  if (!collapsible) {
    return (
      <div ref={nodeRef} className={className}>
        <div className="timeline-node-leaf">
          <TimelineNodeHeading item={item} />
          {item.id ? <span className="timeline-node-id">{item.id}</span> : null}
          {item.status === "error" ? <InlineTag tone="danger">error</InlineTag> : null}
        </div>
        {item.summary ? <div className="timeline-node-preview">{item.summary}</div> : null}
      </div>
    );
  }

  return (
    <details ref={nodeRef} className={className} open={(depth === 0 && hasChildren) || focusedBranch}>
      <summary className="timeline-node-summary">
        <TimelineNodeHeading item={item} />
        {item.id ? <span className="timeline-node-id">{item.id}</span> : null}
        {item.status === "error" ? <InlineTag tone="danger">error</InlineTag> : null}
      </summary>
      {item.summary ? <div className="timeline-node-preview">{item.summary}</div> : null}
      {hasDetails ? <pre className="timeline-node-body">{item.body}</pre> : null}
      {hasChildren ? (
        <div className="timeline-children">
          {item.children.map((child, index) => (
            <TimelineNode key={buildTimelineNodeKey(child, index)} nodeKey={buildTimelineNodeKey(child, index)} item={child} depth={depth + 1} focusPath={focusPath} InlineTag={InlineTag} />
          ))}
        </div>
      ) : null}
    </details>
  );
}

function TimelineNodeHeading({ item }) {
  return (
    <div className="timeline-node-heading">
      <span className="timeline-node-kind">{formatTimelineKind(item.kind)}</span>
      <strong className="timeline-node-title">{formatTimelineTitle(item)}</strong>
    </div>
  );
}

function PayloadSummary({ raw, CodeBlock }) {
  const requestBody = extractHTTPBody(raw.data?.request_protocol || "");
  const responseBody = extractHTTPBody(raw.data?.response_protocol || "");

  return (
    <div className="payload-grid">
      <section className="payload-card">
        <div className="protocol-head">Request body</div>
        <CodeBlock value={formatBodyForDisplay(requestBody)} />
      </section>
      <section className="payload-card">
        <div className="protocol-head">Response body</div>
        <CodeBlock value={formatBodyForDisplay(responseBody)} />
      </section>
    </div>
  );
}

const ProtocolColumn = React.forwardRef(function ProtocolColumn({ title, value, wrap, focused = false }, ref) {
  return (
    <div ref={ref} className={focused ? "protocol-column protocol-column-focused" : "protocol-column"}>
      <div className="protocol-head">{title}</div>
      <pre className={wrap ? "protocol-code protocol-code-wrap" : "protocol-code"}>{value}</pre>
    </div>
  );
});

function MessageCard({ message, renderMarkdown, declaredTools = [], CollapsibleCard, CodeBlock, InlineTag, MessageContent }) {
  const alignClass = message.role === "assistant" ? "message-assistant" : message.role === "tool" ? "message-tool" : "message-user";
  const isCollapsible = message.message_type === "tool_use" || message.message_type === "tool_result";
  const toolSummary = buildToolMessageSummary(message, declaredTools);

  const body = (
    <article className={`message-card ${alignClass}`}>
      <div className="message-meta">
        <span className="role-pill">{message.role}</span>
        <span className="message-kind">{message.message_type || "message"}</span>
      </div>
      {toolSummary ? <div className="tool-message-summary">{toolSummary}</div> : null}
      {message.content ? (
        <MessageContent value={message.content} format={message.content_format} renderMarkdown={renderMarkdown} className="message-body" />
      ) : null}
      {message.tool_calls?.length ? message.tool_calls.map((call) => (
        <ToolCallView key={call.id || call.function?.name} call={call} match={findDeclaredToolForCall(call, declaredTools)} CodeBlock={CodeBlock} InlineTag={InlineTag} />
      )) : null}
      {message.blocks?.length ? message.blocks.map((block, index) => <BlockView key={`${block.kind}-${index}`} block={block} CodeBlock={CodeBlock} />) : null}
      {!message.content && !message.tool_calls?.length && !message.blocks?.length ? (
        <div className="tool-message-placeholder">No structured payload was captured for this tool event.</div>
      ) : null}
    </article>
  );

  if (!isCollapsible) {
    return body;
  }

  return (
    <CollapsibleCard title={`${message.role} / ${message.message_type}`} subtitle={toolSummary || message.name || message.tool_call_id || ""} defaultOpen={false} bodyClassName="collapse-plain">
      {body}
    </CollapsibleCard>
  );
}

function ToolCallView({ call, match = null, CodeBlock, InlineTag }) {
  return (
    <div className="tool-call-box">
      <div className="tool-call-head">
        <div className="tool-call-title">{call.function?.name || "tool"}</div>
        {match?.name ? <InlineTag tone="accent">declared</InlineTag> : null}
      </div>
      {call.id ? <div className="tool-call-meta">call id {call.id}</div> : null}
      <CodeBlock value={call.function?.arguments || "{}"} />
    </div>
  );
}

function BlockView({ block, CodeBlock }) {
  return (
    <div className="tool-call-box">
      <div className="tool-call-title">{block.title || block.kind}</div>
      <CodeBlock value={block.text || block.meta || ""} />
    </div>
  );
}

function extractHTTPBody(value = "") {
  if (!value) {
    return "";
  }
  const separator = value.includes("\r\n\r\n") ? "\r\n\r\n" : "\n\n";
  const index = value.indexOf(separator);
  if (index === -1) {
    return value;
  }
  return value.slice(index + separator.length);
}

function formatBodyForDisplay(value = "") {
  const trimmed = String(value || "").trim();
  if (!trimmed) {
    return "(empty)";
  }
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return trimmed;
  }
}

function formatTimelineKind(kind = "") {
  switch (kind) {
    case "message":
      return "message";
    case "tool_call":
      return "tool call";
    case "tool_response":
      return "tool response";
    case "thinking":
      return "thinking";
    case "output":
      return "output";
    default:
      return kind || "item";
  }
}

function formatTimelineTitle(item = {}) {
  if (item.kind === "message") {
    return item.label || item.role || "Message";
  }
  return item.name || item.label || formatTimelineKind(item.kind);
}

function buildTimelineNodeKey(item = {}, index = 0) {
  return `${item.kind || "item"}-${item.id || item.name || item.label || index}`;
}

function findFirstTimelineErrorPath(events = []) {
  for (const event of events) {
    const path = findTimelineItemErrorPath(event.timeline_items || []);
    if (path.length) {
      return path;
    }
  }
  return [];
}

function findTimelineItemErrorPath(items = []) {
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    const nodeKey = buildTimelineNodeKey(item, index);
    if (item.status === "error") {
      return [nodeKey];
    }
    const childPath = findTimelineItemErrorPath(item.children || []);
    if (childPath.length) {
      return [nodeKey, ...childPath];
    }
  }
  return [];
}

function hasConversation(detail) {
  return Boolean(
    detail?.messages?.length ||
      detail?.ai_content ||
      detail?.ai_reasoning ||
      detail?.ai_blocks?.length ||
      detail?.tool_calls?.length
  );
}
