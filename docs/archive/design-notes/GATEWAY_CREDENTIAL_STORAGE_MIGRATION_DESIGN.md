# Gateway Credential Storage Migration Design

This document is the implementation-ready storage plan for explicit gateway credential records. It extends the credential routing contract in [GATEWAY_CREDENTIAL_DECISION_CHAIN_SPEC.md](./GATEWAY_CREDENTIAL_DECISION_CHAIN_SPEC.md) and the Batch 4 task plan in [GATEWAY_PARALLEL_TASK_PLAN.md](./GATEWAY_PARALLEL_TASK_PLAN.md).

This branch is documentation only. It does not introduce a database migration, does not change router or proxy behavior, and does not change cassette recording or replay.

## Goals

- Add explicit credential records below existing channel/upstream configuration.
- Preserve existing local-first setups where a channel has only inline encrypted key material in `channel_configs.api_key_ciphertext`.
- Make migration and rollback safe for existing SQLite databases.
- Give later storage, router, Monitor, and MCP implementation tasks a concrete schema and test plan.

## Non-Goals

- Payment, recharge, wallet balance, subscription resale, promotional code, quota marketplace, or public relay operation.
- Multi-tenant billing, SaaS account lifecycle management, or public relay abuse controls.
- Router scoring, sticky rebinding, credential health mutation, or proxy event emission in this migration branch.
- Removing `channel_configs.api_key_ciphertext` or requiring users to rewrite existing YAML immediately.

## Current State

The current channel table is `channel_configs`. It stores channel-level routing metadata plus inline encrypted secret material:

- `api_key_ciphertext BLOB NULL`
- `api_key_hint TEXT NOT NULL DEFAULT ''`
- `headers_json TEXT NOT NULL DEFAULT '{}'`

Runtime channel projection turns each enabled channel into one upstream target. Existing behavior is single-key: if a channel has API key material, that key is the credential implicitly used by the target. This must remain valid after adding explicit credential storage.

## Proposed Ent Schema

Add a new ent schema named `ChannelCredential` backed by table `channel_credentials`.

Fields:

- `id string`: non-empty, immutable credential ID. This is stable within a channel and participates in route target identity.
- `channel_id string`: non-empty channel ID referencing `channel_configs.id`.
- `name string`: display name, default `""`.
- `kind string`: default `api_key`. Valid initial values: `api_key`, `bearer_token`, `custom_headers`, `oauth`, `service_account`.
- `enabled bool`: default `true`.
- `secret_ciphertext []byte`: optional/nillable encrypted primary secret material.
- `secret_hint string`: default `""`; display-safe hint only.
- `headers_json string`: default `{}`; encrypted when it may carry secret-bearing header values.
- `concurrency_limit int`: default `0`, where `0` means unlimited at credential scope.
- `rate_limit_json string`: default `{}` for later window/bucket config.
- `cooldown_json string`: default `{}` for later auth/quota cooldown policy.
- `health_state string`: default `unknown`; later router work may use `healthy`, `open`, `half_open`, or `disabled`.
- `open_until time.Time`: optional/nillable.
- `last_error string`: default `""`; must be redacted before persistence.
- `last_used_at time.Time`: optional/nillable.
- `created_at time.Time`: default `time.Now`.
- `updated_at time.Time`: default `time.Now`, updated on mutation.

Indexes:

- unique `(channel_id, id)`
- `(channel_id, enabled)`
- `(health_state, open_until)`
- `(last_used_at)`

Edges:

- From `ChannelCredential` to `ChannelConfig` by `channel_id`.
- The initial implementation may use field-based joins only if adding ent edges would create too much generated-code churn, but the table and indexes should be the same.

Suggested ent file:

```go
// ent/schema/channel_credential.go
type ChannelCredential struct {
	ent.Schema
}

func (ChannelCredential) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "channel_credentials"}}
}
```

## SQL Migration Shape

The first migration should only create the new table and indexes. It must not mutate `channel_configs`.

Up migration:

