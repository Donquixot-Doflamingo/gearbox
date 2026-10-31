# gen/go — generated Go code for the Gearbox protos

Module `github.com/ORG/gearbox/gen/go`, package `gearboxv1`
(`github.com/ORG/gearbox/gen/go/gearbox/v1`), generated from
`proto/gearbox/v1/engine.proto` and `proto/gearbox/v1/bundle.proto`.

| File | From |
|---|---|
| `gearbox/v1/engine.pb.go` | messages of `engine.proto` (protoc-gen-go v1.36.10) |
| `gearbox/v1/engine_grpc.pb.go` | `EngineService` client and server (protoc-gen-go-grpc v1.5.1) |
| `gearbox/v1/bundle.pb.go` | bundle messages (`SignedManifest`, `Manifest`, `ResourceBlob`, …) |

The generated code is committed so that `go get github.com/ORG/gearbox/gen/go`
works without any code generator.

## Regenerating

Standard path (any machine with `buf`):

```bash
buf generate proto --template gen/go/buf.gen.yaml
```

`buf.yaml` and `buf.gen.yaml` live in this directory because `proto/` is owned
by the contracts lead. `buf lint proto` (STANDARD) and
`buf breaking proto --against '.git#branch=main,subdir=proto'` (FILE) are the CI
checks.

Without `buf`/`protoc` (for example in a network-restricted sandbox):

```bash
# in a sandbox first: source scripts/dev/restricted-env.sh
go run ./gen/go/internal/protogen -proto proto -out gen/go -moddir gen/go
```

`internal/protogen` parses the protos with `github.com/bufbuild/protocompile`,
builds a `CodeGeneratorRequest` like protoc does, and runs the real
`protoc-gen-go` and `protoc-gen-go-grpc` plugins, built from this module's
graph so their versions are pinned by `go.mod`. The header of the generated
files says `protoc v5.29.0`; that is the compiler version the tool reports to
the plugins (no protoc is involved).

## Decisions

- The codegen tool lives inside this module (`internal/protogen`) rather than
  in a separate `tools/` module so it shares the pinned plugin versions and the
  workspace; its extra requirements (`protocompile`, `protoc-gen-go-grpc`) are
  never compiled into importers.
- `go.sum` entries for vanity import paths (`google.golang.org/...`) cannot be
  produced inside the restricted sandbox (it fetches GitHub mirrors, whose
  hashes differ). On a normal machine run `go mod tidy` once in this module.
