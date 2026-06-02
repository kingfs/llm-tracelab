# Credential Routing Decision Chain Spec

This document specifies the next additive implementation step for credential-aware routing. It is a design contract for later storage, router, Monitor, and MCP work; this branch does not introduce database migrations or router behavior changes.

## Goals

- Model credentials as explicit route decision identities below channels.
- Keep route selection explainable at both channel and credential levels.
- Preserve cassette/replay compatibility by recording credential decisions only as additive V3 prelude events and metadata.
- Define the minimal storage/config additions needed for a later implementation batch.
- Keep payment, recharge, public relay, subscription resale, and quota marketplace behavior out of scope.

## Current Baseline

The current router hot path compiles configured upstreams and channel records into `internal/router.Target`. A target is also the event-visible candidate identity. `DecisionTrace` and `CandidateDecision` already carry model, endpoint, policy, selected ID, sticky state, filter reason, health state, and candidate attributes. Sticky routing is process-local and currently binds sticky keys to target IDs.

Channel management already has `channel_configs`, `channel_models`, probe runs, model catalog records, and runtime projection back to `config.UpstreamTargetConfig`. Channel records may contain encrypted API key material directly. There is not yet a separate credential table or credential-scoped runtime state.

## Domain Boundaries

### Channel

A Channel is the user-managed upstream configuration boundary:

- Stable ID, display name, source, enabled flag.
- Provider preset, protocol family, routing profile, base URL, API version, deployment/project/location/model resource fields.
- Channel-level headers and non-secret routing metadata.
- Priority, weight, capacity hint, model discovery mode, model enablement.
- Channel-level health and probe status.

A Channel answers: "Which upstream/provider surface can serve this request?"

Channel should not own per-account concurrency, token refresh state, credential-specific cooldown, or credential-specific sticky binding once credential routing is enabled.

### Credential

A Credential is an optional authentication identity under exactly one Channel:

- Stable ID scoped to a channel.
- Enabled flag and display label.
- Secret material type: API key, bearer token, OAuth refresh/access token, service account, or custom header secret.
- Secret ciphertext and non-secret hint only; never plaintext in events, SQLite list rows, MCP output, or Monitor payloads.
- Credential-level limits: concurrency, rate window, cooldown policy, and optional capacity hint.
- Credential-level health state, last failure, open/probation window, and refresh status.

A Credential answers: "Which account/token under this channel is allowed and healthy enough to carry this request?"

For local single-user setups, an existing Channel with inline API key is treated as an implicit default credential during migration. This preserves the current configuration shape and avoids forcing users to create a credential table before the feature is enabled.

### RouteTarget

A RouteTarget is the router hot-path immutable-ish runtime projection compiled from:

- Channel routing surface.
- Credential identity, if credential routing is enabled for that channel.
- Model enablement and provider capability.
- Current health, limits, and sticky eligibility snapshots.

Recommended identity:

```text
route_target_id = channel_id + ":" + credential_id
```

For implicit credentials:

```text
route_target_id = channel_id + ":default"
```

The current `router.Target.ID` may remain the route target ID after credential routing is introduced. Candidate decisions should additionally expose `channel_id` and `credential_id` fields so readers can group by channel without parsing IDs.

RouteTarget answers: "Which concrete channel plus credential will receive this request?"

## Minimal Additive Storage And Config

No migration is performed in this branch. A later implementation should add only optional structures first.

### Storage

Add a new credential table or ent schema with these fields:

Implementation-ready migration details are tracked in [GATEWAY_CREDENTIAL_STORAGE_MIGRATION_DESIGN.md](./GATEWAY_CREDENTIAL_STORAGE_MIGRATION_DESIGN.md).

- `id TEXT PRIMARY KEY`
- `channel_id TEXT NOT NULL`
- `name TEXT NOT NULL`
- `kind TEXT NOT NULL`
- `enabled INTEGER NOT NULL DEFAULT 1`
- `secret_ciphertext BLOB`
- `secret_hint TEXT`
- `headers_json TEXT NOT NULL DEFAULT '{}'`
- `concurrency_limit INTEGER`
- `rate_limit_json TEXT NOT NULL DEFAULT '{}'`
- `cooldown_json TEXT NOT NULL DEFAULT '{}'`
- `health_state TEXT NOT NULL DEFAULT 'unknown'`
- `open_until DATETIME`
- `last_error TEXT`
- `last_used_at DATETIME`
- `created_at DATETIME NOT NULL`
- `updated_at DATETIME NOT NULL`

