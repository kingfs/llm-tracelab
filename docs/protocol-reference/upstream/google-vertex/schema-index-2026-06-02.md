# Google Vertex AI Snapshot 2026-06-02

## Official Source

- AI Platform discovery document: https://aiplatform.googleapis.com/$discovery/rest?version=v1
- Vertex AI Gemini API reference: https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference

## Files

- [`aiplatform-discovery-v1-2026-06-02.json`](./aiplatform-discovery-v1-2026-06-02.json): raw official AI Platform discovery document.
- [`generate-content-core-schemas-2026-06-02.json`](./generate-content-core-schemas-2026-06-02.json): extracted Vertex GenerateContent schemas and model methods used by TraceLab.

## TraceLab Coverage

Current TraceLab parser coverage targets:

- express-style publisher/model GenerateContent paths
- project/location publisher/model GenerateContent paths
- stream GenerateContent variants
- model list paths used for discovery/connectivity

Vertex native routing differs from Google AI Studio primarily in resource path shape and auth behavior, even when the payload schema is broadly Gemini-like.
