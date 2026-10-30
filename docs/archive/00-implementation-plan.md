# 00 · Implementation plan

Status: draft for team review · Updated: 2026-10-31 after an independent review (§10) · Decision record: [ADR 001](adr/001-sidecar-first-engine.md) (sidecar-first)

## 1. What we are building

Gearbox turns the Switchboard service into an open-source decision engine with four parts:

| Part | What it is | Language |
|---|---|---|
| **Engine** | A pure Go library: compiles and evaluates rule sets, decision tables, decision graphs (trees), transforms, gates and experiments. No network or disk I/O. | Go |
| **Sidecar** | One binary per pod. Syncs signed bundles from the object store, compiles them, serves `EngineService` over gRPC on a Unix socket, calls the Integration Hub for AI nodes, writes update, decision and exposure logs. | Go |
| **Control plane** | Authoring, versioning, change requests, approvals, environments, publishing, change feed, fleet status, log search, Remote Decision API. Source of truth in MongoDB. | Go |
| **Admin UI** | Panel for authors, reviewers and operators. | SvelteKit (Svelte 5) |

Client libraries are thin gRPC wrappers (Go first, then Java and Python). Any team can generate its own from `engine.proto`. `gearboxctl` is the CLI for YAML import/export, local tests, CI and offline bundles.

### Goals

1. Every Switchboard capability keeps working: dynamic configs, feature gates, experiments, layers, holdouts, static segments, kill switch, percentage rollouts, approvals, audit, and remote evaluation for callers that cannot run a sidecar (Remote Decision API).
2. New: decision graphs, decision tables, Expr conditions, JSON transforms, lookup tables, AI node.
3. Decision latency measured by the client p99 under 1 ms for non-AI decisions at the reference load (2,000 decisions/s per pod, sidecar CPU request 500m).
4. Every answer carries the exact version and dependency hash that produced it and can be replayed; every update is logged.
5. Repository is free of company names, internal hosts and company-owned libraries, and is publishable under MIT once legal approval is in place.

### Non-goals for v1

- Browser or mobile SDKs that evaluate locally (they use the Remote Decision API).
- Product analytics, experiment result statistics, metric pipelines (Gearbox writes the exposure log; analysis happens elsewhere).
- A general workflow engine. Decisions are synchronous and bounded; the AI node is the only network step.

## 2. Target repository layout

One repository, Go workspace (`go.work`) with separate modules so the engine has no infrastructure dependencies. **Port, don't fork:** the new repository starts empty and code moves in phase by phase, already on public dependencies, so company libraries never enter it.

```
gearbox/
├── go.work
├── proto/                       buf.yaml · buf.gen.yaml
│   └── gearbox/ v1/ (engine.proto, bundle.proto) · admin/v1/ (*.proto)
├── gen/go/                      module github.com/ORG/gearbox/gen/go (generated, committed)
├── engine/                      module github.com/ORG/gearbox/engine (no I/O)
│   ├── model/                   decision, node, rule, table, transform, gate, experiment types
│   ├── expr/                    Expr environment, nil-safe patch, sorted builtins, custom functions
│   ├── rules/                   rule sets, operator conditions → Expr
│   ├── transform/  table/  lookup/
│   ├── graph/                   DAG validation, per-path reference check, planner, runtime
│   ├── experiments/             gates, experiments, layers, holdouts, bucketing (ported)
│   ├── cost/                    worst-case cost analysis from schema maxItems
│   ├── pii/                     tracks x-gearbox-pii through expressions and transforms
│   ├── bundle/                  envelope verify, decode, compile to Snapshot, dependency hashes
│   └── enginetest/              golden-test harness used by every package
├── sidecar/                     module github.com/ORG/gearbox/sidecar
│   ├── cmd/gearbox-sidecar/
│   └── internal/ server · sync · store · swap · logs · metrics · hub · health · config
├── controlplane/                module github.com/ORG/gearbox/controlplane
│   ├── cmd/gearbox-server/  cmd/gearboxctl/
│   └── internal/ api · store · publish · feed · objectstore · auth · rbac · approvals · audit · fleet · logsearch · runtime · outbox
├── clients/ go/ java/ python/
├── ui/                          SvelteKit app
├── deploy/ helm/gearbox-sidecar · helm/gearbox-server · compose/ · k8s-examples/
└── docs/                        public docs, written in Phase 6 with neutral examples
```

