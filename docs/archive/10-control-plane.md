# 10 · Control plane (backend)

The control plane is where decisions are written, reviewed, versioned and published. It is **never on the request path**: if it is down, sidecars keep answering with the last bundle.

```
 Admin UI (SvelteKit) ──Connect/JSON──┐
 CLI / CI (gearboxctl) ──Connect/gRPC─┤
                                      ▼
                         ┌──────────────────────────┐
                         │ gearbox-server (Go)      │
                         │  admin API · auth · RBAC │
                         │  validate · test · publish│
                         │  feed · fleet · audit    │
                         └───┬────────┬────────┬────┘
                    MongoDB  │  Redis │  S3    │  log store (via adapter)
               definitions,  │ pub/sub│ bundles│  ClickHouse / Loki / ES
               versions,     │ feed   │ signed │
               releases,audit│ fan-out│        │
                             ▼        ▼        ▼
                    BundleService.Watch / GET head  ◀── sidecars
```

## 1. Process layout

One binary, `gearbox-server`, stateless, horizontally scaled (≥ 2 replicas). Ports: `8080` Connect (admin API + `BundleService`), `9464` metrics/health. Background work (publishing, outbox webhooks, log retention) runs under a Mongo lease so only one replica does it.

| Package | Job |
|---|---|
| `internal/api` | Connect handlers, request validation (protovalidate), error mapping |
| `internal/auth` | OIDC login (go-oidc), session cookies for the UI, PATs for CLI/CI, app credentials for sidecars |
| `internal/rbac` | role checks per tenant / app / environment |
| `internal/store` | MongoDB repositories (mongo-driver v2), optimistic concurrency by `revision` |
| `internal/validate` | calls `engine` to compile, type check and run test cases |
| `internal/publish` | builds, signs (KMS) and uploads bundles; writes the head pointer; issues presigned URLs; emits feed events |
| `internal/feed` | `Watch` streams, Redis fan-out, ETag head endpoint |
| `internal/fleet` | sidecar heartbeats (generation, version, last error) |
| `internal/approvals` · `internal/audit` | change requests, reviews, append-only audit |
| `internal/logsearch` | adapter interface over the decision-log store |
| `internal/outbox` | webhooks on publish/approval (replaces the company webhook library) |

## 2. Data model (MongoDB)

| Collection | Key fields |
|---|---|
| `tenants`, `members` | tenant, OIDC subject, roles |
| `apps` | name, tenant, owners, environments, bundle size limit |
| `credentials` | app, environment, hashed secret, created_by, last_used |
| `decisions` | app, name, latest draft, `revision` |
| `decision_versions` | decision, version (immutable), definition JSON, content hash, author, created_at |
| `gates`, `experiments`, `layers`, `holdouts` | same draft + immutable version pattern; plus the list of apps whose bundles use them |
| `segments`, `lookups` | version records only (name, version, row count, sha256); the rows live in the object store |
| `change_requests` | items (resource + version), environment, status, reviewers, comments |
| `releases` | environment, items, rollout %, generation, signed manifest hash, status |
| `bundle_heads` | app, environment, generation, manifest hash |
| `fleet` | pod, app, environment, sidecar version, generation, last_seen, last_error |
| `audit` | actor, action, resource, before/after hash, time (TTL never) |
| `outbox` | event, payload, attempts, next_attempt |

## 3. Admin API (Connect services in `proto/gearbox/admin/v1`; `buf.yaml` sits at `proto/`)

