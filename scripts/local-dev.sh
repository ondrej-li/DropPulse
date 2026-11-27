#!/usr/bin/env bash
# Starts DropPulse locally on loopback (127.0.0.1) with LOCAL_TEST_AUTH=true so
# headless tools (curl, Playwright, scripts) and local browsers can skip OAuth
# and act as any role or user.
#
# Usage:
#   ./scripts/local-dev.sh                     # ephemeral in-memory store on 127.0.0.1:8080
#   PORT=18080 ./scripts/local-dev.sh          # custom loopback port
#   DATABASE_URL='postgres://localhost/droppulse?sslmode=disable' ./scripts/local-dev.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT/backend"

export APP_ENV="development"
export BIND_ADDR="127.0.0.1"
export PORT="${PORT:-8080}"
export DATABASE_URL="${DATABASE_URL:-memory://}"
export LOCAL_TEST_AUTH="true"
export FRONTEND_FILE="${FRONTEND_FILE:-../frontend/index.html}"
export PUBLIC_API_URL="http://127.0.0.1:${PORT}"
export WEB_APP_URL="http://127.0.0.1:${PORT}"

echo "Starting DropPulse local test server on http://127.0.0.1:${PORT}/ (store=${DATABASE_URL})"
echo "  - Headless headers: -H 'X-Test-User: alice' -H 'X-Test-Role: user'"
echo "  - Browser URL switch: http://127.0.0.1:${PORT}/?as=alice&role=user"
exec go run ./cmd/server
