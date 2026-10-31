# 03 · Implementation guide

For people building and operating Gearbox. Exact settings, RPC fields, operators, error codes and metric names are in [04 Reference](04-reference.md).

## 1. Architecture

```
                 ┌──────────────────────────── Control plane (gearbox-server, Go) ────────────────────────────┐
 Admin UI ───────┤ HTTP/JSON API (OpenAPI)  auth (OIDC, PAT, app credentials) · RBAC · CSRF                   │
 (SvelteKit)     │ Store: memory | MongoDB    drafts · versions · change requests · releases · audit          │
 gearboxctl/CI ──┤ Validate/test (engine pkg) Publisher: build → test → benchmark → sign → upload → notify    │
                 │ Feed (SSE /sidecar/v1/watch, Redis fan-out)  Fleet · Log store · Webhooks · Remote Decide   │
                 └──────────┬───────────────────────────────────────────────▲──────────────────▲──────────────┘
                 blobs, envelopes, heads                                    │ head, watch,      │ logs (optional)
                            ▼                                               │ heartbeat         │
                 ┌──────────────────────────┐                               │                   │
                 │ Object store: S3 (prod)  │◀── presigned GET of bytes ──┐ │                   │
                 │ filesystem (local dev)   │                             │ │                   │
                 └──────────────────────────┘                             │ │                   │
      Pod ─────────────────────────────────────────────────────────────────┼─┼───────────────────┼────
      ┌──────────────┐ gRPC, Unix socket  ┌────────────────────────────────┴─┴───────────────────┴──┐
      │ Your service │───────────────────▶│ gearbox-sidecar: EngineService → active Snapshot (gen N) │
      │ thin client  │◀───────────────────│ sync → verify → compile → test → atomic swap → disk copy │
      └──────────────┘                    │ log ring buffer → stdout | server | OTLP · Hub client ───┼──▶ Integration Hub (AI)
                                          └──────────────────────────────────────────────────────────┘
```

| Component | Job | State |
|---|---|---|
| Engine (`engine/`) | Validate and compile definitions to an immutable `Snapshot`; evaluate; run tests | none (pure, no I/O) |
| Bundle (`engine/bundle`) | Build, sign, verify, decode bundles | none |
| Sidecar | Serve `EngineService`; sync; verify; swap; logs; metrics; Hub calls | active snapshot in memory, last-known-good on disk |
| Control plane | HTTP API, RBAC, store, publisher, feed, fleet, logs, audit, webhooks, Remote Decision API | store (memory or MongoDB), object store, Redis (fan-out) |
| Admin UI | Authoring, review, operations | none (served static, talks to the API) |
| `gearboxctl` | CLI over the same API; local tests with the engine; offline bundles; dev keys | local token |
| Object store | Content-addressed blobs, signed envelopes, head pointers | S3-compatible, filesystem in dev |

**Request path** (pure CPU unless an inline AI node runs): client → Unix socket → one atomic pointer read of the snapshot → compiled plan → response with version, dependency hash, generation and path → non-blocking log record.

**Update path**: release → publisher builds and signs → uploads blobs → envelope → head → feed event → sidecars fetch head from the API and **bytes from the object store** → verify → compile → test → swap.

### Failure modes

| Failure | Behaviour |
|---|---|
| Control plane down | Running sidecars keep serving; watch reconnects with backoff (1 s → 60 s, jitter); polling continues |
| New pod while the control plane is down | Ready only from a cached bundle (same node, cache volume) or `GEARBOX_FALLBACK_BUNDLE`; otherwise not ready until the API returns |
| Object store down | No new generations reach sidecars; running sidecars keep serving |
| Bad bundle (signature, generation, engine version, hash, size, compile, tests) | Keep the current generation; `kept_previous` update record; `gearbox_sync_failures_total{reason}`; Fleet shows it |
| Sidecar restart | Loads the cached bundle after re-verifying it, then syncs |
| Sidecar down | Kubernetes restarts it; clients return their fallback within the deadline and count it |
| Unknown decision / bad input | `NOT_FOUND` / `INVALID_ARGUMENT` with field errors |
| Expression error at runtime | `onError` → decision `fallback` → `INTERNAL`/`NODE_ERROR` with the node ID |
| Decision repeatedly slower than 10× budget | Breaker returns the fallback for 30 s and counts it |
| AI timeout, Hub error, invalid answer | `onError`, `fallback_used: true` |
| Log buffer full | Record dropped and counted; the call is never blocked |

