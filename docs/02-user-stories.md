# 02 · User stories

Stories are grouped by feature. Each has an ID, a persona, the story and **testable** acceptance criteria (AC). Endpoint paths are relative to `/api/v1` ([OpenAPI](../contracts/openapi/gearbox-admin.v1.yaml)); RPCs are in [`engine.proto`](../proto/gearbox/v1/engine.proto). Ticket IDs (`GBX-…`) are in [`tickets/TICKETS.md`](../tickets/TICKETS.md).

## Personas

| Persona | Role in Gearbox | Wants |
|---|---|---|
| **Asha, rule author** | `author` | Change routing logic safely without a code deploy; see the effect before shipping |
| **Ravi, reviewer/approver** | `approver` | Understand exactly what changes, approve it, roll it out gradually, undo it fast |
| **Dev, service developer** | owns the lead service; app credential | Call decisions in < 1 ms with a fallback; never be paged because of the decision engine |
| **Meera, platform operator** | `admin` | Install and run Gearbox; keep sidecars in sync; manage keys, credentials and members |
| **Kiran, analyst** | `viewer` | Explain past decisions, read experiment exposures and AI shadow results |

## Schema registry

**US-001 Register a schema** — As Asha, I register `lead_input` so rules can be type-checked.
- AC1 `POST /apps/lead_service/schemas` with nested objects, an array with `maxItems`, a nullable field and `x-gearbox-pii` returns `201` with a draft at revision 1.
- AC2 An `examples[]` entry that does not validate returns `422 VALIDATION_FAILED` with an `errors[].path`.
- AC3 `POST …/schemas/lead_input/versions {revision: 1}` returns `201` with `version: 1` and a `contentHash`.
- AC4 A schema whose root is `type: string` can be registered and used as an `outputSchema`.

**US-002 Rules need schemas** — As Ravi, I want every rule and table node bound to registered schemas so that inputs and outputs are predictable.
- AC1 Saving a decision whose `rules` node lacks `inputSchema` returns `422 SCHEMA_REQUIRED` with `errors[0].path = /nodes/<i>/inputSchema`.
- AC2 Pointing at `lead_input@9` when only `@1` exists returns `422 SCHEMA_NOT_FOUND`.
- AC3 A literal `then` that does not match `outputSchema` is a validation error at save.
- AC4 An AI node with only an inline `responseSchema` saves successfully.

**US-003 Evolve a schema safely** — As Asha, I add an optional field without breaking decisions that use the old version.
- AC1 `POST …/compatibility` reports `property_added`, `breaking: false` for a new optional property.
- AC2 Making a property required reports `became_required`, `breaking: true`; cutting the version without `allowBreaking` returns `422`.
- AC3 With `allowBreaking: true` the version is cut with `breaking: true`; decisions pinned to the old version still validate and run.
- AC4 `GET …/schemas/lead_input/usages` lists each user with `usesVersion` and `where`.

**US-004 Validate a document** — As Dev, I check a sample payload against a schema version.
- AC1 `POST …/schemas/lead_input/versions/1/validate-document` returns `{valid: false, errors: [{path: /car/price, …}]}` for a missing required field.

## Rule sets and conditions

**US-010 Write a rule set with structured conditions** — As Asha, I build rules from field conditions without writing code.
- AC1 Every operator in the reference table compiles and behaves as documented (golden tests per operator).
- AC2 `first_match` returns the first matching rule by `priority` then list order; `all_matches` returns `{matches: [...]}`.
- AC3 A disabled rule never matches.
- AC4 Comparing an integer field with a string value is reported as `TYPE_MISMATCH` with the condition's JSON pointer.

**US-011 Use Expr conditions** — As Asha, I write `{expr: …}` for logic the builder cannot express.
- AC1 Unknown fields → `UNKNOWN_FIELD`; unknown functions → `UNKNOWN_FUNCTION`; non-boolean `expr` → `TYPE_MISMATCH`.
- AC2 `now()` is not available; `ctx.now` is.

**US-012 Missing fields are never "true by accident"** — As Ravi, I want a missing field to fail positive and negative operators alike.
- AC1 With `car.km` absent: `GT`, `NEQ`, `NOT_IN`, `NOT_CONTAINS`, `NOT_MATCHES` are false; `NOT_EXISTS` is true; `EXISTS` is false.
- AC2 `input.missing.deep.path` evaluates to nil without error; `nil + 1` routes to `onError`/fallback.

**US-013 Rule rollout** — As Asha, I limit a new rule to 25% of leads.
- AC1 With `rollout: {percent: 25}` the share of 100,000 random units that match is 25% ± 0.5%.
- AC2 The same unit always gets the same result across sidecars and restarts.
- AC3 Calls without `unit_id` never match the rule.

