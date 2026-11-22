# DropPulse

DropPulse is organised as three elements in one repository:

| Element | Directory | Stack |
|---|---|---|
| Backend | [`backend/`](backend/) | Go, net/http and Postgres (pgx) |
| Frontend | [`frontend/`](frontend/) | Plain HTML and CSS |
| Mobile app | [`mobile/`](mobile/) | iOS and Android |

The backend owns the API. The frontend and both mobile apps are clients of it.

## Running locally

The backend needs a Postgres database. It applies its embedded migrations on
start-up, so an empty database is enough:

```sh
createdb droppulse
cd backend
DATABASE_URL='postgres://localhost/droppulse?sslmode=disable' go run ./cmd/server
```

It listens on `:8080` unless `PORT` is set. `GET /healthz` answers with
`{"status":"ok"}`. All product routes require a session; the only public routes
are `/healthz` and the OAuth start/callback endpoints.

Configuration is read from the environment and validated at start-up:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Required Postgres connection string |
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

`PUBLIC_API_URL` and `WEB_APP_URL` must use HTTPS unless they point at
localhost. Registered OAuth callback URLs must match exactly.

The current slice implements accounts and authentication: OAuth login, sessions,
`GET /api/v1/me`, session listing and revocation, and plan entitlements. The
ticket/event/watch API, scheduler, plugins and notifications are not implemented
yet. Provenance, design and the phased plan are in
[`backend/docs/`](backend/docs/).

The frontend is static — open `frontend/index.html` in a browser, or serve the
directory:

```sh
cd frontend
python3 -m http.server 8000
```

The mobile apps are placeholders; see [`mobile/ios/`](mobile/ios/) and
[`mobile/android/`](mobile/android/).

## CI

`.github/workflows/ci.yml` builds, vets and tests the backend on every pull
request. Nothing is built on the VPS.