Clients never retry against the local sidecar: one attempt within the deadline, then the fallback.

### Admin UI screens

| Route | Screen |
|---|---|
| `/` | Overview: generation, p99, call rate, fallback rate, fleet health, recent releases, pending change requests |
| `/apps/[app]/schemas` | Schema registry: editor, examples, compatibility, usages |
| `/apps/[app]/decisions`, `…/[name]` | Decision list; graph editor with node inspector, rules editor and condition builder, table grid, transform mapper, AI node editor; test console and test cases |
| `/apps/[app]/segments`, `/lookups`, `/gates`, `/experiments`, `/layers` | Data and targeting resources |
| `/apps/[app]/changes`, `…/[id]` | Change requests: diff, tests, approvals, comments |
| `/apps/[app]/releases` | Release history, rollout stepper, rollback, kill switch |
| `/apps/[app]/fleet`, `/logs`, `/audit` | Sidecars, decision/update/exposure logs with path trace, audit |
| `/settings/*` | Apps, environments and policies, credentials, members, webhooks, tokens |

App and environment live in the URL (`?env=prod`) so every link is shareable. A clickable prototype is in `ui/prototype/index.html`.

## 2. Repository layout and ownership

```
gearbox/
├── CONTRACTS.md  AGENTS.md  go.work  Makefile  README.md
├── contracts/openapi/        gearbox-admin.v1.yaml · integration-hub-ai.yaml (placeholder)
├── proto/gearbox/v1/         engine.proto (sidecar API) · bundle.proto (bundle format)
├── gen/go/                   generated Go code, module github.com/ORG/gearbox/gen/go
├── engine/                   module …/engine: api.go (contract), bundle/ (contract), model, expr, rules, table, transform, graph, experiments, cost, pii
├── sidecar/                  module …/sidecar: cmd/gearbox-sidecar, internal/{server,sync,store,swap,logs,metrics,hub,health,config}
├── controlplane/             module …/controlplane: cmd/gearbox-server, cmd/gearboxctl, internal/{api,auth,rbac,store,publish,feed,objectstore,fleet,logs,audit,webhook,runtime}
├── clients/go/               module …/clients/go (package gearbox): Dial, Decide, DecideBatch, CheckGate, GetVariant
├── clients/java/  clients/python/   thin wrappers; stubs generated from engine.proto
├── ui/                       SvelteKit app (build → ui/build)
├── examples/                 definitions/ (lead_service YAML) · go-service/
├── deploy/                   docker/ · compose/ · helm/gearbox-sidecar · helm/gearbox-server · local/
├── tickets/                  TICKETS.md
├── scripts/                  check-names.sh · dev/restricted-env.sh
└── docs/
```

| Contract | File | Owner | Users |
|---|---|---|---|
| Control-plane API + definition model | `contracts/openapi/gearbox-admin.v1.yaml` | lead | control plane, UI, CLI, sidecar (`/sidecar/v1/*`), engine (definition JSON) |
| Sidecar gRPC API | `proto/gearbox/v1/engine.proto` | lead | sidecar, all clients |
| Bundle format | `proto/gearbox/v1/bundle.proto`, `engine/bundle/api.go` | lead | control plane (writer), sidecar (reader) |
| Engine Go API | `engine/api.go` | lead | sidecar, control plane |
| AI call | `contracts/openapi/integration-hub-ai.yaml` | Hub owner (placeholder) | sidecar `internal/hub` only |

Change process: write the request in `contracts/CHANGES-REQUESTED.md` on your branch (what, why, who is affected); the lead merges contract changes. Code follows the contracts; the docs follow the code and the contracts.

Module dependency rule: `engine` imports nothing from `sidecar`, `controlplane` or `clients`; `sidecar` and `controlplane` import `engine` and `gen/go`; `clients/go` imports only `gen/go`.

## 3. Engine semantics

### 3.1 One definition model

Definitions are plain JSON matching `components/schemas` in the OpenAPI file (`SchemaSpec`, `DecisionSpec`, `SegmentSpec`, `LookupSpec`, `GateSpec`, `ExperimentSpec`, `LayerSpec`). The control plane stores them without interpreting them; the engine validates and compiles them; the UI edits them; bundles carry them verbatim (`ResourceBlob.spec_json`). There is no second format: YAML files are the same JSON written as YAML.

