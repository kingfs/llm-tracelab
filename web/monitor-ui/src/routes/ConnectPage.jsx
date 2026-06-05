import React from "react";

const baseOrigin = () => {
  if (typeof window === "undefined") {
    return "http://localhost:8080";
  }
  return window.location.origin;
};

export function ConnectPage() {
  const origin = baseOrigin();
  const token = "${LLM_TRACELAB_TOKEN}";
  const examples = [
    {
      title: "OpenAI-compatible Chat Completions",
      baseURL: `${origin}/v1`,
      endpoint: "/v1/chat/completions",
      detail: "Use this for traditional OpenAI-compatible SDKs and gateways.",
      curl: `curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer ${token}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"ping"}],"max_completion_tokens":64}'`,
    },
    {
      title: "OpenAI Responses / Codex",
      baseURL: `${origin}/responses`,
      endpoint: "/responses",
      detail: "Use this for clients that speak the OpenAI Responses API. /v1/responses remains supported.",
      curl: `curl ${origin}/responses \\
  -H "Authorization: Bearer ${token}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"gpt-5.1-codex","input":"ping"}'`,
    },
    {
      title: "Anthropic Messages / Claude Code",
      baseURL: `${origin}/anthropic`,
      endpoint: "/anthropic/messages",
      detail: "Use this for Anthropic Messages clients. Requests route only to Anthropic-capable providers.",
      curl: `curl ${origin}/anthropic/messages \\
  -H "Authorization: Bearer ${token}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"ping"}]}],"max_tokens":64}'`,
    },
  ];

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Client setup</p>
          <h1>Connect</h1>
        </div>
        <div className="topbar-meta">
          <span className="badge">{origin}</span>
        </div>
      </header>

      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Protocol entrypoints</p>
            <h2>Choose the API shape your client speaks</h2>
          </div>
        </div>
        <div className="provider-entry-grid">
          {examples.map((item) => (
            <article className="provider-entry-card" key={item.title}>
              <div>
                <p className="eyebrow">{item.endpoint}</p>
                <h3>{item.title}</h3>
              </div>
              <p className="trace-subline">{item.detail}</p>
              <div className="detail-meta-strip">
                <span className="detail-meta-pill">
                  <span className="detail-meta-label">base url</span>
                  <strong className="mono">{item.baseURL}</strong>
                </span>
              </div>
              <pre className="code-block connect-code">{item.curl}</pre>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