Indexes:

- `(channel_id, enabled)`
- `(health_state, open_until)`

Do not remove `channel_configs.api_key_ciphertext` in the first migration. It remains the implicit default credential source until a later cleanup migration proves all readers and bootstrap paths use explicit credentials.

### Config

Keep the current single-upstream and multi-upstream config valid. Add optional credential config under an upstream/channel only:

```yaml
upstreams:
  - id: openai-primary
    upstream:
      base_url: https://api.openai.com/v1
      provider_preset: openai
    credentials:
      - id: default
        name: personal key
        api_key: ${OPENAI_API_KEY}
        concurrency_limit: 2
      - id: backup
        name: backup key
        api_key: ${OPENAI_BACKUP_API_KEY}
        concurrency_limit: 1
```

If `credentials` is empty and the upstream/channel has an existing API key, compile exactly one implicit credential named `default`. If both inline API key and explicit credentials are present, explicit credentials win and startup should emit a warning event instead of merging secrets silently.

## Router Compilation

Implementation should split the current target construction into two steps:

1. Load channels with model enablement and optional credentials.
2. Compile route targets by expanding each enabled channel into one route target per enabled credential.

Candidate filtering order:

1. Channel enabled and provider/profile supports endpoint.
2. Model supported by channel model enablement.
3. Channel health is not open.
4. Credential enabled and credential secret material is usable.
5. Credential health is not open.
6. Limit primitives allow admission.
7. Sticky constraints prefer an existing eligible binding, or break with a recorded reason.

Scoring should continue to use the existing channel priority/weight/capacity as the default. Credential-specific capacity can multiply or cap the channel score later, but the first implementation should avoid introducing a new balancing policy unless needed for correctness.

## Event Vocabulary

Events remain additive V3 prelude events. Existing event names must continue to be emitted for compatibility.

Extend existing candidate and selected events:

- `routing.candidates`: each candidate may include `route_target_id`, `channel_id`, `credential_id`, `credential_hint`, `credential_health_state`, `credential_selectable`, `credential_filter_reason`.
- `routing.selected`: include `route_target_id`, `channel_id`, `credential_id`, `credential_hint`.
- `routing.filtered`: may use credential-level reasons listed below.
- `routing.outcome`: include selected `route_target_id`, `channel_id`, and `credential_id` when known.

Add credential-specific events only when the credential layer makes a decision not already visible in the candidate list:

- `routing.credential.considered`
- `routing.credential.selected`
- `routing.credential.filtered`
- `routing.credential.health_open`
- `routing.credential.cooldown_open`
- `routing.credential.refresh_failed`
- `routing.credential.missing_secret`

Credential filter reasons:

- `credential_disabled`
- `credential_missing_secret`
- `credential_health_open`
- `credential_probation_full`
- `credential_concurrency_full`
- `credential_rate_limited`
- `credential_refresh_failed`
- `sticky_credential_unavailable`

All event attributes must be display-safe. Use stable IDs and secret hints only. Never record API keys, bearer tokens, OAuth refresh tokens, service-account JSON, provider account email unless explicitly classified as non-secret display metadata, or full custom auth headers.

## Sticky Binding Interaction

Sticky binding should move from "sticky key -> target ID" to "sticky key -> route target ID". The route target ID points to a channel plus credential. This preserves provider-side conversation/session/cache locality at the account level.

Rules:

- A sticky hit is valid only if the route target remains enabled, supports the endpoint/model, and passes channel and credential health/limit gates.
- If the channel is healthy but the credential is open, missing, or limit-rejected, emit `routing.sticky.break` with `break_reason` set to the credential filter reason.
- If the same channel has another eligible credential, the router may rebind to that credential and emit `routing.sticky.bind` with the new route target ID.
- If no credential under the sticky channel is eligible, normal fallback may consider other channels according to policy.
- Sticky events should include `route_target_id`, `channel_id`, and `credential_id`; existing `target_id` can remain as an alias for compatibility.

