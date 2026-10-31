#!/usr/bin/env bash
# Measures, for N real releases (block/unblock a city in blocked_cities):
#   publish  = change request + approval + publish API calls (server builds, tests, signs, uploads the bundle)
#   swap     = from the publish API returning to the sidecar serving the new generation (push feed path)
# Prints one JSON line per release and a summary. Server uses the memory store and filesystem object store.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"; cd "$ROOT"
[ "${GEARBOX_RESTRICTED:-0}" = "1" ] && source scripts/dev/restricted-env.sh >/dev/null
N="${N:-10}"; W="$ROOT/.data/bench-prop"; rm -rf "$W"; mkdir -p "$W/keys" "$W/cache" "$W/obj"
API=http://127.0.0.1:18180/api/v1; CRED=gbx_cred_bench_local_0000000000000000000; APP=lead_service
go build -o bin/ ./controlplane/cmd/gearbox-server ./controlplane/cmd/gearboxctl ./sidecar/cmd/gearbox-sidecar
bin/gearboxctl keys generate --out "$W/keys" --id bench --if-missing >/dev/null
pids=(); trap 'kill "${pids[@]}" 2>/dev/null || true' EXIT
GEARBOX_HTTP_ADDR=127.0.0.1:18180 GEARBOX_METRICS_ADDR=127.0.0.1:19180 GEARBOX_PUBLIC_URL=http://127.0.0.1:18180 \
GEARBOX_STORE=memory GEARBOX_OBJECT_STORE="file://$W/obj" GEARBOX_SIGNING_KEY_FILE="$W/keys/signing.key" GEARBOX_SIGNING_KEY_ID=bench \
GEARBOX_AUTH_MODE=dev GEARBOX_SEED_DIR="$ROOT/examples/definitions" GEARBOX_SEED_CREDENTIAL="$CRED" \
  bin/gearbox-server --seed >"$W/server.log" 2>&1 & pids+=($!)
for _ in $(seq 1 120); do curl -fs -o /dev/null $API/meta/version && break; sleep 0.25; done
GEARBOX_SERVER_URL=http://127.0.0.1:18180 GEARBOX_APP=$APP GEARBOX_ENV=dev GEARBOX_CREDENTIAL="$CRED" \
GEARBOX_TRUSTED_KEYS_FILE="$W/keys/trusted-keys.txt" GEARBOX_SOCKET="$W/e.sock" GEARBOX_CACHE_DIR="$W/cache" \
GEARBOX_METRICS_ADDR=127.0.0.1:19181 GEARBOX_FETCH_JITTER="${JITTER:-0s}" GEARBOX_POLL_INTERVAL=30s GEARBOX_LOG_SINK=stdout \
  bin/gearbox-sidecar >"$W/sidecar.log" 2>&1 & pids+=($!)
for _ in $(seq 1 60); do curl -fs 127.0.0.1:19181/readyz >/dev/null 2>&1 && break; sleep 0.25; done
python3 - "$API" "$N" <<'PY'
import json, sys, time, urllib.request, statistics
api, n = sys.argv[1], int(sys.argv[2]); app = "lead_service"
def call(method, path, body=None, headers=None):
    req = urllib.request.Request(api + path, method=method, data=None if body is None else json.dumps(body).encode(),
                                 headers={"content-type": "application/json", **(headers or {})})
    with urllib.request.urlopen(req, timeout=30) as r:
        raw = r.read(); return json.loads(raw) if raw else None
def gen():
    with urllib.request.urlopen("http://127.0.0.1:19181/metrics", timeout=2) as r:
        for line in r.read().decode().splitlines():
            if line.startswith("gearbox_bundle_generation "): return int(float(line.split()[1]))
pub, swap = [], []
for i in range(n):
    members = ["Leh", "Kargil", "Port Blair"] + (["Indore"] if i % 2 == 0 else [])
    call("PUT", f"/apps/{app}/segments/blocked_cities/members", {"members": members})
    rev = call("GET", f"/apps/{app}/segments/blocked_cities")["draft"]["revision"]
    ver = call("POST", f"/apps/{app}/segments/blocked_cities/versions", {"revision": rev})["version"]
    t0 = time.perf_counter()
    cr = call("POST", f"/apps/{app}/change-requests", {"environment": "dev", "title": f"bench {i}", "items": [{"kind": "segments", "name": "blocked_cities", "version": ver}]})["id"]
    try: call("POST", f"/apps/{app}/change-requests/{cr}/approve", {}, {"X-Gearbox-Dev-User": "approver@example.com", "X-Gearbox-Dev-Role": "approver"})
    except Exception: pass
    g = call("POST", f"/apps/{app}/environments/dev/releases", {"type": "change_request", "changeRequestId": cr})["generation"]
    t1 = time.perf_counter()
    while gen() != g: time.sleep(0.002)
    t2 = time.perf_counter()
    pub.append((t1 - t0) * 1000); swap.append((t2 - t1) * 1000)
    print(json.dumps({"release": i + 1, "generation": g, "publishMs": round(pub[-1], 1), "swapMs": round(swap[-1], 1)}))
s = lambda xs: {"min": round(min(xs), 1), "median": round(statistics.median(xs), 1), "max": round(max(xs), 1)}
print(json.dumps({"summary": True, "releases": n, "publishMs": s(pub), "swapMs": s(swap)}))
PY
