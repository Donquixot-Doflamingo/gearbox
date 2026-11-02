#!/usr/bin/env bash
# Start and stop a complete local Gearbox in the background:
#   control plane + admin UI, a sidecar, the Go / Python / Java example services,
#   and a live "impact" page that shows what every service answers right now.
#
#   scripts/gearbox.sh start      # or: make start   (macOS: double-click "Start Gearbox.command")
#   scripts/gearbox.sh status     # what is running, and the URLs
#   scripts/gearbox.sh logs [server|sidecar|go|python|java|monitor]
#   scripts/gearbox.sh stop       # or: make stop    (macOS: double-click "Stop Gearbox.command")
#   scripts/gearbox.sh reset      # stop and delete local data (definitions, keys, cache)
#
# Data is kept in .data/run/store between runs (your UI changes survive a restart).
# Set GEARBOX_STORE=mongodb://127.0.0.1:27017/?directConnection=true to keep it in MongoDB instead.
# Ports: 8080 for the UI, or the next free one; GEARBOX_PORT=8090 picks one yourself.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"; cd "$ROOT"
[ "${GEARBOX_RESTRICTED:-0}" = "1" ] && source scripts/dev/restricted-env.sh >/dev/null
RUN="$ROOT/.data/run"; PIDS="$RUN/pids"; PORTS="$RUN/ports.env"
SOCK="/tmp/gearbox-run-$(id -u).sock"   # short path: Unix socket paths are limited to ~104 bytes
CRED=gbx_cred_local_run_0000000000000000000000
APP=lead_service

say() { printf '%s\n' "$*"; }

