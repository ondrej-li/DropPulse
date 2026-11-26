# DropPulse

The backend is Go in `backend/`; the web app is the single file
`frontend/index.html`; `mobile/` is placeholders. CI
(`.github/workflows/ci.yml`) builds, vets and tests the backend on every pull
request. Nothing is built on the VPS.

## Serving rule for dp.ondralinek.cz

- The deployed backend binds loopback only: `BIND_ADDR=127.0.0.1`,
  `PORT=8090`. Never give the public service a wildcard bind address.
- Every public request goes through the `dp.ondralinek.cz` nginx vhost. It
  proxies exactly `/app/` and `/api/` to `http://127.0.0.1:8090`; `/healthz`
  is answered `404` by nginx and is not proxied.
- No second vhost, stream, port-forward or firewall allow rule may expose
  8090. UFW is enabled with default incoming `DROP`.
- Keep `deploy/nginx/dp.conf` in sync with the live vhost, and read
  `deploy/DEPLOY.md` before changing serving or deployment.

## Before this site goes live

This repository is served publicly at https://dp.ondralinek.cz. A change that adds or alters a
page, a form, a script, a header or a vhost passes the launch gate before it
ships:

```sh
web-launch-check https://dp.ondralinek.cz
```

- `FAIL` blocks the publish; `WARN` is a decision to state in the reply, not
  something to leave unmentioned.
- The `web-launch-checklist` skill has the 25 items the checker cannot judge:
  privacy and terms content, colour contrast, real-device speed, spam
  protection, analytics and error monitoring, one clear call to action, and the
  signup/login/reset flows.
- Run it again after publishing: it checks the live vhost, headers and
  certificate, not the working tree.
- A preview vhost passes the same gate before DNS moves.

## Deploying this site

Two halves, two accounts, both on a five-minute timer. Merging to `main` is the
deploy for both: there is no publish command, and `sudo vps-site publish` refuses
this web root (one writer per root, and it is not this account).

| What | Who | Proof it arrived |
|---|---|---|
| Static pages (`frontend/public/`) → `/var/www/dp` | the **deploy** account, `site-deploy` | `https://dp.ondralinek.cz/deployed.json` |
| The backend binary (`backend/bin/droppulse-server`) | the **agent** account, `app-deploy`, from the newest successful CI run | `app-deploy status` |

### Static pages

Verify after merging, and give it up to ten minutes:

```sh
site-status --site dp
curl -fsS https://dp.ondralinek.cz/deployed.json           # {"commit": "...", "deployed_at": "..."}
git -C ~/repos/DropPulse rev-parse origin/main
```

The three commits must match. If it has not moved after two timer cycles, read
`journalctl -u site-deploy -n 50`, report the commit that should be live and the
one that is, and do not rsync into `/var/www` or edit a live file to compensate.
The `static-site-deploy` skill has the full procedure.

### The backend binary

`frontend/index.html` is served by the Go process, so a UI change also needs the
binary deployed. That is automatic too:

```
CI (on main) uploads droppulse-server-linux-arm64
app-deploy.timer (5 min, as agent)
  └─ newest successful run → download → aarch64 ELF check
       → install over backend/bin/droppulse-server (one .prev kept)
       → sudo vps-site service restart droppulse.service
       → unit active AND http://127.0.0.1:8090/healthz 2xx, or restore .prev
```

```sh
app-deploy status              # deployed commit, CI run, binary hash, unit, health
app-deploy run --dry-run       # what it would install, without installing
journalctl -u app-deploy -n 50 # the last pass
```

- **Never** `go build` here and never copy a binary onto the box. A hand-placed
  binary is drift: `app-deploy watch` mails about the hash not matching, and the
  next pass replaces it.
- A red CI run leaves the running binary alone, so **fixing the build is the
  deploy**. Do not work around it by installing something local.
- A manual `vps-site service restart` is not a deploy, and doing it in the middle
  of a timer run races the health check. Restart only to pick up a changed
  `frontend/index.html` or environment, or when the timer is not running.

### Email notifications

Mail is enabled and submits over loopback SMTP; `sendmail` is blocked by the unit
sandbox (`NoNewPrivileges=yes`) and must stay blocked — do not weaken the unit to
make it work.

```sh
# /home/agent/.config/droppulse/backend.env, 0600, never in the repository
SMTP_FROM=alerts@ondralinek.cz   # in a DKIM-signed, SPF-aligned domain
SMTP_HOST=127.0.0.1
SMTP_PORT=25
SMTP_TLS=none                    # loopback relay: no credentials, no secrets
```

- Multi-user mode mails each account's **verified** address;
  `NOTIFICATION_EMAIL` is single-user only and the service refuses to start with
  it set here.
- The EHLO name defaults to the sender domain. Postfix has
  `reject_non_fqdn_helo_hostname`, which it applies at `RCPT TO`, so a greeting
  of `localhost` fails with `504 5.5.2` — set `SMTP_HELO` if that ever needs
  overriding.
- Report a mail problem; do not disable delivery to make a test pass.

## Writing HTML and CSS

Before writing or changing HTML, CSS or client-side JavaScript here, use the
`modern-web-guidance` skill (installed for this account): search for the
capability, retrieve the guides it names, apply them. The platform usually has
the answer the library was added for — `<dialog>` and the popover API instead of
div overlays, container queries instead of media-query rewrites, View
Transitions and `@starting-style` instead of JS entrance animations, native
validation and `:user-invalid` instead of a validation library.

Browser support policy: Baseline Widely available as-is; Newly available only
with feature detection and a fallback of 20 lines or fewer, no dependencies, no
polyfills.
