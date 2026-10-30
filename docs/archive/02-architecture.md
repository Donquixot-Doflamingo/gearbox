# 02 · Architecture

## 1. Requirements

### Functional

- Evaluate rule sets, decision tables, decision graphs (trees), JSON transforms, feature gates, experiments, layers and holdouts.
- Call an AI model (through the Integration Hub) as one step of a decision, with a fallback.
- Author, test, review, approve, publish, promote and roll back decisions per environment.
- Tell every running sidecar about a new version within seconds; record which version answered every call.

### Non-functional

| Property | Target |
|---|---|
| Decision latency, full gRPC call to the local sidecar, non-AI, measured by the client | p99 < 1 ms at the reference load: 2,000 decisions/s per pod, sidecar CPU request 500m (measured p50 ≈ 0.15 ms in a 2 vCPU sandbox) |
| Change propagation (publish → all connected sidecars swapped) | p95 < 5 s via the change feed; ≤ 30 s via polling only |
| Availability of decisions | Running sidecars never depend on the control plane. New pods need the object store (not the API) or a baked-in bundle |
| Reproducibility | Same `dependency_hash`, input, `unit_id` and `evaluated_at` → same output, on every pod and every engine version that accepts the bundle. AI nodes are excluded (see §6) |
| Isolation | A sidecar can read only its own app's bundles for its own environment |

### Constraints

- Callers are written in Go, Java, Python and C++; one engine implementation (Go) serves all of them.
- Runs on Kubernetes 1.29+ (native sidecars are on by default from 1.29 and stable in 1.33); older clusters and VMs run the same binary as a regular container or local daemon.
- MongoDB is the source of truth for definitions and history; YAML files are an import/export format (`gearboxctl`), not a second source of truth.

## 2. Components

```
                      ┌──────────────────────── Control plane ────────────────────────┐
 Admin UI (SvelteKit)─┤ Admin API (Connect)  ─ Store (MongoDB) ─ Approvals ─ Audit    │
 gearboxctl / CI     ─┤ Publisher: compile → test → build bundle → sign → upload       │
                      │ Change feed (gRPC stream)   Fleet status · Log search          │
                      └───────────────┬───────────────────────────────┬───────────────┘
                     signed manifest, │ resources by hash             │ "gen 42 ready"
                     head pointer     ▼                               │
                      ┌──────────────────────────────┐                │
                      │ Object store / CDN (S3 API)  │                │
                      └──────────────┬───────────────┘                │
                    ┌────────────────▼────────────────────────────────▼─────────────────┐
  Pod ──────────────┤ Gearbox sidecar                                                   │
  ┌──────────────┐  │  gRPC server (Unix socket) → Evaluator → Active Snapshot (gen 42) │
  │ Your service │◄─┤  Sync loop → verify → compile → test → atomic swap → disk copy    │
  │ thin client  │─►│  Log buffer → OTLP exporter      Hub client → Integration Hub (AI)│
  └──────────────┘  └───────────────────────────────────────────────────────────────────┘
```

| Component | Responsibility | State |
|---|---|---|
| Engine (`engine/`) | Compile definitions into an immutable `Snapshot`; evaluate | none (pure) |
| Sidecar | Serve `EngineService`; sync, verify, compile, swap; log; call the Hub | active Snapshot in memory, last-good bundle on local disk |
| Admin API | CRUD, versions, diff, test-evaluate, change requests, releases, RBAC | MongoDB |
| Publisher | Turn an approved release into a signed bundle, upload it, move the head pointer | object store, MongoDB |
| Change feed | Push `{app, env, generation}` to watching sidecars | Redis pub/sub for fan-out |
| Object store | Signed manifests, head pointers, content-addressed resources | S3-compatible, optionally behind a CDN |
| Fleet status | Sidecars report generation, engine version, errors every 30 s | MongoDB (TTL) |
| Log search | Query decision logs by request ID, decision, version | Log backend (ClickHouse, Loki or similar, via OpenTelemetry) |
| Remote Decision API | Same engine behind HTTP/gRPC for callers that cannot run a sidecar | stateless replicas that sync like sidecars |

## 3. Data flow

### Request path (pure CPU unless the decision has an inline AI node)

