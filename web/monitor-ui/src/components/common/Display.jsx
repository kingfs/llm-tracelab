import React, { useState } from "react";

export function CollapsibleCard({ title, subtitle, defaultOpen = false, children, bodyClassName = "" }) {
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

export function StatCard({ label, value, accent = "", detail = "", mono = false }) {
  return (
    <article className={`stat-card ${accent}`.trim()}>
      <span>{label}</span>
      <strong className={mono ? "mono" : ""}>{value}</strong>
      {detail ? <small className={mono ? "mono stat-detail" : "stat-detail"}>{detail}</small> : null}
    </article>
  );
}

export function CodeBlock({ value }) {
  return <pre className="code-block">{value}</pre>;
}

export function MessageContent({ value, format, renderMarkdown, className = "" }) {
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
