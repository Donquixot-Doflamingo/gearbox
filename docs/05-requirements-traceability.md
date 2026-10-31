# 05 · Requirements traceability

Every requirement from the design conversations, where it is built, and how it is proven. Status: **done** (built and verified), **partial**, **excluded** (deliberately out of scope for now).

AI decisions are excluded from this release by request: the AI node, its Integration Hub client and the placeholder contract exist, but the Hub call itself is not wired to a real service and is not part of the acceptance below.

## Engine: rules, transforms, decision graphs

| # | Requirement | Where | Proof | Status |
|---|---|---|---|---|
| R1 | Rule engine with all the old service's operators, plus free-form expressions | `engine/` conditions (35 operators compiled to Go closures), Expr conditions | `engine` unit tests; operator table in [04-reference](04-reference.md) | done |
| R2 | Simple, Jolt-like JSON transformation using Expr (GoExpr) | `transform` node: ordered `target: expression` fields, defaults, drop, patch mode | engine tests; `lead_routing` uses a transform before its rules node | done |
| R3 | Decision trees / graphs: chain transform → rules → output, branch with switch, error edges, fallback | `engine` graph runtime (entry, next, switch, onError, fallback, subdecision) | engine tests; test console in the UI shows the path | done |
| R4 | **Schema registry first**: a rules node cannot be created without a registered input schema; it runs only on input matching that schema | control plane rejects save with `422 SCHEMA_REQUIRED` / `SCHEMA_NOT_FOUND`; engine validates node input before rules run | `make e2e` check "rule node without schemas → 422 SCHEMA_REQUIRED"; engine tests | done |
| R5 | Rules have an output schema too, of any type (string, number, object) | `outputSchema` on rules/table nodes; `then`/`default` checked against it | engine tests | done |
| R6 | A transform node may sit before a rules node to reshape input to the rule's schema | edge check: transform `outputSchema` must be accepted by the next rules node's `inputSchema` | engine tests; `lead_routing` (`normalize` → `eligibility`) | done |
| R7 | Schemas support nullable fields and nested objects (not flattened) | JSON Schema subset validator: nested objects/arrays, `type: [x, "null"]`, enums, bounds, PII marker | engine schema tests; schema tree editor in the UI | done |
| R8 | Segments inside rule conditions, combined with schema field conditions (AND/OR/NOT) | segment condition leaf; `all`/`any`/`not` groups | `lead_routing` rule `r3` combines field + dynamic + rollout segments | done |
| R9 | Static segments | uploaded members (HMAC when PII), `IN_SEGMENT` | `make e2e`: adding a city to `blocked_cities` changes the sidecar's answer | done |
| R10 | Dynamic segments | conditions over attributes evaluated at decision time | engine tests (`metro_premium`) | done |
| R11 | Experiment / rollout segments | `rollout` segments (stable % of units), `variant` conditions, gates | engine tests; `make e2e` experiment and rollout checks | done |
| R12 | Decision tables, lookups, gates, experiments, layers, holdouts | engine node types and resources | engine tests; old service's bucketing fixtures pass byte-for-byte | done |
| R13 | Low latency | compiled closures, no locks on the hot path | rule 1.28 µs, 12-field transform 11 µs, `lead_routing` 9.3 µs; full client call p99 0.66–0.85 ms on 2 shared vCPUs (`make bench-sidecar`) | done |

## Sidecar and client libraries

| # | Requirement | Where | Proof | Status |
|---|---|---|---|---|
| S1 | Sidecar per pod; services talk to it over gRPC (no language barrier) | `sidecar/` — gRPC on a Unix socket, optional loopback TCP | `make e2e`; Helm chart renders a native sidecar | done |
| S2 | Sidecar detects changes itself (push + poll), downloads, verifies, compiles, swaps without restart | SSE watch + ETag poll, signed bundles from S3, tests before swap, atomic swap | `make e2e`: every publish/rollout/rollback/kill switch is observed by the sidecar | done |
| S3 | **Control-plane changes reach the sidecar** | — | `make e2e` proves each kind: decision version, segment members, gate create, kill switch, experiment, 50% rollout then 100%, rollback | done |
| S4 | Sidecar logs which version answered each call and every update | decision log (version, dependency hash, path), update log, exposure log | `make e2e`: decision and update logs reach the control plane | done |
| S5 | Keeps working when the control plane is down; restarts from disk | last-known-good cache, re-verified on load | `make e2e`: control plane killed → still answers; sidecar restarted with control plane down → ready from disk, same version | done |
| S6 | Thin client libraries: deadline, fallback, request ID | `clients/go` (full), `clients/python`, `clients/java` | Go: tests + `examples/go-service` + `examples/probe` in `make e2e`. Python: 5 tests against a real sidecar (`clients/python/tests`). Java: compiles only on a machine with Maven, **not run** | Go, Python done · Java partial |
| S7 | A separate client to integration-test the sidecar | `examples/probe` (`gearbox-probe`): status, decide, gate, variant, bench — uses only the public Go client | used by `make e2e` for every propagation check | done |
| S8 | Backend service integrated with the sidecar | `examples/go-service` (HTTP service routing leads through the sidecar, 5 ms deadline, fallback) | `make e2e`: answers via sidecar; falls back when the sidecar is down | done |

## Control plane, storage, UI

| # | Requirement | Where | Proof | Status |
|---|---|---|---|---|
| C1 | Control plane: versions, change requests, approvals, releases, rollouts, rollback, kill switch | `controlplane/` (67 API operations) | contract tests validate every response against the OpenAPI file; `make e2e` | done |
| C2 | Contracts defined first so frontend and backend work independently (OpenAPI with descriptions and examples) | `contracts/openapi/gearbox-admin.v1.yaml`, `proto/gearbox/v1/*.proto` | server refuses to start if an operation has no handler; UI client generated from the YAML | done |
| C3 | MongoDB storage | `controlplane/internal/store/mongostore` | conformance suite against MongoDB 7; `make e2e` runs on MongoDB | done |
| C4 | S3 object storage (LocalStack locally) | `controlplane/internal/objstore` (minio-go, presigned URLs) | S3 integration test against LocalStack; `make e2e` bundles in S3 | done |
| C5 | Skip sign-in locally | `GEARBOX_AUTH_MODE=dev` (every request is an admin; header can switch user/role) | used by `make e2e` and `make dev` | done (OIDC built, not tested against a real provider) |
| C6 | Admin UI in Svelte, good design, every panel | `ui/` SvelteKit 2 + Svelte 5 | `svelte-check` 0/0, 58 tests, build OK; all 29 screens opened in a browser against the real server with 0 errors | done |
| C7 | Remove company names | `scripts/check-names.sh` in CI | passes | done |
| C8 | Docs: features, user stories, implementation guide, tickets | `docs/01–05`, `tickets/` | — | done |
| C9 | AI decisions through the Integration Hub | AI node + placeholder Hub contract | — | excluded for now |

## Known gaps (tracked in tickets)

- Java client not compiled or run here (no Maven in the build environment).
- OIDC sign-in not exercised against a real identity provider.
- OpenTelemetry export (GBX-123); AI cache and budget enforcement (GBX-122).
- Latency figures come from a 2-vCPU shared sandbox; measure on production hardware before setting SLOs.
