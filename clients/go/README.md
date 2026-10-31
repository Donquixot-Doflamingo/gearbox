# clients/go — Go client for the Gearbox sidecar

Module `github.com/ORG/gearbox/clients/go`, package `gearbox`. A thin wrapper
over the generated `gearbox.v1` stubs that adds a deadline on every call, a
fallback value, request IDs, W3C `traceparent` propagation and a latency hook.
No Prometheus or OpenTelemetry dependency.

```go
client, err := gearbox.Dial(ctx, os.Getenv("GEARBOX_TARGET")) // unix:///var/run/gearbox/engine.sock
if err != nil { return err }
defer client.Close()

d, err := client.Decide(ctx, "lead_routing", lead,
    gearbox.WithUnit(lead.ID),
    gearbox.WithTimeout(5*time.Millisecond),
    gearbox.WithFallback(map[string]any{"route": "manual_review"}))
// d.Output["route"], d.Version ("lead_routing@17"), d.FallbackUsed, d.FromClientFallback
log.Info("routed", "decision_version", d.Version, "dependency_hash", d.DependencyHash)
```

## API

- `Dial(ctx, target, ...DialOption)` — `unix:///path` or `host:port` (loopback TCP).
  Options: `WithToken` (TCP bearer token), `WithObserver(func(decision, d, outcome))`,
  `WithDefaultTimeout`, `WithWaitReady` (wait for `GetStatus.ready`), `WithGRPCOptions`.
- `Decide(ctx, decision, input, ...CallOption) (Decision, error)` — input is any
  JSON-encodable value, or `json.RawMessage`/`[]byte`. `Decision{Output, Raw, Version,
  DependencyHash, Generation, FallbackUsed, FromClientFallback, RequestID, EvalMicros,
  EvaluatedAt, Path, Err}`. `Output` is nil when the output is not a JSON object (use `Raw`).
- Call options: `WithUnit`, `WithTimeout` (default 5 ms), `WithFallback(v)` (any error →
  `v`, `FromClientFallback=true`, nil error, cause in `Decision.Err`), `WithRequestID`,
  `WithTrace`, `WithForceLog`, `WithPinVersion`, `WithNow`.
- `DecideBatch(ctx, []BatchItem)` (≤ 100, items fail independently),
  `CheckGate(ctx, gate, unit, attrs)` (`WithFallback(bool)`),
  `GetVariant(ctx, experiment, unit, attrs)` (`WithFallback("control")`),
  `Status(ctx)`, `Close()`.
- Errors are `*gearbox.Error{Code, Reason, Message, Node, Fields}`; branch on
  `Reason` (`gearbox.IsReason(err, "DECISION_NOT_FOUND")`).
- Trace context: `gearbox.WithTraceparent(ctx, tp)`, or incoming gRPC metadata
  `traceparent` is forwarded automatically.
- Observer outcomes: `ok`, `fallback` (sidecar used the decision fallback),
  `client_fallback`, `error`. Feed it into `gearbox_client_duration_seconds`.

## Decisions

- The client never retries against the local sidecar (doc 04 client rules).
- Default timeout 5 ms; AI decisions need the node timeout plus a few ms.
- The latency hook is a plain function so the client stays dependency-light.

## Tests

`gearbox_test.go` runs against an in-process sidecar (`sidecar/sidecartest`)
over a real Unix socket and over loopback TCP with a bearer token.
`BenchmarkDecideOverUnixSocket`: ≈126 µs per full call on a 2 vCPU sandbox.
