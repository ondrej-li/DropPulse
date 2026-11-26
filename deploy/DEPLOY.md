# dp.ondralinek.cz — public pages, app and backend

The domain serves three things from one vhost:

| Path | Served by | Notes |
|---|---|---|
| `/`, `/en/`, `/de/`, `/fr/`, `/es/`, `/it/`, `/pt/`, `/ja/`, `/ko/`, `/ru/` | nginx from `/var/www/dp` | Self-contained landing pages; no JavaScript |
| `/privacy/`, `/privacy/en/` | nginx from `/var/www/dp` | Czech authoritative notice and English translation |
| `/terms/`, `/terms/en/` | nginx from `/var/www/dp` | Czech authoritative terms and English translation |
| `/app/` | Go backend through `proxy_pass http://127.0.0.1:8090` | The single-page application |
| `/api/` | Go backend through the same proxy | JSON API, auth callbacks, CSRF-protected writes |
| `/healthz` | nginx `return 404` | Health stays loopback-only |

The static pages are files. Only `/app/` (an exact location) and `/api/` (a
prefix) are proxied; there is no generic proxy and no user-controlled upstream.

## Components

| | |
|---|---|
| Web root | `/var/www/dp`, adopted by `vps-site` (files `agent:www-data`) |
| Static source | [`frontend/public/`](../frontend/public/) |
| Vhost | `/etc/nginx/sites-available/dp` → `sites-enabled/dp`, managed by `vps-site` |
| Headers | `/etc/nginx/snippets/dp-headers.conf` (static HSTS/CSP/…) |
| TLS | `/etc/letsencrypt/live/dp.ondralinek.cz/`; `certbot.timer` renews via the HTTP vhost |
| Backend service | `droppulse.service`, runs as `agent`, binds `127.0.0.1:8090` |
| Backend binary | `~/repos/DropPulse/backend/bin/droppulse-server` (CI-built, linux/arm64) |
| Environment | `/home/agent/.config/droppulse/backend.env`, mode `0600`, outside the repo |
| Database | PostgreSQL `droppulse`, owned by `agent`, socket peer auth |
| Logs | `/var/log/nginx/dp.access.log`, `dp.error.log`; backend JSON in journald |
| Auto-deploy | none |

## Request policy

| Path | Methods | Rate limit | CSP |
|---|---|---|---|
| Static pages | `GET HEAD OPTIONS`; anything else `405` | 1 r/s per IP, burst 5, 3 concurrent | `script-src 'none'`, no network |
| `/app/`, `/api/` | Backend routes; auth, CSRF and Origin checks in the app | 10 r/s per IP, burst 20, 10 concurrent | App CSP from the backend; API has no CSP (JSON) |

The static method policy is deliberately scoped to the static locations. It
used to sit at server level; an `if` there cannot be undone inside a location,
so the API could not have accepted `POST` or `DELETE`. The static pages still
have the same rule — it simply does not cover the proxied locations any more.
The app/API zone is separate from the 1 r/s static zone; the backend also has
its own per-IP auth and API limits.

On the proxied locations nginx adds HSTS, COOP, CORP and
`X-Permitted-Cross-Domain-Policies`. It does **not** add the static CSP: the
backend sets the app's CSP (`script-src 'unsafe-inline'`, `connect-src 'self'`)
and the static `script-src 'none'` would break it.

## Deploying a backend change

The VPS is arm64 and has no Go 1.27 toolchain. CI builds the binary and uploads
it as the `droppulse-server-linux-arm64` artifact (retained 7 days), and
`app-deploy` — the `agent` account's timer — installs it:

```
app-deploy.timer (5 min)
  └─ newest successful CI run on main → download → aarch64 ELF check
       → install over backend/bin/droppulse-server (one .prev kept)
       → sudo vps-site service restart droppulse.service
       → unit active AND loopback /healthz 2xx within 30 s
            └─ otherwise restore .prev, restart again, mail the owner
```

So the deploy is **merging to `main`**; nothing is copied by hand.

```bash
app-deploy status             # deployed commit, CI run, binary hash, unit, health
app-deploy run --dry-run      # what the next pass would install
journalctl -u app-deploy -n 50
```

A red CI run leaves the running binary alone. A hand-placed binary is drift:
`app-deploy watch` mails when the installed hash stops matching the recorded
one. Use the manual steps below only to bootstrap a fresh box, or to debug why
the timer cannot do it.

### Bootstrap a fresh box

