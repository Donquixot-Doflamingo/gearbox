# Gearbox documentation

Gearbox is a decision engine: rules, decision tables, decision trees, JSON transforms and AI decisions, evaluated by a sidecar that runs next to every service. Apps send input over gRPC and get the decision back. The sidecar keeps itself up to date from a control plane.

> Working name. Replace `Gearbox`, `gearbox` and `ORG` everywhere once the team and project names are registered.
>
> **Internal documents.** These pages name the old service and use its domain (used-car leads) on purpose. They stay in the private repository; the public docs are rewritten from them with neutral examples before publication ([01](01-debranding-and-dependencies.md) §4).

| # | Document | Read it when |
|---|---|---|
| 00 | [Implementation plan](00-implementation-plan.md) | You are planning or tracking the build |
| 01 | [De-branding and dependency replacement](01-debranding-and-dependencies.md) | You are removing company-specific code and names before open-sourcing |
| 02 | [Architecture](02-architecture.md) | You need the big picture: control plane, bundles, sidecar, data flow |
| 03 | [Running the sidecar](03-sidecar-setup.md) | You are adding Gearbox to a service (Docker, Kubernetes, Helm, config) |
| 04 | [gRPC API reference](04-grpc-api.md) | You are calling the sidecar from Go, Java, Python or anything else |
| 05 | [Rules and expressions](05-rules-and-expressions.md) | You are writing rules or Expr expressions |
| 06 | [Transforms](06-transforms.md) | You are reshaping JSON (the Jolt replacement) |
| 07 | [Decision graphs](07-decision-graphs.md) | You are building decision trees, tables, lookups, flags and experiments |
| 08 | [AI node](08-ai-node.md) | You are adding an AI decision step via the Integration Hub |
| 09 | [Logs, metrics and traces](09-observability.md) | You are operating Gearbox or debugging a decision |
| 10 | [Control plane and admin API](10-control-plane.md) | You are working on publishing, approvals, bundles or the admin API |
| 11 | [Admin UI](11-admin-ui.md) | You are building the SvelteKit admin panel |
| 12 | [Engineering standards](12-engineering-standards.md) | Before you write or review any code |

Files that ship with these docs:

- [`adr/001-sidecar-first-engine.md`](adr/001-sidecar-first-engine.md): the architecture decision record, with the 2026-10-31 amendments.
- [`proto/gearbox/v1/engine.proto`](proto/gearbox/v1/engine.proto): the sidecar API apps call.
- [`openapi/integration-hub-ai.yaml`](openapi/integration-hub-ai.yaml): placeholder contract for the AI call through the Integration Hub.
- [`ui-prototype/index.html`](ui-prototype/index.html): clickable HTML prototype of every admin screen, to port to SvelteKit.

Measured numbers quoted in these docs come from benchmarks run on Oct 30, 2026 in a 2 vCPU sandbox (Go 1.24.7, 585-byte JSON payload). They are for comparing options, not production SLOs; Phase 1 re-runs them on the pinned Go toolchain.
