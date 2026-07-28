#!/usr/bin/env bash
# run-app.sh — launch the fixture stub plus the production server for the
# Playwright app-* projects.
#
#   APP_PORT   (default 8899)  port the production server listens on
#   STUB_PORT  (default APP_PORT+200)  port the fixture stub listens on
#
# Builds both binaries once (per port, so parallel instances never race on
# the output file), starts the stub, starts the server with every upstream
# base URL pointed at the stub and compressed cache TTLs (E2E_CACHE_TTL) so
# degraded-mode specs can watch entries age out in seconds, then waits for
# /healthz to report all six sources available before going quiet.
#
# Never point this at a real AirNow key — the stub accepts any key.
set -euo pipefail

APP_PORT="${APP_PORT:-8899}"
STUB_PORT="${STUB_PORT:-$((APP_PORT + 200))}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/e2e/bin"
mkdir -p "$BIN"

echo "[run-app] building stub + server (app :$APP_PORT, stub :$STUB_PORT)" >&2
(cd "$ROOT" && go build -o "$BIN/stub-$STUB_PORT" ./e2e/stub \
            && go build -o "$BIN/server-$APP_PORT" ./cmd/server)

STUB_PID=""
SRV_PID=""
cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
  [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

"$BIN/stub-$STUB_PORT" -addr "127.0.0.1:$STUB_PORT" -fixtures "$ROOT" &
STUB_PID=$!

STUB="http://127.0.0.1:$STUB_PORT"
for _ in $(seq 1 100); do
  if curl -fsS "$STUB/control/status" >/dev/null 2>&1; then break; fi
  sleep 0.1
done

PORT="$APP_PORT" \
NWS_BASE_URL="$STUB" \
OPENMETEO_BASE_URL="$STUB" \
OPENMETEO_AQ_BASE_URL="$STUB" \
AIRNOW_BASE_URL="$STUB" \
AIRNOW_API_KEY="e2e-stub-accepts-any-key" \
E2E_CACHE_TTL="${E2E_CACHE_TTL:-1s,8s}" \
"$BIN/server-$APP_PORT" &
SRV_PID=$!

# Warm-up: all six sources available on /healthz. Specs gate on this too
# (Playwright's webServer poll only checks for HTTP 200). The `|| true`
# keeps set -e/pipefail from killing the script while healthz is still
# unreachable or reports zero warm sources.
for _ in $(seq 1 150); do
  n="$(curl -fsS "http://127.0.0.1:$APP_PORT/healthz" 2>/dev/null | grep -c '"available":true' || true)"
  if [ "${n:-0}" -ge 6 ]; then
    echo "[run-app] warm: all $n sources available" >&2
    break
  fi
  sleep 0.2
done

wait "$SRV_PID"