1. Client sends `Decide{decision, input_json, request_id, unit_id}` over the Unix socket.
2. Sidecar loads the active Snapshot with one atomic pointer read (no locks).
3. Evaluator walks the compiled plan for that decision: each node runs pre-compiled Expr bytecode.
4. Response carries `output_json`, `decision_version`, `dependency_hash`, `bundle_generation`, node path, `fallback_used`, `evaluated_at`.
5. A log record goes into the ring buffer (non-blocking); counters and histograms update.

### Update path (background)

1. Author creates a change request → approval (if the environment requires it) → Publisher compiles and runs every test case for the environment.
2. Publisher uploads new resources (content-addressed, `sha256/<hash>`), writes the signed manifest envelope, then the head pointer `heads/<app>/<env>` (generation 42), then publishes `{app, env, 42}` on Redis.
3. Change feed pushes the event to every sidecar watching that app and environment. Sidecars without a feed connection read the head pointer every 30 s (ETag).
4. Sidecar waits a random 0–2 s (spreads load), downloads the manifest envelope and only the resources whose hashes it lacks, **directly from the object store** using short-lived presigned URLs issued with the feed event (or a CDN). The API pods never stream bundle bytes.
5. Sidecar checks, in order: signature with a trusted key, `generation` greater than the active one, `min_engine_version` ≤ its own, every resource hash, decompressed size. Then it compiles, runs the bundle's test cases, swaps the Snapshot pointer, writes the bundle to disk, writes an update log record and reports status.

If any check in step 5 fails, the sidecar keeps generation 41, logs the reason and reports it; the UI shows the failure in Fleet.

## 4. Storage

| Data | Store | Why |
|---|---|---|
| Decisions, versions, change requests, releases, audit, apps, credentials (hashed) | MongoDB | Source of truth; document model fits versioned definitions |
| Large data: segment members, lookup rows | Object store, referenced by hash from MongoDB | Avoids MongoDB's 16 MB document limit; shared with bundles |
| Bundles, manifests, head pointers | Object store | Immutable, cheap, cacheable; sidecars never touch MongoDB or the API for bytes |
| Feed fan-out | Redis pub/sub | Small, already deployed; replaceable by NATS |
| Fleet heartbeats | MongoDB with TTL index | Low volume |
| Decision, update and exposure logs, traces | OpenTelemetry Collector → team's backend | Vendor-neutral |

### Bundle format (`bundle.proto`)

- **Envelope** (what is signed): `SignedManifest { bytes manifest; bytes signature; string key_id; }`. The signature covers the exact `manifest` bytes as stored, so no canonical protobuf encoding is needed.
- **Manifest**: `{ app, environment, generation, created_at, publisher, release_id, min_engine_version, resources: [{kind, name, version, sha256, size, decompressed_size}] }`.
- **Resources**: `Decision`, `Gate`, `Experiment`, `Layer`, `Holdout`, `Segment`, `LookupTable`, `TestSuite`; each protobuf, zstd-compressed, addressed by SHA-256.
- **Keys**: Ed25519. The private key stays in a KMS or secret manager. Sidecars get a list of trusted public keys (`GEARBOX_TRUSTED_KEYS`, several allowed so keys can rotate: add new → publish with new → remove old). The disk copy is re-verified on every load.
- **Limits** (enforced at publish): ≤ 64 MB compressed and ≤ 256 MB decompressed per bundle, ≤ 5,000 decisions, segment ≤ 1 M members. All configurable per app.

## 5. Errors, retries and failure modes

| Failure | Behaviour |
|---|---|
| Control plane down | Running sidecars keep serving; feed reconnects with backoff (1 s → 60 s, jitter); head-pointer polling continues against the object store |
| New pod starts while the control plane is down | Starts normally if the object store is reachable (it does not need the API). If both are down and the cache volume is empty, the sidecar is not ready unless a bundle was baked into the image or mounted (`--bundle-file`) |
| Bundle invalid, signature wrong, generation not newer, engine too old, tests fail | Keep current generation; update log + `gearbox_sync_failures_total{reason}`; Fleet shows the error |
| Sidecar restart | Loads last-good bundle from disk after re-verifying it (seconds), then syncs |
| Sidecar down | Kubernetes restarts it; clients return the decision's fallback value within their deadline and count it |
| Unknown decision | `NOT_FOUND` |
| Input fails schema or size limits | `INVALID_ARGUMENT` with field errors |
| Expression runtime error | Node error → `onError` edge, else decision `fallback`, else `INTERNAL` with node ID |
| Decision too slow repeatedly (see [05](05-rules-and-expressions.md) §8) | Per-decision circuit breaker returns the fallback for 30 s and alerts |
| AI node timeout or invalid answer | Fallback edge, `fallback_used=true` |
| Log buffer full | Drop, count `gearbox_log_dropped_total`; never block a call |

