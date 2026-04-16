# Vertex Native Plan

## Goal

Define the smallest credible path for `llm-tracelab` to add a fourth protocol family: `vertex_native`.

This document is intentionally about execution scope, not only research notes.
The purpose is to decide:

- whether Vertex should reuse `google_genai`
- what the minimal implementation surface should be
- what must be true before the first implementation PR is considered complete

## Decision

`vertex_native` should be added as a new `protocol_family`, not folded into `google_genai`.

## Why It Should Be Its Own Family

### 1. Routing semantics are materially different

Vertex AI supports project/location-scoped model paths instead of the simpler Google AI Studio style base path.

Examples from Google Cloud docs:

- express mode uses `https://aiplatform.googleapis.com/v1/{model}:generateContent`
- standard Vertex-style paths use `https://{location}-aiplatform.googleapis.com/v1/{model}:generateContent`
- publisher model names include project/location context such as `projects/{project}/locations/{location}/publishers/*/models/*`

These are not just `base_url` differences. They affect:

- path normalization
- model extraction from URL
- replay grouping and recorded endpoint semantics
- startup diagnostics and config validation

### 2. Auth semantics are different enough to deserve isolation

Google AI Studio style traffic typically uses API-key auth.
Vertex AI uses Google Cloud auth, typically Bearer tokens for Google Cloud resources.

That means Vertex should not be treated as a small variant of `google_genai`.
Routing/auth mistakes here are easy to make and hard to debug if the two modes share too much implicit logic.

### 3. The long-term surface is broader than current `google_genai`

Even if the first implementation only targets `generateContent` and `streamGenerateContent`, Vertex-native already has a broader surrounding surface in official docs, including token counting and other project/location-scoped resources.

That makes `vertex_native` a better architectural fit than trying to stretch `google_genai` beyond AI Studio.

## What Can Be Reused

The request and response payloads for the first cut are close enough to Gemini-style content generation that `pkg/llm` should reuse as much `GeminiGenerateContentRequest` and `GeminiResponse` logic as possible.

That means:

- request body parsing can likely reuse the existing Gemini request adapter internals
- response parsing can likely reuse the existing Gemini response adapter internals
- stream chunk parsing can likely reuse the existing Google stream logic

But reuse should happen under a new family adapter, not by pretending Vertex is just another `google_genai` routing profile.

## Recommended First Scope

### Protocol family

- `vertex_native`

### Initial routing profiles

- `vertex_express`
- `vertex_project_location`

### Initial operations

- `generateContent`
- `streamGenerateContent`

### Initial non-goals

Do not include these in the first implementation round:

- `predict`
- `rawPredict`
- `serverStreamingPredict`
- `countTokens`
- tuned endpoint-specific flows
- Vertex-specific long-running operations

The first delivery should prove the family boundary, not maximize endpoint count.

## Proposed Config Shape

Additive config only. No breaking migration.

Recommended resolved fields:

- `protocol_family: vertex_native`
- `routing_profile: vertex_express | vertex_project_location`
- `project`
- `location`
- `model_resource`

User-facing preset should come later, after the family exists.
For the first implementation, it is acceptable to require explicit config instead of immediately adding a broad `vertex` preset.

## Routing Rules

### `vertex_express`

Intended for the global `aiplatform.googleapis.com` style endpoint.

Expected characteristics:

- global base URL
- `publishers/google/models/*` style model resource
- OAuth/Bearer auth

### `vertex_project_location`

Intended for regional Vertex paths.

Expected characteristics:

- regional `*-aiplatform.googleapis.com` host
- project/location-scoped model resource
- OAuth/Bearer auth

## Minimal Adapter Contract

The first `vertex_native` adapter should support:

1. request parsing from Vertex-native paths
2. response parsing for non-stream `generateContent`
3. stream parsing for `streamGenerateContent`
4. usage extraction
5. model extraction from Vertex-native paths
6. provider error extraction for non-stream and stream paths

If any of these is missing, the family is not yet replay-safe.

## Cassette Requirements

Before `vertex_native` is considered implemented, cassette coverage should exist for:

- `non_stream`
- `stream`
- `provider_error`
- `stream_error`
- `partial_completion`
- `history`

At least one stream fixture should prove:

- partial content is preserved
- terminating error is preserved
- monitor still renders a useful normalized summary

## Monitor Requirements

The first monitor projection does not need Vertex-specific UI, but it must show:

- request history
- AI content
- provider error blocks
- safety/refusal blocks when present
- usage if returned

If monitor output for Vertex is only “raw body fallback”, the first implementation is incomplete.

## Suggested Implementation Order

1. Add `vertex_native` constants and resolved-upstream scaffolding in `internal/upstream`
2. Add path classification and model extraction in `pkg/llm/trace.go`
3. Add a Vertex adapter that initially reuses Gemini request/response parsing internals
4. Add stream parsing and provider-error handling
5. Add proxy routing/auth tests
6. Add cassette matrix fixtures and monitor assertions
7. Add docs/preset guidance only after the family works end to end

## Risks

### Risk: over-sharing with `google_genai`

If too much logic is shared implicitly, future Vertex-only endpoints will force confusing conditionals into the existing Google family.

Mitigation:

- share helper functions, not family identity
- keep `vertex_native` endpoint normalization separate

### Risk: trying to support too many Vertex surfaces in v1

Vertex has more than one way to invoke models.
Trying to cover all of them immediately will slow down delivery and weaken tests.

Mitigation:

- limit first scope to `generateContent` and `streamGenerateContent`
- postpone `predict` / `rawPredict` / token endpoints

### Risk: auth and routing confusion

Vertex uses Google Cloud auth and project/location-aware paths, which are operationally different from Google AI Studio.

Mitigation:

- use explicit `routing_profile`
- fail fast on missing `project` / `location` / `model_resource` when required

## Exit Criteria For M4 Selection

`vertex_native` is the correct next family if all of these remain true:

- it continues to differ from `google_genai` in routing/auth semantics enough to justify separation
- a minimal first cut can be delivered without destabilizing the current three families
- cassette-based replay can protect the new family from day one

If those assumptions stop being true, re-evaluate before coding.

## Sources

Official Google Cloud docs consulted for this planning note:

- Vertex AI express mode REST API reference: https://cloud.google.com/vertex-ai/generative-ai/docs/start/express-mode/vertex-ai-express-mode-api-reference
- Vertex express `generateContent`: https://cloud.google.com/vertex-ai/generative-ai/docs/reference/express-mode/rest/v1/publishers.models/generateContent
- Vertex project/location REST resource overview: https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.publishers.models
