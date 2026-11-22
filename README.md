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
answers with `{"status":"ok"}`. Without `SINGLE_USER_MODE`, all product routes
require a session and the only public routes are `/healthz` and the OAuth
start/callback endpoints.

In single-user mode there is no login. The backend creates (or repairs) the
`admin` account, serves the frontend at `/`, and exposes the events API. This
mode is refused unless the listener is a loopback address; every request also
has to carry a loopback `Host` header and, on unsafe methods, an exactly
same-origin `Origin` header. It is a local proof of concept, not a way to
publish an unauthenticated API.

Configuration is read from the environment and validated at start-up:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Required Postgres connection string |
| `BIND_ADDR` | Address to bind (default `127.0.0.1`) |
| `PUBLIC_API_URL` | Public origin of this API (default `http://localhost:8080`); its `/api/v1/auth/<provider>/callback` paths are the OAuth redirect URIs |
| `WEB_APP_URL` | Frontend origin users are redirected to after login (default `http://localhost:8000`) |
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
| `FRONTEND_FILE` | Path to the single HTML file served at `/` in single-user mode |
| `NOTIFICATION_EMAIL` | Recipient for availability email; requires single-user mode and `SMTP_FROM` |
| `SMTP_FROM` | Envelope/From address used for notifications |
| `SENDMAIL_PATH` | Local MTA submission command (default `/usr/sbin/sendmail`; must be absolute and executable) |

`PUBLIC_API_URL` and `WEB_APP_URL` must use HTTPS unless they point at
localhost. Registered OAuth callback URLs must match exactly. The web frontend
and the API must be same-site (for example `app.example.com` and
`api.example.com`): the session cookie is `SameSite=Lax`, not `None`.

## Single-user events and notifications

The single-user slice is a proof of concept for event monitoring. An event is a
name, an HTTPS URL, a check interval and an enabled flag. A check issues one
`GET`; exactly HTTP 200 means the event is on. Redirects are not followed, the
request has a timeout, no cookies or proxy are used, and the checker refuses
loopback, private, link-local, CGNAT, multicast and reserved destinations. It
resolves a hostname once and dials the resulting public IP directly, so DNS
rebinding cannot redirect the request.

The events API is mounted only in single-user mode:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/me` | Local admin, plan and enabled-event count |
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
seconds and checks whatever is due. The UI is served at `/` and refreshes every
15 seconds.

The UI creates events through a three-step wizard: choose a provider template,
fill in template-specific details and watch settings, then choose a notification
channel. The template catalogue lives in `frontend/index.html` and currently has
one placeholder, `Example.com`; template management will move it server-side for
higher tiers. Email is the only notification channel. A theme control switches
between Automatic, Light and Dark and stores the choice in `localStorage`.

### Email

When `NOTIFICATION_EMAIL` and `SMTP_FROM` are set, the backend sends one plain
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
single-user UI at `frontend/index.html` is not published. Deployment is
documented in [`deploy/DEPLOY.md`](deploy/DEPLOY.md).

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