Clients do not retry against the local sidecar in a loop: one attempt within the deadline, then fallback.

## 6. Determinism

The same answer on every pod is the main reason for one engine. The rules that keep it:

- **Versions cover dependencies.** `dependency_hash` = hash of the decision version and every segment, lookup, gate and experiment version it reads. `pin_version` replays with the dependencies recorded at that version.
- **Time is an input.** `now()` is disabled in expressions; `ctx.now` comes from `DecideOptions.now` or the sidecar clock and is echoed as `evaluated_at`. Test cases may set `ctx.now` and `unit`.
- **No unordered output.** Expr's `keys`, `values` and `toPairs` return map order, which is random; Gearbox replaces them with sorted versions. Maps are serialised with sorted keys.
- **Hashing is fixed.** murmur3 with per-resource salts, bucket 0–9999; empty `unit_id` is defined as "outside every partial rollout and experiment".
- **Engine versions are pinned to bundles.** Every bundle states `min_engine_version`; a sidecar that is older refuses it and keeps the previous generation. Changes to evaluation semantics bump that number and run the parity suite against both versions.
- **AI nodes are not reproducible.** The Hub may change the concrete model behind an alias and caches are per pod. The decision log records the concrete model and an answer hash so the AI part can be audited, not replayed.

## 7. Scale

- Measured cost ≈ 0.065 vCPU-ms per decision (sidecar side, 585-byte input), so 1 vCPU ≈ 15k decisions/s. A 500m request covers the 2,000/s reference load with headroom; size from the pod's peak decision rate.
- Set `GOMAXPROCS` to the sidecar's CPU request (the Helm chart passes it through the downward API). No CPU limit by default: throttling is the main tail-latency risk.
- Memory per sidecar ≈ 2 × decompressed bundle during a swap + 32 MB baseline + log buffer. The 256 MB decompressed cap keeps the worst case under 600 MB; typical bundles are a few MB.
- Control plane load is proportional to publishes and connected sidecars, not to decisions. 10k feed connections is a few hundred MB on the feed servers. Bundle bytes come from the object store or a CDN.

## 8. Security

- App → sidecar: Unix socket inside the pod, mode `0660`, group set by the pod's `fsGroup`; optional static token for loopback TCP.
- Sidecar → control plane: app credential scoped to tenant + app + environment (hashed at rest, rotated); TLS; workload identity (Kubernetes service account token exchange) later.
- Bundles are signed (§4); the sidecar refuses unsigned bundles in every environment unless started with `--allow-unsigned`, which is only allowed together with `--bundle-file` or `--definitions-dir` (local development).
- Admin: OIDC sign-in, role-based access, two-person approval for production releases, full audit.
- PII: fields marked `x-gearbox-pii` in the input schema are tracked through transforms; they never reach logs or AI prompts. Segment members that are PII are stored as HMACs. Details in [08](08-ai-node.md) and [09](09-observability.md).
- AI: Hub token only in the sidecar; no model keys in sidecars or apps.

## 9. Trade-offs we accepted

| Choice | We gain | We pay | Revisit when |
|---|---|---|---|
| Sidecar for every language | One engine, identical answers, central logs | ~0.15 ms per call, memory per pod | A Go hot path needs < 50 µs: embed `engine/` with the sidecar's `sync` package as a library (optional mode, same bundles, same logs) |
| Expr as the expression language | Fastest Go option measured, typed, safe | Go-only (fine with a sidecar); no cancellation inside one evaluation | Never likely |
| Bundles via object store | Control plane off the hot path, cheap fan-out, new pods start without the API | Publishing latency of a few seconds | Sub-second propagation becomes a requirement |
| MongoDB kept | No data migration | Two stores (Mongo + object store) | Team prefers PostgreSQL |
| Push feed + poll fallback | Fast and robust | Long-lived connections to manage | Connection count becomes a problem (add a relay per cluster) |
