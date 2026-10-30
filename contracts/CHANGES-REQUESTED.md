# Contract changes requested (from `agent/docs`)

Found while writing the docs, examples and deploy files. Each item: what, why, who is affected. The docs follow the choice marked **Docs assume** until the lead decides.

## 1. Version string format: `name@17` or `name@v17`

- **What:** `engine.proto` comments say `decision_version = "lead_routing@v17"` and `gate_version = "new_checkout@v5"`; the OpenAPI (`DecideResult.decisionVersion` example `lead_routing@17`, `AuditEvent.resource` `decisions/lead_routing@18`) and `engine.Result` (`"lead_routing@17"`) use no `v`.
- **Why:** callers log and compare this string; two formats will break replay tooling and log search.
- **Affected:** engine, sidecar, control plane, clients, UI.
- **Docs assume:** `lead_routing@17` everywhere. Fix the proto comments.

## 2. Bucketing input is not fully specified

- **What:** `SegmentSpec.rollout` says `murmur3(salt + key) mod 10000`; `RolloutEntry` says `bucket(salt, unit) < percent*100`. The murmur3 variant (32-bit x86? seed?) and how salt and key are joined (no separator? `:`?) are not stated.
- **Why:** assignments must be identical across engine builds, the control plane's `segments/{name}/check`, and any future client; fixtures cannot be written without it. The OpenAPI example test `fast track` in `CreateLeadRoutingDecision` depends on the bucket of unit `L-1029384` under salt `eligibility.r4` (25% rollout) and may fail depending on this choice.
- **Affected:** engine, control plane, tests.
- **Proposal:** `bucket = murmur3_x86_32(utf8(salt + "." + key), seed 0) mod 10000`; add a fixture table (salt, key, bucket) to the contract; drop the rollout from that example test or pick a unit whose bucket is fixed by the fixture.
- **Docs:** `examples/definitions` tests avoid rollout-dependent outcomes.

## 3. Hub placeholder: `CompletionResponse.output` is `type: object`

- **What:** `AINode.responseSchema` may describe any JSON type (the docs say output schemas can be any type), but the placeholder response's `output` is `type: object`.
- **Why:** a string or number answer cannot be returned.
- **Affected:** sidecar `internal/hub`, Hub owner.
- **Proposal:** `output: {}` (any JSON) in the placeholder.

## 4. `pin_version` type differs

- **What:** `DecideOptions.pin_version` is a `string` in `engine.proto`; `DecideRequest.options.pinVersion` is an `integer` in the OpenAPI; `engine.Request.PinVersion` is `int64`.
- **Why:** clients and docs must say what to send (`"17"`, `17`, `"lead_routing@17"`?).
- **Affected:** sidecar, clients, Remote Decision API.
- **Proposal:** proto comment: "decimal version number, e.g. \"17\"" (keeps the field type), or a new `int64` field.

## 5. gRPC error reasons and domain are not in the proto

- **What:** `engine.Error` lists `DECISION_NOT_FOUND`, `INPUT_SCHEMA`, `INPUT_TOO_LARGE`, `NODE_ERROR`; the sidecar also needs `NOT_READY`. The `google.rpc.ErrorInfo.domain` is not stated anywhere.
- **Why:** clients branch on `reason`; every language client needs the full list.
- **Affected:** sidecar, all clients.
- **Docs assume:** domain `gearbox.dev`; reasons as above plus `NOT_READY`; mapping in `docs/04-reference.md` §8.1. Add both to the `engine.proto` header.

## 6. HTTP status for some problem codes

- **What:** `NOT_APPROVED`, `TESTS_FAILED` and `BUNDLE_TOO_LARGE` are listed as codes but no response says which status carries them.
- **Docs assume:** `422` for all three.

## 7. Definitions-file format (offline mode, `gearboxctl push|pull`)

- **What:** no contract defines the YAML files read by `gearbox-sidecar --definitions-dir` and `gearboxctl`.
- **Proposal (used by `examples/definitions`):** one file per resource in `<kind>/` directories; content = `engine.Resource` JSON shape (`kind`, `name`, `version`, `spec`, `segmentMembers`, `lookupRows`) plus `description`. `version` is what references pin (`lead_input@1`).
- **Affected:** sidecar, CLI. Ticket GBX-005.

## 8. HMAC key distribution for `pii` segments

- **What:** static segments with `pii: true` ship members as HMAC-SHA256 hex, so the sidecar must HMAC the condition `key` with the same installation key. `bundle.proto` and the sidecar API do not say how the sidecar gets that key; decision-log HMACs need it too.
- **Docs assume:** `GEARBOX_HMAC_KEY_FILE` on server and sidecar (same secret, distributed like the credential).
- **Alternative:** the head response or a sidecar endpoint returns a per-app derived key.

## 9. New pods need the API for the head

- **What:** the archive design let new pods start from the object store when the API is down. With the contract, sidecars get the head and presigned URLs only from `GET /sidecar/v1/head`; `heads/<app>/<env>.json` exists in the store but sidecars have no credentials for it.
- **Why:** availability expectation must be stated.
- **Docs assume:** new pods need the API (or a cached/fallback bundle). If start-without-API is required, allow an optional read-only object-store/CDN URL for the head pointer.

## 10. Remote Decision API has no batch endpoint

- **What:** the sidecar has `DecideBatch`; the HTTP runtime API has only single `decide`.
- **Proposal:** `POST /apps/{app}/environments/{env}/decide-batch` with up to 100 items, same per-item error shape.

## 11. `NodeStep` lacks `error`

- **What:** `PathStep` (OpenAPI) has `error`; `NodeStep` (proto) does not.
- **Proposal:** add `string error = 5;` to `NodeStep` so callers see which node failed when `fallback_used` is true.

## 12. Gates and experiments shared across apps

- **What:** the archive design allowed one gate/experiment to be used by several apps with coordinated publishing. The contract scopes them per app (`/apps/{app}/gates`).
- **Docs assume:** per app. Record explicitly in `CONTRACTS.md` that cross-app sharing is out of scope for v1.

## 13. `DecisionInfo.kind` values differ

- **What:** `engine.proto` `DecisionInfo.kind` lists `rules | table | transform | graph | gate | experiment`; `engine.DecisionInfo.Kind` lists `graph | rules | table | transform`; `ResourceSummary.subtype` is `graph | rules | table | transform`.
- **Proposal:** `graph | rules | table | transform` everywhere (gates and experiments are not decisions).

## 14. Sidecar base path

- **What:** the OpenAPI server URL is `http://localhost:8080/api/v1`, so sidecar endpoints are `/api/v1/sidecar/v1/head`. The double version segment is easy to get wrong in sidecar config.
- **Docs assume:** `GEARBOX_SERVER_URL` is the bare origin; the sidecar appends `/api/v1/sidecar/v1/…`.

## 15. Workspace: example module

- **What:** `examples/go-service` is a separate module (it uses `replace` to `clients/go` and `gen/go`). It is not in `go.work`.
- **Proposal:** add `./examples/go-service` to `go.work` once `clients/go` builds, so `go vet ./...` covers it.
