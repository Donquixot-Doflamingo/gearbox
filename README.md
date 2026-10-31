# Gearbox

Gearbox is a low-latency decision engine. Teams author **decisions** — rule sets, decision tables, JSON transforms and small decision graphs, with segments, lookups, feature gates, experiments and AI steps — against **registered, versioned schemas**, review and approve them, and publish them as **signed bundles**. A Go **sidecar** next to every service evaluates them in microseconds over gRPC on a Unix socket. Every answer carries the exact version and dependency hash that produced it, so any logged call can be replayed.

```
 Admin UI / gearboxctl ──HTTP/JSON──▶ gearbox-server ──signed bundle──▶ S3 ──▶ gearbox-sidecar ◀──gRPC (Unix socket)── your service
                                        (control plane)                         (in every pod)
```

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
