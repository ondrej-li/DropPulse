# dp.ondralinek.cz — static preview

A deliberately small public placeholder for DropPulse. nginx serves one static
HTML file; the Go backend stays on loopback and is not part of this domain.

| | |
|---|---|
| Host | `dp.ondralinek.cz` |
| Web root | `/var/www/dp` (deploy:www-data) |
| Static source | [`frontend/public/`](../frontend/public/) |
| Source checkout | `~/DropPulse` (deploy user) |
| vhost | `/etc/nginx/sites-available/dp` → `sites-enabled/dp` |
| headers | `/etc/nginx/snippets/dp-headers.conf` (HSTS + CSP + …) |
| TLS | `/etc/letsencrypt/live/dp.ondralinek.cz/` |
| renewal | `certbot.timer`; the HTTP vhost keeps the ACME path open |
| logs | `/var/log/nginx/dp.access.log`, `dp.error.log` |
| auto-deploy | none; run `deploy/deploy.sh` after a change |

DNS already points here:

| Type | Name | Value |
|---|---|---|
| A | dp.ondralinek.cz | `2.31.33.21` |
| AAAA | dp.ondralinek.cz | `2a01:4f8:c016:e828::1` |

## What is intentionally not here

- No `proxy_pass`, no upstream and no API location. `/api/` and `/healthz`
  return `404`; the Go server on `127.0.0.1:8090` is not reachable.
- The single-user app UI (`frontend/index.html`) is not published. It expects a
  loopback API and says so. `frontend/public/index.html` is the public page.
- No auto-deploy timer yet. The content is one file and changes rarely.

## First deploy

On the VPS, as `deploy`:

```bash
git clone https://github.com/ondrej-li/DropPulse.git ~/DropPulse
cd ~/DropPulse
git switch feat/dp-preview       # until the preview branch is merged to main
./deploy/deploy.sh               # HTTP vhost + static files
./deploy/enable-tls.sh           # certificate + HTTPS vhost
```

`deploy.sh` is safe to re-run. It pulls the branch, rsyncs the static source
into `/var/www/dp`, preserves `.well-known/`, installs the HTTP or HTTPS vhost
depending on whether the certificate exists, and reloads nginx only after
`nginx -t` passes.

## Updates

After the preview branch is merged, the deploy checkout should be on `main`:

```bash
cd ~/DropPulse
git switch main
./deploy/deploy.sh
```

There is no timer, so a merge is not a publish. If this becomes more than a
placeholder, add a systemd timer following the other static sites.

## Request policy

The vhost is stricter than the shared edge:

| Limit | Value |
|---|---|
| `limit_req` | 1 request/second per IP, burst 5, `nodelay` |
| `limit_conn` | 3 concurrent streams per IP |
| methods | `GET HEAD OPTIONS`; everything else `405` |
| scanner paths | dotfiles, executable extensions and CMS probes `444` |
| body | `1m` global limit; the static handler never reads it |

A page load is one request because the HTML is self-contained. Do not run a
rapid loop against the domain to test the limiter: 429s are logged at error
level and fail2ban's `nginx-limit-req` jail watches the nginx error logs
(60 violations in ten minutes is an hour's ban). Once the burst is spent, the
next request in the same second returns `429`.

## Verification

```bash
curl -sSI https://dp.ondralinek.cz/ | head -12
curl -sS  https://dp.ondralinek.cz/ | grep -o '<title>[^<]*</title>'
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/api/v1/events  # 404
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/healthz       # 404
curl -sSI http://dp.ondralinek.cz/ | grep -i '^location'                         # -> https
```

The response should carry HSTS, `X-Content-Type-Options: nosniff`, a CSP with
no `script-src` allowance, and `Cache-Control: no-cache` on the HTML.
`ss -ltn 'sport = :8090'` should still show only `127.0.0.1:8090` for the Go
backend.

## Rollback

Take the name out of service without touching the other sites:

```bash
sudo rm /etc/nginx/sites-enabled/dp
sudo nginx -t
sudo systemctl reload nginx
```

The static files in `/var/www/dp` and the certificate are left in place. To
publish again, re-run `deploy/deploy.sh`.