Private repository only: `internal/docs/` (these design documents), `tools/switchboard-export/` (migration).

## 3. Phases

Each phase lists scope and the acceptance checks that close it. Phase 1 and Phase 2 can run in parallel once `bundle.proto` and the engine's validation API are agreed (first week of Phase 1). Legal approval (R1) runs in parallel with everything and only blocks publication in Phase 7.

### Phase 0 · Foundation

- New private repository, module paths `github.com/ORG/gearbox/...`, Go workspace, `buf` setup, `gen/go` module.
- CI baseline: lint, test, `govulncheck`, `buf lint`, secret scan, licence check, deny-list check ([01](01-debranding-and-dependencies.md) §4).
- Open-source files: `LICENSE` (MIT, copyright line pending R1), `README`, `CONTRIBUTING`, `CODE_OF_CONDUCT`, `SECURITY.md`, `CODEOWNERS`, templates.
- Ask the company's legal team for approval (R1).

Acceptance: CI is green on an empty skeleton; the deny-list check runs on every pull request; no private module can be added to any `go.mod` (CI blocks `replace` to local paths).

### Phase 1 · Engine core

- Model types and definition format ([07](07-decision-graphs.md)); data and code slots ([05](05-rules-and-expressions.md) §0).
- Expr environment `input`, `nodes`, `out`, `ctx`; `now()` removed; `ctx.now` from the caller or clock; nil-safe paths and comparisons (AST patch); sorted `keys`/`values`/`toPairs`; custom functions.
- Rule sets (35 Switchboard operators + 4 new) compiled to Expr; decision tables (`value` placeholder), lookups, switch, sub-decision, output; graph validation (cycles, reachability, per-path node references, schema type check with nullable optional fields).
- Cost analysis from schema `maxItems`; PII tracking; dependency hashes; `min_engine_version`.
- Port gates, experiments, layers, holdouts and murmur3 bucketing; keep hash fixtures byte-identical; define empty-unit behaviour.
- `Snapshot`: immutable, compiled, safe for concurrent use; `Evaluate(ctx, decision, input, opts) → Result{output, version, dependencyHash, path, fallbackUsed, evaluatedAt}`.

Acceptance:

- Parity suite: every behaviour in today's rule-engine tests, `internal/experimentation/*_test.go` and `tests/fixtures/*.json` re-expressed as golden tests and passing. Where today's behaviour depends on the company rule library, the behaviour is written down first and confirmed by the team.
- Determinism suite: every golden test run 1,000 times in random order, on two engine builds, gives identical output.
- Fuzz tests for definition decoding, expression compile and evaluation run nightly without crashes; the cost checker rejects every known slow pattern (nested loops without `maxItems`).
- Benchmarks re-run on the pinned Go toolchain; gates set from those numbers (the Oct 30 sandbox figures were 15.8 µs transform, 1.0 µs rule, 20.4 µs 50-rule first-match).

### Phase 2 · Control-plane core and publishing

- MongoDB store; apps, environments, app credentials (hashed), members and roles enforced in the API.
- Decisions and other resources: drafts, immutable versions, diff, validate, run tests (same engine package).
- Change requests and approvals per environment; audit; outbox webhooks.
- Publisher: build bundle, run tests, cost check and publish benchmark, sign the envelope with a KMS key (`key_id`), upload resources → envelope → head pointer to the object store, emit feed events with presigned URLs. Data releases for segments and lookups. Shared gates and experiments publish every app that uses them.
- `BundleService.Watch` feed with Redis fan-out; fleet heartbeats.
- `gearboxctl`: login, `decision pull|push`, `test`, `change open`, `bundle export`.

