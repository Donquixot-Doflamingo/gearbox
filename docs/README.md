# Gearbox documentation

Gearbox is a low-latency decision engine. Authors build **decisions** (rule sets, decision tables, transforms and small decision graphs, with segments, lookups, gates, experiments and AI steps) against **registered, versioned schemas** in an admin UI or with `gearboxctl`. Reviewers approve them, and the control plane publishes them as a **signed bundle** to an S3-compatible object store. A **Go sidecar** in every service pod downloads the bundle, verifies and compiles it, and answers `Decide` calls over gRPC on a Unix socket in well under a millisecond. Every answer carries the exact version and dependency hash that produced it, so any logged call can be replayed. The control plane is plain HTTP/JSON (OpenAPI) and is never on a service's request path.

| # | Document | Read it when |
|---|---|---|
| 01 | [Features](01-features.md) | You want to know what Gearbox does and how each feature works, with a worked case study per feature |
| 02 | [User stories](02-user-stories.md) | You are planning, testing or accepting work: personas, stories, acceptance criteria |
| 03 | [Implementation guide](03-implementation-guide.md) | You are building or operating Gearbox: architecture, module layout, engine semantics, bundles, security, observability, standards, local development, testing |
| 04 | [Reference](04-reference.md) | You need exact facts: sidecar setup, every `GEARBOX_*` setting, gRPC API, operators, Expr functions, table and transform syntax, error codes, metrics |

Also in this repository:

| Path | What |
|---|---|
| [`CONTRACTS.md`](../CONTRACTS.md) | Contract index. **The contracts win over these docs.** |
| [`contracts/openapi/gearbox-admin.v1.yaml`](../contracts/openapi/gearbox-admin.v1.yaml) | Control-plane HTTP API and the definition model (source of truth) |
| [`proto/gearbox/v1/engine.proto`](../proto/gearbox/v1/engine.proto) | Sidecar gRPC API |
| [`proto/gearbox/v1/bundle.proto`](../proto/gearbox/v1/bundle.proto) | Bundle format |
| [`engine/api.go`](../engine/api.go), [`engine/bundle/api.go`](../engine/bundle/api.go) | Engine and bundle Go APIs |
| [`contracts/openapi/integration-hub-ai.yaml`](../contracts/openapi/integration-hub-ai.yaml) | AI call (placeholder; the owner fills it in) |
| [`tickets/TICKETS.md`](../tickets/TICKETS.md) | Ticket list for the whole system |
| [`examples/`](../examples/) | Lead-routing definitions and a Go service that calls the sidecar |
| [`deploy/`](../deploy/) | Dockerfiles, docker compose, Helm charts |

All examples use one domain: a **used-car lead service** (`app: lead_service`) that routes incoming leads to `fast_track`, `standard`, `reject` or `manual_review`.
| [05-requirements-traceability.md](05-requirements-traceability.md) | Every requirement → where it is built → how it is proven |
