# Gearbox developer tasks. `make help` lists them.
# In a network-restricted sandbox: GEARBOX_RESTRICTED=1 make <target>
SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

MODULES := engine sidecar controlplane clients/go gen/go
BIN     := bin
VERSION ?= dev
GOENV   := $(if $(filter 1,$(GEARBOX_RESTRICTED)),source $(CURDIR)/scripts/dev/restricted-env.sh >/dev/null &&,)

.PHONY: help
help: ## Show targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

.PHONY: gen
gen: ## Generate Go code from proto (and the UI client from OpenAPI when the UI defines `npm run gen`)
	@if [ -f tools/protogen/main.go ]; then $(GOENV) go run ./tools/protogen; \
	elif command -v buf >/dev/null; then cd proto && buf generate; \
	else echo "gen: need tools/protogen or buf (https://buf.build)"; exit 1; fi
	@if [ -f ui/package.json ] && grep -q '"gen"' ui/package.json; then cd ui && npm run gen; fi

.PHONY: build
build: ## Build gearbox-server, gearboxctl and gearbox-sidecar into bin/
	@mkdir -p $(BIN)
	$(GOENV) go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN)/ \
	  ./controlplane/cmd/gearbox-server ./controlplane/cmd/gearboxctl ./sidecar/cmd/gearbox-sidecar

.PHONY: vet
vet: ## go vet every module
	@for m in $(MODULES); do echo "==> vet $$m"; (cd $$m && $(GOENV) go vet ./...); done

.PHONY: test
test: ## go test -race every module
	@for m in $(MODULES); do echo "==> test $$m"; (cd $$m && $(GOENV) go test -race -count=1 ./...); done

.PHONY: fmt-check
fmt-check: ## Fail if any Go file needs gofmt
	@out="$$(gofmt -l $(MODULES) examples 2>/dev/null)"; if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

.PHONY: ui
ui: ## Install UI dependencies and build it (ui/build)
	cd ui && (if [ -f package-lock.json ]; then npm ci; else npm install; fi) && npm run build

.PHONY: ui-check
ui-check: ## svelte-check and UI unit tests
	cd ui && npm run check && (npm run test --if-present)

.PHONY: check-names
check-names: ## No company names, internal hosts or company libraries
	./scripts/check-names.sh

.PHONY: dev
dev: ## Run server (memory store, seed) + sidecar + UI dev server locally
	GEARBOX_RESTRICTED=$(GEARBOX_RESTRICTED) ./deploy/local/dev.sh

.PHONY: sidecar-offline
sidecar-offline: ## Run a sidecar on examples/definitions without a server
	@mkdir -p .data/run
	GEARBOX_SOCKET=$(CURDIR)/.data/run/engine.sock $(GOENV) go run ./sidecar/cmd/gearbox-sidecar \
	  --definitions-dir examples/definitions --allow-unsigned --watch

.PHONY: compose-up
compose-up: ## docker compose stack (server, MinIO, MongoDB, Redis, sidecar, example)
	cd deploy/compose && docker compose up --build

.PHONY: images
images: ## Build container images
	docker build -f deploy/docker/Dockerfile.sidecar -t gearbox-sidecar:$(VERSION) --build-arg VERSION=$(VERSION) .
	docker build -f deploy/docker/Dockerfile.server  -t gearbox-server:$(VERSION)  --build-arg VERSION=$(VERSION) .
	docker build -f deploy/docker/Dockerfile.example -t gearbox-example:$(VERSION) .

.PHONY: clean
clean: ## Remove build output and local data
	rm -rf $(BIN) .data

.PHONY: e2e
e2e: ## End-to-end test: MongoDB + LocalStack S3 (Docker) → server → S3 bundle → sidecar → example service
	scripts/integration/e2e.sh