Acceptance:

- End-to-end test with testcontainers (MongoDB, Redis, MinIO): open change request → approve → publish → feed event → fetch from MinIO → verify → bytes identical.
- Tampered envelope, unknown `key_id`, replayed older generation and a too-new `min_engine_version` are each rejected.
- Key rotation drill passes with no failed sync.
- Publishing is idempotent; two concurrent publishes produce ordered generations.

### Phase 3 · Sidecar and Go client

- gRPC server on a Unix socket (mode `0660`, `fsGroup`), optional loopback TCP, health, optional reflection.
- Sync loop: watch stream, 30 s head-pointer poll, backoff with jitter, delta fetch from the object store, all checks of [02](02-architecture.md) §3, compile + tests off the request path, atomic swap, verified disk copy, fallback bundle.
- Decision, update and exposure logs (ring buffers, sampling, OTLP), metrics, traces, per-decision circuit breaker.
- Go client: dial, deadline, fallback value, request ID and trace propagation, `gearbox_client_duration_seconds`.
- Packaging: distroless multi-arch image, cosign signature, SBOM; Helm chart; offline mode.

Acceptance:

- Latency: client-measured p99 < 1 ms at 2,000 decisions/s per pod with a 500m request on the reference node type (load test in CI on a fixed runner size).
- Kill the control plane: sidecars keep serving; a new pod starts from the object store; with the object store also down, a pod with a fallback bundle starts and one without stays not-ready.
- Kill the sidecar: Kubernetes restarts it; the Go client returns fallback values within the deadline and counts them.
- Swap under load: no failed or mixed-version responses during 1,000 consecutive publishes at full load.

### Phase 4 · Admin UI

- SvelteKit app from the [prototype](ui-prototype/index.html) and [11](11-admin-ui.md): every screen against the real API; OIDC browser sign-in.
- Read APIs the UI needs and Phase 2 did not build: fleet view, log search adapter, audit search.
- In-browser test runner calls the control plane, which evaluates with the same engine package.

Acceptance: every prototype screen works against the real API; Playwright covers create → test → change request → approve → publish → rollout → rollback; `svelte-check`, ESLint and Vitest clean; axe finds no serious issues on main screens.

### Phase 5 · AI node

- AI node type: prompt `vars` allow-list, model alias, output JSON Schema, timeout, fallback edge, cache keyed by rendered prompt, modes `shadow`, `inline`, `precompute`.
- Sidecar Hub client behind an interface (contract placeholder in `openapi/integration-hub-ai.yaml`; replace when the Hub team shares the real one), global budget, circuit breaker, logging of the concrete model and answer hash.
- Shadow report in the UI.

Acceptance: Hub outage, timeout or schema-invalid answer always routes to the fallback edge; no request waits longer than the node timeout; shadow mode adds no latency to the caller (measured); a `var` derived from a PII field fails publish.

### Phase 6 · More clients, Remote Decision API, docs

- Java client (with Netty native transport for Unix sockets) and Python client.
- Remote Decision API (`DecisionRuntimeService`) on stateless replicas that sync like sidecars.
- Public docs written from these internal docs with neutral examples; `examples/` with a runnable service per language using docker compose and an offline bundle.

Acceptance: a team outside the project integrates a service in under one hour following the public sidecar guide only.

### Phase 7 · Migration, v1.0 and publication

- `tools/switchboard-export` (private): read Switchboard collections, convert dynamic configs and feature gates to decisions and gates, keep experiments, layers, holdouts and segments, emit a bundle per app.
- Shadow migration: Switchboard and Gearbox side by side for selected services; replayed traffic must match 100% before clients switch.
- Load, 72 h soak and chaos tests; security review; v1.0 with goreleaser, signed artifacts, changelog.
- Publication, only with R1 approval: deny-list clean, fresh-history public repository ([01](01-debranding-and-dependencies.md) §3).