# true when something accepts connections on the port (IPv4 or IPv6 loopback)
port_in_use() {
  python3 - "$1" <<'PY'
import socket, sys
port = int(sys.argv[1])
for fam, host in ((socket.AF_INET, "127.0.0.1"), (socket.AF_INET6, "::1")):
    try:
        s = socket.socket(fam, socket.SOCK_STREAM)
    except OSError:
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

# first free port from $1 upwards
free_port() { local p=$1; while port_in_use "$p"; do p=$((p + 1)); done; echo "$p"; }

alive() { [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null; }

pid_of() { [ -f "$PIDS" ] && awk -v n="$1" '$1==n{print $2}' "$PIDS" | tail -1; }

any_running() {
  [ -f "$PIDS" ] || return 1
  while read -r _ pid; do alive "$pid" && return 0; done < "$PIDS"
  return 1
}

launch() { # name, then the command; runs in the background, survives closing the terminal
  local name=$1; shift
  nohup "$@" >"$RUN/$name.log" 2>&1 &
  echo "$name $!" >> "$PIDS"
}

wait_http() { # url, seconds
  local i; for i in $(seq 1 $(( $2 * 4 ))); do curl -fs -o /dev/null "$1" && return 0; sleep 0.25; done; return 1
}

open_url() {
  [ "${GEARBOX_NO_OPEN:-0}" = "1" ] && return 0
  if command -v open >/dev/null 2>&1; then open "$1" >/dev/null 2>&1 || true
  elif command -v xdg-open >/dev/null 2>&1; then xdg-open "$1" >/dev/null 2>&1 || true; fi
}

start() {
  if any_running; then say "Gearbox is already running."; status; return 0; fi
  mkdir -p "$RUN/keys" "$RUN/cache" "$RUN/objects" "$RUN/store"
  : > "$PIDS"

  say "==> checking tools"
  command -v go >/dev/null || { say "Go 1.24+ is required (macOS: brew install go)."; exit 1; }
  command -v python3 >/dev/null || { say "python3 is required."; exit 1; }

  say "==> building server, sidecar and Go service"
  go build -o bin/ ./controlplane/cmd/gearbox-server ./controlplane/cmd/gearboxctl ./sidecar/cmd/gearbox-sidecar ./examples/go-service

  if [ ! -f ui/build/index.html ]; then
    if command -v npm >/dev/null; then say "==> building the UI (first run only)"; (cd ui && npm ci --no-audit --no-fund && npm run build) >"$RUN/ui-build.log" 2>&1 || { say "UI build failed, see $RUN/ui-build.log"; exit 1; }
    else say "    UI not built and npm not found: the API runs, the UI page will be missing (install Node 20+)"; fi
  fi

  local java_ok=0 python_ok=0
  if java -version >/dev/null 2>&1; then
    if [ ! -f examples/java-service/target/java-service.jar ] && command -v mvn >/dev/null; then
      say "==> building the Java client and service (first run only)"
      { (cd clients/java && mvn -q install -DskipTests) && (cd examples/java-service && mvn -q package); } >"$RUN/java-build.log" 2>&1 || say "    Java build failed, see $RUN/java-build.log"
    fi
    [ -f examples/java-service/target/java-service.jar ] && java_ok=1
  fi
  python3 -c "import gearbox_client" 2>/dev/null && python_ok=1

  local UI_PORT MON_PORT GO_PORT PY_PORT JAVA_PORT CP_METRICS SC_METRICS
  UI_PORT=$(free_port "${GEARBOX_PORT:-8080}")
  CP_METRICS=$(free_port 9465); SC_METRICS=$(free_port $((CP_METRICS + 1)))
  GO_PORT=$(free_port 8081); PY_PORT=$(free_port $((GO_PORT + 1))); JAVA_PORT=$(free_port $((PY_PORT + 1)))
  MON_PORT=$(free_port "${GEARBOX_MONITOR_PORT:-8099}")
  [ "$UI_PORT" != "${GEARBOX_PORT:-8080}" ] && say "    port ${GEARBOX_PORT:-8080} is used by another program; the UI uses $UI_PORT"
  cat > "$PORTS" <<EOF
UI_PORT=$UI_PORT
MON_PORT=$MON_PORT
GO_PORT=$GO_PORT
PY_PORT=$PY_PORT
JAVA_PORT=$JAVA_PORT
SC_METRICS=$SC_METRICS
JAVA_OK=$java_ok
PYTHON_OK=$python_ok
EOF

  bin/gearboxctl keys generate --out "$RUN/keys" --id local --if-missing >/dev/null
  local STORE="${GEARBOX_STORE:-file://$RUN/store}" SEED=""
  [ -f "$RUN/seeded" ] || SEED="--seed"   # first run: load the example definitions; later runs keep your changes
  local UI_DIR=""; [ -f ui/build/index.html ] && UI_DIR="$ROOT/ui/build"

  say "==> control plane + UI on :$UI_PORT (store: ${STORE%%\?*})"
  GEARBOX_HTTP_ADDR=127.0.0.1:$UI_PORT GEARBOX_METRICS_ADDR=127.0.0.1:$CP_METRICS GEARBOX_PUBLIC_URL=http://127.0.0.1:$UI_PORT \
  GEARBOX_STORE="$STORE" GEARBOX_MONGO_DB="${GEARBOX_MONGO_DB:-gearbox_local}" GEARBOX_OBJECT_STORE="${GEARBOX_OBJECT_STORE:-file://$RUN/objects}" \
  GEARBOX_SIGNING_KEY_FILE="$RUN/keys/signing.key" GEARBOX_SIGNING_KEY_ID=local GEARBOX_AUTH_MODE=dev GEARBOX_UI_DIR="$UI_DIR" \
  GEARBOX_SEED_DIR="$ROOT/examples/definitions" GEARBOX_SEED_CREDENTIAL="$CRED" GEARBOX_LOG_FORMAT=text \
    launch server bin/gearbox-server $SEED
  if ! wait_http "http://127.0.0.1:$UI_PORT/api/v1/meta/version" 60; then
    say "The control plane did not start. Last lines of $RUN/server.log:"; tail -15 "$RUN/server.log"; stop >/dev/null; exit 1
  fi
  [ -n "$SEED" ] && touch "$RUN/seeded"

  say "==> sidecar ($SOCK)"
  rm -f "$SOCK"
  GEARBOX_SERVER_URL=http://127.0.0.1:$UI_PORT GEARBOX_APP=$APP GEARBOX_ENV=dev GEARBOX_CREDENTIAL="$CRED" \
  GEARBOX_TRUSTED_KEYS_FILE="$RUN/keys/trusted-keys.txt" GEARBOX_SOCKET="$SOCK" GEARBOX_CACHE_DIR="$RUN/cache" \
  GEARBOX_METRICS_ADDR=127.0.0.1:$SC_METRICS GEARBOX_SIDECAR_ID=local-sidecar GEARBOX_LOG_SINK=server GEARBOX_LOG_SAMPLE=1 \
  GEARBOX_POLL_INTERVAL=2s GEARBOX_FETCH_JITTER=0s \
    launch sidecar bin/gearbox-sidecar
  wait_http "http://127.0.0.1:$SC_METRICS/readyz" 30 || say "    sidecar not ready yet, see $RUN/sidecar.log"

  say "==> example services"
  GEARBOX_TARGET="unix://$SOCK" EXAMPLE_ADDR=127.0.0.1:$GO_PORT launch go bin/go-service
  if [ $python_ok = 1 ]; then GEARBOX_TARGET="unix://$SOCK" EXAMPLE_ADDR=127.0.0.1:$PY_PORT launch python python3 examples/python-service/app.py
  else say "    python: skipped (one time: python3 -m pip install grpcio protobuf grpcio-tools && (cd clients/python && ./scripts/gen.sh && python3 -m pip install -e .))"; fi
  if [ $java_ok = 1 ]; then GEARBOX_TARGET="unix://$SOCK" EXAMPLE_ADDR=127.0.0.1:$JAVA_PORT launch java java -jar examples/java-service/target/java-service.jar
  elif java -version >/dev/null 2>&1; then say "    java: skipped (could not build the jar; needs Maven: brew install maven — see $RUN/java-build.log)"
  else say "    java: skipped (one time: brew install openjdk@17 maven, then run start again)"; fi

  MONITOR_PORT=$MON_PORT GEARBOX_UI_URL=http://127.0.0.1:$UI_PORT SIDECAR_METRICS=http://127.0.0.1:$SC_METRICS \
  GO_URL=http://127.0.0.1:$GO_PORT PYTHON_URL=$([ $python_ok = 1 ] && echo "http://127.0.0.1:$PY_PORT") \
  JAVA_URL=$([ $java_ok = 1 ] && echo "http://127.0.0.1:$JAVA_PORT") \
    launch monitor python3 scripts/run/monitor.py
  wait_http "http://127.0.0.1:$GO_PORT/healthz" 20 || true
  wait_http "http://127.0.0.1:$MON_PORT/" 10 || true

  say ""
  status
  open_url "http://127.0.0.1:$UI_PORT"
  open_url "http://127.0.0.1:$MON_PORT"
}

stop() {
  if [ ! -f "$PIDS" ]; then say "Gearbox is not running."; return 0; fi
  local name pid
  # services first, the control plane last
  for name in monitor java python go sidecar server; do
    pid=$(pid_of "$name"); alive "$pid" && kill "$pid" 2>/dev/null || true
  done
  for _ in $(seq 1 40); do any_running || break; sleep 0.25; done
  while read -r _ pid; do alive "$pid" && kill -9 "$pid" 2>/dev/null || true; done < "$PIDS"
  rm -f "$PIDS" "$SOCK"
  say "Gearbox stopped. Your data is kept in .data/run (scripts/gearbox.sh reset deletes it)."
}

status() {
  if [ ! -f "$PORTS" ] || ! any_running; then say "Gearbox is not running. Start it: make start"; return 0; fi
  # shellcheck disable=SC1090
  source "$PORTS"
  row() { # name pid url
    if alive "$2"; then printf '  %-22s running   %s\n' "$1" "$3"; else printf '  %-22s stopped   %s\n' "$1" "${4:-see $RUN/$5.log}"; fi
  }
  say "Gearbox (local)"
  row "Admin UI + API"        "$(pid_of server)"  "http://127.0.0.1:$UI_PORT" "" server
  row "Live impact page"      "$(pid_of monitor)" "http://127.0.0.1:$MON_PORT" "" monitor
  row "Sidecar"               "$(pid_of sidecar)" "unix://$SOCK  (metrics http://127.0.0.1:$SC_METRICS/metrics)" "" sidecar
  row "Go service"            "$(pid_of go)"      "POST http://127.0.0.1:$GO_PORT/route" "" go
  if [ "$PYTHON_OK" = 1 ]; then row "Python service" "$(pid_of python)" "POST http://127.0.0.1:$PY_PORT/route" "" python; else printf '  %-22s not set up\n' "Python service"; fi
  if [ "$JAVA_OK" = 1 ]; then row "Java service" "$(pid_of java)" "POST http://127.0.0.1:$JAVA_PORT/route" "" java; else printf '  %-22s not set up\n' "Java service"; fi
  say ""
  say "  Try it: open the UI, Segments → blocked_cities → add Indore → Cut version → Request release → Publish to dev,"
  say "  then watch the Indore row change on the live impact page. Or: GEARBOX_API=http://127.0.0.1:$UI_PORT/api/v1 scripts/demo/change.sh block Indore"
  say "  Stop: make stop   ·   Logs: scripts/gearbox.sh logs server"
}

logs() {
  local name=${1:-server}
  [ -f "$RUN/$name.log" ] || { say "no log for '$name' (server, sidecar, go, python, java, monitor)"; exit 1; }
  tail -n 50 -f "$RUN/$name.log"
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  status) status ;;
  logs) logs "${2:-}" ;;
  reset) stop; rm -rf "$RUN"; say "Local data deleted." ;;
  *) say "usage: scripts/gearbox.sh start|stop|restart|status|logs [name]|reset"; exit 2 ;;
esac
