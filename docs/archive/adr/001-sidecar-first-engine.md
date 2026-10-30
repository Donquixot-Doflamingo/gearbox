# ADR 001 · Sidecar-first decision engine (Gearbox)

Date: 2026-10-30 · Status: Accepted · Amended: 2026-10-31

## Decision

Every service gets a Gearbox sidecar (one Go binary) in its pod. The sidecar:

- detects changes (push change feed + 30 s poll fallback), fetches only changed parts of its app's signed bundle, compiles and tests it off the request path, then atomically swaps the active version;
- evaluates everything: rule sets, decision tables, transforms (Expr), decision trees/graphs, gates/experiments, and AI nodes (through the Integration Hub);
- logs every update and which version answered each call.

Apps in any language (Java, Go, Python, C++) talk to it only over gRPC on a Unix socket: input in, output + version out. Client libraries are thin (connection, deadline, fallback value, request ID); teams can generate their own stub from `engine.proto`.

The engine stays a Go package inside the sidecar. Embedding it directly in a Go service is possible later but is not the default.

## Why

- One engine implementation, so every language gets identical answers; no per-language engine ports.
- One place for sync, versioning, logging and metrics.
- Rule changes are live; only engine upgrades need a rollout.

## Measured (2 vCPU sandbox, Go 1.24.7, 585-byte JSON payload, 12-field transform)

- Engine work: rule 1.0 µs, transform 15.8 µs, decode + run + encode 39 µs.
- Full call, grpc-go 1.67.1: 144.5 µs over a Unix socket, 172 µs over loopback TCP.
- Throughput: ~15k decisions/s through one sidecar with 8 concurrent callers (caller shared the 2 vCPUs).

## Costs accepted

- ~0.15 ms per call instead of ~0.02 ms in-process. Mitigation: `DecideBatch`, send only fields the input schema needs.
- A copy of the app's bundle in every pod. Mitigation: per-app bundles, size caps; memory ≈ 2× decompressed bundle during a swap.
- Sidecar down = no decisions for that pod. Mitigation: Kubernetes native sidecar, auto-restart, per-decision fallback in the client.

## Amendments (2026-10-31, after independent review)

- Kubernetes 1.29+ (native sidecars on by default from 1.29, stable in 1.33).
- Sidecars download bundle bytes from the object store (presigned URLs or CDN), not from the API, so new pods can start while the API is down.
- Signed envelope over exact manifest bytes, trusted keys with `key_id`, rotation, monotonic generations, `min_engine_version`.
- Determinism rules: caller-settable `ctx.now`, dependency hash in every response, sorted map builtins, nil-safe comparisons, defined empty-unit behaviour. AI nodes are excluded from replay.
- Details: [02 Architecture](../02-architecture.md) §3–§6.
