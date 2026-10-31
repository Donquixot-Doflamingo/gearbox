# Working in this repository (agents and people)

## Ownership

| Area | Owner (branch) | Paths |
|---|---|---|
| Data plane | `agent/dataplane` | `engine/`, `sidecar/`, `clients/go/`, `gen/go/`, `tools/protogen/` |
| Control plane | `agent/controlplane` | `controlplane/` (server + `gearboxctl`) |
| Admin UI | `agent/ui` | `ui/` |
| Docs and platform | `agent/docs` | `docs/`, `tickets/`, `deploy/`, `examples/`, `clients/java/`, `clients/python/`, `README.md`, `Makefile`, `.github/` |
| Contracts | lead (`main`) | `contracts/`, `proto/`, `engine/api.go`, `CONTRACTS.md`, `go.work` |

Edit only your paths. If you need a contract change, write it in `contracts/CHANGES-REQUESTED.md` on your branch (what, why, who is affected), work around it, and continue. The lead merges contract changes.

## Rules

- Commit often on your branch with your agent identity (`git -c user.name=… -c user.email=…`). Conventional Commits (`feat(engine): …`).
- No company names, internal hosts or company libraries anywhere (`scripts/check-names.sh` must pass).
- Go: see `docs/` engineering standards; `gofmt`, `go vet`, `go test -race ./...` green in your modules before each commit.
- In a network-restricted sandbox: `source scripts/dev/restricted-env.sh` before any `go` command (maps vanity module paths to GitHub mirrors). Add missing mirrors to that script's MIRRORS list.
