# 01 · De-branding and dependency replacement

Goal: a repository with no company names, internal hosts or company-owned libraries, buildable from public modules only, publishable under MIT.

## Before anything: ownership

Code written for an employer usually belongs to the employer. Renaming the project does not change that. Before the first public commit, get written approval from the company's legal team covering:

- permission to publish this code (and anything derived from it) under MIT;
- the copyright line in `LICENSE` (for example the team's name, or "The Gearbox Authors" with a contributor licence or DCO).

This blocks **publication**, not engineering: all phases can run in the private repository while approval is pending.

## 1. Company Go modules to replace

Today `backend/go.mod` pulls 21 modules from the company's private `go` repository through `replace ../go/...` directives. 53 non-test Go files import them.

| Company module | Import sites | Used for | Replace with | Notes |
|---|---|---|---|---|
| `go/rulekit/core/{model,evaluation,resolver,trace,validation,operator}`, `go/rulekit/sdk` | 29 | The rule engine itself: rule model, operators, evaluation, traces | **New `engine/rules` package** on top of `github.com/expr-lang/expr` | Biggest item. Re-implement from the documented behaviour and today's tests, not by copying company code. See [05](05-rules-and-expressions.md). |
| `go/logger` | 17 | Structured logging | `log/slog` (standard library) | Add a small `slog` handler for trace IDs. |
| `go/mongo` | 14 | Client lifecycle, collection helpers, paging | `go.mongodb.org/mongo-driver/v2` directly | Keep a thin `store` package with paging helpers. |
| `go/redis` | 7 | Segment index, cache L2, catalog | `github.com/redis/go-redis/v9` | Redis is only needed by the control plane now (feed fan-out); sidecars do not use it. |
| `go/webhook` | 5 | Outbound webhooks with retries | In-repo `controlplane/internal/webhook` (outbox collection + worker) | Small; retries with backoff and a lease, as today. |
| `go/health` | 4 | Health endpoints | `google.golang.org/grpc/health` + a 30-line HTTP handler | |
| `go/cache` | 2 | L1 (Otter) + L2 (Redis) object cache | `github.com/maypok86/otter/v2` in the control plane only | Sidecars hold compiled snapshots; no cache layer needed. |
| `go/transport/http/{router,server,middleware}` | 5 | Gin wrapper, server lifecycle, middleware | `connectrpc.com/connect` + `net/http` | Admin API moves to Connect (gRPC + JSON from one proto). |
| `go/transport/http/client` | 2 | HTTP client | `net/http` with explicit timeouts | |
| `go/config` | 1 | Config loading | `github.com/knadh/koanf/v2` (env + YAML) | Env prefix becomes `GEARBOX_`. |
| `go/app` | 1 | Process lifecycle | `golang.org/x/sync/errgroup` + `signal.NotifyContext` | |
| `go/circuitbreaker` (indirect) | – | Breakers | `github.com/sony/gobreaker/v2` | Used by the Hub client. |
| `go/metrics` (indirect) | – | Metrics | `github.com/prometheus/client_golang` | Plus OpenTelemetry for traces. |
| `go/errors`, `go/json` (indirect) | – | Error and JSON helpers | `errors`, `encoding/json` | |
| `go/jwt` (indirect) | – | JWT | `github.com/coreos/go-oidc/v3` for OIDC sign-in | |
| `go/transport/awstrace`, `go/transport/requestid` (indirect) | – | Tracing, request IDs | OpenTelemetry (`go.opentelemetry.io/otel`) + small middleware | |
| `go/testutil` | tests | Test helpers | `github.com/testcontainers/testcontainers-go` (+ `stretchr/testify` if the team prefers) | |

Other company or vendor-specific code:

| Item | Where | Action |
|---|---|---|
| Cosmos feature-store client (`cspl-cars24/cosmos-feature-store-client-golang`) | `internal/featurestore` | Delete with the FEATURE_STORE condition kind. |
| Statsig importer | `internal/statsigimport`, routes, UI dialog | Delete. |
| Datadog Orchestrion (`orchestrion.tool.go`, Dockerfile) | backend | Delete; use OpenTelemetry (vendor-neutral). |
| Internal login SDK (`frontend/vendor/login-sdk-*`) | frontend | Delete with the React admin; SvelteKit app uses OIDC. |
| Gateway identity header `X-Authorized-User` | `httpapi/middleware.go` | Delete; verify OIDC tokens instead. |
| Dynamic segments (retired) | service, domain | Delete. |

## 2. Names, hosts and files to remove or rename

| Find | Replace |
|---|---|
| Module `github.com/CSPL-CARS24/switchboard` | `github.com/ORG/gearbox/...` |
| Proto package `switchboard.runtime.v1` | `gearbox.v1` |
| `SWITCHBOARD_` environment prefix | `GEARBOX_` |
| `c24-switchboard-service`, `switchboard-api`, `swbd` key prefix | `gearbox-server`, `gearbox-sidecar`, `gbx` |
| `cosmos-feature-store.cars24.team` and any `*.cars24.*` host | delete |
| `.github/workflows/*pitlane*`, GHCR paths under the company org, `KUBE_CONFIG_*` secrets | new workflows publishing to the project's own registry |
| `backend/docs/*`, `docs/superpowers/*` (internal migration and planning notes that name internal systems) | delete from the public repository; keep privately if needed |
| Seed scripts, sample tenants and client IDs with company names | regenerate with neutral sample data |

## 3. Publishing checklist

1. Approval from legal (above).
2. Work in the private repo until the deny-list check in §4 passes on the publish tree.
3. Scan for secrets: `gitleaks detect --no-git` on the tree and `gitleaks detect` on history.
4. Check third-party licences: `go-licenses check ./...` and `npx license-checker --production --onlyAllow "MIT;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;0BSD"` for the UI.
5. Create the public repository with a **fresh history**: copy the cleaned tree and make one initial commit. Old history can contain internal names, hosts or secrets that the tree scan cannot see.
6. Add `LICENSE`, `NOTICE` (if any Apache-2.0 dependency requires it), `README`, `CONTRIBUTING`, `CODE_OF_CONDUCT`, `SECURITY.md`, `CODEOWNERS`, `.github/ISSUE_TEMPLATE/*`, PR template.
7. Turn on branch protection, required reviews, signed commits or DCO, Dependabot, CodeQL.

## 4. Deny-list check and internal documents

One check, used by Phase 0 acceptance, CI on every pull request ([12](12-engineering-standards.md)) and the publishing checklist:

```bash
# run on the tree that will be published (export of the private repo without internal/)
grep -rniE "cars24|cspl|c24|bifrost|pitlane|cosmos|statsig|swbd|switchboard" --exclude-dir=.git . && exit 1 || exit 0
```

- `switchboard` is on the list too: the public project never names the service it came from. The migration tool (`tools/switchboard-export`) stays in the private repository.
- **These design documents are internal.** They name the old service, its libraries and its domain on purpose. Keep them in `internal/docs/` in the private repository; the public `docs/` is written from them in Phase 6.
- **Examples use a neutral domain.** The used-car samples here (cities, ₹ prices, lakh, `+91` phone numbers, lead routing) point to the company. Public examples and the UI sample data switch to a generic domain (for example loan applications or e-commerce orders) before publication.
- The list itself is stored as a CI secret so the public repository does not contain the names it is hiding.