```bash
cd ~/repos/DropPulse

# 1. Database, once.
sudo vps-site db create droppulse

# 2. Environment, once; edit the file, never commit it.
install -d -m 0700 ~/.config/droppulse
$EDITOR ~/.config/droppulse/backend.env
chmod 0600 ~/.config/droppulse/backend.env

# 3. Service and vhost. Both commands validate first and roll back on failure.
sudo vps-site service install droppulse.service deploy/droppulse.service
sudo vps-site vhost check deploy/nginx/dp.conf
sudo vps-site vhost install dp deploy/nginx/dp.conf

# 4. Static pages.
sudo vps-site publish dp frontend/public

# 5. The binary, from the CI run you want.
gh run list --branch main --limit 5
gh run download <run-id> -n droppulse-server-linux-arm64 -D backend/bin
chmod 0755 backend/bin/droppulse-server
file backend/bin/droppulse-server        # ELF 64-bit, ARM aarch64

# 6. Restart; a fresh box has no timer to do it.
sudo vps-site service restart droppulse.service
```

On a box with `app-deploy` installed (vps repo, `apps/`), step 5 is what the
timer does on its own and step 6 only matters for a changed environment or
`frontend/index.html`.

`vps-site vhost install dp` adopts the pre-existing vhost; the old file is
backed up under `/var/backups/vps-site/` first. `publish` adopts the
pre-existing deploy-owned web root the same way. From then on the site belongs
to the `agent` account's helper.

The environment file must contain at least:

```sh
APP_ENV=production
BIND_ADDR=127.0.0.1
PORT=8090
PUBLIC_API_URL=https://dp.ondralinek.cz
WEB_APP_URL=https://dp.ondralinek.cz
FRONTEND_FILE=/home/agent/repos/DropPulse/frontend/index.html
DATABASE_URL="postgres:///droppulse?host=/var/run/postgresql&sslmode=disable"
ALLOWED_EMAILS=<the controller's verified address>
ADMIN_EMAILS=<the controller's verified address>
```

Provider credentials are additional; see below. Never put them in the
repository, a commit, a PR body, an issue or a log.

### Email notifications

`droppulse.service` runs with `NoNewPrivileges=yes` and `ProtectSystem=strict`, which blocks Postfix's setgid `postdrop` helper (`sendmail`). Do not weaken the sandbox to make `sendmail` work.

Instead the backend submits over SMTP. On this box the mail it needs is one hop, to the Postfix instance listening on loopback, so there is nothing to authenticate and no secret to keep:

```sh
# /home/agent/.config/droppulse/backend.env, mode 0600, never committed
SMTP_FROM=alerts@ondralinek.cz   # DKIM-signed and SPF-aligned: spf1 mx -all
SMTP_HOST=127.0.0.1
SMTP_PORT=25
SMTP_TLS=none                    # allowed for a loopback host with no credentials
```

`alerts@ondralinek.cz` is a virtual alias to `admin@` (`/etc/postfix/virtual`), so
bounces still land in a mailbox. Multi-user mode mails each account's resolved
address; `NOTIFICATION_EMAIL` is single-user only and the service refuses to
start here with it set.

An external relay works too, when that is what a deployment has:

```sh
SMTP_FROM=alerts@dp.ondralinek.cz
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=<smtp-user>
SMTP_PASSWORD_FILE=/home/agent/.config/droppulse/smtp.pass   # mode 0600
SMTP_TLS=auto   # auto | starttls | implicit (port 465) | none (loopback unauth only)
```

`SMTP_HELO` is optional: the backend greets the server with the domain of
`SMTP_FROM`. Postfix's `reject_non_fqdn_helo_hostname` refuses Go's default
`EHLO localhost` at `RCPT TO` — with `504 5.5.2` — so the greeting name matters
even on loopback. Set `SMTP_HELO` only to override it.

Credentials are never sent without TLS (`SMTP_TLS=none` is rejected for non-loopback hosts or when `SMTP_USERNAME` is set). When `SMTP_FROM` is unset, the backend logs `email notifications are disabled` and the app reports them as unavailable. Sign-in and the API are unaffected.

The frontend is read at service start. After a `git pull` that changes
`frontend/index.html`, restart `droppulse.service`.

## Deploying a static-page change

From the `agent` account:

```bash
cd ~/repos/DropPulse
python3 frontend/landing/build.py --check     # committed pages are current
sudo vps-site publish dp frontend/public
```

