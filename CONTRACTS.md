# Contracts

These files are the agreement between the parts of Gearbox. Code follows them; changes need agreement from every owner involved.

| Contract | File | Between |
|---|---|---|
| Control-plane HTTP API (OpenAPI 3.1) | `contracts/openapi/gearbox-admin.v1.yaml` | UI ↔ control plane; CLI/CI ↔ control plane; sidecar ↔ control plane (`/sidecar/v1/*`) |
| Definition model (schemas, decisions, nodes, conditions, segments, lookups, gates, experiments, layers) | same file, `components/schemas` | control plane ↔ engine ↔ UI (one JSON model everywhere) |
| Sidecar API (gRPC) | `proto/gearbox/v1/engine.proto` | apps ↔ sidecar |
| Bundle format | `proto/gearbox/v1/bundle.proto` | control plane (writer) ↔ sidecar (reader) |
| Engine Go API | `engine/api.go` | engine ↔ sidecar, engine ↔ control plane |
| AI call | `contracts/openapi/integration-hub-ai.yaml` (placeholder; owner fills in) | sidecar ↔ Integration Hub |

## Decisions recorded with the contracts

- **HTTP + OpenAPI for the control plane, gRPC for the sidecar.** Browsers cannot speak native gRPC (they need gRPC-Web or Connect through a proxy), and the control plane is not latency-critical, so it is plain HTTP/JSON described by OpenAPI. The UI gets typed clients from the YAML. The sidecar stays gRPC over a Unix socket because that path is latency-critical.
- **Schema registry first.** `rules` and `table` nodes must point at registered, versioned input and output schemas; saving one without them fails with `422 SCHEMA_REQUIRED`/`SCHEMA_NOT_FOUND`. Transforms may come before a rules node to reshape input to its schema; their `outputSchema` is optional. AI nodes use an inline `responseSchema` instead.
- **Schemas** are JSON Schema 2020-12 (restricted subset): nested objects to any depth, arrays, nullable via `type: [x, "null"]`, enums, PII marker `x-gearbox-pii`. Root may be any type.
- **Segments in conditions**: `{segment, op: IN_SEGMENT|NOT_IN_SEGMENT, key | attributes}` combined with field conditions through `all`/`any`/`not`. Segment types: `static` (members), `dynamic` (conditions over attributes, evaluated at decision time), `rollout` (stable % of units).
- **Object store**: S3 (any S3-compatible service); filesystem store for local development.
- **AI**: one HTTP call to the Integration Hub per AI node; contract is a placeholder.
