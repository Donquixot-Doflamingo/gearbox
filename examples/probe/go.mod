module github.com/ORG/gearbox/examples/probe

go 1.24

require github.com/ORG/gearbox/clients/go v0.0.0

// Local modules of this monorepo (the github.com/ORG path is a placeholder).
replace (
	github.com/ORG/gearbox/clients/go => ../../clients/go
	github.com/ORG/gearbox/gen/go => ../../gen/go
	github.com/ORG/gearbox/engine => ../../engine
	github.com/ORG/gearbox/sidecar => ../../sidecar
)
