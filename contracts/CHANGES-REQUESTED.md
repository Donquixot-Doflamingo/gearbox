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

## 16. `scripts/dev/restricted-env.sh`: wrong `gopkg.in/yaml.v3` mirror version (not a contract; lead-owned path)

- **What:** the MIRRORS entry is `github.com/go-yaml/yaml v0.0.0-20220527083530-f6f7691f1bdb`; that revision does not exist (`unknown revision`). The module cache and GitHub have `v0.0.0-20220527083530-f6f7691b1fde`.
- **Why:** any module that needs `gopkg.in/yaml.v3` fails to build in the restricted sandbox.
- **Affected:** every Go module using YAML (control plane, CLI, sidecar `--definitions-dir`). Also consider adding mirrors for `gopkg.in/yaml.v2` (`github.com/go-yaml/yaml v2.1.0+incompatible`), `golang.org/x/term` and `golang.org/x/telemetry`, which test dependencies pull in.
- **Also:** `scripts/check-names.sh` still excludes `docs/archive/**`; the archive is deleted on `agent/docs`, so the exclusion can go.

---


Each item: what, why, who is affected. The control plane already works around all of them.

1. **Declare error responses on every operation.** Many operations only declare 200/201, yet return 400 (invalid parameters), 401, 403 (role/app scope), 404 (unknown app/resource) and 409 (state conflicts, e.g. approving a withdrawn change request). Proposal: add `default: { $ref: '#/components/responses/Problem' }` (problem+json) to every operation. *Affects:* UI and client generators (typed error handling). Today the server returns problem+json for these and the contract tests accept undeclared ≥400 statuses only if they are valid Problems.

2. **Document the extra problem codes** in `Problem.code` examples: `BREAKING_CHANGE` (422, cutting an incompatible schema version without `allowBreaking`), `NO_CHANGES` (409, cutting an unchanged draft), `INVALID_STATE` (409, change-request transition not allowed), `NOT_ROLLING_OUT` (409), `BUDGET_EXCEEDED` (422, publish benchmark over `budgetMicros`), `BUSY` (409, another publish holds the lease), `NODE_ERROR` (500 from `decide` when a node fails without fallback), `INPUT_SCHEMA` (field error code). Also declare `409` on `approveChangeRequest`/`rejectChangeRequest`/`withdrawChangeRequest`, `422` on `cutVersion` for `BREAKING_CHANGE`, and `500` on `decide`. *Affects:* UI, docs (docs/04-reference.md §8.2 lists `NOT_APPROVED` as 422; the lead decided **409**, which the server returns).

3. **Add `GET /auth/callback`** (OIDC redirect target, sets `gbx_session`, 302 to `returnTo`). It is described in prose but is not a path. *Affects:* UI, docs.

4. **`getBlob` security should be `[]`.** Blob URLs are self-authenticating (`sig`/`exp` HMAC), like presigned S3 URLs, and sidecars should treat both the same way (no `Authorization` header — S3 rejects presigned requests that carry one). The server verifies a credential only if one is sent. *Affects:* sidecar.

5. **AI shadow report inputs.** `DecisionLogRecord.ai[]` has no field for what the deciding path said, so `ShadowReport.matrix` cannot be computed reliably. Proposal: add `ai[].decided` (label of the deciding path) and define `ai[].result` as the AI's label or one of `timeout`, `schema_error`, `error`; optionally `ai[].vars` (non-PII) for `disagreements[].vars`. The server reads these today and falls back to `output.route`/`output` when `decided` is missing. *Affects:* sidecar (log writer), engine (AI node outcome), UI.

6. **PII segment member hashing.** `bundle.proto` says members of a `pii` segment are HMAC-SHA256 hex strings but not under which key. The control plane uses the installation HMAC key (`GEARBOX_HMAC_KEY_FILE`, same key the sidecar config documents for "pii segment lookups") over the raw member string. Please state this in `bundle.proto` and the OpenAPI `SegmentSpec.static.pii` description. *Affects:* engine/sidecar.

