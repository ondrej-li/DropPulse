# DropPulse

DropPulse is organised as three elements in one repository:

| Element | Directory | Stack |
|---|---|---|
| Backend | [`backend/`](backend/) | Go, net/http and Postgres (pgx) |
| Frontend | [`frontend/`](frontend/) | Plain HTML, CSS and JavaScript |
| Mobile app | [`mobile/`](mobile/) | iOS and Android |

The backend owns the API. The frontend and both mobile apps are clients of it.

## Running locally

The backend needs a Postgres database. It applies its embedded migrations on
start-up, so an empty database is enough:

```sh
createdb droppulse
cd backend
DATABASE_URL='postgres://localhost/droppulse?sslmode=disable' \
  SINGLE_USER_MODE=true \
  FRONTEND_FILE=../frontend/index.html \
  go run ./cmd/server
```

It listens on `127.0.0.1:8080` unless `BIND_ADDR` or `PORT` is set. `GET /healthz`
answers with `{"status":"ok"}`.

In single-user mode there is no login. The backend creates (or repairs) the
`admin` account, serves the frontend at `/`, and exposes the events API. This
mode is refused unless the listener is a loopback address; every request also
has to carry a loopback `Host` header and, on unsafe methods, an exactly
same-origin `Origin` header. It is a local proof of concept, not a way to
publish an unauthenticated API.

Without `SINGLE_USER_MODE`, `/` serves `FRONTEND_FILE` if it is set, and the
landing page is public. The page asks the public provider endpoint which
sign-in methods exist; Google and Apple are supported. On iOS and macOS the
interface offers Apple only, as requested; on other platforms it offers both
when both are configured. `/api/v1/events` and every other watch route require
a session, so storing a watch always needs a signed-in user. A local example:

```sh
cd backend
DATABASE_URL='postgres://localhost/droppulse?sslmode=disable' \
  FRONTEND_FILE=../frontend/index.html \
  PUBLIC_API_URL=http://localhost:8080 \
  GOOGLE_CLIENT_ID='...' GOOGLE_CLIENT_SECRET='...' \
  go run ./cmd/server
```

The built-in frontend talks to the API on its own origin. Put it behind the
backend (as above) or proxy `/api/` to the backend from the same origin.

Configuration is read from the environment and validated at start-up:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Required Postgres connection string |
| `BIND_ADDR` | Address to bind (default `127.0.0.1`) |
| `PUBLIC_API_URL` | Public origin of this API (default `http://localhost:8080`); its `/api/v1/auth/<provider>/callback` paths are the OAuth redirect URIs |
| `WEB_APP_URL` | Frontend origin users are redirected to after login (default `http://localhost:8000`, or the API origin when `FRONTEND_FILE` is set) |
| `ALLOWED_ORIGINS` | Exact comma-separated CORS origins; defaults to `WEB_APP_URL`. Wildcards are rejected |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Enable Google sign-in |
| `APPLE_CLIENT_ID`, `APPLE_TEAM_ID`, `APPLE_KEY_ID`, `APPLE_PRIVATE_KEY` or `APPLE_PRIVATE_KEY_FILE` | Enable Apple sign-in |
| `FACEBOOK_CLIENT_ID`, `FACEBOOK_CLIENT_SECRET`, `FACEBOOK_GRAPH_VERSION` | Enable Facebook sign-in |
| `ADMIN_EMAILS` | Comma-separated verified addresses that receive the admin role on first sign-in |
| `SESSION_IDLE_TTL`, `SESSION_ABSOLUTE_TTL`, `OAUTH_FLOW_TTL` | Session and login-flow lifetimes |
| `TRUSTED_PROXY_CIDRS` | Proxies whose `X-Real-IP` / `X-Forwarded-For` are trusted (default loopback) |
| `AUTH_RATE_LIMIT_PER_MINUTE`, `API_RATE_LIMIT_PER_MINUTE` | In-process rate limits |
| `SINGLE_USER_MODE` | Serve the frontend and events API without a login; requires a loopback `BIND_ADDR` |
| `SINGLE_USER_NAME` | Username of the fixed local account (default `admin`) |
| `FRONTEND_FILE` | Path to the single HTML app served at `/`; required in single-user mode |
| `NOTIFICATION_EMAIL` | Fixed recipient for availability email; requires single-user mode and `SMTP_FROM` |
| `SMTP_FROM` | Envelope/From address for notifications. In multi-user mode it enables mail to each account's verified address |
| `SENDMAIL_PATH` | Local MTA submission command (default `/usr/sbin/sendmail`; must be absolute and executable) |

