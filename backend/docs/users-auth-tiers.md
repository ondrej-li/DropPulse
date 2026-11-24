# Users, authentication and tiers — design

Status: **design proposal**, no implementation. Written 2026-11-22. This is the
companion to [`research.md`](research.md): that document covers ticket plugins,
scheduling and storage; this one covers who may use the service, how they prove
it, and what each tier may do. Where the two disagree on ownership or access,
this document wins and `research.md` is updated to point here.

## The one decision that is not negotiable

**CAPTCHA solving is not offered, at any tier.** "Catcha watch" was clarified to
mean automatically solving or paying a service to bypass CAPTCHA challenges.
That is circumvention of a third party's access control, conflicts with the
policy already adopted in [`research.md` §1](research.md#1-policy-constraints-shape-the-design),
very likely breaches the target site's terms, and creates legal exposure. It is
not a premium feature we can sell; being a paying customer does not make it
acceptable.

What is offered instead, to every tier, is **challenge detection**:

- the plugin classifies a response as a challenge / anti-bot wall rather than as
  availability;
- the check is recorded as `blocked`, the event backs off, and after repeated
  blocks it is paused instead of hammered;
- the user is notified with a link that opens the site in their own browser so
  they can complete the challenge themselves, manually, once;
- official APIs remain the supported path for sites that protect their pages.

The distinction is deliberate: we can tell a user "the site wants a human" and
get out of the way. We cannot pretend to be that human, or hire someone to be
that human, on the user's behalf.

## 1. Scope and decisions

- **OAuth only.** Google, Facebook and Apple are the supported identity
  providers. There is no password, no magic link, no unauthenticated demo mode.
- **No anonymous access.** Every `/api/v1/*` route except the auth endpoints
  that establish or renew a session, and the signed provider webhooks, requires
  a live session. `/healthz` stays public for monitoring.
- **Every watch belongs to a user.** `monitors.user_id` becomes `not null`;
  events are created by an authenticated user (or by an administrator).
- **Tiers gate complexity, not just volume.** A tier controls how many active
  watches a user may have, which watch modes they may create or subscribe to,
  the minimum check interval, AI-assisted usage and history retention.
- **Shared checks are preserved.** The efficient event-level check model from
  `research.md` stays. A premium watch mode is a property of the event; only
  users whose tier allows that mode may create or subscribe to it. This avoids
  both duplicate polling and free-riding on an expensive mode.
- **Billing is a later phase.** Plans can be assigned manually at first. The
  schema records the source so Stripe, Apple or Google billing can be added
  without a migration of the entitlement model.

## 2. Tiers

Names and numbers below are **initial placeholders**, not a pricing decision.
The important part is which dimensions are metered.

| Capability | Free | Plus | Pro |
|---|---|---|---|
| Active watches | 3 | 25 | 100 |
| Watch modes | `basic` | `basic`, `scripted` | `basic`, `scripted`, `ai_assisted` |
| Minimum check interval | 15 min | 5 min | 1 min |
| Challenge detection + notification | yes | yes | yes |
| CAPTCHA solving | **no** | **no** | **no** |
| AI-assisted setup previews | 0 | 0 | 5 / day |
| AI-assisted checks | 0 | 0 | 100 / day |
| Check history retention | 30 days | 180 days | 365 days |

A **watch** is one user's subscription to one event (`monitors` row), which is
also the unit the product calls "events I am watching". A user can hold at most
one watch per event. Paused watches do not consume the active-watch allowance,
but creating and pausing in a loop is rate-limited.

History retention is **event-level**, because checks are shared: an event keeps
history according to its owner's plan. Your own retention therefore applies to
events you own; subscribers see the window the event retains. Per-subscriber
retention would require query-time filtering and is deliberately deferred.

The tier dimensions map to the request:

- **How many events** — `max_active_watches`.
- **How complex the watch setup is** — `allowed_modes`:
  - `basic`: choose a resolved event, set filters and a permitted interval.
    The site's native or declarative plugin does the work.
  - `scripted`: use a site that requires a reviewed script, or (once the
    sandbox exists) a user-supplied script. See the open question in §14.
  - `ai_assisted`: let Claude help build the extraction config and, when the
    deterministic parse fails, extract availability from the page.
- **How often it checks** — `min_interval_seconds`, with the site's own floor
  always winning. A paid tier never buys a higher request rate than the target
  site allows.

Challenge detection is intentionally available on every tier. It is a safety
valve, not a premium capability, and it is the honest alternative to the
CAPTCHA-solving feature that was declined.

## 3. Domain model changes

The ticket model in `research.md` is preserved, with these ownership rules:

| Table | Change |
|---|---|
| `users` | A real account record, not a placeholder: email, status, role, terms acceptance. The role is the account type: `user`, `admin`, `vendor`, `agent` or `ai`. |
| `user_identities` | One row per provider identity; `(provider, subject)` is unique. |
| `sessions` / `session_tokens` | Server-side sessions, hashed opaque tokens, rotating refresh tokens. |
| `plans` | Tier definitions; limits are JSONB validated by a schema. |
| `subscriptions` | The user's effective plan, status, source and per-user overrides. |
| `events` | Adds `created_by_user_id` and `watch_mode`; remains one shared check target. |
| `monitors` | `user_id` is required; one subscription per user per event. |
| `ai_usage` | Per-call token and cost accounting for AI-assisted watches. |

### Event ownership and visibility

- Creating an event requires an authenticated user and an entitlement for the
  requested `watch_mode`. Creation automatically creates the creator's monitor,
  so the creator is also a watcher from the start.
- The creator owns the event's connection configuration and `watch_mode`. Only
  the owner (or an administrator) may change them. Changes that could affect
  other subscribers require confirmation and generate a notification.
  **V1 limitation:** because the mode is event-level, a Pro user cannot
  unilaterally upgrade a basic event someone else created. An administrator can
  change it; the longer-term alternative is per-mode check plans, discussed in
  §14 under question 8.
- Events are a shared catalogue: any authenticated user may resolve and view
  event metadata. A user's monitor list is private. Full check history is
  readable by that event's watchers, its creator and administrators; it is not
  a public endpoint.
- Subscribing to an event requires the subscriber's tier to allow the event's
  `watch_mode`. A Free user cannot subscribe to a Pro user's AI-assisted event.
  This is what keeps shared checks from becoming a free-rider loophole.
- Premium events are **owner-sponsored**: the owner must keep an active watch
  and the entitlement for the mode. Unsubscribing from or pausing your own
  premium watch pauses the event and notifies the other watchers. Basic events
  may continue without an owner watch because they cost nothing extra to run.
- If the owner downgrades and loses the mode, the event is **paused**, not
  silently downgraded. Existing subscribers are notified. A silent change from
  AI-assisted to basic could alter availability results without anyone
  understanding why.
- Deleting an event is allowed only when there are no other watchers; otherwise
  the owner may pause it, or ask an administrator to migrate it.
  Account deletion removes the user's monitors; an event with other watchers
  survives with `created_by_user_id` set to `null` and premium modes paused
  until an administrator adopts it. This avoids destroying other people's
  watches while still allowing the account to be erased.

`watch_mode` is `basic | scripted | ai_assisted`. It is stored on the event
because the check is shared. `challenge_detected` is an outcome, not a mode.

## 4. Identity model

### Schema sketch

This is the auth-specific part; the ticket tables stay as in `research.md`.

```sql
create extension if not exists citext;

create table users (
    id                uuid primary key default gen_random_uuid(),
    email             citext,                 -- absent if the provider cannot supply one
    email_verified    boolean not null default false,
    display_name      text,
    avatar_url        text,
    role              text not null default 'user'
                      check (role in ('user', 'admin', 'vendor', 'agent', 'ai')),
    status            text not null default 'active'
                      check (status in ('active', 'suspended', 'deleted')),
    terms_version     text,
    terms_accepted_at timestamptz,
    created_at        timestamptz not null default now(),
    updated_at        timestamptz not null default now(),
    last_login_at     timestamptz,
    deleted_at        timestamptz
);

create unique index users_email_active_idx
    on users (email)
    where email is not null and deleted_at is null;

create table user_identities (
    id             uuid primary key default gen_random_uuid(),
    user_id        uuid not null references users (id) on delete cascade,
    provider       text not null
                   check (provider in ('google', 'apple', 'facebook')),
    subject        text not null,             -- provider's stable user id
    email          citext,
    email_verified boolean not null default false,
    display_name   text,
    avatar_url     text,
    created_at     timestamptz not null default now(),
    last_login_at  timestamptz,
    unique (provider, subject)
);

create index user_identities_user_idx
    on user_identities (user_id);

-- Short-lived server-side state for an authorization-code flow.
create table oauth_flows (
    id            uuid primary key default gen_random_uuid(),
    provider      text not null,
    intent        text not null default 'login'
                  check (intent in ('login', 'link')),
    user_id       uuid references users (id) on delete cascade, -- set for link
    state_hash    bytea not null unique,
    browser_hash  bytea,                      -- binds the flow to the browser
    nonce         text not null,
    code_verifier text not null,
    redirect_uri  text not null,
    return_to     text,
    created_at    timestamptz not null default now(),
    expires_at    timestamptz not null,
    consumed_at   timestamptz
);

-- One row per login family (a browser session or a mobile device session).
create table sessions (
    id                uuid primary key default gen_random_uuid(),
    user_id           uuid not null references users (id) on delete cascade,
    kind              text not null check (kind in ('web', 'mobile')),
    user_agent        text,
    ip                inet,
    created_at        timestamptz not null default now(),
    last_used_at      timestamptz not null default now(),
    idle_expires_at   timestamptz not null,
    absolute_expires_at timestamptz not null,
    revoked_at        timestamptz,
    revoked_reason    text
);

create index sessions_user_idx
    on sessions (user_id)
    where revoked_at is null;

create index oauth_flows_expiry_idx
    on oauth_flows (expires_at)
    where consumed_at is null;

-- Opaque bearer values. The raw token is never stored; only its SHA-256 hash.
create table session_tokens (
    id             uuid primary key default gen_random_uuid(),
    session_id     uuid not null references sessions (id) on delete cascade,
    kind           text not null check (kind in ('web', 'access', 'refresh')),
    token_hash     bytea not null unique,
    issued_at      timestamptz not null default now(),
    expires_at     timestamptz not null,
    rotated_at     timestamptz,
    replaced_by    uuid references session_tokens (id) on delete set null,
    revoked_at     timestamptz
);

create index session_tokens_session_idx on session_tokens (session_id);

create index session_tokens_expiry_idx on session_tokens (expires_at);

create table plans (
    code       text primary key,              -- free, plus, pro
    name       text not null,
    limits     jsonb not null,                 -- validated by a JSON Schema
    active     boolean not null default true,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table subscriptions (
    user_id            uuid primary key references users (id) on delete cascade,
    plan_code          text not null references plans (code),
    status             text not null
                       check (status in ('active', 'trialing', 'past_due', 'canceled')),
    source             text not null default 'manual'
                       check (source in ('manual', 'stripe', 'apple', 'google')),
    current_period_end timestamptz,
    overrides          jsonb not null default '{}',
    created_at         timestamptz not null default now(),
    updated_at         timestamptz not null default now()
);

create table ai_usage (
    id            bigint generated always as identity primary key,
    user_id       uuid not null references users (id) on delete cascade,
    event_id      uuid references events (id) on delete set null,
    kind          text not null check (kind in ('setup', 'check')),
    used_at       timestamptz not null default now(),
    model         text not null,
    prompt_version text not null,
    input_tokens  integer not null default 0,
    output_tokens integer not null default 0,
    cost_usd      numeric(12, 6) not null default 0,
    outcome       text not null               -- ok | invalid | refused | error
);

create index ai_usage_user_day_idx
    on ai_usage (user_id, used_at);
```

Notes:

- `citext` plus the partial unique index gives case-insensitive email
  uniqueness without blocking a new account after a deleted one. If `citext`
  is unwanted, store `lower(email)` and index that instead.
- Provider tokens are **not** stored. We keep only the identity claims needed to
  sign the user in. No access token, refresh token or raw ID token goes to the
  database or the logs.
- `oauth_flows` is single-use and expires after about 10 minutes. `state_hash`
  and `browser_hash` stop replay and login CSRF; `nonce` and `code_verifier`
  are consumed by the callback. A link flow stores `intent = 'link'` and the
  signed-in `user_id`, so the callback cannot attach a provider identity to the
  wrong account.
- A mobile login is one `sessions` row with an access token (15 minutes) and a
  rotating refresh token (30 days idle / 90 days absolute). A web login is one
  `sessions` row with a `web` token delivered as a cookie. Both use the same
  revocation path, so "sign out everywhere" is one update.
- Access tokens are opaque and looked up by hash rather than stateless JWTs.
  That costs one indexed query per request and buys immediate revocation, which
  matters more here than shaving a database round trip. If scale later makes
  that query hot, cache `session_id → user/plan` briefly and keep the token
  hash check in Postgres.
- Cleanup job deletes expired `oauth_flows` and `session_tokens`, and revokes
  sessions whose absolute expiry has passed.
- A provider may not return an email at all (Facebook when the permission is
  declined; Apple always returns one, possibly a private relay). Sign-in is
  keyed by `(provider, subject)`, so a missing email does not block the login:
  create the user with a null email and let them add one later if a channel
  needs it. Never invent a placeholder address.

### Account linking

- Login first resolves `(provider, subject)`. That is the only identity key.
- If no identity exists and the provider supplies a **verified** email that
  matches exactly one active user, link the identity to that user and send a
  notification. This avoids duplicate accounts across Google and Apple.
  Facebook Graph does not expose an `email_verified` flag, so its email is
  treated as unverified and never triggers an auto-link.
- Do **not** auto-link an unverified or missing email, or an Apple private-relay
  address. Create a separate account in that case. A signed-in user can
  explicitly link a provider through `/api/v1/auth/link/{provider}`.
- Never merge accounts on a client-supplied email. The backend fetches or
  verifies the email from the provider; it does not trust the mobile app's
  claim.
- A user cannot unlink their last identity; they can delete the account instead.
- Changing a provider email updates `user_identities.email`; it does not
  silently change `users.email` behind the user's back. The account email is
  user-editable (subject to verification) if that feature is added.

## 5. OAuth flows

### Web (backend-mediated authorization code + PKCE)

1. The web app sends the browser to
   `GET /api/v1/auth/{provider}/start?return_to=/events/123`.
   `return_to` must be a relative path or an exact allow-listed origin; never an
   open redirect.
2. The backend creates an `oauth_flows` row with a random state, nonce and PKCE
   verifier (S256), sets a short-lived `__Host-dp_oauth` cookie bound to the
   flow, and redirects to the provider.
3. The provider returns to the exact registered callback:
   `GET /api/v1/auth/{provider}/callback` for Google/Facebook, or
   `POST /api/v1/auth/{provider}/callback` for Apple's `form_post`.
4. The backend validates state, the browser cookie, the flow's expiry and
   single-use status, then exchanges the code with the provider. The client
   secret is used only here, server-side.
5. It verifies the resulting credential (ID token or Graph lookup), upserts the
   user and identity, creates a session, sets
   `__Host-dp_session` (`Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`) and
   redirects to the web app. No session or access token appears in the URL or
   in JavaScript; the Apple fallback below uses only a one-time completion
   code.
6. Errors redirect to a fixed frontend error route with a short opaque code.
   Provider error text is logged server-side, not reflected to the browser.

Apple sends the callback as a cross-site `POST` when `response_mode=form_post`
is used. A `SameSite=Lax` cookie is not sent on that request, so the dedicated
`__Host-dp_oauth` state cookie uses `SameSite=None; Secure` for the short
lifetime of the flow. The session cookie remains `Lax`. This is the one place
where the stricter cookie setting is relaxed, and only on the auth-callback
path; it is called out here so it is not "fixed" later in a way that breaks
Apple login. If a browser still refuses to store the session cookie on the
cross-site `POST` response, fall back to a single-use, 60-second completion
code in a first-party redirect and exchange it for the session with a `POST`
from the web app. The code is hashed at rest and bound to the flow. Never put
the actual session or access token in a URL.

### Mobile (native providers, backend-issued sessions)

- Use the platform's system browser or native provider SDK, following RFC 8252
  and RFC 7636: Authorization Code + PKCE, no client secret in the app, no
  embedded WebView for credentials.
  - iOS: `ASWebAuthenticationSession` for a generic browser flow, or the native
    Google/Facebook SDKs and `ASAuthorizationAppleIDProvider` for Apple.
  - Android: Custom Tabs / Credential Manager and the provider SDKs. Apple has
    no official native Android SDK, so Sign in with Apple goes through the web
    flow in a Custom Tab.
- The app obtains a provider credential:
  - Google: an ID token.
  - Apple: an identity token.
  - Facebook: an access token.
- Generate a fresh nonce per attempt for Google/Apple, pass it through the
  provider SDK and send the raw nonce to the backend alongside the credential.
  The backend verifies the claim (hashing it first for Apple) and rejects a
  mismatch. Facebook has no OIDC nonce; token validation and the short token
  lifetime are the controls there.
- The app posts the credential to
  `POST /api/v1/auth/{provider}/native`. The backend verifies it with the same
  rules as the web flow, then issues a DropPulse access token (15 minutes) and
  a rotating refresh token. The provider credential is never accepted as an API
  credential and is not stored.
- Tokens live in the iOS Keychain / Android Keystore. Never in `UserDefaults`,
  `SharedPreferences`, logs or a WebView.
- The app refreshes through `POST /api/v1/auth/refresh`; refresh tokens rotate
  on every use. A reused rotated token revokes the whole session family.
- Use Universal Links / App Links where possible. A custom scheme is acceptable
  only where the provider requires it, and the backend still validates state,
  PKCE and the exact redirect URI.

### App Store and provider rules

- App Store Review Guideline 4.8 requires an equivalent privacy-preserving
  login option when an app uses a third-party login service for its primary
  account. Sign in with Apple satisfies it; since the app offers Apple anyway,
  this is covered. Offer the Apple button with equal prominence.
- Account deletion must be available in the app, not only on the website.
- Facebook Login's `email` permission needs Advanced Access and, for new apps,
  Business Verification before it works for users outside the app's testers.
  The UI must still handle a missing email.
- Apple requires the server-to-server notification endpoint to be registered;
  registration has been mandatory since 1 January 2026. See §7.

## 6. Provider specifics

| Provider | Protocol | Scopes / claims | Server-side verification | Quirks |
|---|---|---|---|---|
| Google | OpenID Connect | `openid email profile` | Verify ID token signature against Google JWKS; check `iss`, `aud` (all accepted web/iOS/Android client IDs), `exp`, `nonce` | Multiple client IDs for web and mobile; accept only the configured set. |
| Apple | OpenID Connect | `name email` | Verify identity token via `https://appleid.apple.com/auth/keys`; check `iss`, `aud` (bundle ID or service ID), `exp`, nonce hash | Client secret is an ES256 JWT signed with the `.p8` key; max 6-month lifetime; `client_secret_post`; `form_post` for web; name/email only on first consent; private-relay email; `email_verified` may be a string. |
| Facebook | OAuth 2.0 + Graph API (not OIDC) | `public_profile`, `email` | `GET /debug_token` with an app access token; check `is_valid`, `app_id`, `user_id`, `expires_at`; then Graph `/me?fields=id,name,email,picture` | No ID token; app-scoped user ID is stable per app; pin a Graph API version; email may be absent and Graph has no `email_verified` flag, so treat it as unverified for linking; Advanced Access + Business Verification; deauthorize and data-deletion callbacks. |

Implementation stack:

- [`golang.org/x/oauth2`](https://pkg.go.dev/golang.org/x/oauth2) for the
  authorization-code exchange, PKCE helpers and provider endpoints.
- [`github.com/coreos/go-oidc/v3/oidc`](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)
  for Google and Apple discovery, JWKS caching and ID-token verification.
- [`github.com/golang-jwt/jwt/v5`](https://pkg.go.dev/github.com/golang-jwt/jwt/v5)
  for signing the short-lived Apple client-secret JWT.
- Facebook uses `x/oauth2` for the token exchange plus a small typed Graph API
  client; it is not an OIDC provider, so `go-oidc` does not apply.

Secrets (Google client secret, Apple `.p8` and key ID, Facebook app secret) come
from the environment or a secret manager. They are never committed, stored in
`config` JSONB, or logged. The Apple client secret is generated in memory,
cached only until shortly before expiry, and regenerated; it is not persisted.

## 7. Provider callbacks and account changes

- **Apple server-to-server notifications:** `POST /api/v1/webhooks/apple`.
  Verify the `payload` JWS against Apple's JWKS, check `iss` and `aud`, then
  handle `email-enabled`, `email-disabled`, `consent-revoked` and
  `account-deleted`. Respond `200` for handled and unknown events so Apple does
  not retry indefinitely. `consent-revoked` revokes the user's sessions;
  `account-deleted` deletes or anonymizes the identity and revokes sessions.
- **Facebook deauthorize callback:** remove the identity or mark it
  disconnected when the user removes the app; revoke sessions if that was the
  only identity.
- **Facebook data-deletion callback:** return the confirmation code and status
  URL required by Facebook, and delete the same data an in-app account deletion
  would delete.
- **Google:** no mandatory equivalent for basic OIDC login. If Google's
  cross-account-protection / RISC notifications are enabled later, they get
  their own verified webhook.
- All webhook endpoints are public by necessity but verify provider signatures
  before doing anything. They are rate-limited and never trust the body.

## 8. Sessions, CSRF and request authentication

### Token lifetimes

| Token | Lifetime | Storage | Rotation |
|---|---|---|---|
| Web session cookie | 30 days idle, 90 days absolute | `HttpOnly`, `Secure`, `SameSite=Lax`, `__Host-` prefix | New session on login; re-issued on refresh |
| Mobile access token | 15 minutes | Keychain / Keystore | New token on refresh |
| Mobile refresh token | 30 days idle, 90 days absolute | Keychain / Keystore | Rotate on every use; reuse revokes the family |

Refresh-token rotation follows RFC 9700: opaque high-entropy tokens, SHA-256
hashes at rest, an atomic compare-and-set on rotation, family-wide revocation
on reuse, and a short grace window (or client single-flight) so two parallel
refreshes do not look like an attack.

### CSRF and CORS

- Auth and API traffic is HTTPS only; HSTS is set at the edge. Cookies are
  `Secure` and never sent over plain HTTP.
- Cookie-authenticated mutations require an exact `Origin` match and a
  double-submit CSRF token (`X-CSRF-Token` matching a `__Host-dp_csrf` cookie),
  in addition to `SameSite=Lax`.
- The API allows only exact configured frontend origins with credentials.
  There is no `*` origin, no origin reflection, and no wildcard subdomain.
- Mobile requests use `Authorization: Bearer` and are not cookie-authenticated,
  so they do not use CSRF tokens.
- `GET` requests never change state. Logout is `POST`, even though it is
  harmless, so a link cannot sign a user out.

### Authorization

- Middleware resolves the token, loads the user, rejects `suspended`/`deleted`
  users, and loads the effective plan. The user ID is taken from the session,
  never from a request body or query parameter.
- Ownership checks are explicit: a monitor is visible only to its user; an
  event's config is editable only by its creator or an administrator.
- Admin access is a `role` check plus a separate audit log entry. Admin
  endpoints are not part of the public API in v1; plan assignment and account
  support can start as SQL/CLI operations. The other roles (`vendor`, `agent`,
  `ai`) are account types, not privilege levels, and do not grant admin access.
- Authentication failures are deliberately vague (`401` with a code such as
  `not_authenticated`) and do not reveal whether an email or identity exists.
- Rate-limit the auth start/callback/native/refresh endpoints by IP and by
  provider, and the general API by user. Return `429` with `Retry-After`.

## 9. API surface

Public (no session):

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness |
| GET | `/api/v1/auth/{provider}/start` | Begin web sign-in |
| GET/POST | `/api/v1/auth/{provider}/callback` | Provider redirect (POST for Apple) |
| POST | `/api/v1/auth/{provider}/native` | Verify a native credential |
| POST | `/api/v1/auth/refresh` | Rotate a mobile refresh token or renew a web session |
| POST | `/api/v1/webhooks/apple` | Apple account-change notifications |
| POST | `/api/v1/webhooks/facebook/deauthorize` | Facebook deauthorization |
| POST | `/api/v1/webhooks/facebook/data-deletion` | Facebook deletion request |

Authenticated (session or bearer token):

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/me` | User, effective plan, entitlements, usage |
| DELETE | `/api/v1/me` | Delete/anonymize the account |
| GET | `/api/v1/me/export` | Data export |
| GET | `/api/v1/me/sessions` | List active sessions |
| DELETE | `/api/v1/me/sessions/{id}` | Revoke one session |
| POST | `/api/v1/auth/logout` | Revoke the current session |
| POST | `/api/v1/auth/link/{provider}` | Link another provider |
| GET | `/api/v1/plugins` | Registered site kinds and config schemas |
| POST | `/api/v1/events/resolve` | Site + URL/ID/query → candidates |
| POST | `/api/v1/events` | Register an event (owner becomes a watcher) |
| GET | `/api/v1/events` | Events the user watches or created |
| GET | `/api/v1/events/{id}` | Event metadata (any authenticated user) |
| PATCH | `/api/v1/events/{id}` | Owner: config, interval, watch mode |
| DELETE | `/api/v1/events/{id}` | Owner: only when no other watchers |
| GET | `/api/v1/events/{id}/checks` | History for watchers/owner/admin |
| POST | `/api/v1/ai/preview` | Tier-gated AI setup preview for a site/URL |
| POST | `/api/v1/monitors` | Subscribe to an event |
| GET | `/api/v1/monitors` | List the caller's watches |
| PATCH | `/api/v1/monitors/{id}` | Enable/pause, filters, notifications |
| DELETE | `/api/v1/monitors/{id}` | Unsubscribe |

`GET /api/v1/me` includes the plan code, the remaining AI quota for the current
day, active-watch count and the limits object, so clients do not hard-code
tier rules. `history_days` applies to events the user owns; shared events use
their owner's retention. The backend remains the enforcement point; the
response is for UI only.

## 10. Entitlements and enforcement

### Effective plan

`subscriptions` is the source of truth when present and in an active/trialing
state; otherwise the user is on `free`. `overrides` is a small JSONB patch for
support cases and should be rare. The effective plan is cached briefly (for
example 60 seconds) and invalidated on subscription change. Entitlements are
computed as typed values, not read ad hoc from JSONB at each call site:

```go
type Mode string

const (
    ModeBasic    Mode = "basic"
    ModeScripted Mode = "scripted"
    ModeAI       Mode = "ai_assisted"
)

type Entitlements struct {
    PlanCode              string `json:"plan_code"`
    MaxActiveWatches      int    `json:"max_active_watches"`
    AllowedModes          []Mode `json:"allowed_modes"`
    MinIntervalSeconds    int    `json:"min_interval_seconds"`
    AISetupPreviewsPerDay int    `json:"ai_setup_previews_per_day"`
    AIChecksPerDay        int    `json:"ai_checks_per_day"`
    HistoryDays           int    `json:"history_days"`
}
```

Example `plans.limits`:

```json
{
  "max_active_watches": 3,
  "allowed_modes": ["basic"],
  "min_interval_seconds": 900,
  "ai_setup_previews_per_day": 0,
  "ai_checks_per_day": 0,
  "history_days": 30
}
```

### Enforcement points

1. **Create or update an event** — mode must be allowed; interval must be at
   least the plan floor and the site floor. Creation also creates the owner's
   monitor, so reject the whole transaction if the owner has no watch allowance
   left; never leave an orphan event.
2. **Create or enable a monitor** — count active monitors; require entitlement
   for the event's mode; validate the filter schema.
3. **Scheduler, before every run** — re-resolve the owner's plan and verify the
   event's mode is still allowed and the interval still meets the floor. A
   subscription-change hook and a periodic reconciliation do the same for every
   subscriber's monitor, so a lapsed plan cannot keep a premium watch running
   just because no API call happened.
4. **AI call** — reserve the relevant quota (`setup` or `check`), check it
   atomically, record `ai_usage`; an exhausted quota falls back to the
   deterministic result or records `unknown`, never silently overruns.
5. **Read/history** — apply the event owner's retention at query time and in
   the cleanup job, not by deleting data the moment a user downgrades.

### Downgrade behavior

- Do not delete monitors or events. Pause monitors above the new limit,
  monitors on events whose mode is no longer allowed, and owned events whose
  mode is no longer allowed; notify the user with the list and the reason.
- Keep history until the new plan's retention window expires. A user who
  re-upgrades gets the remaining data, not a destroyed account.
- On upgrade, do not auto-enable paused premium events without confirmation;
  show what would resume and let the user choose. This avoids surprise costs
  and surprise polling.
- If a subscription lapses, treat it as `free` after the grace period recorded
  by the billing integration. Until billing exists, status is manual.

### Abuse controls

Tier limits are not the only limit. Per-site token buckets, per-user request
limits, challenge backoff and the "no CAPTCHA bypass" policy apply to every
tier. A Pro subscription does not authorize a higher request rate than the
site permits, and does not turn a blocked site into a supported one.

## 11. AI-assisted watch

There are two distinct features behind this name, and both are Pro-tier:

1. **Setup assistance.** Given a sample page and the user's description, Claude
   proposes selectors or a declarative extraction config. The user reviews and
   confirms it. It never silently publishes a script or starts a watch.
2. **Check assistance.** When the deterministic plugin parse fails or is
   ambiguous, Claude extracts availability from the page text. The result is
   stored with `extractor = 'ai'` and a confidence; low confidence becomes
   `unknown`, not a false "available".

It is explicitly **not** an agent that browses, clicks, queues or buys. It does
not solve challenges. It gets the same sanitized page the plugin already
fetched through the shared HTTP client, not a browser session or the user's
cookies.

### Request design

- The preview fetches through the same shared HTTP client as a normal check:
  configured site, host allowlist, SSRF protection, size and time caps. It is
  not a generic URL fetcher, and the user cannot point it at an internal host.
- Model: `claude-opus-5-5` by default, called through the official Anthropic Go
  SDK (`github.com/anthropics/anthropic-sdk-go/v2`). Opus is the quality
  default; if the measured cost per check is unacceptable, moving to
  `claude-sonnet-5-5` or `claude-haiku-5-5` is an explicit owner decision, not
  a silent optimization.
- Use structured output constrained by a JSON Schema, for example:

  ```json
  {
    "type": "object",
    "required": ["availability", "confidence"],
    "properties": {
      "availability": {
        "type": "string",
        "enum": ["available", "few_left", "sold_out", "not_on_sale", "unknown"]
      },
      "min_price": { "type": ["number", "null"] },
      "max_price": { "type": ["number", "null"] },
      "currency": { "type": ["string", "null"] },
      "offers": { "type": "array", "items": { "type": "object" } },
      "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
      "evidence": { "type": "string", "maxLength": 500 }
    }
  }
  ```

- Keep the static system prompt stable and use prompt caching; put the
  event-specific and page-specific content after it. Strip scripts, styles,
  hidden fields, cookies and anything that looks like a credential before
  sending. Cap the input (for example 20,000 characters); if the page is
  truncated, lower the confidence and prefer `unknown` over a guess.
- Pin the schema version and store the model ID and prompt version on each
  result so quality can be compared over time.
- Validate the response against the schema and against the plugin's own
  invariants (currency matches the event, prices are plausible, offers do not
  appear from nowhere). An invalid response is an error, not an availability
  change.
- Treat the page as hostile input. The system prompt says the page is untrusted
  data, not instructions; the model has no tools, no network access and no
  ability to trigger a purchase. Prompt injection can at worst produce a bad
  extraction, which schema validation, confidence thresholds and the eval set
  are there to catch. Render extracted evidence as escaped text, not HTML, and
  never auto-open a URL that the model produced.
- Do not send personal data, auth headers, cookies or the user's identity. The
  page text is public content already fetched by the plugin.
- Log token counts and estimated cost in `ai_usage`, not page text. Alert on
  quota exhaustion and on a sudden cost per check increase.

### Quality and cost controls

- Build a labelled fixture set per site: sold out, available, few left, error
  page, challenge page, and a page containing adversarial instructions. Run the
  AI extractor against it before enabling the feature for that site.
- Require a minimum confidence (start around 0.8) and a minimum agreement with
  the deterministic parser when both succeed. Below that, return `unknown` and
  let the user see the uncertainty.
- Prefer the deterministic path. `ai_assisted` means AI is *allowed*, not that
  every poll must call it: run the deterministic parser first and invoke Claude
  only when it fails, is ambiguous, or the site's eval shows the AI path is the
  reliable one. Cache the result for the same event/check window; do not pay
  twice for one page.
- Quota is per user per UTC day, split between `setup` previews and `check`
  extractions, enforced in a transaction and recorded in `ai_usage`. The check
  quota belongs to the event owner, because the event's watch mode is what
  consumes AI checks; subscribers to that event do not each burn their own
  quota for the same shared check.
- If the quota is exhausted, record `unknown` with an `ai_quota_exhausted`
  reason and notify the owner; do not silently fall back to a stale "available"
  state.

## 12. Privacy, account lifecycle and compliance

- **Data minimisation:** store the provider subject, email, display name,
  avatar URL and verification flags. Do not store provider access tokens,
  refresh tokens, ID tokens or raw claims.
- **Account deletion:** `DELETE /api/v1/me` requires a recent session (or a
  fresh provider re-authentication), revokes all sessions, deletes identities,
  deletes monitors, deletes owned events that have no other watchers, and
  anonymizes the user row. Shared events survive without an owner, as described
  in §3. The UI must offer this in the mobile apps too.
- **Export:** `GET /api/v1/me/export` returns the user's profile, monitors,
  owned events, notification preferences and AI usage in a machine-readable
  archive. It does not include other users' data.
- **Retention:** expired flows/tokens are deleted; check history follows the
  event owner's plan retention (§2); deleted accounts are purged on a schedule.
  Backups have their own retention and are documented in the privacy policy.
- **Processors:** Anthropic processes page text for AI-assisted checks; the
  privacy policy and DPA must name it. OAuth providers receive the standard
  authorization data. No analytics or advertising SDKs in the mobile apps.
- **Terms:** record `terms_version` and `terms_accepted_at` at first login.
  Material changes require re-acceptance before the next watch is created.
- **Security logging:** log auth events (login, link, revoke, deletion,
  webhook) with user ID and request ID, never tokens. Keep an audit trail for
  admin actions.

## 13. Phased implementation

1. **Auth foundation** — migrations for users, identities, flows, sessions;
   Google and Apple web login; session middleware; `/api/v1/me`; make every
   existing route authenticated. This is the first PR-sized slice and should
   land before any ticket API is exposed.
2. **Facebook and mobile** — Facebook OAuth + Graph verification; native
   credential endpoints; refresh rotation and reuse detection; Keychain /
   Keystore integration; App Store 4.8 checks.
3. **Plans and entitlements** — `plans`, `subscriptions`, typed entitlements,
   enforcement at event/monitor creation, `/me` limits and usage. Seed the
   three placeholder plans; plan assignment remains manual.
4. **Event ownership and quota** — add `created_by_user_id`, `watch_mode`,
   ownership checks, active-watch counting, downgrade/pause behavior. Update
   the ticket API to be user-scoped.
5. **AI-assisted watch** — setup preview, check fallback, schema validation,
   fixture evals, `ai_usage`, quotas, cost alerts. Gate per site only after its
   eval passes.
6. **Billing and support** — Stripe/App Store/Google Play integration, plan
   changes, invoices, admin tooling. Depends on the pricing decision, not on
   the auth model.
7. **Provider callbacks and account lifecycle** — Apple server notifications,
   Facebook deauthorize/deletion callbacks, export, scheduled purge. Register
   the Apple notification endpoint before launch; the 1 January 2026 deadline
   has already passed, so this is not optional.

## 14. Open questions for the owner

1. **Tier names, prices and exact limits.** The table in §2 is a placeholder.
2. **Scripted watches: curated or user-authored?** Curated scripts can use the
   existing sobek host and be reviewed by us. User-authored scripts need the
   WASM runner from `research.md` before the tier can be sold safely.
3. **May lower-tier users subscribe to a premium-mode event?** The
   recommendation above is no, to prevent free-riding. The alternative is to
   allow reading the shared result but not creating AI/scripted events; that is
   harder to price and audit.
4. **AI budget and model.** Opus 5.5 is the default for quality. The per-check
   cost and the daily quota determine whether that is viable at the target
   price, or whether Sonnet/Haiku is the right default for fallback extraction.
5. **Billing channel.** Web billing (Stripe) and in-app purchases on iOS and
   Android have different rules and different entitlement sources. The
   `subscriptions.source` column anticipates this, but the product decision is
   open.
6. **Account linking policy.** Auto-link on verified email is convenient but
   means a provider email change can move an account. Explicit linking is
   safer but produces duplicates. The compromise in §4 should be confirmed.
7. **Ownership transfer.** What happens to a premium event when its owner
   leaves, besides pausing it for an administrator? A self-service transfer to
   another entitled watcher may be better than an admin queue.
8. **Premium mode on a shared event.** The v1 model puts `watch_mode` on the
   event, so a Pro user who did not create the event cannot upgrade it. Is an
   administrator-mediated change enough for launch, or should events support
   per-mode check plans (basic and AI checks on the same event, each shared by
   the watchers entitled to it)? The latter is more flexible and avoids
   free-riding, but it is a larger scheduler change.
9. **Notifications.** The tier table does not yet include channels (push,
   email, webhook). Those limits need a separate decision.

## Sources

- OAuth 2.0 Security Best Current Practice, RFC 9700 (refresh-token rotation
  and reuse detection): <https://www.rfc-editor.org/rfc/rfc9700>
- OAuth 2.0 for Native Apps, RFC 8252: <https://www.rfc-editor.org/rfc/rfc8252>
- PKCE, RFC 7636: <https://www.rfc-editor.org/rfc/rfc7636>
- Google OpenID Connect: <https://developers.google.com/identity/openid-connect/openid-connect>
- Sign in with Apple REST API and token verification:
  <https://developer.apple.com/documentation/signinwithapple/verifying-a-user>
- Processing changes for Sign in with Apple accounts:
  <https://developer.apple.com/documentation/signinwithapple/processing-changes-for-sign-in-with-apple-accounts>
- App Store Review Guidelines, 4.8 Login Services:
  <https://developer.apple.com/app-store/review/guidelines/#login-services>
- Facebook Login permissions and review:
  <https://developers.facebook.com/docs/permissions/>
- Facebook `debug_token`: <https://developers.facebook.com/docs/graph-api/reference/debug_token/>
- Facebook data deletion callback:
  <https://developers.facebook.com/docs/development/create-an-app/data-deletion-callback/>
- `golang.org/x/oauth2`: <https://pkg.go.dev/golang.org/x/oauth2>
- `coreos/go-oidc`: <https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc>
- `golang-jwt/jwt`: <https://pkg.go.dev/github.com/golang-jwt/jwt/v5>
- Anthropic Go SDK: <https://github.com/anthropics/anthropic-sdk-go>
- Anthropic Claude API documentation: <https://docs.anthropic.com/en/api/overview>
