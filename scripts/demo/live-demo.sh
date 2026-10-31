#!/usr/bin/env bash
# Live demo: control plane (+ UI) → sidecar → Go, Python and Java services, side by side.
#
#   scripts/demo/live-demo.sh            # start everything and watch the three services' answers
#   scripts/demo/change.sh block Indore  # (other terminal) change data in the control plane …
#   … or change it in the UI at http://127.0.0.1:8080 and watch the services flip within seconds.
#
# Port 8080 taken by another app? GEARBOX_DEMO_PORT=8090 scripts/demo/live-demo.sh
# (then: GEARBOX_API=http://127.0.0.1:8090/api/v1 scripts/demo/change.sh block Indore).
#
# Store: memory by default; MongoDB with GEARBOX_STORE=mongodb://localhost:27017/?directConnection=true
# Needs Go 1.24+. Python service needs `pip install -e clients/python` (with stubs generated);
# Java service needs `mvn -q install` in clients/java and `mvn -q package` in examples/java-service.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$ROOT"
[ "${GEARBOX_RESTRICTED:-0}" = "1" ] && source scripts/dev/restricted-env.sh >/dev/null
D="$ROOT/.data/demo"; mkdir -p "$D/keys" "$D/cache" "$D/objects"
SOCK="${GEARBOX_DEMO_SOCK:-/tmp/gearbox-demo.sock}"   # short path: Unix socket paths are limited to ~104 bytes
CRED=gbx_cred_demo_local_000000000000000000000
PORT="${GEARBOX_DEMO_PORT:-8080}"
API="http://127.0.0.1:$PORT/api/v1"

# Refuse to start when a port is already in use. Checks IPv4 and IPv6 loopback, because a browser
# opening "localhost" may go to [::1] and reach another app even when 127.0.0.1 is free.
port_in_use() {
  python3 - "$1" <<'PY'
import socket, sys
port = int(sys.argv[1])
for fam, host in ((socket.AF_INET, "127.0.0.1"), (socket.AF_INET6, "::1")):
    try:
        s = socket.socket(fam, socket.SOCK_STREAM)
    except OSError:  # no IPv6 on this machine
        continue
    s.settimeout(0.3)
    try:
        if s.connect_ex((host, port)) == 0:
            sys.exit(0)
    except OSError:
        pass
    finally:
        s.close()
sys.exit(1)
PY
}
busy=""
for p in "$PORT" 9465 9464 8081 8082 8083; do port_in_use "$p" && busy="$busy $p"; done
if [ -n "$busy" ]; then
  echo "Port(s) already in use by another program:$busy"
  echo "Stop that program, or for the control plane/UI pick another port: GEARBOX_DEMO_PORT=8090 make demo"
  echo "(see what is listening: lsof -nP -iTCP:${busy# } -sTCP:LISTEN)"
  exit 1
fi

pids=(); cleanup() { kill "${pids[@]}" 2>/dev/null || true; }; trap cleanup EXIT INT TERM

echo "==> build"
go build -o bin/ ./controlplane/cmd/gearbox-server ./controlplane/cmd/gearboxctl ./sidecar/cmd/gearbox-sidecar ./examples/go-service
bin/gearboxctl keys generate --out "$D/keys" --id demo --if-missing >/dev/null

UI_DIR=""; [ -f ui/build/index.html ] && UI_DIR="$ROOT/ui/build"
echo "==> control plane on :$PORT (store: ${GEARBOX_STORE:-memory}; UI: ${UI_DIR:-not built — run 'cd ui && npm ci && npm run build'})"
GEARBOX_HTTP_ADDR=127.0.0.1:$PORT GEARBOX_METRICS_ADDR=127.0.0.1:9465 GEARBOX_PUBLIC_URL=http://127.0.0.1:$PORT \
GEARBOX_STORE="${GEARBOX_STORE:-memory}" GEARBOX_MONGO_DB="${GEARBOX_MONGO_DB:-gearbox_demo}" \
GEARBOX_OBJECT_STORE="${GEARBOX_OBJECT_STORE:-file://$D/objects}" \
GEARBOX_SIGNING_KEY_FILE="$D/keys/signing.key" GEARBOX_SIGNING_KEY_ID=demo \
GEARBOX_AUTH_MODE=dev GEARBOX_UI_DIR="$UI_DIR" \
GEARBOX_SEED_DIR="$ROOT/examples/definitions" GEARBOX_SEED_CREDENTIAL="$CRED" \
  bin/gearbox-server --seed >"$D/server.log" 2>&1 & pids+=($!)
