#!/usr/bin/env bash
# End-to-end integration test of the whole system against real infrastructure:
#   MongoDB (replica set) + LocalStack S3  →  gearbox-server (dev auth, no sign-in)
#   → signed bundle in S3 → gearbox-sidecar (presigned S3 downloads, Unix socket)
#   → examples/go-service (Go client) → decisions.
#
# Needs Docker. Usage:  scripts/integration/e2e.sh        (KEEP=1 leaves everything running)
# In a network-restricted sandbox set GEARBOX_RESTRICTED=1.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
[ "${GEARBOX_RESTRICTED:-0}" = "1" ] && source scripts/dev/restricted-env.sh >/dev/null

# stop anything a previous (aborted) run left behind
if [ -f "$ROOT/.data/e2e.pids" ]; then xargs -r kill < "$ROOT/.data/e2e.pids" 2>/dev/null || true; sleep 1; fi
W="$ROOT/.data/e2e"; rm -rf "$W"; mkdir -p "$W/keys" "$W/run" "$W/cache"
: > "$ROOT/.data/e2e.pids"
API=http://127.0.0.1:18080/api/v1
CRED=gbx_cred_e2e_local_00000000000000000000
MONGO_URI="mongodb://127.0.0.1:27017/?replicaSet=rs0"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
pass=0; fail=0
ok()   { echo "  PASS  $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $1"; fail=$((fail+1)); }
check(){ if eval "$2"; then ok "$1"; else bad "$1"; fi; }
j()    { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }

