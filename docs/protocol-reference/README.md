# Protocol Reference

This directory is the current protocol reference entry for TraceLab.

It separates two concerns:

- current TraceLab implementation facts: what the proxy can route, record, replay, and parse today
- upstream protocol materials: official API specs or documentation snapshots used when implementing parsers and protocol-family routing

TraceLab is currently a protocol-family-aware pass-through recorder. It does not translate requests between protocol families in the proxy hot path.

## Current Implementation References

- [Implemented Protocols](./implemented-protocols.md): current code-supported protocol families, endpoints, routing profiles, and parser coverage.
- [Protocol Differences](./protocol-differences.md): practical differences between OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, Google Gemini GenerateContent, and Vertex native GenerateContent.

## Upstream Snapshots

Current snapshot date: 2026-06-02.

The upstream materials are stored under [`upstream/`](./upstream/). When an upstream API changes, add a new dated snapshot instead of overwriting an older file.

```text
protocol-reference/
  upstream/
    openai/
    anthropic/
    google-gemini/
    google-vertex/
```

## Source Policy

- Use official upstream specs or docs where available.
- Keep raw snapshots human-locatable and dated.
- Keep extracted schema subsets near the raw snapshot for implementation convenience.
- Do not treat an upstream schema as TraceLab's internal IR. TraceLab's semantic parser output remains Observation IR.
- OpenAI-compatible providers only claim compatibility with a subset of OpenAI-style behavior; they are not automatically equivalent to the official OpenAI API.
- This directory is the single source of truth for upstream protocol snapshots. The older v1 design-era 2026-05-13 snapshots under `docs/v1/reference-materials/` were superseded by the dated snapshots here and have been removed.
