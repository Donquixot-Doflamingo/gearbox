module github.com/ORG/gearbox/sidecar

go 1.24

require (
	github.com/ORG/gearbox/engine v0.0.0-00010101000000-000000000000
	github.com/ORG/gearbox/gen/go v0.0.0-00010101000000-000000000000
	github.com/sony/gobreaker v1.0.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142
	google.golang.org/grpc v1.67.1
	google.golang.org/protobuf v1.36.10
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/expr-lang/expr v1.17.8 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/text v0.17.0 // indirect
)

// Local modules of this monorepo (the github.com/ORG path is a placeholder).
replace (
	github.com/ORG/gearbox/clients/go => ../clients/go
	github.com/ORG/gearbox/controlplane => ../controlplane
	github.com/ORG/gearbox/engine => ../engine
	github.com/ORG/gearbox/gen/go => ../gen/go
)