`PUBLIC_API_URL` and `WEB_APP_URL` must use HTTPS unless they point at
localhost. Registered OAuth callback URLs must match exactly. The web frontend
and the API must be same-site (for example `app.example.com` and
`api.example.com`): the session cookie is `SameSite=Lax`, not `None`.

## Accounts and sign-in

The landing page is public and there are no passwords: signing in means
completing an OAuth/OIDC flow with a configured provider. `GET
/api/v1/auth/providers` is public and returns the configured provider names so
the page can render the right buttons before anybody has a session. It exposes
names only, never credentials.

Google and Apple are the sign-in providers the landing page offers. The auth
foundation can also be configured with Facebook, but the page deliberately
does not show it.

| Provider | Environment | Callback URL to register |
|---|---|---|
| Google | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`; issuer `https://accounts.google.com` | `<PUBLIC_API_URL>/api/v1/auth/google/callback` |
| Apple | `APPLE_CLIENT_ID`, `APPLE_TEAM_ID`, `APPLE_KEY_ID`, and `APPLE_PRIVATE_KEY` or `APPLE_PRIVATE_KEY_FILE`; issuer `https://appleid.apple.com` | `<PUBLIC_API_URL>/api/v1/auth/apple/callback` |

The callback URL must match exactly. Apple posts its callback (`response_mode=form_post`),
so that endpoint accepts `POST` as well as `GET`; the short-lived Apple client
secret is signed by the backend with the ES256 key, not stored. The sign-in
flow is web-only: sessions are cookies, not bearer tokens, so a native app needs
the same browser flow, and a future mobile client has no token endpoint yet.

The interface offers Apple only on iOS and macOS and both providers elsewhere,
as requested. That is a UI policy, not an authorization rule: the backend
accepts any configured provider from any client, because the user agent is
trivially spoofable and a policy enforced on it would be false assurance.

Accounts are identified by verified email. After the ID token is verified
(signature, issuer, audience, nonce and expiry), the account lookup is:

1. resolve the `(provider, subject)` identity; if it exists, that is the account;
2. otherwise, if the provider asserts a verified email and exactly one active
   account has that verified address, link the new identity to it — signing in
   with the other provider later lands in the same account;
3. otherwise create an account. An email that is not verified is kept on the
   identity but never promoted to the account's address, so it cannot claim
   somebody else's account or receive their notifications.

Apple's "Hide My Email" relay address is verified but differs from the address
Google would assert, so a person who uses it will not be auto-linked across
providers. Signing in with the same provider again still finds the same
account.

Sessions are opaque tokens whose SHA-256 is stored server-side. The cookie is
`__Host-dp_session` on HTTPS (`__Host-` requires a secure, host-only cookie),
`SameSite=Lax`, and session-relative. Unsafe requests must also carry
`X-CSRF-Token` matching the `__Host-dp_csrf` cookie and the session's stored
hash; the frontend reads the cookie and adds the header. `POST
/api/v1/auth/refresh` rotates the session, `POST /api/v1/auth/logout` revokes
it, `GET /api/v1/me/sessions` lists the account's sessions and `DELETE
/api/v1/me/sessions/{id}` revokes one.

## Events and notifications

An event is a name, an HTTPS URL, a check interval and an enabled flag. A
generic watch issues one `GET`; exactly HTTP 200 means the event is on.
Redirects are not followed, the request has a timeout, no cookies or proxy are
used, and the checker refuses loopback, private, link-local, CGNAT, multicast and
reserved destinations. It resolves a hostname once and dials the resulting public
IP directly, so DNS rebinding cannot redirect the request.

A URL on a built-in provider host is interpreted by that provider instead of
being checked for HTTP 200. The first provider is `cinemacity.cz`: a film page
URL such as
`https://www.cinemacity.cz/films/<slug>/<film-id>?cinema=<cinema-id>&auditorium=IMAX`
is checked against Cinema City's public programme API. The watch is on when at
least one matching show is not sold out and has seats available. The optional
`date=YYYY-MM-DD` narrows the check to one date and `auditorium` matches either
the full room name or its short name. The film page itself is never fetched, and
the provider uses the same guarded client, redirect policy and response-size
limit as the generic checker. An upstream failure is recorded as an error, never
as a sold-out result.