7. **Bundle sets may contain several versions of one schema.** The control plane includes every schema version pinned by any resource in the set (plus rollout "from" decision versions). The data-plane engine handles this; please note it on `engine.Set` and `ResourceEntry.live`. *Affects:* engine.

8. **Remove `examples` next to `allOf` in `AINode`** (and any other schema-level `examples` siblings of `$ref`/`allOf`): OpenAPI 3.0-era validators (kin-openapi) reject the document in strict validation. Use `description` or move examples into `components/examples`. *Affects:* tooling only.

9. **Per-decision runtime stats.** `ResourceSummary.stats` (p99, calls/min, fallback rate per decision) can only be approximated from sampled decision logs. Proposal: `Heartbeat.decisions: { <name>: { p99Micros, callsPerMinute, fallbackRate } }`. *Affects:* sidecar, UI.

10. **Rollout percent 0.** `ReleaseCreate.percent` says "0 returns everyone to the previous version". The server keeps the rollout at 0 % (so a later step can resume) rather than ending it; please confirm or specify that 0 ends the rollout. *Affects:* UI, engine (a rollout with percent 0 must be valid).

11. **Operation count.** The contract has 67 operations (the plan mentioned 68).

---


Requests from the admin UI (`agent/ui`). Each says what, why and who is affected. The UI works around every
item today; nothing here blocks it.

## UI-1 · Evaluate an unsaved spec

- **What:** add an optional `spec` (`DecisionSpec`) to `EvaluateRequest` (`POST /apps/{app}/decisions/{name}/evaluate`).
  When present, evaluate it instead of the saved draft or a version (same validation as `validate`).
- **Why:** the transform editor's live preview and the test console should reflect what the author is typing.
  Today the UI offers "Save and preview", which creates a draft revision per preview.
- **Affects:** control plane (evaluate handler), UI.

## UI-2 · Kill-switch state is not readable

- **What:** either add `killed: boolean` and `gate: Name` to `Release` when `type: kill_switch`, or expose the
  current state per environment (for example `killed: true` in `LiveState` of gates in `ResourceSummary.live`,
  or `GET /apps/{app}/environments/{env}/kill-switches`).
- **Why:** `ReleaseCreate` carries `killed`, but nothing returns it, so the UI cannot tell whether a gate is
  currently killed. The UI derives the state from `kill_switch` releases and reads `killed`/`gate` if the server
  adds them (the mock does); otherwise it shows "changed (direction not reported)".
- **Affects:** control plane, UI.

## UI-3 · List and revoke personal access tokens

- **What:** `GET /tokens` (the caller's tokens: id, label, createdAt, expiresAt, lastUsedAt) and
  `DELETE /tokens/{id}`.
- **Why:** only `POST /tokens` exists, so people cannot review or revoke tokens they created.
- **Affects:** control plane, UI, gearboxctl.

## UI-4 · Document the error codes the UI branches on

- **What:** add to `Problem.code` examples: `BREAKING_CHANGE` (cutting a schema version with breaking changes
  and `allowBreaking: false`), `NO_CHANGES` (cutting a version identical to the latest), `INVALID_STATE`
  (approving or withdrawing a change request that is not open), `ALREADY_APPROVED`, `NOT_ROLLING_OUT`
  (`type: rollout` for a decision without a rollout), and `CSRF` (403 when `X-CSRF-Token` is missing or stale;
  the client refetches `/session` on it).
- **Why:** "branch on `code`, never on `detail`" needs the codes to be listed.
- **Affects:** control plane, UI.

## UI-5 · `ResourceSummary.subtype` for more kinds

- **What:** document `subtype` for experiments (`draft | running | stopped`) and schemas (root type, e.g.
  `object`, `string`).
- **Why:** list screens show them without fetching every resource. The UI shows them when present.
- **Affects:** control plane, UI.

## UI-6 · AI nodes across decisions (optional)

- **What:** `GET /apps/{app}/ai-nodes` returning `{decision, nodeId, mode, model, timeoutMs, live: {env: version}}`.
- **Why:** the AI nodes screen currently loads every decision's draft to find AI nodes (N+1 requests).
- **Affects:** control plane, UI.