| Service | Main methods |
|---|---|
| `AppService` | ListApps, CreateApp, UpdateApp, CreateCredential, RevokeCredential |
| `DecisionService` | ListDecisions, GetDecision, SaveDraft, ListVersions, GetVersion, Diff, Validate, RunTests, Evaluate (test console) |
| `GateService` · `ExperimentService` | CRUD, Start/Stop experiment, results link |
| `SegmentService` · `LookupService` | CRUD, Upload (CSV), Preview |
| `ChangeRequestService` | Create, Get, List, Comment, Approve, Reject, Withdraw |
| `ReleaseService` | Publish (from approved change request), SetRollout, Promote, Rollback, ListReleases |
| `FleetService` | ListSidecars, GetSidecar |
| `LogService` | SearchDecisions, GetDecisionRecord, ListUpdates |
| `AuditService` | ListEvents |
| `ReleaseService` (data) | PublishData: segment or lookup versions that pass all dependent tests, without a change request when the app allows data releases |
| `BundleService` (sidecar-facing) | Watch (stream of `{generation, manifest URL, presigned resource URLs}`), Heartbeat. Bundle bytes come from the object store, not from this API |
| `DecisionRuntimeService` (Remote Decision API) | Decide, DecideBatch, CheckGate, GetVariant over Connect for callers that cannot run a sidecar; served by separate stateless replicas that sync like sidecars |

Every mutating call takes the resource `revision` and fails with `ABORTED` if someone else saved first.

## 4. Lifecycle of a change

```
 draft ──Validate+RunTests──▶ version vN ──ChangeRequest──▶ approved ──Publish──▶ release (generation G)
   ▲                                          │ reject                              │
   └──────────────────────────────────────────┘                     SetRollout 5%→25%→100% · Rollback
```

1. Author saves a draft; the server compiles it with the same `engine` package as the sidecar and returns errors with line/field positions.
2. `RunTests` runs the decision's test cases; a version can only be cut when all pass.
3. A change request groups one or more versions for one environment. Rules per environment: `dev` none, `stage` 1 approver, `prod` 1 approver who is not the author (configurable).
4. `Publish` builds the full bundle for the app and environment, re-runs every test, runs the cost check and publish benchmark ([05](05-rules-and-expressions.md) §8), sets `min_engine_version`, signs the manifest, uploads resources then the envelope then the head pointer, writes audit, emits the feed event and webhooks. A release that changes a shared gate or experiment publishes every app that uses it.
5. Rollout and rollback are new generations; nothing is edited in place.

## 5. Roles

| Role | Can |
|---|---|
| viewer | read everything in the tenant, search logs (redacted) |
| author | + save drafts, run tests, create change requests |
| approver | + approve/reject change requests, set rollout, rollback |
| admin | + apps, credentials, members, environments, webhooks |

Roles can be narrowed to an app. Break-glass rollback is allowed to approvers without a change request and is flagged in audit.

## 6. Environments and promotion

Each app has `dev`, `stage`, `prod` (names configurable). Promotion copies the exact version (same content hash) to the next environment through a new change request, so what was tested is what ships.

## 7. Signing keys

- One active Ed25519 signing key per installation, held in a KMS; its `key_id` is written into every envelope.
- Rotation: add the new public key to `gearbox-trusted-keys` everywhere → switch `GEARBOX_SIGNING_KEY_ID` → after all sidecars run a generation signed with the new key, remove the old public key.
- Sidecars refuse a manifest whose generation is not greater than the active one, so an old signed bundle cannot be replayed.

## 8. Source of truth and `gearboxctl`

MongoDB holds definitions and history. `gearboxctl` (CLI, same Connect API) imports and exports YAML (`gearboxctl decision pull|push`), runs tests locally with the engine package, opens change requests from CI and exports offline bundles. Teams that want review in Git keep YAML in their repo and push it from CI with `gearboxctl`; the control plane still decides what is released.

## 9. Configuration (env vars)

`GEARBOX_MONGO_URI`, `GEARBOX_REDIS_URL`, `GEARBOX_BUNDLE_STORE` (`s3://bucket/prefix` or `file:///path`), `GEARBOX_SIGNING_KEY` (KMS URI or file) and `GEARBOX_SIGNING_KEY_ID`, `GEARBOX_OIDC_ISSUER`, `GEARBOX_OIDC_CLIENT_ID`, `GEARBOX_OIDC_CLIENT_SECRET_FILE`, `GEARBOX_PUBLIC_URL`, `GEARBOX_LOG_STORE` (adapter URI), `GEARBOX_OTLP_ENDPOINT`.
