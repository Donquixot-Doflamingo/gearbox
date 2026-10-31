# Gearbox

Gearbox is a low-latency decision engine. Teams author **decisions** — rule sets, decision tables, JSON transforms and small decision graphs, with segments, lookups, feature gates, experiments and AI steps — against **registered, versioned schemas**, review and approve them, and publish them as **signed bundles**. A Go **sidecar** next to every service evaluates them in microseconds over gRPC on a Unix socket. Every answer carries the exact version and dependency hash that produced it, so any logged call can be replayed.

```
 Admin UI / gearboxctl ──HTTP/JSON──▶ gearbox-server ──signed bundle──▶ S3 ──▶ gearbox-sidecar ◀──gRPC (Unix socket)── your service
                                        (control plane)                         (in every pod)
```

## Status (v1 build, 2026-10-31)

Everything in the contracts is built in one pass by four parallel workstreams and merged on this branch.

| Part | State | Verified here |
|---|---|---|
| Engine | Complete: schema registry rules, JSON Schema subset, 35 operators, segment / gate / variant / Expr conditions, all node types, rollouts, dependency hash, tests, cost and PII checks, signed bundles | `go test -race` green (189 runs + fuzz); old service's bucketing fixtures pass |
| Sidecar | Complete except OTLP export | End-to-end test (fake control plane → sync → Decide over Unix socket → swap); real run against `gearbox-server` |
| Go client | Complete | Tests against an in-process sidecar |
| Control plane + `gearboxctl` | All 67 API operations, memory / file / MongoDB stores, filesystem / S3 object stores, publishing, approvals, rollouts, rollback, kill switch, SSE feed, OIDC + dev auth | Every response checked against the OpenAPI spec; seeded server answers `lead_routing`; rule without schema → `422 SCHEMA_REQUIRED` |
| Admin UI (SvelteKit) | Every screen, mock mode with sample data | `svelte-check` 0 errors / 0 warnings, 58 Vitest tests, `vite build` OK |
| Docs, tickets, deploy | 4 docs, 98 tickets, Dockerfiles, compose, Helm | Charts rendered, compose config valid |
| **End to end** | `make e2e`: MongoDB 7 + LocalStack S3 in Docker → server (dev auth, no sign-in) → signed bundle in S3 → sidecar → example Go service; publish, swap, rollback, logs, outages | **18/18 checks pass**; MongoDB and S3 integration suites pass |

### Measured performance (2 vCPU, JSON decode excluded)

| Benchmark | Result | Target |
|---|---|---|
| One 7-condition rule | 1.28 µs | ≤ 2 µs |
| 12-field transform | 11.0 µs | ≤ 25 µs |
| 50-rule first match | 3.56 µs | ≤ 30 µs |
| `lead_routing` graph | 9.3 µs | – |
| Full call: client → Unix socket → sidecar | ≈ 126 µs | p99 < 1 ms |

### First steps

1. Fill in the Integration Hub contract in `contracts/openapi/integration-hub-ai.yaml` (placeholder).
2. Review `contracts/CHANGES-REQUESTED.md` (small contract fixes the workstreams asked for) — GBX-002.

Known gaps: OpenTelemetry export (GBX-123), AI cache/budget enforcement and shadow answers in decision logs (GBX-122), AI `precompute` mode, OIDC sign-in not exercised (local runs use `GEARBOX_AUTH_MODE=dev`, which skips sign-in).

## Repository map

| Path | What | Owner branch |
|---|---|---|
| [`CONTRACTS.md`](CONTRACTS.md), [`contracts/`](contracts/), [`proto/`](proto/) | OpenAPI control-plane API and definition model, sidecar gRPC API, bundle format | lead (`main`) |
| [`engine/`](engine/) | Pure Go engine: validate, compile, evaluate; `engine/bundle` builds and opens signed bundles | `agent/dataplane` |
| [`sidecar/`](sidecar/) | `gearbox-sidecar`: gRPC server, sync, verify, swap, logs, metrics, Hub client | `agent/dataplane` |
| [`clients/go/`](clients/go/) | Go client (`gearbox.Dial`, `Decide`, …) | `agent/dataplane` |
| [`gen/go/`](gen/go/) | Generated Go code for the protos | `agent/dataplane` |
| [`controlplane/`](controlplane/) | `gearbox-server` (HTTP API, publisher, feed) and `gearboxctl` (CLI) | `agent/controlplane` |
| [`ui/`](ui/) | SvelteKit admin UI | `agent/ui` |
| [`clients/java/`](clients/java/), [`clients/python/`](clients/python/) | Thin wrappers (need stub generation) | `agent/docs` |
| [`examples/`](examples/) | `lead_service` definitions and a Go service calling the sidecar | `agent/docs` |
| [`deploy/`](deploy/) | Dockerfiles, docker compose, Helm charts, local dev script | `agent/docs` |
| [`docs/`](docs/), [`tickets/`](tickets/) | Documentation and the ticket list | `agent/docs` |

## Quick start

Needs Go 1.24 (and Node 22 for the UI).

```bash
make dev                                   # server :8080 (seeded), sidecar, UI :5173
DEV_EXAMPLE=1 make dev                     # …plus the example service on :8081
curl -s -XPOST localhost:8081/route -d @examples/go-service/sample-lead.json
```

With Docker instead (adds MinIO, MongoDB and Redis):

```bash
cd deploy/compose && docker compose up --build
```

Sidecar only, from YAML files, no server:

```bash
make sidecar-offline                       # gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned --watch
```

End to end against MongoDB and LocalStack S3 (needs Docker): `make e2e`.

Other targets: `make build`, `make test`, `make vet`, `make ui`, `make gen`, `make check-names`, `make images` (`make help` lists all). In a network-restricted sandbox prefix them with `GEARBOX_RESTRICTED=1` (see [`scripts/dev/restricted-env.sh`](scripts/dev/restricted-env.sh)).

## Documentation

| Document | For |
|---|---|
| [docs/README.md](docs/README.md) | Overview and index |
| [docs/01-features.md](docs/01-features.md) | Every feature, how it works, with a case study each |
| [docs/02-user-stories.md](docs/02-user-stories.md) | Personas, user stories, acceptance criteria |
| [docs/03-implementation-guide.md](docs/03-implementation-guide.md) | Architecture, engine semantics, bundles, security, observability, standards, local dev, testing |
| [docs/04-reference.md](docs/04-reference.md) | Sidecar setup, all settings, gRPC API, operators, Expr, error codes, metrics |
| [tickets/TICKETS.md](tickets/TICKETS.md) | Work list for the whole system |

## Rules for contributors

- Edit only the paths your branch owns ([AGENTS.md](AGENTS.md)); request contract changes in `contracts/CHANGES-REQUESTED.md`.
- Conventional Commits; `gofmt`, `go vet`, `go test -race` green in your modules.
- No company names, internal hosts or company libraries: `make check-names` must pass.
