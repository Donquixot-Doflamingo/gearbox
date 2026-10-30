# 04 · Reference

Exact facts. The contracts are the source of truth: [OpenAPI](../contracts/openapi/gearbox-admin.v1.yaml), [`engine.proto`](../proto/gearbox/v1/engine.proto), [`bundle.proto`](../proto/gearbox/v1/bundle.proto). Configuration names below are the agreed names for the sidecar and server binaries.

1. [Sidecar setup](#1-sidecar-setup) · 2. [Sidecar configuration](#2-sidecar-configuration) · 3. [Server configuration](#3-server-configuration) · 4. [Condition operators](#4-condition-operators) · 5. [Expr cheat sheet](#5-expr-cheat-sheet) · 6. [Transform and table syntax](#6-transform-and-table-syntax) · 7. [gRPC API](#7-grpc-api) · 8. [Error codes](#8-error-codes) · 9. [Metrics](#9-metrics) · 10. [Definition files and gearboxctl](#10-definition-files-and-gearboxctl)

## 1. Sidecar setup

### 1.1 What you need

1. An app (`POST /apps`, admin) — e.g. `lead_service`.
2. An app credential per environment: `POST /apps/lead_service/environments/prod/credentials` → `secret` (shown once). Store it as a Kubernetes secret with key `credential`.
3. The installation's trusted public keys (`key_id:base64` lines) in a ConfigMap with key `keys.txt`.
4. Images built from `deploy/docker/Dockerfile.sidecar` (`gearbox-sidecar:<version>`).

### 1.2 Kubernetes native sidecar (1.29+)

The sidecar is an init container with `restartPolicy: Always`: the app container starts only after the sidecar's startup probe passes (verified bundle loaded) and the sidecar stops after the app.

```yaml
spec:
  template:
    spec:
      securityContext:
        fsGroup: 2000                          # socket is group-owned, mode 0660
      volumes:
        - { name: gearbox-socket, emptyDir: {} }
        - { name: gearbox-cache,  emptyDir: {} }
        - { name: gearbox-credential, secret: { secretName: lead-service-gearbox-prod } }
        - { name: gearbox-keys, configMap: { name: gearbox-trusted-keys } }
      initContainers:
        - name: gearbox
          image: gearbox-sidecar:1.0.0
          restartPolicy: Always
          env:
            - { name: GEARBOX_SERVER_URL, value: "https://gearbox.example.com" }
            - { name: GEARBOX_APP, value: lead_service }
            - { name: GEARBOX_ENV, value: prod }
            - { name: GEARBOX_CREDENTIAL_FILE, value: /etc/gearbox/credential }
            - { name: GEARBOX_TRUSTED_KEYS_FILE, value: /etc/gearbox-keys/keys.txt }
            - name: GEARBOX_SIDECAR_ID
              valueFrom: { fieldRef: { fieldPath: metadata.name } }
            - name: GOMAXPROCS
              valueFrom: { resourceFieldRef: { resource: requests.cpu, divisor: "1" } }
          volumeMounts:
            - { name: gearbox-socket, mountPath: /var/run/gearbox }
            - { name: gearbox-cache, mountPath: /var/cache/gearbox }
            - { name: gearbox-credential, mountPath: /etc/gearbox, readOnly: true }
            - { name: gearbox-keys, mountPath: /etc/gearbox-keys, readOnly: true }
          ports: [ { name: gbx-metrics, containerPort: 9464 } ]
          startupProbe:  { httpGet: { path: /readyz, port: gbx-metrics }, periodSeconds: 1, failureThreshold: 60 }
          readinessProbe: { httpGet: { path: /readyz, port: gbx-metrics }, periodSeconds: 5 }
          livenessProbe: { httpGet: { path: /livez, port: gbx-metrics }, periodSeconds: 10 }
          resources:
            requests: { cpu: 500m, memory: 128Mi }
            limits:   { memory: 512Mi }        # no CPU limit
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
      containers:
        - name: app
          image: lead-service:2.3.1
          env: [ { name: GEARBOX_TARGET, value: "unix:///var/run/gearbox/engine.sock" } ]
          volumeMounts: [ { name: gearbox-socket, mountPath: /var/run/gearbox } ]
```

`GOMAXPROCS` from `requests.cpu` with divisor 1 rounds up (500m → 1). Kubernetes older than 1.29: run the same container under `containers:` and make the app wait for `GetStatus.ready`.

### 1.3 Helm

`deploy/helm/gearbox-sidecar` is a **library chart**. Add it as a dependency of your service chart and include its helpers in the pod spec:

```yaml
# Chart.yaml of your service
dependencies:
  - { name: gearbox-sidecar, version: 0.1.0, repository: "file://../../deploy/helm/gearbox-sidecar" }
```

```yaml
# templates/deployment.yaml of your service
spec:
  template:
    spec:
      securityContext: {{- include "gearbox-sidecar.podSecurityContext" .Values.gearbox | nindent 8 }}
      initContainers:
        {{- include "gearbox-sidecar.container" .Values.gearbox | nindent 8 }}
      containers:
        - name: app
          env:
            {{- include "gearbox-sidecar.appEnv" .Values.gearbox | nindent 12 }}
          volumeMounts:
            {{- include "gearbox-sidecar.appVolumeMounts" .Values.gearbox | nindent 12 }}
      volumes:
        {{- include "gearbox-sidecar.volumes" .Values.gearbox | nindent 8 }}
```

| Value (`gearbox.*`) | Default | Meaning |
|---|---|---|
| `app`, `env` | – (required) | `GEARBOX_APP`, `GEARBOX_ENV` |
| `serverUrl` | – (required unless offline) | `GEARBOX_SERVER_URL` |
| `image.repository` / `image.tag` / `image.pullPolicy` | `gearbox-sidecar` / `1.0.0` / `IfNotPresent` | Sidecar image |
| `credentialSecret` / `credentialKey` | – / `credential` | Secret with the app credential |
| `trustedKeysConfigMap` / `trustedKeysKey` | `gearbox-trusted-keys` / `keys.txt` | Trusted public keys |
| `fsGroup` | `2000` | Pod `fsGroup` for socket access |
| `nativeSidecar` | `true` | `restartPolicy: Always` init container (false: render as a regular container) |
| `resources` | requests `500m`/`128Mi`, limits memory `512Mi` | No CPU limit is rendered unless you set one |
| `logSink` | `server` | `GEARBOX_LOG_SINK` |
| `extraEnv` | `[]` | More `GEARBOX_*` settings |
| `metricsPort` | `9464` | Metrics and probes |

### 1.4 docker compose

```yaml
services:
  gearbox:
    image: gearbox-sidecar:dev
    environment:
      GEARBOX_SERVER_URL: http://gearbox-server:8080
      GEARBOX_APP: lead_service
      GEARBOX_ENV: dev
      GEARBOX_CREDENTIAL: gbx_cred_dev_local_0000000000000000
      GEARBOX_TRUSTED_KEYS_FILE: /keys/trusted-keys.txt
      GEARBOX_SOCKET: /var/run/gearbox/engine.sock
    volumes: [ "gearbox-socket:/var/run/gearbox", "gearbox-keys:/keys:ro" ]
    healthcheck: { test: ["CMD", "/gearbox-sidecar", "healthcheck"], interval: 2s, retries: 30 }
  lead-service:
    environment: { GEARBOX_TARGET: "unix:///var/run/gearbox/engine.sock" }
    volumes: [ "gearbox-socket:/var/run/gearbox" ]
    depends_on: { gearbox: { condition: service_healthy } }
```

The image is distroless, so the health check uses the binary's `healthcheck` subcommand (it calls `/readyz`). Full stack: [`deploy/compose`](../deploy/compose/README.md).

### 1.5 Offline mode

```bash
gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned --watch   # YAML files, recompile on change
gearbox-sidecar --bundle-file lead_service-prod.bundle                           # exported, signed (needs trusted keys)
gearbox-sidecar --bundle-file lead_service-prod.bundle --allow-unsigned          # skip signature check (local only)
```

`--allow-unsigned` is rejected unless `--definitions-dir` or `--bundle-file` is given. In offline mode `GEARBOX_SERVER_URL` and credentials are not needed; `GEARBOX_APP`/`GEARBOX_ENV` default to `local`/`dev`.

### 1.6 Sizing

CPU ≈ 0.065 vCPU-ms per decision (≈ 15k/s per vCPU): request 500m for up to ≈ 2,000 decisions/s per pod. Memory ≈ 2 × decompressed bundle + 32 MB + log buffer. No CPU limit; `GOMAXPROCS` = CPU request.

### 1.7 Production checklist

- [ ] Every call has a deadline (2–10 ms non-AI; AI: node timeout + 5 ms) and a fallback value.
- [ ] One channel per process; no retries against the local sidecar.
- [ ] `decision_version` and `dependency_hash` logged with your business events; `request_id` and `traceparent` propagated.
- [ ] Alerts: `gearbox_sync_lag_seconds > 300`, `gearbox_sync_failures_total`, fallback rate, `gearbox_client_duration_seconds` p99.
- [ ] Sidecar resources set; no CPU limit; `GOMAXPROCS` set.
- [ ] Offline sidecar in integration tests.

## 2. Sidecar configuration

| Variable | Default | Meaning |
|---|---|---|
| `GEARBOX_SERVER_URL` | – | Control-plane base URL (required unless offline). The sidecar calls `<url>/api/v1/sidecar/v1/*` |
| `GEARBOX_APP` | – | App name (required online) |
| `GEARBOX_ENV` | – | Environment (required online) |
| `GEARBOX_CREDENTIAL_FILE` / `GEARBOX_CREDENTIAL` | – | App credential `gbx_cred_…` (file preferred) |
| `GEARBOX_TRUSTED_KEYS_FILE` / `GEARBOX_TRUSTED_KEYS` | – | Ed25519 public keys, `key_id:base64` per line or comma-separated (required unless `--allow-unsigned`) |
| `GEARBOX_SOCKET` | `/var/run/gearbox/engine.sock` | Unix socket, mode `0660` |
| `GEARBOX_TCP_ADDR` | empty (off) | Optional loopback listener, e.g. `127.0.0.1:7443` |
| `GEARBOX_TCP_TOKEN_FILE` | – | Bearer token required on TCP |
| `GEARBOX_REFLECTION` | `false` | gRPC reflection (for grpcurl; not in prod) |
| `GEARBOX_METRICS_ADDR` | `:9464` | `/metrics`, `/readyz`, `/livez` |
| `GEARBOX_CACHE_DIR` | `/var/cache/gearbox` | Last-known-good bundle (re-verified on load) |
| `GEARBOX_FALLBACK_BUNDLE` | – | Bundle file served at start when nothing newer is available |
| `GEARBOX_POLL_INTERVAL` | `30s` | Head poll interval |
| `GEARBOX_FEED` | `true` | Use the SSE change feed |
| `GEARBOX_HEARTBEAT_INTERVAL` | `30s` | Heartbeat interval |
| `GEARBOX_SIDECAR_ID` | hostname | ID in heartbeats and logs (pod name) |
| `GEARBOX_NODE_NAME` | – | Node name in heartbeats |
| `GEARBOX_MAX_BUNDLE_MB` | `64` | Refuse bigger compressed bundles (decompressed cap 4×) |
| `GEARBOX_MAX_INPUT_BYTES` | `262144` | Refuse bigger inputs (`INPUT_TOO_LARGE`) |
| `GEARBOX_LOG_SINK` | `server` online, `stdout` offline | `stdout`, `server` (`POST /sidecar/v1/logs`), `otlp` |
| `GEARBOX_LOG_SAMPLE` | environment `logSampleRate`, else `0.01` | Decision-log sampling when the decision sets none |
| `GEARBOX_LOG_BUFFER` | `65536` | Ring buffer size (records) |
| `GEARBOX_LOG_BATCH` | `1000` | Records per upload (max 1,000) |
| `GEARBOX_HMAC_KEY_FILE` | – | Installation HMAC key for logged hashes and `pii` segment lookups |
| `GEARBOX_OTLP_ENDPOINT` | – | OpenTelemetry collector (logs, traces) |
| `GEARBOX_HUB_URL` | – | Integration Hub base URL (AI nodes only) |
| `GEARBOX_HUB_TOKEN_FILE` | – | Hub credential |
| `GEARBOX_SHUTDOWN_GRACE` | `10s` | Drain time on SIGTERM |
| `GEARBOX_LOG_LEVEL` | `info` | Sidecar's own log level |

| Flag / subcommand | Meaning |
|---|---|
| `--definitions-dir <dir>` | Load YAML/JSON definitions ([§10](#10-definition-files-and-gearboxctl)) instead of syncing |
| `--watch` | With `--definitions-dir`: recompile on change |
| `--bundle-file <file>` | Load an offline bundle file instead of syncing |
| `--allow-unsigned` | Accept unsigned/unchecked local input; only with one of the two flags above |
| `healthcheck` | Exit 0 if `/readyz` returns 200 (for distroless health checks) |
| `version` | Print sidecar and engine versions |

## 3. Server configuration

| Variable | Default | Meaning |
|---|---|---|
| `GEARBOX_HTTP_ADDR` | `:8080` | API (`/api/v1`), UI, auth |
| `GEARBOX_METRICS_ADDR` | `:9464` | `/metrics`, `/readyz`, `/livez` |
| `GEARBOX_PUBLIC_URL` | `http://localhost:8080` | External URL (OIDC redirects, filesystem blob URLs) |
| `GEARBOX_STORE` | `memory` | `memory` (dev, lost on restart) or `mongo` |
| `GEARBOX_MONGO_URI` / `GEARBOX_MONGO_DB` | – / `gearbox` | MongoDB when `GEARBOX_STORE=mongo` |
| `GEARBOX_REDIS_URL` | – | Feed fan-out between replicas (required with more than one replica) |
| `GEARBOX_OBJECT_STORE` | `file://.data/objects` | `s3://bucket/prefix` or `file:///path` |
| `GEARBOX_S3_ENDPOINT` | AWS | S3-compatible endpoint (e.g. `http://minio:9000`) |
| `GEARBOX_S3_REGION` | `us-east-1` | Region |
| `GEARBOX_S3_FORCE_PATH_STYLE` | `false` | `true` for MinIO |
| `GEARBOX_S3_PRESIGN_ENDPOINT` | = `GEARBOX_S3_ENDPOINT` | Endpoint written into presigned URLs (when sidecars reach S3 at another address) |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | AWS default chain | S3 credentials |
| `GEARBOX_PRESIGN_TTL` | `15m` | Lifetime of download URLs |
| `GEARBOX_SIGNING_KEY_FILE` | – | Ed25519 private key (required to publish) |
| `GEARBOX_SIGNING_KEY_ID` | – | `key_id` written into envelopes |
| `GEARBOX_HMAC_KEY_FILE` | – | Installation HMAC key (PII segment members, logs) |
| `GEARBOX_AUTH_MODE` | `oidc` | `oidc`, or `dev` (local only: every browser session is the seeded admin) |
| `GEARBOX_OIDC_ISSUER` / `GEARBOX_OIDC_CLIENT_ID` / `GEARBOX_OIDC_CLIENT_SECRET_FILE` | – | OIDC provider |
| `GEARBOX_SESSION_KEY_FILE` | random per start | Cookie signing key (set it with more than one replica) |
| `GEARBOX_ADMIN_EMAILS` | – | Comma-separated bootstrap admins |
| `GEARBOX_UI_DIR` | – | Serve the built UI from this directory (`/ui` in the image) |
| `GEARBOX_LOG_RETENTION` | `336h` | Retention for logs uploaded by sidecars |
| `GEARBOX_SEED` / `--seed` | `false` | Dev: create `lead_service`, import `GEARBOX_SEED_DIR`, publish to `dev` |
| `GEARBOX_SEED_DIR` | `examples/definitions` | Definitions to seed |
| `GEARBOX_SEED_CREDENTIAL` | – | Dev: register this secret as the `lead_service`/`dev` credential |
| `GEARBOX_OTLP_ENDPOINT` | – | OpenTelemetry collector |
| `GEARBOX_LOG_LEVEL` | `info` | Log level |

`gearboxctl` reads `GEARBOX_SERVER_URL` (default `http://localhost:8080`), `GEARBOX_TOKEN` (or the token saved by `login`) and `GEARBOX_APP`; the global flags `--server`, `--token` and `--app` override them.

## 4. Condition operators

`field` is a dotted path into the node input. A missing or `null` field makes every operator below false except `NOT_EXISTS`.

| Operator | True when | `value` |
|---|---|---|
| `EQ`, `NEQ` | equal / not equal | scalar |
| `EQ_CASE_INSENSITIVE`, `NEQ_CASE_INSENSITIVE` | string equality ignoring case | string |
| `GT`, `GTE`, `LT`, `LTE` | numeric comparison (numeric strings parsed) | number |
| `BETWEEN` | `value ≤ x ≤ valueTo` | number + `valueTo` |
| `IN`, `NOT_IN` | x in list | list |
| `IN_CASE_INSENSITIVE`, `NOT_IN_CASE_INSENSITIVE` | x in list ignoring case | list of strings |
| `IN_NUMERIC`, `NOT_IN_NUMERIC` | numeric membership | list of numbers |
| `CONTAINS`, `NOT_CONTAINS` | substring, or element of a list field | scalar |
| `CONTAINS_CASE_INSENSITIVE`, `NOT_CONTAINS_CASE_INSENSITIVE` | same, ignoring case | string |
| `CONTAINS_NUMERIC`, `NOT_CONTAINS_NUMERIC` | numeric element of a list field | number |
| `CONTAINS_ANY`, `CONTAINS_NONE` | list field shares any / no element with `value` | list |
| `STARTS_WITH`, `ENDS_WITH` | prefix / suffix | string |
| `STARTS_WITH_CASE_INSENSITIVE`, `ENDS_WITH_CASE_INSENSITIVE` | same, ignoring case | string |
| `MATCHES`, `NOT_MATCHES` | RE2 regex | string |
| `MATCHES_CASE_INSENSITIVE`, `NOT_MATCHES_CASE_INSENSITIVE` | RE2 regex ignoring case | string |
| `EXISTS`, `NOT_EXISTS` | present and non-null / absent or null | – |
| `IS_TRUE`, `IS_FALSE` | boolean, or `"true"`/`"false"` | – |

Other leaves: `{segment, op: IN_SEGMENT | NOT_IN_SEGMENT, key?, attributes?}`, `{gate, op: GATE_PASSES | GATE_FAILS}`, `{experiment, variants: [..]}`, `{expr}`. Groups: `{all: [..]}`, `{any: [..]}`, `{not: {..}}`. `GET /meta/expr` returns this list with `valueKind`.

## 5. Expr cheat sheet

Language: [Expr](https://expr-lang.org). `GET /meta/expr` is the authoritative catalogue for a server version.

| Category | Syntax |
|---|---|
| Literals | `42`, `3.14`, `"text"`, `true`, `nil`, `[1, 2]`, `{"a": 1}` |
| Arithmetic | `+ - * / % ^` |
| Comparison / logic | `== != < <= > >=` · `and && or \|\| not !` |
| Conditional | `a ? b : c` · `x ?? "default"` · `if c { a } else { b }` |
| Access | `input.car.year`, `input["car"]["year"]`, `list[0]`, `list[-1]`, `list[1:3]` |
| Membership | `x in [..]`, `"key" in map` |
| Strings | `+`, `contains`, `startsWith`, `endsWith`, `matches` |
| Pipe / variables | `input.lead.city \| lower()` · `let age = ctx.now.Year() - input.car.year; age < 8` |
| Predicates | `filter(input.inspections, .score < 6)`, `all(list, # > 0)` (`#` element, `.f` = `#.f`) |

**Variables:** `input` (node input, validated), `request` (original request), `nodes.<id>` (outputs of nodes that ran), `out` (transform only), `ctx.now` (time), `ctx.unit`, `ctx.requestId`, `ctx.env`.

**Built-ins:** `len type abs ceil floor round int float string max min mean median sum count trim trimPrefix trimSuffix upper lower split splitAfter replace repeat join indexOf lastIndexOf hasPrefix hasSuffix all any one none map filter find findIndex findLast findLastIndex groupBy sortBy sort reduce first last take reverse uniq concat flatten keys values toPairs fromPairs get toJSON fromJSON toBase64 fromBase64 duration date timezone`.

**Changed for determinism:** `now` removed (use `ctx.now`); `keys`, `values`, `toPairs` sorted by key; `toJSON` sorts keys; `date(s)` is UTC unless a zone is given.

**Gearbox functions:**

| Function | Returns | Example |
|---|---|---|
| `lookup(name, key[, default])` | lookup value | `lookup("city_zone", input.lead.city, {"zone": "other"})` |
| `inSegment(name, key)` | bool (static or rollout segment) | `inSegment("blocked_cities", input.city)` |
| `gate(name)` | bool for `ctx.unit` | `gate("dealer_auction_v2")` |
| `variant(experiment)` | variant name for `ctx.unit` (records exposure) | `variant("callback_script") == "treatment"` |
| `bucket(salt)` | int 0–9999 for `ctx.unit`; `nil` without a unit | `bucket("fast-track-2026-10") < 2000` |
| `daysBetween(a, b)` | int | `daysBetween(date(input.lead.created_at), ctx.now) > 30` |
| `coalesce(a, b, …)` | first non-nil | `coalesce(input.lead.alt_phone, input.lead.phone)` |

**Gotchas:** inside predicates `{…}` is a closure — wrap object literals in parentheses: `map(input.inspections, ({"part": .part, "ok": .score >= 6}))`. JSON numbers are floats; use `int()` when needed. `contains` on a string is substring; for lists use `x in list`. Dynamic segments with attributes have no function form; use a segment condition leaf.

**Examples**

```
ctx.now.Year() - input.car.year <= 9 && input.car.km < 60000 && input.lead.city in ["Gurgaon", "Delhi"]
map(filter(input.inspections ?? [], .score < 6), .part)
lookup("price_band", string(int(input.car.price / 100000)), "unbanded")
```

## 6. Transform and table syntax

**Transform node**

| Field | Kind | Meaning |
|---|---|---|
| `mode` | `build` (default) \| `patch` | Start from `{}` or a copy of the input |
| `fields[]` | `{target, expr}` code | Assigned in order; `target` dotted path, `items[0].x`, `items[].x` (append); later fields read `out` |
| `expression` | code | Alternative: one expression builds the whole output |
| `defaults[]` | `{target, value}` data | Fill paths still missing |
| `drop[]` | paths | Removed last (patch mode) |
| `omitNil` | bool, default `true` | Leave out nil results |
| `outputSchema` | SchemaRef, optional | Check the output; must be accepted by the next node's `inputSchema` |

Mapping from other transform tools: move/rename → `{target: a.b, expr: input.x.y}`; default → `defaults`; remove → `drop` in patch mode; string/number functions → Expr built-ins; wildcard array copy → `map(input.items, .id)`; sort → `sort()`/`sortBy()`; cardinality → `first(list)` or `[value]`.

**Table node**

| Field | Meaning |
|---|---|
| `inputSchema`, `outputSchema` | Required SchemaRefs |
| `hitPolicy` | `first` (default) · `unique` (overlap is a validation error) · `collect` (all matches) · `priority` (highest row `priority`) |
| `inputs[]` | `{name, expr}`; evaluated once per call |
| `outputs[]` | Output property names (must exist in `outputSchema`) |
| `rows[]` | `{id, priority?, when: {<input>: <cell>}, then: {<output>: data}}`; max 5,000 |
| `default` | Data when no row matches |

| Cell | Means |
|---|---|
| `-` or absent | any |
| `< 300000`, `<= 5`, `> 3`, `>= 7`, `= 4`, `!= 4` | comparison |
| `[300000..800000]` | inclusive range |
| `"Gurgaon"` | equality |
| `["Delhi","Gurgaon"]` | one of |
| anything else | Expr using `value`, e.g. `value % 2 == 0` |

## 7. gRPC API

Service `gearbox.v1.EngineService` plus `grpc.health.v1.Health` on `unix:///var/run/gearbox/engine.sock` (optional loopback TCP with a token).

| RPC | Use | Typical latency |
|---|---|---|
| `Decide` | One decision (rule set, table, transform or graph) | ≈ 145 µs full call; 16–40 µs inside the sidecar |
| `DecideBatch` | Up to 100 independent decisions in one round trip | one hop + sum of evaluations |
| `CheckGate` | Gate value for a unit | ≈ one hop |
| `GetVariant` | Experiment assignment (layers, holdouts) | ≈ one hop |
| `GetStatus` | Readiness, generation, sync state, decisions | – |

**DecideRequest**

| Field | Type | Notes |
|---|---|---|
| `decision` | string | e.g. `lead_routing` (1–128 chars) |
| `input_json` \| `input_struct` | bytes \| Struct | ≤ `GEARBOX_MAX_INPUT_BYTES` |
| `request_id` | string | generated when empty; echoed |
| `unit_id` | string | empty = outside every partial rollout (previous version; rollout rules never match) and every experiment (`no_unit`) |
| `options.trace` | bool | per-node `micros` in `path` |
| `options.pin_version` | string | evaluate a version present in the active bundle (replay) |
| `options.force_log` | bool | always write a decision-log record |
| `options.now` | Timestamp | value of `ctx.now` (replay) |

**DecideResponse**: `output_json`, `decision_version` (`lead_routing@17`), `bundle_generation`, `path[]` (`node_id`, `node_type`, `outcome`, `micros` when traced), `fallback_used`, `eval_micros`, `request_id`, `evaluated_at`, `dependency_hash`.

**DecideBatch**: `items[]` of `DecideRequest` → `items[]` of `{response | error{code, reason, message}}`, same order; one failing item never fails the batch.

**CheckGate** `{gate, unit_id, attributes_json, request_id}` → `{value, gate_version, rule_id (rule, default, killed, disabled, no_unit), bundle_generation}`.

**GetVariant** `{experiment, unit_id, attributes_json, request_id}` → `{in_experiment, variant, parameters_json, reason (assigned, holdout, not_targeted, layer_conflict, stopped, no_unit), experiment_version, bundle_generation}`.

**GetStatus** → `{ready, app, environment, bundle_generation, last_sync_time, last_swap_time, last_sync_error, sidecar_version, decisions[{name, version, kind}], engine_version}`.

grpcurl inside the pod (bytes are base64 in proto JSON):

```bash
grpcurl -plaintext -unix -import-path proto -proto gearbox/v1/engine.proto \
  -d '{"decision":"lead_routing","unit_id":"L-1","input_json":"'"$(base64 -w0 lead.json)"'"}' \
  /var/run/gearbox/engine.sock gearbox.v1.EngineService/Decide
```

**Client rules:** one channel per process; a deadline on every call; no retries against the local sidecar — on any error return the fallback and count it; propagate `request_id` and `traceparent`; log `decision_version`.

**Versioning:** `gearbox.v1` only gains fields and methods; `buf breaking` (FILE); breaking changes go to `gearbox.v2`.

## 8. Error codes

### 8.1 gRPC (sidecar)

Status details carry `google.rpc.ErrorInfo{reason, domain: "gearbox.dev", metadata}`.

| Code | `reason` | When | Client should |
|---|---|---|---|
| `NOT_FOUND` | `DECISION_NOT_FOUND` | Name not in the active bundle (also unknown gate/experiment) | Fallback; fix name or publish |
| `INVALID_ARGUMENT` | `INPUT_SCHEMA` | Input fails the entry schema (field errors in metadata) | Fix the caller |
| `INVALID_ARGUMENT` | `INPUT_TOO_LARGE` | Input over the byte limit or an array over `maxItems`; batch > 100 | Send less |
| `FAILED_PRECONDITION` | `NOT_READY` | No verified bundle yet | Fallback |
| `INTERNAL` | `NODE_ERROR` | Node failed and the decision has no fallback (`metadata.node`) | Fallback; fix the decision |
| `DEADLINE_EXCEEDED` | – | Client deadline passed | Fallback |
| `UNAVAILABLE` | – | Sidecar not running | Fallback |
| `UNAUTHENTICATED` | – | TCP listener without the token | Fix config |

### 8.2 HTTP problem codes (control plane)

`application/problem+json` with `code`; validation adds `errors[]{path, code, message}`. Branch on `code`.

| `code` | Status | Meaning |
|---|---|---|
| `INVALID_ARGUMENT` | 400 | Malformed request |
| `UNAUTHENTICATED` | 401 | No or invalid session/token/credential |
| `FORBIDDEN` | 403 | Role or app scope does not allow it |
| `SELF_APPROVAL` | 403 | Environment forbids approving your own change request |
| `NOT_FOUND` | 404 | No such resource |
| `REVISION_CONFLICT` | 409 | Draft changed since you loaded it |
| `NAME_TAKEN` | 409 | Name exists |
| `IN_USE` | 409 | Archive blocked: live or referenced |
| `SCHEMA_REQUIRED` | 422 | Rules/table node without `inputSchema`/`outputSchema` |
| `SCHEMA_NOT_FOUND` | 422 | Referenced schema version not registered |
| `VALIDATION_FAILED` | 422 | Spec invalid (see `errors[]`) |
| `TESTS_FAILED` | 422 | A test case failed (cut version, change request, data release, publish) |
| `NOT_APPROVED` | 422 | Change request not approved |
| `BUNDLE_TOO_LARGE` | 422 | Bundle over the app's limit |

### 8.3 Validation problem codes (`POST …/validate`, `ValidationProblem.code`)

`SCHEMA_REQUIRED`, `SCHEMA_NOT_FOUND`, `TYPE_MISMATCH`, `UNKNOWN_FIELD`, `UNKNOWN_FUNCTION`, `PARSE_ERROR`, `CYCLE`, `UNREACHABLE_NODE`, `NODE_MAY_NOT_HAVE_RUN`, `SCHEMA_EDGE_MISMATCH`, `COST_TOO_HIGH`, `PII_IN_PROMPT`, `SEGMENT_NOT_FOUND`. Each has `severity` (`error` | `warning`) and a JSON-pointer `path`.

### 8.4 Sync failure reasons (`gearbox_sync_failures_total{reason}`, update log `error`)

`signature_invalid`, `unknown_key`, `generation_not_newer`, `engine_too_old`, `blob_hash_mismatch`, `too_large`, `compile_failed`, `tests_failed`, `download_failed`, `head_failed`, `unauthorized`.

## 9. Metrics

**Sidecar** (`:9464/metrics`)

| Metric | Type | Labels |
|---|---|---|
| `gearbox_decisions_total` | counter | `decision`, `version`, `status`, `fallback` |
| `gearbox_decision_duration_seconds` | histogram | `decision`, `version` |
| `gearbox_node_errors_total` | counter | `decision`, `node` |
| `gearbox_gate_checks_total` | counter | `gate`, `value` |
| `gearbox_variant_assignments_total` | counter | `experiment`, `reason` |
| `gearbox_bundle_generation` | gauge | – |
| `gearbox_sync_lag_seconds` | gauge | – (time since last successful head check) |
| `gearbox_sync_failures_total` | counter | `reason` |
| `gearbox_swap_duration_seconds` | histogram | – |
| `gearbox_ai_calls_total` | counter | `decision`, `node`, `mode`, `result` |
| `gearbox_ai_duration_seconds` | histogram | `decision`, `node` |
| `gearbox_log_dropped_total` | counter | `log` (`decision`, `update`, `exposure`) |
| `gearbox_decision_breaker_open_total` | counter | `decision` |
| `gearbox_inflight_requests` | gauge | – |

**Clients:** `gearbox_client_duration_seconds` (histogram, `decision`, `outcome`), `gearbox_client_fallbacks_total` (`decision`, `reason`). The 1 ms target is measured on the client histogram.

**Server:** `gearbox_http_requests_total` (`route`, `code`), `gearbox_http_duration_seconds` (`route`), `gearbox_releases_total` (`app`, `env`, `type`), `gearbox_publish_duration_seconds`, `gearbox_feed_connections` (gauge), `gearbox_sidecars` (gauge, `app`, `env`, `status`), `gearbox_webhook_failures_total`.

## 10. Definition files and gearboxctl

Definition files (`--definitions-dir`, `gearboxctl push|pull`) are YAML or JSON, one resource per file, in a directory per kind. Each file is the engine's `Resource` shape plus `description`:

```
examples/definitions/
├── schemas/lead_input.yaml         kind: schemas
├── segments/blocked_cities.yaml    kind: segments (static members inline as segmentMembers)
├── lookups/…                       kind: lookups (rows inline as lookupRows)
└── decisions/lead_routing.yaml     kind: decisions
```

```yaml
kind: schemas            # schemas | decisions | segments | lookups | gates | experiments | layers
name: lead_input
version: 1               # version this file represents; references use it (lead_input@1)
description: A used-car lead
spec: { … }              # exactly the OpenAPI spec for the kind
segmentMembers: [ … ]    # static segments only
lookupRows: { key: value }   # lookups only
```

`gearboxctl push` creates or updates the draft of each resource (`spec`, `description`, members, rows) in dependency order; `version` is used by offline loading and ignored by push (versions are cut on the server).

| Command | Notes |
|---|---|
| `gearboxctl login [--server URL]` | Opens the browser, creates a PAT, stores it in `~/.config/gearbox/token` |
| `gearboxctl push <dir> [--app A]` | Idempotent; prints a diff per resource |
| `gearboxctl pull <dir> [--version live\|draft] [--env E]` | Writes files in the layout above |
| `gearboxctl validate <dir>` / `gearboxctl test <dir> [--remote]` | Local engine by default |
| `gearboxctl version cut <kind>/<name> -m MSG` | Cuts from the current draft revision |
| `gearboxctl change open --env E --title T <kind>/<name>@<v>[:rollout=5] …` | Opens a change request |
| `gearboxctl change approve <id>` | Approver |
| `gearboxctl release <type> …` | `change-request <id>`, `data <kind>/<name>@<v>…`, `rollout <decision> <pct>`, `rollback <gen> --reason`, `kill <gate> [--undo] --reason` |
| `gearboxctl bundle export --env E -o FILE` | Offline bundle (`bundle.WriteFile` format) |
| `gearboxctl decide <decision> --env E -i input.json [--unit U]` | Remote Decision API |
| `gearboxctl keys generate --out DIR [--id ID] [--if-missing]` | Writes `signing.key` and `trusted-keys.txt` (`ID:base64`) and prints `trusted key: ID:base64`; `--if-missing` keeps existing files |