The old `deploy/deploy.sh` path (as the `deploy` user) still installs the same
vhost and content from this repository. It is kept as a fallback, but prefer
`vps-site`: the helper validates, records and rolls back. Both paths use
`deploy/nginx/dp.conf`; do not update one without the other.

## Owner step: make sign-in work

The backend, database, service, vhost and static pages are deployed. With no
provider credentials the app loads and `/api/v1/auth/providers` returns
`{"providers":[]}`; the app then says no sign-in provider is configured. Sign-in
starts working when the owner registers the OAuth clients and adds their
credentials to the environment.

Registration details and exact callback URLs are in
[`backend/docs/privacy.md`](../backend/docs/privacy.md#provider-registration-checklist).
The short version:

1. **Google** — OAuth consent screen, then a Web application client with
   redirect URI `https://dp.ondralinek.cz/api/v1/auth/google/callback`. Add
   `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` to the env file.
2. **Apple** — App ID with Sign in with Apple, a Services ID for the web, the
   domain `dp.ondralinek.cz` verified, return URL
   `https://dp.ondralinek.cz/api/v1/auth/apple/callback`, scopes name/email,
   and a `.p8` key. Add `APPLE_CLIENT_ID` (the Services ID), `APPLE_TEAM_ID`,
   `APPLE_KEY_ID` and `APPLE_PRIVATE_KEY_FILE` (a mode-0600 file in
   `~/.config/droppulse/`) to the env file.

   Apple's domain-association file has to be served at
   `https://dp.ondralinek.cz/.well-known/apple-developer-domain-association.txt`.
   `vps-site publish` excludes `.well-known/` because certbot owns that
   directory, so the file cannot be published from `frontend/public/`. Place it
   directly in the web root as root:

   ```bash
   sudo install -d -o root -g www-data -m 0755 /var/www/dp/.well-known
   sudo install -o root -g www-data -m 0644 <downloaded-file> \
       /var/www/dp/.well-known/apple-developer-domain-association.txt
   ```

   The file is public and survives later static publishes. The `agent` account
   cannot write there; this is an owner/root step.
3. Restart and verify:

   ```bash
   sudo vps-site service restart droppulse.service
   curl -sS https://dp.ondralinek.cz/api/v1/auth/providers   # ["google","apple"]
   ```

4. Open `https://dp.ondralinek.cz/app/` and sign in with the allowlisted
   verified address. Any other verified address is refused before an account
   row is created and returns to `/app/?auth_error=not_invited`.

The allowlist is `ALLOWED_EMAILS`; add an address by editing the env file and
restarting. There is no registration form and no admin UI. `APP_ENV=production`
refuses to start with an empty list.

## Verification

```bash
curl -sSI https://dp.ondralinek.cz/ | head -20          # HSTS, static CSP
curl -sS  https://dp.ondralinek.cz/ | grep -o '<title>[^<]*</title>'
for path in /en/ /de/ /privacy/ /privacy/en/ /terms/ /terms/en/; do
    sleep 1
    printf '%s ' "$path"
    curl -sS -o /dev/null -w '%{http_code}\n' "https://dp.ondralinek.cz$path"
done
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/app/            # 200
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/api/v1/events   # 401
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/healthz         # 404
curl -sS -o /dev/null -w '%{http_code}\n' -X POST https://dp.ondralinek.cz/        # 405
curl -sSI https://dp.ondralinek.cz/app/ | grep -i 'content-security-policy'        # app CSP
```

`ss -ltn` should show `127.0.0.1:8090` only, not a public listener. Backend
logs: `sudo vps-site service logs droppulse.service`.

Do not run a rapid loop against the static pages: 429s are logged at error
level and fail2ban's `nginx-limit-req` jail watches the nginx error logs.

## Rolling back

To stop the application while keeping the static pages, install a static-only
vhost from git history and reload:

```bash
cd ~/repos/DropPulse
git show 708d2a3:deploy/nginx/dp.conf > ~/dp-static.conf
sudo vps-site vhost check ~/dp-static.conf
sudo vps-site vhost install dp ~/dp-static.conf
sudo vps-site service stop droppulse.service
```

`708d2a3` is the last commit before the app was proxied; any later commit that
still has a static-only `dp.conf` works. The static files, certificate and
database are left in place. To remove the service as well:

```bash
sudo vps-site service remove droppulse.service
```

Do not drop the database unless its data is expendable:
`sudo vps-site db drop droppulse` is destructive.