pids=()
cleanup() {
  [ "${KEEP:-0}" = "1" ] && { echo "KEEP=1: leaving processes and containers running"; return; }
  kill "${pids[@]}" 2>/dev/null || true
  docker rm -f gbx-e2e-mongo gbx-e2e-s3 >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> infrastructure: MongoDB 7 + LocalStack S3"
docker rm -f gbx-e2e-mongo gbx-e2e-s3 >/dev/null 2>&1 || true
docker run -d --name gbx-e2e-mongo --network host mongo:7.0 --replSet rs0 --bind_ip 127.0.0.1 --port 27017 >/dev/null
docker run -d --name gbx-e2e-s3 --network host -e SERVICES=s3 -e GATEWAY_LISTEN=127.0.0.1:4566 localstack/localstack:3.8 >/dev/null
for _ in $(seq 1 60); do docker exec gbx-e2e-mongo mongosh --quiet --eval 'db.runCommand({ping:1}).ok' >/dev/null 2>&1 && break; sleep 1; done
docker exec gbx-e2e-mongo mongosh --quiet --eval 'try{rs.initiate({_id:"rs0",members:[{_id:0,host:"127.0.0.1:27017"}]})}catch(e){}' >/dev/null
for _ in $(seq 1 60); do curl -fs 127.0.0.1:4566/_localstack/health | grep -q '"s3": "\(available\|running\)"' && break; sleep 1; done
docker exec gbx-e2e-s3 awslocal s3 mb s3://gearbox >/dev/null

echo "==> build"
go build -o bin/ ./controlplane/cmd/gearbox-server ./controlplane/cmd/gearboxctl ./sidecar/cmd/gearbox-sidecar ./examples/go-service
go build -o bin/gearbox-probe ./examples/probe
bin/gearboxctl keys generate --out "$W/keys" --id e2e --if-missing >/dev/null

echo "==> gearbox-server (MongoDB store, S3 object store, dev auth)"
GEARBOX_HTTP_ADDR=127.0.0.1:18080 GEARBOX_METRICS_ADDR=127.0.0.1:19465 \
GEARBOX_PUBLIC_URL=http://127.0.0.1:18080 \
GEARBOX_STORE="$MONGO_URI" GEARBOX_MONGO_DB=gearbox_e2e \
GEARBOX_OBJECT_STORE="s3://gearbox/e2e?endpoint=http://127.0.0.1:4566&region=us-east-1&pathStyle=true" \
GEARBOX_SIGNING_KEY_FILE="$W/keys/signing.key" GEARBOX_SIGNING_KEY_ID=e2e \
GEARBOX_AUTH_MODE=dev GEARBOX_SEED_DIR="$ROOT/examples/definitions" GEARBOX_SEED_CREDENTIAL="$CRED" \
  bin/gearbox-server --seed >"$W/server.log" 2>&1 &
pids+=($!); echo $! >> "$ROOT/.data/e2e.pids"
for _ in $(seq 1 120); do curl -fs -o /dev/null $API/meta/version && break; sleep 0.5; done
check "server is up" "curl -fs $API/meta/version >/dev/null"
PROBE="bin/gearbox-probe -target unix://$W/run/engine.sock"
gen_now() { curl -fs 127.0.0.1:19464/metrics | awk '/^gearbox_bundle_generation /{print int($2)}'; }
wait_gen() { for _ in $(seq 1 60); do [ "$(gen_now)" = "$1" ] && return 0; sleep 0.5; done; return 1; }
# release_items <json items> → prints the new generation (change request → approve → publish)
release_items() {
  local cr body gen
  for _ in 1 2 3 4 5; do
    body=$(curl -s -XPOST $API/apps/lead_service/change-requests -H 'content-type: application/json' \
      -d "{\"environment\":\"dev\",\"title\":\"e2e\",\"items\":$1}")
    cr=$(echo "$body" | j 'd.get("id","")' 2>/dev/null || true); [ -n "$cr" ] && break
    echo "    change request retry: $body" >&2; sleep 1
  done
  curl -s -o /dev/null -XPOST $API/apps/lead_service/change-requests/$cr/approve -H 'X-Gearbox-Dev-User: approver@example.com' -H 'X-Gearbox-Dev-Role: approver' -H 'content-type: application/json' -d '{}' || true
  for _ in 1 2 3 4 5; do
    body=$(curl -s -XPOST $API/apps/lead_service/environments/dev/releases -H 'content-type: application/json' -d "{\"type\":\"change_request\",\"changeRequestId\":\"$cr\"}")
    gen=$(echo "$body" | j 'd.get("generation","")' 2>/dev/null || true); [ -n "$gen" ] && { echo "$gen"; return 0; }
    echo "    release retry: $body" >&2; sleep 1
  done
  echo ""
}
# create_and_cut <kind> <json body> → prints version 1 after creating and cutting it
create_and_cut() {
  local name; name=$(echo "$2" | j 'd["name"]')
  curl -fs -o /dev/null -XPOST $API/apps/lead_service/$1 -H 'content-type: application/json' -d "$2"
  local rev; rev=$(curl -fs $API/apps/lead_service/$1/$name | j 'd["draft"]["revision"]')
  curl -fs -XPOST $API/apps/lead_service/$1/$name/versions -H 'content-type: application/json' -d "{\"revision\":$rev}" | j 'd["version"]'
}

check "seeded app stored in MongoDB" "docker exec gbx-e2e-mongo mongosh --quiet gearbox_e2e --eval 'db.getCollectionNames().length' | grep -qv '^0$'"
for _ in $(seq 1 30); do docker exec gbx-e2e-s3 awslocal s3 ls s3://gearbox/e2e/ --recursive 2>/dev/null | grep -c envelopes/ >/dev/null && break; sleep 1; done
check "bundle objects written to S3" "docker exec gbx-e2e-s3 awslocal s3 ls s3://gearbox/e2e/ --recursive | grep -c envelopes/ >/dev/null"

echo "==> gearbox-sidecar (syncs from the server, downloads from S3)"
GEARBOX_SERVER_URL=http://127.0.0.1:18080 GEARBOX_APP=lead_service GEARBOX_ENV=dev \
GEARBOX_CREDENTIAL="$CRED" GEARBOX_TRUSTED_KEYS_FILE="$W/keys/trusted-keys.txt" \
GEARBOX_SOCKET="$W/run/engine.sock" GEARBOX_CACHE_DIR="$W/cache" GEARBOX_METRICS_ADDR=127.0.0.1:19464 \
GEARBOX_SIDECAR_ID=e2e-sidecar GEARBOX_LOG_SINK=server GEARBOX_LOG_SAMPLE=1 GEARBOX_POLL_INTERVAL=2s \
  bin/gearbox-sidecar >"$W/sidecar.log" 2>&1 &
pids+=($!); echo $! >> "$ROOT/.data/e2e.pids"
for _ in $(seq 1 60); do curl -fs 127.0.0.1:19464/readyz >/dev/null 2>&1 && break; sleep 0.5; done
check "sidecar ready (verified bundle loaded)" "curl -fs 127.0.0.1:19464/readyz >/dev/null"

echo "==> example service (Go client → sidecar)"
GEARBOX_TARGET="unix://$W/run/engine.sock" EXAMPLE_ADDR=127.0.0.1:18081 bin/go-service >"$W/service.log" 2>&1 &
pids+=($!); echo $! >> "$ROOT/.data/e2e.pids"
for _ in $(seq 1 40); do curl -fs -o /dev/null -XPOST 127.0.0.1:18081/route -d @examples/go-service/sample-lead.json 2>/dev/null && break; sleep 0.25; done
R=$(curl -fs -XPOST 127.0.0.1:18081/route -d @examples/go-service/sample-lead.json || echo '{}')
echo "    $R"
check "decision answered by the sidecar (no client fallback)" "echo '$R' | j 'd.get(\"fromClientFallback\") is False and bool(d.get(\"route\"))' | grep -q True"
V1=$(echo "$R" | j 'd.get("version")')

echo "==> remote Decision API agrees with the sidecar"
RR=$(curl -fs -XPOST $API/apps/lead_service/environments/dev/decide -H 'content-type: application/json' \
  -d "{\"decision\":\"lead_routing\",\"unitId\":\"L-1029384\",\"input\":$(cat examples/go-service/sample-lead.json)}")
check "same route from the remote API" "[ \"\$(echo '$RR' | j 'd[\"output\"][\"route\"]')\" = \"\$(echo '$R' | j 'd[\"route\"]')\" ]"

echo "==> schema registry rule"
code=$(curl -s -o "$W/no_schema.json" -w '%{http_code}' -XPOST $API/apps/lead_service/decisions -H 'content-type: application/json' \
  -d '{"name":"no_schema","spec":{"entry":"r","nodes":[{"id":"r","type":"rules","rules":[]}]}}')
check "rule node without schemas → 422 SCHEMA_REQUIRED" "[ $code = 422 ] && grep -q SCHEMA_REQUIRED $W/no_schema.json"

echo "==> publish a new version and watch the sidecar swap"
GEN0=$(curl -fs 127.0.0.1:19464/metrics | awk '/^gearbox_bundle_generation /{print $2}')
DEC=$(curl -fs $API/apps/lead_service/decisions/lead_routing)
REV=$(echo "$DEC" | j 'd["draft"]["revision"]')
SPEC=$(echo "$DEC" | python3 -c 'import json,sys; d=json.load(sys.stdin); s=d["draft"]["spec"]; s["fallback"]={"route":"manual_review","reason":"fallback_v2"}; print(json.dumps(s))')
check "save draft" "curl -fs -o /dev/null -XPUT $API/apps/lead_service/decisions/lead_routing/draft -H 'content-type: application/json' -d '{\"revision\":$REV,\"spec\":$SPEC}'"
REV2=$(curl -fs $API/apps/lead_service/decisions/lead_routing | j 'd["draft"]["revision"]')
NEWV=$(curl -fs -XPOST $API/apps/lead_service/decisions/lead_routing/versions -H 'content-type: application/json' -d "{\"revision\":$REV2,\"message\":\"e2e\"}" | j 'd["version"]')
check "cut version $NEWV" "[ -n '$NEWV' ] && [ '$NEWV' != 'None' ]"
REL=$(release_items "[{\"kind\":\"decisions\",\"name\":\"lead_routing\",\"version\":$NEWV}]")
check "release published (generation $REL)" "[ -n '$REL' ] && [ '$REL' != 'None' ]"
for _ in $(seq 1 40); do G=$(curl -fs 127.0.0.1:19464/metrics | awk '/^gearbox_bundle_generation /{print $2}'); [ "${G%.*}" = "$REL" ] && break; sleep 0.5; done
check "sidecar swapped to generation $REL (was $GEN0)" "[ \"\${G%.*}\" = '$REL' ]"
R2=$(curl -fs -XPOST 127.0.0.1:18081/route -d @examples/go-service/sample-lead.json || echo '{}')
check "service now answered by lead_routing@$NEWV" "echo '$R2' | j 'd.get(\"version\")' | grep -q '@$NEWV'"

echo "==> operations data reaches the control plane"
sleep 3
check "fleet shows the sidecar in sync" "curl -fs $API/apps/lead_service/environments/dev/sidecars | grep -q e2e-sidecar"
check "decision logs ingested" "curl -fs '$API/apps/lead_service/environments/dev/decision-logs?decision=lead_routing' | j 'len(d[\"items\"])>0' | grep -q True"
check "update logs ingested" "curl -fs $API/apps/lead_service/environments/dev/update-logs | j 'len(d[\"items\"])>0' | grep -q True"

echo "==> rollback"
RB=""
for _ in 1 2 3 4 5; do
  body=$(curl -s -XPOST $API/apps/lead_service/environments/dev/releases -H 'content-type: application/json' -H 'X-Gearbox-Dev-Role: approver' \
    -d "{\"type\":\"rollback\",\"toGeneration\":$((REL-1)),\"reason\":\"e2e\"}")
  RB=$(echo "$body" | j 'd.get("generation","")' 2>/dev/null || true)
  [ -n "$RB" ] && break; echo "    rollback retry: $body"; sleep 1
done
for _ in $(seq 1 40); do G=$(curl -fs 127.0.0.1:19464/metrics | awk '/^gearbox_bundle_generation /{print $2}'); [ "${G%.*}" = "$RB" ] && break; sleep 0.5; done
R3=$(curl -fs -XPOST 127.0.0.1:18081/route -d @examples/go-service/sample-lead.json || echo '{}')
check "after rollback the service is back on $V1" "echo '$R3' | j 'd.get(\"version\")' | grep -q '$V1'"


echo "==> control-plane changes reach the sidecar (checked with the stand-alone probe client)"
PUNE="$W/indore.json"
echo '{"lead":{"id":"L-77","city":"Indore","source":"web"},"car":{"make":"Hyundai","model":"i20","year":2021,"km":12000,"price":690000},"inspections":[{"part":"engine","score":6}]}' > "$PUNE"
check "probe: sidecar status ready" "$PROBE status | j 'd[\"Ready\"]' | grep -q True"
B=$($PROBE decide lead_routing "$PUNE" -unit L-77 -now 2026-10-30T10:00:00Z | j 'd["output"]["reason"]')
check "before: Indore lead is not blocked (reason=$B)" "[ '$B' != 'city_not_served' ]"

# 1a. a data change that breaks the decision's own test cases is refused (Pune is used by test "old car is rejected")
curl -fs -o /dev/null -XPUT $API/apps/lead_service/segments/blocked_cities/members -H 'content-type: application/json' -d '{"members":["Leh","Kargil","Port Blair","Pune"]}'
SREV=$(curl -fs $API/apps/lead_service/segments/blocked_cities | j 'd["draft"]["revision"]')
SVBAD=$(curl -fs -XPOST $API/apps/lead_service/segments/blocked_cities/versions -H 'content-type: application/json' -d "{\"revision\":$SREV}" | j 'd["version"]')
CODE=$(curl -s -XPOST $API/apps/lead_service/change-requests -H 'content-type: application/json' \
  -d "{\"environment\":\"dev\",\"title\":\"e2e bad\",\"items\":[{\"kind\":\"segments\",\"name\":\"blocked_cities\",\"version\":$SVBAD}]}" | j 'd.get("code")')
check "blocking Pune is refused: it breaks the decision's tests ($CODE)" "[ '$CODE' = 'TESTS_FAILED' ]"
# 1b. segment data: add Indore to blocked_cities → new version → release → sidecar rejects Indore
curl -fs -o /dev/null -XPUT $API/apps/lead_service/segments/blocked_cities/members -H 'content-type: application/json' -d '{"members":["Leh","Kargil","Port Blair","Indore"]}'
SREV=$(curl -fs $API/apps/lead_service/segments/blocked_cities | j 'd["draft"]["revision"]')
SV=$(curl -fs -XPOST $API/apps/lead_service/segments/blocked_cities/versions -H 'content-type: application/json' -d "{\"revision\":$SREV}" | j 'd["version"]')
G1=$(release_items "[{\"kind\":\"segments\",\"name\":\"blocked_cities\",\"version\":$SV}]")
check "segment change published (generation $G1) and sidecar swapped" "wait_gen $G1"
A=$($PROBE decide lead_routing "$PUNE" -unit L-77 -now 2026-10-30T10:00:00Z | j 'd["output"]["reason"]')
check "after: Indore lead blocked by the new segment version (reason=$A)" "[ '$A' = 'city_not_served' ]"

# 2. gate: create → release → probe true → kill switch → probe false
GV=$(create_and_cut gates '{"name":"e2e_gate","spec":{"enabled":true,"rules":[],"default":true}}')
G2=$(release_items "[{\"kind\":\"gates\",\"name\":\"e2e_gate\",\"version\":$GV}]")
check "gate published (generation $G2) and sidecar swapped" "wait_gen $G2"
check "probe: gate e2e_gate is on" "$PROBE gate e2e_gate -unit U1 | j 'd[\"Value\"]' | grep -q True"
G3=$(curl -fs -XPOST $API/apps/lead_service/environments/dev/releases -H 'content-type: application/json' -H 'X-Gearbox-Dev-Role: approver' \
  -d '{"type":"kill_switch","gate":"e2e_gate","killed":true,"reason":"e2e"}' | j 'd["generation"]')
check "kill switch published (generation $G3) and sidecar swapped" "wait_gen $G3"
check "probe: gate e2e_gate is off after the kill switch" "$PROBE gate e2e_gate -unit U1 | j 'd[\"Value\"]' | grep -q False"

# 3. experiment: create running 50/50 → release → every unit gets a stable variant
EV=$(create_and_cut experiments '{"name":"e2e_exp","spec":{"status":"running","salt":"e2e","variants":[{"name":"control","weight":50,"parameters":{"delay":60}},{"name":"fast","weight":50,"parameters":{"delay":5}}]}}')
G4=$(release_items "[{\"kind\":\"experiments\",\"name\":\"e2e_exp\",\"version\":$EV}]")
check "experiment published (generation $G4) and sidecar swapped" "wait_gen $G4"
VA=$($PROBE variant e2e_exp -unit U42 | j 'd["Name"]'); VB=$($PROBE variant e2e_exp -unit U42 | j 'd["Name"]')
check "probe: unit U42 gets a stable variant ($VA)" "[ -n '$VA' ] && [ '$VA' = '$VB' ]"
MIX=$(for i in $(seq 1 40); do $PROBE variant e2e_exp -unit "u$i" | j 'd["Name"]'; done | sort -u | wc -l)
check "probe: 40 units are split across both variants" "[ $MIX = 2 ]"

# 4. gradual rollout: new decision version at 50% → mixed versions by unit → 100% → all new
DEC=$(curl -fs $API/apps/lead_service/decisions/lead_routing); DREV=$(echo "$DEC" | j 'd["draft"]["revision"]')
DSPEC=$(echo "$DEC" | python3 -c 'import json,sys; d=json.load(sys.stdin); s=d["draft"]["spec"]; s["fallback"]={"route":"manual_review","reason":"fallback_v3"}; print(json.dumps(s))')
curl -fs -o /dev/null -XPUT $API/apps/lead_service/decisions/lead_routing/draft -H 'content-type: application/json' -d "{\"revision\":$DREV,\"spec\":$DSPEC}"
DREV=$(curl -fs $API/apps/lead_service/decisions/lead_routing | j 'd["draft"]["revision"]')
DV=$(curl -fs -XPOST $API/apps/lead_service/decisions/lead_routing/versions -H 'content-type: application/json' -d "{\"revision\":$DREV}" | j 'd["version"]')
G5=$(release_items "[{\"kind\":\"decisions\",\"name\":\"lead_routing\",\"version\":$DV,\"rollout\":{\"percent\":50}}]")
check "rollout at 50% published (generation $G5) and sidecar swapped" "wait_gen $G5"
VERS=$(for i in $(seq 1 40); do $PROBE decide lead_routing "$PUNE" -unit "lead$i" | j 'd["version"]'; done | sort | uniq -c)
echo "$VERS" | sed 's/^/      /'
NV=$(echo "$VERS" | grep -c lead_routing)
check "probe: 40 units are split between two versions" "[ $NV = 2 ]"
V7A=$($PROBE decide lead_routing "$PUNE" -unit lead7 | j 'd["version"]'); V7B=$($PROBE decide lead_routing "$PUNE" -unit lead7 | j 'd["version"]')
check "probe: the same unit always gets the same version ($V7A)" "[ '$V7A' = '$V7B' ]"
G6=$(curl -fs -XPOST $API/apps/lead_service/environments/dev/releases -H 'content-type: application/json' -H 'X-Gearbox-Dev-Role: approver' \
  -d '{"type":"rollout","decision":"lead_routing","percent":100}' | j 'd["generation"]')
check "rollout to 100% published (generation $G6) and sidecar swapped" "wait_gen $G6"
ALL=$(for i in $(seq 1 20); do $PROBE decide lead_routing "$PUNE" -unit "lead$i" | j 'd["version"]'; done | sort -u)
check "probe: every unit now on lead_routing@$DV" "[ '$ALL' = 'lead_routing@$DV' ]"

echo "==> client-side latency through the Go client (2,000 calls, informational)"
LAT=$($PROBE bench lead_routing "$PUNE" -n 2000); echo "$LAT" | tr -d '\n' | sed 's/^/      /'; echo
P99=$(echo "$LAT" | j 'int(d["p99Micros"])')
echo "  INFO  client p99 ${P99} µs with server, sidecar, MongoDB, LocalStack and logging (sample=1) sharing this machine; isolated figure: make bench-sidecar"

echo "==> resilience"
kill "${pids[0]}"; sleep 1
check "control plane down: sidecar still answers" "curl -fs -XPOST 127.0.0.1:18081/route -d @examples/go-service/sample-lead.json | j 'd.get(\"fromClientFallback\")' | grep -q False"
# restart the sidecar while the control plane is down: it serves the last-known-good bundle from disk
kill "${pids[1]}"; sleep 1
GEARBOX_SERVER_URL=http://127.0.0.1:18080 GEARBOX_APP=lead_service GEARBOX_ENV=dev \
GEARBOX_CREDENTIAL="$CRED" GEARBOX_TRUSTED_KEYS_FILE="$W/keys/trusted-keys.txt" \
GEARBOX_SOCKET="$W/run/engine.sock" GEARBOX_CACHE_DIR="$W/cache" GEARBOX_METRICS_ADDR=127.0.0.1:19464 \
GEARBOX_SIDECAR_ID=e2e-sidecar-2 GEARBOX_LOG_SINK=stdout \
  bin/gearbox-sidecar >"$W/sidecar2.log" 2>&1 &
pids+=($!); echo $! >> "$ROOT/.data/e2e.pids"
for _ in $(seq 1 40); do curl -fs 127.0.0.1:19464/readyz >/dev/null 2>&1 && break; sleep 0.5; done
check "sidecar restarted with control plane down: ready from disk cache" "curl -fs 127.0.0.1:19464/readyz >/dev/null"
RV=$($PROBE decide lead_routing "$PUNE" -unit lead1 | j 'd["version"]')
check "restarted sidecar still answers the latest version ($RV)" "[ '$RV' = 'lead_routing@$DV' ]"
kill "${pids[-1]}"; sleep 1
check "sidecar down: client fallback within deadline" "curl -fs -XPOST 127.0.0.1:18081/route -d @examples/go-service/sample-lead.json | j 'd.get(\"fromClientFallback\")' | grep -q True"

echo
echo "e2e: $pass passed, $fail failed  (logs in $W)"
[ "$fail" = 0 ]
