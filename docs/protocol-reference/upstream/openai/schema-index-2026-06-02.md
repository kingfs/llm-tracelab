# OpenAI API Snapshot 2026-06-02

## Official Source

- OpenAPI spec: https://raw.githubusercontent.com/openai/openai-openapi/manual_spec/openapi.yaml

## Files

- [`openapi.documented-2026-06-02.yml`](./openapi.documented-2026-06-02.yml): raw official OpenAPI snapshot.
- [`chat-completions-path-2026-06-02.json`](./chat-completions-path-2026-06-02.json): extracted `/chat/completions` path item.
- [`responses-path-2026-06-02.json`](./responses-path-2026-06-02.json): extracted `/responses` path item.
- [`models-path-2026-06-02.json`](./models-path-2026-06-02.json): extracted `/models` path item.
- [`embeddings-path-2026-06-02.json`](./embeddings-path-2026-06-02.json): extracted `/embeddings` path item.

## TraceLab Coverage

Current TraceLab parser coverage targets:

- Chat Completions request, response, and stream chunks
- Responses request, response, and stream events
- Models list response

Embeddings are currently part of the routed OpenAI-compatible endpoint set, but not a deep Observation IR parser target.
