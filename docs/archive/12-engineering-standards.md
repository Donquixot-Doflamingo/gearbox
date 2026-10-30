# 12 · Engineering standards

These apply to every pull request. CI enforces what it can; reviewers check the rest with the checklist at the end.

## 1. Go

- Go version: latest stable minus at most one minor; `toolchain` line pinned in `go.mod`. The October 2026 benchmarks ran on Go 1.24.7 and are re-run on the pinned toolchain at the start of Phase 1; gates use those numbers.
- Layout: `cmd/` for binaries, `internal/` for everything not meant for import; `engine/` imports nothing from `sidecar/` or `controlplane/` and does no I/O.
- Errors: wrap with `%w` and context (`fmt.Errorf("compile %s: %w", id, err)`); sentinel errors for conditions callers branch on; no `panic` outside `main` and impossible states.
- Context: first parameter on anything that blocks; respect cancellation; no `context.Background()` inside request paths.
- Concurrency: every goroutine has an owner and a stop path (`errgroup`); the request path never takes a lock that the sync loop holds while doing I/O; the snapshot is read through `atomic.Pointer`.
- Hot path: no reflection-based JSON on the request path beyond the single input decode; reuse `vm.VM` and buffers through `sync.Pool`; benchmarks with `-benchmem` for every engine package.
- Logging: `log/slog`, structured, no secrets or raw inputs; levels: Debug for development, Info for lifecycle, Warn for recoverable, Error for needs-attention.
- Config: `koanf` from env + file; validated at start; fail fast with a clear message.
- Lint: `golangci-lint` with `staticcheck`, `errcheck`, `gosec`, `revive`, `bodyclose`, `contextcheck`, `nilerr`, `prealloc`.
- Tests: table-driven; `-race` always; golden files under `testdata/`; fuzz targets for decoders and expression compile; testcontainers for Mongo/Redis/MinIO.

## 2. gRPC and protobuf

- `buf lint` (STANDARD) and `buf breaking` (FILE, so generated code in every language stays source-compatible) against `main`; never reuse or renumber fields; reserve removed ones. `buf.yaml` lives at `proto/`, packages in matching directories (`proto/gearbox/v1`, `proto/gearbox/admin/v1`).
- Package `gearbox.v1`; a breaking change means `gearbox.v2` side by side.
- Requests are validated with `protovalidate` (`buf.validate`) annotations; handlers do not repeat those checks.
- Errors: canonical status codes with `google.rpc.ErrorInfo` (domain `gearbox.dev`) — clients branch on `reason`, never on message text.
- Every RPC has a server-side timeout; streams send keepalives; servers set `MaxRecvMsgSize`.
- Health: `grpc.health.v1` per service; readiness only after the first snapshot is loaded.

## 3. Sidecar

- Never block a decision on the network (except AI `inline` nodes, bounded by their timeout).
- Validate the whole bundle (signature, compile, tests) before swapping; a bad bundle never replaces a good one.
- Write last-known-good to disk atomically (temp file + rename + fsync).
- Expose `/livez` (process is alive) separately from `/readyz` (snapshot loaded).
- Resource defaults documented in Helm values; no CPU limit by default (throttling hurts tail latency); memory limit set.
- Image: distroless static, non-root, read-only root filesystem, multi-arch, signed with cosign, SBOM attached.

## 4. Svelte UI

- Svelte 5 runes only (no legacy `$:` or stores for new code); TypeScript strict; `svelte-check` clean.
- Components small and typed (`$props()` with an interface); no business logic in `+page.svelte` beyond wiring.
- Data loading in `+page.ts` `load` with the generated client; errors through `+error.svelte`.
- Accessibility: semantic HTML first, labels, focus management for dialogs, colour contrast AA, axe in Playwright.
- No secrets in the bundle; auth is a cookie set by the server.

## 5. Security

- OIDC for people, scoped credentials for sidecars, PATs for CI; all stored hashed.
- Bundles signed (Ed25519, envelope over the exact manifest bytes, `key_id`, rotation); sidecars never accept unsigned bundles from the control plane; `--allow-unsigned` exists only for local files.
- Expressions run in a sandbox: Expr has no I/O; we disable `now`, limit expression size, node count and input size, check worst-case cost at publish and keep Expr's memory budget on; RE2 regex only.
- PII: input fields marked `x-gearbox-pii`, and values derived from them, never leave the sidecar (logs, AI prompts); logged hashes are HMACs; PII segment members are stored as HMACs.
- Dependencies: Renovate or Dependabot; `govulncheck` and Trivy block on high/critical.
- `SECURITY.md` with a private disclosure address.

## 6. Open-source hygiene

- MIT `LICENSE` with the approved copyright line; SPDX header (`// SPDX-License-Identifier: MIT`) in every source file.
- Conventional Commits; signed-off commits (DCO); `CHANGELOG.md` generated at release.
- No company names, hosts, keys or internal links anywhere: CI runs the deny-list from [01](01-debranding-and-dependencies.md) §4 on every PR (the list itself lives in a CI secret, not in the repo).
- Releases with goreleaser: binaries, images, checksums, SBOM, provenance.

## 7. Review checklist

- [ ] Does it change the request path? Benchmarks attached (`-count=10`, `benchstat`), no new allocations without reason.
- [ ] Does it change evaluation semantics? `min_engine_version` bumped, parity suite run against the previous engine.
- [ ] Does it change the proto? `buf breaking` passes; docs in [04](04-grpc-api.md) updated.
- [ ] Does it change rule or transform semantics? Golden tests added; docs in [05](05-rules-and-expressions.md)/[06](06-transforms.md) updated.
- [ ] Errors wrapped, context respected, goroutines owned.
- [ ] Logs free of secrets and raw PII; metrics labels bounded (no user IDs as labels).
- [ ] New config documented in [03](03-sidecar-setup.md) or [10](10-control-plane.md).
- [ ] UI: keyboard and screen-reader checked; server errors shown at the field.
- [ ] No company names or internal URLs.