## 4. Milestones

| Milestone | Contents | Shows that |
|---|---|---|
| M1 Engine alpha | Phase 1 | Rules, tables, transforms, graphs and experiments evaluate correctly, deterministically and fast |
| M2 End to end | Phases 2 + 3 | A change request in the API reaches every sidecar as a signed bundle, with logs, through `gearboxctl` |
| M3 UI beta | Phase 4 | Authors build, test and release without touching YAML |
| M4 AI shadow | Phase 5 in shadow mode | AI decisions measured against rules with zero added latency |
| M5 v1.0 | Phases 6 + 7 | Real services migrated; project published if approved |

## 5. Work streams

| Stream | Owns | Depends on |
|---|---|---|
| Engine | `engine/`, parity and determinism suites, benchmarks | Phase 0 |
| Control plane | store, change requests, approvals, publish, feed, `gearboxctl`, Remote Decision API | `bundle.proto`, engine validation API |
| Platform | `sidecar/`, object store layout, Helm, CI, load tests | Engine `Snapshot` API, `bundle.proto` |
| UI | SvelteKit app | admin protos (mock transport from the prototype data first) |
| AI | Hub client, AI node, shadow report | engine node interface, Hub contract |
| DevEx | clients, examples, public docs, migration tool | `engine.proto` |

## 6. Quality gates in CI (every pull request)

- Go: `gofmt`, `go vet`, `golangci-lint` (staticcheck, errcheck, gosec, revive), `go test -race`, coverage ≥ 80% on `engine/`, `govulncheck`.
- Protos: `buf lint` (STANDARD), `buf breaking` (FILE) against `main`.
- UI: `svelte-check`, ESLint, Prettier, Vitest; Playwright on merge.
- Security and licences: `gitleaks`, `go-licenses`, Trivy image scan, SBOM, deny-list check.
- Performance: engine benchmarks on a dedicated runner with `-count=10`, compared with `benchstat`; a regression fails only when it is statistically significant and above 10%. Sidecar load test nightly.
- Details in [12](12-engineering-standards.md).

## 7. Mapping from Switchboard

| Switchboard today | Gearbox |
|---|---|
| Dynamic config (rule set + default) | Decision with one `rules` node and an output |
| Feature gate | Gate (kept); also callable as `gate("name")` in expressions |
| Experiment, layer, holdout | Kept; evaluator ported into `engine/experiments`; exposure log added |
| Static segment (Redis set) | Segment shipped in the bundle; `inSegment()` in expressions |
| Dynamic segment (retired), Cosmos condition | Removed |
| Rule operators and FIELD / SEGMENT / FEATURE_GATE conditions | Same operators, compiled to Expr |
| DEPENDENCY condition | `subdecision` node, then a condition on its output |
| Remote `/runtime/configs/execute` | Remote Decision API (same engine), Phase 6 |
| Go SDK local evaluation | Removed; use the sidecar (embedding `engine/` stays possible for Go hot paths) |
| Approvals, audit, tenants, clients | Kept; clients become app credentials scoped by environment |
| Evaluation catalog + generations | Bundles + generations |

## 8. Risks

