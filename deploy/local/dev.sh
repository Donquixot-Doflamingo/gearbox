#!/usr/bin/env bash
# `make dev`: run the whole system locally without containers.
#   server   :8080 (API + auth, memory store, filesystem object store in .data/, dev auth, seeded lead_service)
#   sidecar  socket .data/run/engine.sock, metrics :9464, synced from the server
#   UI       :5173 (SvelteKit dev server; API at :8080)
#   example  :8081 (only with DEV_EXAMPLE=1)
# Stop with Ctrl-C. Set GEARBOX_RESTRICTED=1 in a network-restricted sandbox.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
if [ "${GEARBOX_RESTRICTED:-0}" = "1" ]; then
  # shellcheck disable=SC1091
  source scripts/dev/restricted-env.sh
fi

DATA="$ROOT/.data"
CRED="${GEARBOX_DEV_CREDENTIAL:-gbx_cred_dev_local_0000000000000000}"
mkdir -p "$DATA/keys" "$DATA/run" "$DATA/objects" "$DATA/cache" bin

echo "==> building binaries"
go build -o bin/ ./controlplane/cmd/gearbox-server ./controlplane/cmd/gearboxctl ./sidecar/cmd/gearbox-sidecar

echo "==> dev signing key"
bin/gearboxctl keys generate --out "$DATA/keys" --id dev-local --if-missing

pids=()
cleanup() {
  trap - EXIT INT TERM
  kill "${pids[@]}" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "==> server on :8080"
GEARBOX_HTTP_ADDR=:8080 \
GEARBOX_METRICS_ADDR=:9465 \
GEARBOX_PUBLIC_URL=http://localhost:8080 \
GEARBOX_STORE=memory \
GEARBOX_OBJECT_STORE="file://$DATA/objects" \
GEARBOX_SIGNING_KEY_FILE="$DATA/keys/signing.key" \
GEARBOX_SIGNING_KEY_ID=dev-local \
GEARBOX_AUTH_MODE=dev \
GEARBOX_ADMIN_EMAILS=dev@example.com \
GEARBOX_SEED_DIR="$ROOT/examples/definitions" \
GEARBOX_SEED_CREDENTIAL="$CRED" \
  bin/gearbox-server --seed &
pids+=($!)

for _ in $(seq 1 120); do
  curl -fsS -o /dev/null http://localhost:8080/api/v1/meta/version && break
  sleep 0.5
done

echo "==> sidecar on $DATA/run/engine.sock (metrics :9464)"
GEARBOX_SERVER_URL=http://localhost:8080 \
GEARBOX_APP=lead_service \
GEARBOX_ENV=dev \
GEARBOX_CREDENTIAL="$CRED" \
GEARBOX_TRUSTED_KEYS_FILE="$DATA/keys/trusted-keys.txt" \
GEARBOX_SOCKET="$DATA/run/engine.sock" \
GEARBOX_CACHE_DIR="$DATA/cache" \
GEARBOX_METRICS_ADDR=:9464 \
GEARBOX_SIDECAR_ID=dev-sidecar \
GEARBOX_LOG_SINK=server \
GEARBOX_LOG_SAMPLE=1 \
  bin/gearbox-sidecar &
pids+=($!)

if [ -f ui/package.json ]; then
  echo "==> UI dev server on :5173"
  (
    cd ui
    [ -d node_modules ] || npm install
    GEARBOX_PROXY=http://localhost:8080 exec npm run dev -- --port 5173
  ) &
  pids+=($!)
else
  echo "==> ui/package.json not found; skipping the UI (the server also serves GEARBOX_UI_DIR if set)"
fi

if [ "${DEV_EXAMPLE:-0}" = "1" ]; then
  echo "==> example service on :8081"
  (cd examples/go-service && GEARBOX_TARGET="unix://$DATA/run/engine.sock" GOWORK=off exec go run .) &
  pids+=($!)
fi

cat <<MSG

Gearbox is starting:
  UI       http://localhost:5173   (API http://localhost:8080/api/v1, dev auth)
  sidecar  unix://$DATA/run/engine.sock   readiness: curl localhost:9464/readyz
  try      DEV_EXAMPLE=1 make dev, then
           curl -XPOST localhost:8081/route -d @examples/go-service/sample-lead.json
MSG

wait -n "${pids[@]}"
