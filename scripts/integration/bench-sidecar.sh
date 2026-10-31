#!/usr/bin/env bash
# Measures full-call latency (Go client → Unix socket → sidecar → engine → back) with an
# isolated sidecar serving examples/definitions. Prints p50/p95/p99 for 3 runs of 5,000 calls.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$ROOT"
[ "${GEARBOX_RESTRICTED:-0}" = "1" ] && source scripts/dev/restricted-env.sh >/dev/null
W="$ROOT/.data/bench"; rm -rf "$W"; mkdir -p "$W"
go build -o bin/ ./sidecar/cmd/gearbox-sidecar && go build -o bin/gearbox-probe ./examples/probe
GEARBOX_SOCKET="$W/e.sock" GEARBOX_METRICS_ADDR=127.0.0.1:19501 GEARBOX_LOG_SINK=stdout GEARBOX_LOG_SAMPLE=0.01 \
  bin/gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned >"$W/sidecar.log" 2>&1 &
pid=$!; trap 'kill $pid 2>/dev/null' EXIT
for _ in $(seq 1 40); do curl -fs 127.0.0.1:19501/readyz >/dev/null 2>&1 && break; sleep 0.25; done
echo '{"lead":{"id":"L-77","city":"Indore","source":"web"},"car":{"make":"Hyundai","model":"i20","year":2021,"km":12000,"price":690000},"inspections":[{"part":"engine","score":6}]}' > "$W/in.json"
for i in 1 2 3; do bin/gearbox-probe -target "unix://$W/e.sock" bench lead_routing "$W/in.json" -n 5000 | tr -d '\n '; echo; done
