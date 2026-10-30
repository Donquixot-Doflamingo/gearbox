#!/usr/bin/env bash
# Source this ONLY in a network-restricted sandbox where proxy.golang.org and
# vanity import hosts are blocked but github.com is reachable. It generates
# .gowork-mirrors/go.work (gitignored) that maps vanity module paths to their
# GitHub mirrors, and points GOWORK at it. On a normal machine do not use it:
# plain `go build ./...` with the default proxy works.
#
# Add a mirror: append "vanity/path => github.com/owner/repo vX.Y.Z" to MIRRORS.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MIRRORS=(
  "go.mongodb.org/mongo-driver/v2 => github.com/mongodb/mongo-go-driver/v2 v2.0.0"
  "google.golang.org/grpc => github.com/grpc/grpc-go v1.67.1"
  "google.golang.org/protobuf => github.com/protocolbuffers/protobuf-go v1.36.10"
  "google.golang.org/genproto/googleapis/rpc => github.com/googleapis/go-genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142"
  "google.golang.org/genproto/googleapis/api => github.com/googleapis/go-genproto/googleapis/api v0.0.0-20240814211410-ddb44dafa142"
  "golang.org/x/net => github.com/golang/net v0.28.0"
  "golang.org/x/sys => github.com/golang/sys v0.24.0"
  "golang.org/x/text => github.com/golang/text v0.17.0"
  "golang.org/x/sync => github.com/golang/sync v0.8.0"
  "golang.org/x/exp => github.com/golang/exp v0.0.0-20240823005443-9b4947da3948"
  "golang.org/x/oauth2 => github.com/golang/oauth2 v0.22.0"
  "golang.org/x/crypto => github.com/golang/crypto v0.26.0"
  "golang.org/x/time => github.com/golang/time v0.6.0"
  "golang.org/x/mod => github.com/golang/mod v0.20.0"
  "golang.org/x/tools => github.com/golang/tools v0.24.0"
  "gopkg.in/yaml.v3 => github.com/go-yaml/yaml v0.0.0-20220527083530-f6f7691b1fde"
  "gopkg.in/check.v1 => github.com/go-check/check v0.0.0-20201130134442-10cb98267c6c"
  "cel.dev/expr => github.com/google/cel-spec v0.16.0"
  "cloud.google.com/go/compute/metadata => github.com/googleapis/google-cloud-go/compute/metadata v0.5.0"
)
mkdir -p "$ROOT/.gowork-mirrors"
{
  echo "go 1.24"; echo; echo "use ("
  for m in engine sidecar controlplane clients/go gen/go; do [ -f "$ROOT/$m/go.mod" ] && echo "	$ROOT/$m"; done
  echo ")"; echo; echo "replace ("
  for m in "${MIRRORS[@]}"; do echo "	$m"; done
  echo ")"
} > "$ROOT/.gowork-mirrors/go.work"
export GOWORK="$ROOT/.gowork-mirrors/go.work" GOPROXY=direct GOSUMDB=off GONOSUMDB='*' GOTOOLCHAIN=local
echo "restricted env: GOWORK=$GOWORK"
