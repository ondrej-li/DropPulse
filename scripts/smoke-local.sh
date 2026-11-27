#!/usr/bin/env bash
# End-to-end headless smoke test against a local loopback DropPulse instance
# running with LOCAL_TEST_AUTH=true and DATABASE_URL=memory://.
# Verifies multi-role/multi-user impersonation, user isolation, admin controls,
# and both provider kinds (Kind 1 Cinema City seats + schedule, Kind 2 CineStar schedule-only).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-18095}"
BASE="http://127.0.0.1:${PORT}"
COOKIE_JAR="$(mktemp)"
SERVER_LOG="$(mktemp)"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f "$COOKIE_JAR" "$SERVER_LOG"
}
trap cleanup EXIT

(
  cd "$REPO_ROOT/backend"
  APP_ENV=development \
  BIND_ADDR=127.0.0.1 \
  PORT="$PORT" \
  DATABASE_URL="memory://" \
  LOCAL_TEST_AUTH=true \
  FRONTEND_FILE=../frontend/index.html \
  PUBLIC_API_URL="$BASE" \
  WEB_APP_URL="$BASE" \
  go run ./cmd/server >"$SERVER_LOG" 2>&1
) &
SERVER_PID=$!

for _ in $(seq 1 40); do
  if curl -fsS "${BASE}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
curl -fsS "${BASE}/healthz" | grep -q '"ok"'
curl -fsS "${BASE}/app/" | python3 -c '
import re, sys
html = sys.stdin.read()
m = re.search(r"<a\b[^>]*id=\"brand-home\"[^>]*href=\"/\"[^>]*>(.*?)</a>", html, re.S)
assert m and "brand-mark" in m.group(1) and "<h1>DropPulse</h1>" in m.group(1), "brand-home link missing icon or DropPulse text"
'

echo "[1/6] Verify default admin persona & seeded local accounts..."
curl -fsS "${BASE}/api/v1/me" | python3 -c '
import json, sys
d = json.load(sys.stdin)
assert d["local_test_auth"] is True, d
assert d["user"]["role"] == "admin", d
'

echo "[2/6] Verify non-admin roles (user, vendor, agent, ai) and RBAC enforcement..."
for role in user vendor agent ai; do
  code=$(curl -sS -o /dev/null -w "%{http_code}" \
    -H "X-Test-User: actor-${role}" \
    -H "X-Test-Role: ${role}" \
    "${BASE}/api/v1/admin/overview")
  [[ "$code" == "403" ]] || { echo "Expected 403 for role=${role}, got ${code}"; exit 1; }
done

echo "[3/6] Create Kind-1 (Cinema City seats & schedule) and Kind-2 (CineStar schedule) watches as user alice..."
CC_SEATS_ID=$(curl -fsS -X POST "${BASE}/api/v1/events" \
  -H "X-Test-User: alice" -H "X-Test-Role: user" \
  -H "Content-Type: application/json" \
  -d '{"name":"Flora Dune 3 Seats","url":"https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX&date=2026-12-21"}' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["check_kind"]=="seats", d; print(d["id"])')

CC_SCHED_ID=$(curl -fsS -X POST "${BASE}/api/v1/events" \
  -H "X-Test-User: alice" -H "X-Test-Role: user" \
  -H "Content-Type: application/json" \
  -d '{"name":"Flora Dune 3 Schedule","url":"https://www.cinemacity.cz/films/duna-cast-treti/8105s2r?cinema=1052&auditorium=IMAX&date=2026-12-21&mode=schedule"}' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["check_kind"]=="schedule", d; print(d["id"])')

CS_SCHED_ID=$(curl -fsS -X POST "${BASE}/api/v1/events" \
  -H "X-Test-User: alice" -H "X-Test-Role: user" \
  -H "Content-Type: application/json" \
  -d '{"name":"CineStar Andel Dune 3","url":"https://cinestar.cz/cz/praha5/filmy/movie/10100-duna-cast-treti?date=2026-12-21&tag=GOLD+CLASS"}' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["check_kind"]=="schedule", d; print(d["id"])')

# CineStar must reject mode=seats (schedule-only provider).
bad_code=$(curl -sS -o /dev/null -w "%{http_code}" -X POST "${BASE}/api/v1/events" \
  -H "X-Test-User: alice" -H "X-Test-Role: user" \
  -H "Content-Type: application/json" \
  -d '{"name":"Bad CineStar Seats","url":"https://cinestar.cz/cz/praha5/filmy/movie/10100-duna?mode=seats"}')
[[ "$bad_code" == "400" ]] || { echo "Expected 400 for CineStar mode=seats, got ${bad_code}"; exit 1; }

echo "[4/6] Verify user isolation (bob sees 0 of alice's 3 watches; admin sees all 3)..."
curl -fsS -H "X-Test-User: bob" -H "X-Test-Role: user" "${BASE}/api/v1/events" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert len(d["events"])==0, d'
curl -fsS "${BASE}/api/v1/admin/watchers" \
  | python3 -c "import json,sys; ids={w['id'] for w in json.load(sys.stdin)['watchers']}; assert {'$CC_SEATS_ID','$CC_SCHED_ID','$CS_SCHED_ID'}.issubset(ids), ids"

echo "[5/6] Verify cookie-based impersonation (/api/v1/dev/impersonate) for headless browsers..."
BOB_ID=$(curl -fsS -c "$COOKIE_JAR" -X POST "${BASE}/api/v1/dev/impersonate" \
  -H "Content-Type: application/json" \
  -d '{"user":"bob","role":"vendor"}' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["user"]["role"]=="vendor", d; print(d["user"]["id"])')

curl -fsS -b "$COOKIE_JAR" "${BASE}/api/v1/me" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["user"]["username"]=="bob" and d["user"]["role"]=="vendor", d'

echo "[6/6] Verify admin user ban & admin self-protection..."
ADMIN_ID=$(curl -fsS "${BASE}/api/v1/me" | python3 -c 'import json,sys; print(json.load(sys.stdin)["user"]["id"])')
protect_code=$(curl -sS -o /dev/null -w "%{http_code}" -X POST "${BASE}/api/v1/admin/users/${ADMIN_ID}/ban")
[[ "$protect_code" == "403" ]] || { echo "Expected 403 when banning admin, got ${protect_code}"; exit 1; }

curl -fsS -X POST "${BASE}/api/v1/admin/users/${BOB_ID}/ban" >/dev/null
banned_code=$(curl -sS -o /dev/null -w "%{http_code}" -b "$COOKIE_JAR" "${BASE}/api/v1/me")
[[ "$banned_code" == "403" ]] || { echo "Expected 403 for banned bob, got ${banned_code}"; exit 1; }

echo "All local headless smoke checks passed."
