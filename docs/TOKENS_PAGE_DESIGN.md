# Tokens Page Design

## Goal

Tokens should be the single place where a signed-in monitor user manages API access tokens.

The account menu should stay focused on account-level actions such as theme, password, and sign out. It should not contain a competing `Get token` flow because the main navigation already has a Tokens page.

## Current State

Frontend:

- `web/monitor-ui/src/components/PrimaryNav.jsx` account menu includes `Get token`.
- `PrimaryNav` also owns a `TokenDialog` modal that creates a token.
- `web/monitor-ui/src/routes/TokensPage.jsx` has a separate token creation form.
- The two creation surfaces overlap and can confuse users.

Backend:

- `POST /api/auth/tokens` creates a token for the current principal.
- The raw token is only returned once at creation time.
- Tokens are stored hashed in `api_tokens`; only prefix, name, scope, enabled, created_at, expires_at, and last_used_at are safe to list.
- There is no current API for listing the current user's tokens.
- There is no current API for disabling/revoking a token.

## Product Decisions

### Navigation

- Remove `Get token` from the account menu.
- Remove the account-menu `TokenDialog`.
- Keep `Change password` in the account menu because it is account-scoped and not duplicated elsewhere.
- Keep `Tokens` in primary navigation as the only token management entry point.

### Tokens Page

Tokens page should show:

- current user identity summary
- list of tokens created by the current user
- token creation form
- one-time token reveal after creation
- token lifecycle metadata:
  - name
  - prefix
  - scope
  - enabled / revoked / expired status
  - created_at
  - expires_at
  - last_used_at

Initial management actions:

- create token
- copy newly-created raw token from the one-time reveal
- revoke token

Non-goals:

- never show token hashes
- never re-display a raw token after creation
- no multi-user admin token listing in the first version
- no role/scope editor beyond the existing simple scope input/default

## API Design

### List Current User Tokens

```text
GET /api/auth/tokens
```

Response:

```json
{
  "items": [
    {
      "id": 1,
      "name": "local-dev",
      "prefix": "llmtl_xxxx",
      "scope": "api",
      "enabled": true,
      "status": "active",
      "created_at": "2026-05-14T00:00:00Z",
      "expires_at": "2026-06-13T00:00:00Z",
      "last_used_at": null
    }
  ],
  "total": 1
}
```

Status derivation:

- `revoked`: `enabled == false`
- `expired`: `expires_at` is set and before now
- `active`: enabled and not expired

### Create Current User Token

Existing endpoint remains:

```text
POST /api/auth/tokens
```

Request:

```json
{
  "name": "local-dev",
  "ttl": "720h",
  "scope": "api"
}
```

Response should keep returning the raw token once:

```json
{
  "token": "llmtl_...",
  "prefix": "llmtl_..."
}
```

After creation, frontend should refresh the token list.

### Revoke Current User Token

```text
DELETE /api/auth/tokens/{id}
```

Behavior:

- only the owning user can revoke their own token
- set `enabled = false`
- do not delete the row, so the list remains auditable
- if the token does not belong to the current user, return 404

Response:

```json
{
  "ok": true
}
```

## UI Layout

### Header

- title: `API tokens`
- current user badge
- token count and active count

### Token List

Use a dense table/list, not cards-heavy layout:

Columns:

- Name
- Prefix
- Scope
- Status
- Created
- Expires
- Last used
- Actions

Row actions:

- revoke active token

Empty state:

- no tokens have been created for this user

### Create Token Panel

Fields:

- name
- TTL
- scope

Defaults:

- name: `local-dev`
- TTL: empty, meaning no expiration
- scope: `api` or `all` depending on current backend convention; first implementation can keep current `api` default from the existing page

After successful creation:

- show a one-time reveal panel with the raw token
- include prefix
- warn that the token cannot be shown again
- refresh list

## Development Plan

### Phase 1: Design Baseline

- Add this document.
- Confirm account menu / Tokens page ownership boundary.
- Commit documentation separately.

Validation:

- Documentation review only.

### Phase 2: Backend Token Listing And Revocation

- Add auth store methods:
  - list tokens for username
  - revoke token for username and token id
- Extend `/api/auth/tokens`:
  - `GET` list current user's tokens
  - `POST` create current user's token
- Add `/api/auth/tokens/{id}`:
  - `DELETE` revoke current user's token
- Add monitor/auth tests.

Validation:

- `go test ./internal/auth ./internal/monitor`

Status:

- Completed in this phase.
- `GET /api/auth/tokens`, `POST /api/auth/tokens`, and `DELETE /api/auth/tokens/{id}` are scoped to the current authenticated principal.
- Token listing returns safe metadata only and never returns raw tokens or hashes.

### Phase 3: Frontend Tokens Page

- Replace token-only form with list + create form.
- Refresh list after creation and revocation.
- Show one-time token reveal after creation.
- Use current user from `AppShell` or `TokensPage` fetch as needed.

Validation:

- `task ui:build`
- `go test ./internal/monitor`

### Phase 4: Remove Duplicate Account Menu Token Flow

- Remove `Get token` menu item.
- Remove `TokenDialog` from `PrimaryNav`.
- Remove now-unused imports/state.
- Keep `Change password` and `Sign out`.

Validation:

- `task ui:build`
- `go test ./internal/monitor`

### Phase 5: Review

- Update this document with completed commits and any intentional deferrals.
- Confirm token management has one entry point: Tokens page.

## Explicit Deferrals

- Admin view of all users' tokens.
- Token rename.
- Scope presets beyond the current backend scope string.
- Token usage analytics beyond `last_used_at`.
- Hard delete; first implementation should revoke by disabling.
