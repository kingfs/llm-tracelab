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
  const navigate = useNavigate();
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
            <span>Trace</span>
            <span>API</span>
            <span>Status</span>
            <span>Latency</span>
            <span>Tokens</span>
            <span>Open</span>
          </div>
          {items.map((item) => (
            <button key={item.id} className="trace-row" onClick={() => navigate(`/traces/${item.id}`)}>
              <div>
                <strong>{item.model || "unknown-model"}</strong>
                <span>{formatDateTime(item.recorded_at)}</span>
              </div>
              <div>
                <strong>{item.operation || item.endpoint}</strong>
                <span>{item.provider}</span>
              </div>
              <div>
                <strong className={item.status_code >= 200 && item.status_code < 300 ? "status-ok" : "status-err"}>
                  {item.status_code}
                </strong>
                <span>{item.is_stream ? "stream" : "json"}</span>
              </div>
              <div>
                <strong>{item.duration_ms} ms</strong>
                <span>ttft {item.ttft_ms} ms</span>
              </div>
              <div>
                <strong>{item.total_tokens}</strong>
                <span>
                  in {item.prompt_tokens} / out {item.completion_tokens}
                </span>
              </div>
              <div className="row-link">view</div>
            </button>
          ))}
        </div>
      </section>
    </div>
  );
}

function TraceDetailPage() {
  const { traceID = "" } = useParams();
  const [tab, setTab] = useState("timeline");
  const detail = useJSON(`/api/traces/${traceID}`, [traceID]);
  const raw = useJSON(`/api/traces/${traceID}/raw`, [traceID, tab === "raw" ? "raw" : "summary"]);
  const header = detail.data?.header?.meta;
  const usage = detail.data?.header?.usage;

  return (
    <div className="shell shell-detail">
      <header className="topbar detail-topbar">
        <div>
          <Link to="/" className="inline-link">
            overview
          </Link>
          <h1>{header?.model || "trace detail"}</h1>
          <p className="detail-subtitle">
            {header?.provider || "unknown"} / {header?.operation || header?.endpoint || "unknown"} / {header?.status_code || 0}
          </p>
        </div>
        <div className="topbar-meta">
          <a className="ghost-button" href={`/api/traces/${traceID}/download`}>
            download
          </a>
          <span className="badge">{usage?.total_tokens || 0} tokens</span>
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
      </nav>

      {detail.error ? <div className="empty-state error-box">{detail.error}</div> : null}
      {detail.loading && !detail.data ? <div className="empty-state">Loading trace...</div> : null}

      {tab === "timeline" && detail.data ? <TimelinePanel events={detail.data.events || []} /> : null}

      {tab === "summary" && detail.data ? (
        <div className="detail-grid">
          <section className="panel panel-span-2">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Conversation</p>
                <h2>Request and response</h2>
              </div>
            </div>
            <div className="message-list">
              {detail.data.messages.map((message, index) => (
                <MessageCard key={`${message.role}-${index}`} message={message} />
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
                  <div className="message-body prose-block">{detail.data.ai_content}</div>
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
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Trace meta</p>
                <h2>Header</h2>
              </div>
            </div>
            <dl className="meta-list">
              <MetaRow label="Time" value={formatDateTime(header?.time)} />
              <MetaRow label="Endpoint" value={header?.endpoint || header?.url} />
              <MetaRow label="Duration" value={`${header?.duration_ms || 0} ms`} />
              <MetaRow label="TTFT" value={`${header?.ttft_ms || 0} ms`} />
              <MetaRow label="Request ID" value={header?.request_id} mono />
            </dl>
          </section>

          <section className="panel">
            <div className="panel-head">
              <div>
                <p className="eyebrow">Declared tools</p>
                <h2>Request tools</h2>
              </div>
            </div>
            {detail.data.tools?.length ? (
              detail.data.tools.map((tool, index) => (
                <CollapsibleCard key={`${tool.name}-${index}`} title={tool.name} subtitle={tool.source || tool.type} defaultOpen={false}>
                  <p className="tool-description">{tool.description || "No description"}</p>
                  <CodeBlock value={tool.parameters || "{}"} />
                </CollapsibleCard>
              ))
            ) : (
              <div className="empty-state">No tool definitions in request.</div>
            )}
          </section>
        </div>
      ) : null}

      {tab === "raw" ? <RawProtocolPanel raw={raw} /> : null}
    </div>
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
              {event.message ? <div className="timeline-message">{event.message}</div> : null}
              {event.attributes ? <CodeBlock value={JSON.stringify(event.attributes, null, 2)} /> : null}
            </div>
          </article>
        ))}
      </div>
    </section>
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

function MessageCard({ message }) {
  const alignClass = message.role === "assistant" ? "message-assistant" : message.role === "tool" ? "message-tool" : "message-user";
  const isCollapsible = message.message_type === "tool_use" || message.message_type === "tool_result";

  const body = (
    <article className={`message-card ${alignClass}`}>
      <div className="message-meta">
        <span className="role-pill">{message.role}</span>
        <span className="message-kind">{message.message_type || "message"}</span>
      </div>
      {message.content ? <div className="message-body prose-block">{message.content}</div> : null}
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

function MetaRow({ label, value, mono = false }) {
  return (
    <>
      <dt>{label}</dt>
      <dd className={mono ? "mono" : ""}>{value || "-"}</dd>
    </>
  );
}

function StatCard({ label, value, accent = "" }) {
  return (
    <article className={`stat-card ${accent}`.trim()}>
      <span>{label}</span>
      <strong>{value}</strong>
    </article>
  );
}

function CodeBlock({ value }) {
  return <pre className="code-block">{value}</pre>;
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
