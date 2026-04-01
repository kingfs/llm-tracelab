import React, { startTransition, useEffect, useState } from "react";
import { Link, Route, Routes, useNavigate, useParams, useSearchParams } from "react-router-dom";

const REFRESH_MS = 60_000;
const PAGE_SIZE = 50;

function useJSON(url, deps = []) {
  const [state, setState] = useState({ loading: true, data: null, error: "" });

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    startTransition(() => {
      setState((current) => ({ ...current, loading: true, error: "" }));
    });

    fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) {
          const payload = await response.json().catch(() => ({}));
          throw new Error(payload.error || `request failed: ${response.status}`);
        }
        return response.json();
      })
      .then((data) => {
        if (cancelled) {
          return;
        }
        startTransition(() => {
          setState({ loading: false, data, error: "" });
        });
      })
      .catch((error) => {
        if (cancelled || error.name === "AbortError") {
          return;
        }
        startTransition(() => {
          setState({ loading: false, data: null, error: error.message || "unknown error" });
        });
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [url, ...deps]);

  return state;
}

function App() {
  return (
    <Routes>
      <Route path="/" element={<TraceListPage />} />
      <Route path="/traces/:traceID" element={<TraceDetailPage />} />
    </Routes>
  );
}

function TraceListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(1, Number(searchParams.get("page") || "1"));
  const [refreshTick, setRefreshTick] = useState(0);
  const { loading, data, error } = useJSON(`/api/traces?page=${page}&page_size=${PAGE_SIZE}`, [page, refreshTick]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setRefreshTick((tick) => tick + 1);
    }, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, []);

  const items = data?.items ?? [];
  const stats = data?.stats ?? {};

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Local First LLM Replay Proxy</p>
          <h1>Trace Monitor</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge badge-live">refresh / 60s</span>
          <span className="badge">{data?.refreshed_at ? formatTime(data.refreshed_at) : "..."}</span>
        </div>
      </header>

      <section className="hero-grid">
        <StatCard label="Total" value={stats.total_request ?? 0} />
        <StatCard label="Avg TTFT" value={`${stats.avg_ttft ?? 0} ms`} />
        <StatCard label="Tokens" value={stats.total_tokens ?? 0} accent="accent-gold" />
        <StatCard label="Success" value={`${Number(stats.success_rate ?? 0).toFixed(1)}%`} accent="accent-green" />
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Recent traffic</p>
            <h2>Latest 50 traces</h2>
          </div>
          <div className="pager">
            <button className="ghost-button" disabled={page <= 1} onClick={() => setSearchParams({ page: String(page - 1) })}>
              Previous
            </button>
            <span className="pager-label">
              {data?.page ?? page} / {Math.max(data?.total_pages ?? 1, 1)}
            </span>
            <button
              className="ghost-button"
              disabled={!data || page >= (data.total_pages || 1)}
              onClick={() => setSearchParams({ page: String(page + 1) })}
            >
              Next
            </button>
          </div>
        </div>

        {error ? <div className="empty-state error-box">{error}</div> : null}
        {loading && !data ? <div className="empty-state">Loading traces...</div> : null}

        <div className="trace-table">
          <div className="trace-table-head">
            <span>Model</span>
            <span>Status</span>
            <span>Latency</span>
            <span>Tokens</span>
            <span>Actions</span>
          </div>
          {items.map((item) => (
            <article key={item.id} className="trace-row">
              <div>
                <div className="trace-title-row">
                  <strong className="trace-model-name">{item.model || "unknown-model"}</strong>
                  <div className="trace-tag-group">
                    <InlineTag tone="accent">{formatEndpointTag(item.endpoint || item.operation)}</InlineTag>
                    <InlineTag>{formatProviderTag(item.provider)}</InlineTag>
                    {item.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
                  </div>
                </div>
                <span className="trace-subline">{formatDateTime(item.recorded_at)}</span>
              </div>
              <div className="trace-metric-stack">
                <strong className={item.status_code >= 200 && item.status_code < 300 ? "status-ok" : "status-err"}>
                  {item.status_code}
                </strong>
                <span>{item.method || "POST"}</span>
              </div>
              <div className="trace-metric-stack">
                <strong>{item.duration_ms} ms</strong>
                <span>ttft {item.ttft_ms} ms</span>
              </div>
              <div>
                <div className="token-inline-row">
                  <MiniToken metric="in" value={item.prompt_tokens} tone="accent" icon="input" />
                  <MiniToken metric="out" value={item.completion_tokens} tone="green" icon="output" />
                  <MiniToken metric="total" value={item.total_tokens} tone="default" icon="total" />
                  <MiniToken metric="cached" value={item.cached_tokens} tone="gold" icon="cached" />
                </div>
              </div>
              <div className="action-group">
                <Link className="icon-button" to={`/traces/${item.id}`} title="View trace" aria-label="View trace">
                  <ViewIcon />
                </Link>
                <a className="icon-button" href={`/api/traces/${item.id}/download`} title="Download .http" aria-label="Download trace">
                  <DownloadIcon />
                </a>
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}

function TraceDetailPage() {
  const { traceID = "" } = useParams();
  const [tab, setTab] = useState("timeline");
  const [renderMarkdown, setRenderMarkdown] = useState(true);
  const detail = useJSON(`/api/traces/${traceID}`, [traceID]);
  const raw = useJSON(`/api/traces/${traceID}/raw`, [traceID, tab === "raw" ? "raw" : "summary"]);
  const header = detail.data?.header?.meta;
  const usage = detail.data?.header?.usage;
  const hasDeclaredToolsTab = Boolean(detail.data?.tool_calls?.length && detail.data?.tools?.length);

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div className="detail-title-block">
          <div className="detail-heading-row">
            <h1>{header?.model || "trace detail"}</h1>
            <div className="trace-tag-group detail-tag-group">
              <InlineTag tone="accent">{formatEndpointTag(header?.endpoint || header?.operation)}</InlineTag>
              <InlineTag>{formatProviderTag(header?.provider)}</InlineTag>
              {detail.data?.header?.layout?.is_stream ? <InlineTag tone="gold">stream</InlineTag> : null}
              <InlineTag tone={header?.status_code >= 200 && header?.status_code < 300 ? "green" : "danger"}>{header?.status_code || 0}</InlineTag>
            </div>
          </div>
          <div className="detail-meta-strip">
            <DetailMetaPill label="time" value={formatDateTime(header?.time)} />
            <DetailMetaPill label="endpoint" value={header?.endpoint || header?.url || "-"} />
            <DetailMetaPill label="duration" value={`${header?.duration_ms || 0} ms`} />
            <DetailMetaPill label="ttft" value={`${header?.ttft_ms || 0} ms`} />
            <DetailMetaPill label="request id" value={header?.request_id || "-"} mono />
          </div>
        </div>
        <div className="topbar-meta detail-toolbar">
          <div className="detail-toolbar-actions">
            <Link className="icon-button" to="/" title="Back to list" aria-label="Back to list">
              <HomeIcon />
            </Link>
            <a className="icon-button" href={`/api/traces/${traceID}/download`} title="Download .http" aria-label="Download trace">
              <DownloadIcon />
            </a>
          </div>
          <div className="detail-toolbar-tokens">
            <TokenBadge label="in" value={usage?.prompt_tokens || 0} icon="input" />
            <TokenBadge label="out" value={usage?.completion_tokens || 0} icon="output" />
            <TokenBadge label="total" value={usage?.total_tokens || 0} icon="total" accent="token-badge-strong" />
            <TokenBadge label="cached" value={usage?.prompt_token_details?.cached_tokens || 0} icon="cached" />
          </div>
        </div>
      </header>

      <nav className="detail-tabs">
        <button className={tab === "timeline" ? "tab active" : "tab"} onClick={() => setTab("timeline")}>
          Timeline
        </button>
        <button className={tab === "summary" ? "tab active" : "tab"} onClick={() => setTab("summary")}>
          Summary
        </button>
        <button className={tab === "raw" ? "tab active" : "tab"} onClick={() => setTab("raw")}>
          Raw Protocol
        </button>
        {hasDeclaredToolsTab ? (
          <button className={tab === "tools" ? "tab active" : "tab"} onClick={() => setTab("tools")}>
            Declared Tools
          </button>
        ) : null}
      </nav>

      {detail.error ? <div className="empty-state error-box">{detail.error}</div> : null}
      {detail.loading && !detail.data ? <div className="empty-state">Loading trace...</div> : null}

      {tab === "timeline" && detail.data ? <TimelinePanel events={detail.data.events || []} /> : null}

      {tab === "summary" && detail.data ? (
        <div className="detail-grid">
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
                  <MessageCard key={`${message.role}-${index}`} message={message} renderMarkdown={renderMarkdown} />
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
                      <ToolCallView key={call.id || call.function?.name} call={call} />
                    ))}
                  </CollapsibleCard>
                ) : null}
                {detail.data.ai_blocks?.length ? (
                  <CollapsibleCard title="Output Blocks" subtitle={`${detail.data.ai_blocks.length} block(s)`} defaultOpen={false}>
                    {detail.data.ai_blocks.map((block, index) => (
                      <BlockView key={`${block.kind}-${index}`} block={block} />
                    ))}
                  </CollapsibleCard>
                ) : null}
              </div>
            ) : (
              <PayloadSummary raw={raw} />
            )}
          </section>
        </div>
      ) : null}

      {tab === "raw" ? <RawProtocolPanel raw={raw} /> : null}
      {tab === "tools" && detail.data ? <DeclaredToolsPanel tools={detail.data.tools || []} /> : null}
    </div>
  );
}

