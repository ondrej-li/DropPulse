#!/usr/bin/env bash
# Publish the static preview to /var/www/dp and install its nginx vhost.
#
#   ./deploy/deploy.sh              # git pull + publish + nginx config
#   SKIP_NGINX=1 ./deploy/deploy.sh # content only
#
# Runs on the VPS as the deploy user (member of sudo). The Go backend is not
# involved: this script only copies frontend/public/ and installs nginx files.
set -euo pipefail

REPO_DIR="${REPO_DIR:-$HOME/DropPulse}"
SITE_DIR="$REPO_DIR/frontend/public"
WEB_ROOT="${WEB_ROOT:-/var/www/dp}"
DOMAIN="dp.ondralinek.cz"

umask 022

cd "$REPO_DIR"

echo "==> fetching"
before="$(git rev-parse HEAD)"
git pull --ff-only
after="$(git rev-parse HEAD)"
# Bash reads this file incrementally, so never keep running a script that the
# pull just replaced: restart it once with the fresh copy.
if [ "$before" != "$after" ] && [ "${DP_DEPLOY_REEXEC:-0}" != "1" ]; then
    DP_DEPLOY_REEXEC=1 exec "$0" "$@"
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

echo "==> nginx configuration"
sudo install -m 0644 -o root -g root deploy/nginx/dp-headers.conf /etc/nginx/snippets/dp-headers.conf

if sudo test -f /etc/letsencrypt/live/$DOMAIN/fullchain.pem; then
    sudo install -m 0644 -o root -g root deploy/nginx/dp.conf /etc/nginx/sites-available/dp
else
    sudo install -m 0644 -o root -g root deploy/nginx/dp-http.conf /etc/nginx/sites-available/dp
fi

sudo ln -sfn /etc/nginx/sites-available/dp /etc/nginx/sites-enabled/dp
sudo nginx -t
sudo systemctl reload nginx

echo "==> done: static preview at https://$DOMAIN/ (or http:// until TLS is enabled)"
