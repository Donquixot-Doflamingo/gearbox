#!/usr/bin/env bash
# Throughput and latency under load: N goroutines share one Go client and call
# lead_routing in a loop for D per level against an isolated sidecar (offline
# definitions, Unix socket). One JSON line per level: calls, perSecond, perMinute,
# p50/p95/p99/max in microseconds, errors. The load generator runs on the same
# machine as the sidecar, so they compete for CPU.
#   LEVELS=1,4,8,16,32 DURATION=10s scripts/integration/bench-load.sh
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$ROOT"
[ "${GEARBOX_RESTRICTED:-0}" = "1" ] && source scripts/dev/restricted-env.sh >/dev/null
W="$ROOT/.data/bench-load"; rm -rf "$W"; mkdir -p "$W"
go build -o bin/ ./sidecar/cmd/gearbox-sidecar && go build -o bin/gearbox-probe ./examples/probe
GEARBOX_SOCKET="$W/e.sock" GEARBOX_METRICS_ADDR=127.0.0.1:19502 GEARBOX_LOG_SINK=stdout GEARBOX_LOG_SAMPLE=0.01 \
  bin/gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned >"$W/sidecar.log" 2>&1 &
pid=$!; trap 'kill $pid 2>/dev/null' EXIT
for _ in $(seq 1 40); do curl -fs 127.0.0.1:19502/readyz >/dev/null 2>&1 && break; sleep 0.25; done
echo '{"lead":{"id":"L-77","city":"Indore","source":"web"},"car":{"make":"Hyundai","model":"i20","year":2021,"km":12000,"price":690000},"inspections":[{"part":"engine","score":6}]}' > "$W/in.json"
bin/gearbox-probe -target "unix://$W/e.sock" load lead_routing "$W/in.json" -c "${LEVELS:-1,4,8,16,32}" -d "${DURATION:-10s}" | tr -d '\n ' | sed 's/}{/}\n{/g'; echo
