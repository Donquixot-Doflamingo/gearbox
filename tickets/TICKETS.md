# Gearbox tickets

Format and rules: [README.md](README.md). Areas: [Contracts](#contracts) · [Engine](#engine) · [Sidecar](#sidecar) · [Go client](#go-client) · [Control plane](#control-plane) · [CLI](#cli) · [UI](#ui) · [Deploy](#deploy) · [Platform hardening](#platform-hardening) · [AI and Integration Hub](#ai-and-integration-hub) · [Other clients](#other-clients) · [Docs](#docs) · [Quality and CI](#quality-and-ci)

<!-- SUMMARY:START -->
| ID | Title | Area | Status |
|---|---|---|---|
| GBX-001 | Publish contract baseline v1 | Contracts | done |
| GBX-002 | Resolve requested contract changes | Contracts | todo |
| GBX-003 | Proto tooling: buf lint and breaking checks | Contracts | todo |
| GBX-004 | OpenAPI tooling: lint and generated clients | Contracts | todo |
| GBX-005 | Agree the definitions-file format | Contracts | todo |
| GBX-010 | Definition model decoding and validation framework | Engine | done |
| GBX-011 | Schema subset, document validation and compatibility | Engine | done |
| GBX-012 | Expr environment | Engine | done |
| GBX-013 | Structured conditions and operators | Engine | done |
| GBX-014 | Rules node | Engine | done |
| GBX-015 | Table node | Engine | done |
| GBX-016 | Transform and output nodes | Engine | done |
| GBX-017 | Decision graph runtime and validation | Engine | done |
| GBX-018 | Segments and bucketing | Engine | done |
| GBX-019 | Lookups | Engine | done |
| GBX-020 | Gates, experiments, layers, holdouts, exposures | Engine | done |
| GBX-021 | Cost analysis and publish benchmark | Engine | done |
| GBX-022 | PII taint tracking | Engine | done |
| GBX-023 | Dependency hash and determinism suite | Engine | done |
| GBX-024 | Snapshot: Compile, Decide, RunTests | Engine | done |
| GBX-025 | Bundle build, sign, verify, open, file | Engine | done |
| GBX-026 | AI node evaluation | Engine | partial |
| GBX-027 | Engine fuzzing in CI | Engine | todo |
| GBX-030 | gRPC server | Sidecar | done |
| GBX-031 | Configuration and offline modes | Sidecar | done |
| GBX-032 | Sync loop | Sidecar | done |
| GBX-033 | Status and heartbeats | Sidecar | done |
| GBX-034 | Decision, update and exposure logs | Sidecar | done |
| GBX-035 | Metrics, probes, breaker | Sidecar | done |
| GBX-036 | Integration Hub client (placeholder contract) | Sidecar | done |
| GBX-037 | Sidecar resilience tests | Sidecar | done |
| GBX-040 | Go client: Dial and Decide | Go client | done |
| GBX-041 | Go client: batch, gates, variants, status | Go client | done |
| GBX-042 | Go client: metrics and tracing | Go client | done |
| GBX-045 | HTTP server foundation | Control plane | done |
| GBX-046 | Authentication and CSRF | Control plane | done |
| GBX-047 | Apps, environments, credentials, members, RBAC | Control plane | done |
| GBX-048 | Store: memory and MongoDB | Control plane | done |
| GBX-049 | Generic resources: drafts, versions, diff, usages | Control plane | done |
| GBX-050 | Schema endpoints | Control plane | done |
| GBX-051 | Evaluate and test runs | Control plane | done |
| GBX-052 | Segment members and lookup rows | Control plane | done |
| GBX-053 | Change requests and approvals | Control plane | done |
| GBX-054 | Publisher and releases | Control plane | done |
| GBX-055 | Object store: filesystem and S3 | Control plane | done |
| GBX-056 | Sidecar endpoints and change feed | Control plane | done |
| GBX-057 | Operations endpoints | Control plane | done |
| GBX-058 | Webhooks | Control plane | done |
| GBX-059 | Remote Decision API | Control plane | done |
| GBX-060 | Seed mode and UI serving | Control plane | done |
| GBX-065 | gearboxctl: login, push, pull | CLI | done |
| GBX-066 | gearboxctl: validate, test, versions, change requests, releases | CLI | done |
| GBX-067 | gearboxctl: bundle export, decide, keys | CLI | done |
| GBX-070 | App shell, auth and typed client | UI | done |
| GBX-071 | Schema registry screens | UI | done |
| GBX-072 | Decisions list and graph editor | UI | done |
| GBX-073 | Rules editor and condition builder | UI | done |
| GBX-074 | Decision table editor | UI | done |
| GBX-075 | Transform mapper | UI | done |
| GBX-076 | Test console and test cases | UI | done |
| GBX-077 | Segments and lookups screens | UI | done |
| GBX-078 | Gates, experiments and layers screens | UI | done |
| GBX-079 | Change requests and review | UI | done |
| GBX-080 | Releases, rollout, rollback, kill switch | UI | done |
| GBX-081 | Operations screens | UI | done |
| GBX-082 | AI node editor and shadow report | UI | done |
| GBX-083 | Settings screens | UI | done |
| GBX-084 | UI end-to-end and accessibility suite | UI | todo |
| GBX-085 | Container images | Deploy | done |
| GBX-086 | docker compose stack | Deploy | done |
| GBX-087 | Helm library chart for the sidecar | Deploy | done |
| GBX-088 | Helm chart for the server | Deploy | done |
| GBX-089 | Helm validation in CI | Deploy | todo |
| GBX-090 | OpenTelemetry export | Platform hardening | todo |
| GBX-091 | KMS-backed signing | Platform hardening | todo |
| GBX-092 | Multi-replica feed hardening | Platform hardening | todo |
| GBX-093 | Signing key rotation drill | Platform hardening | todo |
| GBX-094 | Image hardening | Platform hardening | todo |
| GBX-095 | AI precompute mode | AI and Integration Hub | todo |
| GBX-096 | Integration Hub real contract | AI and Integration Hub | todo |
| GBX-097 | Global AI call budget | AI and Integration Hub | todo |
| GBX-100 | Java client wrapper | Other clients | done |
| GBX-101 | Python client wrapper | Other clients | done |
| GBX-102 | Java client tests and packaging | Other clients | todo |
| GBX-103 | Python client tests and packaging | Other clients | todo |
| GBX-105 | Documentation set | Docs | done |
| GBX-106 | Keep docs in sync with implementation | Docs | todo |
| GBX-107 | Operator runbooks | Docs | todo |
| GBX-110 | CI workflow | Quality and CI | done |
| GBX-111 | Security scanning | Quality and CI | todo |
| GBX-112 | Load test | Quality and CI | todo |
| GBX-113 | Soak and chaos tests | Quality and CI | todo |
| GBX-114 | Examples in CI | Quality and CI | todo |
| GBX-115 | Engine benchmarks with regression gate | Quality and CI | todo |

Totals: done 55, review 14 (built; verify with npm), partial 1, todo 24
<!-- SUMMARY:END -->

## Contracts

### GBX-001 · Publish contract baseline v1
**Status:** done · **Depends on:** –
OpenAPI control-plane API and definition model, `engine.proto`, `bundle.proto`, `engine/api.go`, `engine/bundle/api.go`, Hub placeholder, `CONTRACTS.md`.
**Acceptance:**
- All files on `main`; every agent branch starts from them.

### GBX-002 · Resolve requested contract changes
**Status:** todo · **Depends on:** GBX-001
Review `contracts/CHANGES-REQUESTED.md` from every branch (version string format, bucketing input, Hub output type, definitions-file format, HMAC key distribution, error status mapping…) and merge agreed changes.
**Acceptance:**
- Each request is accepted (contract updated) or rejected (reason recorded) and removed from the file.
- Docs and examples updated for accepted changes.

### GBX-003 · Proto tooling: buf lint and breaking checks
**Status:** todo · **Depends on:** GBX-001
Add `proto/buf.yaml` and `buf.gen.yaml`; generate `gen/go`; run `buf lint` (STANDARD) and `buf breaking --against main` (FILE) in CI.
**Acceptance:**
- `make gen` regenerates `gen/go` with no diff on a clean tree.
- A renumbered field fails CI.

### GBX-004 · OpenAPI tooling: lint and generated clients
**Status:** todo · **Depends on:** GBX-001
Lint the OpenAPI file (Redocly or Spectral) and generate the TypeScript client for the UI and request/response types for server tests.
**Acceptance:**
- Lint runs in CI with zero errors.
- UI and server tests use types generated from the same file; a contract change that breaks them fails CI.

### GBX-005 · Agree the definitions-file format
**Status:** todo · **Depends on:** GBX-001
Definition YAML files (`--definitions-dir`, `gearboxctl push|pull`) are not in any contract. Proposal: engine `Resource` shape plus `description`, one file per resource in `<kind>/` directories ([04 §10](../docs/04-reference.md#10-definition-files-and-gearboxctl)).
**Acceptance:**
- Format recorded in `CONTRACTS.md`; sidecar, CLI and `examples/definitions` agree.

## Engine

### GBX-010 · Definition model decoding and validation framework
**Status:** done · **Depends on:** GBX-001
Decode every spec kind from JSON; `ValidateResource` with `Resolver`; problems with codes and JSON pointers; `Dependencies`.
**Acceptance:**
- Every OpenAPI example validates; malformed specs give `ValidationProblem`s with correct `path`.
- `SCHEMA_REQUIRED` / `SCHEMA_NOT_FOUND` for rules and table nodes.

### GBX-011 · Schema subset, document validation and compatibility
**Status:** done · **Depends on:** GBX-010
JSON Schema 2020-12 restricted subset, nesting ≤ 32, nullable, `$defs`/local `$ref`, `x-gearbox-pii`; `ValidateDocument`; `CheckSchemaCompatibility` (removed, became required, narrowed, null removed).
**Acceptance:**
- Golden tests for every keyword in the subset; unsupported keywords rejected.
- Compatibility table in [01 §1](../docs/01-features.md#1-schema-registry-and-schema-evolution) covered by tests (US-003).

### GBX-012 · Expr environment
**Status:** done · **Depends on:** GBX-010
Variables `input`, `request`, `nodes`, `out`, `ctx`; nil-safe paths and comparisons; `now` removed; sorted `keys`/`values`/`toPairs`; Gearbox functions (`lookup`, `inSegment`, `gate`, `variant`, `bucket`, `daysBetween`, `coalesce`); `Catalogue()`.
**Acceptance:**
- Missing-field rules (US-012) pass for free Expr.
- `GET /meta/expr` content comes from `Catalogue()`.

### GBX-013 · Structured conditions and operators
**Status:** done · **Depends on:** GBX-012
All 35 field operators, `all`/`any`/`not`, segment, gate, variant and expr leaves; type checks against the input schema; RE2.
**Acceptance:**
- Golden test per operator including missing/null cases.
- `TYPE_MISMATCH` with the condition's pointer for wrong value types.

### GBX-014 · Rules node
**Status:** done · **Depends on:** GBX-013
`first_match`/`all_matches`, `priority`, `enabled`, `default`, rule `rollout`; output checked against `outputSchema`; data-slot `=` expressions.
**Acceptance:**
- US-010, US-013 acceptance criteria pass as engine tests.

### GBX-015 · Table node
**Status:** done · **Depends on:** GBX-013
Inputs, outputs, cell syntax, hit policies `first`/`unique`/`collect`/`priority`, `default`.
**Acceptance:**
- US-030 criteria pass; overlapping rows under `unique` fail validation.

### GBX-016 · Transform and output nodes
**Status:** done · **Depends on:** GBX-012
`build`/`patch`, ordered `fields` with `out`, array targets, `defaults`, `drop`, `omitNil`, `expression`, optional `outputSchema`; output node with `output.schema` check.
**Acceptance:**
- US-040 criteria pass; `SCHEMA_EDGE_MISMATCH` for incompatible edges.

### GBX-017 · Decision graph runtime and validation
**Status:** done · **Depends on:** GBX-014, GBX-015, GBX-016
`entry`, `from`, `next`/`end`, `switch`, `onError`, decision `fallback`, `subdecision`, `lookup`, `gate`, `experiment` nodes; cycle, reachability and per-path reference checks; path steps with outcomes.
**Acceptance:**
- US-050, US-051, US-052 pass; `lead_routing` example produces the documented path.

### GBX-018 · Segments and bucketing
**Status:** done · **Depends on:** GBX-013
Static (incl. HMAC members), dynamic (attributes validated against `attributesSchema`, evaluated per call), rollout (`include`, murmur3 bucket 0–9999); `CheckSegment`.
**Acceptance:**
- US-020…US-023 pass; bucket distribution within ±0.5% over 100k units; fixed bucket fixtures.

### GBX-019 · Lookups
**Status:** done · **Depends on:** GBX-012
Lookup rows from the bundle, `valueSchema` checks, `lookup` node and function, defaults.
**Acceptance:**
- US-060 AC2 passes.

### GBX-020 · Gates, experiments, layers, holdouts, exposures
**Status:** done · **Depends on:** GBX-018
`CheckGate` (rules, rollout, disabled, killed, no_unit); `GetVariant` (targeting, layers, holdout, weights, stopped, no_unit); exposures in `Result.Exposures`.
**Acceptance:**
- US-070, US-080 criteria pass; every reason code covered.

### GBX-021 · Cost analysis and publish benchmark
**Status:** done · **Depends on:** GBX-017
Static limits; worst-case loop steps from `maxItems` (≤ 1M); `Snapshot.Benchmark`; budget check data for the runtime breaker.
**Acceptance:**
- Nested loops without `maxItems` → `COST_TOO_HIGH`; known slow pattern (25 KB input, 2.45 s before) rejected.

### GBX-022 · PII taint tracking
**Status:** done · **Depends on:** GBX-016
Propagate `x-gearbox-pii` through transforms and expressions; block PII in AI vars (`PII_IN_PROMPT`) and logged fields.
**Acceptance:**
- A var derived from `lead.phone` via two transforms fails validation.

### GBX-023 · Dependency hash and determinism suite
**Status:** done · **Depends on:** GBX-017, GBX-020
`dependency_hash` over the decision version and every version read; determinism suite (golden tests 1,000× random order).
**Acceptance:**
- Same hash + input + unit + `evaluated_at` → same output across runs; changing a segment version changes the hash.

### GBX-024 · Snapshot: Compile, Decide, RunTests
**Status:** done · **Depends on:** GBX-017, GBX-023
`Compile(Set)` with rollouts and killed gates, `Decide` (pin version, trace, debug), `RunTests` with mismatches and `expectPath`/`expectError`, `Decisions()`.
**Acceptance:**
- US-053 criteria pass; concurrent `Decide` under `-race` is clean.

### GBX-025 · Bundle build, sign, verify, open, file
**Status:** done · **Depends on:** GBX-024
Implement `engine/bundle`: zstd blobs, manifest, Ed25519 envelope, `ParseTrustedKeys`, `VerifyEnvelope`, `Open`, `WriteFile`/`ReadFile`.
**Acceptance:**
- Tampered envelope, unknown key, wrong blob hash and oversize each fail with distinct errors.
- Build → Open round-trips a `Set` byte-for-byte.

### GBX-026 · AI node evaluation
**Status:** partial · **Depends on:** GBX-017, GBX-022
Render `vars` and prompt, call `AICaller` with timeout, validate against `responseSchema`, `shadow` (background, onError decides) and `inline` modes, cache, `onError` on any failure.
**Acceptance:**
- US-090 AC1/AC3 and US-091 AC1 pass with a fake `AICaller`.

### GBX-027 · Engine fuzzing in CI
**Status:** todo · **Depends on:** GBX-024, GBX-025
Fuzz targets for definition decoding, expression compile, evaluation and bundle decode; nightly CI job.
**Acceptance:**
- Nightly job runs ≥ 10 min per target with no crash; corpus committed.

## Sidecar

### GBX-030 · gRPC server
**Status:** done · **Depends on:** GBX-024
`EngineService` on a Unix socket (`0660`), optional loopback TCP with token, `grpc.health.v1`, optional reflection, input/batch limits, `ErrorInfo` reasons.
**Acceptance:**
- Error table in [04 §8.1](../docs/04-reference.md#81-grpc-sidecar) covered by tests; US-111 passes.

### GBX-031 · Configuration and offline modes
**Status:** done · **Depends on:** GBX-025
All `GEARBOX_*` settings in [04 §2](../docs/04-reference.md#2-sidecar-configuration); `--definitions-dir [--watch]`, `--bundle-file`, `--allow-unsigned` rules; `healthcheck` and `version` subcommands.
**Acceptance:**
- US-141 passes with `examples/definitions`; invalid config fails fast with a clear message.

### GBX-032 · Sync loop
**Status:** done · **Depends on:** GBX-025, GBX-056
Watch SSE with backoff, 30 s head poll with ETag and `have`, 0–2 s jitter, blob download, verification order, compile + tests, atomic swap, disk cache, fallback bundle, update records.
**Acceptance:**
- US-113 AC1, AC2, AC4 pass.

### GBX-033 · Status and heartbeats
**Status:** done · **Depends on:** GBX-032
`GetStatus`; `POST /sidecar/v1/heartbeat` every 30 s with generation, versions, p99, call rate, fallback rate, last error.
**Acceptance:**
- Fleet screen shows a sidecar within 30 s of start with correct generation.

### GBX-034 · Decision, update and exposure logs
**Status:** done · **Depends on:** GBX-030
Ring buffers, sampling (decision setting, environment default, `force_log`), HMACs, listed fields only, never PII; sinks `stdout` and `server` (`POST /sidecar/v1/logs`, ≤ 1,000/batch).
**Acceptance:**
- Exposure records are never sampled; a full buffer drops and counts without blocking (benchmark).

### GBX-035 · Metrics, probes, breaker
**Status:** done · **Depends on:** GBX-030
Metrics in [04 §9](../docs/04-reference.md#9-metrics); `/readyz`, `/livez`; per-decision circuit breaker (10× budget → fallback 30 s).
**Acceptance:**
- `/readyz` 503 until a verified bundle is active (US-112).

### GBX-036 · Integration Hub client (placeholder contract)
**Status:** done · **Depends on:** GBX-026
`sidecar/internal/hub` implements `AICaller` against the placeholder OpenAPI; idempotency key, timeout, breaker, error mapping, metrics.
**Acceptance:**
- Contract test against a stub server built from `integration-hub-ai.yaml`; Hub 429/502/timeout → `onError`.

### GBX-037 · Sidecar resilience tests
**Status:** done · **Depends on:** GBX-032
Swap under load (1,000 publishes), control plane killed, restart from cache, fallback bundle, revoked credential.
**Acceptance:**
- No failed or mixed-version responses during swaps; behaviour matches [01 §12](../docs/01-features.md#12-sidecar-sync-and-last-known-good).

## Go client

### GBX-040 · Go client: Dial and Decide
**Status:** done · **Depends on:** GBX-030
Package `gearbox` (module `clients/go`): `Dial(ctx, target)`, `Decide(ctx, name, input, opts…)` with `WithUnit`, `WithTimeout`, `WithFallback`, `WithRequestID`; returns `Decision{Output, Version, DependencyHash, Generation, FallbackUsed, FromClientFallback}`.
**Acceptance:**
- US-110 AC1, AC2 pass; sidecar down → fallback within the deadline.

### GBX-041 · Go client: batch, gates, variants, status
**Status:** done · **Depends on:** GBX-040
`DecideBatch`, `CheckGate`, `GetVariant`, `Status`, `WaitReady` (for clusters without native sidecars).
**Acceptance:**
- Item errors surface per item; `WaitReady` returns when `GetStatus.ready`.

### GBX-042 · Go client: metrics and tracing
**Status:** done · **Depends on:** GBX-040
`gearbox_client_duration_seconds`, `gearbox_client_fallbacks_total`; `request_id` and `traceparent` metadata propagation.
**Acceptance:**
- Request ID from the context appears in the sidecar's decision log record.

## Control plane

### GBX-045 · HTTP server foundation
**Status:** done · **Depends on:** GBX-001
Router for `/api/v1`, RFC 9457 problems with stable codes, cursor pagination, request validation, `/meta/version`, health and metrics, graceful shutdown.
**Acceptance:**
- Handler tests validate responses against the OpenAPI schemas.

### GBX-046 · Authentication and CSRF
**Status:** done · **Depends on:** GBX-045
OIDC login/callback/logout, session cookie, `GET /session` with CSRF token, CSRF check on cookie mutations, PATs (`POST /tokens`), app credentials for `/sidecar/v1/*` and runtime endpoints, `GEARBOX_AUTH_MODE=dev`.
**Acceptance:**
- US-152 passes; PATs and credentials are stored hashed.

### GBX-047 · Apps, environments, credentials, members, RBAC
**Status:** done · **Depends on:** GBX-046
Endpoints under Apps and Members; roles viewer/author/approver/admin with app scoping; environment policies.
**Acceptance:**
- US-150, US-151 pass.

### GBX-048 · Store: memory and MongoDB
**Status:** done · **Depends on:** GBX-045
One store interface with in-memory (dev) and MongoDB implementations; optimistic concurrency by `revision`; large data (members, rows) kept out of documents.
**Acceptance:**
- The same store test suite passes on both (MongoDB via testcontainers).

### GBX-049 · Generic resources: drafts, versions, diff, usages
**Status:** done · **Depends on:** GBX-048, GBX-010
Create, get, save draft, validate, cut version (validation + tests), list/get versions, diff (YAML unified + changes), usages, archive (`IN_USE`).
**Acceptance:**
- US-001, US-002, US-100, US-101 pass.

### GBX-050 · Schema endpoints
**Status:** done · **Depends on:** GBX-049, GBX-011
`validate-document`, `compatibility`, `allowBreaking` on version cut.
**Acceptance:**
- US-003, US-004 pass.

### GBX-051 · Evaluate and test runs
**Status:** done · **Depends on:** GBX-049, GBX-024
`POST …/evaluate` (draft or version, environment's live data, debug path, `skipAI`) and `…/tests/run`.
**Acceptance:**
- US-053, US-054 pass; results equal the sidecar's for the same generation.

### GBX-052 · Segment members and lookup rows
**Status:** done · **Depends on:** GBX-049
CSV/JSON upload and paging for static segment members (HMAC when `pii`) and lookup rows (validated against `valueSchema`); `segments/{name}/check`.
**Acceptance:**
- US-020, US-021 AC4, US-022 AC1, US-060 AC1 pass.

### GBX-053 · Change requests and approvals
**Status:** done · **Depends on:** GBX-049
Create (validates and tests the would-be bundle), get with diffs and tests, approve, reject, comment, withdraw; policies (`requiredApprovals`, `authorCanApprove`).
**Acceptance:**
- US-102 passes.

### GBX-054 · Publisher and releases
**Status:** done · **Depends on:** GBX-053, GBX-025, GBX-055
`POST …/releases` for `change_request`, `data`, `rollout`, `rollback`, `kill_switch`; build, test, benchmark, size limit, sign, upload blobs → envelope → head; generation per app + environment; release history.
**Acceptance:**
- US-071, US-103, US-104, US-105 pass; two concurrent publishes give ordered generations.

### GBX-055 · Object store: filesystem and S3
**Status:** done · **Depends on:** GBX-045
`GEARBOX_OBJECT_STORE` `file://` and `s3://` (endpoint, path style, presign endpoint); presigned URLs; `/sidecar/v1/blobs/{sha256}` with `sig`/`exp` for the filesystem store.
**Acceptance:**
- Publish → fetch works against MinIO (testcontainers) and the filesystem store.

### GBX-056 · Sidecar endpoints and change feed
**Status:** done · **Depends on:** GBX-054, GBX-046
`GET /sidecar/v1/head` (ETag, `have`), `GET /sidecar/v1/watch` (SSE, pings, Redis fan-out between replicas), heartbeat, log ingest with retention.
**Acceptance:**
- Publish → event delivered to every watching sidecar of that app + environment within 1 s on one replica.

### GBX-057 · Operations endpoints
**Status:** done · **Depends on:** GBX-056
Overview, fleet (lagging first), decision/update/exposure log search, audit, AI shadow report.
**Acceptance:**
- US-120 AC1, US-121, US-153, US-154 AC1 pass.

### GBX-058 · Webhooks
**Status:** done · **Depends on:** GBX-054
Webhook CRUD, signed deliveries (`X-Gearbox-Signature`), retries with backoff for 24 h (outbox).
**Acceptance:**
- US-154 AC2 passes; deliveries survive a server restart.

### GBX-059 · Remote Decision API
**Status:** done · **Depends on:** GBX-054, GBX-024
`POST …/decide`, `…/gates/{name}/check`, `…/experiments/{name}/assign` from an in-process snapshot synced like a sidecar.
**Acceptance:**
- US-130 passes.

### GBX-060 · Seed mode and UI serving
**Status:** done · **Depends on:** GBX-054
`--seed`: create `lead_service`, import `GEARBOX_SEED_DIR`, cut versions, publish to `dev`, register `GEARBOX_SEED_CREDENTIAL`. Serve the built UI from `GEARBOX_UI_DIR` with SPA fallback.
**Acceptance:**
- `make dev` and `docker compose up` produce a ready sidecar without manual steps (US-156).

## CLI

### GBX-065 · gearboxctl: login, push, pull
**Status:** done · **Depends on:** GBX-049, GBX-005
PAT login, idempotent `push` in dependency order, `pull` to the definitions layout.
**Acceptance:**
- US-140 AC1 passes on `examples/definitions`; push twice → no changes.

### GBX-066 · gearboxctl: validate, test, versions, change requests, releases
**Status:** done · **Depends on:** GBX-065
Local validate/test with the engine (`--remote` option), `version cut`, `change open|approve`, `release <type>`.
**Acceptance:**
- US-140 AC2 passes; exit codes non-zero on failure for CI.

### GBX-067 · gearboxctl: bundle export, decide, keys
**Status:** done · **Depends on:** GBX-025, GBX-059
`bundle export` (offline file), `decide` (Remote Decision API), `keys generate` (dev signing key + trusted-keys line).
**Acceptance:**
- US-141 AC3 passes; `keys generate` output works with server and sidecar.

## UI

### GBX-070 · App shell, auth and typed client
**Status:** done · **Depends on:** GBX-004, GBX-046
SvelteKit 2 / Svelte 5 shell, sign-in, app and environment switcher in the URL, typed client from the OpenAPI file, CSRF header, problem display at fields.
**Acceptance:**
- Every route deep-links with app and environment; `svelte-check` clean.

### GBX-071 · Schema registry screens
**Status:** done · **Depends on:** GBX-070, GBX-050
List, editor (nested objects, arrays, nullable, PII toggle), examples, compatibility check, usages, versions.
**Acceptance:**
- An author can register `lead_input` and cut v2 with a compatible change without YAML.

### GBX-072 · Decisions list and graph editor
**Status:** done · **Depends on:** GBX-070, GBX-049
List with live versions and stats; graph canvas for nodes and edges (`next`, `onError`, switch cases), node inspector, live validation.
**Acceptance:**
- Validation problems highlight the node and field from the JSON pointer.

### GBX-073 · Rules editor and condition builder
**Status:** done · **Depends on:** GBX-072
Rule list (priority, enable, rollout), condition builder for all leaves incl. segment and gate, Expr editor with autocomplete from `/meta/expr` and the input schema, `then` editor checked against the output schema.
**Acceptance:**
- The [01 §3](../docs/01-features.md#3-segment-conditions-static-dynamic-rollout) rule can be built without typing JSON.

### GBX-074 · Decision table editor
**Status:** done · **Depends on:** GBX-072
Grid with cell syntax help, hit policy, CSV import/export.
**Acceptance:**
- Round-trip CSV export/import preserves the table.

### GBX-075 · Transform mapper
**Status:** done · **Depends on:** GBX-072
Field list with Expr editors, defaults, drop, live input → output preview from a test case.
**Acceptance:**
- Preview updates on each edit with the server's evaluate result.

### GBX-076 · Test console and test cases
**Status:** done · **Depends on:** GBX-051
Evaluate with input/ctx, per-node path with inputs/outputs; test case editor and runner with mismatches.
**Acceptance:**
- US-054 passes from the UI.

### GBX-077 · Segments and lookups screens
**Status:** done · **Depends on:** GBX-052
Static upload, dynamic condition editor, rollout percent/salt, membership check; lookup rows upload and key preview.
**Acceptance:**
- A static segment upload of 100k members succeeds with progress and errors per line.

### GBX-078 · Gates, experiments and layers screens
**Status:** done · **Depends on:** GBX-070, GBX-049
Gate rules with rollouts, experiment variants/weights/parameters/holdout/targeting/status, layer allocations.
**Acceptance:**
- Weights and layer shares validated in the form before save.

### GBX-079 · Change requests and review
**Status:** done · **Depends on:** GBX-053
Open from versions (with rollout %), diff view, test results, approve/reject/comment/withdraw.
**Acceptance:**
- Self-approval is disabled with the reason when the policy forbids it.

### GBX-080 · Releases, rollout, rollback, kill switch
**Status:** done · **Depends on:** GBX-054
Release history and manifest, rollout stepper, rollback to a generation, kill switch; confirm dialogs repeat the environment.
**Acceptance:**
- Playwright covers publish → 5% → 50% → 100% → rollback.

### GBX-081 · Operations screens
**Status:** done · **Depends on:** GBX-057
Overview, fleet, decision log search with path trace, update log, exposure log, audit.
**Acceptance:**
- A request ID search opens the record with a "replay in console" action pre-filled with version and `now`.

### GBX-082 · AI node editor and shadow report
**Status:** done · **Depends on:** GBX-073, GBX-057
Prompt, vars, response schema, mode, timeout, cache, budget; shadow report with matrix and disagreements.
**Acceptance:**
- `PII_IN_PROMPT` shown at the offending var.

### GBX-083 · Settings screens
**Status:** done · **Depends on:** GBX-047, GBX-058
Members and roles, apps, environments and policies, credentials (secret shown once), webhooks, personal tokens.
**Acceptance:**
- Credential secret is displayed once with a copy button and never again.

### GBX-084 · UI end-to-end and accessibility suite
**Status:** todo · **Depends on:** GBX-080
Playwright flow create → test → change request → approve → publish → rollout → rollback against a seeded server; axe on main screens.
**Acceptance:**
- Runs in CI on merge; axe reports no serious issues.

## Deploy

### GBX-085 · Container images
**Status:** done · **Depends on:** –
`deploy/docker/Dockerfile.sidecar`, `Dockerfile.server` (with UI in `/ui`, `GEARBOX_UI_DIR`), `Dockerfile.ui` (nginx static), `Dockerfile.example`; distroless static non-root.
**Acceptance:**
- `docker build` of each succeeds once the binaries exist (verify when GBX-031/GBX-045 land).

### GBX-086 · docker compose stack
**Status:** done · **Depends on:** GBX-085
`deploy/compose`: keygen, server (memory or MongoDB), MinIO with bucket init, MongoDB, Redis, sidecar, example Go service.
**Acceptance:**
- `docker compose up --build` then `curl localhost:8081/route …` returns a decision (verify when GBX-060 lands).

### GBX-087 · Helm library chart for the sidecar
**Status:** done · **Depends on:** –
`deploy/helm/gearbox-sidecar`: native sidecar container, volumes, fsGroup, credential secret, trusted keys, `GOMAXPROCS` from requests, probes on 9464, no CPU limit.
**Acceptance:**
- Rendered output matches [04 §1.2](../docs/04-reference.md#12-kubernetes-native-sidecar-129).

### GBX-088 · Helm chart for the server
**Status:** done · **Depends on:** GBX-085
`deploy/helm/gearbox-server`: Deployment, Service, ConfigMap, Secret (or existing secret), probes.
**Acceptance:**
- `helm template` renders valid manifests with default values.

### GBX-089 · Helm validation in CI
**Status:** todo · **Depends on:** GBX-087, GBX-088
`helm lint` and `helm template | kubeconform` in CI; kind cluster smoke test with a sample app using the library chart.
**Acceptance:**
- CI job green; the kind test reaches a ready pod with a native sidecar.

## Platform hardening

### GBX-090 · OpenTelemetry export
**Status:** todo · **Depends on:** GBX-034, GBX-045
`GEARBOX_LOG_SINK=otlp` and `GEARBOX_OTLP_ENDPOINT` for logs and traces in sidecar and server; `gearbox.decide` spans continuing `traceparent`; child spans per AI call.
**Acceptance:**
- Records and spans arrive in an OpenTelemetry Collector in an integration test.

### GBX-091 · KMS-backed signing
**Status:** todo · **Depends on:** GBX-054
Signer interface with file and cloud KMS implementations (Ed25519); no private key on server disk in production.
**Acceptance:**
- Publishing works with a KMS key; key ID from configuration.

### GBX-092 · Multi-replica feed hardening
**Status:** todo · **Depends on:** GBX-056
Redis fan-out under replica restarts, connection limits, SSE keepalive tuning, per-connection backpressure, 10k connections per replica.
**Acceptance:**
- Load test with 10k watchers across 3 replicas: event fan-out p95 < 2 s; replica kill reconnects all watchers within 60 s.

### GBX-093 · Signing key rotation drill
**Status:** todo · **Depends on:** GBX-054, GBX-032
Documented and tested rotation (add key → switch → remove).
**Acceptance:**
- US-155 passes in an automated test.

### GBX-094 · Image hardening
**Status:** todo · **Depends on:** GBX-085
Multi-arch builds, image signing, SBOM, pinned base image digests.
**Acceptance:**
- CI produces amd64 and arm64 images with SBOMs.

## AI and Integration Hub

### GBX-095 · AI precompute mode
**Status:** todo · **Depends on:** GBX-026, GBX-054
Define and build `mode: precompute`: a job evaluates the AI node ahead of time and writes answers into a lookup published as a data release; requests read the lookup.
**Acceptance:**
- Design agreed and added to the contracts; a precomputed decision answers without a Hub call.

### GBX-096 · Integration Hub real contract
**Status:** todo · **Depends on:** GBX-036
Replace `contracts/openapi/integration-hub-ai.yaml` with the Hub owner's spec; adapt `sidecar/internal/hub` only.
**Acceptance:**
- Contract test passes against the Hub's sandbox; AI node definitions unchanged.

### GBX-097 · Global AI call budget
**Status:** todo · **Depends on:** GBX-036, GBX-056
Share `budget.maxCallsPerMinute` across all sidecars of an app + environment (shares from the control plane by traffic).
**Acceptance:**
- With 10 sidecars and a 600/min budget, total calls stay ≤ 600/min ± 5%.

## Other clients

### GBX-100 · Java client wrapper
**Status:** done · **Depends on:** GBX-001
`clients/java`: thin wrapper (deadline, fallback, request ID) over grpc-java stubs generated from `engine.proto`, Unix socket through Netty epoll; README with stub generation.
**Acceptance:**
- Builds with Maven on a normal machine after stub generation (verify on a networked machine).

### GBX-101 · Python client wrapper
**Status:** done · **Depends on:** GBX-001
`clients/python`: thin wrapper over grpcio stubs generated with grpcio-tools; README with stub generation.
**Acceptance:**
- `decide` with deadline and fallback works against a local sidecar after stub generation.

### GBX-102 · Java client tests and packaging
**Status:** todo · **Depends on:** GBX-100, GBX-030
Integration tests against an offline sidecar; package as a library artifact for internal use.
**Acceptance:**
- US-114 passes for Java.

### GBX-103 · Python client tests and packaging
**Status:** todo · **Depends on:** GBX-101, GBX-030
Integration tests against an offline sidecar; build a wheel for internal use.
**Acceptance:**
- US-114 passes for Python.

## Docs

### GBX-105 · Documentation set
**Status:** done · **Depends on:** GBX-001
`docs/README.md`, `01-features.md`, `02-user-stories.md`, `03-implementation-guide.md`, `04-reference.md` consistent with the contracts.
**Acceptance:**
- No claim contradicts the OpenAPI or proto files; `scripts/check-names.sh` passes.

### GBX-106 · Keep docs in sync with implementation
**Status:** todo · **Depends on:** GBX-105
After the parallel branches merge, reconcile config names, CLI flags and metric names in [04](../docs/04-reference.md) with the code.
**Acceptance:**
- Every `GEARBOX_*` read by the binaries is documented, and nothing documented is missing from the code.

### GBX-107 · Operator runbooks
**Status:** todo · **Depends on:** GBX-057
Runbooks for sync lag, `kept_previous`, fallback-rate spikes, breaker openings, key rotation, control-plane outage.
**Acceptance:**
- Each suggested alert links to a runbook section.

## Quality and CI

### GBX-110 · CI workflow
**Status:** done · **Depends on:** –
`.github/workflows/ci.yml`: go vet and test per module, check-names, UI check and build.
**Acceptance:**
- Workflow file valid; runs green once modules compile.

### GBX-111 · Security scanning
**Status:** todo · **Depends on:** GBX-110
`govulncheck`, `golangci-lint` with gosec, secret scan, container image scan; block on high/critical.
**Acceptance:**
- CI fails on a known vulnerable dependency.

### GBX-112 · Load test
**Status:** todo · **Depends on:** GBX-040, GBX-030
Reference load 2,000 decisions/s per pod, sidecar request 500m, on a fixed runner; measure `gearbox_client_duration_seconds`.
**Acceptance:**
- Client p99 < 1 ms recorded in CI (US-110 AC3); benchmark results stored per run.

### GBX-113 · Soak and chaos tests
**Status:** todo · **Depends on:** GBX-112, GBX-037
72 h soak with publishes every minute; chaos (kill control plane, object store, Redis, sidecar).
**Acceptance:**
- No memory growth beyond 10%; no failed or mixed-version responses; behaviour matches the failure table.

### GBX-114 · Examples in CI
**Status:** todo · **Depends on:** GBX-031, GBX-040
Load `examples/definitions` in an offline sidecar, run their tests, build and smoke-test `examples/go-service`.
**Acceptance:**
- CI fails if an example definition stops validating.

### GBX-115 · Engine benchmarks with regression gate
**Status:** todo · **Depends on:** GBX-024
Benchmarks with `-count=10` on a dedicated runner compared by `benchstat`; fail on a significant regression > 10%.
**Acceptance:**
- Gate catches an injected 20% slowdown.

## Follow-ups from the v1 build

### GBX-120 · Generate go.sum on a normal machine
Run `go mod tidy` in each module (`engine`, `sidecar`, `controlplane`, `clients/go`, `gen/go`, `examples/go-service`) with the default Go proxy and commit the go.sum files. The build sandbox could not reach the proxy.
**Acceptance:** `go build ./...` and `go test ./...` pass in every module without `scripts/dev/restricted-env.sh`.
**Status:** done · **Depends on:** –

### GBX-121 · First UI verification pass
`cd ui && npm install && npm run check && npm test && npm run build`, then `npm run dev:mock` and click through every screen. Fix svelte-check type and a11y findings.
**Acceptance:** check, test and build pass; every screen works in mock mode and against `gearbox-server serve --seed`.
**Status:** done · **Depends on:** GBX-070…GBX-083

### GBX-122 · Enforce AI cache and budget
AI node `cache` (key = hash of rendered prompt, model, schema) and app-wide `budget.maxCallsPerMinute` are parsed but not enforced; shadow AI answers go to metrics only, not into the decision record's `ai` list (needed by the shadow report).
**Acceptance:** cache hits skip the Hub; budget exceeded → onError; shadow answers appear in decision logs and the shadow report.
**Status:** todo · **Depends on:** GBX-026

### GBX-123 · OpenTelemetry export
`GEARBOX_LOG_SINK=otlp` falls back to stdout; no traces yet.
**Acceptance:** logs and spans reach an OTLP collector; trace continues from `traceparent`.
**Status:** todo · **Depends on:** GBX-034