for _ in $(seq 1 120); do curl -fs -o /dev/null $API/meta/version && break; sleep 0.5; done

echo "==> sidecar ($SOCK), polling every 2 s plus the push feed"
rm -f "$SOCK"
GEARBOX_SERVER_URL=http://127.0.0.1:$PORT GEARBOX_APP=lead_service GEARBOX_ENV=dev GEARBOX_CREDENTIAL="$CRED" \
GEARBOX_TRUSTED_KEYS_FILE="$D/keys/trusted-keys.txt" GEARBOX_SOCKET="$SOCK" GEARBOX_CACHE_DIR="$D/cache" \
GEARBOX_METRICS_ADDR=127.0.0.1:9464 GEARBOX_SIDECAR_ID=demo-sidecar GEARBOX_LOG_SINK=server GEARBOX_LOG_SAMPLE=1 \
GEARBOX_POLL_INTERVAL=2s \
  bin/gearbox-sidecar >"$D/sidecar.log" 2>&1 & pids+=($!)
for _ in $(seq 1 60); do curl -fs 127.0.0.1:9464/readyz >/dev/null 2>&1 && break; sleep 0.5; done

echo "==> services: go :8081, python :8082, java :8083"
GEARBOX_TARGET="unix://$SOCK" EXAMPLE_ADDR=127.0.0.1:8081 bin/go-service >"$D/go.log" 2>&1 & pids+=($!)
if python3 -c "import gearbox_client" 2>/dev/null; then
  GEARBOX_TARGET="unix://$SOCK" EXAMPLE_ADDR=127.0.0.1:8082 python3 examples/python-service/app.py >"$D/python.log" 2>&1 & pids+=($!)
else echo "    python: skipped (pip install -e clients/python after scripts/gen.sh)"; fi
if ! java -version >/dev/null 2>&1; then echo "    java: skipped (no Java runtime; macOS: brew install openjdk@17)"
elif [ -f examples/java-service/target/java-service.jar ]; then
  GEARBOX_TARGET="unix://$SOCK" EXAMPLE_ADDR=127.0.0.1:8083 java -jar examples/java-service/target/java-service.jar >"$D/java.log" 2>&1 & pids+=($!)
else echo "    java: skipped (build clients/java and examples/java-service with Maven)"; fi
sleep 3

LEAD='{"lead":{"id":"L-77","city":"Indore","source":"web"},"car":{"make":"Hyundai","model":"i20","year":2021,"km":12000,"price":690000},"inspections":[{"part":"engine","score":6}]}'
echo
echo "Open http://127.0.0.1:$PORT (no sign-in; use 127.0.0.1, not localhost). Change something (e.g. Segments → blocked_cities → add Indore → Versions → cut → Change requests → publish to dev),"
echo "or run: GEARBOX_API=$API scripts/demo/change.sh block Indore   — and watch the lines below change. Ctrl-C stops everything."
echo
printf '%-8s %-5s | %-36s | %-36s | %-36s\n' time gen "go :8081" "python :8082" "java :8083"
show() { curl -s -m 1 -XPOST "127.0.0.1:$1/route" -d "$LEAD" 2>/dev/null | python3 -c 'import json,sys
try:
  d=json.load(sys.stdin); print("%s/%s %s%s"%(d.get("route"),d.get("reason"),d.get("version")," FALLBACK" if d.get("fromClientFallback") else ""))
except Exception: print("-")'; }
for _ in $(seq 1 "${DEMO_TICKS:-100000}"); do
  gen=$(curl -fs 127.0.0.1:9464/metrics 2>/dev/null | awk '/^gearbox_bundle_generation /{print int($2)}')
  printf '%-8s %-5s | %-36s | %-36s | %-36s\n' "$(date +%T)" "${gen:-?}" "$(show 8081)" "$(show 8082)" "$(show 8083)"
  sleep "${DEMO_INTERVAL:-1}"
done
