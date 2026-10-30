# 04 · gRPC API reference

Source of truth: [`proto/gearbox/v1/engine.proto`](proto/gearbox/v1/engine.proto). The sidecar serves `gearbox.v1.EngineService` and the standard `grpc.health.v1.Health` service on `unix:///var/run/gearbox/engine.sock`.

## Methods

| Method | Use it for | Typical latency (sidecar, measured) |
|---|---|---|
| `Decide` | One decision: rule set, table, transform or graph | ≈ 145 µs full call, ≈ 16–40 µs inside the sidecar |
| `DecideBatch` | Several decisions for the same request, one round trip | one hop + sum of evaluation times |
| `CheckGate` | Feature gate on/off for a unit | ≈ one hop |
| `GetVariant` | Experiment assignment (respects layers and holdouts) | ≈ one hop |
| `GetStatus` | Readiness, active generation, sync state, decision list | – |

## `Decide`

Request:

| Field | Type | Required | Notes |
|---|---|---|---|
| `decision` | string | yes | Name as published, e.g. `lead_routing` |
| `input_json` / `input_struct` | bytes / Struct | yes | The input document. JSON bytes are fastest from most languages |
| `request_id` | string | no | Echoed in logs and traces; generated if empty |
| `unit_id` | string | for rollouts/experiments | Stable ID used for hashing (user, lead, car). Empty: outside every partial rollout and experiment |
| `options.trace` | bool | no | Adds per-node timings to `path` |
| `options.pin_version` | string | no | Evaluate a specific version present in the bundle (replay) |
| `options.force_log` | bool | no | Always write a decision-log record for this call |
| `options.now` | Timestamp | no | Value of `ctx.now`; set it when replaying a logged call |

Response:

| Field | Notes |
|---|---|
| `output_json` | The decision's output document |
| `decision_version` | `name@vN`; log it with your business event |
| `dependency_hash` | Hash of the version plus every segment, lookup, gate and experiment it read; same hash + input + unit + `evaluated_at` → same output (AI nodes excepted) |
| `bundle_generation` | Generation of the bundle that answered |
| `path[]` | Executed nodes with outcome (matched rule, table row, switch case, `fallback`, `shadow`) |
| `fallback_used` | A fallback edge or the decision's fallback produced the output |
| `eval_micros` | Time inside the sidecar |
| `request_id` | The caller's ID, or the one the sidecar generated |
| `evaluated_at` | The `ctx.now` used |

Example, as grpcurl prints it (proto JSON mapping: `bytes` fields are base64, `int64` fields are strings):

```json
// request
{ "decision": "lead_routing", "unit_id": "L-1029384",
  "inputJson": "eyJsZWFkIjp7ImNpdHkiOiJHdXJnYW9uIn0sImNhciI6eyJ5ZWFyIjoyMDE5LCJwcmljZSI6NTI1MDAwfX0=" }

// response  (outputJson decodes to {"route":"fast_track","band":"A"})
{ "outputJson": "eyJyb3V0ZSI6ImZhc3RfdHJhY2siLCJiYW5kIjoiQSJ9",
  "decisionVersion": "lead_routing@v17", "dependencyHash": "sha256:5c0e…", "bundleGeneration": "42",
  "path": [ {"nodeId":"normalize","nodeType":"transform","outcome":"ok"},
            {"nodeId":"eligibility","nodeType":"rules","outcome":"r4"},
            {"nodeId":"route","nodeType":"switch","outcome":"eligible"},
            {"nodeId":"price_band","nodeType":"table","outcome":"row_2"},
            {"nodeId":"priority","nodeType":"ai","outcome":"shadow"},
            {"nodeId":"default_priority","nodeType":"rules","outcome":"r1"},
            {"nodeId":"output","nodeType":"output","outcome":"ok"} ],
  "evalMicros": "21", "requestId": "req_8c1f2a", "evaluatedAt": "2026-10-31T09:15:22.103Z" }
```

`fallbackUsed: false` is omitted because proto JSON leaves out default values.

## Errors

Standard gRPC codes; the status carries a `google.rpc.ErrorInfo` with `domain = "gearbox.dev"` and a `reason`.

| Code | `reason` | When | Client should |
|---|---|---|---|
| `NOT_FOUND` | `DECISION_NOT_FOUND` | Name not in the active bundle | Use fallback; fix the name or publish |
| `INVALID_ARGUMENT` | `INPUT_SCHEMA` | Input fails the decision's input schema (details list fields) | Fix the caller |
| `INVALID_ARGUMENT` | `INPUT_TOO_LARGE` | Input over the limit (default 256 KB) or an array over its schema `maxItems` | Send only needed fields |
| `FAILED_PRECONDITION` | `NOT_READY` | No verified bundle loaded yet | Use fallback; should not happen with native sidecars |
| `DEADLINE_EXCEEDED` | – | Client deadline passed | Use fallback |
| `INTERNAL` | `NODE_ERROR` | Expression failed at runtime and the decision has no fallback (`ErrorInfo.metadata.node`) | Use fallback; fix the decision |
| `UNAVAILABLE` | – | Sidecar restarting | Use fallback |

`DecideBatch` never fails as a whole for item errors; each failed item carries a `google.rpc.Status` with the same `ErrorInfo` reasons. Up to 100 items per call.

## Client rules

1. Reuse one channel per process; it multiplexes calls.
2. Set a deadline on every call (2–10 ms for non-AI decisions; AI decisions: node timeout + 5 ms).
3. Do not retry against the local sidecar. On any error, return the decision's fallback value and increment a counter.
4. Propagate `request_id` and the W3C `traceparent` header (gRPC metadata) so logs and traces join up.
5. Log `decision_version` with the business event that used the decision.

## Versioning

- `gearbox.v1` only gains fields and methods. Field numbers are never reused.
- `buf breaking` (FILE rules, so generated code stays source-compatible) runs on every pull request against `main`.
- A breaking change ships as `gearbox.v2` alongside v1 for at least two minor releases.
