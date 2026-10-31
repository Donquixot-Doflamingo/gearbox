#!/usr/bin/env bash
# Make a real control-plane change through the API (the same calls the UI makes):
#   scripts/demo/change.sh block   <City>   # add a city to the blocked_cities segment and publish to dev
#   scripts/demo/change.sh unblock <City>
# Steps: replace segment members → cut a version → open a change request → approve → publish.
set -euo pipefail
API="${GEARBOX_API:-http://127.0.0.1:8080/api/v1}"; APP=lead_service; ENV="${GEARBOX_ENV:-dev}"
op="${1:?block|unblock}"; city="${2:?city}"
j() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }
members=$(curl -fs "$API/apps/$APP/segments/blocked_cities/members?limit=200" | j 'd["items"]')
new=$(python3 -c "import json,sys; m=$members; c=sys.argv[2]
m=[x for x in m if x!=c]+([c] if sys.argv[1]=='block' else []); print(json.dumps({'members':m}))" "$op" "$city")
curl -fs -o /dev/null -XPUT "$API/apps/$APP/segments/blocked_cities/members" -H 'content-type: application/json' -d "$new"
rev=$(curl -fs "$API/apps/$APP/segments/blocked_cities" | j 'd["draft"]["revision"]')
ver=$(curl -fs -XPOST "$API/apps/$APP/segments/blocked_cities/versions" -H 'content-type: application/json' -d "{\"revision\":$rev,\"message\":\"$op $city\"}" | j 'd["version"]')
cr=$(curl -fs -XPOST "$API/apps/$APP/change-requests" -H 'content-type: application/json' \
  -d "{\"environment\":\"$ENV\",\"title\":\"$op $city\",\"items\":[{\"kind\":\"segments\",\"name\":\"blocked_cities\",\"version\":$ver}]}" | j 'd["id"]')
curl -fs -o /dev/null -XPOST "$API/apps/$APP/change-requests/$cr/approve" -H 'X-Gearbox-Dev-User: approver@example.com' -H 'X-Gearbox-Dev-Role: approver' -H 'content-type: application/json' -d '{}' || true
gen=$(curl -fs -XPOST "$API/apps/$APP/environments/$ENV/releases" -H 'content-type: application/json' -d "{\"type\":\"change_request\",\"changeRequestId\":\"$cr\"}" | j 'd["generation"]')
echo "$(date +%T) published generation $gen: blocked_cities v$ver ($op $city)"