```sql
CREATE TABLE IF NOT EXISTS `channel_credentials` (
  `id` text NOT NULL,
  `channel_id` text NOT NULL,
  `name` text NOT NULL DEFAULT (''),
  `kind` text NOT NULL DEFAULT ('api_key'),
  `enabled` bool NOT NULL DEFAULT (true),
  `secret_ciphertext` blob NULL,
  `secret_hint` text NOT NULL DEFAULT (''),
  `headers_json` text NOT NULL DEFAULT ('{}'),
  `concurrency_limit` integer NOT NULL DEFAULT (0),
  `rate_limit_json` text NOT NULL DEFAULT ('{}'),
  `cooldown_json` text NOT NULL DEFAULT ('{}'),
  `health_state` text NOT NULL DEFAULT ('unknown'),
  `open_until` datetime NULL,
  `last_error` text NOT NULL DEFAULT (''),
  `last_used_at` datetime NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`channel_id`, `id`)
);

CREATE INDEX IF NOT EXISTS `channelcredential_channel_id_enabled`
  ON `channel_credentials` (`channel_id`, `enabled`);
CREATE INDEX IF NOT EXISTS `channelcredential_health_state_open_until`
  ON `channel_credentials` (`health_state`, `open_until`);
CREATE INDEX IF NOT EXISTS `channelcredential_last_used_at`
  ON `channel_credentials` (`last_used_at`);
```

Down migration:

```sql
DROP INDEX IF EXISTS `channelcredential_last_used_at`;
DROP INDEX IF EXISTS `channelcredential_health_state_open_until`;
DROP INDEX IF EXISTS `channelcredential_channel_id_enabled`;
DROP TABLE IF EXISTS `channel_credentials`;
```

Foreign keys are intentionally not required in the first SQLite migration. The project already uses local SQLite files and generated ent accessors; avoiding strict FK enforcement keeps startup resilient for users with hand-edited or partially restored local DBs. Store-level deletes should still remove credentials for deleted channels in service code.

## Store Records And APIs

Add a store record:

```go
type ChannelCredentialRecord struct {
	ID               string
	ChannelID        string
	Name             string
	Kind             string
	Enabled          bool
	SecretCiphertext []byte
	SecretHint       string
	HeadersJSON      string
	ConcurrencyLimit int
	RateLimitJSON    string
	CooldownJSON     string
	HealthState      string
	OpenUntil        time.Time
	LastError        string
	LastUsedAt       time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
```

Add store methods:

- `ListChannelCredentials(channelID string, enabledOnly bool) ([]ChannelCredentialRecord, error)`
- `GetChannelCredential(channelID string, credentialID string) (ChannelCredentialRecord, error)`
- `UpsertChannelCredential(record ChannelCredentialRecord) (ChannelCredentialRecord, error)`
- `DeleteChannelCredential(channelID string, credentialID string) error`
- `ReplaceChannelCredentials(channelID string, records []ChannelCredentialRecord) error`

Secret handling must reuse the existing local secret envelope path used for `channel_configs.api_key_ciphertext` and encrypted headers. API responses and Monitor/MCP list rows must expose only `secret_hint`, never `secret_ciphertext` or decrypted values.

## Runtime Projection Semantics

Later router work should load each enabled channel plus credentials using this order:

1. If enabled explicit credentials exist for a channel, use only those credentials.
2. If no explicit credentials exist and `channel_configs.api_key_ciphertext` or secret-bearing channel headers exist, synthesize an implicit credential:
   - `credential_id = "default"`
   - `route_target_id = channel_id` for backward-compatible target identity unless router expansion explicitly needs `channel_id + ":default"`.
   - `secret_hint = channel_configs.api_key_hint`
   - secret/header material comes from the existing channel fields.
3. If neither explicit credentials nor inline secret material exist, the channel remains usable only for providers/configurations that do not require auth. It should not create a fake secret credential.

Explicit credentials win over inline channel secrets. The implementation should emit a warning system event when both are present, with a message that explicit credentials are used and inline channel key material is ignored for routing. The warning must include channel ID and credential IDs only, not secret material.

## Inline Secret Migration Strategy

Do not move inline secrets during the table-creation migration. The first release with `channel_credentials` should support both sources:

- Existing DBs start unchanged.
- Existing inline-only channels behave as if they have an implicit `default` credential.
- New explicit credential rows can be created without deleting inline channel secrets.

An optional later backfill can create explicit `default` records from inline channel secrets. That backfill must be idempotent:

- For each channel with inline `api_key_ciphertext` and no `channel_credentials` rows, insert `channel_credentials(channel_id, id='default')`.
- Copy `api_key_ciphertext` to `secret_ciphertext`.
- Copy `api_key_hint` to `secret_hint`.
- Copy secret-bearing `headers_json` only if the storage layer already treats those headers as encrypted and display-safe.
- Leave the original inline fields in place.
- If `default` already exists, do not overwrite it.

