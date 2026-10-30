# 03 · Running the sidecar

Your service never runs rules itself. It asks the Gearbox sidecar next to it, over gRPC, and gets the answer back. This page shows how to add the sidecar to a service.

## 1. What you need

1. An **app** registered in the admin UI (Settings → Apps), for example `lead-service`.
2. An **app credential** for each environment (`dev`, `stage`, `prod`). It lets the sidecar download only that app's bundles.
3. The **trusted signing keys** for your installation (public keys, safe to share). Your platform team publishes them; the Helm chart reads them from a ConfigMap.
4. The sidecar image: `ghcr.io/ORG/gearbox-sidecar:<version>`.

## 2. Kubernetes (recommended)

Kubernetes 1.29+ runs the sidecar as a *native sidecar*: an init container with `restartPolicy: Always` (on by default since 1.29, stable since 1.33). Your container starts only after the sidecar's startup probe passes (a verified bundle is loaded), and the sidecar stops after your container.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: lead-service
spec:
  template:
    spec:
      securityContext:
        fsGroup: 2000                     # socket is created group-owned 2000, mode 0660
      volumes:
        - name: gearbox-socket
          emptyDir: {}
        - name: gearbox-cache
          emptyDir: {}
        - name: gearbox-credential
          secret: { secretName: lead-service-gearbox-prod }
        - name: gearbox-keys
          configMap: { name: gearbox-trusted-keys }
      initContainers:
        - name: gearbox
          image: ghcr.io/ORG/gearbox-sidecar:1.0.0
          restartPolicy: Always           # native sidecar
          env:
            - { name: GEARBOX_SERVER_URL, value: "https://gearbox.example.internal" }
            - { name: GEARBOX_APP, value: "lead-service" }
            - { name: GEARBOX_ENV, value: "prod" }
            - { name: GEARBOX_CREDENTIAL_FILE, value: "/etc/gearbox/credential" }
            - { name: GEARBOX_TRUSTED_KEYS_FILE, value: "/etc/gearbox-keys/keys.txt" }
            - name: GOMAXPROCS               # match the CPU request (rounded up)
              valueFrom: { resourceFieldRef: { resource: requests.cpu, divisor: "1" } }
          volumeMounts:
            - { name: gearbox-socket, mountPath: /var/run/gearbox }
            - { name: gearbox-cache, mountPath: /var/cache/gearbox }
            - { name: gearbox-credential, mountPath: /etc/gearbox, readOnly: true }
            - { name: gearbox-keys, mountPath: /etc/gearbox-keys, readOnly: true }
          ports:
            - { name: metrics, containerPort: 9464 }
          startupProbe:
            httpGet: { path: /readyz, port: metrics }
            periodSeconds: 1
            failureThreshold: 60
          livenessProbe:
            httpGet: { path: /livez, port: metrics }
          resources:
            requests: { cpu: 500m, memory: 128Mi }
            limits:   { memory: 512Mi }        # set from bundle size; avoid CPU limits (throttling hurts p99)
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
      containers:
        - name: app
          image: example/lead-service:2.3.1
          env:
            - { name: GEARBOX_TARGET, value: "unix:///var/run/gearbox/engine.sock" }
          volumeMounts:
            - { name: gearbox-socket, mountPath: /var/run/gearbox }
```

The app container may run as any user: it reaches the socket through the shared `fsGroup`.

With Helm, add the `gearbox-sidecar` chart as a dependency of your service chart and include its template helper in your pod spec; it renders all of the above from a few values:

```yaml
# values.yaml of your service chart
gearbox:
  enabled: true
  app: lead-service
  env: prod
  credentialSecret: lead-service-gearbox-prod
  trustedKeysConfigMap: gearbox-trusted-keys
  resources:
    requests: { cpu: 500m, memory: 128Mi }
