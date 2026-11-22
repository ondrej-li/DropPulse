#!/usr/bin/env bash
# One-off cutover step: request the Let's Encrypt certificate for
# dp.ondralinek.cz and switch the vhost to HTTPS. Run it only after
# deploy/deploy.sh has installed the HTTP vhost and DNS points at this server.
#
#   ./deploy/enable-tls.sh
#
# Renewals afterwards are handled by certbot.timer; the global deploy hook
# reloads nginx.
set -euo pipefail

DOMAIN="dp.ondralinek.cz"
REPO_DIR="${REPO_DIR:-$HOME/DropPulse}"
WEB_ROOT="${WEB_ROOT:-/var/www/dp}"
PUBLIC_IP="2.31.33.21"
PUBLIC_IP6="2a01:4f8:c016:e828::1"

cd "$REPO_DIR"

check_record() {
    local type="$1" name="$2" value=""
    for resolver in 1.1.1.1 8.8.8.8; do
        value="$(dig +short +time=3 +tries=1 "$type" "$name" "@$resolver" | grep -E '^[0-9a-fA-F:.]+$' | head -1)"
        [ -n "$value" ] && break
    done
    printf '%s' "$value"
}

echo "==> checking DNS"
# Ask public resolvers: the local stub resolver may still hold an old answer
# while Let's Encrypt validates against public DNS. IPv6 matters: Let's Encrypt
# prefers AAAA, so a stale AAAA makes webroot validation fail even when A is
# correct.
v4="$(check_record A "$DOMAIN")"
v6="$(check_record AAAA "$DOMAIN")"
echo "    A    $DOMAIN -> ${v4:-nothing} (expected $PUBLIC_IP)"
echo "    AAAA $DOMAIN -> ${v6:-nothing} (expected $PUBLIC_IP6, or none)"

if [ "$v4" != "$PUBLIC_IP" ]; then
    cat >&2 <<EOM
ERROR: $DOMAIN does not resolve to this server.
       Point the A record at $PUBLIC_IP and retry.
EOM
    exit 1
fi

if [ -n "$v6" ] && [ "$v6" != "$PUBLIC_IP6" ]; then
    cat >&2 <<EOM
ERROR: $DOMAIN AAAA is $v6, but this server is $PUBLIC_IP6.
       Let's Encrypt prefers AAAA, so validation would go to the wrong host.
       Change the AAAA record to $PUBLIC_IP6 (or remove it) and retry.
EOM
    exit 1
fi

if ! sudo test -L /etc/nginx/sites-enabled/dp; then
    echo "ERROR: the dp vhost is not enabled; run ./deploy/deploy.sh first." >&2
    exit 1
fi

echo "==> requesting certificate for $DOMAIN"
# Webroot mode: certbot drops the challenge into the site root, which the HTTP
# vhost already serves under /.well-known/acme-challenge/. No config rewriting,
# so the versioned vhost in this repo stays the single source of truth.
sudo certbot certonly --webroot -w "$WEB_ROOT" \
    -d "$DOMAIN" --cert-name "$DOMAIN" \
    --non-interactive --agree-tos \
    --deploy-hook "systemctl reload nginx"

echo "==> switching vhost to HTTPS"
sudo install -m 0644 -o root -g root deploy/nginx/dp-headers.conf /etc/nginx/snippets/dp-headers.conf
sudo install -m 0644 -o root -g root deploy/nginx/dp.conf /etc/nginx/sites-available/dp
sudo ln -sfn /etc/nginx/sites-available/dp /etc/nginx/sites-enabled/dp
sudo nginx -t
sudo systemctl reload nginx

echo "==> verifying"
# nginx picks the new config up a moment after `systemctl reload`, so give the
# reload a few seconds before failing the cutover.
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
