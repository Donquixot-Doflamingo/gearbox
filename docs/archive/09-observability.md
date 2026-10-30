# 09 · Logs, metrics and traces

The sidecar records two logs, exposes Prometheus metrics and emits OpenTelemetry traces. Logging never slows a decision: records go into an in-memory ring buffer and ship in batches; if the buffer is full the record is dropped and counted.

## 1. Update log

One record per sync attempt that changes something or fails.

```json
{
  "ts": "2026-10-31T09:12:04.551Z", "type": "update",
  "app": "lead-service", "env": "prod", "pod": "lead-service-7d9f-abcde",
  "from_generation": 41, "to_generation": 42, "result": "swapped",
  "changed": [ {"kind": "decision", "name": "lead_routing", "from": "v16", "to": "v17", "sha256": "9f2c…"} ],
  "release_id": "rel_01JB…", "published_by": "asha@team", "approved_by": "ravi@team",
  "signature": "valid", "download_ms": 38, "compile_ms": 41, "tests": {"passed": 12, "failed": 0},
  "swap_at": "2026-10-31T09:12:04.640Z", "error": null
}
```

`result` is one of `swapped`, `kept_previous` (with `error`), `loaded_from_disk`, `up_to_date`.

## 2. Decision log

Sampling per decision (`spec.logging.sample`, default `GEARBOX_LOG_SAMPLE` = 1%); set `1.0` for audit decisions. `options.force_log` forces a record for one call.

```json
{
  "ts": "2026-10-31T09:15:22.103Z", "type": "decision",
  "request_id": "req_8c1…", "trace_id": "4bf9…", "app": "lead-service", "env": "prod", "pod": "lead-service-7d9f-abcde",
  "decision": "lead_routing", "decision_version": "lead_routing@v17", "dependency_hash": "sha256:5c0e…",
  "bundle_generation": 42, "kind": "graph", "unit_id": "L-1029384", "evaluated_at": "2026-10-31T09:15:22.103Z",
  "path": [ {"node_id": "eligibility", "node_type": "rules", "outcome": "r4"}, {"node_id": "price_band", "node_type": "table", "outcome": "row_2"} ],
  "fallback_used": false, "eval_micros": 21, "status": "OK",
  "input_hmac": "hmac:6a1e…", "input": {"car.year": 2019, "lead.city": "Gurgaon"},
  "output_hmac": "hmac:d03b…", "output": {"route": "fast_track"}
}
```

- Field names match the gRPC response (`decision_version`, `node_id`, `eval_micros`…), so a log record and a response can be compared directly.
- `input` / `output` contain only fields listed in `spec.logging.fields` (default none); everything else is represented by an HMAC keyed per installation (a plain hash of a phone number could be reversed by guessing).
- Fields marked `x-gearbox-pii` in the input schema, and values derived from them, are never logged, even if listed.
- With `evaluated_at`, `unit_id` and the full input (when logged), a call can be replayed with `pin_version` + `options.now`.

## 2b. Exposure log

One record per experiment assignment (`GetVariant` or an `experiment` node), **never sampled**: `ts, unit_id, experiment, experiment_version, variant, reason, app, request_id`. Experiment analysis reads this log only.

## 3. Metrics (Prometheus, `:9464/metrics`)

| Metric | Type | Labels |
|---|---|---|
| `gearbox_decisions_total` | counter | `decision`, `version`, `status`, `fallback` |
| `gearbox_decision_duration_seconds` | histogram | `decision`, `version` |
| `gearbox_node_errors_total` | counter | `decision`, `node` |
| `gearbox_bundle_generation` | gauge | – |
| `gearbox_sync_lag_seconds` | gauge | – (time since last successful sync check) |
| `gearbox_sync_failures_total` | counter | `reason` |
| `gearbox_swap_duration_seconds` | histogram | – |
| `gearbox_ai_calls_total` | counter | `decision`, `node`, `mode`, `result` |
| `gearbox_ai_duration_seconds` | histogram | `decision`, `node` |
| `gearbox_log_dropped_total` | counter | `log` (`decision`, `update`, `exposure`) |
| `gearbox_decision_breaker_open_total` | counter | `decision` |
| `gearbox_inflight_requests` | gauge | – |

The client libraries add `gearbox_client_duration_seconds` (histogram, labels `decision`, `outcome`) on the caller's side. **The 1 ms target is measured on this metric**, because it includes the socket hop and the client's own work; `gearbox_decision_duration_seconds` is evaluation time inside the sidecar only.

Suggested alerts: sync lag > 5 min; any `kept_previous` update; fallback rate > 1% for 5 min; client p99 > 1 ms for 10 min; any exposure-log drop; decision log drops > 0.1%; any breaker opening.

## 4. Traces

One span per `Decide` (`gearbox.decide`) with child spans per AI call. Attributes: `gearbox.decision`, `gearbox.version`, `gearbox.generation`, `gearbox.fallback`. The sidecar continues the caller's trace from the `traceparent` gRPC metadata.

## 5. Where logs go

`GEARBOX_OTLP_ENDPOINT` set: OTLP to an OpenTelemetry Collector, which routes to the team's store (ClickHouse, Loki, Elasticsearch, a Kafka topic). Not set: JSON lines on stdout. The control plane's log search screen reads from that store through a small adapter interface.
