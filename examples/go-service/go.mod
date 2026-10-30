module github.com/ORG/gearbox/examples/go-service

go 1.24

require github.com/ORG/gearbox/clients/go v0.0.0

replace (
	github.com/ORG/gearbox/clients/go => ../../clients/go
	github.com/ORG/gearbox/gen/go => ../../gen/go
)
