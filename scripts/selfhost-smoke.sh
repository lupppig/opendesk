#!/usr/bin/env bash
#
# Brings the root docker-compose.yml up for real and checks it from outside,
# through Caddy, the way a browser would:
#
#   * /healthz and /readyz answer 200 (the database and NATS are connected)
#   * the web index, /config.js and /widget/v1.js are served by opendesk
#   * /socket/websocket upgrades to a WebSocket with a valid `ws` token in
#     Sec-WebSocket-Protocol, and refuses one in the URL or none
#   * an event published on NATS reaches a joined workspace channel
#
#   scripts/selfhost-smoke.sh            build this checkout, then test
#   scripts/selfhost-smoke.sh --no-build test images already tagged OPENDESK_VERSION
#
# It uses its own compose project, env file and ports, so it never touches a
# real install's .env or volumes, and tears everything down at the end.
set -euo pipefail

cd "$(dirname "$0")/.."
BUILD=--build
[[ ${1:-} == --no-build ]] && BUILD=--no-build

PROJECT=opendesk-smoke
HTTP_PORT=${SMOKE_HTTP_PORT:-18080}
HTTPS_PORT=${SMOKE_HTTPS_PORT:-18443}
BASE="https://localhost:${HTTPS_PORT}"
work=$(mktemp -d)

fail() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; dc logs --tail=80 >&2 || true; exit 1; }
pass() { printf '\033[32m✓\033[0m %s\n' "$*"; }
dc() { docker compose -p "$PROJECT" --env-file "$work/.env" "$@"; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT

sh deploy/self-host/gen-env.sh "$work/.env" localhost >/dev/null
{
  echo "OPENDESK_VERSION=${OPENDESK_VERSION:-smoke}"
  echo "HTTP_PORT=$HTTP_PORT"
  echo "HTTPS_PORT=$HTTPS_PORT"
  echo "OPENDESK_URL=$BASE"
  echo "OPENDESK_GATEWAY_URL=wss://localhost:${HTTPS_PORT}/socket"
} >>"$work/.env"

dc up -d "$BUILD" --wait --wait-timeout 300 || fail "the stack did not become healthy"
pass "stack is up and healthy ($(dc ps --services | tr '\n' ' '))"

code() { curl -sk -o /dev/null -w '%{http_code}' "$@"; }
[[ $(code "$BASE/healthz") == 200 ]] || fail "/healthz through Caddy"
[[ $(code "$BASE/readyz") == 200 ]] || fail "/readyz through Caddy (database or NATS not ready)"
pass "/healthz and /readyz answer 200 through Caddy"

grep -qi '<!doctype html' <<<"$(curl -sk "$BASE/")" || fail "the web index is not served"
grep -q 'window.__OPENDESK__' <<<"$(curl -sk "$BASE/config.js")" || fail "/config.js is not served"
[[ $(code "$BASE/widget/v1.js") == 200 ]] || fail "/widget/v1.js is not served"
grep -qi '^access-control-allow-origin: \*' <<<"$(curl -sk -D - -o /dev/null "$BASE/widget/v1.js")" ||
  fail "/widget/v1.js lacks Access-Control-Allow-Origin: *"
pass "web index, /config.js and /widget/v1.js are served"

[[ $(code "http://localhost:${HTTP_PORT}/") =~ ^30[178]$ ]] || fail "plain HTTP does not redirect to HTTPS"
pass "HTTP redirects to HTTPS"

secret=$(sed -n 's/^AUTH_SECRET=//p' "$work/.env")
wid=$(python3 -c 'import uuid; print(uuid.uuid4())')
token=$(SECRET="$secret" WID="$wid" python3 - <<'PY'
import base64, hashlib, hmac, json, os, time, uuid
b = lambda o: base64.urlsafe_b64encode(json.dumps(o, separators=(",", ":")).encode()).rstrip(b"=").decode()
now = int(time.time())
h = b({"alg": "HS256", "typ": "JWT"})
p = b({"purpose": "ws", "sub": str(uuid.uuid4()), "kind": "user", "wid": os.environ["WID"], "role": "owner",
       "name": "Smoke", "jti": str(uuid.uuid4()), "iat": now, "exp": now + 300})
s = base64.urlsafe_b64encode(hmac.new(os.environ["SECRET"].encode(), f"{h}.{p}".encode(), hashlib.sha256).digest()).rstrip(b"=").decode()
print(f"{h}.{p}.{s}")
PY
)
# The token rides in Sec-WebSocket-Protocol, as phoenix.js sends it (contracts/realtime.md);
# the gateway reads no token from the URL.
protocol="phoenix, base64url.bearer.phx.$(printf '%s' "$token" | base64 -w0 | tr '+/' '-_' | tr -d '=')"
upgrade() {
  curl -sk --http1.1 -o /dev/null -w '%{http_code}' --max-time 5 \
    -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
    -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' -H "Origin: $BASE" "$@" || true
}
[[ $(upgrade -H "Sec-WebSocket-Protocol: $protocol" "$BASE/socket/websocket?vsn=2.0.0") == 101 ]] || fail "/socket/websocket did not upgrade with a valid token"
[[ $(upgrade "$BASE/socket/websocket?vsn=2.0.0&token=$token") == 403 ]] || fail "/socket/websocket accepted a token in the URL"
[[ $(upgrade "$BASE/socket/websocket?vsn=2.0.0") == 403 ]] || fail "/socket/websocket accepted a connection without a token"
pass "/socket/websocket upgrades with a ws token in its protocol header (101) and refuses one in the URL or none (403)"

if command -v node >/dev/null 2>&1 && [[ $(node -p 'process.versions.node.split(".")[0]') -ge 22 ]]; then
  nats_token=$(sed -n 's/^NATS_TOKEN=//p' "$work/.env")
  envelope=$(printf '{"id":"%s","seq":1,"workspace_id":"%s","type":"ticket.updated","visibility":"agents","topics":["workspace:%s"],"data":{},"occurred_at":"2026-01-01T00:00:00Z"}' \
    "$(python3 -c 'import uuid; print(uuid.uuid4())')" "$wid" "$wid")
  (
    sleep 3
    docker run --rm --network "${PROJECT}_internal" natsio/nats-box:latest \
      nats --server "nats://${nats_token}@opendesk:4222" pub "od.events.$wid" "$envelope" >/dev/null 2>&1
  ) &
  NODE_TLS_REJECT_UNAUTHORIZED=0 URL="wss://localhost:${HTTPS_PORT}/socket/websocket?vsn=2.0.0" TOKEN="$token" WID="$wid" \
    node --no-warnings -e '
      const ws = new WebSocket(process.env.URL, ["phoenix", "base64url.bearer.phx." + Buffer.from(process.env.TOKEN).toString("base64url")]);
      const t = setTimeout(() => { console.error("no event within 20s"); process.exit(1); }, 20000);
      ws.onopen = () => ws.send(JSON.stringify(["1", "1", `workspace:${process.env.WID}`, "phx_join", {}]));
      ws.onmessage = (m) => { const [, , , ev] = JSON.parse(m.data); if (ev === "event") { clearTimeout(t); process.exit(0); } };
      ws.onerror = () => { console.error("socket error"); process.exit(1); };
    ' || fail "an event published on NATS did not reach the joined channel"
  wait
  pass "an event on NATS od.events.<wid> reaches the joined workspace channel"
fi

echo "self-host smoke test passed"
