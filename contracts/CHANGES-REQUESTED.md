# Contract changes requested by the control plane (agent/controlplane)

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
