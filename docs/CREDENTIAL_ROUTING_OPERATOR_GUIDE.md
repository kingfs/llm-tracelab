# Credential Routing Operator Guide

This guide explains how to operate credential-aware routing in a local-first TraceLab deployment. It is user-facing: use it when you want one upstream channel to choose between multiple provider credentials while preserving record/replay behavior and safe diagnostics.

Credential routing is not payment, recharge, public relay, subscription resale, or quota distribution. It is an account-selection layer for debugging, resilience, and local/team governance.

## Example: Two Credentials Under One Upstream

Use explicit credentials when one provider channel has more than one account or token. The channel still owns provider surface fields such as base URL, preset, routing profile, priority, weight, and model coverage. Each credential owns secret material and credential-scoped limits.

```yaml
upstreams:
  - id: openai-primary
    enabled: true
    priority: 100
    weight: 1
    capacity_hint: 2
    model_discovery: static
    static_models:
      - gpt-5
      - gpt-5.1-codex
    upstream:
      base_url: "https://api.openai.com/v1"
      provider_preset: "openai"
      routing_profile: "openai_default"
    credentials:
      - id: primary
        name: "primary local key"
        api_key: "$env:OPENAI_PRIMARY_API_KEY"
        concurrency_limit: 2
      - id: backup
        name: "backup local key"
        api_key: "$env:OPENAI_BACKUP_API_KEY"
        concurrency_limit: 1
```

Notes:

- `api_key` values should reference environment variables. Do not put real provider secrets in committed YAML.
- `id` should be stable. It becomes part of the route target identity and may appear in cassette metadata as `credential_id`.
- `name` is an operator label. Keep it non-sensitive.
- The channel ID plus credential ID forms the route target identity, for example `openai-primary:primary`.

Long-lived channel configuration should still be managed from Monitor Web when possible. YAML `upstreams` remains useful for first-run bootstrap, tests, and migration-friendly examples.

## Implicit Default Credential

Existing configs that put one key directly on a channel remain compatible:

```yaml
upstream:
  base_url: "https://api.openai.com/v1"
  provider_preset: "openai"
  api_key: "$env:OPENAI_API_KEY"
```

When no explicit `credentials` list is present, TraceLab treats the inline channel key as one implicit credential:

```text
credential_id = default
route_target_id = <channel_id>:default
```

This keeps older single-key deployments working. If explicit credentials are configured under the same upstream, operators should treat the explicit list as the source of truth and avoid also setting an inline `api_key`.

## Sticky Route Target Binding

Sticky routing binds a detected session key to a concrete route target, not just a provider name. With credential routing enabled, that route target is channel plus credential.

Example:

```text
sticky key fingerprint -> openai-primary:primary
```

This matters for coding agents because provider-side conversation state, prompt cache, file cache, or account quota may be tied to the credential that handled earlier turns.

Operational behavior:

- A sticky hit reuses the same route target while the channel and credential remain enabled, compatible, healthy, and within limit.
- If the credential becomes unavailable, TraceLab records a sticky break event and may rebind to another eligible route target, such as `openai-primary:backup`.
- Sticky metadata uses `sticky_key_fingerprint`; raw sticky keys are not exposed in Monitor, MCP, or cassette event attributes.

## Credential-Safe Metadata Fields

Credential-aware cassettes may include these safe routing fields in V3 prelude events:

- `route_target_id`
- `channel_id`
- `credential_id`
- `credential_hint`
- `credential_health_state`
- `credential_filter_reason`
- `sticky_key_fingerprint`

These fields are intended for Monitor, MCP tools, routing summaries, and failure clustering. They must not contain raw provider secrets.

Never write these values to cassette metadata, logs, Monitor JSON, or MCP output:

- API keys
- bearer tokens
- OAuth access or refresh tokens
- service-account JSON
- custom auth header values
- full raw sticky keys

URL display fields are redacted before they are written to routing events. Sensitive query parameters such as `api_key`, `access_token`, `token`, `secret`, `password`, `signature`, and related markers are displayed as `REDACTED`.

## Limit Scope Semantics

Limit scopes describe which identity is protected when a request is admitted or rejected. Credential routing adds narrower scopes without changing the local-first default that strict limits can remain disabled.

Common scopes:

- `global`: the whole proxy process.
- `token`: the TraceLab personal API token used by the client.
- `channel`: one upstream channel, regardless of credential.
- `route_target`: one compiled channel plus credential target.
- `credential`: one credential under a channel.

If a credential limit rejects a request, operators should expect structured evidence such as:

```text
limit.concurrency_rejected scope=credential
routing.filtered credential_filter_reason=credential_concurrency_full
```

HTTP status guidance:

- `429` means the caller or configured policy hit a rate/concurrency limit.
- `503` means the selected upstream, channel, credential, or route target is temporarily unavailable.

## Monitor And MCP Workflow

Monitor and MCP read credential routing from cassette events and indexed trace metadata. They do not need raw provider secrets.

Use:

- Monitor `Routing` to inspect selected route counts, sticky statuses, failure reasons, and credential-aware grouping when those fields exist.
- Trace detail routing context to inspect one request's selected upstream, route target, channel, credential, candidates, and sticky break context.
- MCP `query_routing_decisions` for one trace.
- MCP `query_sticky_routing` for sticky hit/bind/break traces.
- MCP `query_failures` and `summarize_failure_clusters` to group failures by route target, channel, and credential when cassette events include those fields.

Older cassettes remain valid. If a cassette only has `upstream_id`, Monitor and MCP fall back to upstream-level output.

## Replay Compatibility

Credential routing does not change replay facts:

- Raw HTTP request bytes are preserved.
- Raw HTTP response bytes are preserved.
- V3 routing events are additive metadata.
- V2 cassettes remain readable.
- `pkg/replay` does not need channel or credential storage.

This means a test replaying a cassette will not refresh credentials, look up provider accounts, re-run sticky binding, or mutate health state. Replay uses the recorded HTTP exchange as its source of truth.