Cleanup of inline fields is out of scope until at least one release window after explicit credentials are generally available.

## Bootstrap Behavior

YAML/config bootstrap should follow the same priority rules:

- Existing YAML with only `upstream.api_key` imports as the existing channel inline key and continues to project as implicit `default`.
- YAML with `credentials` should create explicit `channel_credentials` rows after the new schema exists.
- If YAML has both `upstream.api_key` and `credentials`, bootstrap should import explicit credentials and preserve or ignore inline key according to existing channel bootstrap compatibility, but runtime routing must prefer explicit credentials.
- Bootstrap must be idempotent: rerunning startup must not duplicate credentials or rotate IDs.
- Bootstrap must not log API keys, bearer tokens, service account JSON, OAuth refresh tokens, or custom auth header values.

Recommended YAML-to-storage mapping:

- `credentials[].id` -> `channel_credentials.id`
- `credentials[].name` -> `name`
- `credentials[].enabled` -> `enabled`
- `credentials[].api_key` -> encrypted `secret_ciphertext`, `kind='api_key'`
- `credentials[].headers` -> encrypted or redacted `headers_json` according to the existing secret-box rules
- `credentials[].concurrency_limit` -> `concurrency_limit`

## Rollback Safety

Rollback means running old code against a DB that may contain `channel_credentials`, or applying the down migration before old code starts.

Safety requirements:

- Old code that ignores unknown tables must continue to start with SQLite databases containing `channel_credentials`.
- Down migration only drops `channel_credentials`; it must not touch `channel_configs`, trace indexes, cassettes, or auth tables.
- Because inline channel secrets remain in `channel_configs`, inline-only behavior survives rollback.
- If a user creates explicit-only credentials and later rolls back, old code will not see those credentials. This is acceptable only if documented in release notes; it is why inline secrets are not removed in the first migration.
- Store startup initialization must use `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS` style generated migrations consistent with existing ent migration files.

## Replay And Cassette Compatibility

Credential storage must not affect cassette replay:

- `pkg/replay` reads raw `.http` cassette files and must not open credential tables.
- V2 cassette readers remain unchanged.
- V3 cassette metadata and events remain additive. Credential fields such as `credential_id`, `credential_hint`, and `route_target_id` are optional attributes.
- Existing V3 readers must ignore unknown credential attributes.
- Local replay must never attempt token refresh, credential lookup, sticky rebinding, health mutation, or route selection.

Existing SQLite startup is also preserved:

- The migration adds a new table only.
- Existing `trace_index.sqlite3` / auth DB startup paths continue to initialize when `channel_credentials` is absent.
- Raw `.http` files remain the source of truth for replay and trace detail.

## Required Migration Tests

Add deterministic tests when implementing the migration:

- Fresh DB migration creates `channel_credentials` with all expected columns and indexes.
- Existing DB with `channel_configs.api_key_ciphertext` migrates without changing that row.
- Existing inline-only channel projects one implicit `default` credential when no explicit rows exist.
- Explicit credentials win over inline key when both exist.
- Bootstrap from YAML with `credentials` creates explicit rows exactly once.
- Bootstrap from existing single-key YAML still creates current inline channel config and implicit `default` projection.
- Missing or disabled explicit credentials do not silently fall back to inline key unless there are no explicit credential rows for that channel.
- Down migration drops only `channel_credentials` and leaves `channel_configs`, `channel_models`, trace logs, cassettes, and auth tables intact.
- Old V2 and V3 cassette replay tests pass without credential storage initialized.
- Secret redaction tests prove API keys, bearer tokens, custom auth header values, OAuth tokens, and service account JSON never appear in logs, cassette prelude metadata, Monitor JSON, or MCP output.

Suggested command coverage:

- `go test ./internal/store`
- `go test ./internal/channel`
- `go test ./internal/config ./cmd/server`
- `go test ./pkg/replay ./pkg/recordfile`

## Implementation Split

Later branches can be split safely:

1. Storage migration and generated ent code.
2. Store and channel service APIs for credentials.
3. Config/bootstrap import of explicit credentials.
4. Router route-target expansion and credential filter behavior.
5. Proxy event emission and redaction checks.
6. Monitor/MCP read-side grouping.

Each branch should remain additive and should keep payment/recharge/public relay out of scope.
