module github.com/ORG/gearbox/controlplane

go 1.24

require (
	github.com/ORG/gearbox/engine v0.0.0-00010101000000-000000000000
	github.com/coreos/go-oidc/v3 v3.11.0
	github.com/getkin/kin-openapi v0.128.0
	github.com/klauspost/compress v1.18.0
	github.com/minio/minio-go/v7 v7.0.80
	github.com/redis/go-redis/v9 v9.7.0
	go.mongodb.org/mongo-driver/v2 v2.0.0
	golang.org/x/oauth2 v0.22.0
	google.golang.org/protobuf v1.36.10
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/ORG/gearbox/gen/go v0.0.0-00010101000000-000000000000 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/expr-lang/expr v1.17.8 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-jose/go-jose/v4 v4.0.2 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/goccy/go-json v0.10.3 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/yaml v0.3.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.8 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.1.2 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.29.0 // indirect
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/sync v0.9.0 // indirect
	golang.org/x/sys v0.27.0 // indirect
	golang.org/x/text v0.20.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142 // indirect
	google.golang.org/grpc v1.67.1 // indirect
)

// Local modules of this monorepo (the github.com/ORG path is a placeholder).
replace (
	github.com/ORG/gearbox/clients/go => ../clients/go
	github.com/ORG/gearbox/engine => ../engine
	github.com/ORG/gearbox/gen/go => ../gen/go
	github.com/ORG/gearbox/sidecar => ../sidecar
)