The watch routes are mounted in both modes. In single-user mode they act on the
fixed local account; otherwise every one of them requires a session and is
scoped to the signed-in account, so one user can never see or change another
user's events.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/me` | Account, plan, configured providers and enabled-event count |
| `GET`, `DELETE` | `/api/v1/me/sessions`, `/api/v1/me/sessions/{id}` | List or revoke sessions |
| `GET` | `/api/v1/events` | List events |
| `POST` | `/api/v1/events` | Create an event |
| `GET`, `PUT`, `DELETE` | `/api/v1/events/{id}` | Read, replace or delete one event |
| `POST` | `/api/v1/events/{id}/check` | Run a check now |

A create or replace body looks like:

```json
{
  "name": "Saturday show",
  "url": "https://tickets.example/show",
  "check_interval_seconds": 300,
  "enabled": true
}
```

Intervals are 60 to 86400 seconds (default 300). The scheduler runs every 15
seconds and checks whatever is due; events of suspended or deleted accounts are
skipped. The UI is served at `/` when `FRONTEND_FILE` is set and refreshes every
15 seconds.

The UI creates events through a three-step wizard: choose a provider template,
fill in template-specific details and watch settings, then choose a notification
channel. The template catalogue lives in `frontend/index.html` and currently has
the `cinemacity.cz` Flora IMAX watch and an `Example.com` placeholder; template
management will move it server-side for higher tiers. Email is the only
notification channel. A theme control switches between Automatic, Light and Dark
and stores the choice in `localStorage`.

### Email

Email is sent when `SMTP_FROM` is set. In single-user mode the recipient is the
fixed `NOTIFICATION_EMAIL`; in multi-user mode it is the verified address of the
account that owns the event. An account with no verified address sends nothing,
and nothing is guessed or sent to a placeholder. The backend sends one plain
text message per off→on transition. While an event stays on it sends nothing
more; when the event goes off the edge re-arms, so a later show or restock sends
another message. Editing the URL resets the availability state and re-arms the
edge. A send failure is recorded and retried on the next check while the event
remains on.

Mail is submitted with `SENDMAIL_PATH` (default `/usr/sbin/sendmail`), the
local MTA's privileged submission command. The backend deliberately does not
use the SMTP listener: the host's SMTP policy refuses relay to external
recipients from loopback, and relaxing that policy to make the check pass would
weaken a security control. Locally submitted mail still passes through
Postfix's `non_smtpd_milters`, so rspamd signs it. The backend never connects
to a remote SMTP server and has no SMTP credentials.

## Public preview

`frontend/public/index.html` is a static holding page for
`https://dp.ondralinek.cz`. nginx serves it from `/var/www/dp` with a strict
per-IP limit (1 request/second, burst 5, 3 concurrent streams) and never
proxies the Go backend; `/api/` and `/healthz` return `404` on that domain. The
application UI at `frontend/index.html` is not published there, and the backend
still listens on loopback only. Deployment of the holding page is one command,
`~/DropPulse/deploy/deploy.sh`; see [`deploy/DEPLOY.md`](deploy/DEPLOY.md).

Serving the application itself needs the backend reachable by the browser and
OAuth credentials registered for that origin. Nothing in this repository
deploys that, and no provider secret belongs in the tree or in the static
preview.

## SSH tunnel

The default loopback bind keeps a development instance unreachable from
outside the host. Reach it through an SSH tunnel from your own machine:

```sh
ssh -N -L 8090:127.0.0.1:8090 <user>@<host>
# then open http://localhost:8090/
```

Set `PORT=8090` (or any free port) on the server and use the same port in the
tunnel. Set `BIND_ADDR=0.0.0.0` only when a reverse proxy or firewall policy
requires a non-loopback listener; single-user mode refuses to start in that
case.

`frontend/index.html` is a single file with inline CSS and JavaScript and no
external assets. The backend serves it; opening it directly from disk will not
reach the API.

The mobile apps are placeholders; see [`mobile/ios/`](mobile/ios/) and
[`mobile/android/`](mobile/android/).

## CI

`.github/workflows/ci.yml` builds, vets and tests the backend on every pull
request. Nothing is built on the VPS.