```

Kubernetes older than 1.29: the chart adds a regular container instead, and the client library waits for `GetStatus.ready` before the app reports ready.

**Starting while the control plane is down.** New pods fetch bundles from the object store, not the API, so they start as long as the object store is reachable. For services that must start even then, bake a recent bundle into your image or mount it and set `GEARBOX_FALLBACK_BUNDLE`; the sidecar serves it until it can sync.

## 3. Docker and docker compose

```yaml
services:
  gearbox:
    image: ghcr.io/ORG/gearbox-sidecar:1.0.0
    environment:
      GEARBOX_SERVER_URL: http://gearbox-server:8080
      GEARBOX_APP: lead-service
      GEARBOX_ENV: dev
      GEARBOX_CREDENTIAL: dev-credential-value
      GEARBOX_TRUSTED_KEYS: "dev-2026:MCowBQYDK2VwAyEA…"
    volumes:
      - gearbox-socket:/var/run/gearbox
    healthcheck:
      test: ["CMD", "/gearbox-sidecar", "healthcheck"]
      interval: 2s
      retries: 30
  lead-service:
    build: .
    environment:
      GEARBOX_TARGET: unix:///var/run/gearbox/engine.sock
    volumes:
      - gearbox-socket:/var/run/gearbox
    depends_on:
      gearbox: { condition: service_healthy }
volumes:
  gearbox-socket: {}
```

The image is distroless, so the health check uses the binary's own `healthcheck` subcommand (it calls `/readyz`).

## 4. Offline mode (local development and tests)

No control plane needed. Export a bundle from the UI (Decision → Export) or with `gearboxctl bundle export`, and point the sidecar at it:

```bash
gearbox-sidecar --bundle-file ./testdata/lead-service.bundle --allow-unsigned
# or watch a directory of YAML definitions and recompile on change
gearbox-sidecar --definitions-dir ./decisions --watch --allow-unsigned
```

`--allow-unsigned` is accepted only together with `--bundle-file` or `--definitions-dir`. Use this in CI integration tests so they are hermetic.

## 5. Configuration reference

| Variable | Default | Meaning |
|---|---|---|
| `GEARBOX_SERVER_URL` | – | Control plane URL (required unless offline) |
| `GEARBOX_APP` | – | App name (required) |
| `GEARBOX_ENV` | – | Environment (required) |
| `GEARBOX_CREDENTIAL_FILE` / `GEARBOX_CREDENTIAL` | – | App credential |
| `GEARBOX_TRUSTED_KEYS_FILE` / `GEARBOX_TRUSTED_KEYS` | – | Trusted Ed25519 public keys, `key_id:base64` per line or comma-separated (required unless offline) |
| `GEARBOX_SOCKET` | `/var/run/gearbox/engine.sock` | Unix socket for `EngineService` (created with mode `0660`) |
| `GEARBOX_TCP_ADDR` | empty (off) | Optional loopback address, e.g. `127.0.0.1:7443` |
| `GEARBOX_TCP_TOKEN_FILE` | – | Required bearer token when TCP is on |
| `GEARBOX_REFLECTION` | `false` | Enable gRPC server reflection (for grpcurl) |
| `GEARBOX_CACHE_DIR` | `/var/cache/gearbox` | Last-known-good bundle (re-verified on load) |
| `GEARBOX_FALLBACK_BUNDLE` | – | Bundle file to serve if nothing newer can be fetched at start |
| `GEARBOX_POLL_INTERVAL` | `30s` | Head-pointer poll interval when the change feed is down |
| `GEARBOX_FEED` | `true` | Use the push change feed |
| `GEARBOX_MAX_BUNDLE_MB` | `64` | Refuse bigger compressed bundles (decompressed cap is 4×) |
| `GEARBOX_MAX_INPUT_BYTES` | `262144` | Refuse bigger inputs with `INVALID_ARGUMENT` |
| `GEARBOX_LOG_SAMPLE` | `0.01` | Default decision-log sample rate (decisions can override) |
| `GEARBOX_LOG_BUFFER` | `65536` | Ring buffer size in records |
| `GEARBOX_OTLP_ENDPOINT` | – | OpenTelemetry collector for logs and traces; empty logs to stdout |
| `GEARBOX_METRICS_ADDR` | `:9464` | Prometheus `/metrics`, `/readyz`, `/livez` |
| `GEARBOX_HUB_URL` | – | Integration Hub base URL (needed only for AI nodes) |
| `GEARBOX_HUB_TOKEN_FILE` | – | Integration Hub credential |
| `GEARBOX_SHUTDOWN_GRACE` | `10s` | Drain time on SIGTERM |

Unsigned bundles are never accepted from the control plane; there is no setting for it.

## 6. Calling it

Generated stubs come from [`engine.proto`](proto/gearbox/v1/engine.proto) (Go module `github.com/ORG/gearbox/gen/go`; Java and Python packages are published with each release). The thin client libraries add a deadline, a fallback value, request-ID propagation and a client-side latency histogram.

### Go

```go
client, err := gearbox.Dial(ctx, os.Getenv("GEARBOX_TARGET")) // unix:///var/run/gearbox/engine.sock
if err != nil { return err }
defer client.Close()