function DeclaredToolsPanel({ tools }) {
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Declared tools</p>
          <h2>Request tools</h2>
        </div>
      </div>
      {tools.length ? (
        tools.map((tool, index) => (
          <CollapsibleCard key={`${tool.name}-${index}`} title={tool.name} subtitle={tool.source || tool.type} defaultOpen={false}>
            <p className="tool-description">{tool.description || "No description"}</p>
            <CodeBlock value={tool.parameters || "{}"} />
          </CollapsibleCard>
        ))
      ) : (
        <div className="empty-state">No tool definitions in request.</div>
      )}
    </section>
  );
}

function RawProtocolPanel({ raw }) {
  const [wrap, setWrap] = useState(false);

  if (raw.error) {
    return <div className="empty-state error-box">{raw.error}</div>;
  }
  if (raw.loading && !raw.data) {
    return <div className="empty-state">Loading raw protocol...</div>;
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
        <ProtocolColumn title="Request" value={raw.data?.request_protocol || ""} wrap={wrap} />
        <ProtocolColumn title="Response" value={raw.data?.response_protocol || ""} wrap={wrap} />
      </div>
    </section>
  );
}

function TimelinePanel({ events }) {
  if (!events.length) {
    return <div className="empty-state">No timeline events recorded for this trace.</div>;
  }

  return (
    <section className="panel timeline-panel">
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
              {event.timeline_items?.length ? <TimelineTree items={event.timeline_items} /> : null}
              {!event.timeline_items?.length && event.message ? <div className="timeline-message">{event.message}</div> : null}
              {event.attributes ? <CodeBlock value={JSON.stringify(event.attributes, null, 2)} /> : null}
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

function TimelineTree({ items }) {
  return (
    <div className="timeline-tree">
      {items.map((item, index) => (
        <TimelineNode key={`${item.kind}-${item.id || item.name || item.label || index}`} item={item} depth={0} />
      ))}
    </div>
  );
}

function TimelineNode({ item, depth = 0 }) {
  const hasChildren = Boolean(item.children?.length);
  const hasDetails = Boolean(item.body && item.body !== item.summary);
  const collapsible = hasChildren || hasDetails;
  const className = `timeline-node timeline-node-${item.kind || "item"}`;

  if (!collapsible) {
    return (
      <div className={className}>
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
    <details className={className} open={depth === 0 && hasChildren}>
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
            <TimelineNode key={`${child.kind}-${child.id || child.name || child.label || index}`} item={child} depth={depth + 1} />
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

function PayloadSummary({ raw }) {
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

function ProtocolColumn({ title, value, wrap }) {
  return (
    <div className="protocol-column">
      <div className="protocol-head">{title}</div>
      <pre className={wrap ? "protocol-code protocol-code-wrap" : "protocol-code"}>{value}</pre>
    </div>
  );
}

function InlineTag({ children, tone = "default" }) {
  return <span className={`inline-tag inline-tag-${tone}`}>{children}</span>;
}

function MiniToken({ metric, value, tone = "default", icon = "total" }) {
  return (
    <span className={`mini-token mini-token-${tone}`}>
      <span className="metric-icon-wrap">
        <MetricIcon type={icon} />
      </span>
      <span className="mini-token-label">{metric}</span>
      <strong>{value || 0}</strong>
    </span>
  );
}

function TokenBadge({ label, value, accent = "", icon = "total" }) {
  return (
    <span className={`badge token-badge ${accent}`.trim()}>
      <span className="metric-icon-wrap token-badge-icon">
        <MetricIcon type={icon} />
      </span>
      <span className="token-badge-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function DetailMetaPill({ label, value, mono = false }) {
  return (
    <span className={`detail-meta-pill ${mono ? "mono" : ""}`.trim()}>
      <span className="detail-meta-label">{label}</span>
      <strong>{value}</strong>
    </span>
  );
}

function IconFrame({ children }) {
  return <span className="icon-frame">{children}</span>;
}

function MetricIcon({ type = "total" }) {
  if (type === "input") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M14 3.5h-4.5M14 12.5h-4.5M6 8H14" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <path d="m6.5 4.5-3.5 3.5 3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (type === "output") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M2 3.5h4.5M2 12.5h4.5M2 8H10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        <path d="m9.5 4.5 3.5 3.5-3.5 3.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (type === "cached") {
    return (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path d="M5 5.5h7v7H5z" fill="none" stroke="currentColor" strokeWidth="1.3" />
        <path d="M3.5 3.5h7v7" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 4.5h10M3 8h10M3 11.5h10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

function ViewIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M2.5 12s3.4-6 9.5-6 9.5 6 9.5 6-3.4 6-9.5 6-9.5-6-9.5-6Z" fill="none" stroke="currentColor" strokeWidth="1.8" />
        <circle cx="12" cy="12" r="3.2" fill="none" stroke="currentColor" strokeWidth="1.8" />
      </svg>
    </IconFrame>
  );
}

function DownloadIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 4v10" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        <path d="m8 11.5 4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M5 19h14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    </IconFrame>
  );
}

function HomeIcon() {
  return (
    <IconFrame>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M4 11.5 12 5l8 6.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M7.5 10.5V19h9v-8.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </IconFrame>
  );
}

function MessageCard({ message, renderMarkdown }) {
  const alignClass = message.role === "assistant" ? "message-assistant" : message.role === "tool" ? "message-tool" : "message-user";
  const isCollapsible = message.message_type === "tool_use" || message.message_type === "tool_result";

  const body = (
    <article className={`message-card ${alignClass}`}>
      <div className="message-meta">
        <span className="role-pill">{message.role}</span>
        <span className="message-kind">{message.message_type || "message"}</span>
      </div>
      {message.content ? (
        <MessageContent
          value={message.content}
          format={message.content_format}
          renderMarkdown={renderMarkdown}
          className="message-body"
        />
      ) : null}
      {message.tool_calls?.length ? message.tool_calls.map((call) => <ToolCallView key={call.id || call.function?.name} call={call} />) : null}
      {message.blocks?.length ? message.blocks.map((block, index) => <BlockView key={`${block.kind}-${index}`} block={block} />) : null}
    </article>
  );

  if (!isCollapsible) {
    return body;
  }

  return (
    <CollapsibleCard
      title={`${message.role} / ${message.message_type}`}
      subtitle={message.name || message.tool_call_id || ""}
      defaultOpen={false}
      bodyClassName="collapse-plain"
    >
      {body}
    </CollapsibleCard>
  );
}

function ToolCallView({ call }) {
  return (
    <div className="tool-call-box">
      <div className="tool-call-title">{call.function?.name || "tool"}</div>
      <CodeBlock value={call.function?.arguments || "{}"} />
    </div>
  );
}

function BlockView({ block }) {
  return (
    <div className="tool-call-box">
      <div className="tool-call-title">{block.title || block.kind}</div>
      <CodeBlock value={block.text || block.meta || ""} />
    </div>
  );
}

function CollapsibleCard({ title, subtitle, defaultOpen = false, children, bodyClassName = "" }) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <section className="collapse-card">
      <button className="collapse-head" onClick={() => setOpen((value) => !value)}>
        <div>
          <strong>{title}</strong>
          {subtitle ? <span>{subtitle}</span> : null}
        </div>
        <span>{open ? "hide" : "show"}</span>
      </button>
      {open ? <div className={`collapse-body ${bodyClassName}`.trim()}>{children}</div> : null}
    </section>
  );
}

function StatCard({ label, value, accent = "", detail = "", mono = false }) {
  return (
    <article className={`stat-card ${accent}`.trim()}>
      <span>{label}</span>
      <strong className={mono ? "mono" : ""}>{value}</strong>
      {detail ? <small className={mono ? "mono stat-detail" : "stat-detail"}>{detail}</small> : null}
    </article>
  );
}

function CodeBlock({ value }) {
  return <pre className="code-block">{value}</pre>;
}

function MessageContent({ value, format, renderMarkdown, className = "" }) {
  if (renderMarkdown && format === "markdown") {
    return <MarkdownBlock value={value} className={className} />;
  }
  return <div className={`${className} prose-block`.trim()}>{value}</div>;
}

function MarkdownBlock({ value, className = "" }) {
  return <div className={`${className} prose-block rendered-markdown`.trim()} dangerouslySetInnerHTML={{ __html: renderMarkdownToHTML(value) }} />;
}

function renderMarkdownToHTML(input) {
  if (!input) {
    return "";
  }

  const codeBlocks = [];
  const placeholderPrefix = "__LLM_TRACELAB_CODE_BLOCK_";
  let text = String(input).replace(/\r\n/g, "\n");

  text = text.replace(/```([\w-]+)?\n([\s\S]*?)```/g, (_, language = "", code = "") => {
    const html = `<pre class="md-pre"><code${language ? ` data-lang="${escapeHTML(language)}"` : ""}>${escapeHTML(code.trimEnd())}</code></pre>`;
    const token = `${placeholderPrefix}${codeBlocks.length}__`;
    codeBlocks.push(html);
    return token;
  });

  const blocks = text
    .split(/\n{2,}/)
    .map((block) => block.trim())
    .filter(Boolean)
    .map((block) => renderMarkdownBlock(block, placeholderPrefix));

  let html = blocks.join("");
  codeBlocks.forEach((codeBlock, index) => {
    html = html.replace(`${placeholderPrefix}${index}__`, codeBlock);
  });
  return html;
}

function renderMarkdownBlock(block, placeholderPrefix) {
  if (block.startsWith(placeholderPrefix)) {
    return block;
  }

  const lines = block.split("\n");
  if (lines.every((line) => /^>\s?/.test(line))) {
    const content = lines.map((line) => renderMarkdownInline(line.replace(/^>\s?/, ""))).join("<br />");
    return `<blockquote>${content}</blockquote>`;
  }
  if (lines.every((line) => /^[-*]\s+/.test(line))) {
    return `<ul>${lines.map((line) => `<li>${renderMarkdownInline(line.replace(/^[-*]\s+/, ""))}</li>`).join("")}</ul>`;
  }
  if (lines.every((line) => /^\d+\.\s+/.test(line))) {
    return `<ol>${lines.map((line) => `<li>${renderMarkdownInline(line.replace(/^\d+\.\s+/, ""))}</li>`).join("")}</ol>`;
  }

  const heading = block.match(/^(#{1,6})\s+(.+)$/);
  if (heading) {
    const level = Math.min(heading[1].length, 6);
    return `<h${level}>${renderMarkdownInline(heading[2])}</h${level}>`;
  }

  return `<p>${lines.map((line) => renderMarkdownInline(line)).join("<br />")}</p>`;
}

function renderMarkdownInline(text) {
  let html = escapeHTML(text);
  html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
  html = html.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|mailto:[^\s)]+)\)/g, (_, label, href) => {
    const safeHref = escapeHTML(href);
    return `<a href="${safeHref}" target="_blank" rel="noreferrer">${label}</a>`;
  });
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  html = html.replace(/(^|[\s(])\*([^*]+)\*(?=[\s).,!?:;]|$)/g, "$1<em>$2</em>");
  html = html.replace(/(^|[\s(])_([^_]+)_(?=[\s).,!?:;]|$)/g, "$1<em>$2</em>");
  return html;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function hasConversation(detail) {
  return Boolean(
    detail.messages?.length ||
      detail.ai_content ||
      detail.ai_reasoning ||
      detail.ai_blocks?.length ||
      detail.tool_calls?.length
  );
}

function extractHTTPBody(protocol = "") {
  if (!protocol) {
    return "";
  }
  const separator = protocol.includes("\r\n\r\n") ? "\r\n\r\n" : "\n\n";
  const index = protocol.indexOf(separator);
  if (index === -1) {
    return protocol;
  }
  return protocol.slice(index + separator.length);
}

function formatBodyForDisplay(value = "") {
  const trimmed = String(value).trim();
  if (!trimmed) {
    return "(empty)";
  }
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return trimmed;
  }
}

function formatEndpointTag(value = "") {
  const endpoint = String(value || "").toLowerCase();
  if (endpoint.includes("/v1/chat/completions")) {
    return "chat";
  }
  if (endpoint.includes("/v1/responses")) {
    return "resp";
  }
  if (endpoint.includes("/v1/messages")) {
    return "msg";
  }
  if (endpoint.includes("/v1/models")) {
    return "models";
  }
  return value || "api";
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

function formatProviderTag(value = "") {
  if (!value) {
    return "unknown";
  }
  if (value === "openai_compatible") {
    return "openai";
  }
  return String(value).replaceAll("_", " ");
}

function formatDateTime(value) {
  if (!value) {
    return "-";
  }
  return new Date(value).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatTime(value) {
  if (!value) {
    return "-";
  }
  return new Date(value).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export default App;
