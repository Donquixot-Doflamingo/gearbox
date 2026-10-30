# Gearbox Java client (thin wrapper)

> **Needs stub generation.** The gRPC stubs are generated from `proto/gearbox/v1/engine.proto` at build time by `protobuf-maven-plugin` (protoc + `protoc-gen-grpc-java`). Build on a machine with Maven Central access.

Adds to the generated stub: Unix-socket transport (Netty native epoll on Linux), a deadline per call (default 5 ms), a fallback value when the sidecar cannot answer (`UNAVAILABLE`, `DEADLINE_EXCEEDED`, `NOT_READY`, `DECISION_NOT_FOUND`, `NODE_ERROR`), and a request ID. Caller errors such as `INPUT_SCHEMA` are thrown as `StatusRuntimeException`.

## Build

```bash
cd clients/java
mvn -q package          # generates org.gearbox.v1.* stubs into target/generated-sources, compiles the wrapper
```

Requirements: JDK 17+, Maven 3.9+. Without Maven, generate stubs by hand:

```bash
protoc -I ../../proto --java_out=src/main/java \
  --plugin=protoc-gen-grpc-java=/path/to/protoc-gen-grpc-java --grpc-java_out=src/main/java \
  ../../proto/gearbox/v1/engine.proto
```

grpc-java reaches a Unix socket only through Netty's native transport; this module depends on `netty-transport-native-epoll` (x86_64 and aarch64). On macOS for local development, use the sidecar's loopback TCP listener (`GEARBOX_TCP_ADDR=127.0.0.1:7443`, target `127.0.0.1:7443`).

## Use

```java
try (GearboxClient gearbox = GearboxClient.connect(System.getenv("GEARBOX_TARGET"))) {
  Decision d = gearbox.decide("lead_routing", leadJson,
      DecideOptions.unit(leadId)
          .timeout(Duration.ofMillis(5))
          .fallback("{\"route\":\"manual_review\"}"));
  log.info("route={} version={} hash={} clientFallback={}",
      d.outputJson(), d.version(), d.dependencyHash(), d.fromClientFallback());
}
```

Create one `GearboxClient` per process. Never retry against the local sidecar; log `version` and `dependencyHash` with your business event.