out, err := client.Decide(ctx, "lead_routing", lead,
    gearbox.WithUnit(lead.ID),
    gearbox.WithTimeout(5*time.Millisecond),
    gearbox.WithFallback(map[string]any{"route": "manual_review"}))
// out.Output (map), out.Version ("lead_routing@v17"), out.FallbackUsed
```

### Java

grpc-java reaches a Unix socket only through Netty's native transport (`netty-transport-native-epoll` on Linux). The Gearbox Java client bundles it; if you use raw stubs without it, turn on loopback TCP (`GEARBOX_TCP_ADDR`) instead.

```java
GearboxClient gearbox = GearboxClient.connect(System.getenv("GEARBOX_TARGET"));
Decision d = gearbox.decide("lead_routing", leadJson,
    DecideOpts.unit(lead.getId())
              .timeout(Duration.ofMillis(5))
              .fallback("{\"route\":\"manual_review\"}"));
log.info("route={} version={}", d.output().get("route"), d.version());
```

### Python (generated stub, no wrapper)

```python
channel = grpc.insecure_channel("unix:///var/run/gearbox/engine.sock")
stub = engine_pb2_grpc.EngineServiceStub(channel)
resp = stub.Decide(engine_pb2.DecideRequest(
    decision="lead_routing", input_json=json.dumps(lead).encode(), unit_id=lead["id"]),
    timeout=0.005)
```

### grpcurl (debugging inside the pod)

Pass the proto files, or start the sidecar with `GEARBOX_REFLECTION=true` in non-production environments.

```bash
grpcurl -plaintext -unix -import-path proto -proto gearbox/v1/engine.proto \
  -d '{"decision":"lead_routing","input_json":"eyJsZWFkIjp7fX0="}' \
  /var/run/gearbox/engine.sock gearbox.v1.EngineService/Decide
```

## 7. Sizing

- CPU: about 0.065 vCPU-ms per decision (≈ 15k/s per vCPU). Request 500m for up to about 2,000 decisions/s per pod and raise it with your peak rate. `GOMAXPROCS` must follow the request (the chart does this).
- Memory: ≈ 2 × decompressed bundle + 32 MB + log buffer. The UI shows each app's bundle size.
- Latency: measured full call p50 ≈ 145 µs (Unix socket, 2 vCPU sandbox). Use `DecideBatch` when one request needs several decisions.

## 8. Checklist before production

- [ ] Every `Decide` call has a deadline and a fallback value.
- [ ] Log `decision_version` and `dependency_hash` next to your own business logs.
- [ ] Alerts on `gearbox_sync_lag_seconds > 300`, `gearbox_sync_failures_total` and the fallback rate (`gearbox_decisions_total{fallback="true"}`).
- [ ] Client-side latency histogram (`gearbox_client_duration_seconds`) on your dashboard; that is what the 1 ms target is measured on.
- [ ] Sidecar resources set; no CPU limit; `GOMAXPROCS` set.
- [ ] Offline bundle used in integration tests.