## Segments

**US-020 Static segment** — As Asha, I upload blocked cities and use them in a rule.
- AC1 `PUT …/segments/blocked_cities/members` (CSV with optional `key` header) stores members and returns `{total, revision}`.
- AC2 With `pii: true`, members are stored and shipped as HMACs; the raw value never appears in the bundle.
- AC3 More than 1,000,000 members returns `422`.

**US-021 Dynamic segment** — As Asha, I define `metro_premium` by conditions over city and price and use it in a rule.
- AC1 Creating a dynamic segment without `attributesSchema` returns `422`.
- AC2 A segment condition whose `attributes` do not match the segment's `attributesSchema` is a validation error.
- AC3 Membership is evaluated at decision time: changing the input price changes the result without a new release.
- AC4 `POST …/segments/metro_premium/check {attributes: {city: Gurgaon, price: 950000}}` → `{member: true, reason: conditions_matched}`.

**US-022 Rollout segment** — As Asha, I target a stable 10% of units.
- AC1 `check {unit}` returns the bucket in `reason` (e.g. `bucket 731 < 1000`).
- AC2 Changing `salt` reshuffles membership; keeping it keeps membership across versions.

**US-023 Combine a field condition and a segment** — As Asha, I write `all: [field, dynamic segment, rollout segment]`.
- AC1 The case-study table in [01 §3](01-features.md#3-segment-conditions-static-dynamic-rollout) passes as decision test cases (rollout cases with fixed units).
- AC2 `not: {segment …}` and `any: [...]` behave as boolean NOT/OR.

## Decision tables

**US-030 Build a decision table** — As Asha, I express price bands as rows.
- AC1 Cell syntax `-`, `< n`, `[a..b]`, JSON list and `value` expressions each have golden tests.
- AC2 `first`, `collect` and `priority` hit policies return the documented result; `unique` with overlapping rows fails validation.
- AC3 An `outputs` entry not in `outputSchema` is a validation error.
- AC4 The path shows the matched row ID.

## Transforms

**US-040 Reshape input before rules** — As Asha, I map the raw lead to a flat `eligibility_input`.
- AC1 Fields are assigned in order and later fields can read `out`.
- AC2 `defaults` fill only missing paths; `drop` runs last; `omitNil` omits nil results.
- AC3 With `outputSchema` set, a next rules node whose `inputSchema` does not accept it fails validation (`SCHEMA_EDGE_MISMATCH`).
- AC4 A field derived from a PII field is still PII in the output (cannot be logged or sent to a prompt).

## Decision graphs

**US-050 Branch with switch** — As Asha, I route eligible leads to pricing and others to reject.
- AC1 The first true case is taken; otherwise `default`.
- AC2 Cycles, unreachable nodes and `NODE_MAY_NOT_HAVE_RUN` references are rejected at save with JSON pointers.

**US-051 Error handling** — As Dev, I always get an answer.
- AC1 A node error with `onError` runs that node; without it the decision returns `fallback` and `fallback_used: true`.
- AC2 Entry-node input that fails its schema returns gRPC `INVALID_ARGUMENT` with reason `INPUT_SCHEMA` and field errors.

**US-052 Reuse with subdecision** — As Asha, I call `lead_scoring` from `lead_routing`.
- AC1 The subdecision's output is available as `nodes.<id>`; a cycle between decisions is rejected.

**US-053 Test cases** — As Asha, I keep test cases with the decision.
- AC1 `POST …/decisions/lead_routing/tests/run` returns `passed`, `failed` and per-case mismatches with paths.
- AC2 `expectPath: ["eligibility:r4", "output:ok"]` and `expectError: INPUT_SCHEMA` are checked.
- AC3 A version cannot be cut while any test fails (`422 TESTS_FAILED`).

**US-054 Test console** — As Asha, I run a draft with a sample input.
- AC1 `POST …/evaluate {source: draft, environment: dev, input, ctx}` returns output, `path` with each node's input/output and `micros`.
- AC2 AI nodes are skipped by default (`skipAI: true`) and take `onError`.

## Lookups

**US-060 Lookup table** — As Asha, I map city → zone.
- AC1 CSV `key,value` upload validates each value against `valueSchema`; one bad row rejects the upload with its line.
- AC2 A `lookup` node returns `default` for a missing key; `lookup(name, key, default)` works in any expression.

## Gates and kill switch

**US-070 Gate** — As Dev, I check `dealer_auction_v2` per unit.
- AC1 `CheckGate` returns `value`, `gate_version`, `rule_id` and `bundle_generation`.
- AC2 Disabled → `rule_id: disabled`; no unit with only rollout rules → `no_unit`.

**US-071 Kill switch** — As Ravi, I turn a feature off in seconds.
- AC1 `POST …/releases {type: kill_switch, gate, killed: true, reason}` by an approver creates a new generation; an author gets `403`.
- AC2 Every connected sidecar returns `{value: false, rule_id: killed}` within 5 s (p95) of the release.
- AC3 The audit log records actor and reason.

## Experiments

**US-080 Run an experiment** — As Kiran, I want stable, exclusive assignments and a complete exposure log.
- AC1 Weights 50/50 give 50% ± 0.5% over 100,000 units; the same unit always gets the same variant.
- AC2 Reasons `assigned`, `holdout`, `not_targeted`, `layer_conflict`, `stopped`, `no_unit` are each covered by tests.
- AC3 Two experiments in one layer never both assign the same unit.
- AC4 Every assignment produces exactly one exposure record, regardless of decision-log sampling; drops are counted in `gearbox_log_dropped_total{log="exposure"}`.

## AI node

**US-090 Shadow an AI node** — As Asha, I compare an AI step with my rules without affecting callers.
- AC1 In `shadow` mode the caller's latency is unchanged (benchmark within noise) and the deciding path is `onError`.
- AC2 The shadow report shows calls, agreement, matrix, latency p50/p95, timeout and schema-failure rates.
- AC3 A `var` derived from a PII field fails validation with `PII_IN_PROMPT`.

**US-091 Go inline** — As Ravi, I approve switching to `inline` as a new version.
- AC1 Timeout, Hub error, `429` and schema-invalid answers each take `onError`; no call waits longer than `timeoutMs` (+ 5 ms).
- AC2 The Hub call carries `Idempotency-Key: <request_id>:<node_id>`.
- AC3 The decision log has model, latency, cache hit and answer hash; prompt/answer only with `logging.aiPayloads`.

## Versions, change requests, releases

**US-100 Concurrent editing** — As Asha, I do not overwrite a colleague's change.
- AC1 Saving with a stale `revision` returns `409 REVISION_CONFLICT`.

**US-101 Diff** — As Ravi, I see exactly what changed.
- AC1 `GET …/diff?from=17&to=18` returns a unified YAML diff and structured `changes`.

**US-102 Change request and approval** — As Ravi, I approve production changes made by others.
- AC1 Opening a change request validates and tests the would-be bundle; a failure returns `422` and no request is opened.
- AC2 With `authorCanApprove: false` the author's approve returns `403 SELF_APPROVAL`.
- AC3 With `requiredApprovals: 2` the request stays `open` after one approval.
- AC4 Publishing an unapproved request returns a problem with `code: NOT_APPROVED` and publishes nothing.

**US-103 Gradual rollout and rollback** — As Ravi, I ship to 5% and widen.
- AC1 During a rollout, the share of units on the new version matches `percent` ± 0.5% and a unit never flips between versions at the same percent.
- AC2 `rollout percent: 0` returns everyone to the previous version; `100` completes it.
- AC3 `rollback toGeneration: 41` creates a new, higher generation with the content of 41; without a change request it is flagged `breakGlass` in the release and audit.

**US-104 Data release** — As Asha, I publish a new member list without a review.
- AC1 With `allowDataReleases: true` and passing tests, `{type: data}` publishes; with failing tests it returns `422 TESTS_FAILED`.
- AC2 With `allowDataReleases: false` it returns `403`.

**US-105 Promotion** — As Ravi, I promote exactly what was tested.
- AC1 A version live in `stage` opened for `prod` has the same `contentHash`.

## Sidecar and clients

**US-110 Call a decision** — As Dev, I call `lead_routing` from Go with a deadline and a fallback.
- AC1 `client.Decide(ctx, "lead_routing", input, WithUnit, WithTimeout(5ms), WithFallback(...))` returns `Decision{Output, Version, DependencyHash, Generation, FallbackUsed, FromClientFallback}`.
- AC2 With the sidecar stopped, the call returns the client fallback within the deadline with `FromClientFallback: true` and increments a counter.
- AC3 Client p99 < 1 ms at 2,000 decisions/s per pod with a 500m sidecar CPU request (load test).

**US-111 Batch** — As Dev, I evaluate several decisions in one round trip.
- AC1 `DecideBatch` with 3 items, one unknown, returns 2 responses and 1 `ItemError{reason: DECISION_NOT_FOUND}` in request order.
- AC2 More than 100 items returns `INVALID_ARGUMENT`.

**US-112 Readiness** — As Meera, I want app containers to start only after the sidecar has a verified bundle.
- AC1 `/readyz` is 503 until a verified bundle is active, then 200; `/livez` is 200 while the process runs.
- AC2 `GetStatus` returns `ready`, generation, last sync and decision list.

**US-113 Sync and last-known-good** — As Meera, I want sidecars to survive bad bundles and outages.
- AC1 Tampered envelope, unknown `key_id`, older generation, too-new `min_engine_version`, wrong blob hash, oversize bundle and failing tests each leave the active generation unchanged and produce `kept_previous` with the reason.
- AC2 After a restart with the control plane down, the sidecar is ready from its cache volume.
- AC3 Publish → all connected sidecars swapped: p95 < 5 s via the feed; ≤ 35 s via polling only.
- AC4 No failed or mixed-version responses during 1,000 consecutive swaps under load.

**US-114 Java and Python** — As Dev on another team, I call the sidecar from Java or Python.
- AC1 Following `clients/java/README.md` or `clients/python/README.md`, stubs are generated from `engine.proto` and a `decide` with deadline and fallback works against a local sidecar.

## Logs and replay

**US-120 Find and replay a decision** — As Kiran, I explain a past decision.
- AC1 `GET …/decision-logs?requestId=…` returns the record with version, dependency hash, path and `evaluatedAt`.
- AC2 Replaying with `pin_version` and `now` returns the same output and `dependency_hash` (non-AI decisions).
- AC3 PII fields never appear in records even if listed in `logging.fields`; hashed values are HMACs.

**US-121 Update log** — As Meera, I see every swap and failure.
- AC1 `GET …/update-logs?result=kept_previous` lists failed syncs with sidecar ID, generations and error.

## Remote Decision API

**US-130 Decide over HTTP** — As Dev on a batch job, I call decisions without a sidecar.
- AC1 `POST /apps/{app}/environments/{env}/decide` with an app credential returns the same output and `dependencyHash` as the sidecar for the same generation and input.
- AC2 A credential for another app or environment returns `403`; a revoked one `401`.

## gearboxctl and offline

**US-140 Push from Git** — As Asha's team, I keep YAML in our repo and push it from CI.
- AC1 `gearboxctl push examples/definitions` creates or updates drafts in dependency order (schemas first) and is idempotent.
- AC2 `gearboxctl test <dir>` runs tests locally with the engine and exits non-zero on failure.

**US-141 Offline sidecar** — As Dev, I run hermetic integration tests.
- AC1 `gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned` serves `lead_routing`.
- AC2 `--allow-unsigned` without `--definitions-dir` or `--bundle-file` exits with an error.
- AC3 `gearboxctl bundle export` + `gearbox-sidecar --bundle-file` serves the exported generation with signature check.

## Operations and security

**US-150 Credentials** — As Meera, I issue and revoke sidecar credentials per environment.
- AC1 The secret is returned once; list endpoints never return it.
- AC2 A revoked credential gets `401` on `/sidecar/v1/*`; sidecars keep serving their current bundle.

**US-151 Roles** — As Meera, I give people the least access they need.
- AC1 viewer cannot save drafts (`403`); author cannot approve or roll back; approver cannot manage members; admin can.
- AC2 A member with `apps: [lead_service]` gets `403`/`404` on other apps.

**US-152 Browser safety** — As Meera, I want the UI protected against CSRF.
- AC1 A cookie-authenticated mutating request without a valid `X-CSRF-Token` returns `403`.

**US-153 Fleet** — As Meera, I see which sidecars lag.
- AC1 `GET …/sidecars` lists sidecars seen in the last 10 minutes, lagging first, with generation, versions, status and last error; `summary.generations` counts per generation.

**US-154 Audit and webhooks** — As Meera, I get an append-only audit trail and notifications.
- AC1 Every mutating action appears in `GET /audit` with actor, action, resource.
- AC2 Webhooks are signed `X-Gearbox-Signature: sha256=<hmac>` and retried for 24 h.

**US-155 Signing key rotation** — As Meera, I rotate the signing key with no failed sync.
- AC1 Add new public key → switch the server's signing key → remove the old key after all sidecars run a new-key generation; `gearbox_sync_failures_total` stays 0.

**US-156 Run locally** — As any contributor, I start the whole system with one command.
- AC1 `make dev` starts the server (memory store, seed data), a sidecar synced to it and the UI dev server; `curl` against the example service returns a decision.
- AC2 `docker compose up` in `deploy/compose` does the same with MinIO, MongoDB and Redis.