## Health And Limit Interaction

Health should be tracked at two levels:

- Channel health: provider surface, base URL, model availability, broad upstream failure.
- Credential health: account/token auth failures, quota exhaustion, refresh failure, credential-specific 429/403, credential concurrency saturation.

Outcome classification should update the narrowest level possible:

- Network/DNS/base URL failures affect channel health.
- 401/403 caused by invalid or expired token affect credential health.
- Provider account quota or account-level 429 affects credential health.
- Provider-wide 5xx can affect channel health.
- Model-specific failure can continue to use existing model health.

Limit events should be layered:

- `limit.concurrency_rejected` with `scope=credential` for credential concurrency.
- `limit.rate_rejected` with `scope=credential` for credential rate windows.
- `limit.queue_saturated` remains global/token/channel depending on the rejecting guard.

Route decision traces should embed limit outcomes as candidate filter reasons rather than requiring readers to correlate separate limit events for basic explanations.

## Decision Trace Shape

Future `DecisionTrace` should keep the current fields and add:

- `SelectedRouteTargetID`
- `SelectedChannelID`
- `SelectedCredentialID`
- `CredentialEvents []CredentialDecision`

Future `CandidateDecision` should add:

- `RouteTargetID`
- `ChannelID`
- `CredentialID`
- `CredentialHint`
- `CredentialHealthState`
- `CredentialSelectable`
- `CredentialFilterReason`

The JSON shape must be additive. Existing fields such as `selected_id`, `candidates[].id`, and sticky `target_id` remain populated with route target IDs so older readers still have a concrete selected identity.

## Replay And Cassette Compatibility

Replay compatibility requirements:

- Raw HTTP request and response bytes are unchanged.
- V2 cassette reading remains unchanged.
- V3 prelude events are additive; unknown event types and unknown attributes must be ignored by older readers.
- `pkg/replay` must not need credential storage or channel storage.
- A replayed cassette must not attempt token refresh, credential lookup, sticky rebinding, or health mutation.

Monitor/MCP readers should prefer structured credential fields when present and fall back to existing target-level fields when absent.

## Security And Redaction

Credential-aware routing increases the risk of leaking account identity through diagnostics. The implementation must follow these rules:

- Event payloads may include `credential_id` and `credential_hint`; the hint should be generated like existing secret hints and contain only a short non-sensitive suffix/prefix.
- Custom header names may be recorded only when they are not secret-bearing, or after sensitive marker filtering.
- URL display fields must use shared URL redaction before event recording.
- OAuth refresh tokens, access tokens, API keys, service account JSON, and custom auth header values are never written to cassette metadata, SQLite analytics rows, MCP output, or Monitor JSON.
- Error text from upstream auth failures should pass through existing sensitive-data redaction before display or indexing.

## Migration Strategy

Recommended phases:

1. Add credential storage/config readers and compile implicit default credentials from existing channel secrets.
2. Extend router snapshots and decision traces with additive credential fields while preserving current target IDs for channels without explicit credentials.
3. Emit credential-aware candidate and selected attributes in cassette events.
4. Add credential-scoped health and limit updates.
5. Add Monitor/MCP grouping by channel and credential.
6. Only after one release window, consider migrating inline channel secrets into explicit credential records.

Rollback safety:

- Disabling credential routing should collapse each channel back to its existing implicit target behavior.
- Existing cassettes remain readable because raw bytes and old event names are preserved.
- Existing SQLite databases start without the new table until the later migration batch introduces it.

## Out Of Scope

- Payment, recharge, wallet balance, subscription resale, promotional codes, or public relay operation.
- Multi-tenant quota marketplace behavior.
- Request semantic rewriting beyond existing provider routing adapters.
- Public SaaS account lifecycle management.
- DB migration or router code changes in this documentation branch.

## Acceptance For Implementation Batches

A later implementation can be split safely when:

- Storage owns optional credential records and implicit default credential migration.
- Router owns route target expansion, credential filters, scoring, sticky rebinding, and health/limit state.
- Proxy/recorder owns additive event emission and redaction.
- Monitor/MCP own read-side grouping and fallback behavior.
- Tests prove old cassettes, V2 readers, and `pkg/replay` remain independent from credential storage.