| ID | Risk | Mitigation |
|---|---|---|
| R1 | The code was written for the company; removing its name does not change who owns it. | Written approval from legal for licence and copyright line. Blocks publication only; engineering continues privately. |
| R2 | The company rule library defines today's rule semantics; a rewrite can drift. | Behaviour written down and confirmed, golden parity suite, 100% match on replayed traffic before switching (Phase 7). |
| R3 | Sidecar adds per-pod memory and a ~0.15 ms hop. | Reference load and CPU request defined; `DecideBatch`; load-test gate; optional embedded mode for Go hot paths. |
| R4 | Large segments or lookup tables bloat bundles and memory. | Rows in the object store; 64 MB compressed / 256 MB decompressed caps; guidance to keep very large sets in the caller's own service. |
| R5 | Integration Hub contract unknown. | Client behind an interface; placeholder OpenAPI; contract test once the Hub team shares the spec. |
| R6 | AI answers are non-deterministic and slow. | Shadow first, timeouts, fallbacks, schema validation, prompt-keyed cache, version pinning; excluded from the replay guarantee. |
| R7 | Kubernetes older than 1.29 lacks native sidecars. | Helm chart falls back to a regular container plus a client-side readiness wait. |
| R8 | Two engine versions in one fleet evaluate differently. | `min_engine_version` in every bundle; parity suite across versions; semantics changes bump it. |
| R9 | One expensive expression stalls a sidecar (Expr cannot be cancelled). | Cost check from `maxItems`, publish benchmark, per-decision circuit breaker, Expr memory budget. |
| R10 | PII leaks into logs, segments or prompts. | `x-gearbox-pii` tracked through transforms; HMACs for logged hashes and PII segment members; prompt `vars` allow-list. |

## 9. Decisions still open

1. Project and organisation name, and copyright holder (R1).
2. MongoDB stays as the store (assumed) or move to PostgreSQL.
3. Object store: S3, GCS or both (assumed S3-compatible), and whether a CDN sits in front.
4. Confirm the reference load (2,000 decisions/s per pod) against the busiest service.
5. Which services migrate first.

## 10. Review log

An independent review of these documents on 2026-10-31 found three blocking problems and eleven major ones. All are addressed in this version:

| Finding | Fixed in |
|---|---|
| Phase order had backward dependencies (approvals needed before Phase 4; clients needed in Phase 3; Phase 0 contained rewrites) | §3 here |
| Signature scheme incomplete (self-contained signature, no trusted keys, rotation or replay protection) | [02](02-architecture.md) §4, [03](03-sidecar-setup.md), [10](10-control-plane.md) §7 |
| Determinism gaps (random `keys` order, versions without dependencies, no caller-set time, flaky tests, empty unit, mixed engine versions) | [02](02-architecture.md) §6, [05](05-rules-and-expressions.md), [04](04-grpc-api.md), proto |
| Unbounded evaluation cost (2.45 s measured on a 25 KB input) | [05](05-rules-and-expressions.md) §8 |
| Missing fields errored in free-form expressions but not in structured ones | [05](05-rules-and-expressions.md) §2 |
| New pods could not start without the API; bundle bytes went through the API | [02](02-architecture.md) §3, [03](03-sidecar-setup.md) §2 |
| PII gaps (segments, derived values, unsalted hashes, AI cache key) | [08](08-ai-node.md), [09](09-observability.md) |
| Shared experiments could disagree across apps; no unsampled exposure log | [07](07-decision-graphs.md) §5, [09](09-observability.md) |
| Latency target not testable (no load, server-side histogram, GOMAXPROCS) | §1 here, [02](02-architecture.md) §7, [09](09-observability.md) |
| Remote Decision API and `gearboxctl` in no phase; source of truth unclear | §3 here, [10](10-control-plane.md) §8 |
| Large data in MongoDB documents; data changes forced through full review; AI budget per pod | [02](02-architecture.md) §4, [07](07-decision-graphs.md) §4, [08](08-ai-node.md) |
| Socket permissions and Java Unix sockets | [03](03-sidecar-setup.md) |
| Generated Go code not importable | §2 here, proto header |
| Model alias can change under a version | [08](08-ai-node.md) §4 |
| Minor: `?` in tables, K8s version, data vs code slots, naming mismatches between docs and proto | [07](07-decision-graphs.md), [02](02-architecture.md), [05](05-rules-and-expressions.md) §0, [04](04-grpc-api.md), [09](09-observability.md) |
