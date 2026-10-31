module github.com/ORG/gearbox/gen/go

go 1.24

require (
	github.com/bufbuild/protocompile v0.14.1
	google.golang.org/grpc v1.67.1
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.5.1
	google.golang.org/protobuf v1.36.10
)

require (
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/text v0.17.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142 // indirect
)

// Local modules of this monorepo (the github.com/ORG path is a placeholder).
replace (
	github.com/ORG/gearbox/clients/go => ../../clients/go
	github.com/ORG/gearbox/controlplane => ../../controlplane
	github.com/ORG/gearbox/engine => ../../engine
	github.com/ORG/gearbox/sidecar => ../../sidecar
)
