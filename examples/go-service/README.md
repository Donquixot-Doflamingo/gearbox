# Example: Go service calling the sidecar

A small HTTP service (`POST /route`) that asks the local Gearbox sidecar for `lead_routing` with a 5 ms deadline and a client-side fallback, and logs the version and dependency hash with every routed lead.

```bash
# 1. a sidecar serving the example definitions (no control plane needed)
mkdir -p .data/run
GEARBOX_SOCKET=$PWD/.data/run/engine.sock \
  go run ./sidecar/cmd/gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned
# 2. this service
cd examples/go-service
GEARBOX_TARGET=unix://$PWD/../../.data/run/engine.sock go run .
# 3. a lead
curl -s -XPOST localhost:8081/route -d @sample-lead.json
# {"route":"standard","reason":"default","version":"lead_routing@1",...,"fallbackUsed":false,"fromClientFallback":false}
```

Stop the sidecar and call again: the answer is `manual_review` with `fromClientFallback: true` within 5 ms.

This is its own Go module with `replace` directives to `clients/go` and `gen/go`, so it builds once the Go client exists. `make dev` and `deploy/compose` run it for you.
