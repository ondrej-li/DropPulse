# dp.ondralinek.cz — static public site

A deliberately small public site for DropPulse: ten self-contained landing pages
(Czech default, then English, German, French, Spanish, Italian, Portuguese,
Japanese, Korean and Russian) plus the Czech and English privacy notices. nginx
serves them as files; the Go backend stays on loopback and is not part of this
domain.

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
| auto-deploy | none; run `~/DropPulse/deploy/deploy.sh` after a change |

DNS already points here:

| Type | Name | Value |
|---|---|---|
| A | dp.ondralinek.cz | `2.31.33.21` |
| AAAA | dp.ondralinek.cz | `2a01:4f8:c016:e828::1` |

## What is intentionally not here

- No `proxy_pass`, no upstream and no API location. `/api/` and `/healthz`
  return `404`; the Go server on `127.0.0.1:8090` is not reachable.
- `/app/` is not published yet. Every landing page links to it, but the
  application UI (`frontend/index.html`) needs the backend and OAuth clients, so
  the link currently returns `404`. Do not replace it with a local copy of the
  app: the app expects the API on the same origin.
- The static CSP is `script-src 'none'`. When `/app/` is proxied, give it its own
  `Content-Security-Policy` (the backend already sets the app's policy) instead
  of weakening the static pages' header.
- No auto-deploy timer yet. The public site changes rarely; the generated pages
  are reviewed in the repository before a deploy.

## One command

On the VPS, as `deploy`, the first deployment is one line:

```bash
git clone -b feat/dp-preview https://github.com/ondrej-li/DropPulse.git ~/DropPulse && ~/DropPulse/deploy/deploy.sh
```

After the preview branch is merged, clone `main` instead:

```bash
git clone https://github.com/ondrej-li/DropPulse.git ~/DropPulse && ~/DropPulse/deploy/deploy.sh
```

After that, every deployment is one command:

```bash
~/DropPulse/deploy/deploy.sh
```

The script is safe to re-run. It pulls the checkout, rsyncs `frontend/public/`
(the ten landing pages and both privacy notices) into `/var/www/dp`, preserves
`.well-known/`, and installs the correct vhost. On the first run it also checks
DNS, installs the HTTP vhost so Let's Encrypt can validate, requests the
certificate, switches to the HTTPS vhost and verifies that `/`, `/en/`,
`/privacy/` and `/privacy/en/` answer `200`, while `/api/v1/events` and `/app/`
are still `404`.
Later runs skip the certificate step. nginx is reloaded only after `nginx -t`
passes.

If certificate issuance fails, the HTTP preview stays live, the command exits
non-zero, and re-running it retries the certificate.

## Updates

There is no timer, so a merge is not a publish. After the branch is merged,
switch the deploy checkout to `main` once:

```bash
git -C ~/DropPulse switch main
```

Then deploy with `~/DropPulse/deploy/deploy.sh`. If this becomes more than a
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

Space these out: the vhost allows one request per second per IP.

```bash
curl -sSI https://dp.ondralinek.cz/ | head -12
curl -sS  https://dp.ondralinek.cz/ | grep -o '<title>[^<]*</title>'
for path in /en/ /privacy/ /privacy/en/; do
    sleep 1
    printf '%s ' "$path"
    curl -sS -o /dev/null -w '%{http_code}\n' "https://dp.ondralinek.cz$path"  # 200
done
sleep 1
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/api/v1/events  # 404
sleep 1
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/healthz       # 404
sleep 1
curl -sS -o /dev/null -w '%{http_code}\n' https://dp.ondralinek.cz/app/          # 404 for now
curl -sSI http://dp.ondralinek.cz/ | grep -i '^location'                         # -> https
```

The response should carry HSTS, `X-Content-Type-Options: nosniff`, a CSP with
no `script-src` allowance, and `Cache-Control: no-cache` on the HTML. Each page
is self-contained, so one page load is one request and no JavaScript runs.
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
