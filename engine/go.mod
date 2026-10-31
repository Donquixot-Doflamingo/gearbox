module github.com/ORG/gearbox/engine

go 1.24

require (
	github.com/ORG/gearbox/gen/go v0.0.0-00010101000000-000000000000
	github.com/expr-lang/expr v1.17.8
	github.com/klauspost/compress v1.18.0
	google.golang.org/protobuf v1.36.10
)

require (
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/text v0.17.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142 // indirect
	google.golang.org/grpc v1.67.1 // indirect
)

// Local modules of this monorepo (the github.com/ORG path is a placeholder).
replace (
	github.com/ORG/gearbox/clients/go => ../clients/go
	github.com/ORG/gearbox/controlplane => ../controlplane
	github.com/ORG/gearbox/gen/go => ../gen/go
	github.com/ORG/gearbox/sidecar => ../sidecar
)
