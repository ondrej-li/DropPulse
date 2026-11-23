#!/usr/bin/env bash
# One-command deploy for the dp.ondralinek.cz static public site.
#
#   ~/DropPulse/deploy/deploy.sh
#
# Run as the deploy user on the VPS. It pulls, publishes frontend/public/ (the
# ten landing pages and both privacy notices), installs the static-only vhost,
# obtains the TLS certificate on the first run, switches to HTTPS and verifies
# the result. The Go backend is not involved and is never proxied here.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
SITE_DIR="$REPO_DIR/frontend/public"
WEB_ROOT="${WEB_ROOT:-/var/www/dp}"
DOMAIN="dp.ondralinek.cz"
PUBLIC_IP="2.31.33.21"
PUBLIC_IP6="2a01:4f8:c016:e828::1"

umask 022

cd "$REPO_DIR"

echo "==> fetching"
before="$(git rev-parse HEAD)"
git pull --ff-only
after="$(git rev-parse HEAD)"
# Bash reads this file incrementally, so never keep running a script that the
# pull just replaced: restart it once with the fresh copy.
if [ "$before" != "$after" ] && [ "${DP_DEPLOY_REEXEC:-0}" != "1" ]; then
    DP_DEPLOY_REEXEC=1 exec "$REPO_DIR/deploy/deploy.sh" "$@"
fi

[ -d "$SITE_DIR" ] || {
    echo "ERROR: $SITE_DIR is missing; is this the DropPulse checkout?" >&2
    exit 1
}

echo "==> publishing $SITE_DIR to $WEB_ROOT"
sudo install -d -o deploy -g www-data -m 0755 "$WEB_ROOT"
# Keep the ACME challenge directory: certbot renewals write into it. Set modes
# and ownership during the copy instead of recursively chowning the web root.
sudo rsync -a --delete \
    --chown=deploy:www-data \
    --chmod=D755,F644 \
    --exclude ".well-known/" \
    "$SITE_DIR"/ "$WEB_ROOT"/

if [ "${SKIP_NGINX:-0}" = "1" ]; then
    echo "==> nginx skipped (SKIP_NGINX=1)"
    exit 0
fi

check_record() {
    local type="$1" name="$2" value=""
    for resolver in 1.1.1.1 8.8.8.8; do
        value="$(dig +short +time=3 +tries=1 "$type" "$name" "@$resolver" | grep -E '^[0-9a-fA-F:.]+$' | head -1 || true)"
        [ -n "$value" ] && break
    done
    printf '%s' "$value"
}

echo "==> nginx configuration"
sudo install -m 0644 -o root -g root "$REPO_DIR/deploy/nginx/dp-headers.conf" /etc/nginx/snippets/dp-headers.conf

if ! sudo test -f "/etc/letsencrypt/live/$DOMAIN/fullchain.pem"; then
    echo "==> first run: checking DNS for the certificate"
    command -v dig >/dev/null || {
        echo "ERROR: dig is required to check the DNS records." >&2
        exit 1
    }
    v4="$(check_record A "$DOMAIN")"
    v6="$(check_record AAAA "$DOMAIN")"
    echo "    A    $DOMAIN -> ${v4:-nothing} (expected $PUBLIC_IP)"
    echo "    AAAA $DOMAIN -> ${v6:-nothing} (expected $PUBLIC_IP6, or none)"

    if [ "$v4" != "$PUBLIC_IP" ]; then
        cat >&2 <<EOM
ERROR: $DOMAIN does not resolve to this server.
       Point the A record at $PUBLIC_IP and run this command again.
EOM
        exit 1
    fi

    if [ -n "$v6" ] && [ "$v6" != "$PUBLIC_IP6" ]; then
        cat >&2 <<EOM
ERROR: $DOMAIN AAAA is $v6, but this server is $PUBLIC_IP6.
       Let's Encrypt prefers AAAA, so validation would go to the wrong host.
       Change the AAAA record to $PUBLIC_IP6 (or remove it) and run this command again.
EOM
        exit 1
    fi

    echo "==> installing the HTTP vhost for ACME"
    sudo install -m 0644 -o root -g root "$REPO_DIR/deploy/nginx/dp-http.conf" /etc/nginx/sites-available/dp
    sudo ln -sfn /etc/nginx/sites-available/dp /etc/nginx/sites-enabled/dp
    sudo nginx -t
    sudo systemctl reload nginx

    echo "==> requesting certificate for $DOMAIN"
    if ! sudo certbot certonly --webroot -w "$WEB_ROOT" \
        -d "$DOMAIN" --cert-name "$DOMAIN" \
        --non-interactive --agree-tos \
        --deploy-hook "systemctl reload nginx"; then
        cat >&2 <<EOM
ERROR: the certificate request failed. The HTTP preview is live; fix the cause
       and run this command again.
EOM
        exit 1
    fi
fi

echo "==> switching to the HTTPS vhost"
sudo install -m 0644 -o root -g root "$REPO_DIR/deploy/nginx/dp.conf" /etc/nginx/sites-available/dp
sudo ln -sfn /etc/nginx/sites-available/dp /etc/nginx/sites-enabled/dp
sudo nginx -t
sudo systemctl reload nginx

echo "==> verifying"
# nginx picks the new config up a moment after `systemctl reload`, so give the
# reload a few seconds before failing.
code=""
for _ in 1 2 3 4 5; do
    code="$(curl -sS -o /dev/null -w '%{http_code}' --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/" 2>/dev/null || true)"
    [ "$code" = "200" ] && break
    sleep 1
done
echo "    https://$DOMAIN/ -> ${code:-no response}"
if [ "$code" != "200" ]; then
    echo "ERROR: HTTPS did not come up; the certificate is installed but the vhost is not healthy." >&2
    exit 1
fi

redirect="$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' --resolve "$DOMAIN:80:127.0.0.1" "http://$DOMAIN/")"
echo "    http://$DOMAIN/ -> $redirect"

# The vhost allows one request per second per IP. Space the page checks so a
# successful deploy does not trip its own limiter.
for path in /privacy/ /en/ /privacy/en/; do
    sleep 1
    page_code="$(curl -sS -o /dev/null -w '%{http_code}' --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN$path" || true)"
    echo "    https://$DOMAIN$path -> ${page_code:-no response}"
    if [ "$page_code" != "200" ]; then
        echo "ERROR: $path did not return 200; the public site is incomplete." >&2
        exit 1
    fi
done

sleep 1
api_code="$(curl -sS -o /dev/null -w '%{http_code}' --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/api/v1/events")"
if [ "$api_code" != "404" ]; then
    echo "ERROR: /api/v1/events returned $api_code; the Go backend must not be reachable here." >&2
    exit 1
fi
echo "    /api/v1/events -> 404 (the Go backend stays loopback-only)"

# /app/ is the sign-in link on every landing page. It becomes live only when the
# backend is proxied and has OAuth credentials; until then it must not silently
# serve something else. Update this check with that deployment.
sleep 1
app_code="$(curl -sS -o /dev/null -w '%{http_code}' --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/app/")"
if [ "$app_code" != "404" ]; then
    echo "ERROR: /app/ returned $app_code; the application is not deployed yet." >&2
    exit 1
fi
echo "    /app/ -> 404 (the application backend is not deployed yet)"

echo "==> done: https://$DOMAIN/"
