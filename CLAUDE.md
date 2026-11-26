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

https://dp.ondralinek.cz is published by the **deploy** account from this repository's
`main` branch — the `site-deploy` timer, every five minutes. Merge is
deploy: there is no publish command, and `sudo vps-site publish` refuses this
web root (one writer per root, and it is not this account).

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