### 3.2 Schema validation

| When | What is checked | Failure |
|---|---|---|
| Save/validate | `rules`/`table` nodes have registered `inputSchema` and `outputSchema` | `SCHEMA_REQUIRED`, `SCHEMA_NOT_FOUND` |
| Save/validate | Field paths and expression types against the input schema; optional fields typed nullable | `UNKNOWN_FIELD`, `TYPE_MISMATCH` |
| Save/validate | Literal `then`/`default`/`fallback` against the output schema | `TYPE_MISMATCH` |
| Save/validate | Transform `outputSchema` accepted by the next node's `inputSchema` | `SCHEMA_EDGE_MISMATCH` |
| Runtime | Node input against `inputSchema` before the node runs | entry node: `INVALID_ARGUMENT/INPUT_SCHEMA`; inner: `onError` → fallback |
| Runtime | `=` expressions in data slots and the output against `output.schema` | node error → `onError` → fallback |
| Runtime | Arrays longer than `maxItems` | `INVALID_ARGUMENT/INPUT_TOO_LARGE` |

The decision-level `inputSchema` is optional; when the entry node is a `rules`/`table` node, the request is checked against that node's `inputSchema`.

### 3.3 Conditions

- Structured conditions compile to the same Expr program form as `{expr}` conditions, so semantics are identical.
- **Missing fields**: every `a.b.c` compiles as optional access (`a?.b?.c`). A missing or `null` value makes every positive and negative operator false; only `NOT_EXISTS` is true. `==` against `nil` compares normally in free Expr. Arithmetic on `nil` is a node error.
- Numeric comparisons parse numeric strings; regexes are RE2 and compiled at validation.
- Segment leaves: static → member lookup of `key` (HMAC'd first when the segment is `pii`); dynamic → evaluate the segment's condition over `attributes` (validated against `attributesSchema`); rollout → `include` (if any) then bucket of `key` (default `ctx.unit`).
- Gate and variant leaves evaluate for `ctx.unit` and record exposures for variants.

### 3.4 Data slots vs code slots

Code slots are always Expr; data slots are literal JSON where a string starting with `=` is an expression and `==` escapes a literal `=` (list in [01 §16](01-features.md#16-data-slots-and-code-slots-applies-everywhere)). The compiler records which data values are expressions so literal values are type-checked at save and only `=` values at runtime.

### 3.5 Determinism

| Rule | Mechanism |
|---|---|
| Time is an input | `now()` removed; `ctx.now` = `DecideOptions.now` or the sidecar clock at call start; echoed as `evaluated_at` |
| Versions cover dependencies | `dependency_hash` = hash of the decision version plus every segment, lookup, gate, experiment, schema and sub-decision version it read |
| No unordered output | `keys`, `values`, `toPairs` sorted by key; maps serialised with sorted keys |
| Fixed hashing | murmur3, per-resource salt, bucket 0–9999; empty unit = outside every partial rollout and experiment |
| Engine pinned to bundles | `min_engine_version` in every manifest; older sidecars refuse the bundle; semantic changes bump `engine.Version` |
| AI excluded | AI answers are logged (model, answer hash), not replayed |

Guarantee: same `dependency_hash`, input, `unit_id` and `evaluated_at` → same output on every pod and every accepting engine version.

### 3.6 Cost limits

| Limit | Default |
|---|---|
| Expression length | 4 KB |
| Nodes per decision | 200 |
| Rules per node / table rows / transform fields | 500 / 5,000 / 500 |
| Input size | 256 KiB (`GEARBOX_MAX_INPUT_BYTES`) |
| Schema nesting | 32 |
| Worst-case loop steps (product of `maxItems` along nested loops) | 1,000,000; loops over arrays without `maxItems` are rejected (`COST_TOO_HIGH`) |
| Publish benchmark | each decision's tests run 1,000×; p99 must be ≤ `budgetMicros` (default 200 µs, AI excluded) |
| Runtime breaker | evaluation repeatedly > 10× budget → fallback for 30 s, `gearbox_decision_breaker_open_total` |
| Bundle | `bundleLimitMB` (64 MB compressed default), decompressed cap 4× |

Expr cannot be interrupted mid-evaluation, so bounds are enforced before shipping. Expr's memory budget stays on. (Before cost checks existed, a 25 KB input with nested loops took 2.45 s.)

### 3.7 PII

- `x-gearbox-pii: true` marks schema fields. The compiler tracks taint through transforms and expressions: anything derived from a PII field is PII.
- PII values never appear in decision logs (even if listed in `logging.fields`), never reach AI prompts (`PII_IN_PROMPT`), and never appear in shadow-report vars.
- Logged hashes are HMAC-SHA256 with a per-installation key (a plain hash of a phone number can be reversed by guessing).
- Static segments with `pii: true` store and ship members as HMACs; lookups compare by HMAC of the key.

## 4. Bundle format and sync protocol

### 4.1 Layout (`bundle.proto`)

```
blobs/sha256/<hex>                       zstd(ResourceBlob)   kind, name, version, spec_json, segment_members, lookup_rows
envelopes/<app>/<env>/<generation>.pb    SignedManifest {manifest bytes, Ed25519 signature, key_id}
heads/<app>/<env>.json                   {"generation": 42, "envelope": "envelopes/…", "sha256": "…"}
```

`Manifest`: app, environment, generation (strictly increasing), created_at, published_by, release_type, release_id, `min_engine_version`, `resources[]` (kind, name, version, sha256, size, decompressed_size, `live`), `rollouts[]` (decision, from/to version, percent, salt), `killed_gates[]`. The signature covers the exact manifest bytes, so no canonical encoding is needed. Blobs are content-addressed and shared between generations; only new hashes are uploaded or downloaded.

### 4.2 Sync protocol (sidecar)

```
start ──▶ load cache dir (re-verify) or GEARBOX_FALLBACK_BUNDLE ──▶ ready if verified
  │
  ├─ SSE GET /sidecar/v1/watch ── event: generation {N} ──▶ sleep rand(0–2 s) ─┐
  ├─ every 30 s ───────────────────────────────────────────────────────────────┤
  ▼                                                                            ▼
GET /sidecar/v1/head  (If-None-Match: <etag>, have=<sha,…>)  ── 304 ──▶ nothing to do
  │ 200 {generation, envelope{url,sha256}, resources[{sha256,url}], expiresAt}
  ▼
download envelope + missing blobs from object store (presigned S3 or /sidecar/v1/blobs/{sha}?sig&exp)
  ▼
bundle.VerifyEnvelope: signature(key_id ∈ trusted) · generation > active · min_engine ≤ engine.Version
  ▼
bundle.Open: every blob sha256 · decompressed size ≤ cap  ──▶ engine.Compile ──▶ Snapshot.RunTests
  ▼ all pass                                                    ▼ any failure
atomic.Pointer swap · write disk (tmp+rename+fsync)          keep active · update log kept_previous
update log "swapped" · heartbeat                             gearbox_sync_failures_total{reason}
```

Reconnect the watch with backoff 1 s → 60 s plus jitter; polling continues while disconnected. A revoked credential stops updates but not serving. Heartbeats every 30 s feed the Fleet screen.

### 4.3 Publish pipeline (control plane)

1. Resolve the target set: live version of every resource for app + environment after the release (plus rollout "from" versions, killed gates).
2. `engine.Compile(set)`; `Snapshot.RunTests` for every decision; `Snapshot.Benchmark` against budgets; size limits (`BUNDLE_TOO_LARGE`).
3. `bundle.Build(set, meta, signer)` → envelope + blobs; generation = previous + 1 (atomic per app + environment; concurrent publishes serialise).
4. Upload new blobs → envelope → head pointer (in that order, so a reader never sees a head without its bytes).
5. Record the release (`manifestHash`, `bundleBytes`, `contents`), update live state, audit event, webhooks (`release.published`), feed event via Redis to every server replica's SSE clients.

Publishing is idempotent per request; a failure before step 4's head write leaves the old generation live.

## 5. Security

| Area | Design |
|---|---|
| App ↔ sidecar | Unix socket in the pod, mode `0660`, group from the pod's `fsGroup`. Optional loopback TCP requires `GEARBOX_TCP_TOKEN_FILE` |
| Sidecar ↔ control plane | App credential `gbx_cred_…` scoped to one app + environment, hashed at rest, shown once, revocable; TLS |
| Bundle integrity | Ed25519 over exact manifest bytes; `key_id` → trusted public keys (`GEARBOX_TRUSTED_KEYS[_FILE]`, several allowed); monotonic generation (no replay); `min_engine_version`; cache re-verified on load; unsigned accepted only with `--allow-unsigned` plus `--bundle-file`/`--definitions-dir` |
| Signing keys | Server key from `GEARBOX_SIGNING_KEY_FILE` + `GEARBOX_SIGNING_KEY_ID` today; KMS signer is ticket GBX-091. Rotation: add new public key everywhere → switch server key → remove old key after every sidecar runs a new-key generation |
| People | OIDC sign-in, HTTP-only `gbx_session` cookie; roles `viewer` < `author` < `approver` < `admin`, optionally narrowed to apps |
| CI/CLI | Personal access tokens `gbx_pat_…` (1–365 days), hashed |
| CSRF | Browser mutations must send `X-CSRF-Token` from `GET /session`; bearer-token requests are exempt |
| Approvals | Per-environment `requiredApprovals`, `authorCanApprove`; break-glass rollback flagged in audit |
| Webhooks | `X-Gearbox-Signature: sha256=<hmac>` |
| Expressions | Sandboxed Expr (no I/O, no `now`), size/cost limits, RE2 only |
| PII | §3.7 |
| AI | Hub token only in sidecars; no model keys anywhere else |
| Supply chain | Distroless non-root images, read-only root filesystem; `govulncheck`, image scan (ticket GBX-111) |

## 6. Observability

| Signal | Where | Notes |
|---|---|---|
| Decision log | ring buffer → sink | sampled; HMACs + listed fields; replayable |
| Update log | ring buffer → sink | every swap/failure |
| Exposure log | ring buffer → sink | never sampled |
| Sink | `GEARBOX_LOG_SINK`: `stdout` (JSON lines), `server` (`POST /sidecar/v1/logs`, ≤ 1,000/batch, retention 14 d), `otlp` (ticket GBX-090) | a full buffer drops and counts, never blocks |
| Metrics | Prometheus on `:9464/metrics` (sidecar and server) | names in [04 §9](04-reference.md#9-metrics) |
| Health | sidecar `/readyz` (verified bundle active), `/livez`; gRPC `grpc.health.v1` | server `/readyz`, `/livez` |
| Traces | span `gearbox.decide` per call, child span per AI call, attributes `gearbox.decision/version/generation/fallback`; continues caller `traceparent` | OTLP export is ticket GBX-090 |
| Server logs | `log/slog` JSON | no secrets, no raw inputs |

Suggested alerts: sync lag > 5 min; any `kept_previous`; fallback rate > 1% for 5 min; client p99 > 1 ms for 10 min; any exposure drop; decision-log drops > 0.1%; any breaker opening.

## 7. Performance

| Target | Value |
|---|---|
| Client-measured p99, non-AI `Decide`, Unix socket | < 1 ms at 2,000 decisions/s per pod, sidecar CPU request 500m |
| Publish → all connected sidecars swapped | p95 < 5 s via feed; ≤ 30 s (+ jitter) via polling |
| Sidecar memory | ≈ 2 × decompressed bundle + 32 MB + log buffer |

Measured (Oct 30 2026, 2 vCPU sandbox, Go 1.24.7, 585-byte JSON input; for comparing options, re-run on the pinned toolchain):

| Measurement | Result |
|---|---|
| One rule | 1.0 µs |
| 50-rule first-match | 20.4 µs |
| 12-field transform | 15.8 µs |
| Decode + run + encode | 39 µs |
| Full gRPC call, Unix socket (grpc-go 1.67.1) | 144.5 µs p50 |
| Full gRPC call, loopback TCP | 172 µs |
| Throughput, one sidecar, 8 callers sharing 2 vCPU | ≈ 15k decisions/s (≈ 0.065 vCPU-ms per decision) |

Sizing: request 500m CPU per 2,000 decisions/s; set `GOMAXPROCS` from the CPU request; **no CPU limit** (throttling is the main tail-latency risk); memory limit from bundle size. Use `DecideBatch` when one request needs several decisions.

## 8. Engineering standards

**Go**
- Go 1.24 with the toolchain pinned in `go.mod`; `cmd/` for binaries, `internal/` for the rest; `engine/` does no I/O.
- Errors wrapped with `%w` and context; sentinel errors for conditions callers branch on; no `panic` outside `main`.
- `context.Context` first on anything that blocks; no `context.Background()` on request paths.
- Every goroutine has an owner and stop path (`errgroup`); the request path never takes a lock the sync loop holds during I/O; snapshot via `atomic.Pointer`.
- Hot path: one JSON decode per request, `sync.Pool` for VMs and buffers, `-benchmem` benchmarks for every engine package.
- `log/slog` structured logs, no secrets or raw inputs. Config from env (+ file) validated at start; fail fast.
- Lint: `gofmt`, `go vet`, `golangci-lint` (staticcheck, errcheck, gosec, revive, bodyclose, contextcheck, nilerr, prealloc).
- Tests: table-driven, `-race` always, golden files in `testdata/`, fuzz targets for decoders and expression compile, testcontainers for MongoDB/Redis/MinIO.

**Protobuf and gRPC**
- `buf lint` (STANDARD), `buf breaking` (FILE) against `main`; never reuse or renumber fields; breaking changes go to `gearbox.v2`.
- Canonical status codes with `google.rpc.ErrorInfo` and a stable `reason`; clients branch on `reason`.
- Server-side limits (`MaxRecvMsgSize`, input size, batch size); `grpc.health.v1`; readiness only after the first snapshot.

**HTTP API**
- Handlers follow the OpenAPI file; RFC 9457 problems with stable `code`; optimistic concurrency by `revision`; cursor pagination.

**Sidecar**
- Never block a decision on the network (except inline AI, bounded by its timeout); validate the whole bundle before swapping; atomic disk writes; `/livez` separate from `/readyz`; no CPU limit by default.
- Image: distroless static, non-root (65532), read-only root filesystem.

**UI**
- Svelte 5 runes, TypeScript strict, `svelte-check` clean; typed client generated from the OpenAPI file, no ad-hoc `fetch`; server field errors shown at the field; confirm dialogs repeat the target environment for prod-affecting actions; keyboard and screen-reader accessible (axe in Playwright).

**Repository**
- Conventional Commits; commits on your own branch; `scripts/check-names.sh` must pass (no company names, internal hosts or company libraries).

**Review checklist**
- [ ] Request path changed? Benchmarks attached (`-count=10`, `benchstat`).
- [ ] Evaluation semantics changed? `engine.Version` bumped, golden tests added, docs updated.
- [ ] Proto/OpenAPI changed? Contract change requested; `buf breaking` passes.
- [ ] Errors wrapped, contexts respected, goroutines owned.
- [ ] Logs free of secrets and PII; metric labels bounded (no unit IDs).
- [ ] New config documented in [04](04-reference.md).
- [ ] No company names or internal URLs.

## 9. Local development

**Prerequisites:** Go 1.24, Node 22 (UI), Docker (optional, for compose).

| Goal | Command |
|---|---|
| Everything, no containers | `make dev` → server on `:8080` (memory store, filesystem object store in `.data/`, dev auth, seeded `lead_service`), sidecar synced to it on `.data/run/engine.sock`, UI dev server on `:5173` |
| Everything in containers | `cd deploy/compose && docker compose up --build` ([README](../deploy/compose/README.md)) |
| Sidecar only, no server | `go run ./sidecar/cmd/gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned --watch` |
| Example service | `GEARBOX_TARGET=unix://$PWD/.data/run/engine.sock go run ./examples/go-service` then `curl -XPOST localhost:8081/route -d @examples/go-service/sample-lead.json` |
| Tests | `make test` (all Go modules with `-race`), `make ui-check` |
| Names check | `make check-names` |

`make dev` uses [`deploy/local/dev.sh`](../deploy/local/dev.sh): it generates a dev signing key with `gearboxctl keys generate` into `.data/keys/` (if missing), starts the server with `--seed`, which creates `lead_service`, imports `examples/definitions`, publishes generation 1 to `dev` and registers the dev credential from `GEARBOX_SEED_CREDENTIAL`, then starts the sidecar with the matching trusted-keys file and the UI dev server (proxying `/api` to `:8080`).

**Network-restricted sandboxes.** Where `proxy.golang.org` and vanity import hosts are blocked but GitHub is reachable, run `source scripts/dev/restricted-env.sh` before any `go` command. It writes `.gowork-mirrors/go.work` mapping vanity paths (`google.golang.org/grpc`, `golang.org/x/*`, `gopkg.in/yaml.v3`…) to GitHub mirrors and sets `GOWORK`, `GOPROXY=direct`, `GOSUMDB=off`, `GOTOOLCHAIN=local`. Add missing mirrors to its `MIRRORS` list. Never use it on a normal machine. The Makefile sources it automatically when `GEARBOX_RESTRICTED=1`.

## 10. Testing strategy

| Layer | What | Where |
|---|---|---|
| Engine unit + golden | Every operator, missing-field rule, cell syntax, hit policy, transform rule, node type, error path; inputs and expected outputs in `testdata/` | `engine/...` |
| Determinism | Every golden test 1,000× in random order on two engine builds → identical output and `dependency_hash` | `engine/` |
| Fuzz | Definition decoding, expression compile, evaluation; bundle decode | `engine/`, `engine/bundle` |
| Cost | Known slow patterns (nested loops without `maxItems`) rejected | `engine/cost` |
| Bundle | Tampered envelope, unknown `key_id`, older generation, too-new `min_engine_version`, wrong blob hash, oversize → each rejected | `engine/bundle`, `sidecar/internal/sync` |
| Contract | Server handlers vs OpenAPI (request/response validation in tests); proto `buf breaking`; UI client generated from the same file | `controlplane/`, CI |
| Control plane e2e | testcontainers (MongoDB, Redis, MinIO): change request → approve → publish → feed → sidecar fetch → verify → swap | `controlplane/`, `sidecar/` |
| Sidecar | Swap under load (1,000 publishes, no failed/mixed responses); kill control plane (keeps serving; restart from cache); fallback bundle | `sidecar/` |
| Client | Deadline, fallback on `UNAVAILABLE`/`DEADLINE_EXCEEDED`, request-ID propagation | `clients/go` |
| UI | Vitest components, Playwright flow create → test → change request → approve → publish → rollout → rollback, axe | `ui/` |
| Load and soak | Client p99 < 1 ms at 2,000/s per pod; 72 h soak; chaos (tickets GBX-112, GBX-113) | `deploy/`, CI runner |
| Examples | `examples/definitions` load in an offline sidecar and their test cases pass | CI |

## 11. Design decisions

| Decision | Why | Cost accepted | Revisit when |
|---|---|---|---|
| **Sidecar per pod, one Go engine for every language** | Identical answers in Go, Java, Python, C++; one place for sync, versioning, logs and metrics; rule changes are live without deploys | ≈ 0.15 ms per call instead of ≈ 0.02 ms in-process; a bundle copy per pod; sidecar down = no decisions for that pod (mitigated by native sidecars, restarts and client fallbacks) | A Go hot path needs < 50 µs: embed `engine/` with the sidecar's sync code as a library (same bundles, same logs) |
| **HTTP/JSON + OpenAPI for the control plane, gRPC for the sidecar** | Browsers cannot speak native gRPC; the control plane is not latency-critical; the UI gets typed clients from the YAML. The sidecar path is latency-critical, so it stays gRPC on a Unix socket | Two API styles | – |
| **Schema registry first** | Rules and tables are type-checked against real shapes; inputs validated before rules run; safe evolution with compatibility checks | Authors register schemas before writing rules (transforms help adapt shapes) | – |
| **Expr as the expression language** | Fastest Go option measured; typed; sandboxed; compiled once per bundle | Go-only (fine behind a sidecar); one evaluation cannot be interrupted (hence cost limits) | – |
| **Bundles through an S3 object store** | Control plane off the hot path; cheap fan-out; content-addressed deltas | A publish takes seconds to propagate | Sub-second propagation is required |
| **Signed envelope over exact manifest bytes** | No canonical encoding needed; rotation through `key_id`; replay blocked by monotonic generations | Key management | – |
| **Push feed (SSE) + 30 s poll** | Fast and robust to feed outages | Long-lived connections on the server | Connection counts grow: add a relay per cluster |
| **Memory store for dev, MongoDB for real use** | Zero-dependency local runs; document model fits versioned definitions; large data (members, rows) lives in the object store | Two stores in production (MongoDB + S3) | The team prefers PostgreSQL |
| **AI through one Integration Hub call** | Provider keys, routing and quotas stay in the Hub; Gearbox holds only a Hub token | Hub contract is a placeholder until its owner fills it in | Hub contract lands (GBX-096) |

